// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sichkarmg/vaultcrab/internal/biometric"
	"github.com/sichkarmg/vaultcrab/internal/quickunlock"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Touch ID (unlock.touch_id, macOS): in front of the PIN and the master
// password. The stored master password is read from the Keychain only
// after Touch ID succeeded; canceled, failed, unavailable or changed
// fingerprints fall back to the PIN, then the master password.

// Errors of Touch ID, for the UI.
var (
	ErrTouchCanceled    = biometric.ErrCanceled
	ErrTouchUnsupported = biometric.ErrUnsupported
	ErrBiometryChanged  = quickunlock.ErrBiometryChanged
)

func (s *Service) touchOn() bool {
	s.lock.mu.Lock()
	defer s.lock.mu.Unlock()
	return s.lock.unlock.TouchID
}

// TouchIDErr says why Touch ID cannot be used here (nil if it can).
func (s *Service) TouchIDErr() error {
	if s.opts.Biometric == nil {
		return ErrTouchUnsupported
	}
	if err := s.opts.Biometric.Available(); err != nil {
		return err
	}
	return s.KeystoreErr()
}

// touchReady: nil when vault v can be unlocked with Touch ID (before the
// prompt).
func (s *Service) touchReady(v *vaultConn) ([]byte, error) {
	m, ok := s.quick()
	if !s.touchOn() || !ok || s.opts.Biometric == nil {
		return nil, ErrTouchUnsupported
	}
	if err := s.opts.Biometric.Available(); err != nil {
		return nil, err
	}
	domain, err := s.opts.Biometric.DomainState()
	if err != nil {
		return nil, err
	}
	_, _, pol := s.unlockPolicy()
	if err := m.TouchReady(v.key(), v.accountKey(), domain, pol); err != nil {
		if errors.Is(err, quickunlock.ErrBiometryChanged) {
			logf("Touch ID: the enrolled fingerprints changed; Touch ID data deleted")
		}
		return nil, err
	}
	return domain, nil
}

// UnlockWithTouchID asks for Touch ID and unlocks vault id with the stored
// master password. Errors: ErrTouchCanceled, a failed or unavailable Touch
// ID, ErrBiometryChanged, ErrMasterRequired, or why bw refused.
func (s *Service) UnlockWithTouchID(ctx context.Context, id string) error {
	v := s.vault(id)
	if v == nil {
		return fmt.Errorf("no vault %q", id)
	}
	domain, err := s.touchReady(v)
	if err != nil {
		return err
	}
	if err := s.opts.Biometric.Authenticate(ctx, "unlock the vault "+s.vaultLabel(v)); err != nil {
		logf("%s: Touch ID: %v", v.who(), err)
		return err
	}
	m, _ := s.quick()
	_, _, pol := s.unlockPolicy()
	pw, err := m.OpenTouch(v.key(), v.accountKey(), domain, pol)
	if err != nil {
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
		_ = m.DropTouch(v.key())
		return errStoredPWChanged
	}
	logf("%s: unlocked with Touch ID", v.who())
	s.touch()
	v.refresh(ctx)
	return nil
}

func (s *Service) vaultLabel(v *vaultConn) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return v.info().Label()
}

// EnableTouchID checks the master password of vault id with bw, asks for
// Touch ID once and stores the password for it. password is wiped.
func (s *Service) EnableTouchID(ctx context.Context, id string, password []byte) error {
	defer secret.Zero(password)
	if err := s.TouchIDErr(); err != nil {
		return err
	}
	v := s.vault(id)
	if v == nil {
		return fmt.Errorf("no vault %q", id)
	}
	src := v.source()
	if src == nil {
		return v.notOnline()
	}
	pw := slices.Clone(password)
	defer secret.Zero(pw)
	if err := src.Unlock(ctx, password); err != nil {
		return fmt.Errorf("master password: %w", err)
	}
	if err := s.opts.Biometric.Authenticate(ctx, "turn on Touch ID for Vaultcrab"); err != nil {
		return err
	}
	domain, err := s.opts.Biometric.DomainState()
	if err != nil {
		return err
	}
	m, _ := s.quick()
	_, _, pol := s.unlockPolicy()
	if err := m.SealTouch(v.key(), v.accountKey(), pw, domain, pol); err != nil {
		return err
	}
	logf("Touch ID on; %s stored in %s", v.who(), s.KeystoreName())
	return nil
}

// ForgetTouchID deletes the Touch ID data of every vault.
func (s *Service) ForgetTouchID() error {
	m, ok := s.quick()
	if !ok {
		return nil
	}
	var keys []string
	for _, v := range s.vaultList() {
		keys = append(keys, v.key())
	}
	return m.ForgetTouch(keys...)
}

// sealTouchAfterUnlock renews the Touch ID copy after a master password
// unlock (password is not wiped).
func (v *vaultConn) sealTouchAfterUnlock(password []byte) {
	s := v.s
	m, ok := s.quick()
	if !s.touchOn() || !ok || s.opts.Biometric == nil || s.opts.Biometric.Available() != nil {
		return
	}
	domain, err := s.opts.Biometric.DomainState()
	if err != nil {
		return
	}
	_, _, pol := s.unlockPolicy()
	if err := m.SealTouch(v.key(), v.accountKey(), password, domain, pol); err != nil {
		logf("%s: storing the master password for Touch ID: %v", v.who(), err)
	}
}
