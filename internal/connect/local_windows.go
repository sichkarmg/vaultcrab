// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"os/exec"
)

// setCtty is not needed with ConPTY.
func setCtty(*exec.Cmd) {}

// Exec is not available for the local shell on Windows (no POSIX shell).
func (c *LocalConn) Exec(context.Context, string) ([]byte, error) { return nil, ErrNoExec }
