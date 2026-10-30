// SPDX-License-Identifier: MIT

package connect

import "os/exec"

// setCtty is not needed with ConPTY.
func setCtty(*exec.Cmd) {}
