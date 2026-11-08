// SPDX-License-Identifier: MIT

// Package keystore keeps small secrets in the secure store of the system:
// the macOS Keychain (Security framework, needs cgo), the Windows Credential
// Manager (DPAPI-protected, per user) and the Linux Secret Service (GNOME
// Keyring, KWallet, KeePassXC… over D-Bus). Secrets are handed to the store
// through its API only: never on a command line, in the environment or in a
// file of Vaultcrab.
//
// Items are named "vaultcrab/<name>" (service "vaultcrab", account <name>).
package keystore

import "errors"

// ErrNotFound means there is no item of that name.
var ErrNotFound = errors.New("keystore: no such item")

// ErrUnsupported means there is no secure store here (e.g. no Secret
// Service on Linux, a macOS build without cgo).
var ErrUnsupported = errors.New("keystore: no secure store of the system is available")

// Store is a secure store of named secrets.
type Store interface {
	// Get returns a copy of the item (the caller wipes it).
	Get(name string) ([]byte, error)
	// Set creates or replaces the item.
	Set(name string, data []byte) error
	// Delete removes the item (no error if it does not exist).
	Delete(name string) error
	// Name describes the store ("macOS Keychain", …).
	Name() string
}

// Open returns the secure store of this system.
func Open() (Store, error) { return open() }

const service = "vaultcrab"

// Memory is a Store in memory (tests).
type Memory struct{ Items map[string][]byte }

func (m *Memory) Get(name string) ([]byte, error) {
	v, ok := m.Items[name]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), v...), nil
}

func (m *Memory) Set(name string, data []byte) error {
	if m.Items == nil {
		m.Items = map[string][]byte{}
	}
	m.Items[name] = append([]byte(nil), data...)
	return nil
}

func (m *Memory) Delete(name string) error { delete(m.Items, name); return nil }
func (m *Memory) Name() string             { return "memory" }
