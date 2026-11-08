// SPDX-License-Identifier: MIT

//go:build !linux && !darwin && !windows

package quickunlock

// BootID is unknown here: the after-reboot rule does not apply.
func BootID() string { return "" }
