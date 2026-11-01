// SPDX-License-Identifier: MIT

//go:build !windows

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

type idleVNC struct{ done chan struct{} }

func (f *idleVNC) Read([]byte) (int, error)    { <-f.done; return 0, errors.New("eof") }
func (f *idleVNC) Write(p []byte) (int, error) { return len(p), nil }
func (f *idleVNC) Resize(int, int) error       { return nil }
func (f *idleVNC) Done() <-chan struct{}       { return f.done }
func (f *idleVNC) Err() error                  { return errors.New("closed") }
func (f *idleVNC) Close() error                { return nil }

// The External programs page: clients and presets chosen with the mouse,
// arguments edited with the keyboard, checks before saving, the save into
// the config file (applied at once) and a test start of a draft.
func TestProgramsPage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("checks the Linux client list")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	os.Mkdir(bin, 0o755)
	out := filepath.Join(dir, "started")
	for _, name := range []string{"xtigervncviewer", "xtightvncviewer", "webclient"} {
		body := "#!/bin/sh\necho " + name + " \"$@\" >> " + out + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	path := filepath.Join(dir, "config.yaml")
	orig := "# mine\nexternal:\n  vnc: {linux: [xtigervncviewer, '{host}::{port}']} # the viewer\n" +
		"scripts:\n  - {name: web, mode: external, run: {linux: [webclient, 'http://{host}/']}}\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	l := &connect.Launcher{Specs: core.LaunchSpecs(cfg), Settle: 50 * time.Millisecond}
	svc := core.New(core.Options{
		Connect:     func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Config:      &cfg,
		ConfigPoll:  -1,
		ApplyConfig: func(c config.Config) { l.SetSpecs(core.LaunchSpecs(c)) },
		Start:       l.Start,
		VNC: func(context.Context, core.Host, *secret.Secret, func(context.Context) (*connect.Tunnel, error)) (session.Remote, error) {
			return &idleVNC{done: make(chan struct{})}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActList: "l"}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 50})

	keys(m, ",")
	s := m.modal.(*settingsModal)
	keys(m, "tab", "tab")
	if s.page != pagePrograms {
		t.Fatalf("page %d", s.page)
	}
	p := s.prog
	view := func() string { return ansi.Strip(m.View()) }
	v := view()
	checkFrame(t, m.View(), 130, 50)
	for _, want := range []string{"Item       vnc  smb", "Client     auto  TigerVNC ✓  vncviewer  TightVNC ✓", "Program   xtigervncviewer",
		"✓ " + filepath.Join(bin, "xtigervncviewer"), "Args    1 {host}::{port}", "Preview   xtigervncviewer 127.0.0.1::<port>", "for vnc-box"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	click := func(item int, label string) {
		t.Helper()
		m.View()
		for _, h := range p.hits {
			if h.item != item {
				continue
			}
			o, _ := p.options(m, item)
			for i, sp := range h.spans {
				if label == "" || i < len(o) && strings.HasPrefix(o[i], label) {
					m.Update(tea.MouseMsg{X: m.dlgX + boxPadX + sp[0] + 1, Y: m.dlgY + h.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
					return
				}
			}
		}
		t.Fatalf("no %q at item %d:\n%s", label, item, view())
	}

	// the mouse: TightVNC, then its stdin preset
	click(itClient, "TightVNC")
	if got := p.cmd(p.cur(), "linux"); strings.Join(got, " ") != filepath.Join(bin, "xtightvncviewer")+" {host}::{port}" || p.mode(p.cur()) != "proxy" {
		t.Fatalf("after TightVNC: %q %s", got, p.mode(p.cur()))
	}
	click(itPreset, "password on stdin")
	if got := p.cmd(p.cur(), "linux"); strings.Join(got[1:], " ") != "-autopass {host}::{port}" || p.mode(p.cur()) != "stdin" {
		t.Fatalf("stdin preset: %q %s", got, p.mode(p.cur()))
	}
	if p.item() != itPreset {
		t.Errorf("focus not on the clicked row: %d", p.item())
	}

	// the keyboard: an argument with a typo is refused on save
	p.focusOn(itArg + 1)
	keys(m, "-", "{", "p", "r", "t", "}")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !strings.Contains(p.err, "unknown placeholder {prt}") {
		t.Fatalf("err %q", p.err)
	}
	if b, _ := os.ReadFile(path); string(b) != orig {
		t.Fatalf("saved despite the error:\n%s", b)
	}
	if !strings.Contains(view(), "unsaved changes") {
		t.Error("no unsaved mark")
	}
	for range 6 {
		m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlN}) // a new argument after it
	keys(m, "-", "q", "u", "a", "l", "i", "t", "y", "=", "9")
	if got := p.cmd(p.cur(), "linux"); strings.Join(got[1:], " ") != "-autopass {host}::{port} -quality=9" {
		t.Fatalf("args %q", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyUp, Alt: true}) // moved before the address
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if p.err != "" || !strings.Contains(p.status, "saved to config.yaml") {
		t.Fatalf("save: %q %q", p.err, p.status)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"# mine", "# the viewer", "password: stdin", "linux: [" + filepath.Join(bin, "xtightvncviewer") + ", -autopass, -quality=9, '{host}::{port}']"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if got := svc.Config().External["vnc"].Password; got != "stdin" {
		t.Errorf("not applied: %q", got)
	}
	if m.toast != "" {
		t.Errorf("own save reported as a reload: %q", m.toast)
	}

	// a test start of a draft of the external script: for the first item
	click(itEntry, "script web")
	p.focusOn(itArg)
	keys(m, "?", "x", "=", "1")
	p.focusOn(itTest)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("no test start: %q", p.err)
	}
	m.Update(cmd())
	if p.err != "" || !strings.HasPrefix(p.status, "started script web for alpha") {
		t.Fatalf("test: %q %q", p.err, p.status)
	}
	if got := waitStarted(t, out); got != "webclient http://10.0.0.1/?x=1" {
		t.Errorf("started %q", got)
	}
	if b2, _ := os.ReadFile(path); string(b2) != string(b) {
		t.Error("the test start saved the draft")
	}

	// a test start of the VNC client opens a tab; the draft is kept
	click(itEntry, "vnc")
	click(itTest, "")
	if m.modal != nil || len(m.tabs) != 1 || m.programs != p || !p.dirty() {
		t.Fatalf("VNC test: modal %T tabs %d dirty %v err %q", m.modal, len(m.tabs), p.dirty(), p.err)
	}
	if got := m.tabs[0].title(); !strings.Contains(got, "vnc-box") {
		t.Errorf("tab %q", got)
	}

	// back in the settings: Esc with unsaved changes asks first
	keys(m, "ctrl+b", "l", ",")
	if m.modal.(*settingsModal).prog != p {
		t.Fatal("draft lost")
	}
	m.modal.(*settingsModal).page = pagePrograms
	keys(m, "esc")
	if m.modal == nil || !strings.Contains(p.err, "unsaved changes") {
		t.Fatalf("closed without asking: %q", p.err)
	}
	keys(m, "esc")
	if m.modal != nil || p.dirty() {
		t.Fatalf("second Esc: modal %T dirty %v", m.modal, p.dirty())
	}
}

func waitStarted(t *testing.T, p string) string {
	t.Helper()
	for range 200 {
		if b, _ := os.ReadFile(p); len(b) > 0 {
			os.Remove(p)
			return strings.TrimSpace(string(b))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nothing started")
	return ""
}

// A config file that does not load: marked in the status bar until fixed;
// r re-reads it.
func TestConfigErrorStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := config.Load(path)
	svc := core.New(core.Options{
		Connect:    func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Config:     &cfg,
		ConfigPoll: -1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Icons: testIcons(false), ShowTree: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	os.WriteFile(path, []byte("ui: {accent: plaid}\n"), 0o600)
	keys(m, "r")
	last := func() string { l := strings.Split(ansi.Strip(m.View()), "\n"); return l[len(l)-1] }
	if !strings.Contains(last(), "config error (r: re-read)") || !m.toastErr || !strings.Contains(m.toast, "previous one stays in use") {
		t.Fatalf("status %q toast %q", last(), m.toast)
	}
	m.toastUntil = time.Now() // the message is gone, the mark stays
	if !strings.Contains(last(), "config error") {
		t.Fatalf("mark gone: %q", last())
	}
	os.WriteFile(path, []byte("ui: {accent: green}\n"), 0o600)
	keys(m, "r")
	if strings.Contains(last(), "config error") || m.opt.Appearance.Accent != "green" {
		t.Fatalf("after the fix: %q accent %s", last(), m.opt.Appearance.Accent)
	}
	t.Cleanup(func() { setAccent(core.Accents[0].Color) })
}
