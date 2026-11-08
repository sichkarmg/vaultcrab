// SPDX-License-Identifier: MIT

// Package spawn runs programs outside vaultcrab's process tree.
//
// macOS Terminal (and other terminals that walk the process tree of the
// foreground program) put the names of a program's children into the window
// title and update it on every fork: periodic widget commands (bash, awk,
// date) and bw serve (node) made the title flicker. A helper process — vaultcrab
// itself started in helper mode — detaches (double fork: its parent becomes
// launchd/init) and starts the programs on request over a pipe, so they are
// never descendants of vaultcrab.
//
// The helper exits, killing what it started, when the pipe from vaultcrab
// closes — also when vaultcrab is killed with SIGKILL. Requests carry command
// lines and environment over a private pipe; never passwords. The one secret
// is the BW_SESSION of a bw serve restarted after a crash (vault/session.go),
// which the helper does not keep or log.
package spawn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Helper-mode arguments (argv[1] of the vaultcrab executable).
const (
	argDetach = "__spawn-detach" // first stage: start the helper and exit
	argServe  = "__spawn-serve"  // the helper
)

// maxOutput bounds the output returned by Run and the kept stderr of Start.
const maxOutput = 64 << 10

type request struct {
	ID      int64    `json:"id"`
	Op      string   `json:"op"` // run | start | kill
	Argv    []string `json:"argv,omitempty"`
	Env     []string `json:"env,omitempty"` // added to the helper's environment
	Timeout int64    `json:"timeoutMs,omitempty"`
}

type response struct {
	ID     int64  `json:"id"`
	Out    []byte `json:"out,omitempty"` // run: combined output; start (on exit): stderr
	Err    string `json:"err,omitempty"`
	Pid    int    `json:"pid,omitempty"`
	Exited bool   `json:"exited,omitempty"` // start: the process ended
}

// Main runs the helper modes; it returns false for ordinary invocations.
// Call it first in main (and in TestMain of tests that start a Client).
func Main() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case argDetach:
		// Second fork: the helper inherits our stdin/stdout pipes; we exit
		// at once, so it is reparented to launchd/init.
		self, err := os.Executable()
		if err != nil {
			os.Exit(1)
		}
		cmd := exec.Command(self, argServe)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, nil
		if err := cmd.Start(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case argServe:
		serve(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	return false
}

// serve handles requests until in is closed, then kills started processes.
func serve(in io.Reader, out io.Writer) {
	var wmu sync.Mutex
	enc := json.NewEncoder(out)
	send := func(r response) {
		wmu.Lock()
		defer wmu.Unlock()
		_ = enc.Encode(r)
	}
	var pmu sync.Mutex
	procs := map[int64]*exec.Cmd{}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var req request
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		switch req.Op {
		case "run":
			go func() {
				out, err := runLocal(req.Argv, time.Duration(req.Timeout)*time.Millisecond)
				r := response{ID: req.ID, Out: out}
				if err != nil {
					r.Err = err.Error()
				}
				send(r)
			}()
		case "start":
			if len(req.Argv) == 0 {
				send(response{ID: req.ID, Err: "empty command"})
				continue
			}
			cmd := exec.Command(req.Argv[0], req.Argv[1:]...)
			cmd.Env = append(os.Environ(), req.Env...)
			var stderr limited
			cmd.Stderr = &stderr
			cmd.WaitDelay = time.Second // children may keep stderr open
			groupAttrs(cmd)
			if err := cmd.Start(); err != nil {
				send(response{ID: req.ID, Err: err.Error(), Exited: true})
				continue
			}
			pmu.Lock()
			procs[req.ID] = cmd
			pmu.Unlock()
			send(response{ID: req.ID, Pid: cmd.Process.Pid})
			go func(id int64) {
				err := cmd.Wait()
				pmu.Lock()
				delete(procs, id)
				pmu.Unlock()
				r := response{ID: id, Exited: true, Out: stderr.Bytes()}
				if err != nil {
					r.Err = err.Error()
				}
				send(r)
			}(req.ID)
		case "kill":
			pmu.Lock()
			if cmd := procs[req.ID]; cmd != nil {
				killGroup(cmd)
			}
			pmu.Unlock()
		}
	}
	// vaultcrab is gone (or closed us): nothing we started may outlive it.
	pmu.Lock()
	for _, cmd := range procs {
		killGroup(cmd)
	}
	pmu.Unlock()
}

// runLocal runs argv with a timeout and returns its combined output.
func runLocal(argv []string, timeout time.Duration) ([]byte, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf limited
	cmd.Stdout, cmd.Stderr = &buf, &buf
	cmd.WaitDelay = time.Second
	groupAttrs(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = ErrTimeout
	}
	return buf.Bytes(), err
}

