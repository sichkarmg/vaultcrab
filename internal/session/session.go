// SPDX-License-Identifier: MIT

// Package session ties a host, a terminal emulator and an SSH connection
// together. The terminal (and its scrollback) outlives connections, so a
// session can be reconnected without losing history. The credential is
// fetched anew for every connection attempt and wiped right after the
// handshake.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/applog"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/term"
)

// State of a session.
type State int

const (
	Idle State = iota
	Connecting
	Connected
	Disconnected // ended (cleanly or by error); can reconnect
	Closed       // closed by the user
)

func (s State) String() string {
	return [...]string{"idle", "connecting", "connected", "disconnected", "closed"}[s]
}

// FieldVia is the field of a host reached through a jump host: the name of
// the jump host (shown while connecting).
const FieldVia = "via"

// SecretFunc returns the credential for one connection attempt (nil: no
// credential, e.g. a local shell).
type SecretFunc func(ctx context.Context) (secret.Secret, error)

// Dialer opens the remote shell; connect.DialSSH in production.
type Dialer func(ctx context.Context, h model.Host, sec *secret.Secret, cols, rows int) (Remote, error)

// Remote is an established interactive connection.
type Remote interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Options configures a session.
type Options struct {
	Cols, Rows int
	Scrollback int
	Secret     SecretFunc
	Dial       Dialer
	Notify     func() // called (from any goroutine) when output or state changes
	Timeout    time.Duration
	// Hold keeps the pane open after a clean exit (script output stays
	// visible; Enter runs it again).
	Hold bool
}

// SSHDialer adapts connect.DialSSH.
func SSHDialer(o connect.SSHOptions) Dialer {
	return func(ctx context.Context, h model.Host, sec *secret.Secret, cols, rows int) (Remote, error) {
		return connect.DialSSH(ctx, h, sec, cols, rows, o)
	}
}

// LocalDialer starts a local program on a pseudo-terminal instead of an SSH
// connection.
func LocalDialer(o connect.LocalOptions) Dialer {
	return func(ctx context.Context, _ model.Host, _ *secret.Secret, cols, rows int) (Remote, error) {
		return connect.StartLocal(ctx, cols, rows, o)
	}
}

// Session is safe for concurrent use.
type Session struct {
	Host model.Host
	Term *term.Terminal

	opts Options

	mu       sync.Mutex
	state    State
	err      error
	remote   Remote
	cancel   context.CancelFunc
	attempts int
	since    time.Time
	exited   bool // the remote shell exited cleanly (status 0)
}

// Exited reports whether the last connection ended with a clean exit of the
// remote shell (as opposed to an error or a dropped connection).
func (s *Session) Exited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited && s.state == Disconnected
}

// Hold reports whether the session stays open after a clean exit.
func (s *Session) Hold() bool { return s.opts.Hold }

// New creates an idle session; call Connect.
func New(h model.Host, o Options) *Session {
	if o.Notify == nil {
		o.Notify = func() {}
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute // vault + TCP + a slow SSH handshake
	}
	return &Session{Host: h, Term: term.New(o.Cols, o.Rows, o.Scrollback), opts: o, since: time.Now()}
}

// State returns the state and the last error.
func (s *Session) State() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.err
}

// Since returns when the current state was entered.
func (s *Session) Since() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.since
}

// Attempts returns the number of connection attempts so far.
func (s *Session) Attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

func (s *Session) setState(st State, err error) {
	s.mu.Lock()
	old := s.state
	s.state, s.err, s.since = st, err, time.Now()
	s.mu.Unlock()
	if st != old || err != nil {
		if err != nil {
			applog.Printf("session: %s (%s): %s: %v", s.Host.Name, s.Host.Target(), st, err)
		} else {
			applog.Printf("session: %s (%s): %s", s.Host.Name, s.Host.Target(), st)
		}
	}
	s.opts.Notify()
}

