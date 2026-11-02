// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// prefix c opens the host of the focused pane again in a new tab.
func TestCloneTab(t *testing.T) {
	m, _ := newTestModel(t, false)
	m.keymap["c"] = core.ActCloneTab
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "enter")
	if len(m.tabs) != 1 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	keys(m, "ctrl+b", "c")
	if len(m.tabs) != 2 || m.active != 2 {
		t.Fatalf("tabs %d active %d", len(m.tabs), m.active)
	}
	a, b := m.tabs[0].focused(), m.tabs[1].focused()
	if b.Host.ID != a.Host.ID || a == b {
		t.Errorf("clone of %q is %q (same session: %v)", a.Host.Name, b.Host.Name, a == b)
	}
}

// Splitting offers the host of the focused pane first, selected (before
// the local shell, which is first otherwise).
func TestSplitPickerOffersSameHost(t *testing.T) {
	m, _ := newTestModel(t, false)
	m.keymap["|"] = core.ActSplitRight
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "enter")
	keys(m, "ctrl+b", "|")
	p, ok := m.modal.(*pickerModal)
	if !ok {
		t.Fatalf("modal %T", m.modal)
	}
	if len(p.rows) < 3 || p.rows[0].Host.Name != "alpha" || p.cursor != 0 {
		t.Fatalf("first row %q cursor %d", p.rows[0].Host.Name, p.cursor)
	}
	n := 0
	for _, r := range p.rows {
		if r.Host.ID == p.first {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the host is listed %d times", n)
	}
	keys(m, "enter")
	tb := m.curTab()
	if tb.ws.Len() != 2 {
		t.Fatalf("panes %d", tb.ws.Len())
	}
	for _, s := range tb.ws.Sessions() {
		if s.Host.Name != "alpha" {
			t.Errorf("pane with %q", s.Host.Name)
		}
	}
}
