// SPDX-License-Identifier: Apache-2.0

package forward

import (
	"net/netip"
	"testing"
)

// Only the node may send a PROXY header: anyone else could forge the visitor's address.
func TestOnlyTheNodeSendsAProxyHeader(t *testing.T) {
	node := netip.MustParseAddr("100.64.0.1")

	cases := []struct {
		remote string
		want   bool
	}{
		{"100.64.0.1:52100", true},
		{"[::ffff:100.64.0.1]:52100", true},
		{"100.64.0.7:52100", false},
		{"203.0.113.9:52100", false},
	}
	for _, c := range cases {
		if got := fromNode(netip.MustParseAddrPort(c.remote), node); got != c.want {
			t.Errorf("fromNode(%s) = %v, want %v", c.remote, got, c.want)
		}
	}
}

// An agent that does not know its node keeps working rather than refusing every web route.
func TestAnUnknownNodeRefusesNothing(t *testing.T) {
	if !fromNode(netip.MustParseAddrPort("203.0.113.9:52100"), netip.Addr{}) {
		t.Error("an unknown node address refused a connection")
	}
}
