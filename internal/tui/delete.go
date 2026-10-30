// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// deleteModal confirms deleting a vault item. Cancel is the default: the
// item is deleted only with y or the Delete button.
type deleteModal struct {
	host  core.Host
	focus int // delDelete or delCancel
	busy  bool
	err   string
	spans [][2]int
	row   int
}

const (
	delDelete = iota
	delCancel
)

var deleteLabels = []string{"Delete", "Cancel"}

type deletedMsg struct {
	name string
	err  error
}

func newDelete(h core.Host) *deleteModal { return &deleteModal{host: h, focus: delCancel} }

func (d *deleteModal) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case deletedMsg:
		d.busy = false
		if msg.err != nil {
			d.err = msg.err.Error()
			return nil
		}
		m.modal = nil
		m.notify(false, "%s moved to the vault trash", msg.name)
	case tea.KeyMsg:
		if d.busy {
			return nil
		}
		switch strings.ToLower(msg.String()) {
		case "left", "right", "tab", "shift+tab", "h", "l":
			d.focus = 1 - d.focus
		case "enter", " ":
			return d.choose(m, d.focus)
		case "y", "д":
			return d.choose(m, delDelete)
		case "n", "esc", "q", "н":
			return d.choose(m, delCancel)
		}
	}
	return nil
}

func (d *deleteModal) choose(m *Model, i int) tea.Cmd {
	if i == delCancel {
		m.modal = nil
		return nil
	}
	d.busy, d.err = true, ""
	svc, ctx, h := m.svc, m.ctx, d.host
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return deletedMsg{name: h.Name, err: svc.DeleteHost(c, h.ID)}
	}
}

func (d *deleteModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if d.busy || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || y != d.row {
		return nil
	}
	if i := spanAt(d.spans, x-boxPadX); i >= 0 {
		return d.choose(m, i)
	}
	return nil
}

func (d *deleteModal) view(m *Model, w, h int) []string {
	target := d.host.Addr
	if d.host.User != "" {
		target = d.host.User + "@" + target
	}
	lines := []string{
		fmt.Sprintf("Delete %s?", stBold.Render(d.host.Name)),
		stDim.Render(string(d.host.Scheme) + " " + target),
		"",
		"The item is moved to the vault trash (it can be restored there).",
	}
	var btn string
	btn, d.spans = buttonRow(deleteLabels, d.focus)
	switch {
	case d.busy:
		btn = stDim.Render("Deleting…")
	case d.err != "":
		lines = append(lines, "", stErr.Render(d.err))
	}
	lines = append(lines, "", btn, "", stDim.Render("y: delete · n/Esc: cancel · ←/→ Enter: choose"))
	d.row = boxPadY + boxTitleGap + len(lines) - 3
	return m.box("Delete item", strings.Join(lines, "\n"), w)
}