// ErrTimeout is returned by Run when the command did not finish in time.
var ErrTimeout = errors.New("timed out")

type limited struct {
	mu  sync.Mutex
	buf []byte
}

func (l *limited) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := maxOutput - len(l.buf); room > 0 {
		l.buf = append(l.buf, p[:min(len(p), room)]...)
	}
	return len(p), nil
}

func (l *limited) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf...)
}

// Client talks to a helper.
type Client struct {
	w      io.WriteCloser
	mu     sync.Mutex // guards the fields below and writes to w
	nextID int64
	calls  map[int64]chan response // waiting for the first response
	procs  map[int64]*Proc         // started, waiting for the exit
	closed bool
}

// NewClient starts the helper (the running executable in helper mode).
func NewClient() (*Client, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(self, argDetach)
	detachAttrs(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn helper: %w", err)
	}
	// The first stage exits right after starting the helper; reap it
	// without closing our pipe ends (cmd.Wait would).
	go func() { _, _ = cmd.Process.Wait() }()
	c := &Client{w: stdin, calls: map[int64]chan response{}, procs: map[int64]*Proc{}}
	go c.read(stdout)
	return c, nil
}

func (c *Client) read(r io.Reader) {
	dec := json.NewDecoder(r)
	for {
		var resp response
		if err := dec.Decode(&resp); err != nil {
			break
		}
		c.mu.Lock()
		if ch := c.calls[resp.ID]; ch != nil {
			delete(c.calls, resp.ID)
			ch <- resp
		}
		if p := c.procs[resp.ID]; p != nil && resp.Exited {
			delete(c.procs, resp.ID)
			p.exit(resp)
		}
		c.mu.Unlock()
	}
	// The helper is gone: fail what is pending.
	c.mu.Lock()
	c.closed = true
	gone := response{Err: "spawn helper exited", Exited: true}
	for id, ch := range c.calls {
		delete(c.calls, id)
		ch <- gone
	}
	for id, p := range c.procs {
		delete(c.procs, id)
		p.exit(gone)
	}
	c.mu.Unlock()
}

// call sends req (with a new ID) and registers ch and p for its responses.
func (c *Client) call(req request, ch chan response, p *Proc) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, errors.New("spawn helper exited")
	}
	c.nextID++
	req.ID = c.nextID
	b, _ := json.Marshal(req)
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		return 0, err
	}
	if ch != nil {
		c.calls[req.ID] = ch
	}
	if p != nil {
		p.id = req.ID
		c.procs[req.ID] = p
	}
	return req.ID, nil
}

// Run runs argv in the helper and returns its combined output (bounded).
func (c *Client) Run(ctx context.Context, argv []string, timeout time.Duration) ([]byte, error) {
	ch := make(chan response, 1)
	id, err := c.call(request{Op: "run", Argv: argv, Timeout: timeout.Milliseconds()}, ch, nil)
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		switch r.Err {
		case "":
			return r.Out, nil
		case ErrTimeout.Error():
			return r.Out, ErrTimeout
		}
		return r.Out, errors.New(r.Err)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.calls, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Proc is a long-running process started by the helper.
type Proc struct {
	c      *Client
	id     int64
	Pid    int
	done   chan struct{}
	stderr []byte
	err    string
}

// exit is called once, with c.mu held.
func (p *Proc) exit(r response) {
	p.stderr, p.err = r.Out, r.Err
	close(p.done)
}

// Done is closed when the process has exited.
func (p *Proc) Done() <-chan struct{} { return p.done }

// Stderr returns the first 64 KiB the process wrote to stderr (after Done).
func (p *Proc) Stderr() []byte { return p.stderr }

// Kill stops the process.
func (p *Proc) Kill() {
	p.c.mu.Lock()
	defer p.c.mu.Unlock()
	if !p.c.closed {
		b, _ := json.Marshal(request{ID: p.id, Op: "kill"})
		_, _ = p.c.w.Write(append(b, '\n'))
	}
}

// Start starts argv in the helper with env added to the helper's
// environment (no secrets except a bw session key, see the package doc).
func (c *Client) Start(argv, env []string) (*Proc, error) {
	p := &Proc{c: c, done: make(chan struct{})}
	ch := make(chan response, 1)
	if _, err := c.call(request{Op: "start", Argv: argv, Env: env}, ch, p); err != nil {
		return nil, err
	}
	r := <-ch // the start acknowledgment (or the failure)
	if r.Pid == 0 {
		return nil, fmt.Errorf("start %s: %s", argv[0], r.Err)
	}
	p.Pid = r.Pid
	return p, nil
}

// Close stops the helper and everything it started.
func (c *Client) Close() { _ = c.w.Close() }
