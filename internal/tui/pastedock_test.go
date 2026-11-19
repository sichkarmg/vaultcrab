// SPDX-License-Identifier: MIT

//go:build !windows

package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The paste dialog stays with its tab: the other tabs and the host list
// can be switched to (prefix + digit, Ctrl+PgDn, a click on the tab bar),
// the dialog waits on its tab (marked ✎) and is shown again there.
func TestPasteDialogKeepsTabsSwitchable(t *testing.T) {
	m, _ := restoreModel(t, filepath.Join(t.TempDir(), "w.json"))
	m.opt.ConfirmPaste = true
	keys(m, "t")
	keys(m, "ctrl+b", "0", "t") // a second local tab
	if len(m.tabs) != 2 || m.active != 2 {
		t.Fatalf("tabs %d active %d", len(m.tabs), m.active)
	}
	for _, tb := range m.tabs {
		s := tb.focused()
		for st, _ := s.State(); st != core.SessionConnected; st, _ = s.State() {
			time.Sleep(10 * time.Millisecond)
		}
	}
	keys(m, "ctrl+b", "1")
	first := m.curTab()
	m.Update(pasteMsg("echo a-$((1+1))\necho b-$((2+2))\n"))
	p, ok := m.modal.(*pasteModal)
	if !ok {
		t.Fatalf("modal %T", m.modal)
	}

	// prefix + 2: the other tab, the dialog waits on tab 1
	keys(m, "ctrl+b", "2")
	if m.active != 2 || m.modal != nil || first.parked != p {
		t.Fatalf("active %d modal %T parked %T", m.active, m.modal, first.parked)
	}
	if bar := ansi.Strip(m.tabBar()); !strings.Contains(bar, "✎") {
		t.Fatalf("no mark: %q", bar)
	}
	// the host list, then Ctrl+PgDn back to tab 1: shown again
	keys(m, "ctrl+b", "0")
	if m.active != 0 || m.modal != nil {
		t.Fatalf("host list: active %d modal %T", m.active, m.modal)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlPgDown})
	if m.active != 1 || m.modal != p || first.parked != nil {
		t.Fatalf("back: active %d modal %T", m.active, m.modal)
	}

	// a click on tab 2 in the tab bar parks it too
	_, starts, _ := m.tabSpans()
	m.Update(tea.MouseMsg{X: starts[2] + 2, Y: m.tabBarY(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 2 || m.modal != nil || first.parked != p {
		t.Fatalf("click: active %d modal %T", m.active, m.modal)
	}
	keys(m, "ctrl+b", "1")
	if m.modal != p {
		t.Fatalf("modal %T", m.modal)
	}
	// the dialog still pastes into its session
	p.focus = pasteDo
	keys(m, "enter")
	if m.modal != nil {
		t.Fatal("not pasted")
	}
	waitView(t, m, "b-4")
}
