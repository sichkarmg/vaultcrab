// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

func newKey(t *testing.T) (ed25519.PrivateKey, ssh.Signer) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, s
}

// jumpPair starts a jump server and a target server, both accepting
// clientKey (and the password pw for the user u).
type jumpPair struct {
	jump, target *testServer
	key          ssh.Signer
	kh           string
}

func newJumpPair(t *testing.T) *jumpPair {
	_, key := newKey(t)
	return &jumpPair{
		jump:   newTestServer(t, key.PublicKey()),
		target: newTestServer(t, key.PublicKey()),
		key:    key,
		kh:     filepath.Join(t.TempDir(), "known_hosts"),
	}
}

func (p *jumpPair) hops(mode HostKeyMode, ask func(context.Context, HostKeyQuestion) (HostKeyAnswer, error)) (Hop, Hop) {
	auth := []ssh.AuthMethod{ssh.PublicKeys(p.key)}
	j := Hop{Name: "lab-jump", Addr: p.jump.addr, User: "u", Auth: auth, Keys: HostKeyPolicy{File: p.kh, Mode: HostKeyAcceptNew}}
	tg := Hop{Addr: p.target.addr, User: "u", Auth: auth, Keys: HostKeyPolicy{File: p.kh, Mode: mode, Ask: ask}}
	return j, tg
}

func dialJump(t *testing.T, j, tg Hop, o SSHOptions) (*Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return DialJump(ctx, j, tg, 80, 24, o)
}

// The shell runs on the target, reached through a channel of the jump
// host; closing the connection closes both.
func TestJumpChain(t *testing.T) {
	p := newJumpPair(t)
	j, tg := p.hops(HostKeyAcceptNew, nil)
	c, err := dialJump(t, j, tg, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "welcome")
	c.Write([]byte("ping-through-jump"))
	readUntil(t, c, "ping-through-jump")
	p.jump.mu.Lock()
	reqs := strings.Join(p.jump.reqs, " ")
	p.jump.mu.Unlock()
	if !strings.Contains(reqs, "direct-tcpip:127.0.0.1:") {
		t.Errorf("target not reached through the jump host: %s", reqs)
	}
	if strings.Contains(reqs, "agent") {
		t.Errorf("agent forwarding requested: %s", reqs)
	}
	c.Close()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("not done")
	}
	select {
	case <-c.via.(*JumpClient).Done():
	case <-time.After(time.Second):
		t.Fatal("the jump connection is still open")
	}
	if _, _, err := c.via.(*JumpClient).c.SendRequest("x", true, nil); err == nil {
		t.Fatal("the jump connection is still open")
	}
	// both host keys were remembered (accept-new)
	b, _ := os.ReadFile(p.kh)
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Fatalf("known_hosts has %d lines:\n%s", n, b)
	}
}

