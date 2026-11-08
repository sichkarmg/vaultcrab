// SPDX-License-Identifier: MIT

package quickunlock

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/keystore"
)

var fast = KDF{Time: 1, MemKiB: 64, Threads: 1}

func setup(t *testing.T) (Manager, *keystore.Memory, Policy) {
	t.Helper()
	st := &keystore.Memory{}
	m := Manager{Store: st}
	pol := Policy{Boot: "boot-1", Now: time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC), KDF: fast}
	if err := m.SetPIN([]byte("482913"), false, pol); err != nil {
		t.Fatal(err)
	}
	if err := m.Seal("default", "u@s", []byte("master pw"), pol); err != nil {
		t.Fatal(err)
	}
	return m, st, pol
}

func TestOpenWithPIN(t *testing.T) {
	m, st, pol := setup(t)
	pw, err := m.Open("default", "u@s", []byte("482913"), pol)
	if err != nil || string(pw) != "master pw" {
		t.Fatalf("open = %q, %v", pw, err)
	}
	// nothing of the store holds the password or the PIN in clear
	for name, v := range st.Items {
		if bytes.Contains(v, []byte("master pw")) || bytes.Contains(v, []byte("482913")) {
			t.Fatalf("%s holds a secret in clear: %s", name, v)
		}
	}
	if _, err := m.Open("other", "", []byte("482913"), pol); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("unsealed vault: %v", err)
	}
}

// The default KDF is argon2id with a heavy memory cost and a random salt.
func TestKDF(t *testing.T) {
	if DefaultKDF.MemKiB < 64<<10 || DefaultKDF.Time < 2 {
		t.Fatalf("weak default KDF %+v", DefaultKDF)
	}
	st := &keystore.Memory{}
	m := Manager{Store: st}
	pol := Policy{KDF: fast}
	m.SetPIN([]byte("482913"), false, pol)
	var a pinItem
	json.Unmarshal(st.Items[pinName], &a)
	m.SetPIN([]byte("482913"), false, pol)
	var b pinItem
	json.Unmarshal(st.Items[pinName], &b)
	if len(a.Salt) != 16 || bytes.Equal(a.Salt, b.Salt) || bytes.Equal(a.Priv, b.Priv) {
		t.Fatal("salt not random")
	}
	if !bytes.Equal(deriveKey([]byte("1"), a.Salt, fast), deriveKey([]byte("1"), a.Salt, fast)) {
		t.Fatal("KDF not deterministic")
	}
}

// Wrong PINs count; after the limit everything is gone and the master
// password is needed; a right PIN or the master password resets the count.
func TestAttemptLimit(t *testing.T) {
	m, st, pol := setup(t)
	pol.MaxAttempts = 3
	for left := 2; left >= 1; left-- {
		_, err := m.Open("default", "u@s", []byte("000001"), pol)
		var w WrongPIN
		if !errors.As(err, &w) || w.Left != left || !errors.Is(err, ErrWrongPIN) {
			t.Fatalf("wrong PIN: %v", err)
		}
	}
	// a right PIN resets the counter
	pw, err := m.Open("default", "u@s", []byte("482913"), pol)
	if err != nil {
		t.Fatal(err)
	}
	_ = pw
	for range 2 {
		m.Open("default", "u@s", []byte("000001"), pol)
	}
	if _, err := m.Open("default", "u@s", []byte("000001"), pol); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("third wrong PIN: %v", err)
	}
	if len(st.Items) != 0 {
		t.Fatalf("items left after the limit: %v", st.Items)
	}
	if _, err := m.Open("default", "u@s", []byte("482913"), pol); !errors.Is(err, ErrNoPIN) {
		t.Fatalf("after the limit: %v", err)
	}
}

func TestMasterRequired(t *testing.T) {
	m, _, pol := setup(t)
	// another boot
	reboot := pol
	reboot.Boot, reboot.AfterReboot = "boot-2", true
	if err := m.Ready("default", "u@s", reboot); !errors.Is(err, ErrMasterRequired) || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("after reboot: %v", err)
	}
	reboot.AfterReboot = false
	if err := m.Ready("default", "u@s", reboot); err != nil {
		t.Fatalf("reboot rule off: %v", err)
	}
	// 15 days later
	old := pol
	old.Now = pol.Now.Add(15 * 24 * time.Hour)
	if err := m.Ready("default", "u@s", old); !errors.Is(err, ErrMasterRequired) || !strings.Contains(err.Error(), "14 days") {
		t.Fatalf("after 15 days: %v", err)
	}
	if _, err := m.Open("default", "u@s", []byte("482913"), old); !errors.Is(err, ErrMasterRequired) {
		t.Fatalf("open after 15 days: %v", err)
	}
	// the master password entered again: sealed anew without the PIN
	if err := m.Seal("default", "u@s", []byte("master pw 2"), old); err != nil {
		t.Fatal(err)
	}
	pw, err := m.Open("default", "u@s", []byte("482913"), old)
	if err != nil || string(pw) != "master pw 2" {
		t.Fatalf("after re-seal: %q %v", pw, err)
	}
	// another account
	if err := m.Ready("default", "x@s", old); !errors.Is(err, ErrMasterRequired) {
		t.Fatalf("other account: %v", err)
	}
	if err := m.Ready("default", "u@s", old); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("after the account change the password must be gone: %v", err)
	}
}

func TestChangePINDropsPasswords(t *testing.T) {
	m, _, pol := setup(t)
	if err := m.CheckPINOnly([]byte("482913"), pol); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPIN([]byte("739154"), false, pol); err != nil {
		t.Fatal(err)
	}
	if err := m.Ready("default", "u@s", pol); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("old sealed password kept: %v", err)
	}
}

func TestForget(t *testing.T) {
	m, st, pol := setup(t)
	m.Seal("work", "w@s", []byte("pw2"), pol)
	st.Set("touchid/default", []byte("x"))
	if err := m.Forget("touchid/default"); err != nil {
		t.Fatal(err)
	}
	if len(st.Items) != 0 {
		t.Fatalf("left: %v", st.Items)
	}
	if err := m.Forget(); err != nil {
		t.Fatalf("forget twice: %v", err)
	}
}

func TestCheckPIN(t *testing.T) {
	for _, c := range []struct {
		pin   string
		alnum bool
		ok    bool
	}{
		{"482913", false, true}, {"48291", false, false}, {"111111", false, false}, {"123456", false, false},
		{"654321", false, false}, {"48a913", false, false}, {"k7x9q2", true, true}, {"k7 9q2", true, false},
		{"4829130517", false, true},
	} {
		if err := CheckPIN([]byte(c.pin), c.alnum); (err == nil) != c.ok {
			t.Errorf("%q alnum=%v: %v", c.pin, c.alnum, err)
		}
	}
}

func TestBootID(t *testing.T) {
	a, b := BootID(), BootID()
	if a != b {
		t.Fatalf("boot id changes: %q %q", a, b)
	}
}
