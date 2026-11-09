// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/sichkarmg/vaultcrab/internal/knock"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Hop is one SSH connection of a chain through a jump host.
type Hop struct {
	// AuthInfo describes the credential for the debug trace (never the
	// secret: "public key ssh-ed25519 SHA256:… from ~/.ssh/id", "password").
	AuthInfo string
	Name     string // shown in errors (the vault item, the template's jump)
	Addr     string // host:port
	User     string
	Auth     []ssh.AuthMethod
	Keys     HostKeyPolicy
	// Knock is knocked at the host before connecting (only for a hop
	// reached directly: the jump host).
	Knock knock.Sequence
}

// JumpError is a failed connection through a jump host, by stage: the jump
// host unreachable, the target unreachable from it, the authentication or
// the host key of either.
type JumpError struct {
	Target bool  // the target failed (else the jump host)
	Stage  Stage // what failed
	Jump   string
	Addr   string // of the hop that failed
	User   string
	Err    error
}

func (e *JumpError) Error() string {
	who := fmt.Sprintf("jump host %s", e.Jump)
	if e.Target {
		who = "target " + e.Addr
	}
	switch e.Stage {
	case StageConnect:
		if e.Target {
			return fmt.Sprintf("target %s unreachable from the jump host %s: %v", e.Addr, e.Jump, plainDialErr(e.Err))
		}
		return fmt.Sprintf("jump host %s (%s) unreachable: %v", e.Jump, e.Addr, plainDialErr(e.Err))
	case StageAuth:
		return fmt.Sprintf("authentication failed on the %s (user %s)", who, e.User)
	case StageHostKey:
		return fmt.Sprintf("%s: %v", who, e.Err)
	}
	return fmt.Sprintf("%s: %v", who, e.Err)
}

func (e *JumpError) Unwrap() error { return e.Err }

// plainDialErr shortens the error of a channel the jump host could not
// open ("ssh: rejected: connect failed (No route to host)").
func plainDialErr(err error) error {
	var oe *ssh.OpenChannelError
	if errors.As(err, &oe) {
		if oe.Message != "" {
			return errors.New(oe.Message)
		}
		return errors.New(oe.Reason.String())
	}
	return err
}

// DialJump connects to the jump host, opens a TCP channel from it to the
// target, runs the second SSH session over that channel and starts a login
// shell with a cols×rows PTY there. The agent is never forwarded. Errors
// are *JumpError. The jump host is closed with the connection.
func DialJump(ctx context.Context, jump, target Hop, cols, rows int, o SSHOptions) (*Conn, error) {
	jc, err := DialJumpHost(ctx, jump, o)
	if err != nil {
		return nil, err
	}
	c, err := DialVia(ctx, jc, jump.Name, target, cols, rows, o)
	if err != nil {
		jc.Close()
		return nil, err
	}
	c.via = jc
	return c, nil
}

func (h *Hop) defaults(role, name string) {
	if h.Keys.Role == "" {
		h.Keys.Role = role
	}
	if h.Keys.Name == "" {
		h.Keys.Name = name
	}
}

// hopFail classifies the error of a hop as a *JumpError.
func hopFail(ctx context.Context, isTarget bool, jumpName string, h Hop, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	je := &JumpError{Target: isTarget, Jump: jumpName, Addr: h.Addr, User: h.User, Err: err, Stage: StageHandshake}
	var he *hopErr
	if errors.As(err, &he) {
		je.Stage, je.Err = he.stage, he.err
	}
	return je
}

// JumpClient is an authenticated SSH connection to a jump host: it opens
// TCP connections from there (shells to targets, tunnels) and can be shared
// by several of them. Done is closed when the connection is lost (or
// closed); keepalives notice a dead one.
type JumpClient struct {
	c    *ssh.Client
	done chan struct{}
	once sync.Once
}

// DialJumpHost connects and authenticates to the jump host. Errors are
// *JumpError. ctx bounds the connection only: the client outlives it.
func DialJumpHost(ctx context.Context, jump Hop, o SSHOptions) (*JumpClient, error) {
	o = o.withDefaults()
	jump.defaults("jump host", jump.Name)
	tracef(ctx, "jump host %s (%s) as %q, %s", jump.Name, jump.Addr, jump.User, jump.AuthInfo)
	if err := knockAt(ctx, jump.Addr, jump.Knock); err != nil {
		return nil, &JumpError{Jump: jump.Name, Addr: jump.Addr, User: jump.User, Stage: StageConnect, Err: err}
	}
	c, _, err := dialHop(ctx, jump.Addr, jump.User, jump.Auth, jump.Keys, o, nil)
	if err != nil {
		return nil, hopFail(ctx, false, jump.Name, jump, err)
	}
	j := &JumpClient{c: c, done: make(chan struct{})}
	go func() {
		_ = c.Wait()
		j.finish()
	}()
	if o.Keepalive > 0 {
		go j.keepalive(o.Keepalive)
	}
	return j, nil
}

// DialContext opens a TCP connection to addr from the jump host.
func (j *JumpClient) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return j.c.DialContext(ctx, network, addr)
}

// Done is closed when the connection is lost or closed.
func (j *JumpClient) Done() <-chan struct{} { return j.done }

// Close ends the connection and every connection through it.
func (j *JumpClient) Close() error {
	err := j.c.Close()
	j.finish()
	return err
}

