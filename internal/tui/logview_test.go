// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The event log: turned on in Settings → Behavior, a ≡ button appears in
// the tab bar and opens the viewer (also prefix g).
func TestEventLogView(t *testing.T) {
	cfg := config.Default()
	cfg.Log.File = filepath.Join(t.TempDir(), "vaultcrab.log")
	svc := core.New(core.Options{Config: &cfg, ConfigPoll: -1,
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { svc.SetLogging(false) })
	svc.Start(ctx)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActShowLog: "g", core.ActList: "l"}, Icons: testIcons(false), ShowTree: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if strings.Contains(ansi.Strip(m.tabBar()), "≡") {
		t.Fatal("log button while the log is off")
	}
	// Settings → Behavior → Event log: on
	m.openSettings()
	s := m.modal.(*settingsModal)
	s.page = pageBehavior
	rows := s.settings(m)
	r := slices.IndexFunc(rows, func(x setting) bool { return x.label == "Event log" })
	if r < 0 {
		t.Fatal("no Event log row")
	}
	s.change(m, r, 0)
	if !svc.LogEnabled() {
		t.Fatal("the log was not turned on")
	}
	m.modal = nil
	keys(m, "s") // sync: something to log
	time.Sleep(100 * time.Millisecond)

	bar := ansi.Strip(m.tabBar())
	x := strings.Index(bar, "≡")
	if x < 0 || !strings.HasSuffix(bar, "│ ≡ │ ⚙ │ ⊗ ") {
		t.Fatalf("no log button: %q", bar)
	}
	m.Update(tea.MouseMsg{X: ansi.StringWidth(bar[:x]), Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	l, ok := m.modal.(*logModal)
	if !ok {
		t.Fatalf("click opened %T", m.modal)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Event log") || !strings.Contains(v, "log: on") || !strings.Contains(v, cfg.Log.File) {
		t.Fatalf("viewer:\n%s", v)
	}
	checkFrame(t, m.View(), 120, 30)
	if len(l.lines) == 0 {
		t.Fatal("no lines")
	}
	keys(m, "esc")
	if m.modal != nil {
		t.Fatal("Esc did not close")
	}
	// the settings button still works next to it
	if m.buttonAt(m.w-1) != "quit" || m.buttonAt(m.tabsWidth()) != "log" {
		t.Errorf("buttons: %q %q", m.buttonAt(m.w-1), m.buttonAt(m.tabsWidth()))
	}
	keys(m, "ctrl+b", "g")
	if _, ok := m.modal.(*logModal); !ok {
		t.Fatalf("prefix g opened %T", m.modal)
	}
	// it stays with the host list: tab 1 can be used while it is open
	lv := m.modal.(*logModal)
	m.tabs = append(m.tabs, newTab(svc.OpenLocal(80, 20)))
	keys(m, "ctrl+b", "1")
	if m.active != 1 || m.modal != nil || m.parked != lv {
		t.Fatalf("switch: active %d modal %T parked %T", m.active, m.modal, m.parked)
	}
	keys(m, "ctrl+b", "g") // back to it from the session tab
	if m.active != 0 || m.modal != lv {
		t.Fatalf("back: active %d modal %T", m.active, m.modal)
	}
}
