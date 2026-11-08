// SPDX-License-Identifier: MIT

package quickunlock

import (
	"errors"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/keystore"
)

func TestTouch(t *testing.T) {
	st := &keystore.Memory{}
	m := Manager{Store: st}
	pol := Policy{Boot: "b1", Now: time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)}
	fingers := []byte("enrolled fingerprints v1")
	if err := m.TouchReady("default", "u@s", fingers, pol); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("before: %v", err)
	}
	if err := m.SealTouch("default", "u@s", []byte("master"), fingers, pol); err != nil {
		t.Fatal(err)
	}
	pw, err := m.OpenTouch("default", "u@s", fingers, pol)
	if err != nil || string(pw) != "master" || !m.HasTouch("default") {
		t.Fatalf("open %q %v", pw, err)
	}
	reboot := pol
	reboot.Boot, reboot.AfterReboot = "b2", true
	if err := m.TouchReady("default", "u@s", fingers, reboot); !errors.Is(err, ErrMasterRequired) {
		t.Fatalf("after reboot: %v", err)
	}
	later := pol
	later.Now = pol.Now.Add(15 * 24 * time.Hour)
	if err := m.TouchReady("default", "u@s", fingers, later); !errors.Is(err, ErrMasterRequired) {
		t.Fatalf("after 15 days: %v", err)
	}
	// a finger added: the data is deleted
	if err := m.TouchReady("default", "u@s", []byte("enrolled fingerprints v2"), pol); !errors.Is(err, ErrBiometryChanged) {
		t.Fatalf("changed fingerprints: %v", err)
	}
	if m.HasTouch("default") || len(st.Items) != 0 {
		t.Fatalf("left after the change: %v", st.Items)
	}
	m.SealTouch("default", "u@s", []byte("master"), fingers, pol)
	m.SealTouch("work", "w@s", []byte("pw2"), fingers, pol)
	if err := m.ForgetTouch(); err != nil || len(st.Items) != 0 {
		t.Fatalf("forget: %v %v", err, st.Items)
	}
}
