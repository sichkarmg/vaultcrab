// SPDX-License-Identifier: MIT

// Package tunnel forwards local TCP ports through an SSH connection (like
// ssh -L, and ssh -D for a SOCKS proxy). The SSH side is abstract: a
// Connector hands out a connection (in Vaultcrab a shared connection to a
// jump host), and the tunnel asks for a new one when it is lost, with a
// backoff and a limit of attempts. Nothing ticks while the tunnel is idle:
// timers run only between reconnection attempts.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Client opens TCP connections from the far side of an SSH connection.
type Client interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// Lease is a connection in use by a tunnel: Done is closed when it is lost,
// Release gives it back (the tunnel calls it once).
type Lease struct {
	Client  Client
	Done    <-chan struct{}
	Release func()
}

// Connector returns a connection for the tunnel; it may ask the user
// (a passphrase, a host key) and may take long.
type Connector func(ctx context.Context) (Lease, error)

// Kind is the type of a tunnel.
type Kind int

const (
	// Local forwards the local port to Target (ssh -L).
	Local Kind = iota
	// SOCKS is a SOCKS5 proxy on the local port: each connection goes to
	// the address it asks for (ssh -D).
	SOCKS
)

func (k Kind) String() string {
	if k == SOCKS {
		return "socks"
	}
	return "local"
}

// Spec describes a tunnel.
type Spec struct {
	Kind   Kind
	Target string // host:port (Local)
	Bind   string // the local address ("" = 127.0.0.1)
	Port   int    // the local port (0 = any free one)
}

// State is the state of a tunnel.
type State int

const (
	Connecting   State = iota // the first connection is being made
	Active                    // connected: new local connections are forwarded
	Reconnecting              // the connection was lost: waiting before the next attempt
	Failed                    // no more attempts (or the error needs the user): a new local connection or Restart tries again
	Stopped                   // stopped: the port is closed
)

func (s State) String() string {
	return [...]string{"connecting", "active", "reconnecting", "failed", "stopped"}[s]
}

// Options tune the reconnection.
type Options struct {
	// Backoff is the first delay before a reconnection attempt (0 = 1 s);
	// it doubles up to MaxBackoff (0 = 1 min).
	Backoff, MaxBackoff time.Duration
	// Attempts is the number of attempts after a loss before the tunnel
	// fails (0 = 8).
	Attempts int
	// Permanent reports errors that another attempt cannot fix (an
	// authentication or host key refused, a question canceled): the
	// tunnel fails at once.
	Permanent func(error) bool
	// Wait bounds how long a new local connection waits for the
	// connection while the tunnel connects (0 = 30 s).
	Wait time.Duration
	// Notify is called after each change of the status (from any
	// goroutine; it must not block).
	Notify func()
	// Log receives a line per event (no secrets). Optional.
	Log func(format string, args ...any)
}

func (o Options) withDefaults() Options {
	if o.Backoff <= 0 {
		o.Backoff = time.Second
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = time.Minute
	}
	if o.Attempts <= 0 {
		o.Attempts = 8
	}
	if o.Wait <= 0 {
		o.Wait = 30 * time.Second
	}
	if o.Notify == nil {
		o.Notify = func() {}
	}
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.Permanent == nil {
		o.Permanent = func(error) bool { return false }
	}
	return o
}

// Status is a snapshot of a tunnel.
type Status struct {
	Spec  Spec
	Addr  string // the local address listened on (host:port)
	Port  int    // its port
	Note  string // e.g. the requested port was busy
	State State
	Err   error     // why the last attempt failed (or the connection was lost)
	Since time.Time // of the state
	Retry time.Time // Reconnecting: the next attempt
	Open  int       // connections open now
	Total int       // connections forwarded so far
	// Connects counts the connections made: more than one means it was
	// connected again (after a loss or Restart).
	Connects int
}

// Tunnel is a running tunnel.
type Tunnel struct {
	spec Spec
	opt  Options
	ln   net.Listener
	note string

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{} // an attempt now (Restart, a connection while failed)
	done   chan struct{} // run ended

	restart atomic.Bool // Restart: the attempts start over, an active connection is dropped

	mu      sync.Mutex
	st      Status
	lease   *Lease
	ready   chan struct{}         // closed while active; replaced when not
	conns   map[net.Conn]struct{} // local connections
	remotes map[net.Conn]struct{} // their connections from the far side
}

