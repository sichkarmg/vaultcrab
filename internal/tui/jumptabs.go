// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// Sessions through a jump host stay out of the tab bar (ui.jump_tabs_menu):
// the ↪N button by the settings opens a menu of them, as ⇄N does for the
// tunnels. The active one is shown in the bar while it is active, so its
// ✕ and state work as on any tab.

// isJump reports a tab whose focused session goes through a jump host.
func (t *tab) isJump() bool { return core.IsJump(t.focused().Host) }

// inJumpMenu reports tab n (1-based) as kept in the jump menu, not in the
// tab bar.
func (m *Model) inJumpMenu(n int) bool {
	return m.opt.Appearance.JumpTabsMenu && n != m.active && n >= 1 && n <= len(m.tabs) && m.tabs[n-1].isJump()
}

// jumpTabs returns the numbers (1-based) of the tabs through a jump host.
func (m *Model) jumpTabs() []int {
	var out []int
	for i, t := range m.tabs {
		if t.isJump() {
			out = append(out, i+1)
		}
	}
	return out
}

// jumpButton is the ↪N button (empty when off or without such tabs), in the
// color of the worst state: red if one is broken, yellow while one
// connects.
func (m *Model) jumpButton() (string, lipgloss.Style) {
	if !m.opt.Appearance.JumpTabsMenu {
		return "", stKey
	}
	ns := m.jumpTabs()
	if len(ns) == 0 {
		return "", stKey
	}
	st := stKey
	for _, n := range ns {
		switch m.tabs[n-1].ws.State() {
		case core.SessionDisconnected:
			st = stErr
		case core.SessionConnecting:
			if st.GetForeground() != stErr.GetForeground() {
				st = stWarn
			}
		}
	}
	return " " + m.g.jump + strconv.Itoa(len(ns)) + " ", st
}

// jumpTabsModal is the menu of the jump sessions: Enter or a click shows
// one, d / [close] closes it.
type jumpTabsModal struct {
	cursor int
	listY  int         // dialog row of the first session
	btns   [][]btnSpan // per row: the x ranges of [open] and [close]
}

func (m *Model) openJumpTabs() {
	j := &jumpTabsModal{}
	for i, n := range m.jumpTabs() {
		if n == m.active {
			j.cursor = i
		}
	}
	m.modal = j
}

func (j *jumpTabsModal) act(m *Model, name string) tea.Cmd {
	ns := m.jumpTabs()
	if j.cursor >= len(ns) {
		return nil
	}
	n := ns[j.cursor]
	switch name {
	case "open":
		m.modal = nil
		m.switchTab(n)
	case "close":
		m.modal = nil
		m.confirmCloseTab(m.tabs[n-1])
	}
	return nil
}

func (j *jumpTabsModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	n := len(m.jumpTabs())
	switch k.String() {
	case "esc", "q":
		m.modal = nil
	case "up", "k", "ctrl+p":
		j.cursor = max(0, j.cursor-1)
	case "down", "j", "ctrl+n":
		j.cursor = max(0, min(n-1, j.cursor+1))
	case "enter":
		return j.act(m, "open")
	case "d", "x", "delete":
		return j.act(m, "close")
	}
	return nil
}

func (j *jumpTabsModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	r := y - j.listY
	if r < 0 || r >= len(j.btns) {
		return nil
	}
	j.cursor = r
	for _, b := range j.btns[r] {
		if x-boxPadX >= b.x0 && x-boxPadX < b.x1 {
			return j.act(m, b.name)
		}
	}
	return j.act(m, "open") // a click on the session itself shows it
}

func (j *jumpTabsModal) view(m *Model, w, h int) []string {
	ns := m.jumpTabs()
	if len(ns) == 0 {
		j.btns = nil
		return m.box("Sessions through a jump host", stDim.Render("no sessions through a jump host")+"\n\n"+stDim.Render("Esc: back"), w)
	}
	j.cursor = max(0, min(j.cursor, len(ns)-1))
	tw := max(20, min(60, w-30))
	var b strings.Builder
	j.btns = j.btns[:0]
	for i, n := range ns {
		t := m.tabs[n-1]
		title := ansi.Truncate(t.title(m.g), tw, m.g.ell)
		lead := t.icon(m, false) + " " + fmt.Sprintf("%-3s", strconv.Itoa(n)) + padRight(title, tw) + "  "
		if i == j.cursor {
			lead = t.icon(m, false) + " " + stSelected.Render(ansi.Strip(fmt.Sprintf("%-3s", strconv.Itoa(n))+padRight(title, tw))) + "  "
		}
		x := ansi.StringWidth(lead)
		var row []btnSpan
		line := lead
		for k, name := range []string{"open", "close"} {
			if k > 0 {
				line += " "
				x++
			}
			lbl := "[" + name + "]"
			line += stKey.Render(lbl)
			row = append(row, btnSpan{x0: x, x1: x + len(lbl), name: name})
			x += len(lbl)
		}
		if n == m.active {
			line += stDim.Render("  (shown)")
		}
		j.btns = append(j.btns, row)
		b.WriteString(line + "\n")
	}
	j.listY = boxPadY + boxTitleGap
	b.WriteString("\n" + stDim.Render("Enter/click: show · d: close · Esc: back"))
	return m.box("Sessions through a jump host", b.String(), w)
}
