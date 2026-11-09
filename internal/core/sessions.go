// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	osuser "os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/scripts"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

// sessions is the registry of open sessions.
type sessions struct {
	svc  *Service
	mu   sync.Mutex
	list []*Session
	dial session.Dialer // overridable in tests
}

func (r *sessions) init(s *Service) {
	r.svc = s
	r.dial = session.SSHDialer(s.opts.SSH)
}

// ErrNotSSH is returned when opening a terminal for a non-SSH host.
var ErrNotSSH = errors.New("not an SSH host")

// InTab is the protocol dispatch (extension point, see docs/extending.md):
// it reports whether h opens in a tab of its own: SSH and the local shell
// (a terminal), VNC (the external client followed by vaultcrab).
func (s *Service) InTab(h model.Host) bool {
	switch h.Scheme {
	case model.SSH, model.Local:
		return true
	case model.VNC:
		return s.opts.VNC != nil
	}
	return false
}

// OpenSession creates a session for an SSH or VNC host and starts
// connecting. The credential is fetched from the vault on every (re)connect.
func (s *Service) OpenSession(h model.Host, cols, rows int) (*Session, error) {
	sess, err := s.newSSHSession(h, cols, rows, nil)
	if err != nil {
		return nil, err
	}
	sess.Connect()
	return sess, nil
}

// OpenIdle creates a session for a host or the local shell (h = LocalHost)
// without connecting: a restored tab waits for Enter.
func (s *Service) OpenIdle(h model.Host, cols, rows int) (*Session, error) {
	var sess *Session
	if h.Scheme == model.Local {
		sess = s.newLocalSession(cols, rows)
	} else {
		var err error
		if sess, err = s.newSSHSession(h, cols, rows, nil); err != nil {
			return nil, err
		}
	}
	sess.Pending("restored from last run — press Enter to connect")
	return sess, nil
}

func (s *Service) newSSHSession(h model.Host, cols, rows int, vncSpec *connect.LaunchSpec) (*Session, error) {
	dial := s.sessions.dial
	switch {
	case h.Scheme == model.VNC && s.opts.VNC != nil:
		dial = s.vncDialer(vncSpec)
	case h.Scheme != model.SSH:
		return nil, ErrNotSSH
	}
	hostID := h.ID
	sec := func(ctx context.Context) (secret.Secret, error) {
		return s.Secret(ctx, hostID)
	}
	if spec, ok := JumpSpecOf(h); ok {
		// through a jump host: the dialer gets the keys of both hops
		dial, sec = s.jumpDialer(spec), nil
	}
	sess := session.New(h, session.Options{
		Cols: cols, Rows: rows,
		Scrollback: s.opts.Scrollback,
		Secret:     sec,
		Dial:       dial,
		Notify:     s.emit,
		Debug:      s.connDebug.Load,
	})
	s.sessions.mu.Lock()
	s.sessions.list = append(s.sessions.list, sess)
	s.sessions.mu.Unlock()
	return sess, nil
}

// vncDialer starts the VNC client (with the item's jump host, if any) as the
// "connection" of a session; spec, if not nil, replaces the configured
// client (a test start from the settings).
func (s *Service) vncDialer(spec *connect.LaunchSpec) session.Dialer {
	return func(ctx context.Context, h model.Host, sec *secret.Secret, _, _ int) (session.Remote, error) {
		if spec != nil {
			ctx = connect.WithSpec(ctx, model.VNC, *spec)
		}
		jump, err := s.jumpHost(h)
		if err != nil {
			return nil, err
		}
		var tunnel func(context.Context) (*connect.Tunnel, error)
		if jump != nil {
			j := *jump
			tunnel = func(ctx context.Context) (*connect.Tunnel, error) {
				jsec, err := s.Secret(ctx, j.ID)
				if err != nil {
					return nil, fmt.Errorf("jump host %s: credential: %w", j.Name, err)
				}
				defer jsec.Wipe()
				return connect.DialTunnel(ctx, j, &jsec, s.opts.SSH)
			}
		}
		return s.opts.VNC(ctx, h, sec, tunnel)
	}
}

// JumpField is the custom field of an item naming the SSH item (by name or
// ID) through which its server is reached.
const JumpField = "jump"

