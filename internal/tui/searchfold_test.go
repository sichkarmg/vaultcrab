// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A search shows the hosts it finds in folded folders: the folders open
// for the search (and can be folded during it); clearing it brings back
// the folds of the list.
func TestSearchOpensFoldedFolders(t *testing.T) {
	m, _ := newTestModel(t, false)
	l := &m.list
	for _, r := range l.rows {
		if r.Label == "Lab" {
			l.setCollapsed(r.Key, true)
		}
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "alpha") {
		t.Fatalf("alpha shown in a folded folder:\n%s", v)
	}
	keys(m, "/")
	typeText(m, "alph")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "alpha") {
		t.Fatalf("the search does not show alpha:\n%s", v)
	}
	// folded during the search: the search's own fold
	for _, r := range l.rows {
		if r.Label == "Linux" {
			l.setCollapsed(r.Key, true)
		}
	}
	for _, r := range l.rows {
		if r.Host != nil && r.Host.Name == "alpha" {
			t.Fatal("alpha shown in a folder folded during the search")
		}
	}
	if len(l.collapsed) != 1 {
		t.Fatalf("the user's folds changed: %v", l.collapsed)
	}
	keys(m, "esc", "esc")
	if l.search.Value() != "" {
		t.Fatalf("search %q", l.search.Value())
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "alpha") {
		t.Fatalf("Lab not folded again after the search:\n%s", v)
	}
}
