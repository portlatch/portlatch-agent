// SPDX-License-Identifier: Apache-2.0

// Package proxyproto reads and writes the binary PROXY protocol header,
// version 2 only. HAProxy on the node prepends one to every web route
// connection, and the agent prepends one towards the LAN when asked to.
package proxyproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
)

var signature = [12]byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}

const (
	versionCommandLocal = 0x20
	versionCommandProxy = 0x21

	familyTCP4 = 0x11
	familyTCP6 = 0x21

	addressLengthTCP4 = 12
	addressLengthTCP6 = 36

	// Far above what an address block and a few TLVs need: a longer header
	// is not HAProxy talking.
	maxLength = 512
)

var ErrNotProxy = errors.New("not a PROXY protocol v2 header")

// Header is what the connection carried: who dialled, and what they dialled.
// Both are invalid for a LOCAL header, which HAProxy sends for its own checks.
type Header struct {
	Source      netip.AddrPort
	Destination netip.AddrPort
}

// Read consumes exactly the header and nothing past it, so the caller can
// copy the rest of the stream as is.
func Read(r io.Reader) (Header, error) {
	var fixed [16]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return Header{}, fmt.Errorf("read PROXY header: %w", err)
	}
	if [12]byte(fixed[:12]) != signature {
		return Header{}, ErrNotProxy
	}

	length := int(binary.BigEndian.Uint16(fixed[14:16]))
	if length > maxLength {
		return Header{}, fmt.Errorf("PROXY header of %d bytes: %w", length, ErrNotProxy)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return Header{}, fmt.Errorf("read PROXY addresses: %w", err)
	}

	switch fixed[12] {
	case versionCommandLocal:
		return Header{}, nil
	case versionCommandProxy:
	default:
		return Header{}, fmt.Errorf("PROXY version and command 0x%02x: %w", fixed[12], ErrNotProxy)
	}

	switch fixed[13] {
	case familyTCP4:
		if length < addressLengthTCP4 {
			return Header{}, fmt.Errorf("short TCP4 address block: %w", ErrNotProxy)
		}
		return Header{
			Source:      netip.AddrPortFrom(netip.AddrFrom4([4]byte(body[0:4])), binary.BigEndian.Uint16(body[8:10])),
			Destination: netip.AddrPortFrom(netip.AddrFrom4([4]byte(body[4:8])), binary.BigEndian.Uint16(body[10:12])),
		}, nil
	case familyTCP6:
		if length < addressLengthTCP6 {
			return Header{}, fmt.Errorf("short TCP6 address block: %w", ErrNotProxy)
		}
		return Header{
			Source:      netip.AddrPortFrom(netip.AddrFrom16([16]byte(body[0:16])).Unmap(), binary.BigEndian.Uint16(body[32:34])),
			Destination: netip.AddrPortFrom(netip.AddrFrom16([16]byte(body[16:32])).Unmap(), binary.BigEndian.Uint16(body[34:36])),
		}, nil
	default:
		return Header{}, fmt.Errorf("PROXY family 0x%02x: %w", fixed[13], ErrNotProxy)
	}
}

// Write prepends a PROXY header carrying the visitor's address.
func Write(w io.Writer, h Header) error {
	if !h.Source.IsValid() || !h.Destination.IsValid() {
		return errors.New("write PROXY header: both addresses are needed")
	}

	src, dst := h.Source.Addr().Unmap(), h.Destination.Addr().Unmap()

	var buf []byte
	buf = append(buf, signature[:]...)
	buf = append(buf, versionCommandProxy)

	if src.Is4() && dst.Is4() {
		buf = append(buf, familyTCP4)
		buf = binary.BigEndian.AppendUint16(buf, addressLengthTCP4)
		s4, d4 := src.As4(), dst.As4()
		buf = append(buf, s4[:]...)
		buf = append(buf, d4[:]...)
	} else {
		buf = append(buf, familyTCP6)
		buf = binary.BigEndian.AppendUint16(buf, addressLengthTCP6)
		s16, d16 := src.As16(), dst.As16()
		buf = append(buf, s16[:]...)
		buf = append(buf, d16[:]...)
	}
	buf = binary.BigEndian.AppendUint16(buf, h.Source.Port())
	buf = binary.BigEndian.AppendUint16(buf, h.Destination.Port())

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("write PROXY header: %w", err)
	}
	return nil
}
