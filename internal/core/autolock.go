// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
)

// Auto-lock (unlock: in the config, Settings → Security): the vaults are
// locked after unlock.lock_timeout without keyboard or mouse input
// (Activity) and, with unlock.lock_on_screen_lock, when the screen is
// locked.
//
// The time is wall-clock time: a sleep of the computer counts (the timers of
// Go do not advance while it sleeps), so Activity and Secret check the
// deadline themselves; the timer only locks an idle Vaultcrab that stays
// awake. One timer, set to the deadline: no periodic wake-ups.

// ErrScreenLockUnsupported is returned by a ScreenLock watcher that cannot
// detect screen locks on this system or in this build.
var ErrScreenLockUnsupported = errors.New("screen lock detection is not available here")

type autoLock struct {
	mu       sync.Mutex
	timeout  time.Duration // 0 = never
	onScreen bool
	last     time.Time // wall clock (Round(0)) of the last input
	wake     chan struct{}
	watching bool  // the screen lock watcher runs
	watchErr error // why it does not
	enabled  bool  // events of the watcher lock (lock_on_screen_lock)
	unlock   config.Unlock
}

// LockPolicy is the auto-lock in effect.
type LockPolicy struct {
	Timeout      time.Duration // 0 = never
	OnScreenLock bool
	// ScreenLockErr says why screen locks are not detected (nil when they
	// are, or when OnScreenLock is off).
	ScreenLockErr error
}

func (s *Service) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now().Round(0)
	}
	return time.Now().Round(0) // wall clock: a sleep counts
}

func (s *Service) initAutoLock() {
	a := &s.lock
	a.wake = make(chan struct{}, 1)
	a.last = s.now()
	u := config.Default().Unlock
	if s.opts.Config != nil {
		u = s.opts.Config.Unlock
	}
	a.timeout, a.onScreen, a.unlock = u.Timeout(), u.LockOnScreenLock, u
}

func (s *Service) kickAutoLock() {
	select {
	case s.lock.wake <- struct{}{}:
	default:
	}
}

// Policy returns the auto-lock in effect.
func (s *Service) Policy() LockPolicy {
	a := &s.lock
	a.mu.Lock()
	defer a.mu.Unlock()
	p := LockPolicy{Timeout: a.timeout, OnScreenLock: a.onScreen}
	if a.onScreen && !a.watching {
		p.ScreenLockErr = a.watchErr
		if p.ScreenLockErr == nil {
			p.ScreenLockErr = ErrScreenLockUnsupported
		}
	}
	return p
}

// SetLockPolicy changes the auto-lock while running (the settings, a config
// reload).
func (s *Service) SetLockPolicy(u config.Unlock) {
	a := &s.lock
	a.mu.Lock()
	if u.PINAttempts == 0 || u.MasterEveryDays == 0 { // not from a loaded config
		d := config.DefaultUnlock()
		if u.PINAttempts == 0 {
			u.PINAttempts = d.PINAttempts
		}
		if u.MasterEveryDays == 0 {
			u.MasterEveryDays = d.MasterEveryDays
		}
	}
	changed := a.timeout != u.Timeout() || a.onScreen != u.LockOnScreenLock
	a.timeout, a.onScreen, a.unlock = u.Timeout(), u.LockOnScreenLock, u
	a.enabled = a.onScreen
	start := a.onScreen && !a.watching && a.watchErr == nil
	a.mu.Unlock()
	if changed {
		logf("auto-lock: after %s without input, with the screen lock %v", timeoutText(u.Timeout()), u.LockOnScreenLock)
	}
	if start {
		s.startScreenWatch()
	}
	s.kickAutoLock()
}

func timeoutText(d time.Duration) string {
	if d == 0 {
		return "never"
	}
	return d.String()
}

// Activity records keyboard or mouse input. If the lock timeout passed since
// the previous input (a sleep counts), the vaults are locked first and true
// is returned.
func (s *Service) Activity() bool {
	now := s.now()
	a := &s.lock
	a.mu.Lock()
	expired := a.timeout > 0 && now.Sub(a.last) >= a.timeout
	idle := now.Sub(a.last)
	a.last = now
	a.mu.Unlock()
	if expired && s.anyUnlocked() {
		s.LockAll(context.Background(), fmt.Errorf("%w after %s without input", errAutoLocked, idle.Round(time.Minute)))
		return true
	}
	return false
}

// lockExpired locks the vaults if the timeout passed (before handing out a
// secret: the timer may be late after a sleep).
func (s *Service) lockExpired() {
	now := s.now()
	a := &s.lock
	a.mu.Lock()
	expired := a.timeout > 0 && now.Sub(a.last) >= a.timeout
	idle := now.Sub(a.last)
	a.mu.Unlock()
	if expired && s.anyUnlocked() {
		s.LockAll(context.Background(), fmt.Errorf("%w after %s without input", errAutoLocked, idle.Round(time.Minute)))
	}
}

func (s *Service) anyUnlocked() bool {
	for _, v := range s.Vaults() {
		if v.State == Online || v.State == Syncing {
			return true
		}
	}
	return false
}

// errAutoLocked is the reason shown in the unlock dialog.
var errAutoLocked = errors.New("locked by Vaultcrab")

// LockAll locks every unlocked vault (bw /lock: the session key is dropped
// too) with reason shown in the unlock dialog.
func (s *Service) LockAll(ctx context.Context, reason error) {
	for _, v := range s.vaultList() {
		v.lockNow(ctx, reason)
	}
}

// runAutoLock locks the vaults when the timeout passes without input.
func (s *Service) runAutoLock(ctx context.Context) {
	a := &s.lock
	var t *time.Timer
	for {
		a.mu.Lock()
		timeout, last := a.timeout, a.last
		a.mu.Unlock()
		var due <-chan time.Time
		if timeout > 0 && s.anyUnlocked() {
			d := last.Add(timeout).Sub(s.now())
			if d < 0 {
				d = 0
			}
			if t == nil {
				t = time.NewTimer(d)
			} else {
				t.Reset(d)
			}
			due = t.C
		}
		select {
		case <-ctx.Done():
			if t != nil {
				t.Stop()
			}
			return
		case <-a.wake:
		case <-due:
			s.lockExpired()
		}
	}
}

// startScreenWatch starts the screen lock watcher (once; its events are
// ignored while lock_on_screen_lock is off).
func (s *Service) startScreenWatch() {
	a := &s.lock
	s.mu.Lock()
	ctx := s.ctx
	s.mu.Unlock()
	if ctx == nil {
		return // Start does it
	}
	if s.opts.ScreenLock == nil {
		a.mu.Lock()
		a.watchErr = ErrScreenLockUnsupported
		a.mu.Unlock()
		logf("auto-lock: %v", ErrScreenLockUnsupported)
		return
	}
	a.mu.Lock()
	a.watching = true
	a.mu.Unlock()
	err := s.opts.ScreenLock(ctx, func() {
		a.mu.Lock()
		on := a.enabled
		a.mu.Unlock()
		if on && s.anyUnlocked() {
			logf("auto-lock: the screen was locked")
			s.LockAll(context.Background(), fmt.Errorf("%w when the screen was locked", errAutoLocked))
		}
	})
	a.mu.Lock()
	if err != nil {
		a.watching, a.watchErr = false, err
	}
	a.mu.Unlock()
	if err != nil {
		logf("auto-lock: screen locks are not detected: %v", err)
	}
	s.emit()
}
