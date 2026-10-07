// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

// A route whose target changes is retired and reopened on the same port, at
// once: the connections it had must not keep that port from being bound again.
func TestAPortReopensRightAfterItsListenerAndConnectionsClose(t *testing.T) {
	address := netip.MustParseAddr("100.64.0.2")
	st, err := newStack(address, mtu)
	if err != nil {
		t.Fatalf("newStack: %v", err)
	}
	defer st.Close()

	listenOn := netip.AddrPortFrom(address, 41001)
	ln, err := st.ListenTCP(listenOn)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()

	// The node's side, here dialled from the stack itself
	client, err := gonet.DialTCP(st.stack, tcpip.FullAddress{
		NIC:  nicID,
		Addr: tcpip.AddrFromSlice(address.AsSlice()),
		Port: listenOn.Port(),
	}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("no connection accepted")
	}

	// As forward retires a listener: the listener, then each connection it accepted
	ln.Close()
	server.Close()

	again, err := st.ListenTCP(listenOn)
	if err != nil {
		t.Fatalf("listen again on the same port: %v", err)
	}
	again.Close()
}
