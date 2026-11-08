// SPDX-License-Identifier: MIT

package quickunlock

import (
	"os"
	"strings"
)

// BootID identifies the current boot of the system ("" if unknown).
func BootID() string {
	b, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
