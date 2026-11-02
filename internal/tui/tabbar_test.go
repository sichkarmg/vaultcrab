// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// openTabs opens n session tabs of the first host and returns to the list.
func openTabs(t *testing.T, m *Model, n int) {
	t.Helper()
	keys(m, "/", "a", "l", "p", "h", "a", "enter")
	for range n {
		keys(m, "enter", "ctrl+b", "l")
	}
	if len(m.tabs) != n {
		t.Fatalf("tabs %d, want %d", len(m.tabs), n)
	}
}

// With many tabs the host list tab stays at the left while the session tabs
// scroll to keep the active one visible.
func TestTabBarKeepsHostsTab(t *testing.T) {
	m, _ := newTestModel(t, false)
	openTabs(t, m, 15)
	m.setActive(15)
	bar := ansi.Strip(m.tabBar())
	if !strings.HasPrefix(bar, " 0:Hosts ") {
		t.Fatalf("hosts tab not first: %q", bar)
	}
	if !strings.Contains(bar, m.g.more) || !strings.Contains(bar, "15:alpha") {
		t.Fatalf("no scroll mark or active tab: %q", bar)
	}
	if strings.Contains(bar, " 1:alpha") {
		t.Errorf("tab 1 shown although scrolled: %q", bar)
	}
	if ansi.StringWidth(m.tabBar()) != m.w {
		t.Errorf("tab bar width %d", ansi.StringWidth(m.tabBar()))
	}
	// the labels are where tabAt finds them
	_, starts, first := m.tabSpans()
	if starts[0] != 0 || first < 2 || m.tabAt(starts[first]+2) != first || m.tabAt(starts[15]+2) != 15 {
		t.Fatalf("spans %v first %d", starts, first)
	}
	m.Update(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 0 {
		t.Fatalf("click on 0:Hosts: active %d", m.active)
	}
	// few tabs: no scroll mark
	m2, _ := newTestModel(t, false)
	openTabs(t, m2, 2)
	if bar := ansi.Strip(m2.tabBar()); strings.Contains(bar, m2.g.more) || !strings.Contains(bar, "1:alpha") {
		t.Errorf("few tabs: %q", bar)
	}
}

// Tab status "color": no state icon, the tab number in the state color.
func TestTabStatusColor(t *testing.T) {
	m, _ := newTestModel(t, false)
	openTabs(t, m, 1)
	broken := m.opt.Icons.Get(core.IconDisconnected)
	waitTabState(t, m, core.SessionDisconnected) // the fake source has no secret
	if !strings.Contains(m.tabBar(), broken) {
		t.Fatalf("icon mode: no state icon in %q", m.tabBar())
	}
	a := m.opt.Appearance
	a.TabStatus = "color"
	m.applyAppearance(a)
	bar := m.tabBar()
	if strings.Contains(bar, broken) {
		t.Errorf("color mode shows the icon: %q", bar)
	}
	if !strings.Contains(ansi.Strip(bar), " 1:alpha ") {
		t.Errorf("label %q", ansi.Strip(bar))
	}
	// (tests render without colors: check the chosen color)
	if c, ok := m.tabs[0].stateColor(false); !ok || c != stErr.GetForeground() {
		t.Errorf("broken tab color %v %v", c, ok)
	}
}

func waitTabState(t *testing.T, m *Model, want core.SessionState) {
	t.Helper()
	for range 500 {
		if m.tabs[0].ws.State() == want {
			return
		}
		sleepTick()
	}
	t.Fatalf("tab state %v", m.tabs[0].ws.State())
}

func sleepTick() { time.Sleep(10 * time.Millisecond) }
