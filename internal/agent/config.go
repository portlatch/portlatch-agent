// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"os"
	"strings"
)

// Production by default, so that a plain `docker run` with no environment
// variable at all works, and so does a build from source. A development build
// points elsewhere with PORTLATCH_API_URL, or at link time:
//
//	go build -ldflags "-X github.com/portlatch/portlatch-agent/internal/agent.defaultAPIBaseURL=http://localhost:44/api/v1"
var (
	defaultAPIBaseURL = "https://portlatch.eu/api/v1"

	// Relative on purpose: ./.data when run from a checkout, /.data in the
	// container, where the working directory is the root.
	defaultDataDir = ".data"

	// Version is what the heartbeat reports.
	Version = "1.0.2"
)

type Config struct {
	APIBaseURL string
	DataDir    string
	LogLevel   string
	Version    string
}

// LoadConfig reads the optional overrides. None of them is required.
func LoadConfig() Config {
	return Config{
		APIBaseURL: strings.TrimRight(env("PORTLATCH_API_URL", defaultAPIBaseURL), "/"),
		DataDir:    env("PORTLATCH_DATA_DIR", defaultDataDir),
		LogLevel:   env("PORTLATCH_LOG_LEVEL", "info"),
		Version:    Version,
	}
}

func (c Config) UserAgent() string { return "portlatch-agent/" + c.Version }

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
