// SPDX-License-Identifier: MIT

package widget

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps console programs (powershell, cmd) from opening a window
// or touching vaultcrab's console.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
