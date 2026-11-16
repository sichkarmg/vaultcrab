// SPDX-License-Identifier: MIT

package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// Color labels of hosts: the field color of an item, ui.tag_colors or
// ui.folder_colors (core.Service.HostColor). A labeled host shows its name
// in that color in the host list, and its tabs and pane headers have their
// title in it (on that color when active or focused), so that a session on
// e.g. a production server stands out.

// hostColor returns the color label of h ("" for none).
func (m *Model) hostColor(h *core.Host) string {
	if h == nil {
		return ""
	}
	return m.svc.HostColor(h)
}

// sessionColor is hostColor of the host of a session, as it is now in the
// vault (its label may have changed since the session opened).
func (m *Model) sessionColor(h *core.Host) string {
	if live, ok := m.svc.Host(h.ID); ok && h.ID != "" {
		return m.hostColor(&live)
	}
	return m.hostColor(h)
}

// labelStyle is the style of a labeled title: the text in color c, or
// on c (black or white text) when active.
func labelStyle(c string, active bool) lipgloss.Style {
	if active {
		return lipgloss.NewStyle().Bold(true).Background(lipgloss.Color(c)).Foreground(lipgloss.Color(core.ContrastText(c)))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(c))
}

// colorSwatch is a sample of a color in the host form.
func colorSwatch(m *Model) string {
	if m.opt.Icons.ASCII() {
		return "##"
	}
	return "██"
}
