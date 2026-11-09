// SPDX-License-Identifier: MIT

package tunnel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// socksDial connects to addr (host:port, a domain name sent as such)
// through the SOCKS5 proxy at proxyAddr.
func socksDial(proxyAddr, addr string) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	host, ps, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(ps)
	c.Write([]byte{5, 1, 0})
	m := make([]byte, 2)
	if _, err := io.ReadFull(c, m); err != nil || m[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("greeting %v %v", m, err)
	}
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port))
	c.Write(req)
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil {
		c.Close()
		return nil, err
	}
	if rep[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("socks reply %d", rep[1])
	}
	return c, nil
}

// fakeSSH stands for the SSH connection: it dials directly; lose() ends
// the current connection; fail makes the next attempts fail.
type fakeSSH struct {
	mu       sync.Mutex
	done     chan struct{}
	leases   int
	released int
	attempts int
	fail     error
}

func (f *fakeSSH) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

func (f *fakeSSH) connect(context.Context) (Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.fail != nil {
		return Lease{}, f.fail
	}
	f.leases++
	done := make(chan struct{})
	f.done = done
	var once sync.Once
	return Lease{Client: f, Done: done, Release: func() {
		once.Do(func() {
			f.mu.Lock()
			f.released++
			f.mu.Unlock()
		})
	}}, nil
}

func (f *fakeSSH) lose() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.done)
}

func (f *fakeSSH) set(err error) {
	f.mu.Lock()
	f.fail = err
	f.mu.Unlock()
}

func (f *fakeSSH) count() (attempts, leases, released int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts, f.leases, f.released
}