// Start listens on the local port and connects in the background. It
// fails only when no port can be listened on.
func Start(ctx context.Context, spec Spec, connect Connector, o Options) (*Tunnel, error) {
	if spec.Kind == Local {
		if _, _, err := net.SplitHostPort(spec.Target); err != nil {
			return nil, fmt.Errorf("target %q: want host:port", spec.Target)
		}
	}
	ln, note, err := Listen(spec.Bind, spec.Port)
	if err != nil {
		return nil, err
	}
	o = o.withDefaults()
	ctx, cancel := context.WithCancel(ctx)
	t := &Tunnel{spec: spec, opt: o, ln: ln, note: note, ctx: ctx, cancel: cancel,
		wake: make(chan struct{}, 1), done: make(chan struct{}), ready: make(chan struct{}),
		conns: map[net.Conn]struct{}{}, remotes: map[net.Conn]struct{}{}}
	addr := ln.Addr().(*net.TCPAddr)
	t.st = Status{Spec: spec, Addr: ln.Addr().String(), Port: addr.Port, Note: note, State: Connecting, Since: time.Now()}
	o.Log("tunnel: listening on %s (%s)", t.st.Addr, describe(spec))
	go t.accept()
	go t.run(connect)
	return t, nil
}

func describe(s Spec) string {
	if s.Kind == SOCKS {
		return "SOCKS5 proxy"
	}
	return "to " + s.Target
}

// Status returns the current status.
func (t *Tunnel) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.st
	st.Open = len(t.conns)
	return st
}

// Restart drops the connection (if any) and connects again now; the
// attempts start over. The local port stays open.
func (t *Tunnel) Restart() {
	t.restart.Store(true)
	t.kick()
}

// kick wakes the run loop: an attempt now, or (Restart) dropping an active
// connection.
func (t *Tunnel) kick() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// Stop closes the port, the connections through the tunnel and gives the
// connection back. It waits until the tunnel has ended.
func (t *Tunnel) Stop() {
	t.cancel()
	t.ln.Close()
	t.mu.Lock()
	for c := range t.conns {
		c.Close()
	}
	for c := range t.remotes {
		c.Close()
	}
	t.mu.Unlock()
	<-t.done
}

// Done is closed when the tunnel has stopped.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

func (t *Tunnel) set(f func(st *Status)) {
	t.mu.Lock()
	f(&t.st)
	t.mu.Unlock()
	t.opt.Notify()
}

// run keeps the tunnel connected: an attempt, then waiting for the loss of
// the connection, a Restart or the end; after a loss the attempts repeat
// with a growing delay until Attempts, then the tunnel fails and waits for
// a wake-up (a new local connection, Restart).
func (t *Tunnel) run(connect Connector) {
	defer close(t.done)
	defer func() {
		t.set(func(st *Status) { st.State, st.Since, st.Retry = Stopped, time.Now(), time.Time{} })
		t.opt.Log("tunnel %s: stopped", t.st.Addr)
	}()
	attempt := 0
	delay := t.opt.Backoff
	for t.ctx.Err() == nil {
		l, err := connect(t.ctx)
		if t.ctx.Err() != nil {
			if err == nil {
				l.Release()
			}
			return
		}
		if err != nil {
			attempt++
			t.opt.Log("tunnel %s: connection failed (attempt %d): %v", t.st.Addr, attempt, err)
			if t.opt.Permanent(err) || attempt >= t.opt.Attempts {
				t.set(func(st *Status) {
					st.State, st.Err, st.Since, st.Retry = Failed, err, time.Now(), time.Time{}
				})
				if !t.sleep(0) {
					return
				}
				t.restart.Store(false)
				attempt, delay = 0, t.opt.Backoff
				t.set(func(st *Status) { st.State, st.Since = Connecting, time.Now() })
				continue
			}
			retry := time.Now().Add(delay)
			t.set(func(st *Status) {
				if st.State != Connecting {
					st.State = Reconnecting
				}
				st.Err, st.Retry = err, retry
			})
			if !t.sleep(delay) {
				return
			}
			delay = min(2*delay, t.opt.MaxBackoff)
			if t.restart.Swap(false) {
				attempt, delay = 0, t.opt.Backoff
				t.set(func(st *Status) { st.State, st.Since = Connecting, time.Now() })
			}
			continue
		}
		attempt, delay = 0, t.opt.Backoff
		lost := t.hold(l)
		if t.ctx.Err() != nil {
			return
		}
		t.opt.Log("tunnel %s: %v, connecting again", t.st.Addr, lost)
		state := Reconnecting
		if errors.Is(lost, errRestart) {
			state, lost = Connecting, nil
		}
		t.set(func(st *Status) { st.State, st.Err, st.Since = state, lost, time.Now() })
	}
}

var errRestart = errors.New("restarted")

// hold makes l the connection of the tunnel until it is lost, Restart or
// the end; it gives it back and returns why it ended.
func (t *Tunnel) hold(l Lease) error {
	t.mu.Lock()
	t.lease = &l
	t.st.State, t.st.Err, t.st.Since, t.st.Retry = Active, nil, time.Now(), time.Time{}
	t.st.Connects++
	close(t.ready)
	t.mu.Unlock()
	t.opt.Log("tunnel %s: connected", t.st.Addr)
	t.opt.Notify()
	var lost error
	for lost == nil {
		select {
		case <-t.ctx.Done():
			lost = t.ctx.Err()
		case <-l.Done:
			lost = errors.New("connection lost")
		case <-t.wake:
			if t.restart.Swap(false) {
				lost = errRestart
			} // else: a new local connection while active
		}
	}
	t.mu.Lock()
	t.lease = nil
	t.ready = make(chan struct{})
	t.mu.Unlock()
	l.Release()
	return lost
}

