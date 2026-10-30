// SPDX-License-Identifier: MIT

//go:build !windows

package widget

import (
	"os/exec"
	"syscall"
)

// hideWindow starts the command in its own session, without a controlling
// terminal: otherwise it belongs to the terminal's foreground process group
// and terminals (and tmux) show its name (bash, date, awk...) as the window
// title every time a widget refreshes.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