// ask mode: a new key of the target is shown; Once connects without
// remembering, Forever remembers (not asked again), Reject fails at the
// host key stage of the target.
func TestJumpHostKeyAsk(t *testing.T) {
	p := newJumpPair(t)
	var asked []HostKeyQuestion
	answer := HostKeyReject
	ask := func(_ context.Context, q HostKeyQuestion) (HostKeyAnswer, error) {
		asked = append(asked, q)
		return answer, nil
	}
	j, tg := p.hops(HostKeyAsk, ask)

	_, err := dialJump(t, j, tg, SSHOptions{})
	var je *JumpError
	if !errors.As(err, &je) || !je.Target || je.Stage != StageHostKey {
		t.Fatalf("rejected: %v", err)
	}
	if len(asked) != 1 || asked[0].Role != "target" || asked[0].Fingerprint != ssh.FingerprintSHA256(p.target.hostKey.PublicKey()) {
		t.Fatalf("question %+v", asked)
	}

	answer = HostKeyOnce
	c, err := dialJump(t, j, tg, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if known(t, p.kh, p.target.addr, p.target.hostKey.PublicKey()) {
		t.Fatal("Once remembered the key")
	}

	answer = HostKeyForever
	c, err = dialJump(t, j, tg, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if !known(t, p.kh, p.target.addr, p.target.hostKey.PublicKey()) {
		t.Fatal("Forever did not remember the key")
	}
	n := len(asked)
	answer = HostKeyReject
	c, err = dialJump(t, j, tg, SSHOptions{})
	if err != nil || len(asked) != n {
		t.Fatalf("a remembered key was asked again: %v, %d questions", err, len(asked)-n)
	}
	c.Close()
}

// A changed key of a known address is refused in every mode, without
// asking and without remembering the new key.
func TestJumpHostKeyChanged(t *testing.T) {
	for _, mode := range []HostKeyMode{HostKeyAsk, HostKeyAcceptNew, HostKeyStrict} {
		p := newJumpPair(t)
		_, other := newKey(t)
		line := knownhosts.Line([]string{knownhosts.Normalize(p.target.addr)}, other.PublicKey())
		if err := os.WriteFile(p.kh, []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		asked := 0
		j, tg := p.hops(mode, func(context.Context, HostKeyQuestion) (HostKeyAnswer, error) {
			asked++
			return HostKeyForever, nil
		})
		_, err := dialJump(t, j, tg, SSHOptions{})
		var je *JumpError
		var hk *HostKeyError
		if !errors.As(err, &je) || !je.Target || je.Stage != StageHostKey || !errors.As(err, &hk) {
			t.Fatalf("mode %d: %v", mode, err)
		}
		if !strings.Contains(err.Error(), "HOST KEY CHANGED") || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("mode %d: message %q", mode, err)
		}
		if asked != 0 || known(t, p.kh, p.target.addr, p.target.hostKey.PublicKey()) {
			t.Fatalf("mode %d: asked %d times or remembered the new key", mode, asked)
		}
	}
}

// strict: an unknown target is refused.
func TestJumpHostKeyStrict(t *testing.T) {
	p := newJumpPair(t)
	j, tg := p.hops(HostKeyStrict, nil)
	_, err := dialJump(t, j, tg, SSHOptions{})
	var unk *UnknownHostError
	if !errors.As(err, &unk) {
		t.Fatalf("%v", err)
	}
}

// Every stage has its own message.
func TestJumpStages(t *testing.T) {
	p := newJumpPair(t)
	closed := func() string {
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		a := l.Addr().String()
		l.Close()
		return a
	}
	_, wrong := newKey(t)
	cases := []struct {
		name   string
		mod    func(j, tg *Hop)
		target bool
		stage  Stage
		msg    string
	}{
		{"jump unreachable", func(j, _ *Hop) { j.Addr = closed() }, false, StageConnect, "jump host lab-jump (127.0.0.1:"},
		{"target unreachable", func(_, tg *Hop) { tg.Addr = closed() }, true, StageConnect, "unreachable from the jump host lab-jump"},
		{"jump auth", func(j, _ *Hop) { j.Auth = []ssh.AuthMethod{ssh.PublicKeys(wrong)} }, false, StageAuth, "authentication failed on the jump host lab-jump (user u)"},
		{"target auth", func(_, tg *Hop) { tg.Auth = []ssh.AuthMethod{ssh.PublicKeys(wrong)}; tg.User = "bob" }, true, StageAuth, "authentication failed on the target 127.0.0.1:"},
		{"jump host key", func(j, _ *Hop) { j.Keys.Mode, j.Keys.File = HostKeyStrict, filepath.Join(t.TempDir(), "kh2") }, false, StageHostKey, "jump host lab-jump: unknown host"},
	}
	for _, c := range cases {
		j, tg := p.hops(HostKeyAcceptNew, nil)
		c.mod(&j, &tg)
		_, err := dialJump(t, j, tg, SSHOptions{})
		var je *JumpError
		if !errors.As(err, &je) || je.Target != c.target || je.Stage != c.stage || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("%s: %v (want %q)", c.name, err, c.msg)
		}
	}
}

// The handshake timer does not run while the user decides about a key.
func TestJumpAskPausesHandshakeTimer(t *testing.T) {
	p := newJumpPair(t)
	j, tg := p.hops(HostKeyAsk, func(context.Context, HostKeyQuestion) (HostKeyAnswer, error) {
		time.Sleep(600 * time.Millisecond)
		return HostKeyOnce, nil
	})
	c, err := dialJump(t, j, tg, SSHOptions{Handshake: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestSignerFromFile(t *testing.T) {
	priv, s := newKey(t)
	dir := t.TempDir()
	plain, _ := ssh.MarshalPrivateKey(priv, "")
	enc, _ := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("s3cret"))
	pf, ef := filepath.Join(dir, "plain"), filepath.Join(dir, "enc")
	os.WriteFile(pf, pem.EncodeToMemory(plain), 0o600)
	os.WriteFile(ef, pem.EncodeToMemory(enc), 0o600)

	calls := 0
	pass := func(pp string) func() ([]byte, error) {
		return func() ([]byte, error) { calls++; return []byte(pp), nil }
	}
	got, err := SignerFromFile(pf, pass("unused"))
	if err != nil || calls != 0 || string(got.PublicKey().Marshal()) != string(s.PublicKey().Marshal()) {
		t.Fatalf("plain: %v, %d calls", err, calls)
	}
	if _, err := SignerFromFile(ef, pass("wrong")); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	got, err = SignerFromFile(ef, pass("s3cret"))
	if err != nil || string(got.PublicKey().Marshal()) != string(s.PublicKey().Marshal()) {
		t.Fatalf("passphrase: %v", err)
	}
	if _, err := SignerFromFile(ef, nil); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("no prompt: %v", err)
	}
	if _, err := SignerFromFile(filepath.Join(dir, "missing"), nil); err == nil {
		t.Fatal("missing file accepted")
	}
	// the passphrase prompt can fail (canceled)
	if _, err := SignerFromFile(ef, func() ([]byte, error) { return nil, errors.New("canceled") }); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("canceled: %v", err)
	}
}

// Keys of the ssh-agent authenticate both hops; the agent is not
// forwarded.
func TestJumpAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket agent")
	}
	priv, key := newKey(t)
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "agent.sock")
	if len(sock) > 100 {
		sock = filepath.Join(os.TempDir(), "vc-agent-test.sock")
		os.Remove(sock)
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip(err)
	}
	defer l.Close()
	var served atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			served.Add(1)
			go agent.ServeAgent(kr, c)
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", sock)
	p := &jumpPair{jump: newTestServer(t, key.PublicKey()), target: newTestServer(t, key.PublicKey()), kh: filepath.Join(t.TempDir(), "kh")}
	a1, close1, err := Agent()
	if err != nil {
		t.Fatal(err)
	}
	defer close1()
	a2, close2, err := Agent()
	if err != nil {
		t.Fatal(err)
	}
	defer close2()
	j := Hop{Name: "j", Addr: p.jump.addr, User: "u", Auth: []ssh.AuthMethod{a1}}
	tg := Hop{Addr: p.target.addr, User: "u", Auth: []ssh.AuthMethod{a2}}
	c, err := dialJump(t, j, tg, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if served.Load() != 2 {
		t.Errorf("agent connections %d", served.Load())
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	if _, _, err := Agent(); err == nil {
		t.Error("no agent: no error")
	}
}

func known(t *testing.T, file, addr string, key ssh.PublicKey) bool {
	t.Helper()
	cb, err := knownhosts.New(file)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatal(err)
	}
	return cb(addr, &net.TCPAddr{}, key) == nil
}

// One connection to the jump host carries two shells on the target; a
// shell that ends does not close it, closing it ends the shells.
func TestJumpClientShared(t *testing.T) {
	p := newJumpPair(t)
	j, tg := p.hops(HostKeyAcceptNew, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	jc, err := DialJumpHost(ctx, j, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := DialVia(ctx, jc, j.Name, tg, 80, 24, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := DialVia(ctx, jc, j.Name, tg, 80, 24, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	<-a.Done()
	select {
	case <-jc.Done():
		t.Fatal("the jump connection ended with one shell")
	case <-b.Done():
		t.Fatal("the other shell ended")
	default:
	}
	// a TCP connection from the jump host (a tunnel) still works
	c, err := jc.DialContext(ctx, "tcp", p.target.addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	jc.Close()
	select {
	case <-b.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the shell outlived the jump connection")
	}
	<-jc.Done()
	// a refused jump host is a *JumpError of the jump stage
	bad := j
	bad.Auth = []ssh.AuthMethod{ssh.Password("nope")}
	_, err = DialJumpHost(ctx, bad, SSHOptions{})
	var je *JumpError
	if !errors.As(err, &je) || je.Target || je.Stage != StageAuth {
		t.Fatalf("%v", err)
	}
}
