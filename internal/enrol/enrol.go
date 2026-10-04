// SPDX-License-Identifier: Apache-2.0

// Package enrol runs the device code flow: ask for a code, show it, poll until
// somebody approves it, come back with a token.
package enrol

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/portlatch/portlatch-agent/internal/api"
	"github.com/portlatch/portlatch-agent/internal/state"
	"github.com/portlatch/portlatch-agent/internal/wgkey"
)

const (
	defaultInterval = 5 * time.Second
	maxInterval     = 60 * time.Second
	retryDelay      = 15 * time.Second
)

// Run blocks until the agent is approved, or until ctx is cancelled. Only the
// public key is sent.
func Run(ctx context.Context, client *api.Client, store *state.Store, publicKey wgkey.PublicKey) (*api.TokenExchange, error) {
	for {
		code, err := client.RequestDeviceCode(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			slog.Error("could not request an enrolment code", "error", err)
			if err := sleep(ctx, retryDelay); err != nil {
				return nil, err
			}
			continue
		}

		announce(code)
		if err := store.SaveUserCode(code.UserCode, code.VerificationURI, code.ExpiresAt); err != nil {
			slog.Warn("could not write the enrolment code to the data directory", "error", err)
		}

		result, err := poll(ctx, client, code, publicKey)
		if err != nil {
			return nil, err
		}
		if result == nil {
			continue // the code is dead, ask for a new one
		}
		if err := store.ClearUserCode(); err != nil {
			slog.Warn("could not remove the enrolment file", "error", err)
		}
		return result, nil
	}
}

// poll returns (nil, nil) when the code must be replaced by a fresh one.
func poll(ctx context.Context, client *api.Client, code *api.DeviceCode, publicKey wgkey.PublicKey) (*api.TokenExchange, error) {
	interval := time.Duration(code.Interval) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}

	for {
		if err := sleep(ctx, interval); err != nil {
			return nil, err
		}

		status, result, err := client.ExchangeDeviceCode(ctx, code.DeviceCode, publicKey.Base64())
		if err == nil && status == http.StatusOK && result != nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		switch status {
		case http.StatusAccepted:
			// Nobody has approved the code yet.
		case http.StatusNotFound, http.StatusConflict, http.StatusGone:
			slog.Info("the enrolment code is no longer usable, asking for a new one", "status", status)
			return nil, nil
		case http.StatusUnprocessableEntity:
			return nil, fmt.Errorf("the control plane rejected our public key: %w", err)
		case http.StatusTooManyRequests:
			interval = min(interval*2, maxInterval)
			slog.Warn("rate limited, slowing down", "interval", interval.String())
		default:
			slog.Warn("unexpected answer while polling", "status", status, "error", err)
		}

		if !code.ExpiresAt.IsZero() && time.Now().After(code.ExpiresAt) {
			slog.Info("the enrolment code expired, asking for a new one")
			return nil, nil
		}
	}
}

// announce writes the code where a human will see it. The device_code stays out
// of both the logs and the banner.
func announce(code *api.DeviceCode) {
	fmt.Fprintf(os.Stderr, `
  ┌──────────────────────────────────────────────┐
  │  This agent is waiting for your approval.    │
  └──────────────────────────────────────────────┘

    Open   %s
    Enter  %s

  The code expires at %s.

`, code.VerificationURI, code.UserCode, code.ExpiresAt.UTC().Format(time.RFC3339))

	slog.Info("waiting for approval",
		"user_code", code.UserCode,
		"verification_uri", code.VerificationURI,
		"expires_at", code.ExpiresAt.UTC().Format(time.RFC3339))
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