func (j *JumpClient) finish() {
	j.once.Do(func() {
		j.c.Close()
		close(j.done)
	})
}

// keepalive probes the jump host while the connection lasts, on a session
// channel of its own (see Conn.keepalive: a global request breaks some
// servers). A server that refuses the channel is not probed (TCP
// keepalives still end a dead connection, later).
func (j *JumpClient) keepalive(every time.Duration) {
	sess, err := j.c.NewSession()
	if err != nil {
		return
	}
	defer sess.Close()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-j.done:
			return
		case <-t.C:
			res := make(chan error, 1)
			go func() {
				_, err := sess.SendRequest("keepalive@openssh.com", true, nil)
				res <- err
			}()
			select {
			case err := <-res:
				if err != nil {
					j.finish()
					return
				}
			case <-time.After(3 * every):
				j.finish()
				return
			case <-j.done:
				return
			}
		}
	}
}

// DialVia opens a TCP channel from via (the jump host jumpName) to the
// target, runs the SSH session of the target over it and starts a login
// shell with a cols×rows PTY. Errors are *JumpError. via is not closed.
func DialVia(ctx context.Context, via Dialer, jumpName string, target Hop, cols, rows int, o SSHOptions) (*Conn, error) {
	o = o.withDefaults()
	target.defaults("target", target.Addr)
	tracef(ctx, "target %s as %q, %s", target.Addr, target.User, target.AuthInfo)
	tc, _, err := dialHop(ctx, target.Addr, target.User, target.Auth, target.Keys, o, via)
	if err != nil {
		return nil, hopFail(ctx, true, jumpName, target, err)
	}
	c, err := startShell(tc, o.Term, cols, rows)
	if err != nil {
		tc.Close()
		return nil, hopFail(ctx, true, jumpName, target, err)
	}
	tracef(ctx, "PTY %s %dx%d, shell started on the target", o.Term, cols, rows)
	if o.Keepalive > 0 {
		go c.keepalive(o.Keepalive)
	}
	return c, nil
}

// SetVia makes c close v (a lease of a shared jump host) when it ends.
func (c *Conn) SetVia(v io.Closer) { c.via = v }

// ErrPassphrase: the passphrase of a key file is wrong.
var ErrPassphrase = errors.New("wrong passphrase")

// SignerFromFile reads the private key in path; for an encrypted key it
// calls passphrase (which may ask the user) and wipes what it returns. The
// file contents are wiped after parsing.
func SignerFromFile(path string, passphrase func() ([]byte, error)) (ssh.Signer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("key file: %w", err)
	}
	defer secret.Zero(b)
	s, err := ssh.ParsePrivateKey(b)
	var pm *ssh.PassphraseMissingError
	if err == nil || !errors.As(err, &pm) {
		if err != nil {
			return nil, fmt.Errorf("key file %s: %w", path, err)
		}
		return s, nil
	}
	if passphrase == nil {
		return nil, fmt.Errorf("key file %s is protected with a passphrase", path)
	}
	pp, err := passphrase()
	if err != nil {
		return nil, err
	}
	defer secret.Zero(pp)
	s, err = ssh.ParsePrivateKeyWithPassphrase(b, pp)
	if err != nil {
		return nil, fmt.Errorf("key file %s: %w", path, ErrPassphrase)
	}
	return s, nil
}

// SignerAuth returns the authentication with a key.
func SignerAuth(s ssh.Signer) ssh.AuthMethod { return ssh.PublicKeys(s) }

// Agent connects to the ssh-agent (SSH_AUTH_SOCK; on Windows the OpenSSH
// agent pipe when SSH_AUTH_SOCK is not set). The keys stay in the agent;
// close the connection after the handshake.
func Agent() (ssh.AuthMethod, func(), error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	var rw interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	}
	switch {
	case sock != "" && runtime.GOOS != "windows":
		c, err := net.Dial("unix", sock)
		if err != nil {
			return nil, nil, fmt.Errorf("ssh-agent (%s): %w", sock, err)
		}
		rw = c
	case runtime.GOOS == "windows":
		if sock == "" {
			sock = `\\.\pipe\openssh-ssh-agent`
		}
		f, err := os.OpenFile(sock, os.O_RDWR, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("ssh-agent (%s): %w (is the OpenSSH Authentication Agent service running?)", sock, err)
		}
		rw = f
	default:
		return nil, nil, errors.New("ssh-agent: SSH_AUTH_SOCK is not set")
	}
	ag := agent.NewClient(rw)
	return ssh.PublicKeysCallback(ag.Signers), func() { rw.Close() }, nil
}

// Item fields of port knocking (a vault host; a jump template has keys of
// the same names).
const (
	FieldKnock      = "knock"       // "7000,8000/udp,9000"
	FieldKnockDelay = "knock_delay" // "200ms" (the default)
)

// KnockOf returns the port knocking sequence of a host's fields.
func KnockOf(fields map[string]string) (knock.Sequence, error) {
	return knock.Parse(fields[FieldKnock], fields[FieldKnockDelay])
}

// knockAt knocks at the host of addr (host:port) when seq has knocks.
func knockAt(ctx context.Context, addr string, seq knock.Sequence) error {
	if seq.Empty() {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	tracef(ctx, "port knocking at %s: %s, %s after each", host, seq, seq.Delay)
	return seq.Do(ctx, host, func(f string, a ...any) { tracef(ctx, f, a...) })
}
