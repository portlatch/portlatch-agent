// SPDX-License-Identifier: Apache-2.0

package proxyproto

import (
	"bytes"
	"errors"
	"io"
	"net/netip"
	"testing"
)

func TestRoundTripKeepsTheVisitorAndTheRestOfTheStream(t *testing.T) {
	for _, h := range []Header{
		{Source: netip.MustParseAddrPort("203.0.113.9:51234"), Destination: netip.MustParseAddrPort("62.210.87.216:443")},
		{Source: netip.MustParseAddrPort("[2001:db8::7]:40000"), Destination: netip.MustParseAddrPort("62.210.87.216:443")},
	} {
		var stream bytes.Buffer
		if err := Write(&stream, h); err != nil {
			t.Fatal(err)
		}
		stream.WriteString("GET / HTTP/1.1\r\n")

		got, err := Read(&stream)
		if err != nil {
			t.Fatal(err)
		}
		if got != h {
			t.Fatalf("read %+v, wrote %+v", got, h)
		}

		// Nothing past the header may be consumed
		rest, _ := io.ReadAll(&stream)
		if string(rest) != "GET / HTTP/1.1\r\n" {
			t.Fatalf("the stream lost bytes: %q", rest)
		}
	}
}

func TestAConnectionWithoutHeaderIsRefused(t *testing.T) {
	_, err := Read(bytes.NewBufferString("GET / HTTP/1.1\r\nHost: x\r\n\r\n"))
	if !errors.Is(err, ErrNotProxy) {
		t.Fatalf("expected ErrNotProxy, got %v", err)
	}
}

func TestALocalHeaderCarriesNoAddress(t *testing.T) {
	stream := append(signature[:], versionCommandLocal, 0x00, 0x00, 0x00)

	got, err := Read(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.IsValid() {
		t.Fatalf("a LOCAL header carried a source: %v", got.Source)
	}
}

func TestAnOversizedHeaderIsRefused(t *testing.T) {
	stream := append(signature[:], versionCommandProxy, familyTCP4, 0xFF, 0xFF)

	if _, err := Read(bytes.NewReader(stream)); !errors.Is(err, ErrNotProxy) {
		t.Fatalf("expected ErrNotProxy, got %v", err)
	}
}
