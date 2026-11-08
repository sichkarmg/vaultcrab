// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/jump"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/session"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// fakeRemote is a connected shell that ends when closed.
type fakeRemote struct {
	done chan struct{}
	once sync.Once
}

func newFakeRemote() *fakeRemote { return &fakeRemote{done: make(chan struct{})} }

func (f *fakeRemote) Read([]byte) (int, error) {
	<-f.done
	return 0, errors.New("closed")
}
func (f *fakeRemote) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeRemote) Resize(int, int) error       { return nil }
func (f *fakeRemote) Done() <-chan struct{}       { return f.done }
func (f *fakeRemote) Err() error                  { return errors.New("connection lost") }
func (f *fakeRemote) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

// dialed records what a jump connection was given.
type dialed struct {
	jump, target connect.Hop
	jumpAuth     int
}

type jumpEnv struct {
	s     *Service
	mu    sync.Mutex
	calls []dialed
	fail  error
	hist  string
}

func newJumpEnv(t *testing.T, tpls []config.JumpTemplate) *jumpEnv {
	t.Helper()
	hosts := []model.Host{
		{ID: "j1", Name: "bastion", Scheme: model.SSH, User: "ops", Addr: "203.0.113.10"},
		{ID: "j2", Name: "edge", Scheme: model.SSH, User: "root", Addr: "203.0.113.11", Port: 2222},
		{ID: "w1", Name: "build-box", Scheme: model.SSH, User: "ci", Addr: "10.1.0.7", Port: 2200},
		{ID: "k1", Name: "lab key", Scheme: model.SSH, Addr: "10.1.0.8"},
		{ID: "d1", Name: "twin", Scheme: model.SSH, Addr: "10.1.0.9"},
		{ID: "d2", Name: "twin", Scheme: model.SSH, Addr: "10.1.0.10"},
	}
	creds := []model.Credential{{ID: "c1", Name: "workstations", User: "wsadmin"}}
	src := &fakeSource{status: vault.Unlocked, snap: model.Snapshot{Hosts: hosts, Credentials: creds},
		secrets: map[string]string{"j1": "bastion-pw", "j2": "edge-pw", "w1": "build-pw", "k1": "lab-pw", "c1": "ws-pw"}}
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")
	cfg.JumpTemplates = tpls
	e := &jumpEnv{hist: filepath.Join(t.TempDir(), "jump-history.json")}
	e.s = New(Options{
		Connect:     func(context.Context) (Source, func(), error) { return src, nil, nil },
		Config:      &cfg,
		ConfigPoll:  -1,
		JumpHistory: jump.NewHistory(e.hist, 0, true),
		SSH:         connect.SSHOptions{KnownHosts: filepath.Join(t.TempDir(), "known_hosts")},
		JumpDial: func(ctx context.Context, j, tg connect.Hop, cols, rows int, o connect.SSHOptions) (session.Remote, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.calls = append(e.calls, dialed{jump: j, target: tg, jumpAuth: len(j.Auth)})
			if e.fail != nil {
				return nil, e.fail
			}
			return newFakeRemote(), nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	e.s.Start(ctx)
	waitState(t, e.s, Online)
	return e
}

func (e *jumpEnv) last(t *testing.T) dialed {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) == 0 {
		t.Fatal("not dialed")
	}
	return e.calls[len(e.calls)-1]
}

// Users, ports, keys and the host key check come from the template, the
// typed target and the vault item of the jump host.
func TestPlanJump(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "id")
	e := newJumpEnv(t, []config.JumpTemplate{
		{Name: "Workstations", Jump: "bastion", User: "admin", Port: 2022, Key: keyFile},
		{Name: "Raw", Jump: "jumper@198.51.100.1:2200", Key: "agent", HostKeyCheck: "accept-new"},
		{Name: "Vaulted", Jump: "edge", Key: "vault:lab key", JumpKey: "agent", HostKeyCheck: "strict"},
		{Name: "Gone", Jump: "no where!"},
		{Name: "Twins", Jump: "twin"},
	})
	s := e.s
	cases := []struct {
		spec               JumpSpec
		jumpAddr, jumpUser string
		jumpKey            keySource
		tAddr, tUser       string
		tKey               keySource
		mode               connect.HostKeyMode
		hist               string
	}{
		// template on a vault jump host: its user and credential; the
		// template's user, port and key on the target
		{JumpSpec{Via: "t:Workstations", Target: "10.20.0.5"}, "203.0.113.10:22", "ops", keySource{kind: jump.KeyVault, item: "j1"},
			"10.20.0.5:2022", "admin", keySource{kind: jump.KeyFile, path: keyFile}, connect.HostKeyAsk, "10.20.0.5:2022"},
		// typed user and port win
		{JumpSpec{Via: "t:Workstations", Target: "root@10.20.0.5:22"}, "203.0.113.10:22", "ops", keySource{kind: jump.KeyVault, item: "j1"},
			"10.20.0.5:22", "root", keySource{kind: jump.KeyFile, path: keyFile}, connect.HostKeyAsk, "10.20.0.5"},
		// a host:port jump: its user; the key (agent) on both hops
		{JumpSpec{Via: "t:Raw", Target: "ws7.lab"}, "198.51.100.1:2200", "jumper", keySource{kind: jump.KeyAgent},
			"ws7.lab:22", "jumper", keySource{kind: jump.KeyAgent}, connect.HostKeyAcceptNew, "ws7.lab"},
		// jump_key and key: vault:<name>
		{JumpSpec{Via: "t:Vaulted", Target: "10.9.9.9"}, "203.0.113.11:2222", "root", keySource{kind: jump.KeyAgent},
			"10.9.9.9:22", "root", keySource{kind: jump.KeyVault, item: "k1"}, connect.HostKeyStrict, "10.9.9.9"},
		// any SSH host as the jump host: its credential on both hops, ask
		{JumpSpec{Via: "h:j2", Target: "10.3.3.3"}, "203.0.113.11:2222", "root", keySource{kind: jump.KeyVault, item: "j2"},
			"10.3.3.3:22", "root", keySource{kind: jump.KeyVault, item: "j2"}, connect.HostKeyAsk, "10.3.3.3"},
		// a vault host as the target: its address, user and credential
		{JumpSpec{Via: "t:Workstations", Item: "w1"}, "203.0.113.10:22", "ops", keySource{kind: jump.KeyVault, item: "j1"},
			"10.1.0.7:2200", "ci", keySource{kind: jump.KeyVault, item: "w1"}, connect.HostKeyAsk, "10.1.0.7:2200"},
	}
	for _, c := range cases {
		p, err := s.planJump(c.spec)
		if err != nil {
			t.Fatalf("%+v: %v", c.spec, err)
		}
		if p.jump.Addr != c.jumpAddr || p.jump.User != c.jumpUser || p.jumpKey != c.jumpKey ||
			p.target.Addr != c.tAddr || p.target.User != c.tUser || p.targetKey != c.tKey || p.mode != c.mode || p.histTarget != c.hist {
			t.Errorf("%+v:\n got jump %s %s %+v target %s %s %+v mode %d hist %s", c.spec,
				p.jump.Addr, p.jump.User, p.jumpKey, p.target.Addr, p.target.User, p.targetKey, p.mode, p.histTarget)
		}
	}
	for _, bad := range []JumpSpec{
		{Via: "t:Gone", Target: "10.0.0.1"},      // jump neither a vault item nor host:port
		{Via: "t:Missing", Target: "10.0.0.1"},   // no such template
		{Via: "h:nope", Target: "10.0.0.1"},      // no such host
		{Via: "t:Workstations", Target: "a b"},   // invalid target
		{Via: "t:Workstations", Target: ""},      // empty
		{Via: "t:Workstations", Item: "deleted"}, // the target item is gone
	} {
		if _, err := s.planJump(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// A jump session connects through the dialer of both hops, is recorded in
// the history (names and addresses only), reconnects through both hops, and
// a clone (OpenSession with its pseudo-host) connects the same way.
func TestJumpSession(t *testing.T) {
	e := newJumpEnv(t, []config.JumpTemplate{{Name: "WS", Jump: "bastion", Key: "vault:lab key"}})
	s := e.s
	spec := JumpSpec{Via: "t:WS", Target: "admin@10.20.0.5"}
	sess, err := s.OpenJump(spec, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	waitSession(t, sess, session.Connected)
	d := e.last(t)
	if d.jump.Name != "bastion" || d.target.Addr != "10.20.0.5:22" || d.target.User != "admin" || d.jumpAuth == 0 {
		t.Fatalf("dialed %+v", d)
	}
	if d.target.Keys.Mode != connect.HostKeyAsk || d.target.Keys.Ask == nil || d.jump.Keys.File == "" {
		t.Fatalf("host key policy %+v", d.target.Keys)
	}
	h := sess.Host
	if h.Name != "10.20.0.5" || h.Fields[FieldVia] != "WS" || !IsJump(h) {
		t.Fatalf("pseudo-host %+v", h)
	}
	if got, ok := JumpSpecOf(h); !ok || got != spec {
		t.Fatalf("spec %+v", got)
	}
	// history: the target without the default port, the typed user
	ts := s.JumpTargets("t:WS")
	if len(ts) != 1 || ts[0].Label != "admin@10.20.0.5" || ts[0].Spec.Target != "admin@10.20.0.5" {
		t.Fatalf("targets %+v", ts)
	}
	if c := s.JumpChoices(); c[0].Key != "t:WS" || !c[0].Recent {
		t.Fatalf("choices %+v", c)
	}
	b, _ := os.ReadFile(e.hist)
	for _, secret := range []string{"bastion-pw", "lab-pw"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret in the history: %s", b)
		}
	}

	// reconnect: both hops again
	sess.Disconnect()
	waitSession(t, sess, session.Disconnected)
	sess.Reconnect()
	waitSession(t, sess, session.Connected)
	// a clone of the tab
	c, err := s.OpenSession(sess.Host, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	waitSession(t, c, session.Connected)
	e.mu.Lock()
	n := len(e.calls)
	e.mu.Unlock()
	if n != 3 {
		t.Fatalf("%d dials, want 3", n)
	}
	if ts := s.JumpTargets("t:WS"); ts[0].Count != 3 {
		t.Fatalf("count %+v", ts)
	}

	// a failed connection is not remembered
	e.mu.Lock()
	e.fail = &connect.JumpError{Target: true, Stage: connect.StageConnect, Jump: "bastion", Addr: "10.20.0.6:22", Err: errors.New("connect failed")}
	e.mu.Unlock()
	f, _ := s.OpenJump(JumpSpec{Via: "t:WS", Target: "10.20.0.6"}, 80, 24)
	waitSession(t, f, session.Disconnected)
	if _, err := f.State(); err == nil || !strings.Contains(err.Error(), "unreachable from the jump host bastion") {
		t.Fatalf("error %v", err)
	}
	if ts := s.JumpTargets("t:WS"); len(ts) != 1 {
		t.Fatalf("failed target remembered: %+v", ts)
	}
}

// Picking a vault host as the target remembers its item, shown by name.
func TestJumpItemTarget(t *testing.T) {
	e := newJumpEnv(t, nil)
	sess, err := e.s.OpenJump(JumpSpec{Via: "h:j1", Item: "w1"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	waitSession(t, sess, session.Connected)
	if sess.Host.Name != "build-box" {
		t.Errorf("name %q", sess.Host.Name)
	}
	ts := e.s.JumpTargets("h:j1")
	if len(ts) != 1 || ts[0].Spec.Item != "w1" || ts[0].Label != "build-box (ci@10.1.0.7:2200)" {
		t.Fatalf("targets %+v", ts)
	}
}

// The passphrase of a key file is asked once (wrong answers again, up to
// three times) and the key is kept in memory for the next connections.
func TestJumpKeyPassphrase(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	blk, _ := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("open sesame"))
	path := filepath.Join(t.TempDir(), "id_ed25519")
	os.WriteFile(path, pem.EncodeToMemory(blk), 0o600)
	e := newJumpEnv(t, nil)
	s := e.s
	answers := []string{"wrong", "open sesame"}
	var warnings []string
	go func() {
		for len(answers) > 0 {
			p := s.Prompt()
			if p == nil {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			if p.Kind != PromptPassphrase || !strings.Contains(p.Text, path) {
				p.Cancel()
				return
			}
			warnings = append(warnings, p.Warning)
			a := answers[0]
			answers = answers[1:]
			p.AnswerSecret([]byte(a))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute) // bcrypt_pbkdf is slow under -race
	defer cancel()
	k1, err := s.keyFile(ctx, path, "target x")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 || warnings[0] != "" || !strings.Contains(warnings[1], "Wrong passphrase") {
		t.Fatalf("warnings %q", warnings)
	}
	k2, err := s.keyFile(ctx, path, "target x") // no prompt: nobody answers
	if err != nil || k1 != k2 {
		t.Fatalf("not kept: %v", err)
	}
	// a canceled question fails the connection
	other := filepath.Join(t.TempDir(), "other")
	os.WriteFile(other, pem.EncodeToMemory(blk), 0o600)
	go func() {
		for {
			if p := s.Prompt(); p != nil {
				p.Cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if _, err := s.keyFile(ctx, other, "x"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("canceled: %v", err)
	}
	// a question nobody answers is withdrawn when the connection gives up
	c2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	third := filepath.Join(t.TempDir(), "third")
	os.WriteFile(third, pem.EncodeToMemory(blk), 0o600)
	if _, err := s.keyFile(c2, third, "x"); err == nil {
		t.Fatal("no error")
	}
	if s.Prompt() != nil {
		t.Fatal("the question is still open")
	}
}

// The host key question carries the fingerprint; the answer goes back.
func TestJumpAskHostKey(t *testing.T) {
	e := newJumpEnv(t, nil)
	ask := e.s.askHostKey(connect.HostKeyAsk)
	if e.s.askHostKey(connect.HostKeyAcceptNew) != nil {
		t.Fatal("accept-new asks")
	}
	go func() {
		for {
			if p := e.s.Prompt(); p != nil {
				if p.Kind == PromptHostKey && strings.Contains(p.Text, "SHA256:abc") && strings.Contains(p.Text, "target 10.0.0.5:22") {
					p.AnswerHostKey(connect.HostKeyOnce)
				} else {
					p.Cancel()
				}
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	a, err := ask(context.Background(), connect.HostKeyQuestion{Role: "target", Name: "10.0.0.5:22", Host: "10.0.0.5:22", KeyType: "ssh-ed25519", Fingerprint: "SHA256:abc"})
	if err != nil || a != connect.HostKeyOnce {
		t.Fatalf("%v %v", a, err)
	}
}

// Templates are saved to the config and applied at once.
func TestSaveJumpTemplates(t *testing.T) {
	e := newJumpEnv(t, nil)
	tpl := config.JumpTemplate{Name: "WS", Jump: "bastion", User: "admin", Port: 22, Key: "~/.ssh/id_ed25519", HostKeyCheck: "accept-new"}
	if err := e.s.SaveJumpTemplates([]config.JumpTemplate{tpl}); err != nil {
		t.Fatal(err)
	}
	got := e.s.Config().JumpTemplates
	if len(got) != 1 || got[0] != tpl {
		t.Fatalf("%+v", got)
	}
	if err := e.s.SaveJumpTemplates([]config.JumpTemplate{tpl, tpl}); err == nil {
		t.Fatal("duplicate names saved")
	}
	if err := e.s.SaveJumpTemplates([]config.JumpTemplate{{Name: "x"}}); err == nil {
		t.Fatal("template without jump saved")
	}
}

// The credential of typed addresses chosen in the picker: a password typed
// once per jump host (asked again after the target refused it), or a vault
// item marked as a credential (its user name is the default user); the
// choice is remembered in the history and offered again.
func TestJumpTypedCredentials(t *testing.T) {
	e := newJumpEnv(t, []config.JumpTemplate{{Name: "WS", Jump: "bastion", User: "", Key: "agent"}})
	s := e.s
	asked := 0
	var answerMu sync.Mutex
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			if p := s.Prompt(); p != nil && p.Kind == PromptPassphrase {
				answerMu.Lock()
				asked++
				answerMu.Unlock()
				if !strings.Contains(p.Title, "SSH password") || !strings.Contains(p.Text, "through WS") {
					p.Cancel()
					continue
				}
				p.AnswerSecret([]byte("same-for-all"))
			}
		}
	}()
	count := func() int {
		answerMu.Lock()
		defer answerMu.Unlock()
		return asked
	}

	// plan: the chosen credential replaces the template's key
	p, err := s.planJump(JumpSpec{Via: "t:WS", Target: "10.0.0.1", Cred: "password"})
	if err != nil || p.targetKey.kind != jump.KeyPassword || p.targetKey.scope != "t:WS" || p.jumpKey.kind != jump.KeyVault {
		t.Fatalf("plan %+v %v", p, err)
	}
	p, err = s.planJump(JumpSpec{Via: "t:WS", Target: "10.0.0.1", Cred: "vault:workstations"})
	if err != nil || p.targetKey.item != "c1" || p.target.User != "wsadmin" {
		t.Fatalf("vault credential: %+v %v", p, err)
	}
	p, _ = s.planJump(JumpSpec{Via: "t:WS", Target: "root@10.0.0.1", Cred: "vault:c1"})
	if p.target.User != "root" {
		t.Fatalf("typed user lost: %s", p.target.User)
	}

	// two addresses with the typed password: asked once
	for _, ip := range []string{"10.0.0.1", "10.0.0.2"} {
		sess, err := s.OpenJump(JumpSpec{Via: "t:WS", Target: ip, Cred: "password"}, 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		waitSession(t, sess, session.Connected)
	}
	if count() != 1 {
		t.Fatalf("asked %d times", count())
	}
	if c := s.JumpLastCred("t:WS"); c != "password" {
		t.Fatalf("last cred %q", c)
	}
	if ts := s.JumpTargets("t:WS"); ts[0].Spec.Cred != "password" {
		t.Fatalf("history %+v", ts)
	}
	// refused by a target: forgotten, asked again next time
	e.mu.Lock()
	e.fail = &connect.JumpError{Target: true, Stage: connect.StageAuth, Jump: "bastion", Addr: "10.0.0.3:22", User: "ops", Err: errors.New("denied")}
	e.mu.Unlock()
	f, _ := s.OpenJump(JumpSpec{Via: "t:WS", Target: "10.0.0.3", Cred: "password"}, 80, 24)
	waitSession(t, f, session.Disconnected)
	e.mu.Lock()
	e.fail = nil
	e.mu.Unlock()
	g, _ := s.OpenJump(JumpSpec{Via: "t:WS", Target: "10.0.0.4", Cred: "password"}, 80, 24)
	waitSession(t, g, session.Connected)
	if count() != 2 {
		t.Fatalf("not asked again after a refusal: %d", count())
	}

	// the picker offers the vault credential and names the choices
	var labels []string
	for _, c := range s.JumpCreds("t:WS") {
		labels = append(labels, c.Label)
	}
	all := strings.Join(labels, "|")
	for _, want := range []string{"Default", "Password", "ssh-agent", "Other key file…", "workstations", "bastion"} {
		if !strings.Contains(all, want) {
			t.Fatalf("choices %q lack %q", all, want)
		}
	}
	if l := s.CredLabel("vault:c1"); l != "vault: workstations" {
		t.Fatalf("label %q", l)
	}
	if l := s.CredLabel(""); l != "default" {
		t.Fatalf("label %q", l)
	}
	if !strings.Contains(s.JumpCreds("t:WS")[0].Detail, "ssh-agent") {
		t.Fatalf("default %q", s.JumpCreds("t:WS")[0].Detail)
	}
	// the pseudo-host keeps the choice (clone, restore)
	h, _ := s.JumpHost(JumpSpec{Via: "t:WS", Target: "10.0.0.9", Cred: "vault:c1"})
	if sp, _ := JumpSpecOf(h); sp.Cred != "vault:c1" {
		t.Fatalf("spec %+v", sp)
	}
	b, _ := os.ReadFile(e.hist)
	if strings.Contains(string(b), "same-for-all") || strings.Contains(string(b), "ws-pw") {
		t.Fatalf("secret in the history: %s", b)
	}
}
