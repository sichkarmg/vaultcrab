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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		client.Close()
		return nil, err
	}
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
	port := h.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(h.Addr, strconv.Itoa(port))
	cfg := &ssh.ClientConfig{
		User:            h.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback(o.KnownHosts, o.StrictHosts),
		Timeout:         o.Timeout,
	}
	if o.Legacy {
		sup, weak := ssh.SupportedAlgorithms(), ssh.InsecureAlgorithms()
		cfg.KeyExchanges = append(sup.KeyExchanges, weak.KeyExchanges...)
		cfg.Ciphers = append(sup.Ciphers, weak.Ciphers...)
		cfg.MACs = append(sup.MACs, weak.MACs...)
		cfg.HostKeyAlgorithms = append(sup.HostKeys, weak.HostKeys...)
	}
	// Prefer the key types already pinned for this host, so that the server
	// is not asked for another algorithm we would then report as a mismatch.
	if algos := knownAlgorithms(o.KnownHosts, addr); algos != nil {
		cfg.HostKeyAlgorithms = algos
	}

	d := net.Dialer{Timeout: o.Timeout, KeepAlive: 30 * time.Second}
	tcp, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// Bound the handshake by ctx and the timeout.
	stop := context.AfterFunc(ctx, func() { tcp.Close() })
	_ = tcp.SetDeadline(time.Now().Add(o.Handshake))
	cc, chans, reqs, err := ssh.NewClientConn(tcp, addr, cfg)
	stop()
	if err != nil {
		tcp.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, fmt.Errorf("ssh handshake timed out after %s (ssh.handshake_timeout)", o.Handshake)
		}
		return nil, cleanAuthErr(err)
	}
	_ = tcp.SetDeadline(time.Time{})
	return ssh.NewClient(cc, chans, reqs), nil
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

// hostKeyCallback implements trust-on-first-use with our own known_hosts.
func hostKeyCallback(file string, strict bool) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if file == "" {
			if strict {
				return &UnknownHostError{Host: hostname, Fingerprint: ssh.FingerprintSHA256(key)}
			}
			return nil
		}
		khMu.Lock()
		defer khMu.Unlock()
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(file, os.O_CREATE|os.O_RDONLY, 0o600)
		if err != nil {
			return err
		}
		f.Close()
		cb, err := knownhosts.New(file)
		if err != nil {
			return fmt.Errorf("known_hosts: %w", err)
		}
		err = cb(hostname, remote, key)
		var ke *knownhosts.KeyError
		if err == nil || !errors.As(err, &ke) {
			return err
		}
		if len(ke.Want) > 0 {
			return &HostKeyError{Host: hostname, Fingerprint: ssh.FingerprintSHA256(key), File: file, Line: ke.Want[0].Line}
		}
		if strict {
			return &UnknownHostError{Host: hostname, Fingerprint: ssh.FingerprintSHA256(key)}
		}
		af, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer af.Close()
		_, err = fmt.Fprintln(af, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
		return err
	}
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
