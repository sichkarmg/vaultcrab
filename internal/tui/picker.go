// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// pickerModal chooses a host (e.g. for a new pane).
type pickerModal struct {
	in     textinput.Model
	rows   []core.Row
	cursor int
	first  string // ID of the host offered first (the focused session: a clone)
	choose func(m *Model, h core.Host) tea.Cmd
}

// newPicker offers the host with ID first at the top, selected, so Enter
// opens the same host again.
func newPicker(m *Model, first string, choose func(m *Model, h core.Host) tea.Cmd) *pickerModal {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "host to open in the new pane"
	in.Focus()
	p := &pickerModal{in: in, first: first, choose: choose}
	p.refresh(m)
	return p
}

func (p *pickerModal) refresh(m *Model) {
	q, err := core.ParseRule(p.in.Value())
	if err != nil {
		q, _ = core.ParseRule("")
	}
	p.rows = m.svc.Rows(core.ListOptions{Query: q, Grouping: core.Flat})
	// The local shell is offered first (when the query matches it).
	if lh := m.svc.LocalHost(); q.Match(&lh, nil) {
		p.rows = append([]core.Row{{Kind: core.HostRow, Key: "h:local", Host: &lh}}, p.rows...)
	}
	// ...unless the host of the current session matches: it comes first
	for i, r := range p.rows {
		if i > 0 && r.Host.ID == p.first {
			p.rows = append([]core.Row{r}, append(p.rows[:i:i], p.rows[i+1:]...)...)
			break
		}
	}
	p.cursor = max(0, min(p.cursor, len(p.rows)-1))
}

func (p *pickerModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		p.in, cmd = p.in.Update(msg)
		return cmd
	}
	switch k.String() {
	case "esc":
		m.modal = nil
		return nil
	case "enter":
		m.modal = nil
		if p.cursor < len(p.rows) {
			return p.choose(m, *p.rows[p.cursor].Host)
		}
		return nil
	case "up", "ctrl+p":
		p.cursor = max(0, p.cursor-1)
		return nil
	case "down", "ctrl+n":
		p.cursor = min(len(p.rows)-1, p.cursor+1)
		return nil
	}
	var cmd tea.Cmd
	p.in, cmd = p.in.Update(msg)
	p.refresh(m)
	return cmd
}

func (p *pickerModal) view(m *Model, w, h int) []string {
	n := max(3, min(15, h-8))
	start := max(0, min(p.cursor-n/2, len(p.rows)-n))
	var b strings.Builder
	b.WriteString(p.in.View() + "\n\n")
	for i := start; i < len(p.rows) && i < start+n; i++ {
		hst := p.rows[i].Host
		icon := padRight(m.opt.Icons.Get(string(hst.Scheme)), 2) // icon names equal scheme names; some icons are 2 wide
		line := fmt.Sprintf("%s %-28s %s", icon, ansi.Truncate(hst.Name, 28, m.g.ell), stDim.Render(hst.Target()))
		if hst.ID == p.first {
			line += stDim.Render("  (this session)")
		}
		if i == p.cursor {
			line = stSelected.Render(ansi.Strip(line))
		}
		b.WriteString(line + "\n")
	}
	if len(p.rows) == 0 {
		b.WriteString(stDim.Render("no hosts match") + "\n")
	}
	b.WriteString("\n" + stDim.Render("Enter: open · Esc: cancel"))
	return m.box("Open in new pane", b.String(), w)
}
