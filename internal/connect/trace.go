// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Trace collects the steps of one connection attempt, like `ssh -vvv`:
// name resolution, TCP, the versions, the host key and its check, the
// authentication methods offered and the result, the channel through a jump
// host, the PTY. It never holds a secret: keys appear by type and
// fingerprint, passwords only as "password". Safe for concurrent use.
type Trace struct {
	mu    sync.Mutex
	start time.Time
	lines []string
}

// NewTrace starts a trace.
func NewTrace() *Trace { return &Trace{start: time.Now()} }

// Add appends a step.
func (t *Trace) Add(format string, args ...any) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) >= 500 {
		return
	}
	t.lines = append(t.lines, fmt.Sprintf("debug %6.3fs: ", time.Since(t.start).Seconds())+fmt.Sprintf(format, args...))
}

// Lines returns the steps so far.
func (t *Trace) Lines() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.lines...)
}

// String returns the steps, one per line.
func (t *Trace) String() string { return strings.Join(t.Lines(), "\n") }

type traceKey struct{}

// WithTrace returns ctx carrying t: the connection steps under ctx are
// recorded there.
func WithTrace(ctx context.Context, t *Trace) context.Context {
	return context.WithValue(ctx, traceKey{}, t)
}

// TraceOf returns the trace of ctx (nil: none; Add on nil does nothing).
func TraceOf(ctx context.Context) *Trace {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	return t
}

func tracef(ctx context.Context, format string, args ...any) { TraceOf(ctx).Add(format, args...) }

// DescribeSecret names a vault credential for the trace: the key type and
// fingerprint of a private key, "password" otherwise.
func DescribeSecret(sec *secret.Secret) string {
	if sec == nil || sec.IsZero() {
		return "no credential"
	}
	if sec.Kind == secret.PrivateKey {
		if s, err := ssh.ParsePrivateKey(sec.Value); err == nil {
			return "public key " + DescribeKey(s.PublicKey())
		}
		return "private key (not parsed)"
	}
	return "password / keyboard-interactive"
}

// DescribeKey returns "type SHA256:...".
func DescribeKey(k ssh.PublicKey) string { return k.Type() + " " + ssh.FingerprintSHA256(k) }

// versionConn records the identification line of the server (the first
// line it sends) for the trace.
type versionConn struct {
	net.Conn
	ctx  context.Context
	buf  []byte
	done bool
}

func (c *versionConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if !c.done && n > 0 {
		c.buf = append(c.buf, p[:n]...)
		for {
			i := strings.IndexByte(string(c.buf), '\n')
			if i < 0 {
				if len(c.buf) > 1024 {
					c.done = true
				}
				break
			}
			line := strings.TrimRight(string(c.buf[:i]), "\r")
			c.buf = c.buf[i+1:]
			if strings.HasPrefix(line, "SSH-") {
				tracef(c.ctx, "remote protocol version: %s", line)
				c.done, c.buf = true, nil
				break
			}
			tracef(c.ctx, "server line before the version: %q", trunc(line, 120))
		}
	}
	return n, err
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
