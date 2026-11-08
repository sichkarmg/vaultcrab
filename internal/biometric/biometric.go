// SPDX-License-Identifier: MIT

// Package biometric asks for Touch ID on macOS (LocalAuthentication,
// LAPolicyDeviceOwnerAuthenticationWithBiometrics) before Vaultcrab reads
// a stored master password from the Keychain (unlock.touch_id).
//
// This is the simple variant: Vaultcrab is not signed with an Apple
// Developer ID, so its Keychain items cannot carry a biometric access
// control of the Data Protection Keychain; Touch ID is asked by Vaultcrab
// itself before it reads the item. A process of the same user that reads
// the login keychain item directly (macOS asks the user to allow that
// unless it is Vaultcrab's own binary) skips Touch ID.
//
// Other systems: Unsupported. macOS without cgo: Unsupported.
package biometric

import (
	"context"
	"errors"
)

var (
	// ErrUnsupported: no Touch ID here (not macOS, a build without cgo).
	ErrUnsupported = errors.New("Touch ID is available on macOS only (in builds with cgo, like the releases)")
	// ErrUnavailable: the Mac has no Touch ID, or no finger is enrolled.
	ErrUnavailable = errors.New("Touch ID is not available or no fingerprint is enrolled")
	// ErrCanceled: the user canceled the prompt (or chose the password).
	ErrCanceled = errors.New("Touch ID was canceled")
	// ErrTimeout: nobody touched the sensor in time (e.g. the prompt
	// appeared while the Mac was asleep).
	ErrTimeout = errors.New("Touch ID timed out: nobody touched the sensor")
	// ErrFailed: the fingerprint was not recognized.
	ErrFailed = errors.New("Touch ID did not recognize the fingerprint")
	// ErrLockout: too many failed attempts; macOS wants the login password.
	ErrLockout = errors.New("Touch ID is locked after too many attempts (unlock the Mac with its password)")
)

// Authenticator is Touch ID (or a fake in tests).
type Authenticator interface {
	// Available reports whether Touch ID can be used now.
	Available() error
	// DomainState identifies the enrolled fingerprints: it changes when
	// one is added or removed.
	DomainState() ([]byte, error)
	// Authenticate shows the Touch ID prompt with reason and waits.
	Authenticate(ctx context.Context, reason string) error
}

// Default is Touch ID of this system.
var Default Authenticator = system{}
