// SPDX-License-Identifier: MIT

package quickunlock

import (
	"strconv"

	"golang.org/x/sys/unix"
)

// BootID identifies the current boot of the system: the boot time
// (kern.boottime), "" if unknown.
func BootID() string {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return ""
	}
	return strconv.FormatInt(tv.Sec, 10)
}
