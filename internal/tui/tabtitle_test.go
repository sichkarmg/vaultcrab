// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A narrow tab width shortens the name of a split tab, never its pane
// count.
func TestNarrowTabKeepsPaneCount(t *testing.T) {
	m, _ := splitTab(t)
	for w, want := range map[int]string{6: "1:al… +1", 8: "1:alpha +1", 24: "1:alpha +1"} {
		m.opt.Appearance.TabWidth = w
		if l := ansi.Strip(m.tabLabels()[1]); !strings.Contains(l, want) {
			t.Fatalf("width %d: %q, want %q", w, l, want)
		}
	}
}
