// SPDX-License-Identifier: Apache-2.0

// Package forward is the data path: it listens on the ports the control plane
// hands out, inside the userspace stack, and copies each connection to its
// target on the LAN. It holds no certificate and reads nothing but an optional
// PROXY header.
package forward

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/portlatch/portlatch-agent/internal/api"
	"github.com/portlatch/portlatch-agent/internal/proxyproto"
	"github.com/portlatch/portlatch-agent/internal/tunnel"
)

const (
	proxyHeaderTimeout = 5 * time.Second
	dialTimeout        = 10 * time.Second
)

type running struct {
	cfg  api.Listener
	ln   net.Listener
	node netip.Addr

	mu      sync.Mutex
	retired bool
	conns   map[net.Conn]struct{}
}

func newRunning(cfg api.Listener, ln net.Listener, node netip.Addr) *running {
	return &running{cfg: cfg, ln: ln, node: node, conns: map[net.Conn]struct{}{}}
}

// track refuses a connection accepted while the listener was being retired.
func (r *running) track(c net.Conn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.retired {
		return false
	}
	r.conns[c] = struct{}{}
	return true
}

func (r *running) untrack(c net.Conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, c)
}

// retire closes the listener and every connection it accepted. A connection
// left open would keep the port bound in the stack, and the next listener
// handed that port could never bind it; it would also keep copying to a
// target the control plane no longer wants.
func (r *running) retire() {
	r.ln.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retired = true
	for c := range r.conns {
		c.Close()
	}
}

// Manager keeps the open listeners equal to the last list it was given. They
// are keyed by port, the one thing unique per agent: a web route comes as two
// listeners sharing an id, one for HTTPS and one for HTTP.
type Manager struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	stack   *tunnel.Stack
	running map[uint16]*running
}

func New() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{ctx: ctx, cancel: cancel, running: map[uint16]*running{}}
}

// Apply is a desired state, like the node's own files: what is gone closes,
// what is new opens, what changed reopens. A new tunnel reopens everything,
// since the listeners of the old stack died with it. node is the node's end of
// the tunnel, the only peer allowed to send a PROXY header; an invalid one
// skips that check.
func (m *Manager) Apply(st *tunnel.Stack, address, node netip.Addr, desired []api.Listener) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if st != m.stack {
		m.closeAll()
		m.stack = st
	}

	wanted := make(map[uint16]api.Listener, len(desired))
	for _, l := range desired {
		wanted[l.Port] = l
	}

	for port, r := range m.running {
		if l, ok := wanted[port]; !ok || l != r.cfg {
			r.retire()
			delete(m.running, port)
			slog.Info("listener closed", "listener", r.cfg.ID, "port", port)
		}
	}

	for _, l := range desired {
		if _, ok := m.running[l.Port]; ok {
			continue
		}
		ln, err := st.ListenTCP(netip.AddrPortFrom(address, l.Port))
		if err != nil {
			// Retried at the next beat, which hands the same list back
			slog.Error("could not listen", "listener", l.ID, "port", l.Port, "error", err)
			continue
		}
		r := newRunning(l, ln, node)
		m.running[l.Port] = r
		go m.serve(r)
		slog.Info("listening",
			"listener", l.ID,
			"port", l.Port,
			"target", target(l),
			"accept_proxy", l.AcceptProxy,
			"send_proxy", l.SendProxy)
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancel()
	m.closeAll()
}

func (m *Manager) closeAll() {
	for port, r := range m.running {
		r.retire()
		delete(m.running, port)
	}
}

func (m *Manager) serve(r *running) {
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			// Closing the listener is how a listener is retired
			return
		}
		if !r.track(conn) {
			conn.Close()
			return
		}
		go m.handle(r, conn)
	}
}

func (m *Manager) handle(r *running, conn net.Conn) {
	defer r.untrack(conn)
	defer conn.Close()

	l := r.cfg

	visitor := addrPort(conn.RemoteAddr())
	local := addrPort(conn.LocalAddr())

	if l.AcceptProxy {
		// Only HAProxy, on the node, sends a PROXY header: from anywhere else it would forge the visitor
		if !fromNode(visitor, r.node) {
			slog.Warn("connection closed, a PROXY header is only taken from the node", "listener", l.ID, "from", visitor)
			return
		}

		// HAProxy opened this connection from the node: the visitor is in the header, never in RemoteAddr
		conn.SetReadDeadline(time.Now().Add(proxyHeaderTimeout))
		header, err := proxyproto.Read(conn)
		if err != nil {
			slog.Warn("connection closed, no valid PROXY header", "listener", l.ID, "from", visitor, "error", err)
			return
		}
		conn.SetReadDeadline(time.Time{})
		if header.Source.IsValid() {
			visitor, local = header.Source, header.Destination
		}
	}

	dialer := net.Dialer{Timeout: dialTimeout}
	upstream, err := dialer.DialContext(m.ctx, "tcp", target(l))
	if err != nil {
		slog.Warn("target unreachable", "listener", l.ID, "visitor", visitor, "target", target(l), "error", err)
		return
	}
	defer upstream.Close()

	if l.SendProxy {
		if err := proxyproto.Write(upstream, proxyproto.Header{Source: visitor, Destination: local}); err != nil {
			slog.Warn("could not send the PROXY header", "listener", l.ID, "error", err)
			return
		}
	}

	slog.Info("connection", "listener", l.ID, "visitor", visitor, "target", target(l))

	pipe(conn, upstream)
}

// pipe copies both ways and closes each write half as its side finishes, so a
// client that half-closes still gets its answer.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); copyAndCloseWrite(b, a) }()
	go func() { defer wg.Done(); copyAndCloseWrite(a, b) }()
	wg.Wait()
}

func copyAndCloseWrite(dst, src net.Conn) {
	_, err := io.Copy(dst, src)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		slog.Debug("copy ended", "error", err)
	}
	if cw, ok := dst.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	} else {
		dst.Close()
	}
}

// fromNode tells whether a connection comes from the node's end of the tunnel.
// Without a known node address, nothing is refused.
func fromNode(remote netip.AddrPort, node netip.Addr) bool {
	return !node.IsValid() || remote.Addr().Unmap() == node
}

func target(l api.Listener) string {
	return net.JoinHostPort(l.TargetHost, strconv.Itoa(int(l.TargetPort)))
}

func addrPort(a net.Addr) netip.AddrPort {
	if tcp, ok := a.(*net.TCPAddr); ok {
		return tcp.AddrPort()
	}
	ap, _ := netip.ParseAddrPort(a.String())
	return ap
}
