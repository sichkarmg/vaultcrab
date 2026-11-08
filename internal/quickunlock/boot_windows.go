// SPDX-License-Identifier: MIT

package quickunlock

import (
	"strconv"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// BootID identifies the current boot of the system: the boot counter of
// Windows (PrefetchParameters\BootId), else the boot time to 10 minutes.
func BootID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if n, _, err := k.GetIntegerValue("BootId"); err == nil {
			return "boot-" + strconv.FormatUint(n, 10)
		}
	}
	up := time.Duration(windows.DurationSinceBoot())
	return strconv.FormatInt(time.Now().Add(-up).Truncate(10*time.Minute).Unix(), 10)
}
