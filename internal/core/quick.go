// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/quickunlock"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Quick unlock with a PIN (unlock.pin, Settings → Security): the master
// passwords of the vaults are kept in the secure store of the system
// (Options.Keystore), sealed so that only the PIN opens them (see
// internal/quickunlock). After every unlock with the master password it is
// stored again; a logout, another account or a master password bw no longer
// accepts deletes it.

// Errors of the quick unlock, for the UI.
var (
	ErrWrongPIN        = quickunlock.ErrWrongPIN
	ErrTooManyPINs     = quickunlock.ErrTooManyAttempts
	ErrMasterRequired  = quickunlock.ErrMasterRequired
	ErrWeakPIN         = quickunlock.ErrWeakPIN
	ErrNoKeystore      = errors.New("no secure store of the system is available")
	errStoredPWChanged = errors.New("bw did not accept the stored master password (changed?): it was deleted; use the master password")
)

// WrongPIN tells the attempts left after a wrong PIN.
type WrongPIN = quickunlock.WrongPIN

// QuickState says how a locked vault can be unlocked.
type QuickState struct {
	TouchID bool // Touch ID unlocks it now (try it first)
	// TouchWhy: why Touch ID does not, when it is on.
	TouchWhy string
	PIN      bool // the PIN unlocks it now
	// Why the PIN does not, when it is on (e.g. "the master password is
	// required after a restart of the computer"); "" otherwise.
	Why string
}

// KeystoreName names the secure store in use ("" = none).
func (s *Service) KeystoreName() string {
	if s.opts.Keystore == nil {
		return ""
	}
	return s.opts.Keystore.Name()
}

// KeystoreErr says why there is no secure store (nil if there is).
func (s *Service) KeystoreErr() error {
	if s.opts.Keystore != nil {
		return nil
	}
	if s.opts.KeystoreErr != nil {
		return s.opts.KeystoreErr
	}
	return ErrNoKeystore
}

func (s *Service) quick() (quickunlock.Manager, bool) {
	if s.opts.Keystore == nil {
		return quickunlock.Manager{}, false
	}
	return quickunlock.Manager{Store: s.opts.Keystore}, true
}

func (s *Service) unlockPolicy() (on bool, alnum bool, p quickunlock.Policy) {
	a := &s.lock
	a.mu.Lock()
	u := a.unlock
	a.mu.Unlock()
	boot := ""
	if s.opts.BootID != nil {
		boot = s.opts.BootID()
	}
	return u.PIN, u.PINAlphanumeric, quickunlock.Policy{
		MaxAttempts: u.PINAttempts,
		MaxAge:      time.Duration(u.MasterEveryDays) * 24 * time.Hour,
		AfterReboot: u.MasterAfterReboot,
		Boot:        boot,
		Now:         s.now(),
		KDF:         s.opts.PINKDF,
	}
}

// key names a vault in the secure store.
func (v *vaultConn) key() string {
	if v.opts.ID == "" {
		return "default"
	}
	return v.opts.ID
}

func (v *vaultConn) accountKey() string {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	if v.email == "" {
		return ""
	}
	return v.email + "@" + v.serverURL
}

// QuickUnlock says whether vault id ("" = default) can be unlocked with the
// PIN now.
func (s *Service) QuickUnlock(id string) QuickState {
	on, _, pol := s.unlockPolicy()
	m, ok := s.quick()
	v := s.vault(id)
	var q QuickState
	if !ok || v == nil {
		return q
	}
	if s.touchOn() {
		if _, err := s.touchReady(v); err == nil {
			q.TouchID = true
		} else {
			q.TouchWhy = err.Error()
		}
	}
	if !on {
		return q
	}
	switch err := m.Ready(v.key(), v.accountKey(), pol); {
	case err == nil:
		q.PIN = true
	case errors.Is(err, quickunlock.ErrNoPIN):
		q.Why = "no PIN is set (Settings → Security)"
	default:
		q.Why = err.Error()
	}
	return q
}

// HasPIN reports whether a PIN is set in the secure store.
func (s *Service) HasPIN() bool {
	m, ok := s.quick()
	return ok && m.HasPIN()
}

