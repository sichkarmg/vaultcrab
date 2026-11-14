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

// The sessions menu: the ↪N button by the settings, as ⇄N for the tunnels.
// It holds the sessions through a jump host (ui.jump_tabs_menu) and the VNC
// sessions, the client behind a local proxy (ui.vnc_tabs_menu). Such tabs
// stay out of the tab bar; the active one is shown there while it is
// active, so its ✕ and state work as on any tab. The clients of RDP, SMB,
// FTP and SFTP open directly, without a tab: only the event log has them.

// isJump reports a tab whose focused session goes through a jump host.
func (t *tab) isJump() bool { return core.IsJump(t.focused().Host) }

// isVNC reports a tab of a VNC client (behind a local proxy).
func (t *tab) isVNC() bool { return t.focused().Host.Scheme == core.SchemeVNC }

// menuTab reports a tab that belongs to the sessions menu.
func (m *Model) menuTab(t *tab) bool {
	a := m.opt.Appearance
	return a.JumpTabsMenu && t.isJump() || a.VNCTabsMenu && t.isVNC()
}

// inMenu reports tab n (1-based) as kept in the sessions menu, not in the
// tab bar.
func (m *Model) inMenu(n int) bool {
	return n != m.active && n >= 1 && n <= len(m.tabs) && m.menuTab(m.tabs[n-1])
}

// menuItems returns the tabs (1-based) of the menu: the jump sessions,
// then the VNC sessions (a VNC session through a jump host is a jump one).
func (m *Model) menuItems() (jumps, vncs []int) {
	a := m.opt.Appearance
	for i, t := range m.tabs {
		switch {
		case a.JumpTabsMenu && t.isJump():
			jumps = append(jumps, i+1)
		case a.VNCTabsMenu && t.isVNC():
			vncs = append(vncs, i+1)
		}
	}
	return jumps, vncs
}

// menuButton is the ↪N button (empty when the menu is empty), in the color
// of the worst state of its tabs: red if one is broken, yellow while one
// connects.
func (m *Model) menuButton() (string, lipgloss.Style) {
	jumps, vncs := m.menuItems()
	n := len(jumps) + len(vncs)
	if n == 0 {
		return "", stKey
	}
	st := stKey
	for _, it := range append(jumps, vncs...) {
		switch m.tabs[it-1].ws.State() {
		case core.SessionDisconnected:
			st = stErr
		case core.SessionConnecting:
			if st.GetForeground() != stErr.GetForeground() {
				st = stWarn
			}
		}
	}
	return " " + m.g.jump + strconv.Itoa(n) + " ", st
}

// sessionsMenu is the dialog: Enter or a click shows a session, d /
// [close] closes it.
type sessionsMenu struct {
	cursor int
	items  []int // the tabs as last shown
	rows   []int // dialog row of each item
	btns   [][]btnSpan
}

func (m *Model) openSessionsMenu() {
	s := &sessionsMenu{}
	jumps, vncs := m.menuItems()
	for i, it := range append(jumps, vncs...) {
		if it == m.active {
			s.cursor = i
		}
	}
	m.modal = s
}

func (s *sessionsMenu) act(m *Model, name string) tea.Cmd {
	if s.cursor >= len(s.items) {
		return nil
	}
	n := s.items[s.cursor]
	m.modal = nil
	if n > len(m.tabs) {
		return nil
	}
	if name == "open" {
		m.switchTab(n)
	} else {
		m.confirmCloseTab(m.tabs[n-1])
	}
	return nil
}

func (s *sessionsMenu) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "esc", "q":
		m.modal = nil
	case "up", "k", "ctrl+p":
		s.cursor = max(0, s.cursor-1)
	case "down", "j", "ctrl+n":
		s.cursor = max(0, min(len(s.items)-1, s.cursor+1))
	case "enter":
		return s.act(m, "open")
	case "d", "x", "delete":
		return s.act(m, "close")
	}
	return nil
}

func (s *sessionsMenu) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	for i, r := range s.rows {
		if r != y {
			continue
		}
		s.cursor = i
		for _, b := range s.btns[i] {
			if x-boxPadX >= b.x0 && x-boxPadX < b.x1 {
				return s.act(m, b.name)
			}
		}
		return s.act(m, "open") // a click on the session itself shows it
	}
	return nil
}

func (s *sessionsMenu) view(m *Model, w, h int) []string {
	jumps, vncs := m.menuItems()
	s.items = append(jumps, vncs...)
	s.cursor = max(0, min(s.cursor, len(s.items)-1))
	s.rows, s.btns = s.rows[:0], s.btns[:0]
	tw := max(20, min(56, w-34))
	var b strings.Builder
	line := 0 // lines written
	write := func(str string) {
		b.WriteString(str + "\n")
		line++
	}
	section := func(title string, items []int, base int) {
		if len(items) == 0 {
			return
		}
		if line > 0 {
			write("")
		}
		write(stDim.Render(title))
		for i, it := range items {
			t := m.tabs[it-1]
			icon, num, title, note := t.icon(m, false), strconv.Itoa(it), t.title(m.g), ""
			if it == m.active {
				note = "  (shown)"
			}
			text := fmt.Sprintf("%-3s", num) + padRight(ansi.Truncate(title, tw, m.g.ell), tw)
			if base+i == s.cursor {
				text = stSelected.Render(ansi.Strip(text))
			}
			lead := padRight(icon, 2) + " " + text + "  "
			x := ansi.StringWidth(lead)
			out := lead
			var spans []btnSpan
			for k, name := range []string{"open", "close"} {
				if k > 0 {
					out += " "
					x++
				}
				lbl := "[" + name + "]"
				out += stKey.Render(lbl)
				spans = append(spans, btnSpan{x0: x, x1: x + len(lbl), name: name})
				x += len(lbl)
			}
			s.rows = append(s.rows, boxPadY+boxTitleGap+line)
			s.btns = append(s.btns, spans)
			write(out + stDim.Render(note))
		}
	}
	section("Through a jump host", jumps, 0)
	section("VNC (local proxy)", vncs, len(jumps))
	if len(s.items) == 0 {
		write(stDim.Render("no sessions here"))
	}
	write("")
	b.WriteString(stDim.Render("Enter/click: show · d: close · Esc: back"))
	return m.box("Sessions", b.String(), w)
}
