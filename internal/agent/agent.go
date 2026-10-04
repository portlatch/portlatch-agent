// SPDX-License-Identifier: Apache-2.0

// Package agent wires the pieces together: local keys, enrolment, the tunnel,
// and the single loop that beats and carries the configuration back.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/portlatch/portlatch-agent/internal/api"
	"github.com/portlatch/portlatch-agent/internal/enrol"
	"github.com/portlatch/portlatch-agent/internal/forward"
	"github.com/portlatch/portlatch-agent/internal/state"
	"github.com/portlatch/portlatch-agent/internal/tunnel"
	"github.com/portlatch/portlatch-agent/internal/wgkey"
)

const heartbeatInterval = 30 * time.Second

type agent struct {
	cfg      Config
	client   *api.Client
	store    *state.Store
	key      wgkey.PrivateKey
	tun      *tunnel.Tunnel
	tunAddr  netip.Addr
	nodeAddr netip.Addr
	applied  api.Tunnel
	forwards *forward.Manager
}

func Run(ctx context.Context, cfg Config) error {
	store, err := state.Open(cfg.DataDir)
	if err != nil {
		return err
	}

	key, err := loadOrCreateKey(store)
	if err != nil {
		return err
	}
	public, err := key.Public()
	if err != nil {
		return err
	}

	a := &agent{
		cfg:      cfg,
		client:   api.NewClient(cfg.APIBaseURL, cfg.UserAgent()),
		store:    store,
		key:      key,
		forwards: forward.New(),
	}
	defer a.tun.Close()
	defer a.forwards.Close()

	slog.Info("starting",
		"version", cfg.Version,
		"api", cfg.APIBaseURL,
		"data_dir", store.Dir(),
		"wg_public_key", public.Base64())

	token, err := ensureToken(ctx, a.client, store, public)
	if err != nil {
		return err
	}
	a.client.SetToken(token)

	// A beat rather than GET /agents/self: same configuration back, and the
	// dashboard shows the agent online now instead of a minute later.
	self, err := a.client.Heartbeat(ctx, a.cfg.Version)
	if err != nil {
		if api.IsStatus(err, http.StatusUnauthorized) {
			return a.rejected(err)
		}
		return fmt.Errorf("read configuration: %w", err)
	}
	a.reconcile(ctx, self)

	return a.beat(ctx)
}

// Enrol goes through the enrolment alone, up to the token, and returns: what
// an installer needs before it hands the agent over to a service.
func Enrol(ctx context.Context, cfg Config) error {
	store, err := state.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	key, err := loadOrCreateKey(store)
	if err != nil {
		return err
	}
	public, err := key.Public()
	if err != nil {
		return err
	}
	_, err = ensureToken(ctx, api.NewClient(cfg.APIBaseURL, cfg.UserAgent()), store, public)
	return err
}

func ensureToken(ctx context.Context, client *api.Client, store *state.Store, public wgkey.PublicKey) (string, error) {
	token, found, err := store.Token()
	if err != nil || found {
		return token, err
	}
	result, err := enrol.Run(ctx, client, store, public)
	if err != nil {
		return "", err
	}
	if err := store.SaveToken(result.Token); err != nil {
		return "", err
	}
	slog.Info("enrolled", "agent_id", result.Agent.ID, "name", result.Agent.Name)
	return result.Token, nil
}

func (a *agent) beat(ctx context.Context) error {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("stopping")
			return nil
		case <-ticker.C:
			self, err := a.client.Heartbeat(ctx, a.cfg.Version)
			if err != nil {
				if ctx.Err() != nil {
					continue
				}
				if api.IsStatus(err, http.StatusUnauthorized) {
					return a.rejected(err)
				}
				slog.Warn("heartbeat failed", "error", err)
				continue
			}
			a.reconcile(ctx, self)
		}
	}
}

// reconcile brings the tunnel up, or back up, when the configuration changes,
// then the listeners. A failure here is never fatal: the next heartbeat tries
// again.
func (a *agent) reconcile(ctx context.Context, self *api.Agent) {
	if self.Tunnel == nil {
		slog.Warn("the configuration carries no tunnel yet")
		return
	}
	if a.tun == nil || *self.Tunnel != a.applied {
		if !a.bringTunnelUp(ctx, *self.Tunnel) {
			return
		}
	}
	a.forwards.Apply(a.tun.Stack(), a.tunAddr, a.nodeAddr, self.Listeners)
}

func (a *agent) bringTunnelUp(ctx context.Context, t api.Tunnel) bool {
	cfg, err := tunnelConfig(a.key, t)
	if err != nil {
		slog.Error("unusable tunnel configuration", "error", err)
		return false
	}

	if a.tun != nil {
		slog.Info("tunnel configuration changed, bringing it back up")
		a.tun.Close()
		a.tun = nil
	}

	next, err := tunnel.Up(ctx, cfg)
	if err != nil {
		slog.Error("could not bring the tunnel up", "error", err)
		return false
	}
	a.tun = next
	a.tunAddr = cfg.Address
	a.nodeAddr = netip.Addr{}
	if node, err := tunnel.NodeAddress(t.Network); err == nil {
		a.nodeAddr = node
	} else {
		// Never seen from the control plane; the listeners still work, without checking who sends a PROXY header
		slog.Warn("unknown node address, PROXY headers will not be checked against it", "error", err)
	}
	a.applied = t
	return true
}

// ErrRejected is returned once the control plane refuses the token: the agent
// was deleted, and restarting it would change nothing.
var ErrRejected = errors.New("token rejected")

func (a *agent) rejected(err error) error {
	return fmt.Errorf("the control plane rejected our token, the agent was probably deleted — remove %s to enrol again: %w", a.store.TokenPath(), errors.Join(ErrRejected, err))
}

func tunnelConfig(key wgkey.PrivateKey, t api.Tunnel) (tunnel.Config, error) {
	address, err := netip.ParseAddr(t.Address)
	if err != nil {
		return tunnel.Config{}, fmt.Errorf("parse tunnel address %q: %w", t.Address, err)
	}
	nodeKey, err := wgkey.ParsePublicKey(t.NodePublicKey)
	if err != nil {
		return tunnel.Config{}, fmt.Errorf("parse node public key: %w", err)
	}
	if t.Endpoint == "" {
		return tunnel.Config{}, fmt.Errorf("the configuration carries no endpoint")
	}
	return tunnel.Config{
		PrivateKey:    key,
		Address:       address,
		Endpoint:      t.Endpoint,
		NodePublicKey: nodeKey,
	}, nil
}

func loadOrCreateKey(store *state.Store) (wgkey.PrivateKey, error) {
	key, found, err := store.PrivateKey()
	if err != nil {
		return wgkey.PrivateKey{}, err
	}
	if found {
		return key, nil
	}

	key, err = wgkey.GeneratePrivateKey()
	if err != nil {
		return wgkey.PrivateKey{}, err
	}
	if err := store.SavePrivateKey(key); err != nil {
		return wgkey.PrivateKey{}, err
	}
	slog.Info("generated a new WireGuard key pair")
	return key, nil
}
