// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
)

// fakeJump is a connection to a jump host that dials directly.
type fakeJump struct {
	hop  connect.Hop
	done chan struct{}
	once sync.Once
}

func (f *fakeJump) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}
func (f *fakeJump) Done() <-chan struct{} { return f.done }
func (f *fakeJump) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

type jumpConns struct {
	mu    sync.Mutex
	conns []*fakeJump
	fail  error
}

func (j *jumpConns) connect(_ context.Context, h connect.Hop, _ connect.SSHOptions) (JumpConn, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.fail != nil {
		return nil, j.fail
	}
	c := &fakeJump{hop: h, done: make(chan struct{})}
	j.conns = append(j.conns, c)
	return c, nil
}

func (j *jumpConns) list() []*fakeJump {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]*fakeJump(nil), j.conns...)
}

func waitTunnel(t *testing.T, s *Service, id int, want TunnelState) TunnelInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, ti := range s.Tunnels() {
			if ti.ID == id && ti.State == want {
				return ti
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("tunnel %d not %v: %+v", id, want, s.Tunnels())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	r, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// Two tunnels through the same jump host share one connection, made with
// the jump host's credential; stopping the last one closes it. A lost
// connection is made again, a refused one fails the tunnel at once.
func TestTunnelsShareTheJumpConnection(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "pve") }))
	defer web.Close()
	target := strings.TrimPrefix(web.URL, "http://")
	e := newJumpEnv(t, []config.JumpTemplate{{Name: "Lab", Jump: "bastion"}})
	jc := &jumpConns{}
	e.s.opts.JumpConnect = jc.connect
	e.s.opts.TunnelBackoff = 10 * time.Millisecond
	var opened []string
	e.s.opts.OpenURL = func(u string) error { opened = append(opened, u); return nil }
	defer e.s.Shutdown()

	via, err := e.s.ResolveJump("lab")
	if err != nil || via != "t:Lab" {
		t.Fatal(via, err)
	}
	a, err := e.s.StartTunnel(TunnelSpec{Via: via, Target: target, Open: "https://localhost:{port}/ui"})
	if err != nil {
		t.Fatal(err)
	}
	a = waitTunnel(t, e.s, a.ID, TunnelActive)
	if !strings.HasPrefix(a.Addr, "127.0.0.1:") || a.Via != "Lab" {
		t.Fatalf("%+v", a)
	}
	if got := httpGet(t, "http://"+a.Addr); got != "pve" {
		t.Fatalf("got %q", got)
	}
	h, err := e.s.ResolveJump("bastion")
	if err != nil || h != "h:j1" {
		t.Fatal(h, err)
	}
	b, err := e.s.StartTunnel(TunnelSpec{Via: h, SOCKS: true})
	if err != nil {
		t.Fatal(err)
	}
	waitTunnel(t, e.s, b.ID, TunnelActive)
	conns := jc.list()
	if len(conns) != 1 {
		t.Fatalf("%d connections to the jump host", len(conns))
	}
	if j := conns[0].hop; j.Addr != "203.0.113.10:22" || j.User != "ops" || len(j.Auth) == 0 || !strings.Contains(j.AuthInfo, "bastion") {
		t.Fatalf("jump hop %+v", j)
	}
	if u, err := e.s.OpenTunnel(a.ID); err != nil || u != fmt.Sprintf("https://localhost:%d/ui", a.Port) || len(opened) != 1 {
		t.Fatalf("open %q %v %v", u, err, opened)
	}

	// the connection is lost: both tunnels connect again, sharing the new one
	conns[0].Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(jc.list()) != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	waitTunnel(t, e.s, a.ID, TunnelActive)
	waitTunnel(t, e.s, b.ID, TunnelActive)
	if n := len(jc.list()); n != 2 {
		t.Fatalf("%d connections after the loss", n)
	}
	if got := httpGet(t, "http://"+a.Addr); got != "pve" {
		t.Fatalf("after the loss: %q", got)
	}

	e.s.StopTunnel(a.ID)
	second := jc.list()[1]
	select {
	case <-second.done:
		t.Fatal("closed while the other tunnel uses it")
	default:
	}
	e.s.StopTunnel(b.ID)
	select {
	case <-second.done:
	case <-time.After(time.Second):
		t.Fatal("the connection to the jump host stays open without users")
	}
	if len(e.s.Tunnels()) != 0 {
		t.Fatal("stopped tunnels listed")
	}

	// a refused authentication: failed at once, not retried
	jc.mu.Lock()
	jc.fail = &connect.JumpError{Stage: connect.StageAuth, Jump: "bastion", User: "ops", Err: errors.New("denied")}
	jc.mu.Unlock()
	c, err := e.s.StartTunnel(TunnelSpec{Via: via, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	f := waitTunnel(t, e.s, c.ID, TunnelFailed)
	if !strings.Contains(f.Err.Error(), "authentication failed") {
		t.Fatalf("err %v", f.Err)
	}
}

// Saved tunnels: resolved from the config, started once, saved back with
// the jump host's name.
func TestSavedTunnels(t *testing.T) {
	e := newJumpEnv(t, nil)
	jc := &jumpConns{}
	e.s.opts.JumpConnect = jc.connect
	defer e.s.Shutdown()
	if err := e.s.SaveJumpTemplates([]config.JumpTemplate{{Name: "Lab", Jump: "bastion"}}); err != nil {
		t.Fatal(err)
	}
	ts := []TunnelTemplate{
		{Name: "Proxmox web", Jump: "Lab", Target: "192.0.2.10:8006", Open: "https://localhost:{port}"},
		{Name: "proxy", Jump: "edge", Type: config.TunnelSOCKS, LocalPort: 0},
	}
	if err := e.s.SaveTunnels(ts); err != nil {
		t.Fatal(err)
	}
	if got := e.s.SavedTunnels(); len(got) != 2 || got[1].Kind() != config.TunnelSOCKS {
		t.Fatalf("%+v", got)
	}
	sp, err := e.s.TunnelSpecOf(e.s.SavedTunnels()[0])
	if err != nil || sp.Via != "t:Lab" || sp.Target != "192.0.2.10:8006" || sp.Name != "Proxmox web" {
		t.Fatalf("%+v %v", sp, err)
	}
	a, err := e.s.StartTunnel(sp)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.s.StartTunnel(sp)
	if err != nil || b.ID != a.ID || len(e.s.Tunnels()) != 1 {
		t.Fatalf("started twice: %d %d", a.ID, b.ID)
	}
	if u := a.TunnelURL(); u != fmt.Sprintf("https://localhost:%d", a.Port) || !SelfSigned(u) {
		t.Fatalf("url %q", u)
	}
	if _, err := e.s.TunnelSpecOf(TunnelTemplate{Name: "x", Jump: "nowhere", Target: "h:1"}); err == nil {
		t.Fatal("an unknown jump host accepted")
	}
	if _, err := e.s.ResolveJump("twin"); err == nil {
		t.Fatal("an ambiguous host name accepted")
	}
	if r := e.s.JumpRef("h:j2"); r != "edge" {
		t.Fatalf("ref %q", r)
	}
	if r := e.s.JumpRef("h:d1"); r != "d1" {
		t.Fatalf("ref of a name used twice %q", r)
	}
	if r := e.s.JumpRef("t:Lab"); r != "Lab" {
		t.Fatalf("ref %q", r)
	}
	if _, err := e.s.StartTunnel(TunnelSpec{Via: "t:Lab", Target: "nohost"}); err == nil {
		t.Fatal("a target without a port accepted")
	}
}

func TestBrowserArgv(t *testing.T) {
	if got := browserArgv([]string{"firefox", "--new-tab", "{url}"}, "https://localhost:1"); strings.Join(got, " ") != "firefox --new-tab https://localhost:1" {
		t.Fatal(got)
	}
	if got := browserArgv([]string{"chromium"}, "http://x"); strings.Join(got, " ") != "chromium http://x" {
		t.Fatal(got)
	}
	if got := browserArgv(nil, "http://x"); got[len(got)-1] != "http://x" {
		t.Fatal(got)
	}
	ti := TunnelInfo{Port: 1080, Spec: TunnelSpec{Bind: "0.0.0.0"}}
	if ti.LocalAddr() != "localhost:1080" || !ti.Exposed() {
		t.Fatal(ti.LocalAddr())
	}
	ti.Spec.Bind = "192.168.1.5"
	if ti.LocalAddr() != "192.168.1.5:1080" || ti.TunnelURL() != "http://192.168.1.5:1080" {
		t.Fatal(ti.LocalAddr(), ti.TunnelURL())
	}
}
