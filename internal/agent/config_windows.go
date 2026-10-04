// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"os"
	"path/filepath"
)

// On Windows the agent runs as a service, whose working directory is
// System32: the data goes to ProgramData, the same for the service and for the
// enrol command that precedes it.
func init() {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	defaultDataDir = filepath.Join(root, "Portlatch")
}
