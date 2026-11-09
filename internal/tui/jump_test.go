// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/crypto/ssh"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/jump"
)

// jumpRemote is a connected shell through a jump host (fake).
type jumpRemote struct {
	done chan struct{}
	once sync.Once
}

func (r *jumpRemote) Read([]byte) (int, error) {
	<-r.done
	return 0, errors.New("closed")
}
func (r *jumpRemote) Write(p []byte) (int, error) { return len(p), nil }
func (r *jumpRemote) Resize(int, int) error       { return nil }
func (r *jumpRemote) Done() <-chan struct{}       { return r.done }
func (r *jumpRemote) Err() error                  { return nil }
func (r *jumpRemote) Close() error {
	r.once.Do(func() { close(r.done) })
	return nil
}

// jumpSrc is fakeSrc with a password for every item.
type jumpSrc struct{ fakeSrc }

func (*jumpSrc) Secret(context.Context, string) (core.Secret, error) {
	return core.Secret{Value: []byte("pw")}, nil
}

type jumpTest struct {
	m     *Model
	svc   *core.Service
	mu    sync.Mutex
	dials []string // "jump user@addr -> user@addr"
	ask   bool     // the dialer asks about a host key of the target
	jumps []*fakeJumpConn
	urls  []string // opened in the browser
}

// fakeJumpConn is a connection to a jump host that dials directly.
type fakeJumpConn struct {
	done chan struct{}
	once sync.Once
}

func (f *fakeJumpConn) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}
func (f *fakeJumpConn) Done() <-chan struct{} { return f.done }
func (f *fakeJumpConn) Close() error {
	f.once.Do(func() { close(f.done) })
	return nil
}

func (jt *jumpTest) jumpConns() []*fakeJumpConn {
	jt.mu.Lock()
	defer jt.mu.Unlock()
	return append([]*fakeJumpConn(nil), jt.jumps...)
}

func newJumpTest(t *testing.T, ws string, extra ...config.JumpTemplate) *jumpTest {
	t.Helper()
	cfg := config.Default()
	cfg.Path = filepath.Join(t.TempDir(), "config.yaml")
	cfg.JumpTemplates = append([]config.JumpTemplate{{Name: "Workstations", Jump: "alpha", User: "admin"}}, extra...)
	jt := &jumpTest{}
	svc := core.New(core.Options{
		Connect:     func(context.Context) (core.Source, func(), error) { return &jumpSrc{}, nil, nil },
		Config:      &cfg,
		ConfigPoll:  -1,
		JumpHistory: jump.NewHistory(filepath.Join(t.TempDir(), "jump-history.json"), 0, true),
		JumpConnect: func(context.Context, connect.Hop, connect.SSHOptions) (core.JumpConn, error) {
			c := &fakeJumpConn{done: make(chan struct{})}
			jt.mu.Lock()
			jt.jumps = append(jt.jumps, c)
			jt.mu.Unlock()
			return c, nil
		},
		OpenURL: func(u string) error {
			jt.mu.Lock()
			jt.urls = append(jt.urls, u)
			jt.mu.Unlock()
			return nil
		},
		TunnelBackoff: 10 * time.Millisecond,
		JumpDial: func(ctx context.Context, j, tg connect.Hop, cols, rows int, o connect.SSHOptions) (core.Remote, error) {
			jt.mu.Lock()
			jt.dials = append(jt.dials, j.Name+" "+j.User+"@"+j.Addr+" -> "+tg.User+"@"+tg.Addr)
			ask := jt.ask
			jt.mu.Unlock()
			if ask {
				a, err := tg.Keys.Ask(ctx, connect.HostKeyQuestion{Role: "target", Name: tg.Addr, Host: tg.Addr, KeyType: "ssh-ed25519", Fingerprint: "SHA256:test"})
				if err != nil || a == connect.HostKeyReject {
					return nil, &connect.JumpError{Target: true, Stage: connect.StageHostKey, Jump: j.Name, Addr: tg.Addr, Err: errors.New("host key not accepted")}
				}
			}
			return &jumpRemote{done: make(chan struct{})}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Shutdown)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		select {
		case <-svc.Events():
		case <-time.After(2 * time.Second):
			t.Fatal("not online")
		}
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: cfg.UI.Keys, Icons: testIcons(false), ShowTree: true,
		ConfigPath: cfg.Path, RestoreTabs: ws != "", WorkspacePath: ws})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	jt.m, jt.svc = m, svc
	return jt
}