// sleep waits d (0 = until woken), a wake-up or the end; it reports false
// at the end.
func (t *Tunnel) sleep(d time.Duration) bool {
	var timer <-chan time.Time
	if d > 0 {
		tm := time.NewTimer(d)
		defer tm.Stop()
		timer = tm.C
	}
	select {
	case <-t.ctx.Done():
		return false
	case <-t.wake:
		return true
	case <-timer:
		return true
	}
}

// client waits for the connection (waking a failed tunnel) and returns it.
func (t *Tunnel) client() (Client, error) {
	t.mu.Lock()
	l, ready, state := t.lease, t.ready, t.st.State
	t.mu.Unlock()
	if l != nil {
		return l.Client, nil
	}
	if state == Failed || state == Reconnecting {
		t.kick() // a new connection tries at once
	}
	tm := time.NewTimer(t.opt.Wait)
	defer tm.Stop()
	select {
	case <-ready:
	case <-tm.C:
		return nil, errors.New("not connected")
	case <-t.ctx.Done():
		return nil, t.ctx.Err()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lease == nil {
		return nil, errors.New("not connected")
	}
	return t.lease.Client, nil
}

func (t *Tunnel) accept() {
	for {
		c, err := t.ln.Accept()
		if err != nil {
			if t.ctx.Err() != nil {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			t.opt.Log("tunnel %s: accept: %v", t.st.Addr, err)
			return
		}
		t.mu.Lock()
		t.conns[c] = struct{}{}
		t.st.Total++
		t.mu.Unlock()
		t.opt.Notify()
		go t.serve(c)
	}
}

func (t *Tunnel) serve(c net.Conn) {
	defer func() {
		c.Close()
		t.mu.Lock()
		delete(t.conns, c)
		t.mu.Unlock()
		t.opt.Notify()
	}()
	target := t.spec.Target
	var reply func(error)
	if t.spec.Kind == SOCKS {
		var err error
		target, reply, err = socksHandshake(c)
		if err != nil {
			t.opt.Log("tunnel %s: SOCKS: %v", t.st.Addr, err)
			return
		}
	}
	cl, err := t.client()
	if err != nil {
		if reply != nil {
			reply(err)
		}
		return
	}
	dctx, cancel := context.WithTimeout(t.ctx, 30*time.Second)
	r, err := cl.DialContext(dctx, "tcp", target)
	cancel()
	if err != nil {
		t.opt.Log("tunnel %s: to %s: %v", t.st.Addr, target, err)
		if reply != nil {
			reply(err)
		}
		return
	}
	if reply != nil {
		reply(nil)
	}
	t.mu.Lock()
	if t.ctx.Err() != nil {
		t.mu.Unlock()
		r.Close()
		return
	}
	t.remotes[r] = struct{}{} // closed by Stop too
	t.mu.Unlock()
	defer func() {
		r.Close()
		t.mu.Lock()
		delete(t.remotes, r)
		t.mu.Unlock()
	}()
	pipe(c, r)
}

// pipe copies both ways until one side ends, then closes both.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			dst.Close()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}

// Loopback is the default address of the local side.
const Loopback = "127.0.0.1"

// IsLoopback reports whether bind ("" = the default) listens on this
// computer only.
func IsLoopback(bind string) bool {
	if bind == "" || bind == "localhost" {
		return true
	}
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}

// portTries is how many ports after a busy one are tried before any free
// port.
const portTries = 20

// Listen listens on bind:port ("" = 127.0.0.1, port 0 = any free port).
// When port is busy, the next ones are tried, then any free port; note
// says so.
func Listen(bind string, port int) (net.Listener, string, error) {
	if bind == "" {
		bind = Loopback
	}
	listen := func(p int) (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(p)))
	}
	ln, err := listen(port)
	if err == nil || port == 0 {
		if err != nil {
			return nil, "", fmt.Errorf("listen on %s: %w", bind, err)
		}
		return ln, "", nil
	}
	first := err
	for p := port + 1; p <= min(port+portTries, 65535); p++ {
		if ln, err := listen(p); err == nil {
			return ln, fmt.Sprintf("port %d is busy: using %d", port, p), nil
		}
	}
	ln, err = listen(0)
	if err != nil {
		return nil, "", fmt.Errorf("listen on %s:%d: %w", bind, port, first)
	}
	return ln, fmt.Sprintf("port %d is busy: using %d", port, ln.Addr().(*net.TCPAddr).Port), nil
}

// FreePort returns port if it can be listened on at bind, else the free
// port Listen would take instead (0 if none).
func FreePort(bind string, port int) int {
	ln, _, err := Listen(bind, port)
	if err != nil {
		return 0
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return p
}
