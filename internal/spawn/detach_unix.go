// SPDX-License-Identifier: MIT

//go:build !windows

package spawn

import (
	"os/exec"
	"syscall"
)

// detachAttrs starts the helper in its own session, without a controlling
// terminal.
func detachAttrs(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// groupAttrs puts a started program into its own process group, so that
// killGroup also stops its children.
func groupAttrs(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
