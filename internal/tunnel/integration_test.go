// SPDX-License-Identifier: MIT

//go:build integration

package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/sichkarmg/vaultcrab/internal/connect"
)

// A real jump host on the test VMs (scripts/e2e.sh): VAULTCRAB_IT_JUMP is
// user@host[:port], VAULTCRAB_IT_JUMP_KEY a TEST key it accepts.
func itJump(t *testing.T) (connect.Hop, string) {
	t.Helper()
	j, kf := os.Getenv("VAULTCRAB_IT_JUMP"), os.Getenv("VAULTCRAB_IT_JUMP_KEY")
	if j == "" || kf == "" {
		t.Skip("VAULTCRAB_IT_JUMP, VAULTCRAB_IT_JUMP_KEY not set")
	}
	key, err := connect.SignerFromFile(kf, nil)
	if err != nil {
		t.Fatal(err)
	}
	user, hp, ok := strings.Cut(j, "@")
	if !ok {
		t.Fatalf("%q: want user@host[:port]", j)
	}
	if _, _, err := net.SplitHostPort(hp); err != nil {
		hp = net.JoinHostPort(hp, "22")
	}
	kh := filepath.Join(t.TempDir(), "known_hosts")
	host, _, _ := net.SplitHostPort(hp)
	return connect.Hop{Name: hp, Addr: hp, User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(key)},
		Keys: connect.HostKeyPolicy{File: kh, Mode: connect.HostKeyAcceptNew}}, host
}

// banner reads the first line of the server behind the tunnel.
func banner(t *testing.T, addr string) string {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	l, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("read through the tunnel: %v", err)
	}
	return l
}

// isLocal reports whether host is an address of this machine (the jump
// host is the VM the test runs on).
func isLocal(host string) bool {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.String() == host {
			return true
		}
	}
	return false
}

// Forward to the SSH server of the jump host itself (127.0.0.1:22 there):
// its banner comes through the tunnel; the connection is lost and made
// again; a busy local port is replaced. When the jump host is this VM, an
// HTTP server of the test is reached through it too.
func TestIntegrationTunnel(t *testing.T) {
	hop, jumpHost := itJump(t)
	var mu sync.Mutex
	var cur *connect.JumpClient
	dials := 0
	connector := func(ctx context.Context) (Lease, error) {
		jc, err := connect.DialJumpHost(ctx, hop, connect.SSHOptions{})
		if err != nil {
			return Lease{}, err
		}
		mu.Lock()
		cur, dials = jc, dials+1
		mu.Unlock()
		return Lease{Client: jc, Done: jc.Done(), Release: func() { jc.Close() }}, nil
	}
	ctx := context.Background()
	o := Options{Backoff: 200 * time.Millisecond, Log: t.Logf}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	tn, err := Start(ctx, Spec{Target: "127.0.0.1:22", Port: port}, connector, o)
	if err != nil {
		t.Fatal(err)
	}
	defer tn.Stop()
	st := waitStateIT(t, tn, Active)
	if st.Port == port || !strings.Contains(st.Note, "busy") || !strings.HasPrefix(st.Addr, "127.0.0.1:") {
		t.Fatalf("busy port: %+v", st)
	}
	if b := banner(t, st.Addr); !strings.HasPrefix(b, "SSH-2.0-") {
		t.Fatalf("banner %q", b)
	}

	// the jump connection is lost: the tunnel connects again
	mu.Lock()
	cur.Close()
	mu.Unlock()
	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		n := dials
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not connected again")
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitStateIT(t, tn, Active)
	if b := banner(t, st.Addr); !strings.HasPrefix(b, "SSH-2.0-") {
		t.Fatalf("banner after the reconnect %q", b)
	}

	if !isLocal(jumpHost) {
		t.Logf("the jump host %s is another machine: the HTTP check needs it to be this one", jumpHost)
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "pve-ok") })}
	go srv.Serve(ln)
	defer srv.Close()
	web, err := Start(ctx, Spec{Target: ln.Addr().String()}, connector, o)
	if err != nil {
		t.Fatal(err)
	}
	defer web.Stop()
	ws := waitStateIT(t, web, Active)
	c := &http.Client{Timeout: 20 * time.Second}
	r, err := c.Get("http://" + ws.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b := make([]byte, 16)
	n, _ := r.Body.Read(b)
	if string(b[:n]) != "pve-ok" {
		t.Fatalf("http through the tunnel: %q", b[:n])
	}
}

func waitStateIT(t *testing.T, tn *Tunnel, want State) Status {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		st := tn.Status()
		if st.State == want {
			return st
		}
		if st.State == Failed || time.Now().After(deadline) {
			t.Fatalf("state %v (%v), want %v", st.State, st.Err, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
