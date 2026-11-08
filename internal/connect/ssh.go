// SPDX-License-Identifier: MIT

// Package connect establishes SSH connections (built-in client) and launches
// external programs for the other connection types.
package connect

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// SSHOptions configures Dial.
type SSHOptions struct {
	KnownHosts string        // path of our own known_hosts file (TOFU)
	Timeout    time.Duration // TCP connect timeout (default 15s)
	// Handshake bounds key exchange and authentication (default 60s): slow
	// devices (MikroTik RouterOS 6 on small CPUs) need many seconds for a
	// Diffie-Hellman group exchange.
	Handshake   time.Duration
	Keepalive   time.Duration // keepalive interval (default 15s, <0 disables)
	Term        string        // TERM for the PTY (default xterm-256color)
	StrictHosts bool          // reject unknown hosts instead of trusting on first use
	// Legacy also offers weak algorithms (diffie-hellman-group1-sha1, CBC
	// ciphers, ssh-dss/ssh-rsa host keys, ...) after all strong ones, for old
	// devices such as MikroTik RouterOS 6. They are chosen only when the
	// server supports nothing better.
	Legacy bool
}

// HostKeyError is returned when the server key does not match known_hosts.
type HostKeyError struct {
	Host        string
	Fingerprint string
	File        string
	Line        int
}

func (e *HostKeyError) Error() string {
	return fmt.Sprintf("HOST KEY CHANGED for %s (now %s); if expected, remove line %d from %s",
		e.Host, e.Fingerprint, e.Line, e.File)
}

// UnknownHostError is returned in strict mode for hosts not in known_hosts.
type UnknownHostError struct {
	Host, Fingerprint string
}

func (e *UnknownHostError) Error() string {
	return fmt.Sprintf("unknown host %s (%s)", e.Host, e.Fingerprint)
}

// Conn is an interactive SSH shell with a PTY.
type Conn struct {
	client *ssh.Client
	via    *ssh.Client // the jump host, closed with the connection
	sess   *ssh.Session
	stdin  io.WriteCloser
	stdout io.Reader

	once sync.Once
	done chan struct{}
	err  error
}

var khMu sync.Mutex // serializes known_hosts updates within the process

// DialSSH connects to h, authenticates with sec and starts a login shell
// with a cols×rows PTY. sec is not retained after DialSSH returns: the caller
// should wipe it.
func DialSSH(ctx context.Context, h model.Host, sec *secret.Secret, cols, rows int, o SSHOptions) (*Conn, error) {
	o = o.withDefaults()
	client, err := dialClient(ctx, h, sec, o)
	if err != nil {
		return nil, err
	}
	c, err := startShell(client, o.Term, cols, rows)
	if err != nil {
		tracef(ctx, "shell: %v", err)
		client.Close()
		return nil, err
	}
	tracef(ctx, "PTY %s %dx%d, shell started", o.Term, cols, rows)
	if o.Keepalive > 0 {
		go c.keepalive(o.Keepalive)
	}
	return c, nil
}

func (o SSHOptions) withDefaults() SSHOptions {
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}
	if o.Handshake == 0 {
		o.Handshake = 60 * time.Second
	}
	if o.Keepalive == 0 {
		o.Keepalive = 15 * time.Second
	}
	if o.Term == "" {
		o.Term = "xterm-256color"
	}
	return o
}

// dialClient connects to h and authenticates with sec.
func dialClient(ctx context.Context, h model.Host, sec *secret.Secret, o SSHOptions) (*ssh.Client, error) {
	auth, err := authMethods(sec)
	if err != nil {
		return nil, err
	}
	mode := HostKeyAcceptNew
	if o.StrictHosts {
		mode = HostKeyStrict
	}
	tracef(ctx, "authentication: %s from the vault", DescribeSecret(sec))
	c, _, err := dialHop(ctx, hostAddr(h), h.User, auth, HostKeyPolicy{File: o.KnownHosts, Mode: mode}, o, nil)
	if err != nil {
		var he *hopErr
		if errors.As(err, &he) {
			if he.stage == StageAuth {
				return nil, errors.New("authentication failed")
			}
			return nil, he.err
		}
		return nil, err
	}
	return c, nil
}

