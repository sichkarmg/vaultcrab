// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
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

// ui.tab_fit: the titles share the width of the bar evenly; with many
// tabs only the numbers are left, and all tabs stay visible as long as the
// numbers fit (beyond that the bar scrolls as with a fixed width).
func TestTabFit(t *testing.T) {
	m, svc := newTestModel(t, false)
	t.Cleanup(svc.Shutdown)
	m.opt.Appearance.TabFit = true
	m.opt.Appearance.GroupTabs = false
	add := func(name string) {
		s, err := svc.RunScript(m.ctx, svc.LocalHost(), core.Script{Name: name, Command: []string{"sleep", "5"}}, 80, 20)
		if err != nil {
			t.Fatal(err)
		}
		m.tabs = append(m.tabs, newTab(s))
	}
	add("short")
	add("a-rather-long-title-of-a-tab-that-would-not-fit")
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 30})
	labels := m.tabLabels()
	if l := ansi.Strip(labels[2]); !strings.Contains(l, "a-rather-long-title-of-a-tab-that-would-not-fit: local") {
		t.Fatalf("wide window, full titles: %q", l)
	}
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	_, starts, first := m.tabSpans()
	if first != 1 || starts[1] < 0 || starts[2] < 0 {
		t.Fatalf("not all tabs shown: first %d starts %v", first, starts)
	}
	short, long := ansi.Strip(m.tabLabels()[1]), ansi.Strip(m.tabLabels()[2])
	if !strings.Contains(short, "short: local") || !strings.Contains(long, "…") {
		t.Fatalf("narrow: %q %q", short, long)
	}
	if bar := ansi.Strip(m.tabBar()); ansi.StringWidth(bar) != 90 {
		t.Fatalf("bar width %d", ansi.StringWidth(bar))
	}
	// many tabs: the titles shrink evenly, down to the numbers
	for i := 0; i < 8; i++ {
		add(fmt.Sprintf("tab-number-%d", i))
	}
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	_, starts, first = m.tabSpans()
	if bar := ansi.Strip(m.tabBar()); first != 1 || !strings.Contains(bar, " 10 ") {
		t.Fatalf("scrolled: first %d\n%s", first, bar)
	}
	for i, l := range m.tabLabels()[1:] {
		if !strings.Contains(ansi.Strip(l), strconv.Itoa(i+1)) {
			t.Fatalf("tab %d: %q", i+1, ansi.Strip(l))
		}
	}
	// off: the fixed width again
	m.opt.Appearance.TabFit = false
	if l := ansi.Strip(m.tabLabels()[2]); !strings.Contains(l, "a-rather-long-title-of-…") {
		t.Fatalf("fixed: %q", l)
	}
}

// With ui.tab_fit a split tab keeps its pane count when its title is cut
// to nothing.
func TestTabFitKeepsPaneCount(t *testing.T) {
	m, _ := splitTab(t)
	m.opt.Appearance.TabFit = true
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	if l := ansi.Strip(m.tabLabels()[1]); !strings.Contains(l, "+1") {
		t.Fatalf("label %q", l)
	}
}
