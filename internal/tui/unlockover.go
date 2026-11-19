// SPDX-License-Identifier: MIT

package tui

import tea "github.com/charmbracelet/bubbletea"

// The unlock dialog opens over any other dialog: the dialog under it
// (m.under) waits and is shown again, as it was, when the unlock dialog
// closes (unlocked or Esc).

// openUnlock shows u over the open dialog, if any.
func (m *Model) openUnlock(u *unlockModal) {
	if _, ok := m.modal.(*unlockModal); m.modal != nil && !ok && m.under == nil {
		m.under = m.modal
	}
	m.modal = u
}

// restoreUnder shows the dialog under the unlock dialog again once it is
// closed.
func (m *Model) restoreUnder() {
	if m.modal == nil && m.under != nil {
		m.modal, m.under = m.under, nil
		if r, ok := m.modal.(interface{ reshow(*Model) }); ok {
			r.reshow(m)
		}
	}
}

// toUnder delivers a background result to the dialog under the unlock
// dialog.
func (m *Model) toUnder(msg tea.Msg) tea.Cmd {
	cur := m.modal
	m.modal = m.under
	cmd := m.modal.update(m, msg)
	if m.modal == nil {
		m.under = nil // it closed itself (e.g. saved)
	} else {
		m.under = m.modal
	}
	m.modal = cur
	return cmd
}