func hostAddr(h model.Host) string {
	port := h.Port
	if port == 0 {
		port = 22
	}
	return net.JoinHostPort(h.Addr, strconv.Itoa(port))
}

// Stage names the step of a connection that failed.
type Stage int

const (
	StageConnect   Stage = iota // the TCP connection (or the channel from the jump host)
	StageHostKey                // the host key was not accepted
	StageAuth                   // authentication was refused
	StageHandshake              // another SSH error (algorithms, timeout)
)

// hopErr is a failed hop, classified by stage (dialHop).
type hopErr struct {
	stage Stage
	err   error
}

func (e *hopErr) Error() string { return e.err.Error() }
func (e *hopErr) Unwrap() error { return e.err }

// dialHop connects to addr (through via, an established SSH client, if not
// nil), checks the host key with hk and authenticates as user. The
// handshake is bounded by o.Handshake, except while the user is asked
// about the host key.
func dialHop(ctx context.Context, addr, user string, auth []ssh.AuthMethod, hk HostKeyPolicy, o SSHOptions, via *ssh.Client) (*ssh.Client, net.Conn, error) {
	cfg := &ssh.ClientConfig{
		User:    user,
		Auth:    auth,
		Timeout: o.Timeout,
	}
	if o.Legacy {
		sup, weak := ssh.SupportedAlgorithms(), ssh.InsecureAlgorithms()
		cfg.KeyExchanges = append(sup.KeyExchanges, weak.KeyExchanges...)
		cfg.Ciphers = append(sup.Ciphers, weak.Ciphers...)
		cfg.MACs = append(sup.MACs, weak.MACs...)
		cfg.HostKeyAlgorithms = append(sup.HostKeys, weak.HostKeys...)
	}
	if o.Legacy {
		tracef(ctx, "legacy algorithms on: weak ones are offered last (ssh.legacy_algorithms)")
	}
	// Prefer the key types already pinned for this host, so that the server
	// is not asked for another algorithm we would then report as a mismatch.
	if algos := knownAlgorithms(hk.File, addr); algos != nil {
		cfg.HostKeyAlgorithms = algos
		tracef(ctx, "host key algorithms (pinned in known_hosts): %s", strings.Join(algos, ","))
	}
	cfg.BannerCallback = func(msg string) error {
		for _, l := range strings.Split(strings.TrimRight(msg, "\r\n"), "\n") {
			tracef(ctx, "server banner: %s", trunc(strings.TrimRight(l, "\r"), 160))
		}
		return nil
	}
	tracef(ctx, "local version: SSH-2.0-Go")

	var conn net.Conn
	var err error
	if via != nil {
		tracef(ctx, "opening a direct-tcpip channel to %s through the jump host (timeout %s)", addr, o.Timeout)
		dctx, cancel := context.WithTimeout(ctx, o.Timeout)
		conn, err = via.DialContext(dctx, "tcp", addr)
		cancel()
		if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("no answer within %s (ssh.timeout)", o.Timeout)
		}
		if err == nil {
			tracef(ctx, "channel to %s open", addr)
		}
	} else {
		if h, _, e := net.SplitHostPort(addr); e == nil && net.ParseIP(h) == nil {
			rctx, cancel := context.WithTimeout(ctx, o.Timeout)
			ips, rerr := net.DefaultResolver.LookupHost(rctx, h)
			cancel()
			if rerr != nil {
				tracef(ctx, "resolving %s: %v", h, rerr)
			} else {
				tracef(ctx, "%s resolves to %s", h, strings.Join(ips, ", "))
			}
		}
		tracef(ctx, "connecting to %s (timeout %s)", addr, o.Timeout)
		d := net.Dialer{Timeout: o.Timeout, KeepAlive: 30 * time.Second}
		conn, err = d.DialContext(ctx, "tcp", addr)
		if err == nil {
			tracef(ctx, "connection established: %s -> %s", conn.LocalAddr(), conn.RemoteAddr())
		}
	}
	if err != nil {
		tracef(ctx, "connect failed: %v", plainDialErr(err))
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, &hopErr{StageConnect, err}
	}
	conn = &versionConn{Conn: conn, ctx: ctx}
	tracef(ctx, "SSH handshake (timeout %s), user %q, %d authentication method(s)", o.Handshake, user, len(auth))
	// Bound the handshake by ctx and the timeout; the timer pauses while
	// the user is asked about the host key.
	var timedOut atomic.Bool
	timer := time.AfterFunc(o.Handshake, func() { timedOut.Store(true); conn.Close() })
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	pause := func(p bool) {
		if p {
			timer.Stop()
		} else {
			timer.Reset(o.Handshake)
		}
	}
	cfg.HostKeyCallback = hk.callback(ctx, pause)
	cc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	timer.Stop()
	stop()
	if err != nil {
		tracef(ctx, "handshake failed: %v", err)
		conn.Close()
		var hkErr *HostKeyError
		var unk *UnknownHostError
		var rej *HostKeyRejectedError
		switch {
		case ctx.Err() != nil:
			return nil, nil, ctx.Err()
		case errors.As(err, &hkErr), errors.As(err, &unk), errors.As(err, &rej):
			return nil, nil, &hopErr{StageHostKey, unwrapHostKey(err)}
		case timedOut.Load():
			return nil, nil, &hopErr{StageHandshake, fmt.Errorf("ssh handshake timed out after %s (ssh.handshake_timeout)", o.Handshake)}
		case strings.Contains(err.Error(), "unable to authenticate"):
			return nil, nil, &hopErr{StageAuth, err}
		}
		return nil, nil, &hopErr{StageHandshake, err}
	}
	tracef(ctx, "authenticated as %q; server %s", user, cc.ServerVersion())
	return ssh.NewClient(cc, chans, reqs), conn, nil
}

