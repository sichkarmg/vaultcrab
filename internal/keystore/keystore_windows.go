// SPDX-License-Identifier: MIT

package keystore

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows Credential Manager: generic credentials of the user, encrypted by
// the system with DPAPI; persisted on this machine only (not roaming).

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	maxBlob                 = 5 * 512
)

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type credStore struct{}

func open() (Store, error) { return credStore{}, nil }

func (credStore) Name() string { return "Windows Credential Manager" }

func target(name string) (*uint16, error) { return windows.UTF16PtrFromString(service + "/" + name) }

func (credStore) Set(name string, data []byte) error {
	if len(data) > maxBlob {
		return fmt.Errorf("keystore: item too large (%d bytes)", len(data))
	}
	t, err := target(name)
	if err != nil {
		return err
	}
	user, _ := windows.UTF16PtrFromString(service)
	c := credential{Type: credTypeGeneric, TargetName: t, Persist: credPersistLocalMachine,
		CredentialBlobSize: uint32(len(data)), UserName: user}
	if len(data) > 0 {
		c.CredentialBlob = &data[0]
	}
	if r, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return fmt.Errorf("keystore: CredWrite: %w", err)
	}
	return nil
}

func (credStore) Get(name string) ([]byte, error) {
	t, err := target(name)
	if err != nil {
		return nil, err
	}
	var p *credential
	if r, _, err := procCredReadW.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&p))); r == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("keystore: CredRead: %w", err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(p)))
	blob := unsafe.Slice(p.CredentialBlob, p.CredentialBlobSize)
	out := append([]byte(nil), blob...)
	for i := range blob {
		blob[i] = 0
	}
	return out, nil
}

func (credStore) Delete(name string) error {
	t, err := target(name)
	if err != nil {
		return err
	}
	if r, _, err := procCredDelete.Call(uintptr(unsafe.Pointer(t)), credTypeGeneric, 0); r == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("keystore: CredDelete: %w", err)
	}
	return nil
}
