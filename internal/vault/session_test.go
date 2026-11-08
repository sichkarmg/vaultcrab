// SPDX-License-Identifier: MIT

package vault

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The session key of /unlock is kept for a restart and dropped when the
// vault is locked (by vaultcrab or by bw).
func TestSessionKey(t *testing.T) {
	f, c := newFake(t, Locked)
	ctx := context.Background()
	if c.HasSession() {
		t.Fatal("session before unlock")
	}
	if err := c.Unlock(ctx, []byte(f.password)); err != nil {
		t.Fatal(err)
	}
	k := c.TakeSession()
	if string(k) != "SESSIONKEY" || c.HasSession() {
		t.Fatalf("session %q, still held %v", k, c.HasSession())
	}
	c.AdoptSession(k)
	if err := c.Lock(ctx); err != nil || c.HasSession() {
		t.Fatalf("lock: %v, session kept %v", err, c.HasSession())
	}
	if string(k) == "SESSIONKEY" {
		t.Fatal("session key not wiped")
	}
	// bw locked by itself: the key opens nothing any more
	c.Unlock(ctx, []byte(f.password))
	f.mu.Lock()
	f.status = Locked
	f.mu.Unlock()
	if _, err := c.Status(ctx); err != nil || c.HasSession() {
		t.Fatalf("status: %v, session kept %v", err, c.HasSession())
	}
}

type captureSpawner struct {
	argv, env []string
}

func (c *captureSpawner) Start(argv, env []string) (Process, error) {
	c.argv, c.env = argv, env
	return nil, errors.New("not started")
}

// A restarted bw serve gets the session key in its environment, never on
// its command line.
func TestServeSessionEnv(t *testing.T) {
	sp := &captureSpawner{}
	bw, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true(1)")
	}
	_, err = StartServer(context.Background(), ServeOptions{BW: bw, Spawn: sp, Session: []byte("SESSIONKEY")})
	if err == nil {
		t.Fatal("spawner error lost")
	}
	if strings.Contains(strings.Join(sp.argv, " "), "SESSIONKEY") {
		t.Fatalf("session key in argv: %v", sp.argv)
	}
	found := false
	for _, e := range sp.env {
		found = found || e == "BW_SESSION=SESSIONKEY"
	}
	if !found {
		t.Fatalf("BW_SESSION not in env: %v", sp.env)
	}
	StartServer(context.Background(), ServeOptions{BW: bw, Spawn: sp})
	for _, e := range sp.env {
		if strings.HasPrefix(e, "BW_SESSION") {
			t.Fatal("BW_SESSION without a session")
		}
	}
}

func TestExitReason(t *testing.T) {
	stderr := []byte(`
/snapshot/clients/node_modules/node-fetch/lib/index.js:1501
			reject(new FetchError(` + "`request to ${request.url} failed`" + `));
			       ^
FetchError: request to https://vw.test/identity/connect/token failed, reason: socket hang up
    at ClientRequest.<anonymous> (/snapshot/clients/node_modules/node-fetch/lib/index.js:1501:11)
`)
	if got := ExitReason(stderr); got != "FetchError: request to https://vw.test/identity/connect/token failed, reason: socket hang up" {
		t.Fatalf("reason %q", got)
	}
	if got := ExitReason([]byte("bw: fatal error, giving up\n")); got != "bw: fatal error, giving up" {
		t.Fatalf("fallback reason %q", got)
	}
	if ExitReason(nil) != "" || len(ExitReason([]byte(strings.Repeat("Error ", 100)))) > 200 {
		t.Fatal("empty or long reason")
	}
}

func TestDialAddr(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cases := []struct {
		srv  string
		env  []string
		want string
	}{
		{"https://vw.example.test", nil, "vw.example.test:443"},
		{"https://vw.example.test:8443/", nil, "vw.example.test:8443"},
		{"http://10.0.0.1", nil, "10.0.0.1:80"},
		{"https://vw.example.test", []string{"HTTPS_PROXY=proxy.test:3128"}, "proxy.test:3128"},
		{"https://vw.example.test", []string{"https_proxy=http://proxy.test"}, "proxy.test:80"},
		{"https://vw.example.test", []string{"HTTPS_PROXY=proxy.test:3128", "NO_PROXY=.example.test"}, "vw.example.test:443"},
	}
	for _, c := range cases {
		got, err := dialAddr(c.srv, c.env)
		if err != nil || got != c.want {
			t.Errorf("dialAddr(%q, %v) = %q, %v; want %q", c.srv, c.env, got, err, c.want)
		}
	}
	if _, err := dialAddr("::bad", nil); err == nil {
		t.Error("bad URL accepted")
	}
}

func TestReachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	c := NewClient("http://127.0.0.1:1")
	c.serverURL = "https://" + addr
	c.ProbeTimeout = time.Second
	if err := c.Reachable(context.Background()); err != nil {
		t.Fatalf("listening server: %v", err)
	}
	l.Close()
	if err := c.Reachable(context.Background()); !errors.Is(err, ErrServerUnreachable) {
		t.Fatalf("closed port: %v", err)
	}
}
