// SPDX-License-Identifier: MIT

package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// splitTab opens a tab of alpha split into two panes, both broken (the fake
// source has no secret).
func splitTab(t *testing.T) (*Model, *tab) {
	t.Helper()
	m, _ := newTestModel(t, false)
	m.opt.Keys = map[string]string{core.ActSplitRight: "|", core.ActClosePane: "x", core.ActCloseTab: "&"}
	m.keymap = map[string]string{"|": core.ActSplitRight, "x": core.ActClosePane, "&": core.ActCloseTab}
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "enter")
	keys(m, "ctrl+b", "|", "enter")
	tb := m.curTab()
	if tb == nil || tb.ws.Len() != 2 {
		t.Fatal("no split tab")
	}
	for _, s := range tb.ws.Sessions() {
		for i := 0; ; i++ {
			if st, _ := s.State(); st == core.SessionDisconnected {
				break
			}
			if i == 500 {
				t.Fatal("the session did not fail")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return m, tb
}

// The ✕ in a pane header closes that pane only.
func TestPaneCloseButton(t *testing.T) {
	m, tb := splitTab(t)
	m.View()
	left := tb.ws.Panes()[0]
	pr := tb.ws.Geometry().Panes[left]
	r := m.bodyRect()
	x := r.x + pr.X + pr.W - 2 // inside " ✕ "
	if !tb.closeButtonAt(m, left, x-r.x, pr.Y) {
		t.Fatalf("no close button at %d (pane %+v)", x, pr)
	}
	m.Update(tea.MouseMsg{X: x, Y: r.y + pr.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(m.tabs) != 1 || tb.ws.Len() != 1 {
		t.Fatalf("after the click: tabs %d panes %d (modal %T)", len(m.tabs), tb.ws.Len(), m.modal)
	}
	if tb.ws.Panes()[0] == left {
		t.Error("the other pane was closed")
	}
	// one pane: no header, no button
	if tb.closeButtonAt(m, tb.ws.Panes()[0], x-r.x, 0) {
		t.Error("close button on a single pane")
	}
}

// Ctrl+D in a broken pane closes it; in the last pane it closes the tab.
func TestCtrlDClosesBrokenPane(t *testing.T) {
	m, tb := splitTab(t)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if tb.ws.Len() != 1 || len(m.tabs) != 1 {
		t.Fatalf("panes %d tabs %d (modal %T)", tb.ws.Len(), len(m.tabs), m.modal)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if len(m.tabs) != 0 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
}