// jumpHost returns the SSH host named in the jump field of h, nil if none.
func (s *Service) jumpHost(h model.Host) (*model.Host, error) {
	want := strings.TrimSpace(h.Fields[JumpField])
	if want == "" {
		return nil, nil
	}
	var found []model.Host
	for _, c := range s.Snapshot().Hosts {
		if c.Scheme == model.SSH && (c.ID == want || strings.EqualFold(c.Name, want)) {
			if c.ID == want {
				return &c, nil
			}
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("jump host %q (field %s of %s): no SSH item with this name or ID", want, JumpField, h.Name)
	case 1:
		return &found[0], nil
	}
	return nil, fmt.Errorf("jump host %q (field %s of %s): %d SSH items have this name; use the item ID", want, JumpField, h.Name, len(found))
}

// LocalHost describes the local shell as a pseudo-host.
func (s *Service) LocalHost() model.Host {
	argv := s.opts.Local.Command
	if len(argv) == 0 {
		argv = connect.DefaultShell()
	}
	shell := filepath.Base(argv[0])
	user := ""
	if u, err := osuser.Current(); err == nil {
		user = u.Username
		if i := strings.LastIndexAny(user, `\`); i >= 0 {
			user = user[i+1:] // DOMAIN\user on Windows
		}
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return model.Host{ID: "local", Name: "local: " + shell, Scheme: model.Local, User: user, Addr: host,
		Fields: map[string]string{"command": strings.Join(argv, " ")}}
}

// OpenLocal starts a local shell session (no vault access needed).
func (s *Service) OpenLocal(cols, rows int) *Session {
	sess := s.newLocalSession(cols, rows)
	sess.Connect()
	return sess
}

func (s *Service) newLocalSession(cols, rows int) *Session {
	sess := session.New(s.LocalHost(), session.Options{
		Cols: cols, Rows: rows,
		Scrollback: s.opts.Scrollback,
		Dial:       session.LocalDialer(s.opts.Local),
		Notify:     s.emit,
	})
	s.sessions.mu.Lock()
	s.sessions.list = append(s.sessions.list, sess)
	s.sessions.mu.Unlock()
	return sess
}

// Scripts returns the launch scripts available for h.
func (s *Service) Scripts(h model.Host) []scripts.Script {
	snap := s.Snapshot()
	return scripts.For(s.scriptList(), &h, &snap)
}

// DefaultScript returns the script Enter runs for h, if any.
func (s *Service) DefaultScript(h model.Host) (scripts.Script, bool) {
	snap := s.Snapshot()
	return scripts.DefaultFor(s.scriptList(), &h, &snap)
}

// RunScript runs sc for h: in terminal mode it returns a new session (shown
// in a tab, kept open after the command ends); in external mode it starts a
// detached program and returns nil.
func (s *Service) RunScript(ctx context.Context, h model.Host, sc scripts.Script, cols, rows int) (*Session, error) {
	argv := connect.Expand(sc.Command, h)
	if len(argv) == 0 {
		return nil, fmt.Errorf("script %q has no command for this OS", sc.Name)
	}
	shown := strings.Join(argv, " ")
	if connect.HasPassword(sc.Command) {
		sec, err := s.Secret(ctx, h.ID)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		argv = connect.ExpandPassword(sc.Command, h, string(sec.Value))
		sec.Wipe()
		shown = strings.Join(connect.ExpandPassword(sc.Command, h, "******"), " ")
	}
	logf("script: %q for %s: %s", sc.Name, h.Name, shown) // {password} masked
	if sc.Mode == scripts.External {
		if s.opts.Start == nil {
			return nil, ErrNoLauncher
		}
		hostID := h.ID
		return nil, s.opts.Start(ctx, argv, sc.PasswordStdin, func(ctx context.Context) (secret.Secret, error) {
			return s.Secret(ctx, hostID)
		})
	}
	ph := h // keeps the ID: the host list shows the session state
	ph.Name = sc.Name + ": " + h.Name
	ph.Scheme = model.Local
	ph.Fields = map[string]string{"command": shown}
	lo := s.opts.Local
	lo.Command = argv
	sess := session.New(ph, session.Options{
		Cols: cols, Rows: rows,
		Scrollback: s.opts.Scrollback,
		Dial:       session.LocalDialer(lo),
		Notify:     s.emit,
		Hold:       true,
	})
	s.sessions.mu.Lock()
	s.sessions.list = append(s.sessions.list, sess)
	s.sessions.mu.Unlock()
	sess.Connect()
	return sess, nil
}

// CloseSession closes and forgets a session.
func (s *Service) CloseSession(sess *Session) {
	sess.Close()
	s.sessions.mu.Lock()
	s.sessions.list = slices.DeleteFunc(s.sessions.list, func(x *Session) bool { return x == sess })
	s.sessions.mu.Unlock()
	s.emit()
}

// Sessions returns open sessions in creation order.
func (s *Service) Sessions() []*Session {
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	return slices.Clone(s.sessions.list)
}

// Shutdown stops the tunnels and closes all sessions and the connections
// to jump hosts.
func (s *Service) Shutdown() {
	s.StopTunnels()
	for _, sess := range s.Sessions() {
		s.CloseSession(sess)
	}
	s.closeJumps()
}