// UnlockWithPIN unlocks vault id with the PIN. pin is wiped. Errors:
// WrongPIN (attempts left), ErrTooManyPINs (everything deleted),
// ErrMasterRequired, or why bw refused.
func (s *Service) UnlockWithPIN(ctx context.Context, id string, pin []byte) error {
	defer secret.Zero(pin)
	m, ok := s.quick()
	v := s.vault(id)
	if !ok || v == nil {
		return ErrNoKeystore
	}
	_, _, pol := s.unlockPolicy()
	pw, err := m.Open(v.key(), v.accountKey(), pin, pol)
	if err != nil {
		logf("%s: PIN unlock refused: %v", v.who(), err)
		return err
	}
	src := v.source()
	if src == nil {
		secret.Zero(pw)
		return v.notOnline()
	}
	if err := src.Unlock(ctx, pw); err != nil { // wipes pw
		if errors.Is(err, ErrNotOnline) || ctx.Err() != nil {
			return err
		}
		logf("%s: the stored master password was not accepted: %v", v.who(), err)
		_ = m.Drop(v.key())
		return errStoredPWChanged
	}
	logf("%s: unlocked with the PIN", v.who())
	s.touch()
	v.refresh(ctx)
	return nil
}

// SetPIN sets (or changes) the PIN and stores the master password of vault
// id with it, after bw accepted the password (bw checks it without locking).
// Other vaults are stored at their next unlock with the master password.
// pin and password are wiped.
func (s *Service) SetPIN(ctx context.Context, id string, pin, password []byte) error {
	defer secret.Zero(pin)
	defer secret.Zero(password)
	m, ok := s.quick()
	v := s.vault(id)
	if !ok {
		return s.KeystoreErr()
	}
	if v == nil {
		return fmt.Errorf("no vault %q", id)
	}
	_, alnum, pol := s.unlockPolicy()
	if err := quickunlock.CheckPIN(pin, alnum); err != nil {
		return err
	}
	src := v.source()
	if src == nil {
		return v.notOnline()
	}
	pw := slices.Clone(password)
	defer secret.Zero(pw)
	if err := src.Unlock(ctx, password); err != nil { // checks it; wipes password
		return fmt.Errorf("master password: %w", err)
	}
	if err := m.SetPIN(slices.Clone(pin), alnum, pol); err != nil {
		return err
	}
	if err := m.Seal(v.key(), v.accountKey(), pw, pol); err != nil {
		return err
	}
	logf("PIN set in %s; %s stored", s.KeystoreName(), v.who())
	return nil
}

// CheckPIN verifies the current PIN (before changing it); a wrong PIN counts.
func (s *Service) CheckPIN(pin []byte) error {
	m, ok := s.quick()
	if !ok {
		secret.Zero(pin)
		return ErrNoKeystore
	}
	_, _, pol := s.unlockPolicy()
	return m.CheckPINOnly(pin, pol)
}

// ForgetPIN deletes the PIN and the stored master passwords (turning the
// PIN off, vaultcrab forget).
func (s *Service) ForgetPIN() error {
	m, ok := s.quick()
	if !ok {
		return nil
	}
	var extra []string
	for _, v := range s.vaultList() {
		extra = append(extra, "vault/"+v.key())
	}
	extra = append(extra, s.opts.ForgetExtra...)
	err := m.Forget(extra...)
	if s.touchOn() {
		err = errors.Join(err, s.ForgetTouchID())
	}
	logf("PIN and stored master passwords deleted from %s", s.KeystoreName())
	return err
}

// sealAfterUnlock stores the master password just accepted by bw, when the
// PIN is on (password is not wiped here).
func (v *vaultConn) sealAfterUnlock(password []byte) {
	s := v.s
	v.sealTouchAfterUnlock(password)
	on, _, pol := s.unlockPolicy()
	m, ok := s.quick()
	if !on || !ok || !m.HasPIN() {
		return
	}
	if err := m.Seal(v.key(), v.accountKey(), password, pol); err != nil {
		logf("%s: storing the master password for the PIN: %v", v.who(), err)
	}
}

// dropSealed deletes the stored master password (a logout).
func (v *vaultConn) dropSealed(why string) {
	m, ok := v.s.quick()
	if !ok {
		return
	}
	if slices.Contains(m.Vaults(), v.key()) {
		_ = m.Drop(v.key())
		logf("%s: stored master password deleted (%s)", v.who(), why)
	}
	if m.HasTouch(v.key()) {
		_ = m.DropTouch(v.key())
		logf("%s: Touch ID data deleted (%s)", v.who(), why)
	}
}

// touch records an unlock as activity (the auto-lock counts from it).
func (s *Service) touch() {
	s.lock.mu.Lock()
	s.lock.last = s.now()
	s.lock.mu.Unlock()
}
