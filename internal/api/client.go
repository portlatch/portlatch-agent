// SPDX-License-Identifier: Apache-2.0

// Package api talks to the control plane. Every answer carries the same
// envelope, and the HTTP status carries the meaning: message is human text and
// is never parsed.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

const (
	requestTimeout  = 30 * time.Second
	maxResponseSize = 1 << 20
)

// Error is a non-2xx answer. StatusCode is what the caller decides on.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("http %d", e.StatusCode)
	}
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Message)
}

// IsStatus reports whether err is an Error carrying that status.
func IsStatus(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

type envelope struct {
	Version string          `json:"version"`
	Code    int             `json:"code"`
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type Client struct {
	baseURL   string
	userAgent string
	token     string
	http      *http.Client
}

func NewClient(baseURL, userAgent string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		userAgent: userAgent,
		http:      &http.Client{Timeout: requestTimeout},
	}
}

// SetToken installs the bearer token used by the authenticated routes.
func (c *Client) SetToken(token string) { c.token = token }

func (c *Client) RequestDeviceCode(ctx context.Context) (*DeviceCode, error) {
	var out DeviceCode
	if _, err := c.do(ctx, http.MethodPost, "/agents/device/code", nil, &out); err != nil {
		return nil, err
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return nil, errors.New("POST /agents/device/code: answer carries no code")
	}
	return &out, nil
}

type exchangeRequest struct {
	DeviceCode  string `json:"device_code"`
	WGPublicKey string `json:"wg_public_key"`
}

// ExchangeDeviceCode returns the HTTP status alongside the result, because the
// whole polling decision hangs on it. A status of 0 means the request never
// reached the control plane.
func (c *Client) ExchangeDeviceCode(ctx context.Context, deviceCode, wgPublicKey string) (int, *TokenExchange, error) {
	var out TokenExchange
	body := exchangeRequest{DeviceCode: deviceCode, WGPublicKey: wgPublicKey}
	status, err := c.do(ctx, http.MethodPost, "/agents/device/token", body, &out)
	if err != nil {
		return status, nil, err
	}
	if status != http.StatusOK {
		return status, nil, nil
	}
	if out.Token == "" {
		return status, nil, errors.New("POST /agents/device/token: answer carries no token")
	}
	return status, &out, nil
}

func (c *Client) Self(ctx context.Context) (*Agent, error) {
	var out Agent
	if _, err := c.do(ctx, http.MethodGet, "/agents/self", nil, &out); err != nil {
		return nil, err
	}
	if out.ID == 0 {
		return nil, errors.New("GET /agents/self: answer carries no agent")
	}
	return &out, nil
}

// What the agent says about itself: its version and the platform it was built
// for, nothing about the machine or the network it runs on.
type heartbeatRequest struct {
	Version string `json:"version,omitempty"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

func (c *Client) Heartbeat(ctx context.Context, version string) (*Agent, error) {
	var out Agent
	request := heartbeatRequest{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH}
	if _, err := c.do(ctx, http.MethodPost, "/agents/self/heartbeat", request, &out); err != nil {
		return nil, err
	}
	if out.ID == 0 {
		return nil, errors.New("POST /agents/self/heartbeat: answer carries no agent")
	}
	return &out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) (int, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("%s %s: encode request: %w", method, path, err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return 0, fmt.Errorf("%s %s: build request: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: read response: %w", method, path, err)
	}

	var env envelope
	// A body that is not the envelope is not fatal: the status still decides.
	_ = json.Unmarshal(raw, &env)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, &Error{StatusCode: resp.StatusCode, Message: env.Message}
	}
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return resp.StatusCode, nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: decode data: %w", method, path, err)
	}
	return resp.StatusCode, nil
}
