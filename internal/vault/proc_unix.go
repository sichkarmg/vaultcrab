// SPDX-License-Identifier: MIT

//go:build !windows && !linux

package vault

import (
	"os/exec"
	"syscall"
)

// setProcAttrs starts bw serve in its own session without a controlling
// terminal: terminal-generated signals do not reach it, and terminals that
// show the processes of the tty in the window title (macOS Terminal:
// "vaultcrab ▸ node") do not list it.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// afterStart is a no-op here; vaultcrab stops bw serve on exit and on
// SIGHUP/SIGINT/SIGTERM (see cmd/vaultcrab).
func afterStart(*exec.Cmd) {}
