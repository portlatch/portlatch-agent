// SPDX-License-Identifier: Apache-2.0

// Package tunnel brings WireGuard up entirely in userspace: no system
// interface, no NET_ADMIN, nothing visible from the host.
package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"

	"github.com/portlatch/portlatch-agent/internal/wgkey"
)

const (
	mtu                 = 1420
	persistentKeepalive = 25
	allowedIPs          = "0.0.0.0/0"
)

type Config struct {
	PrivateKey    wgkey.PrivateKey
	Address       netip.Addr
	Endpoint      string
	NodePublicKey wgkey.PublicKey
}

type Tunnel struct {
	dev   *device.Device
	stack *Stack
}

func Up(ctx context.Context, cfg Config) (*Tunnel, error) {
	endpoint, err := resolveEndpoint(ctx, cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	st, err := newStack(cfg.Address, mtu)
	if err != nil {
		return nil, fmt.Errorf("create userspace interface: %w", err)
	}

	dev := device.NewDevice(st, conn.NewDefaultBind(), newLogger())

	// allowed_ip is 0.0.0.0/0: behind a port redirection the node's DNAT keeps
	// the visitor's address as source, so the reply goes to that address, and
	// WireGuard silently drops a packet no peer covers. It only routes this
	// userspace stack, nothing on the host.
	uapi := strings.Join([]string{
		"private_key=" + cfg.PrivateKey.Hex(),
		"replace_peers=true",
		"public_key=" + cfg.NodePublicKey.Hex(),
		"endpoint=" + endpoint,
		fmt.Sprintf("persistent_keepalive_interval=%d", persistentKeepalive),
		"allowed_ip=" + allowedIPs,
		"",
	}, "\n")

	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		return nil, fmt.Errorf("configure userspace interface: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bring userspace interface up: %w", err)
	}

	slog.Info("tunnel up",
		"address", cfg.Address.String(),
		"allowed_ips", allowedIPs,
		"endpoint", endpoint)

	return &Tunnel{dev: dev, stack: st}, nil
}

// NodeAddress is the node's own end of the tunnel, the first address of the
// network: the control plane renders it so, and HAProxy dials from it.
func NodeAddress(network string) (netip.Addr, error) {
	prefix, err := netip.ParsePrefix(network)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse tunnel network %q: %w", network, err)
	}
	if !prefix.Addr().Is4() {
		return netip.Addr{}, fmt.Errorf("tunnel network %s is not IPv4", network)
	}
	return prefix.Masked().Addr().Next(), nil
}

// Stack is the userspace network the data path listens on.
func (t *Tunnel) Stack() *Stack {
	if t == nil {
		return nil
	}
	return t.stack
}

func (t *Tunnel) Close() {
	if t == nil || t.dev == nil {
		return
	}
	t.dev.Close()
}

// resolveEndpoint turns node-x.portlatch.eu:51820 into an address:port, which
// is all the wireguard-go UAPI accepts.
func resolveEndpoint(ctx context.Context, endpoint string) (string, error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse endpoint %q: %w", endpoint, err)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return net.JoinHostPort(addr.String(), port), nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return "", fmt.Errorf("resolve endpoint %q: %w", endpoint, err)
	}
	if len(addrs) == 0 {
		return "", fmt.Errorf("resolve endpoint %q: no address", endpoint)
	}
	return net.JoinHostPort(addrs[0].Unmap().String(), port), nil
}

func newLogger() *device.Logger {
	return &device.Logger{
		Verbosef: func(format string, args ...any) {
			slog.Debug(strings.TrimSpace(fmt.Sprintf(format, args...)), "component", "wireguard")
		},
		Errorf: func(format string, args ...any) {
			slog.Error(strings.TrimSpace(fmt.Sprintf(format, args...)), "component", "wireguard")
		},
	}
}
