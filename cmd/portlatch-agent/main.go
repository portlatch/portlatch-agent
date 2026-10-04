// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/portlatch/portlatch-agent/internal/agent"
)

// exitRejected tells a supervisor not to restart a deleted agent: systemd's
// RestartPreventExitStatus and the Windows service both read it.
const exitRejected = 3

func main() {
	cfg := agent.LoadConfig()

	// The Windows service and its commands; nothing elsewhere.
	if platformMain(cfg) {
		return
	}

	setUpLogging(os.Stderr, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx, cfg); err != nil {
		if ctx.Err() != nil {
			return
		}
		slog.Error("agent stopped", "error", err)
		if errors.Is(err, agent.ErrRejected) {
			os.Exit(exitRejected)
		}
		os.Exit(1)
	}
}

func setUpLogging(w io.Writer, level string) {
	parsed := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		parsed = slog.LevelDebug
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: parsed})))
}
