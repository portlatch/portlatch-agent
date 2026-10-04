// SPDX-License-Identifier: Apache-2.0

package tunnel

import "testing"

// The node is the first address of the network, as the control plane renders its wg0.
func TestTheNodeIsTheFirstAddressOfTheNetwork(t *testing.T) {
	for network, want := range map[string]string{
		"100.64.0.0/16": "100.64.0.1",
		"100.65.0.0/16": "100.65.0.1",
		"100.64.3.7/16": "100.64.0.1",
	} {
		got, err := NodeAddress(network)
		if err != nil {
			t.Fatalf("NodeAddress(%s): %v", network, err)
		}
		if got.String() != want {
			t.Errorf("NodeAddress(%s) = %s, want %s", network, got, want)
		}
	}

	for _, bad := range []string{"", "100.64.0.0", "fd00::/64"} {
		if _, err := NodeAddress(bad); err == nil {
			t.Errorf("NodeAddress(%q) accepted", bad)
		}
	}
}