// Connect starts a connection attempt in the background. It is a no-op while
// connecting/connected or after Close.
func (s *Session) Connect() {
	s.mu.Lock()
	if s.state == Connecting || s.state == Connected || s.state == Closed {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.attempts++
	s.exited = false
	reconnect := s.attempts > 1
	s.state, s.err, s.since = Connecting, nil, time.Now()
	s.mu.Unlock()
	applog.Printf("session: %s (%s): connecting (attempt %d)", s.Host.Name, s.Host.Target(), s.attempts)
	switch {
	case s.Host.Scheme == model.Local:
		s.status("starting %s…", s.Host.Fields["command"])
	case s.Host.Scheme == model.VNC:
		s.status("opening %s…", s.Host.Target())
	case s.Host.Fields[FieldVia] != "" && reconnect:
		s.status("reconnecting to %s through %s…", s.Host.Target(), s.Host.Fields[FieldVia])
	case s.Host.Fields[FieldVia] != "":
		s.status("connecting to %s through %s…", s.Host.Target(), s.Host.Fields[FieldVia])
	case reconnect:
		s.status("reconnecting to %s…", s.Host.Target())
	default:
		s.status("connecting to %s…", s.Host.Target())
	}
	s.opts.Notify()
	go s.run(ctx)
}

// status writes a local status line into the terminal (dim, prefixed).
func (s *Session) status(format string, args ...any) {
	s.Term.WriteString("\r\n\x1b[0;2m[vaultcrab] " + fmt.Sprintf(format, args...) + "\x1b[0m\r\n")
}

func (s *Session) run(ctx context.Context) {
	cctx, ccancel := context.WithTimeout(ctx, s.opts.Timeout)
	var sec secret.Secret
	if s.opts.Secret != nil { // nil for a local shell
		var err error
		if sec, err = s.opts.Secret(cctx); err != nil {
			ccancel()
			s.fail(ctx, fmt.Errorf("credential: %w", err))
			return
		}
	}
	cols, rows := s.Term.Size()
	r, err := s.opts.Dial(cctx, s.Host, &sec, cols, rows)
	sec.Wipe()
	ccancel()
	if err != nil {
		s.fail(ctx, err)
		return
	}
	s.mu.Lock()
	if s.state == Closed || ctx.Err() != nil {
		s.mu.Unlock()
		r.Close()
		return
	}
	s.remote = r
	s.state, s.err, s.since = Connected, nil, time.Now()
	s.mu.Unlock()
	s.Term.SetSink(r)
	// The terminal may have been resized while connecting.
	if c2, r2 := s.Term.Size(); c2 != cols || r2 != rows {
		_ = r.Resize(c2, r2)
	}
	s.opts.Notify()

	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_, _ = s.Term.Write(buf[:n])
			s.opts.Notify()
		}
		if err != nil {
			break
		}
	}
	<-r.Done()
	s.Term.SetSink(nil)
	s.mu.Lock()
	closed := s.state == Closed
	s.remote = nil
	s.mu.Unlock()
	if closed {
		return
	}
	cause := r.Err()
	clean := cause == nil
	if clean {
		cause = errors.New("session ended")
	}
	switch {
	case s.Host.Scheme == model.Local && clean:
		s.status("finished — press Enter to run again")
	case s.Host.Scheme == model.Local:
		s.status("%v — press Enter to run again", cause)
	case s.Host.Scheme == model.VNC:
		s.status("%v — press Enter to open it again", cause)
	default:
		s.status("%v — press Enter to reconnect", cause)
	}
	s.mu.Lock()
	s.exited = clean
	s.mu.Unlock()
	s.setState(Disconnected, cause)
}

func (s *Session) fail(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return // closed by the user
	}
	s.status("\x1b[31merror:\x1b[0;2m %v — press Enter to retry", err)
	s.setState(Disconnected, err)
}

// Pending writes a hint into an idle (not yet connected) session.
func (s *Session) Pending(hint string) { s.status("%s", hint) }

// Reconnect connects again if the session is not active.
func (s *Session) Reconnect() { s.Connect() }

// Resize changes the terminal and PTY size.
func (s *Session) Resize(cols, rows int) {
	if !s.Term.Resize(cols, rows) {
		return
	}
	s.mu.Lock()
	r := s.remote
	s.mu.Unlock()
	if r != nil {
		_ = r.Resize(cols, rows)
	}
}

// Disconnect drops the connection but keeps the session (for tests / "kill").
func (s *Session) Disconnect() {
	s.mu.Lock()
	r := s.remote
	s.mu.Unlock()
	if r != nil {
		r.Close()
	}
}

// Close terminates the session and releases the terminal.
func (s *Session) Close() {
	s.mu.Lock()
	if s.state == Closed {
		s.mu.Unlock()
		return
	}
	s.state = Closed
	r, cancel := s.remote, s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if r != nil {
		r.Close()
	}
	s.Term.SetSink(nil)
	s.Term.Close()
	s.opts.Notify()
}
