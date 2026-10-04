// SPDX-License-Identifier: Apache-2.0
//
// The device half is adapted from golang.zx2c4.com/wireguard/tun/netstack
// (MIT, Copyright (C) 2017-2025 WireGuard LLC), cut down to IPv4 and to
// listening on TCP, the only two things the agent does.

package tunnel

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"

	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

const (
	nicID = 1

	// A download is sent by this stack, end to end to a visitor who may be on
	// a mobile link: the buffers must not be what caps the window.
	tcpBufferDefault = 1 << 20
	tcpBufferMax     = 8 << 20
)

// Stack is the userspace network the tunnel carries: a WireGuard device on
// one side, TCP listeners on the other.
type Stack struct {
	ep             *channel.Endpoint
	stack          *stack.Stack
	events         chan tun.Event
	notifyHandle   *channel.NotificationHandle
	incomingPacket chan *buffer.View
	mtu            int
}

func newStack(address netip.Addr, mtu int) (*Stack, error) {
	if !address.Is4() {
		return nil, fmt.Errorf("tunnel address %s is not IPv4", address)
	}

	s := &Stack{
		ep: channel.New(1024, uint32(mtu), ""),
		stack: stack.New(stack.Options{
			NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
			TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol},
			HandleLocal:        true,
		}),
		events:         make(chan tun.Event, 10),
		incomingPacket: make(chan *buffer.View),
		mtu:            mtu,
	}

	// gVisor defaults to Reno without SACK, which collapses on the losses and
	// jitter of a mobile link: measured at 2 MB/s from 5G where the same path
	// through HAProxy, sent by Linux, reached 12.
	sack := tcpip.TCPSACKEnabled(true)
	cubic := tcpip.CongestionControlOption("cubic")
	moderate := tcpip.TCPModerateReceiveBufferOption(true)
	send := tcpip.TCPSendBufferSizeRangeOption{Min: 4096, Default: tcpBufferDefault, Max: tcpBufferMax}
	receive := tcpip.TCPReceiveBufferSizeRangeOption{Min: 4096, Default: tcpBufferDefault, Max: tcpBufferMax}

	for _, opt := range []tcpip.SettableTransportProtocolOption{&sack, &cubic, &moderate, &send, &receive} {
		if err := s.stack.SetTransportProtocolOption(tcp.ProtocolNumber, opt); err != nil {
			return nil, fmt.Errorf("set TCP option %T: %v", opt, err)
		}
	}

	s.notifyHandle = s.ep.AddNotify(s)
	if err := s.stack.CreateNIC(nicID, s.ep); err != nil {
		return nil, fmt.Errorf("create NIC: %v", err)
	}

	protoAddr := tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddrFromSlice(address.AsSlice()).WithPrefix(),
	}
	if err := s.stack.AddProtocolAddress(nicID, protoAddr, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("add address %s: %v", address, err)
	}

	// The only route: whatever the destination, the answer goes back through the tunnel
	s.stack.AddRoute(tcpip.Route{Destination: header.IPv4EmptySubnet, NIC: nicID})

	s.events <- tun.EventUp
	return s, nil
}

// ListenTCP opens a listener inside the tunnel, invisible from the host.
func (s *Stack) ListenTCP(addr netip.AddrPort) (net.Listener, error) {
	return gonet.ListenTCP(s.stack, tcpip.FullAddress{
		NIC:  nicID,
		Addr: tcpip.AddrFromSlice(addr.Addr().AsSlice()),
		Port: addr.Port(),
	}, ipv4.ProtocolNumber)
}

// The tun.Device half, what wireguard-go reads from and writes to.

func (s *Stack) Name() (string, error) { return "portlatch", nil }

func (s *Stack) File() *os.File { return nil }

func (s *Stack) Events() <-chan tun.Event { return s.events }

func (s *Stack) MTU() (int, error) { return s.mtu, nil }

func (s *Stack) BatchSize() int { return 1 }

func (s *Stack) Read(buf [][]byte, sizes []int, offset int) (int, error) {
	view, ok := <-s.incomingPacket
	if !ok {
		return 0, os.ErrClosed
	}
	n, err := view.Read(buf[0][offset:])
	if err != nil {
		return 0, err
	}
	sizes[0] = n
	return 1, nil
}

func (s *Stack) Write(buf [][]byte, offset int) (int, error) {
	for _, b := range buf {
		packet := b[offset:]
		if len(packet) == 0 {
			continue
		}
		if packet[0]>>4 != 4 {
			return 0, syscall.EAFNOSUPPORT
		}
		pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(packet)})
		s.ep.InjectInbound(header.IPv4ProtocolNumber, pkb)
	}
	return len(buf), nil
}

// WriteNotify hands each packet the stack sends to the WireGuard device.
func (s *Stack) WriteNotify() {
	pkt := s.ep.Read()
	if pkt == nil {
		return
	}
	view := pkt.ToView()
	pkt.DecRef()
	s.incomingPacket <- view
}

func (s *Stack) Close() error {
	s.stack.RemoveNIC(nicID)
	s.stack.Close()
	s.ep.RemoveNotify(s.notifyHandle)
	s.ep.Close()
	close(s.events)
	close(s.incomingPacket)
	return nil
}
