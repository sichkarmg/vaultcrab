// SPDX-License-Identifier: MIT

package workspace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// ViewState is the state of the host list kept between runs: the chosen
// type filter, organization, collection, grouping, search, folded groups
// and the selected row. No secrets.
type ViewState struct {
	Version    int      `json:"version"`
	Filter     string   `json:"filter,omitempty"` // filter tab name
	Org        string   `json:"org,omitempty"`
	Collection string   `json:"collection,omitempty"`
	Grouping   string   `json:"grouping,omitempty"`
	Query      string   `json:"query,omitempty"`
	Collapsed  []string `json:"collapsed,omitempty"`
	Selected   string   `json:"selected,omitempty"` // row key
}

const viewVersion = 1

// SaveView writes the host list state atomically (0600).
func SaveView(path string, v ViewState) error {
	v.Version = viewVersion
	return writeJSON(path, v)
}

// LoadView reads a saved host list state; a missing file is an empty state.
func LoadView(path string) (ViewState, error) {
	var v ViewState
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(b, &v); err != nil || v.Version != viewVersion {
		return ViewState{}, errors.New("unsupported view state")
	}
	return v, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// a temporary file of its own: several copies of vaultcrab closed at once
	// save at the same time
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // after a failure; gone after the rename
	_ = tmp.Chmod(0o600)        // CreateTemp already uses 0600; best effort on Windows
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return rename(tmp.Name(), path)
}

// rename replaces newpath with oldpath. Windows refuses (access denied) while
// another process replaces the same file; that takes milliseconds, so it is
// retried for a while.
func rename(oldpath, newpath string) error {
	err := os.Rename(oldpath, newpath)
	for wait := time.Duration(0); err != nil && runtime.GOOS == "windows" && errors.Is(err, fs.ErrPermission) && wait < 2*time.Second; wait += 10 * time.Millisecond {
		time.Sleep(10 * time.Millisecond)
		err = os.Rename(oldpath, newpath)
	}
	return err
}
