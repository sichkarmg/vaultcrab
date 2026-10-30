// SPDX-License-Identifier: MIT

package cache

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

func TestRoundTrip(t *testing.T) {
	c := &Cache{Dir: filepath.Join(t.TempDir(), "sub")}
	if _, err := c.Load(""); !errors.Is(err, ErrNoCache) {
		t.Fatalf("want ErrNoCache, got %v", err)
	}
	s := model.Snapshot{
		Account:  "a@b",
		SyncedAt: time.Date(2026, 10, 21, 1, 2, 3, 0, time.UTC),
		Hosts:    []model.Host{{ID: "1", Name: "h", Scheme: model.SSH, User: "u", Addr: "a", Port: 22, Tags: []string{"x"}}},
		Folders:  []model.Folder{{ID: "f", Name: "A/B", Path: []string{"A", "B"}}},
	}
	if err := c.Save(s); err != nil {
		t.Fatal(err)
	}
	got, err := c.Load("a@b")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("got %+v", got)
	}
	if _, err := c.Load("other@b"); !errors.Is(err, ErrNoCache) {
		t.Fatal("cache of another account must not be used")
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(c.Path())
		if st.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", st.Mode())
		}
	}
	ents, _ := os.ReadDir(c.Dir)
	if len(ents) != 1 {
		t.Errorf("temp files left: %v", ents)
	}
}

func TestBadVersion(t *testing.T) {
	c := &Cache{Dir: t.TempDir()}
	os.WriteFile(c.Path(), []byte(`{"version":999,"snapshot":{}}`), 0o600)
	if _, err := c.Load(""); !errors.Is(err, ErrNoCache) {
		t.Fatal(err)
	}
	os.WriteFile(c.Path(), []byte(`garbage`), 0o600)
	if _, err := c.Load(""); !errors.Is(err, ErrNoCache) {
		t.Fatal(err)
	}
}
