// SPDX-License-Identifier: MIT

//go:build !windows

package connect

import (
	"os/exec"
	"syscall"
)

// detach starts the client in its own session so it survives vaultcrab and
// never receives terminal signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
