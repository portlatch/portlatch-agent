// SPDX-License-Identifier: Apache-2.0

// Package wgkey holds the agent's Curve25519 key pair. The private key is
// generated here and is never transmitted: only Public() is sent to the API.
package wgkey

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// KeyLen is the size of a Curve25519 key, in bytes.
const KeyLen = 32

type PrivateKey [KeyLen]byte

type PublicKey [KeyLen]byte

func GeneratePrivateKey() (PrivateKey, error) {
	var k PrivateKey
	if _, err := rand.Read(k[:]); err != nil {
		return PrivateKey{}, fmt.Errorf("read random bytes: %w", err)
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	return k, nil
}

func ParsePrivateKey(s string) (PrivateKey, error) {
	b, err := decode(s)
	if err != nil {
		return PrivateKey{}, err
	}
	var k PrivateKey
	copy(k[:], b)
	return k, nil
}

func ParsePublicKey(s string) (PublicKey, error) {
	b, err := decode(s)
	if err != nil {
		return PublicKey{}, err
	}
	var k PublicKey
	copy(k[:], b)
	return k, nil
}

func (k PrivateKey) Public() (PublicKey, error) {
	b, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		return PublicKey{}, fmt.Errorf("derive public key: %w", err)
	}
	var pub PublicKey
	copy(pub[:], b)
	return pub, nil
}

// Base64 is the wire format used by the API and by wg(8).
func (k PrivateKey) Base64() string { return base64.StdEncoding.EncodeToString(k[:]) }

// Hex is the format the wireguard-go UAPI expects.
func (k PrivateKey) Hex() string { return hex.EncodeToString(k[:]) }

func (k PublicKey) Base64() string { return base64.StdEncoding.EncodeToString(k[:]) }

func (k PublicKey) Hex() string { return hex.EncodeToString(k[:]) }

func decode(s string) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(b) != KeyLen {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(b), KeyLen)
	}
	return b, nil
}
