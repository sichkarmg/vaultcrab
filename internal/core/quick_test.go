// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/keystore"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/quickunlock"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

type pinTest struct {
	s     *Service
	src   *lockingSource
	clk   *fakeClock
	store *keystore.Memory
	ctx   context.Context
	mu    sync.Mutex
	boot  string
}

func newPINTest(t *testing.T) *pinTest {
	t.Helper()
	cfg := config.Default()
	cfg.Unlock.PIN = true
	cfg.Unlock.LockTimeout = "never"
	cfg.Log = config.Log{Enabled: true, File: filepath.Join(t.TempDir(), "vaultcrab.log")}
	p := &pinTest{clk: &fakeClock{t: time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local)}, store: &keystore.Memory{}, boot: "b1"}
	p.src = &lockingSource{fakeSource: &fakeSource{status: vault.Unlocked,
		snap: model.Snapshot{Hosts: []model.Host{{ID: "h"}}}, secrets: map[string]string{"h": "pw"}}}
	p.s = New(Options{Config: &cfg, ConfigPoll: -1, Now: p.clk.Now, Keystore: p.store,
		PINKDF:  quickunlock.KDF{Time: 1, MemKiB: 64, Threads: 1},
		BootID:  func() string { p.mu.Lock(); defer p.mu.Unlock(); return p.boot },
		Connect: func(context.Context) (Source, func(), error) { return p.src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); p.s.SetLogging(false) })
	p.ctx = ctx
	p.s.Start(ctx)
	waitState(t, p.s, Online)
	return p
}

func (p *pinTest) lock(t *testing.T) {
	t.Helper()
	p.s.LockAll(p.ctx, errAutoLocked)
	waitState(t, p.s, Locked)
}

// Set the PIN (the master password is checked by bw), lock, unlock with the
// PIN; a wrong PIN says how many attempts are left.
func TestPINUnlock(t *testing.T) {
	p := newPINTest(t)
	if err := p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("wrong")); err == nil || p.s.HasPIN() {
		t.Fatalf("PIN set with a wrong master password: %v", err)
	}
	if err := p.s.SetPIN(p.ctx, "", []byte("123456"), []byte("master")); !errors.Is(err, ErrWeakPIN) {
		t.Fatalf("weak PIN: %v", err)
	}
	if err := p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("master")); err != nil {
		t.Fatal(err)
	}
	p.lock(t)
	if q := p.s.QuickUnlock(""); !q.PIN {
		t.Fatalf("quick state %+v", q)
	}
	err := p.s.UnlockWithPIN(p.ctx, "", []byte("000001"))
	var w WrongPIN
	if !errors.As(err, &w) || w.Left != 4 {
		t.Fatalf("wrong PIN: %v", err)
	}
	if err := p.s.UnlockWithPIN(p.ctx, "", []byte("482913")); err != nil {
		t.Fatal(err)
	}
	waitState(t, p.s, Online)
	// neither the PIN nor the password in the log
	lines, _ := p.s.LogTail(200)
	log := strings.Join(lines, "\n")
	if !strings.Contains(log, "unlocked with the PIN") {
		t.Fatalf("log:\n%s", log)
	}
	for _, sec := range []string{"482913", "master"} {
		if strings.Contains(log, sec) {
			t.Fatalf("%q in the log:\n%s", sec, log)
		}
	}
}

// After N days or a restart the master password is required; entering it
// stores it again and the PIN works again.
func TestPINMasterRequired(t *testing.T) {
	p := newPINTest(t)
	p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("master"))
	p.lock(t)
	p.clk.add(15 * 24 * time.Hour)
	if q := p.s.QuickUnlock(""); q.PIN || !strings.Contains(q.Why, "every 14 days") {
		t.Fatalf("after 15 days %+v", q)
	}
	if err := p.s.UnlockWithPIN(p.ctx, "", []byte("482913")); !errors.Is(err, ErrMasterRequired) {
		t.Fatalf("PIN after 15 days: %v", err)
	}
	if err := p.s.Unlock(p.ctx, []byte("master")); err != nil {
		t.Fatal(err)
	}
	waitState(t, p.s, Online)
	p.lock(t)
	if q := p.s.QuickUnlock(""); !q.PIN {
		t.Fatalf("after the master password %+v", q)
	}
	p.mu.Lock()
	p.boot = "b2"
	p.mu.Unlock()
	if q := p.s.QuickUnlock(""); q.PIN || !strings.Contains(q.Why, "restart") {
		t.Fatalf("after a restart %+v", q)
	}
}

// Five wrong PINs delete everything: the master password is needed.
func TestPINLimit(t *testing.T) {
	p := newPINTest(t)
	p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("master"))
	p.lock(t)
	var err error
	for range 5 {
		err = p.s.UnlockWithPIN(p.ctx, "", []byte("000001"))
	}
	if !errors.Is(err, ErrTooManyPINs) || p.s.HasPIN() || len(p.store.Items) != 0 {
		t.Fatalf("after 5 wrong PINs: %v, items %v", err, p.store.Items)
	}
	if q := p.s.QuickUnlock(""); q.PIN || !strings.Contains(q.Why, "no PIN") {
		t.Fatalf("quick state %+v", q)
	}
}

// A changed master password (bw refuses the stored one) or a logout
// deletes the stored password.
func TestPINStalePassword(t *testing.T) {
	p := newPINTest(t)
	p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("master"))
	p.lock(t)
	p.src.mu.Lock()
	p.src.password = "new master"
	p.src.mu.Unlock()
	if err := p.s.UnlockWithPIN(p.ctx, "", []byte("482913")); !errors.Is(err, errStoredPWChanged) {
		t.Fatalf("stale password: %v", err)
	}
	if q := p.s.QuickUnlock(""); q.PIN {
		t.Fatal("stale password kept")
	}
	// logout
	p.s.Unlock(p.ctx, []byte("new master"))
	waitState(t, p.s, Online)
	if q := p.s.QuickUnlock(""); !p.s.HasPIN() {
		t.Fatalf("PIN gone %+v", q)
	}
	p.src.mu.Lock()
	p.src.status = vault.Unauthenticated
	p.src.mu.Unlock()
	p.s.Refresh(p.ctx)
	waitState(t, p.s, NeedLogin)
	if _, ok := p.store.Items["vault/default"]; ok {
		t.Fatal("stored password kept after a logout")
	}
}

func TestForgetPIN(t *testing.T) {
	p := newPINTest(t)
	p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("master"))
	if err := p.s.ForgetPIN(); err != nil {
		t.Fatal(err)
	}
	if len(p.store.Items) != 0 || p.s.HasPIN() {
		t.Fatalf("left %v", p.store.Items)
	}
}
