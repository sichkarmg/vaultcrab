// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// clickItem clicks at x (relative) in the tab bar entry i.
func clickItem(m *Model, i, x int) {
	_, starts, _ := m.tabSpans()
	m.Update(tea.MouseMsg{X: starts[i] + x, Y: m.tabBarY(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

// ✕ on the active tab closes it (also when it is not connected).
func TestTabCloseButton(t *testing.T) {
	m, _ := newTestModel(t, false)
	a := m.opt.Appearance
	a.TabCloseButton = true
	m.applyAppearance(a)
	openTabs(t, m, 2)
	waitAllTabs(t, m, core.SessionDisconnected)
	m.setActive(2)
	items := m.tabItems()
	if items[1].close != [2]int{} || items[2].close == [2]int{} {
		t.Fatalf("close button on the wrong tab: %+v / %+v", items[1].close, items[2].close)
	}
	if !strings.Contains(ansi.Strip(items[2].label), "✕") {
		t.Fatalf("label %q", ansi.Strip(items[2].label))
	}
	clickItem(m, 2, items[2].close[0])
	if len(m.tabs) != 1 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	// off: no button
	a.TabCloseButton = false
	m.applyAppearance(a)
	m.setActive(1)
	if m.tabItems()[1].close != [2]int{} {
		t.Fatal("button while off")
	}
}

// A click on the state icon or number closes a tab when the setting is on:
// an inactive one at once (nothing connected), the active one after a
// confirmation.
func TestTabStatusClose(t *testing.T) {
	m, _ := newTestModel(t, false)
	openTabs(t, m, 3)
	waitAllTabs(t, m, core.SessionDisconnected)
	m.setActive(1)
	// off: a click on the number shows the tab
	clickItem(m, 2, 2)
	if m.active != 2 || len(m.tabs) != 3 {
		t.Fatalf("off: active %d tabs %d", m.active, len(m.tabs))
	}
	a := m.opt.Appearance
	a.TabStatusClose = true
	m.applyAppearance(a)
	m.setActive(1)
	clickItem(m, 3, 1) // the icon of the inactive tab 3
	if len(m.tabs) != 2 || m.modal != nil {
		t.Fatalf("inactive: tabs %d modal %T", len(m.tabs), m.modal)
	}
	clickItem(m, 1, 1) // the active tab: asks
	c, ok := m.modal.(*confirmModal)
	if !ok || !strings.Contains(c.text, "Close the active tab") {
		t.Fatalf("no confirmation: %T", m.modal)
	}
	keys(m, "esc")
	if len(m.tabs) != 2 {
		t.Fatal("closed although canceled")
	}
	clickItem(m, 1, 1)
	keys(m, "y")
	if len(m.tabs) != 1 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	// a click on the title still shows the tab
	items := m.tabItems()
	clickItem(m, 1, items[1].status[1]+1)
	if len(m.tabs) != 1 || m.active != 1 {
		t.Fatalf("title click: tabs %d active %d", len(m.tabs), m.active)
	}
}

// The tabs of one host are one group: the lead tab in full, the others by
// state and number after it; a click on one makes it the lead (and active),
// the lead it replaces goes back among the others. Keys still address every
// tab.
func TestTabGroups(t *testing.T) {
	m, _ := newTestModel(t, false)
	a := m.opt.Appearance
	a.GroupTabs = true
	m.applyAppearance(a)
	openTabs(t, m, 3)
	waitAllTabs(t, m, core.SessionDisconnected)
	keys(m, "t") // a local shell: another host
	if len(m.tabs) != 4 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	m.setActive(2)
	items := m.tabItems()
	if len(items) != 5 || items[1].tab != 2 || items[2].tab != 1 || items[3].tab != 3 || items[1].group == "" {
		t.Fatalf("items %+v", items)
	}
	bar := ansi.Strip(m.tabBar())
	if !strings.Contains(bar, "2:alpha") || !strings.Contains(bar, " ✗1 ✗3 │") || strings.Contains(bar, "1:alpha") || !strings.Contains(bar, "4:local") {
		t.Fatalf("group: %q", bar)
	}
	clickItem(m, 3, 1) // the number of tab 3: the new lead
	bar = ansi.Strip(m.tabBar())
	if m.active != 3 || !strings.Contains(bar, "3:alpha") || !strings.Contains(bar, " ✗1 ✗2 │") {
		t.Fatalf("lead: active %d %q", m.active, bar)
	}
	// another tab active: the group keeps its last lead
	keys(m, "ctrl+b", "n")
	if m.active != 4 || !strings.Contains(ansi.Strip(m.tabBar()), "3:alpha") {
		t.Fatalf("next: %d %q", m.active, ansi.Strip(m.tabBar()))
	}
	clickItem(m, 2, 1) // tab 1 (the first chip)
	if m.active != 1 {
		t.Fatalf("active %d", m.active)
	}
	// off: every tab on its own
	a.GroupTabs = false
	m.applyAppearance(a)
	if n := len(m.tabItems()); n != 5 {
		t.Fatalf("items %d", n)
	}
}

// With ui.tab_status_close the number of the lead closes it (the active one
// after a confirmation), also in the color mode; a click on a chip shows
// that tab, only its state icon closes it.
func TestTabGroupsStatusClose(t *testing.T) {
	m, _ := newTestModel(t, false)
	a := m.opt.Appearance
	a.GroupTabs, a.TabStatusClose = true, true
	m.applyAppearance(a)
	openTabs(t, m, 4)
	waitAllTabs(t, m, core.SessionDisconnected)
	m.setActive(1)
	clickItem(m, 2, 1) // the number of chip tab 2
	if m.active != 2 || len(m.tabs) != 4 || m.modal != nil {
		t.Fatalf("number: active %d tabs %d modal %T", m.active, len(m.tabs), m.modal)
	}
	clickItem(m, 4, 0) // the icon of chip tab 4 closes it
	if len(m.tabs) != 3 {
		t.Fatalf("icon: tabs %d", len(m.tabs))
	}
	a.TabStatus = "color"
	m.applyAppearance(a)
	bar := ansi.Strip(m.tabBar())
	if !strings.Contains(bar, "2:alpha") || !strings.Contains(bar, "‹1 ‹3 │") {
		t.Fatalf("color: %q", bar)
	}
	clickItem(m, 3, 1) // chip tab 3: shown, not closed
	if m.active != 3 || len(m.tabs) != 3 || m.modal != nil {
		t.Fatalf("color chip: active %d tabs %d modal %T", m.active, len(m.tabs), m.modal)
	}
	clickItem(m, 1, 1) // the number of the active lead: asks
	if c, ok := m.modal.(*confirmModal); !ok || !strings.Contains(c.text, "Close the active tab") {
		t.Fatalf("no confirmation: %T", m.modal)
	}
	keys(m, "y")
	if len(m.tabs) != 2 {
		t.Fatalf("lead close: tabs %d", len(m.tabs))
	}
}

// waitAllTabs waits until every tab is in state want.
func waitAllTabs(t *testing.T, m *Model, want core.SessionState) {
	t.Helper()
	for range 500 {
		ok := true
		for _, tb := range m.tabs {
			ok = ok && tb.ws.State() == want
		}
		if ok {
			return
		}
		sleepTick()
	}
	t.Fatal("tabs not all in state", want)
}
