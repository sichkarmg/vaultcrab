// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/xpty"
)

// LocalOptions configures a local shell.
type LocalOptions struct {
	Command []string // program and arguments; empty = DefaultShell()
	Term    string   // TERM for Unix shells (default xterm-256color)
	Dir     string   // working directory (default: the user's home)
}

// DefaultShell returns the user's shell: $SHELL (or /bin/sh) on Unix;
// PowerShell 7, Windows PowerShell or %ComSpec% on Windows.
func DefaultShell() []string {
	if runtime.GOOS == "windows" {
		for _, sh := range []string{"pwsh.exe", "powershell.exe"} {
			if p, err := exec.LookPath(sh); err == nil {
				return []string{p, "-NoLogo"}
			}
		}
		if cs := os.Getenv("ComSpec"); cs != "" {
			return []string{cs}
		}
		return []string{"cmd.exe"}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh}
	}
	return []string{"/bin/sh"}
}

// LocalConn is a local program (normally a shell) running on a
// pseudo-terminal: a Unix PTY or a Windows ConPTY. It implements the same
// interface as an SSH connection.
type LocalConn struct {
	pty xpty.Pty
	cmd *exec.Cmd

	readEOF atomic.Bool
	once    sync.Once
	done    chan struct{}
	err     error
}

// StartLocal starts the program on a new cols×rows pseudo-terminal.
func StartLocal(ctx context.Context, cols, rows int, o LocalOptions) (*LocalConn, error) {
	argv := o.Command
	if len(argv) == 0 {
		argv = DefaultShell()
	}
	if o.Term == "" {
		o.Term = "xterm-256color"
	}
	p, err := xpty.NewPty(max(cols, 1), max(rows, 1))
	if err != nil {
		return nil, fmt.Errorf("pseudo-terminal: %w", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	if runtime.GOOS != "windows" {
		cmd.Env = append(cmd.Env, "TERM="+o.Term, "COLORTERM=truecolor")
	}
	setCtty(cmd)
	cmd.Dir = o.Dir
	if cmd.Dir == "" {
		cmd.Dir, _ = os.UserHomeDir()
	}
	if cmd.Err != nil { // program not found
		p.Close()
		return nil, cmd.Err
	}
	if err := p.Start(cmd); err != nil {
		p.Close()
		return nil, fmt.Errorf("start %s: %w", argv[0], err)
	}
	if u, ok := p.(*xpty.UnixPty); ok {
		// The child has its copy; without ours the master reads EIO as soon
		// as the program exits and its output is consumed.
		_ = u.Slave().Close()
	}
	c := &LocalConn{pty: p, cmd: cmd, done: make(chan struct{})}
	go c.wait()
	return c, nil
}

func (c *LocalConn) wait() {
	err := xpty.WaitProcess(context.Background(), c.cmd)
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		err = fmt.Errorf("shell exited with status %d", ee.ExitCode())
	}
	// Let the reader drain the remaining output (Unix returns EIO once it
	// is consumed); ConPTY never ends the stream by itself.
	for i := 0; i < 50 && !c.readEOF.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	c.finish(err)
}

func (c *LocalConn) finish(err error) {
	c.once.Do(func() {
		c.err = err
		_ = c.pty.Close()
		close(c.done)
	})
}

// Read reads the program output.
func (c *LocalConn) Read(p []byte) (int, error) {
	n, err := c.pty.Read(p)
	if err != nil {
		c.readEOF.Store(true)
	}
	return n, err
}

// Write sends input to the program.
func (c *LocalConn) Write(p []byte) (int, error) { return c.pty.Write(p) }

// Resize changes the pseudo-terminal size.
func (c *LocalConn) Resize(cols, rows int) error { return c.pty.Resize(max(cols, 1), max(rows, 1)) }

// Done is closed when the program has exited; Err then reports why (nil =
// exit status 0).
func (c *LocalConn) Done() <-chan struct{} { return c.done }

// Err returns the reason the program ended.
func (c *LocalConn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close kills the program and releases the pseudo-terminal.
func (c *LocalConn) Close() error {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	c.finish(nil)
	return nil
}
