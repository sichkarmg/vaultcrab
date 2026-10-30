// SPDX-License-Identifier: MIT

// Package cache persists the vault metadata snapshot (no secrets) so that the
// host list is available offline.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// SchemaVersion is bumped on incompatible changes of the file format.
const SchemaVersion = 1

const fileName = "snapshot.json"

// Cache is a directory holding the snapshot file.
type Cache struct {
	Dir string
}

// DefaultDir returns <user cache dir>/vaultcrab.
func DefaultDir() (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "vaultcrab"), nil
}

type file struct {
	Version  int            `json:"version"`
	Snapshot model.Snapshot `json:"snapshot"`
}

// ErrNoCache is returned by Load when there is no usable cache.
var ErrNoCache = errors.New("no cached snapshot")

// Path returns the snapshot file path.
func (c *Cache) Path() string { return filepath.Join(c.Dir, fileName) }

// Load reads the snapshot. A missing file, another schema version or a
// different account (when account != "") yield ErrNoCache.
func (c *Cache) Load(account string) (model.Snapshot, error) {
	b, err := os.ReadFile(c.Path())
	if errors.Is(err, os.ErrNotExist) {
		return model.Snapshot{}, ErrNoCache
	}
	if err != nil {
		return model.Snapshot{}, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return model.Snapshot{}, fmt.Errorf("%w: %v", ErrNoCache, err)
	}
	if f.Version != SchemaVersion {
		return model.Snapshot{}, ErrNoCache
	}
	if account != "" && f.Snapshot.Account != "" && f.Snapshot.Account != account {
		return model.Snapshot{}, ErrNoCache
	}
	return f.Snapshot, nil
}

// Save writes the snapshot atomically (temp file + rename), mode 0600.
func (c *Cache) Save(s model.Snapshot) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(file{Version: SchemaVersion, Snapshot: s}, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.Dir, fileName+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_ = tmp.Chmod(0o600) // CreateTemp already uses 0600; best effort on Windows
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), c.Path())
}

// Clear removes the cached snapshot.
func (c *Cache) Clear() error {
	err := os.Remove(c.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
