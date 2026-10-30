// SPDX-License-Identifier: MIT

package vault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// ServeOptions configures a managed `bw serve` process.
type ServeOptions struct {
	BW      string   // path to bw executable ("bw" if empty)
	Port    int      // 0 = pick a free port
	DataDir string   // BITWARDENCLI_APPDATA_DIR, empty = bw default
	Env     []string // extra environment (e.g. NODE_EXTRA_CA_CERTS=...); never secrets
	Spawn   Spawner  // optional: start bw serve outside vaultcrab's process tree
}

// Process is bw serve started by a Spawner.
type Process interface {
	Done() <-chan struct{}
	Stderr() []byte // available after Done
	Kill()
}

// Spawner starts bw serve outside vaultcrab's process tree (spawn.Client);
// env is added to the spawner's environment and never holds secrets.
type Spawner interface {
	Start(argv, env []string) (Process, error)
}

// Server is a bw serve process bound to 127.0.0.1.
type Server struct {
	URL string

	mu     sync.Mutex
	cmd    *exec.Cmd // started directly
	proc   Process   // or by a Spawner
	done   chan struct{}
	stderr bytes.Buffer // first 64 KiB of bw diagnostics
}

// Write collects bw stderr (bounded).
func (s *Server) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if room := 64<<10 - s.stderr.Len(); room > 0 {
		s.stderr.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// StartServer launches bw serve and waits until it answers /status.
func StartServer(ctx context.Context, o ServeOptions) (*Server, error) {
	bw := o.BW
	if bw == "" {
		bw = "bw"
	}
	path, err := exec.LookPath(bw)
	if err != nil {
		return nil, fmt.Errorf("bw CLI not found (%s): %w", bw, err)
	}
	port := o.Port
	if port == 0 {
		if port, err = freePort(); err != nil {
			return nil, err
		}
	}
	s := &Server{URL: "http://127.0.0.1:" + strconv.Itoa(port), done: make(chan struct{})}
	argv := []string{path, "serve", "--hostname", "127.0.0.1", "--port", strconv.Itoa(port)}
	env := []string{"NODE_NO_WARNINGS=1", "BW_NOINTERACTION=true"}
	if o.DataDir != "" {
		env = append(env, "BITWARDENCLI_APPDATA_DIR="+o.DataDir)
	}
	env = append(env, o.Env...)
	if o.Spawn != nil {
		p, err := o.Spawn.Start(argv, env)
		if err != nil {
			return nil, fmt.Errorf("start bw serve: %w", err)
		}
		s.proc = p
		go func() {
			<-p.Done()
			_, _ = s.Write(p.Stderr())
			close(s.done)
		}()
	} else {
		// Not CommandContext: the server must outlive the startup context.
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = append(os.Environ(), env...)
		cmd.Stdout = nil
		cmd.Stderr = s
		setProcAttrs(cmd)
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("start bw serve: %w", err)
		}
		afterStart(cmd)
		s.cmd = cmd
		go func() {
			_ = cmd.Wait()
			close(s.done)
		}()
	}

	c := NewClient(s.URL)
	c.HTTP.Timeout = 2 * time.Second
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return nil, ctx.Err()
		case <-deadline.C:
			s.Stop()
			return nil, errors.New("bw serve did not start in 60s")
		case <-s.done:
			msg := bytes.TrimSpace(s.stderrBytes())
			if bytes.Contains(bytes.ToLower(msg), []byte("not logged in")) {
				// newer bw refuses to serve without a logged in account
				return nil, ErrNotLoggedIn
			}
			return nil, fmt.Errorf("bw serve exited: %s", msg)
		case <-tick.C:
			if _, err := c.Status(ctx); err == nil {
				return s, nil
			}
		}
	}
}

func (s *Server) stderrBytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stderr.Bytes()
}

// Done is closed when the process exits.
func (s *Server) Done() <-chan struct{} { return s.done }

// Stop terminates the server.
func (s *Server) Stop() {
	if s == nil || s.proc == nil && (s.cmd == nil || s.cmd.Process == nil) {
		return
	}
	select {
	case <-s.done:
		return
	default:
	}
	if s.proc != nil {
		s.proc.Kill()
	} else {
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
