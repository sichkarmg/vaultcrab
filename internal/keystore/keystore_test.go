// SPDX-License-Identifier: MIT

package keystore

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func testStore(t *testing.T, s Store) {
	t.Helper()
	name := "test/item-" + t.Name()
	s.Delete(name)
	if _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get missing: %v", err)
	}
	for _, v := range [][]byte{[]byte("first\x00value"), []byte("second, longer value")} {
		if err := s.Set(name, v); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(name)
		if err != nil || !bytes.Equal(got, v) {
			t.Fatalf("get = %q, %v; want %q", got, err, v)
		}
	}
	if err := s.Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err := s.Delete(name); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestMemory(t *testing.T) { testStore(t, &Memory{}) }

// TestSystemStore uses the real store of the system: run with
// VAULTCRAB_KEYSTORE_LIVE=1 in a desktop session (it may ask to unlock the
// keyring).
func TestSystemStore(t *testing.T) {
	if os.Getenv("VAULTCRAB_KEYSTORE_LIVE") == "" {
		t.Skip("VAULTCRAB_KEYSTORE_LIVE not set")
	}
	useTestCollection(t)
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("store: %s", s.Name())
	testStore(t, s)
}