func (jt *jumpTest) dialed() []string {
	jt.mu.Lock()
	defer jt.mu.Unlock()
	return append([]string(nil), jt.dials...)
}

func typeKeys(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func waitConnected(t *testing.T, m *Model, s *core.Session) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // key decryption is slow under -race
	for {
		st, err := s.State()
		if st == core.SessionConnected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %v %v", st, err)
		}
		m.onEvent()
		m.Update(nil)
		time.Sleep(10 * time.Millisecond)
	}
}

// j opens the picker: step 1 lists the template and the SSH hosts, the
// search filters them, Enter goes on to the target; an invalid address is
// refused in place; Enter connects in a new tab titled "↪ jump → target";
// Esc goes back a step.
func TestJumpPicker(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	keys(m, "j")
	p, ok := m.modal.(*jumpPicker)
	if !ok {
		t.Fatalf("no picker: %T", m.modal)
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"Step 1 of 2", "Workstations", "template", "alpha", "u@10.0.0.1"} {
		if !strings.Contains(v, want) {
			t.Fatalf("step 1 lacks %q:\n%s", want, v)
		}
	}
	for _, c := range p.choices {
		if c.Label == "vnc-box" {
			t.Fatal("a VNC host offered as a jump host")
		}
	}
	typeKeys(m, "wrk")
	if len(p.shown) != 1 || p.shown[0].Label != "Workstations" {
		t.Fatalf("search: %+v", p.shown)
	}
	keys(m, "enter")
	if p.step != 1 || p.via.Key != "t:Workstations" {
		t.Fatalf("step %d via %+v", p.step, p.via)
	}
	keys(m, "esc")
	if p.step != 0 || m.modal == nil {
		t.Fatal("Esc did not go back to step 1")
	}
	keys(m, "enter") // Workstations again (the first)
	typeKeys(m, "10.0.0.300")
	keys(m, "enter")
	if m.modal == nil || !strings.Contains(p.err, "not a valid") {
		t.Fatalf("invalid address accepted: %q", p.err)
	}
	for range len("10.0.0.300") {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	typeKeys(m, "root@10.20.0.5:2222")
	keys(m, "enter")
	if m.modal != nil || len(m.tabs) != 1 {
		t.Fatalf("not connected: modal %T tabs %d", m.modal, len(m.tabs))
	}
	s := m.tabs[0].focused()
	waitConnected(t, m, s)
	if got := jt.dialed(); len(got) != 1 || got[0] != "alpha u@10.0.0.1:22 -> root@10.20.0.5:2222" {
		t.Fatalf("dialed %q", got)
	}
	if title := m.tabs[0].title(m.g); title != "↪ Workstations → 10.20.0.5:2222" {
		t.Fatalf("title %q", title)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "↪ Workstations → 10.20") || !strings.Contains(v, "through Workstations") {
		t.Fatalf("tab bar:\n%s", v)
	}

	// the target is suggested next time; Enter on the empty input takes it
	m.switchTab(0)
	keys(m, "j", "enter")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "root@10.20.0.5:2222") || !strings.Contains(v, "just now") {
		t.Fatalf("no suggestion:\n%s", v)
	}
	keys(m, "enter")
	if len(m.tabs) != 2 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	waitConnected(t, m, m.tabs[1].focused())
	// the template is now first, marked recent
	keys(m, "ctrl+b", "j")
	p = m.modal.(*jumpPicker)
	if !p.shown[0].Recent || p.shown[0].Label != "Workstations" {
		t.Fatalf("recent first: %+v", p.shown[0])
	}
	keys(m, "esc")
	if m.modal != nil {
		t.Fatal("Esc did not close")
	}
}

// Step 2 searches the hosts of the list too: one picked connects with its
// own address and user; a host of the list is a jump host without a
// template.
func TestJumpPickerHostTarget(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	keys(m, "j")
	typeKeys(m, "alpha")
	p := m.modal.(*jumpPicker)
	if p.shown[0].Key != "h:1" {
		t.Fatalf("first %+v", p.shown[0])
	}
	keys(m, "enter")
	typeKeys(m, "10.9.8.7")
	keys(m, "enter")
	waitConnected(t, m, m.tabs[0].focused())
	if got := jt.dialed(); got[0] != "alpha u@10.0.0.1:22 -> u@10.9.8.7:22" {
		t.Fatalf("dialed %q", got)
	}
}

