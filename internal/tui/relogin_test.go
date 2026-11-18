// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// loginSrc is an editable fake vault whose items have the password "old"
// and that records SetLogin.
type loginSrc struct {
	editSrc
	lmu   sync.Mutex
	saved []string
}

func (*loginSrc) Secret(context.Context, string) (core.Secret, error) {
	return core.Secret{Value: []byte("old")}, nil
}

func (l *loginSrc) SetLogin(_ context.Context, id, user string, pw []byte) error {
	l.lmu.Lock()
	defer l.lmu.Unlock()
	l.saved = append(l.saved, id+" "+user+" "+string(pw))
	return nil
}

// The server refuses the password of the vault: the login dialog opens
// with the login of the item and the password focused; a wrong password
// asks again, the right one connects and the save question writes the
// pair to the item.
func TestReloginDialog(t *testing.T) {
	src := &loginSrc{editSrc: editSrc{updated: map[string]core.HostEdit{}}}
	var mu sync.Mutex
	var dialed []string
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil },
		SSHDial: func(_ context.Context, h model.Host, sec *secret.Secret, _, _ int) (core.Remote, error) {
			mu.Lock()
			dialed = append(dialed, h.User+":"+string(sec.Value))
			mu.Unlock()
			if h.User == "admin" && string(sec.Value) == "s3cret" {
				return &jumpRemote{done: make(chan struct{})}, nil
			}
			return nil, connect.ErrAuthFailed
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	h, _ := svc.Host("1")
	s, err := svc.OpenSession(h, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.CloseSession(s) })

	pm := waitPrompt(t, m)
	v := ansi.Strip(m.View())
	if pm.p.Kind != core.PromptLogin || pm.in.Value() != "u" || pm.focus != 1 ||
		!strings.Contains(v, "SSH login") || !strings.Contains(v, "refused the login and password") {
		t.Fatalf("login dialog (focus %d):\n%s", pm.focus, v)
	}
	// change the login (Tab goes to it), type a wrong password
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	typeText(m, "admin")
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	typeText(m, "nope")
	if v := ansi.Strip(m.View()); strings.Contains(v, "nope") || !strings.Contains(v, "••••") {
		t.Fatalf("password shown:\n%s", v)
	}
	keys(m, "enter")
	pm = waitPrompt(t, m)
	if pm.p.Kind != core.PromptLogin || pm.in.Value() != "admin" || !strings.Contains(ansi.Strip(m.View()), "Wrong login or password") {
		t.Fatalf("retry dialog:\n%s", ansi.Strip(m.View()))
	}
	typeText(m, "s3cret")
	keys(m, "enter")
	waitConnected(t, m, s)

	pm = waitPrompt(t, m)
	v = ansi.Strip(m.View())
	if pm.p.Kind != core.PromptSaveLogin || pm.focus != 0 || !strings.Contains(v, "u → admin") || !strings.Contains(v, "Save to the vault") {
		t.Fatalf("save question:\n%s", v)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runCmd(m, cmd)
	if m.modal != nil {
		t.Fatalf("dialog left open: %T", m.modal)
	}
	src.lmu.Lock()
	saved := strings.Join(src.saved, "|")
	src.lmu.Unlock()
	if saved != "1 admin s3cret" {
		t.Fatalf("saved %q", saved)
	}
	if !strings.Contains(m.toast, "saved to the vault") || m.toastErr {
		t.Fatalf("toast %q", m.toast)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(dialed, ",") != "u:old,admin:nope,admin:s3cret" {
		t.Fatalf("dialed %q", dialed)
	}
}

// Esc in the login dialog ends the attempt with the refusal; "n" in the
// save question saves nothing.
func TestReloginCancelAndDontSave(t *testing.T) {
	src := &loginSrc{editSrc: editSrc{updated: map[string]core.HostEdit{}}}
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil },
		SSHDial: func(_ context.Context, h model.Host, sec *secret.Secret, _, _ int) (core.Remote, error) {
			if string(sec.Value) == "good" {
				return &jumpRemote{done: make(chan struct{})}, nil
			}
			return nil, connect.ErrAuthFailed
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	h, _ := svc.Host("1")
	s, _ := svc.OpenSession(h, 80, 24)
	t.Cleanup(func() { svc.CloseSession(s) })
	waitPrompt(t, m)
	keys(m, "esc")
	waitState(t, m, s, core.SessionDisconnected)
	if _, err := s.State(); err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("error %v", err)
	}
	s.Reconnect()
	waitPrompt(t, m)
	typeText(m, "good")
	keys(m, "enter")
	waitConnected(t, m, s)
	if pm := waitPrompt(t, m); pm.p.Kind != core.PromptSaveLogin {
		t.Fatalf("kind %v", pm.p.Kind)
	}
	keys(m, "n")
	if m.modal != nil || svc.Prompt() != nil {
		t.Fatal("question left open")
	}
	src.lmu.Lock()
	defer src.lmu.Unlock()
	if len(src.saved) != 0 {
		t.Fatalf("saved %q", src.saved)
	}
}
