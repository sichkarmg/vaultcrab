// SPDX-License-Identifier: MIT

//go:build !windows

package connect

import (
	"os/exec"
	"syscall"
)

// setCtty makes the pseudo-terminal (the child's stdin) the controlling
// terminal of a new session, so that job control and Ctrl+C work.
func setCtty(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
}
