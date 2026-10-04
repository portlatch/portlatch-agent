// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func envelopeOf(t *testing.T, status int, data any) []byte {
	t.Helper()
	raw := json.RawMessage("null")
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		raw = encoded
	}
	body, err := json.Marshal(envelope{Version: "1.0.0", Code: status, Status: "success", Data: raw})
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return body
}

// The status carries the meaning, so the caller must get it back untouched.
func TestExchangeDeviceCodeReturnsEachStatusOfTheContract(t *testing.T) {
	for _, status := range []int{
		http.StatusAccepted,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusGone,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write(envelopeOf(t, status, nil))
		}))

		got, result, _ := NewClient(server.URL, "test").ExchangeDeviceCode(context.Background(), "secret", "key")
		if got != status {
			t.Errorf("status = %d, want %d", got, status)
		}
		if result != nil {
			t.Errorf("status %d returned a result", status)
		}
		server.Close()
	}
}

func TestExchangeDeviceCodeSendsThePublicKeyAndReadsTheToken(t *testing.T) {
	var sent exchangeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &sent)
		w.WriteHeader(http.StatusOK)
		w.Write(envelopeOf(t, http.StatusOK, TokenExchange{
			Token: "42|abcdef",
			Agent: Agent{ID: 12, Name: "Living room NAS"},
		}))
	}))
	defer server.Close()

	status, result, err := NewClient(server.URL, "test").ExchangeDeviceCode(context.Background(), "secret", "public-key")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusOK || result.Token != "42|abcdef" {
		t.Fatalf("status = %d, token = %q", status, result.Token)
	}
	if sent.DeviceCode != "secret" || sent.WGPublicKey != "public-key" {
		t.Errorf("request body = %+v", sent)
	}
}

func TestHeartbeatSendsTheTokenAndReadsTheTunnel(t *testing.T) {
	var authorization string
	var sent map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusOK)
		w.Write(envelopeOf(t, http.StatusOK, Agent{
			ID:     12,
			Status: "online",
			Tunnel: &Tunnel{Address: "100.64.0.2", Network: "100.64.0.0/16"},
		}))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test")
	client.SetToken("42|abcdef")

	self, err := client.Heartbeat(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if authorization != "Bearer 42|abcdef" {
		t.Errorf("Authorization = %q", authorization)
	}
	if sent["version"] != "1.0.0" || sent["os"] != runtime.GOOS || sent["arch"] != runtime.GOARCH {
		t.Errorf("heartbeat body = %v", sent)
	}
	if len(sent) != 3 {
		t.Errorf("heartbeat body carries more than version, os and arch: %v", sent)
	}
	if self.Tunnel == nil || self.Tunnel.Address != "100.64.0.2" {
		t.Errorf("tunnel = %+v", self.Tunnel)
	}
}

func TestARevokedTokenIsRecognisable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write(envelopeOf(t, http.StatusUnauthorized, nil))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "test").Self(context.Background())
	if !IsStatus(err, http.StatusUnauthorized) {
		t.Errorf("error = %v, want a 401", err)
	}
}
