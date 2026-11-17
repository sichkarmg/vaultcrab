// SPDX-License-Identifier: MIT

package connect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// maxExecOutput bounds the output of Exec (a directory listing).
const maxExecOutput = 1 << 20

// Exec runs a command over the connection in a channel of its own, next to
// the shell (which it does not touch), and returns its standard output.
// The command runs in the login directory of the user, under the login
// shell of the account.
func (c *Conn) Exec(ctx context.Context, cmd string) ([]byte, error) {
	sess, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open a channel: %w", err)
	}
	defer sess.Close()
	var out, errb limitedBuf
	out.max, errb.max = maxExecOutput, 4096
	sess.Stdout, sess.Stderr = &out, &errb
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Close()
		return nil, ctx.Err()
	}
	if err != nil {
		if msg := bytes.TrimSpace(errb.Bytes()); len(msg) > 0 {
			return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

// limitedBuf keeps the first max bytes written to it.
type limitedBuf struct {
	bytes.Buffer
	max int
}

func (b *limitedBuf) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// ErrNoExec: the connection cannot run commands of its own.
var ErrNoExec = errors.New("this session cannot run commands")
