// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// errSharingViolation: the file is being replaced (a rename over it by a
// save of this or another vaultcrab, an editor) at this very moment.
const errSharingViolation syscall.Errno = 32

// readFile reads the config, waiting out a replace of the file under way.
func readFile(path string) ([]byte, error) {
	b, err := readShared(path)
	for wait := time.Duration(0); errors.Is(err, errSharingViolation) && wait < 2*time.Second; wait += 10 * time.Millisecond {
		time.Sleep(10 * time.Millisecond)
		b, err = readShared(path)
	}
	return b, err
}

// readShared is os.ReadFile, but the file is opened with FILE_SHARE_DELETE
// too: os.Open leaves it out, so while any vaultcrab reads the config, a save
// (a rename over it) fails with "Access is denied". With it the rename
// succeeds and this reader keeps the old, complete contents.
func readShared(path string) ([]byte, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()
	return io.ReadAll(f)
}
