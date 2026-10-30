// SPDX-License-Identifier: MIT

package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// TermMouse converts a Bubble Tea mouse event to terminal coordinates of a
// pane whose top-left corner is at (x0, y0).
func TermMouse(m tea.MouseMsg, x0, y0 int) (core.Mouse, bool) {
	ev := core.Mouse{X: m.X - x0, Y: m.Y - y0, Shift: m.Shift, Alt: m.Alt, Ctrl: m.Ctrl}
	if ev.X < 0 || ev.Y < 0 {
		return ev, false
	}
	switch m.Action {
	case tea.MouseActionRelease:
		ev.Action = core.MouseRelease
	case tea.MouseActionMotion:
		ev.Action = core.MouseMotion
	default:
		ev.Action = core.MousePress
	}
	switch m.Button {
	case tea.MouseButtonLeft:
		ev.Button = core.MouseLeft
	case tea.MouseButtonMiddle:
		ev.Button = core.MouseMiddle
	case tea.MouseButtonRight:
		ev.Button = core.MouseRight
	case tea.MouseButtonWheelUp:
		ev.Button = core.MouseWheelUp
	case tea.MouseButtonWheelDown:
		ev.Button = core.MouseWheelDown
	case tea.MouseButtonNone:
		ev.Button = core.MouseNone
	default:
		return ev, false
	}
	return ev, true
}
