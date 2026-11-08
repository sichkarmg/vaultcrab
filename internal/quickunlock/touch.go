// SPDX-License-Identifier: MIT

package quickunlock

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"

	"github.com/sichkarmg/vaultcrab/internal/keystore"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Touch ID (macOS): the master password of a vault is kept in the Keychain
// item "vaultcrab/touchid/<key>" and read only after Touch ID succeeded
// (internal/biometric; the simple variant without a signed app, see there).
// The item records a hash of the enrolled fingerprints: when they change,
// the item is deleted (an added finger must not unlock). The reboot and
// N-days rules of the PIN apply too.

type touchItem struct {
	V          int    `json:"v"`
	PW         []byte `json:"pw"`
	Account    string `json:"account"`
	VerifiedAt int64  `json:"verified"` // unix seconds
	Boot       string `json:"boot"`
	Domain     []byte `json:"domain"` // sha256 of the domain state
}

type touchIndex struct {
	Vaults []string `json:"vaults"`
}

const (
	touchPrefix = "touchid/"
	touchIndexN = "touchid"
)

// ErrBiometryChanged: the enrolled fingerprints changed; the Touch ID data
// was deleted.
var ErrBiometryChanged = errors.New("the enrolled fingerprints changed: the Touch ID data was deleted; use the PIN or the master password")

func domainHash(d []byte) []byte { h := sha256.Sum256(d); return h[:] }

// SealTouch stores the master password of vault key for Touch ID (after bw
// accepted it). password is not wiped.
func (m Manager) SealTouch(key, account string, password, domain []byte, pol Policy) error {
	it := touchItem{V: 1, PW: password, Account: account, VerifiedAt: pol.now().Unix(), Boot: pol.Boot, Domain: domainHash(domain)}
	if err := m.save(touchPrefix+key, it); err != nil {
		return err
	}
	var idx touchIndex
	_ = m.load(touchIndexN, &idx)
	if !slices.Contains(idx.Vaults, key) {
		idx.Vaults = append(idx.Vaults, key)
		return m.save(touchIndexN, idx)
	}
	return nil
}

// TouchReady says whether vault key can be unlocked with Touch ID now
// (before the prompt): nil, ErrNotSealed, ErrBiometryChanged (the data was
// deleted) or ErrMasterRequired with the reason.
func (m Manager) TouchReady(key, account string, domain []byte, pol Policy) error {
	var it touchItem
	if err := m.load(touchPrefix+key, &it); err != nil {
		if errors.Is(err, keystore.ErrNotFound) {
			return ErrNotSealed
		}
		return err
	}
	defer secret.Zero(it.PW)
	switch {
	case subtle.ConstantTimeCompare(it.Domain, domainHash(domain)) != 1:
		_ = m.ForgetTouch()
		return ErrBiometryChanged
	case account != "" && it.Account != "" && it.Account != account:
		_ = m.DropTouch(key)
		return fmt.Errorf("%w: the vault is logged in to another account", ErrMasterRequired)
	case pol.AfterReboot && pol.Boot != "" && it.Boot != pol.Boot:
		return fmt.Errorf("%w after a restart of the computer", ErrMasterRequired)
	case pol.now().Unix()-it.VerifiedAt > int64(pol.maxAge().Seconds()):
		return fmt.Errorf("%w every %d days", ErrMasterRequired, int(pol.maxAge().Hours()/24))
	}
	return nil
}

// OpenTouch returns the stored master password of vault key: call it only
// after Touch ID succeeded. The caller wipes it.
func (m Manager) OpenTouch(key, account string, domain []byte, pol Policy) ([]byte, error) {
	if err := m.TouchReady(key, account, domain, pol); err != nil {
		return nil, err
	}
	var it touchItem
	if err := m.load(touchPrefix+key, &it); err != nil {
		return nil, err
	}
	return it.PW, nil
}

// HasTouch reports whether vault key has Touch ID data.
func (m Manager) HasTouch(key string) bool {
	var idx touchIndex
	return m.load(touchIndexN, &idx) == nil && slices.Contains(idx.Vaults, key)
}

// DropTouch deletes the Touch ID data of vault key.
func (m Manager) DropTouch(key string) error {
	if err := m.Store.Delete(touchPrefix + key); err != nil {
		return err
	}
	var idx touchIndex
	if m.load(touchIndexN, &idx) == nil && slices.Contains(idx.Vaults, key) {
		idx.Vaults = slices.DeleteFunc(idx.Vaults, func(s string) bool { return s == key })
		return m.save(touchIndexN, idx)
	}
	return nil
}

// ForgetTouch deletes all Touch ID data (extra: further vault keys).
func (m Manager) ForgetTouch(extra ...string) error {
	var idx touchIndex
	var errs []error
	_ = m.load(touchIndexN, &idx)
	for _, k := range append(idx.Vaults, extra...) {
		errs = append(errs, m.Store.Delete(touchPrefix+k))
	}
	errs = append(errs, m.Store.Delete(touchIndexN))
	return errors.Join(errs...)
}