// unwrapHostKey returns the host key error itself, without the "ssh:
// handshake failed:" prefix.
func unwrapHostKey(err error) error {
	var hkErr *HostKeyError
	var unk *UnknownHostError
	var rej *HostKeyRejectedError
	switch {
	case errors.As(err, &hkErr):
		return hkErr
	case errors.As(err, &unk):
		return unk
	case errors.As(err, &rej):
		return rej
	}
	return err
}

// Tunnel is an SSH connection to a jump host that opens TCP connections
// from there (like ssh -J / -L).
type Tunnel struct {
	client *ssh.Client
}

// DialTunnel connects to the jump host h with sec. sec is not retained: the
// caller wipes it.
func DialTunnel(ctx context.Context, h model.Host, sec *secret.Secret, o SSHOptions) (*Tunnel, error) {
	client, err := dialClient(ctx, h, sec, o.withDefaults())
	if err != nil {
		return nil, fmt.Errorf("jump host %s: %w", h.Name, err)
	}
	return &Tunnel{client: client}, nil
}

// Dial opens a TCP connection to addr from the jump host.
func (t *Tunnel) Dial(ctx context.Context, addr string) (net.Conn, error) {
	c, err := t.client.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("via the jump host: %w", err)
	}
	return c, nil
}

// Close ends the SSH connection and every connection through it.
func (t *Tunnel) Close() error { return t.client.Close() }

func startShell(client *ssh.Client, termName string, cols, rows int) (*Conn, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}
	if err := sess.RequestPty(termName, max(rows, 1), max(cols, 1), modes); err != nil {
		sess.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	sess.Stderr = nil // with a PTY stderr is merged into stdout
	if err := sess.Shell(); err != nil {
		sess.Close()
		return nil, fmt.Errorf("start shell: %w", err)
	}
	c := &Conn{client: client, sess: sess, stdin: stdin, stdout: stdout, done: make(chan struct{})}
	go func() {
		err := sess.Wait()
		var exitErr *ssh.ExitError
		var missing *ssh.ExitMissingError
		switch {
		case err == nil:
		case errors.As(err, &exitErr):
			err = fmt.Errorf("remote shell exited with status %d", exitErr.ExitStatus())
		case errors.As(err, &missing):
			err = errors.New("connection lost")
		}
		c.finish(err)
	}()
	return c, nil
}