// The mouse: a click selects a row, a click on the selected row chooses it.
func TestJumpPickerMouse(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	keys(m, "j")
	m.View()
	p := m.modal.(*jumpPicker)
	click := func(row int) {
		m.Update(tea.MouseMsg{X: m.dlgX + 4, Y: m.dlgY + p.listY + row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		m.View()
	}
	click(1)
	if p.cur != 1 || p.step != 0 {
		t.Fatalf("cur %d step %d", p.cur, p.step)
	}
	click(1)
	if p.step != 1 || p.via.Label != p.shown[1].Label {
		t.Fatalf("not chosen: step %d", p.step)
	}
}

// A new host key of the target is asked in a dialog; Reject fails the
// connection at the host key stage, y accepts.
func TestJumpHostKeyPrompt(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	jt.ask = true
	keys(m, "j", "enter")
	typeKeys(m, "10.1.1.1")
	keys(m, "enter")
	s := m.tabs[0].focused()
	pm := waitPrompt(t, m)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "SHA256:test") || !strings.Contains(v, "Accept and remember") || pm.focus != 2 {
		t.Fatalf("question:\n%s", v)
	}
	keys(m, "enter") // Reject is focused
	waitState(t, m, s, core.SessionDisconnected)
	if _, err := s.State(); err == nil || !strings.Contains(err.Error(), "host key") {
		t.Fatalf("error %v", err)
	}
	s.Reconnect()
	waitPrompt(t, m)
	keys(m, "y")
	waitConnected(t, m, s)
	if m.modal != nil {
		t.Fatalf("dialog left open: %T", m.modal)
	}
}

func waitPrompt(t *testing.T, m *Model) *promptModal {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // key decryption is slow under -race
	for {
		m.Update(nil)
		if pm, ok := m.modal.(*promptModal); ok {
			return pm
		}
		if time.Now().After(deadline) {
			t.Fatalf("no question: %T", m.modal)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitState(t *testing.T, m *Model, s *core.Session, want core.SessionState) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // key decryption is slow under -race
	for {
		if st, _ := s.State(); st == want {
			return
		}
		if time.Now().After(deadline) {
			st, err := s.State()
			t.Fatalf("state %v %v, want %v", st, err, want)
		}
		m.Update(nil)
		time.Sleep(10 * time.Millisecond)
	}
}

// A key file with a passphrase: the dialog hides the input; the key is
// read once and kept for the next connection.
func TestPassphrasePrompt(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	blk, _ := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte("hunter2"))
	key := filepath.Join(t.TempDir(), "id_ed25519")
	os.WriteFile(key, pem.EncodeToMemory(blk), 0o600)
	jt := newJumpTest(t, "", config.JumpTemplate{Name: "Keyed", Jump: "10.0.0.50", User: "ops", Key: key})
	m := jt.m
	keys(m, "j")
	typeKeys(m, "keyed")
	keys(m, "enter")
	typeKeys(m, "10.5.5.5")
	keys(m, "enter")
	s := m.tabs[0].focused()
	waitPrompt(t, m)
	typeKeys(m, "hunter2")
	if v := ansi.Strip(m.View()); strings.Contains(v, "hunter2") || !strings.Contains(v, "•••••••") || !strings.Contains(v, key) {
		t.Fatalf("passphrase dialog:\n%s", v)
	}
	keys(m, "enter")
	waitConnected(t, m, s)
	if got := jt.dialed(); len(got) != 1 || got[0] != "10.0.0.50 ops@10.0.0.50:22 -> ops@10.5.5.5:22" {
		t.Fatalf("dialed %q", got)
	}
	// the next connection does not ask
	s.Disconnect()
	waitState(t, m, s, core.SessionDisconnected)
	s.Reconnect()
	waitConnected(t, m, s)
	if m.modal != nil {
		t.Fatalf("asked again: %T", m.modal)
	}
}

// Prefix R renames the tab; the name and the jump pane are kept for the
// next start.
func TestJumpTabRenameAndRestore(t *testing.T) {
	ws := filepath.Join(t.TempDir(), "workspace.json")
	jt := newJumpTest(t, ws)
	m := jt.m
	keys(m, "j", "enter")
	typeKeys(m, "10.4.4.4")
	keys(m, "enter")
	keys(m, "ctrl+b", "R")
	if _, ok := m.modal.(*renameModal); !ok {
		t.Fatalf("no rename dialog: %T", m.modal)
	}
	typeKeys(m, "build farm")
	keys(m, "enter")
	if m.tabs[0].title(m.g) != "build farm" {
		t.Fatalf("title %q", m.tabs[0].title(m.g))
	}
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(ws)
	if !strings.Contains(string(b), `"via": "t:Workstations"`) || !strings.Contains(string(b), `"title": "build farm"`) {
		t.Fatalf("workspace:\n%s", b)
	}
	jt2 := newJumpTest(t, ws)
	m2 := jt2.m
	if len(m2.tabs) != 1 || m2.tabs[0].name != "build farm" {
		t.Fatalf("restored %d tabs", len(m2.tabs))
	}
	s := m2.tabs[0].focused()
	if st, _ := s.State(); st == core.SessionConnected || !core.IsJump(s.Host) {
		t.Fatalf("restored pane %v %+v", st, s.Host)
	}
	s.Connect()
	waitConnected(t, m2, s)
	if got := jt2.dialed(); len(got) != 1 || !strings.HasSuffix(got[0], "-> admin@10.4.4.4:22") {
		t.Fatalf("restored dial %q", got)
	}
}

// Settings → Jump hosts: J on a host starts a template through it; saving
// writes the config; D twice removes it.
func TestJumpSettingsPage(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	keys(m, "J") // on alpha (the first host row)
	s, ok := m.modal.(*settingsModal)
	if !ok || s.page != pageJump || !s.jp.editing {
		t.Fatalf("no template form: %T", m.modal)
	}
	if s.jp.in[1].Value() != "alpha" {
		t.Fatalf("jump %q", s.jp.in[1].Value())
	}
	s.jp.in[0].SetValue("Lab")
	s.jp.in[4].SetValue("~/.ssh/id_lab")
	s.jp.in[6].SetValue("accept-new")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "accept-new remembers a new host key without asking") {
		t.Fatalf("no warning:\n%s", v)
	}
	for range 7 {
		keys(m, "enter")
	}
	if s.jp.editing || s.jp.err != "" {
		t.Fatalf("not saved: %q", s.jp.err)
	}
	ts := jt.svc.Config().JumpTemplates
	if len(ts) != 2 || ts[1].Name != "Lab" || ts[1].Jump != "alpha" || ts[1].HostKeyCheck != "accept-new" {
		t.Fatalf("templates %+v", ts)
	}
	b, _ := os.ReadFile(jt.svc.Config().Path)
	if !strings.Contains(string(b), "jump_templates:") || !strings.Contains(string(b), "~/.ssh/id_lab") {
		t.Fatalf("config:\n%s", b)
	}
	keys(m, "D")
	if len(jt.svc.Config().JumpTemplates) != 2 {
		t.Fatal("removed on the first D")
	}
	keys(m, "D")
	if ts := jt.svc.Config().JumpTemplates; len(ts) != 1 || ts[0].Name != "Workstations" {
		t.Fatalf("after D D: %+v", ts)
	}
	// history off and on
	keys(m, "H")
	if jt.svc.JumpHistoryEnabled() {
		t.Fatal("history still on")
	}
	keys(m, "H")
	if !jt.svc.JumpHistoryEnabled() {
		t.Fatal("history still off")
	}
}

// Step 2: Ctrl+K picks the credential of typed addresses. A password is
// asked once for all addresses through the jump host; the choice is shown
// and offered again next time; a key file can be typed.
func TestJumpPickerCredentials(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	keys(m, "j", "enter") // Workstations
	p := m.modal.(*jumpPicker)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Credentials: default") || !strings.Contains(v, "Ctrl+K") {
		t.Fatalf("no credentials line:\n%s", v)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if p.cp == nil {
		t.Fatal("no credential chooser")
	}
	typeKeys(m, "passw")
	keys(m, "enter")
	if p.cp != nil || p.cred != "password" {
		t.Fatalf("cred %q", p.cred)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Credentials: password (typed)") {
		t.Fatalf("label:\n%s", v)
	}
	typeKeys(m, "10.7.7.1")
	keys(m, "enter")
	pm := waitPrompt(t, m)
	if pm.p.Title != "SSH password" {
		t.Fatalf("title %q", pm.p.Title)
	}
	typeKeys(m, "secret-for-all")
	keys(m, "enter")
	waitConnected(t, m, m.tabs[0].focused())

	// the next address: the same credential offered, no question
	m.switchTab(0)
	keys(m, "j", "enter")
	p = m.modal.(*jumpPicker)
	if p.cred != "password" {
		t.Fatalf("not remembered: %q", p.cred)
	}
	typeKeys(m, "10.7.7.2")
	keys(m, "enter")
	waitConnected(t, m, m.tabs[1].focused())
	if _, ok := m.modal.(*promptModal); ok {
		t.Fatal("asked again")
	}

	// another key file, typed
	key := filepath.Join(t.TempDir(), "id_test")
	os.WriteFile(key, []byte("x"), 0o600)
	m.switchTab(0)
	keys(m, "j", "enter")
	p = m.modal.(*jumpPicker)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	typeKeys(m, "other")
	keys(m, "enter")
	if !p.cp.path {
		t.Fatal("no path input")
	}
	p.cp.in.SetValue(filepath.Join(t.TempDir(), "missing"))
	keys(m, "enter")
	if p.cp == nil || p.cp.err == "" {
		t.Fatal("a missing file accepted")
	}
	p.cp.in.SetValue(key)
	keys(m, "enter")
	if p.cp != nil || p.cred != key {
		t.Fatalf("cred %q", p.cred)
	}
	// Esc leaves the chooser without changing the choice
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	keys(m, "esc")
	if p.cp != nil || p.cred != key || p.step != 1 {
		t.Fatalf("esc: %q step %d", p.cred, p.step)
	}
}

// The picker stays with the host list: the other tabs can be switched to
// while it is open (prefix + digit, a click in the tab bar), and j from a
// session tab (or the Hosts tab) shows the same picker again, with the jump
// host and the typed address kept.
func TestJumpPickerDocked(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	keys(m, "j", "enter")
	typeKeys(m, "10.20.0.5")
	keys(m, "enter")
	if len(m.tabs) != 1 || m.active != 1 {
		t.Fatalf("tabs %d active %d", len(m.tabs), m.active)
	}
	waitConnected(t, m, m.tabs[0].focused())

	// prefix j on the session tab: the picker opens on the host list
	keys(m, "ctrl+b", "j")
	p, ok := m.modal.(*jumpPicker)
	if !ok || m.active != 0 {
		t.Fatalf("prefix j: active %d modal %T", m.active, m.modal)
	}
	keys(m, "enter")
	typeKeys(m, "10.20.0.6")

	// prefix 1: the session tab, the picker waits
	keys(m, "ctrl+b", "1")
	if m.active != 1 || m.modal != nil || m.parked != p {
		t.Fatalf("switch: active %d modal %T parked %T", m.active, m.modal, m.parked)
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "Step 2 of 2") {
		t.Error("the picker is shown over the session tab")
	}
	if bar := ansi.Strip(m.tabBar()); !strings.Contains(bar, "0:Hosts ↪") {
		t.Errorf("no mark of the waiting picker: %q", bar)
	}
	// typing goes to the session, not to the picker
	typeKeys(m, "x")
	if p.in.Value() != "10.20.0.6" {
		t.Fatalf("input %q", p.in.Value())
	}

	// prefix j again: the same picker, step 2 with the typed address
	keys(m, "ctrl+b", "j")
	if m.active != 0 || m.modal != p || m.parked != nil {
		t.Fatalf("back: active %d modal %T parked %T", m.active, m.modal, m.parked)
	}
	if p.step != 1 || p.in.Value() != "10.20.0.6" {
		t.Fatalf("step %d input %q", p.step, p.in.Value())
	}

	// a click on tab 1 parks it, a click on the Hosts tab shows it
	_, starts, _ := m.tabSpans()
	m.Update(tea.MouseMsg{X: starts[1] + 4, Y: m.tabBarY(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 1 || m.parked != p {
		t.Fatalf("click: active %d parked %T", m.active, m.parked)
	}
	m.Update(tea.MouseMsg{X: 2, Y: m.tabBarY(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 0 || m.modal != p {
		t.Fatalf("click back: active %d modal %T", m.active, m.modal)
	}
	keys(m, "enter")
	if m.modal != nil || len(m.tabs) != 2 || m.active != 2 {
		t.Fatalf("connect: modal %T tabs %d active %d", m.modal, len(m.tabs), m.active)
	}
	waitConnected(t, m, m.tabs[1].focused())
}
