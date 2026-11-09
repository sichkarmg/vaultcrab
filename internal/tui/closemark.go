// SPDX-License-Identifier: MIT

package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Every dialog in a box has a ✕ in its top border, near the right corner:
// a click closes it, like Esc pressed until it is gone (a form inside the
// dialog first, a step back, then the dialog; a dialog that asks before
// closing still asks).

// closeMark draws the ✕ into the top border of dlg (a dialog in a box).
func (m *Model) closeMark(dlg []string) []string {
	m.dlgClose = -1
	if len(dlg) < 3 || m.dlgW < 8 {
		return dlg
	}
	top := ansi.Strip(dlg[0])
	if !strings.HasPrefix(top, m.g.border.TopLeft) {
		return dlg
	}
	col := m.dlgW - 3
	mark := m.ascii(" ✕ ")
	if ansi.StringWidth(top) < col+2 {
		return dlg
	}
	out := append([]string(nil), dlg...)
	out[0] = ansi.Truncate(dlg[0], col-1, "") + stKey.Render(mark) + ansi.TruncateLeft(dlg[0], col+2, "")
	m.dlgClose = col
	return out
}

// onCloseMark reports a left click on the ✕ of the dialog.
func (m *Model) onCloseMark(msg tea.MouseMsg) bool {
	if m.dlgClose < 0 || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return false
	}
	x := msg.X - m.dlgX
	return msg.Y == m.dlgY && x >= m.dlgClose-1 && x <= m.dlgClose+1
}

// closeModal closes the dialog as Esc does, repeated while the same
// dialog stays open (at most a few times).
func (m *Model) closeModal() tea.Cmd {
	d := m.modal
	var cmds []tea.Cmd
	for range 4 {
		if d == nil || m.modal != d {
			break
		}
		cmds = append(cmds, d.update(m, tea.KeyMsg{Type: tea.KeyEsc}))
	}
	return tea.Batch(cmds...)
}