func waitState(t *testing.T, tn *Tunnel, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := tn.Status()
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("state %v (%v), want %v", st.State, st.Err, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func get(t *testing.T, url string) string {
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

func webServer(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "proxmox")
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// A local tunnel listens on 127.0.0.1 only, forwards to the target, counts
// the connections, reconnects after the loss of the connection and stops.
func TestLocalTunnelLifecycle(t *testing.T) {
	target := webServer(t)
	f := &fakeSSH{}
	var notes atomic.Int32
	tn, err := Start(context.Background(), Spec{Target: target}, f.connect,
		Options{Backoff: 10 * time.Millisecond, Notify: func() { notes.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Stop()
	st := waitState(t, tn, Active)
	if !strings.HasPrefix(st.Addr, "127.0.0.1:") || st.Port == 0 {
		t.Fatalf("listens on %s", st.Addr)
	}
	if got := get(t, "http://"+st.Addr); got != "proxmox" {
		t.Fatalf("got %q", got)
	}
	if st := tn.Status(); st.Total != 1 {
		t.Fatalf("total %d", st.Total)
	}
	if notes.Load() == 0 {
		t.Error("no notification")
	}

	// the connection is lost: connected again, the port is the same
	f.lose()
	waitFor(t, func() bool { _, l, _ := f.count(); return l == 2 })
	st2 := waitState(t, tn, Active)
	if st2.Port != st.Port {
		t.Fatalf("port changed %d -> %d", st.Port, st2.Port)
	}
	if _, leases, released := f.count(); leases != 2 || released != 1 {
		t.Fatalf("leases %d released %d", leases, released)
	}
	if got := get(t, "http://"+st.Addr); got != "proxmox" {
		t.Fatalf("after the reconnect: %q", got)
	}

	// Restart drops and connects again
	tn.Restart()
	waitFor(t, func() bool { _, l, _ := f.count(); return l == 3 })
	waitState(t, tn, Active)

	tn.Stop()
	if st := tn.Status(); st.State != Stopped {
		t.Fatalf("state %v", st.State)
	}
	if _, leases, released := f.count(); released != leases {
		t.Fatalf("leases %d released %d", leases, released)
	}
	if _, err := net.DialTimeout("tcp", st.Addr, time.Second); err == nil {
		t.Fatal("the port is still open")
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Failed attempts are retried with a growing delay up to the limit; then
// the tunnel fails, and a new local connection tries again (and is served
// once connected). A permanent error fails at once.
func TestReconnectLimitAndWake(t *testing.T) {
	target := webServer(t)
	f := &fakeSSH{}
	f.set(errors.New("no route to host"))
	tn, err := Start(context.Background(), Spec{Target: target}, f.connect,
		Options{Backoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond, Attempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Stop()
	st := waitState(t, tn, Failed)
	if a, _, _ := f.count(); a != 3 {
		t.Fatalf("attempts %d", a)
	}
	if st.Err == nil || !strings.Contains(st.Err.Error(), "no route") {
		t.Fatalf("err %v", st.Err)
	}
	time.Sleep(50 * time.Millisecond)
	if a, _, _ := f.count(); a != 3 {
		t.Fatalf("attempts go on while failed: %d", a)
	}
	// the jump host is back: a request wakes the tunnel and is served
	f.set(nil)
	if got := get(t, "http://"+st.Addr); got != "proxmox" {
		t.Fatalf("got %q", got)
	}
	waitState(t, tn, Active)

	perm := errors.New("authentication failed")
	f2 := &fakeSSH{}
	f2.set(perm)
	tn2, err := Start(context.Background(), Spec{Target: target}, f2.connect,
		Options{Backoff: 5 * time.Millisecond, Permanent: func(err error) bool { return errors.Is(err, perm) }})
	if err != nil {
		t.Fatal(err)
	}
	defer tn2.Stop()
	waitState(t, tn2, Failed)
	if a, _, _ := f2.count(); a != 1 {
		t.Fatalf("a permanent error retried: %d", a)
	}
}

// A busy port: the next free one is used and the note says so; port 0 is
// any free port; FreePort suggests one.
func TestBusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	ln, note, err := Listen("", port)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := ln.Addr().(*net.TCPAddr)
	if got.Port == port || !got.IP.IsLoopback() {
		t.Fatalf("listens on %s", got)
	}
	if note != fmt.Sprintf("port %d is busy: using %d", port, got.Port) {
		t.Fatalf("note %q", note)
	}
	if p := FreePort("", port); p == port || p == 0 {
		t.Fatalf("free port %d", p)
	}
	tn, err := Start(context.Background(), Spec{Target: "127.0.0.1:1", Port: port}, (&fakeSSH{}).connect, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Stop()
	if st := tn.Status(); st.Port == port || !strings.Contains(st.Note, "busy") {
		t.Fatalf("status %+v", st)
	}
}

func TestLoopbackOnly(t *testing.T) {
	for _, b := range []string{"", "127.0.0.1", "localhost", "::1", "127.0.0.2"} {
		if !IsLoopback(b) {
			t.Errorf("%q not loopback", b)
		}
	}
	for _, b := range []string{"0.0.0.0", "::", "192.168.1.5"} {
		if IsLoopback(b) {
			t.Errorf("%q loopback", b)
		}
	}
	ln, _, err := Listen("", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if a := ln.Addr().(*net.TCPAddr); a.IP.String() != "127.0.0.1" {
		t.Fatalf("default bind %s", a)
	}
	if _, err := Start(context.Background(), Spec{Target: "nohostport"}, (&fakeSSH{}).connect, Options{}); err == nil {
		t.Fatal("a target without a port accepted")
	}
}

// A SOCKS5 tunnel connects each client to the address it asks for (a
// domain name resolved by the far side), and refuses other commands.
func TestSOCKS(t *testing.T) {
	target := webServer(t)
	f := &fakeSSH{}
	tn, err := Start(context.Background(), Spec{Kind: SOCKS}, f.connect, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Stop()
	st := waitState(t, tn, Active)
	_, port, _ := net.SplitHostPort(target)
	tr := &http.Transport{DisableKeepAlives: true, DialContext: func(_ context.Context, _, addr string) (net.Conn, error) {
		return socksDial(st.Addr, addr)
	}}
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	r, err := c.Get("http://localhost:" + port)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if string(b) != "proxmox" {
		t.Fatalf("got %q", b)
	}
	// a refused connection: the SOCKS error
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	fp := free.Addr().(*net.TCPAddr).Port
	free.Close()
	if _, err := socksDial(st.Addr, "127.0.0.1:"+strconv.Itoa(fp)); err == nil || !strings.Contains(err.Error(), "reply 5") {
		t.Fatal("dial to a closed port succeeded")
	}
	// BIND is not supported
	conn, err := net.Dial("tcp", st.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte{5, 1, 0})
	br := bufio.NewReader(conn)
	m := make([]byte, 2)
	io.ReadFull(br, m)
	conn.Write([]byte{5, 2, 0, 1, 127, 0, 0, 1, 0, 80})
	rep := make([]byte, 2)
	io.ReadFull(br, rep)
	if rep[1] != socksCmdUnsupported {
		t.Fatalf("BIND reply %v", rep)
	}
}
