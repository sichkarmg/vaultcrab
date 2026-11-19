// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The vault locks while the item form is open: the unlock dialog opens over
// the form (at once, and on a key after the auto-lock); after unlocking, or
// Esc, the form is back with what was typed and can be saved.
func TestUnlockOverItemForm(t *testing.T) {
	src := &editSrc{updated: map[string]core.HostEdit{}}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "e")
	f, ok := m.modal.(*hostForm)
	if !ok {
		t.Fatalf("no form: %T", m.modal)
	}
	f.in[fName].SetValue("alpha-renamed")

	// locked (bw session expired, auto-lock): the unlock dialog is on top
	svc.LockAll(ctx, nil)
	m.onEvent()
	if _, ok := m.modal.(*unlockModal); !ok || m.under != f {
		t.Fatalf("modal %T under %T", m.modal, m.under)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "master password") {
		t.Fatalf("no unlock dialog:\n%s", v)
	}
	// Esc: the form again, as it was
	keys(m, "esc")
	if m.modal != f || m.under != nil || f.in[fName].Value() != "alpha-renamed" {
		t.Fatalf("after Esc: modal %T", m.modal)
	}
	// a key after the auto-lock opens it the same way
	m.openUnlock(newUnlock(m))
	typeText(m, "master")
	enter(m)
	if m.modal != f || svc.Status().State != core.Online {
		t.Fatalf("after unlock: modal %T state %v", m.modal, svc.Status().State)
	}
	enterAndRun(t, m)
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.updated["1"].Name != "alpha-renamed" {
		t.Fatalf("saved %+v", src.updated)
	}
}

// The unlock dialog from Settings → Vaults opens over the settings, which
// are back after it.
func TestUnlockOverSettings(t *testing.T) {
	m, svc := newTestModel(t, false)
	keys(m, ",")
	st, ok := m.modal.(*settingsModal)
	if !ok {
		t.Fatalf("modal %T", m.modal)
	}
	svc.LockAll(m.ctx, nil)
	m.onEvent()
	if _, ok := m.modal.(*unlockModal); !ok || m.under != st {
		t.Fatalf("modal %T under %T", m.modal, m.under)
	}
	typeText(m, "master")
	enter(m)
	if m.modal != st {
		t.Fatalf("modal %T", m.modal)
	}
}
