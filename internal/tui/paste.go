// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// pasteInto pastes text into a session; text of several lines is shown
// first and pasted only after confirmation (terminal.confirm_paste), since
// every line may run as a command.
func (m *Model) pasteInto(s *core.Session, text string) {
	if text == "" {
		return
	}
	lines := plainLines(text)
	if !m.opt.ConfirmPaste || len(lines) < 2 {
		paste(s, text)
		return
	}
	m.modal = &pasteModal{s: s, text: text, lines: lines, focus: pasteCancel}
}

// plainLines splits text into lines for display (pasted text, notes):
// escape sequences and
// control characters are removed, tabs become spaces; a final line break
// does not count as a line.
func plainLines(text string) []string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(text)
	text = strings.TrimSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		l = strings.ReplaceAll(ansi.Strip(l), "\t", "    ")
		lines[i] = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, l)
	}
	return lines
}

// pasteModal shows the text to paste with scrolling and asks to confirm.
// Cancel is the default: Enter alone pastes nothing.
type pasteModal struct {
	s     *core.Session
	text  string
	lines []string
	top   int // first shown line
	shown int // lines shown in the last view
	focus int // pasteDo or pasteCancel

	spans [][2]int
	row   int
}

const (
	pasteDo = iota
	pasteCancel
)

var pasteLabels = []string{"Paste", "Cancel"}

func (p *pasteModal) scroll(d int) {
	p.top = max(0, min(p.top+d, len(p.lines)-max(1, p.shown)))
}

func (p *pasteModal) choose(m *Model, i int) tea.Cmd {
	m.modal = nil
	if i != pasteDo {
		return nil
	}
	if st, _ := p.s.State(); st != core.SessionConnected || p.s.Exited() {
		m.notify(true, "not pasted: the session is not connected")
		return nil
	}
	paste(p.s, p.text)
	m.notify(false, "pasted %d lines", len(p.lines))
	return nil
}

func (p *pasteModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	page := max(1, p.shown-1)
	switch strings.ToLower(k.String()) {
	case "up", "k":
		p.scroll(-1)
	case "down", "j":
		p.scroll(1)
	case "pgup":
		p.scroll(-page)
	case "pgdown":
		p.scroll(page)
	case "home":
		p.top = 0
	case "end":
		p.scroll(len(p.lines))
	case "left", "right", "tab", "shift+tab", "h", "l":
		p.focus = 1 - p.focus
	case "enter", " ":
		return p.choose(m, p.focus)
	case "y", "д":
		return p.choose(m, pasteDo)
	case "n", "esc", "q", "н":
		return p.choose(m, pasteCancel)
	}
	return nil
}

func (p *pasteModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		p.scroll(-3)
	case msg.Button == tea.MouseButtonWheelDown:
		p.scroll(3)
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && y == p.row:
		if i := spanAt(p.spans, x-boxPadX); i >= 0 {
			return p.choose(m, i)
		}
	}
	return nil
}

func (p *pasteModal) view(m *Model, w, h int) []string {
	n := len(p.lines)
	// the text: as many lines as fit, numbered, long lines cut
	p.shown = max(3, min(n, h-14))
	p.scroll(0)
	numW := len(fmt.Sprint(n))
	textW := max(20, min(100, w-12-numW))
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Paste %s into %s?\n", stBold.Render(fmt.Sprintf("%d lines", n)), stBold.Render(p.s.Host.Name)))
	b.WriteString(stDim.Render("Each line may run as a command.") + "\n\n")
	sep := stDim.Render(m.g.v)
	for i := p.top; i < p.top+p.shown && i < n; i++ {
		b.WriteString(stDim.Render(fmt.Sprintf("%*d", numW, i+1)) + " " + sep + " " + ansi.Truncate(p.lines[i], textW, m.g.ell) + "\n")
	}
	pos := fmt.Sprintf("lines %d-%d of %d", p.top+1, min(p.top+p.shown, n), n)
	if n > p.shown {
		pos += " · ↑/↓ PgUp/PgDn or wheel: scroll"
	}
	b.WriteString(stDim.Render(pos) + "\n\n")
	var btn string
	btn, p.spans = buttonRow(pasteLabels, p.focus)
	b.WriteString(btn + "\n\n")
	b.WriteString(stDim.Render("y: paste · n/Esc: cancel · ←/→ Enter: choose"))
	// title, gap, 3 header lines, the text, position, blank: then the buttons
	p.row = boxPadY + boxTitleGap + 3 + min(p.shown, n) + 2
	return m.box("Paste", b.String(), w)
}
