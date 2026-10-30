// SPDX-License-Identifier: MIT

//go:build linux

package vault

import (
	"os/exec"
	"syscall"
)

// setProcAttrs starts bw serve in its own session without a controlling
// terminal (no terminal signals, not shown in window titles) and asks the
// kernel to kill it if vaultcrab dies without cleaning up (so an unlocked vault
// API never outlives the UI).
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
}

func afterStart(*exec.Cmd) {}
