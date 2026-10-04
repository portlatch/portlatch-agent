// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package main

import "github.com/portlatch/portlatch-agent/internal/agent"

// Elsewhere, the agent only ever runs in the foreground: Docker or systemd
// supervise it.
func platformMain(agent.Config) bool { return false }
