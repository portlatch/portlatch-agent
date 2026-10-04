// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheLogFileStartsOverAndKeepsOnePrevious(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	logs, err := openRotatingFile(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()

	for _, line := range []string{"first\n", "second\n", "third\n"} {
		if _, err := logs.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}

	current, _ := os.ReadFile(path)
	previous, _ := os.ReadFile(path + ".1")
	if string(current) != "third\n" || string(previous) != "second\n" {
		t.Errorf("current = %q, previous = %q", current, previous)
	}

	matches, _ := filepath.Glob(path + "*")
	if len(matches) != 2 {
		t.Errorf("files = %s, want the log and one previous", strings.Join(matches, ", "))
	}
}
