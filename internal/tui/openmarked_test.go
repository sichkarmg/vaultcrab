// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// Space marks hosts (✓) and moves down; Enter asks "Open N sessions at
// once?" and opens them all, the first one shown; Esc clears the marks.
func TestOpenMarkedHosts(t *testing.T) {
	m, svc := newTestModel(t, false)
	t.Cleanup(svc.Shutdown)
	// the hosts of the fake vault: alpha (SSH, ID 1) and vnc-box (ID 2)
	keys(m, "/", "a", "l", "p", "h", "a", "enter")
	if r := m.list.current(); r == nil || r.Kind != core.HostRow || r.Host.Name != "alpha" {
		t.Fatalf("current %+v", r)
	}
	keys(m, " ")
	if !m.list.marked["1"] {
		t.Fatal("not marked")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "✓") || !strings.Contains(v, "1 marked") {
		t.Fatalf("no mark:\n%s", v)
	}
	// Esc clears the marks first, the search stays
	keys(m, "esc")
	if len(m.list.marked) != 0 || m.list.search.Value() == "" {
		t.Fatalf("marks %v search %q", m.list.marked, m.list.search.Value())
	}
	keys(m, "esc") // clears the search
	m.list.toggleMarkOf("1")
	m.list.toggleMarkOf("2")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := m.modal.(*confirmModal); !ok {
		t.Fatalf("modal %T", m.modal)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Open 2 sessions at once?") || !strings.Contains(v, "alpha") || !strings.Contains(v, "vnc-box") || !strings.Contains(v, "[ Open ]") {
		t.Fatalf("question:\n%s", v)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Open is focused
	runCmd(m, cmd)
	if m.modal != nil || len(m.list.marked) != 0 {
		t.Fatalf("modal %T marks %v", m.modal, m.list.marked)
	}
	// alpha opens in a tab; the VNC host starts its external program (no
	// client in tests: it is not a tab)
	if len(m.tabs) != 1 || m.tabs[0].focused().Host.Name != "alpha" || m.active != 1 {
		t.Fatalf("tabs %d active %d", len(m.tabs), m.active)
	}
}

// Ctrl+click marks a host without opening it; one marked host opens
// without a question.
func TestCtrlClickMarks(t *testing.T) {
	m, svc := newTestModel(t, false)
	t.Cleanup(svc.Shutdown)
	m.View()
	y := -1
	for i, r := range m.list.rows {
		if r.Kind == core.HostRow && r.Host.ID == "1" {
			y = m.bodyRect().y + m.list.listTop + i - m.list.offset
		}
	}
	if y < 0 {
		t.Fatal("no row")
	}
	m.Update(tea.MouseMsg{X: 6, Y: y, Ctrl: true, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !m.list.marked["1"] || len(m.tabs) != 0 {
		t.Fatalf("marks %v tabs %d", m.list.marked, len(m.tabs))
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.modal != nil || len(m.tabs) != 1 {
		t.Fatalf("modal %T tabs %d", m.modal, len(m.tabs))
	}
}
