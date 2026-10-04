// SPDX-License-Identifier: Apache-2.0

package wgkey

import (
	"strings"
	"testing"
)

// The API wants 32 bytes of Curve25519 in base64: 43 characters and a trailing '='.
func TestPublicKeyHasTheShapeTheAPIWants(t *testing.T) {
	key, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	public, err := key.Public()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	encoded := public.Base64()
	if len(encoded) != 44 {
		t.Errorf("public key is %d characters, want 44: %q", len(encoded), encoded)
	}
	if !strings.HasSuffix(encoded, "=") {
		t.Errorf("public key does not end with '=': %q", encoded)
	}
}

func TestPrivateKeyIsClampedAndSurvivesARoundTrip(t *testing.T) {
	key, err := GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if key[0]&7 != 0 || key[31]&128 != 0 || key[31]&64 == 0 {
		t.Errorf("private key is not clamped: %x %x", key[0], key[31])
	}

	parsed, err := ParsePrivateKey(key.Base64())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed != key {
		t.Error("private key did not survive a base64 round trip")
	}
	if len(key.Hex()) != 64 {
		t.Errorf("hex key is %d characters, want 64", len(key.Hex()))
	}
}

func TestParseRejectsAKeyOfTheWrongSize(t *testing.T) {
	if _, err := ParsePublicKey("c2hvcnQ="); err == nil {
		t.Error("a short key was accepted")
	}
}