func (c *Conn) finish(err error) {
	c.once.Do(func() {
		c.err = err
		c.client.Close()
		if c.via != nil {
			c.via.Close() // the jump host
		}
		close(c.done)
	})
}

// keepalive probes the server periodically. The probe is a channel request
// on the session channel, not a global request: servers answer unknown
// channel requests with SSH_MSG_CHANNEL_FAILURE, while some (MikroTik
// RouterOS 6/7) answer an unknown global request with SSH_MSG_UNIMPLEMENTED,
// which x/crypto/ssh treats as a protocol error and drops the connection.
func (c *Conn) keepalive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			res := make(chan error, 1)
			go func() {
				_, err := c.sess.SendRequest("keepalive@openssh.com", true, nil)
				res <- err
			}()
			select {
			case err := <-res:
				if err != nil {
					// The channel also fails when the shell has just exited:
					// let that (clean) ending win.
					select {
					case <-c.done:
					case <-time.After(200 * time.Millisecond):
						c.finish(errors.New("connection lost (keepalive failed)"))
					}
					return
				}
			case <-time.After(3 * every):
				c.finish(errors.New("connection lost (keepalive timeout)"))
				return
			case <-c.done:
				return
			}
		}
	}
}

// Read reads shell output.
func (c *Conn) Read(p []byte) (int, error) { return c.stdout.Read(p) }

// Write sends input to the shell.
func (c *Conn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

// Resize changes the PTY size.
func (c *Conn) Resize(cols, rows int) error {
	return c.sess.WindowChange(max(rows, 1), max(cols, 1))
}

// Done is closed when the connection ends; Err then reports why (nil = clean exit).
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns the reason the connection ended.
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close terminates the connection.
func (c *Conn) Close() error {
	c.finish(nil)
	return nil
}

// AuthFromSecret builds the authentication of a vault credential (a
// password or a private key). The methods refer to sec: wipe it only after
// the connection is established.
func AuthFromSecret(sec *secret.Secret) ([]ssh.AuthMethod, error) { return authMethods(sec) }

// authMethods builds auth from the secret. The password is converted to a
// string only inside the callbacks, during the handshake.
func authMethods(sec *secret.Secret) ([]ssh.AuthMethod, error) {
	if sec == nil || sec.IsZero() {
		return nil, errors.New("empty credential in vault")
	}
	if sec.Kind == secret.PrivateKey {
		signer, err := ssh.ParsePrivateKey(sec.Value)
		if err != nil {
			var pp *ssh.PassphraseMissingError
			if errors.As(err, &pp) {
				return nil, errors.New("private key is passphrase-protected (not supported)")
			}
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	pw := sec.Value
	return []ssh.AuthMethod{
		ssh.PasswordCallback(func() (string, error) { return string(pw), nil }),
		ssh.KeyboardInteractive(func(_, _ string, questions []string, echos []bool) ([]string, error) {
			ans := make([]string, len(questions))
			for i, q := range questions {
				if !echos[i] || strings.Contains(strings.ToLower(q), "password") {
					ans[i] = string(pw)
				}
			}
			return ans, nil
		}),
	}, nil
}

func cleanAuthErr(err error) error {
	if strings.Contains(err.Error(), "unable to authenticate") {
		return errors.New("authentication failed")
	}
	return err
}

// knownAlgorithms returns host key algorithms for keys pinned for addr.
func knownAlgorithms(file, addr string) []string {
	if file == "" {
		return nil
	}
	khMu.Lock()
	defer khMu.Unlock()
	cb, err := knownhosts.New(file)
	if err != nil {
		return nil
	}
	pub, _, _ := ed25519.GenerateKey(nil)
	probe, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil
	}
	var ke *knownhosts.KeyError
	if err := cb(addr, &net.TCPAddr{}, probe); !errors.As(err, &ke) {
		return nil
	}
	var algos []string
	for _, k := range ke.Want {
		switch t := k.Key.Type(); t {
		case ssh.KeyAlgoRSA:
			algos = append(algos, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA)
		default:
			algos = append(algos, t)
		}
	}
	return algos
}
