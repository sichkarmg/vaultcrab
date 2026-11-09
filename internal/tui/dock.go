// SPDX-License-Identifier: MIT

package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The settings, the item form and the jump picker belong to the host list (tab 0): while
// one is open the other tabs can still be switched to (prefix + digit,
// next/previous tab, a click in the tab bar). The dialog is parked then and
// shown again on the return to the host list.

// docked is a dialog that stays with the host list.
type docked interface{ docked() }

func (*settingsModal) docked() {}
func (*hostForm) docked()      {}
func (*jumpPicker) docked()    {}

func isDocked(d modal) bool { _, ok := d.(docked); return ok }

// switchTab activates tab i, parking a docked dialog of the host list.
func (m *Model) switchTab(i int) {
	if i != 0 && m.active == 0 && isDocked(m.modal) {
		m.parked, m.modal = m.modal, nil
	}
	m.setActive(i)
	m.unpark()
}

// unpark shows the parked dialog again once the host list is active and no
// other dialog is open.
func (m *Model) unpark() {
	if m.parked != nil && m.active == 0 && m.modal == nil {
		m.modal, m.parked = m.parked, nil
	}
}

// toParked delivers a message for the parked dialog (a background result,
// e.g. the notes of the item loaded or the item saved).
func (m *Model) toParked(msg tea.Msg) tea.Cmd {
	cur := m.modal
	m.modal = m.parked
	cmd := m.modal.update(m, msg)
	m.parked, m.modal = m.modal, cur
	return cmd
}

// openSettings opens the settings over the host list: the parked ones
// (or the parked item form) are shown again instead of a new dialog.
func (m *Model) openSettings() {
	if m.parked != nil {
		m.switchTab(0)
		return
	}
	if m.active != 0 && m.modal == nil {
		m.setActive(0)
	}
	if m.modal == nil {
		m.modal = newSettings(m)
	}
}

// dockedKey handles the keys that switch tabs while a docked dialog is
// open: the prefix with a digit, next/previous tab or the host list;
// Ctrl+PgDn/PgUp.
// It reports whether the key was used.
func (m *Model) dockedKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	k := msg.String()
	if m.prefix {
		m.prefix = false
		if len(k) == 1 && k[0] >= '0' && k[0] <= '9' || isTabAction(m.keymap[k]) {
			return m.prefixed(msg), true
		}
		return nil, true // other prefixed actions wait for the dialog to close
	}
	if k == m.opt.Prefix {
		m.prefix = true
		return nil, true
	}
	if k == "ctrl+pgdown" || k == "ctrl+pgup" {
		return m.tabKey(k), true
	}
	return nil, false
}

func isTabAction(a string) bool {
	switch a {
	case core.ActNextTab, core.ActPrevTab, core.ActList:
		return true
	}
	return false
}

// tabKey handles the keys that go to the next/previous tab without the
// prefix: Ctrl+PgDn/PgUp, Alt+→/← (not while a text field has the focus:
// it moves by words there). Terminals send Ctrl+Tab as a plain Tab (or keep
// it for their own tabs), so it is not one of them.
func (m *Model) tabKey(k string) tea.Cmd {
	switch k {
	case "ctrl+pgdown", "alt+right":
		m.switchTab((m.active + 1) % (len(m.tabs) + 1))
	case "ctrl+pgup", "alt+left":
		m.switchTab((m.active + len(m.tabs)) % (len(m.tabs) + 1))
	}
	return nil
}

func isTabKey(k string, typing bool) bool {
	switch k {
	case "ctrl+pgdown", "ctrl+pgup":
		return true
	case "alt+right", "alt+left":
		return !typing
	}
	return false
}
