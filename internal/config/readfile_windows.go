// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// errSharingViolation: the file is being replaced (a rename over it by a
// save of this or another vaultcrab, an editor) at this very moment.
const errSharingViolation syscall.Errno = 32

// readFile reads the config, waiting out a replace of the file under way.
func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	for wait := time.Duration(0); errors.Is(err, errSharingViolation) && wait < 2*time.Second; wait += 10 * time.Millisecond {
		time.Sleep(10 * time.Millisecond)
		b, err = os.ReadFile(path)
	}
	return b, err
}
