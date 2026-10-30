// SPDX-License-Identifier: MIT

package spawn

import "os/exec"

// detachAttrs: the helper is not used on Windows (no process names in the
// console title); kept for building.
func detachAttrs(*exec.Cmd) {}

func groupAttrs(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
