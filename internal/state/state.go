// SPDX-License-Identifier: Apache-2.0

// Package state persists what must survive a container restart: the WireGuard
// private key and the API token. Both are written 0600, and neither is ever
// logged.
package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/portlatch/portlatch-agent/internal/wgkey"
)

const (
	privateKeyFile = "private.key"
	tokenFile      = "token"
	userCodeFile   = "enrolment.txt"

	secretMode = 0o600
	publicMode = 0o644
)

type Store struct {
	dir string
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) TokenPath() string { return filepath.Join(s.dir, tokenFile) }

func (s *Store) PrivateKey() (wgkey.PrivateKey, bool, error) {
	raw, found, err := s.read(privateKeyFile)
	if err != nil || !found {
		return wgkey.PrivateKey{}, false, err
	}
	key, err := wgkey.ParsePrivateKey(raw)
	if err != nil {
		return wgkey.PrivateKey{}, false, fmt.Errorf("read %s: %w", privateKeyFile, err)
	}
	return key, true, nil
}

func (s *Store) SavePrivateKey(key wgkey.PrivateKey) error {
	return s.write(privateKeyFile, key.Base64()+"\n", secretMode)
}

func (s *Store) Token() (string, bool, error) {
	return s.read(tokenFile)
}

func (s *Store) SaveToken(token string) error {
	return s.write(tokenFile, token+"\n", secretMode)
}

// SaveUserCode leaves the code where the owner of a NAS will actually find it.
// The secret device_code is deliberately absent.
func (s *Store) SaveUserCode(userCode, verificationURI string, expiresAt time.Time) error {
	body := fmt.Sprintf(`portlatch agent — waiting for approval

Open  %s
Enter %s

The code expires at %s.
This file disappears once the agent is enrolled.
`, verificationURI, userCode, expiresAt.UTC().Format(time.RFC3339))
	return s.write(userCodeFile, body, publicMode)
}

func (s *Store) ClearUserCode() error {
	err := os.Remove(filepath.Join(s.dir, userCodeFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", userCodeFile, err)
	}
	return nil
}

// Forget removes the key, the token and the enrolment code, and nothing else:
// the directory may be one the user chose, holding other files.
func (s *Store) Forget() error {
	for _, name := range []string{privateKeyFile, tokenFile, userCodeFile} {
		err := os.Remove(filepath.Join(s.dir, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) read(name string) (string, bool, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", name, err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func (s *Store) write(name, content string, mode os.FileMode) error {
	tmp, err := os.CreateTemp(s.dir, name+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, name)); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
