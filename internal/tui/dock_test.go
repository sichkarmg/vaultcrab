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

// The item form and the settings stay with the host list: the other tabs
// can be switched to with the keys and the mouse while they are open, and
// they are there again, unchanged, on the return.
func TestDockedDialogs(t *testing.T) {
	src := &editSrc{updated: map[string]core.HostEdit{}}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActNextTab: "n", core.ActList: "l", core.ActSettings: ","},
		Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	m.tabs = append(m.tabs, newTab(svc.OpenLocal(80, 20)))
	t.Cleanup(func() { svc.Shutdown() })

	keys(m, "/", "a", "l", "p", "h", "a", "enter", "e")
	f, ok := m.modal.(*hostForm)
	if !ok {
		t.Fatal("no edit form")
	}
	f.setFocus(fPort)
	typeText(m, "22")

	// the prefix and a digit: tab 1, the form waits
	keys(m, "ctrl+b", "1")
	if m.active != 1 || m.modal != nil || m.parked != f {
		t.Fatalf("active %d modal %T parked %T", m.active, m.modal, m.parked)
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "Edit alpha") {
		t.Error("the form is shown over the session tab")
	}
	if !strings.Contains(ansi.Strip(m.tabBar()), "0:Hosts ✎") {
		t.Errorf("no mark of the waiting form: %q", ansi.Strip(m.tabBar()))
	}
	// back with a click on the Hosts tab: the same form, the typed port kept
	m.Update(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 0 || m.modal != f || m.parked != nil {
		t.Fatalf("back: active %d modal %T", m.active, m.modal)
	}
	if !strings.Contains(ansi.Strip(m.View()), "Edit alpha") {
		t.Error("form not shown again")
	}

	// a click on tab 1 in the tab bar while the form is open
	_, starts, _ := m.tabSpans()
	m.Update(tea.MouseMsg{X: starts[1] + 2, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 1 || m.parked != f {
		t.Fatalf("click: active %d parked %T", m.active, m.parked)
	}
	// the settings from a session tab: the parked form is shown instead
	keys(m, "ctrl+b", ",")
	if m.active != 0 || m.modal != f {
		t.Fatalf("settings with a parked form: active %d modal %T", m.active, m.modal)
	}
	keys(m, "esc")
	if m.modal != nil {
		t.Fatalf("form not closed: %T", m.modal)
	}

	// the settings: parked and back with prefix n / prefix l
	keys(m, ",")
	s, ok := m.modal.(*settingsModal)
	if !ok {
		t.Fatalf("no settings: %T", m.modal)
	}
	keys(m, "ctrl+b", "n")
	if m.active != 1 || m.parked != s {
		t.Fatalf("settings: active %d parked %T", m.active, m.parked)
	}
	keys(m, "ctrl+b", "l")
	if m.active != 0 || m.modal != s {
		t.Fatalf("settings back: active %d modal %T", m.active, m.modal)
	}
	// other prefixed keys do nothing while the dialog is open
	keys(m, "ctrl+b", "?")
	if m.modal != s {
		t.Fatalf("help over the settings: %T", m.modal)
	}
}

// Ctrl+PgDn/PgUp and Alt+→/← go to the next/previous tab without the
// prefix; Alt+arrows stay with the search field while it is edited.
func TestTabKeys(t *testing.T) {
	m, svc := newTestModel(t, false)
	for range 2 {
		m.tabs = append(m.tabs, newTab(svc.OpenLocal(80, 20)))
	}
	t.Cleanup(func() { svc.Shutdown() })
	press := func(k tea.KeyType, alt bool) { m.Update(tea.KeyMsg{Type: k, Alt: alt}) }
	press(tea.KeyCtrlPgDown, false)
	press(tea.KeyCtrlPgDown, false)
	if m.active != 2 {
		t.Fatalf("ctrl+pgdown: %d", m.active)
	}
	press(tea.KeyCtrlPgDown, false) // wraps to the host list
	if m.active != 0 {
		t.Fatalf("wrap: %d", m.active)
	}
	press(tea.KeyCtrlPgUp, false)
	if m.active != 2 {
		t.Fatalf("ctrl+pgup: %d", m.active)
	}
	press(tea.KeyLeft, true)
	if m.active != 1 {
		t.Fatalf("alt+left: %d", m.active)
	}
	press(tea.KeyRight, true)
	press(tea.KeyRight, true)
	if m.active != 0 {
		t.Fatalf("alt+right: %d", m.active)
	}
	keys(m, "/")
	press(tea.KeyRight, true)
	if m.active != 0 {
		t.Fatal("alt+right left the search field")
	}
	press(tea.KeyCtrlPgDown, false)
	if m.active != 1 {
		t.Fatalf("ctrl+pgdown from the search: %d", m.active)
	}
	// with the settings open
	m.switchTab(0)
	keys(m, "esc", "esc", ",")
	if _, ok := m.modal.(*settingsModal); !ok {
		t.Fatalf("no settings: %T", m.modal)
	}
	press(tea.KeyCtrlPgDown, false)
	if m.active != 1 || m.parked == nil {
		t.Fatalf("ctrl+pgdown in the settings: %d %T", m.active, m.parked)
	}
}
