// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Hop is one SSH connection of a chain through a jump host.
type Hop struct {
	Name string // shown in errors (the vault item, the template's jump)
	Addr string // host:port
	User string
	Auth []ssh.AuthMethod
	Keys HostKeyPolicy
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
// are *JumpError.
func DialJump(ctx context.Context, jump, target Hop, cols, rows int, o SSHOptions) (*Conn, error) {
	o = o.withDefaults()
	if jump.Keys.Role == "" {
		jump.Keys.Role = "jump host"
	}
	if jump.Keys.Name == "" {
		jump.Keys.Name = jump.Name
	}
	if target.Keys.Role == "" {
		target.Keys.Role = "target"
	}
	if target.Keys.Name == "" {
		target.Keys.Name = target.Addr
	}
	fail := func(isTarget bool, h Hop, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		je := &JumpError{Target: isTarget, Jump: jump.Name, Addr: h.Addr, User: h.User, Err: err, Stage: StageHandshake}
		var he *hopErr
		if errors.As(err, &he) {
			je.Stage, je.Err = he.stage, he.err
		}
		return je
	}
	jc, _, err := dialHop(ctx, jump.Addr, jump.User, jump.Auth, jump.Keys, o, nil)
	if err != nil {
		return nil, fail(false, jump, err)
	}
	tc, _, err := dialHop(ctx, target.Addr, target.User, target.Auth, target.Keys, o, jc)
	if err != nil {
		jc.Close()
		return nil, fail(true, target, err)
	}
	c, err := startShell(tc, o.Term, cols, rows)
	if err != nil {
		tc.Close()
		jc.Close()
		return nil, fail(true, target, err)
	}
	c.via = jc
	if o.Keepalive > 0 {
		go c.keepalive(o.Keepalive)
	}
	return c, nil
}

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
