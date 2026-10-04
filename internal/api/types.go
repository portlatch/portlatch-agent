// SPDX-License-Identifier: Apache-2.0

package api

import "time"

// DeviceCode is the answer to POST /agents/device/code.
type DeviceCode struct {
	// DeviceCode is a secret: it is never logged, nor written to disk.
	DeviceCode      string    `json:"device_code"`
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	ExpiresAt       time.Time `json:"expires_at"`
	Interval        int       `json:"interval"`
}

// TokenExchange is the answer to POST /agents/device/token once approved.
type TokenExchange struct {
	Token string `json:"token"`
	Agent Agent  `json:"agent"`
}

// Agent is the configuration, served by GET /agents/self and by the heartbeat.
type Agent struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	WGPublicKey     string     `json:"wg_public_key"`
	Version         string     `json:"version"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
	Tunnel          *Tunnel    `json:"tunnel"`
	Listeners       []Listener `json:"listeners"`
}

// Tunnel is everything needed to bring the interface up. Nothing here is secret.
type Tunnel struct {
	Address       string `json:"address"`
	Network       string `json:"network"`
	Endpoint      string `json:"endpoint"`
	NodePublicKey string `json:"node_public_key"`
}

// Listener is one port to listen on inside the tunnel, and the LAN target to
// copy to. It carries no type on purpose: the agent never learns what a web
// route is, only whether a PROXY header precedes the connection.
type Listener struct {
	ID          int64  `json:"id"`
	Port        uint16 `json:"port"`
	AcceptProxy bool   `json:"accept_proxy"`
	SendProxy   bool   `json:"send_proxy"`
	TargetHost  string `json:"target_host"`
	TargetPort  uint16 `json:"target_port"`
}
