// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// The trace names every step like ssh -vvv and never holds the secret.
func TestTraceDirect(t *testing.T) {
	srv := newTestServer(t, nil)
	tr := NewTrace()
	ctx := WithTrace(context.Background(), tr)
	sec := secret.FromString("pw")
	c, err := DialSSH(ctx, srv.host("u"), &sec, 80, 24, SSHOptions{KnownHosts: filepath.Join(t.TempDir(), "kh")})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	got := tr.String()
	for _, want := range []string{"authentication: password / keyboard-interactive from the vault", "connecting to 127.0.0.1:",
		"connection established", "remote protocol version: SSH-2.0-Go", "local version: SSH-2.0-Go",
		"server host key: ssh-ed25519 SHA256:", "host key not in known_hosts", `user "u"`, `authenticated as "u"`, "PTY xterm-256color 80x24"} {
		if !strings.Contains(got, want) {
			t.Errorf("trace lacks %q:\n%s", want, got)
		}
	}
	for _, l := range tr.Lines() {
		if !strings.HasPrefix(l, "debug ") {
			t.Errorf("line %q", l)
		}
	}

	// failures: a wrong password, a closed port; the password never shows
	tr = NewTrace()
	bad := secret.FromString("wrong-secret-xyz")
	_, err = DialSSH(WithTrace(context.Background(), tr), srv.host("u"), &bad, 80, 24, SSHOptions{KnownHosts: filepath.Join(t.TempDir(), "kh")})
	if err == nil || !strings.Contains(tr.String(), "handshake failed: ssh: handshake failed: ssh: unable to authenticate") {
		t.Fatalf("%v\n%s", err, tr)
	}
	if strings.Contains(tr.String(), "wrong-secret-xyz") {
		t.Fatal("the password is in the trace")
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	h := srv.host("u")
	_, p, _ := net.SplitHostPort(addr)
	h.Port = atoi(p)
	tr = NewTrace()
	_, err = DialSSH(WithTrace(context.Background(), tr), h, &sec, 80, 24, SSHOptions{Timeout: time.Second})
	if err == nil || !strings.Contains(tr.String(), "connect failed:") {
		t.Fatalf("%v\n%s", err, tr)
	}
	// no trace in the context: nothing recorded, nothing breaks
	var none *Trace
	none.Add("x")
	if none.String() != "" || TraceOf(context.Background()) != nil {
		t.Fatal("nil trace")
	}
}

// Through a jump host: both hops, the channel, the credentials described.
func TestTraceJump(t *testing.T) {
	p := newJumpPair(t)
	j, tg := p.hops(HostKeyAcceptNew, nil)
	j.AuthInfo, tg.AuthInfo = "public key A", "public key B"
	tr := NewTrace()
	ctx, cancel := context.WithTimeout(WithTrace(context.Background(), tr), 10*time.Second)
	defer cancel()
	c, err := DialJump(ctx, j, tg, 80, 24, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	got := tr.String()
	for _, want := range []string{`jump host lab-jump (127.0.0.1:`, "public key A", "target 127.0.0.1:", "public key B",
		"opening a direct-tcpip channel to", "channel to 127.0.0.1:", "shell started on the target"} {
		if !strings.Contains(got, want) {
			t.Errorf("trace lacks %q:\n%s", want, got)
		}
	}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
