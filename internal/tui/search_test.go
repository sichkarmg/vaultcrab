// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The search: Esc leaves the field and keeps it (the list keys work), a
// second Esc clears it, / edits it again; the buttons of the second header
// row do the same with the mouse, and [Reset filters] shows all items.
func TestSearchEscAndButtons(t *testing.T) {
	m, _ := newTestModel(t, false)
	row := func() string { return ansi.Strip(strings.Split(m.View(), "\n")[2]) } // the tab bar, the filters, this
	click := func(part int) {
		t.Helper()
		m.View()
		r := m.list.selX[part]
		if r[0] < 0 {
			t.Fatalf("part %d not shown: %q", part, row())
		}
		m.Update(tea.MouseMsg{X: r[0] + 1, Y: 2, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	}
	all := len(m.list.rows)
	keys(m, "/", "a", "l", "p")
	if !strings.Contains(row(), searchExitLabel) {
		t.Fatalf("no exit button: %q", row())
	}
	keys(m, "esc")
	if m.list.searching || m.list.search.Value() != "alp" {
		t.Fatalf("first Esc: searching %v %q", m.list.searching, m.list.search.Value())
	}
	if r := row(); !strings.Contains(r, "/alp") || !strings.Contains(r, searchClearLabel) || !strings.Contains(r, resetLabel) {
		t.Fatalf("after Esc: %q", r)
	}
	n := len(m.list.rows)
	if n >= all {
		t.Fatalf("not filtered: %d of %d", n, all)
	}
	keys(m, "g") // a list key, not typed into the search
	if m.list.search.Value() != "alp" {
		t.Fatalf("typed into the search: %q", m.list.search.Value())
	}
	keys(m, "/", "h")
	if !m.list.searching || m.list.search.Value() != "alph" {
		t.Fatalf("/ again: %v %q", m.list.searching, m.list.search.Value())
	}
	keys(m, "esc", "esc")
	if m.list.searching || m.list.search.Value() != "" || strings.Contains(row(), resetLabel) {
		t.Fatalf("second Esc: %v %q %q", m.list.searching, m.list.search.Value(), row())
	}

	// the mouse: into the search, out with its button, clear with it
	click(selSearch)
	if !m.list.searching {
		t.Fatal("click on the search")
	}
	keys(m, "b")
	click(selSearchBtn)
	if m.list.searching || m.list.search.Value() != "b" {
		t.Fatalf("[Esc to exit]: %v %q", m.list.searching, m.list.search.Value())
	}
	click(selSearchBtn)
	if m.list.search.Value() != "" {
		t.Fatalf("[Esc again to clear]: %q", m.list.search.Value())
	}

	// the selectors are hit where they are drawn
	g := m.list.grouping
	click(selGroup)
	if m.list.grouping == g {
		t.Error("click on group")
	}
	click(selOrg)
	if m.list.org == "" {
		t.Error("click on org")
	}

	// [Reset filters]: type filter, org, search
	keys(m, "tab", "/", "x")
	click(selReset)
	if m.list.filterIdx != 0 || m.list.org != "" || m.list.search.Value() != "" || m.list.searching {
		t.Fatalf("reset: filter %d org %q search %q", m.list.filterIdx, m.list.org, m.list.search.Value())
	}
	// F with the keyboard
	keys(m, "tab", "F")
	if m.list.filterIdx != 0 {
		t.Fatal("F did not reset")
	}
}
