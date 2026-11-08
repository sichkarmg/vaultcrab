// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// fakeClock is a wall clock the test moves (a sleep of the computer is a
// jump).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// lockingSource counts the /lock calls.
type lockingSource struct {
	*fakeSource
	locks atomic.Int32
}

func (l *lockingSource) Lock(context.Context) error {
	l.locks.Add(1)
	l.mu.Lock()
	l.status = vault.Locked
	l.mu.Unlock()
	return nil
}

func newLockTest(t *testing.T, u config.Unlock, screen func(context.Context, func()) error) (*Service, *lockingSource, *fakeClock, context.Context) {
	t.Helper()
	cfg := config.Default()
	cfg.Unlock = u
	clk := &fakeClock{t: time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local)}
	src := &lockingSource{fakeSource: &fakeSource{status: vault.Unlocked,
		snap: model.Snapshot{Hosts: []model.Host{{ID: "h"}}}, secrets: map[string]string{"h": "pw"}}}
	s := New(Options{Config: &cfg, ConfigPoll: -1, Now: clk.Now, ScreenLock: screen,
		Connect: func(context.Context) (Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.Start(ctx)
	waitState(t, s, Online)
	return s, src, clk, ctx
}

// Input after more than the timeout (a sleep counts: the computer slept
// 9 h with the default 8 h) locks the vaults first; bw is asked to lock.
func TestAutoLockAfterSleep(t *testing.T) {
	s, src, clk, ctx := newLockTest(t, config.Unlock{LockTimeout: "8h"}, nil)
	clk.add(9 * time.Hour)
	if !s.Activity() {
		t.Fatal("not locked after 9 h")
	}
	if st := s.Status(); st.State != Locked || !errors.Is(st.Err, errAutoLocked) {
		t.Fatalf("status %+v", st)
	}
	if _, err := s.Secret(ctx, "h"); !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("secret after the auto-lock: %v", err)
	}
	if src.locks.Load() != 1 {
		t.Fatalf("bw lock calls: %d", src.locks.Load())
	}
}

// Input within the timeout keeps the vault unlocked; the 1-4 h sleeps of
// the report do not lock with the default.
func TestActivityKeepsUnlocked(t *testing.T) {
	s, src, clk, _ := newLockTest(t, config.Unlock{}, nil) // default 8h
	for range 4 {
		clk.add(4 * time.Hour)
		if s.Activity() {
			t.Fatal("locked within the timeout")
		}
	}
	if s.Status().State != Online || src.locks.Load() != 0 {
		t.Fatalf("status %+v, locks %d", s.Status(), src.locks.Load())
	}
}

// A connection after the timeout without input (the timer is late after a
// sleep) is refused: the vault is locked first.
func TestSecretChecksDeadline(t *testing.T) {
	s, _, clk, ctx := newLockTest(t, config.Unlock{LockTimeout: "15m"}, nil)
	clk.add(16 * time.Minute)
	if _, err := s.Secret(ctx, "h"); !errors.Is(err, ErrVaultLocked) {
		t.Fatalf("secret: %v", err)
	}
}

func TestLockNever(t *testing.T) {
	s, _, clk, ctx := newLockTest(t, config.Unlock{LockTimeout: "never"}, nil)
	clk.add(100 * 24 * time.Hour)
	if s.Activity() || s.Status().State != Online {
		t.Fatal("locked with never")
	}
	sec, err := s.Secret(ctx, "h")
	if err != nil {
		t.Fatal(err)
	}
	sec.Wipe()
}

// The timer locks an idle Vaultcrab that stays awake.
func TestAutoLockTimer(t *testing.T) {
	s, _, clk, _ := newLockTest(t, config.Unlock{LockTimeout: "never"}, nil)
	s.lock.mu.Lock()
	s.lock.timeout = time.Hour
	s.lock.mu.Unlock()
	// the clock jumps past the deadline; the timer set for it (1 h of
	// monotonic time) is woken by the policy change and sees it
	clk.add(2 * time.Hour)
	s.kickAutoLock()
	waitState(t, s, Locked)
}

// A changed policy applies at once (Settings, a config reload).
func TestSetLockPolicy(t *testing.T) {
	s, _, clk, _ := newLockTest(t, config.Unlock{LockTimeout: "never"}, nil)
	clk.add(2 * time.Hour)
	s.SetLockPolicy(config.Unlock{LockTimeout: "1h"})
	waitState(t, s, Locked)
	if p := s.Policy(); p.Timeout != time.Hour {
		t.Fatalf("policy %+v", p)
	}
}

// With lock_on_screen_lock the screen lock locks the vaults; without it,
// it does not; a sleep by itself does not either.
func TestScreenLock(t *testing.T) {
	var onLock func()
	watch := func(_ context.Context, f func()) error { onLock = f; return nil }
	s, _, _, ctx := newLockTest(t, config.Unlock{LockOnScreenLock: true}, watch)
	if onLock == nil {
		t.Fatal("watcher not started")
	}
	if p := s.Policy(); !p.OnScreenLock || p.ScreenLockErr != nil {
		t.Fatalf("policy %+v", p)
	}
	s.SetLockPolicy(config.Unlock{LockOnScreenLock: false})
	onLock()
	if s.Status().State != Online {
		t.Fatal("locked with lock_on_screen_lock off")
	}
	s.SetLockPolicy(config.Unlock{LockOnScreenLock: true})
	onLock()
	if st := s.Status(); st.State != Locked || !errors.Is(st.Err, errAutoLocked) {
		t.Fatalf("status %+v", st)
	}
	if err := s.Unlock(ctx, []byte("master")); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Online)
}

func TestScreenLockUnsupported(t *testing.T) {
	why := errors.New("no session bus")
	watch := func(context.Context, func()) error { return why }
	s, _, _, _ := newLockTest(t, config.Unlock{LockOnScreenLock: true}, watch)
	if p := s.Policy(); !errors.Is(p.ScreenLockErr, why) {
		t.Fatalf("policy %+v", p)
	}
	s2, _, _, _ := newLockTest(t, config.Unlock{LockOnScreenLock: true}, nil)
	if p := s2.Policy(); !errors.Is(p.ScreenLockErr, ErrScreenLockUnsupported) {
		t.Fatalf("policy without a watcher %+v", p)
	}
}
