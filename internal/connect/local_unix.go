// SPDX-License-Identifier: MIT

//go:build !windows

package connect

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// setCtty makes the pseudo-terminal (the child's stdin) the controlling
// terminal of a new session, so that job control and Ctrl+C work.
func setCtty(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
}

// Exec runs a command with /bin/sh in the home directory, as Conn.Exec
// does on a server.
func (c *LocalConn) Exec(ctx context.Context, cmd string) ([]byte, error) {
	sh := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	if home, err := os.UserHomeDir(); err == nil {
		sh.Dir = home
	}
	var out, errb limitedBuf
	out.max, errb.max = maxExecOutput, 4096
	sh.Stdout, sh.Stderr = &out, &errb
	if err := sh.Run(); err != nil {
		if msg := bytes.TrimSpace(errb.Bytes()); len(msg) > 0 {
			return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}
