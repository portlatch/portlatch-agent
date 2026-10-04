// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// rotatingFile appends to a log file and starts it over past max bytes,
// keeping the previous one beside it as <name>.1: a Windows service has no
// journal to do that for it, and the agent logs every relayed connection.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	file *os.File
	size int64
}

func openRotatingFile(path string, max int64) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	r := &rotatingFile{path: path, max: max}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("open log file: %w", err)
	}
	r.file, r.size = file, info.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size > 0 && r.size+int64(len(p)) > r.max {
		r.file.Close()
		// os.Rename replaces the older file, on Windows too.
		if err := os.Rename(r.path, r.path+".1"); err != nil {
			return 0, fmt.Errorf("rotate log file: %w", err)
		}
		if err := r.open(); err != nil {
			return 0, err
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}
