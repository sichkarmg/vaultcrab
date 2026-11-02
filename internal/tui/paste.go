// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
// Cancel is the default: Enter alone pastes nothing. Edit opens the text in
// an editor first.
type pasteModal struct {
	s     *core.Session
	text  string
	lines []string
	top   int // first shown line
	shown int // lines shown in the last view
	focus int // pasteDo, pasteEdit or pasteCancel

	editing bool
	ed      textarea.Model
	edited  bool // the text was changed in the editor

	spans [][2]int
	row   int
}

const (
	pasteDo = iota
	pasteEdit
	pasteCancel
)

var pasteLabels = []string{"Paste", "Edit", "Cancel"}

// startEdit opens the text in the editor (escape sequences and control
// characters removed, as shown).
func (p *pasteModal) startEdit() {
	if !p.editing {
		p.ed = textarea.New()
		p.ed.ShowLineNumbers = true
		p.ed.Prompt = ""
		p.ed.CharLimit = 0
		p.ed.MaxHeight = 0
		p.ed.FocusedStyle.CursorLine = lipgloss.NewStyle()
		p.ed.Cursor.SetMode(cursor.CursorStatic) // no blink timer while idle
		p.ed.SetValue(strings.Join(p.lines, "\n"))
		p.ed.Focus()
	}
	p.editing = true
}

// stopEdit takes the edited text back into the preview. A final line break
// of the pasted text is kept: it decides whether the last line runs.
func (p *pasteModal) stopEdit() {
	p.editing = false
	text := p.ed.Value()
	if text == strings.Join(p.lines, "\n") {
		return
	}
	if strings.HasSuffix(p.text, "\n") || strings.HasSuffix(p.text, "\r") {
		text += "\n"
	}
	p.text, p.lines, p.edited = text, plainLines(text), true
	p.top = 0
}

func (p *pasteModal) scroll(d int) {
	p.top = max(0, min(p.top+d, len(p.lines)-max(1, p.shown)))
}

func (p *pasteModal) choose(m *Model, i int) tea.Cmd {
	if i == pasteEdit {
		p.startEdit()
		return nil
	}
	m.modal = nil
	if i != pasteDo {
		return nil
	}
	if st, _ := p.s.State(); st != core.SessionConnected || p.s.Exited() {
		m.notify(true, "not pasted: the session is not connected")
		return nil
	}
	paste(p.s, p.text)
	if p.edited {
		m.notify(false, "pasted %d edited lines", len(p.lines))
	} else {
		m.notify(false, "pasted %d lines", len(p.lines))
	}
	return nil
}

func (p *pasteModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if p.editing {
		switch k.String() {
		case "ctrl+s": // paste the edited text
			p.stopEdit()
			return p.choose(m, pasteDo)
		case "esc": // back to the preview, the changes are kept
			p.stopEdit()
			p.focus = pasteDo
			return nil
		}
		var cmd tea.Cmd
		p.ed, cmd = p.ed.Update(msg)
		return cmd
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
	case "right", "tab", "l":
		p.focus = (p.focus + 1) % len(pasteLabels)
	case "left", "shift+tab", "h":
		p.focus = (p.focus + len(pasteLabels) - 1) % len(pasteLabels)
	case "e", "у":
		return p.choose(m, pasteEdit)
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
	if p.editing {
		return nil
	}
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
	if p.editing {
		return p.editView(m, w, h)
	}
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
	b.WriteString(stDim.Render("y: paste · e: edit · n/Esc: cancel · ←/→ Enter: choose"))
	// title, gap, 3 header lines, the text, position, blank: then the buttons
	p.row = boxPadY + boxTitleGap + 3 + min(p.shown, n) + 2
	return m.box("Paste", b.String(), w)
}

// editView shows the editor: the text with line numbers, all keys edit it.
func (p *pasteModal) editView(m *Model, w, h int) []string {
	p.ed.SetWidth(max(20, min(110, w-10)))
	p.ed.SetHeight(max(3, min(p.ed.LineCount()+1, h-12)))
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Edit the text to paste into %s\n\n", stBold.Render(p.s.Host.Name)))
	b.WriteString(p.ed.View() + "\n\n")
	b.WriteString(stDim.Render(fmt.Sprintf("%d lines · Ctrl+S: paste · Esc: back to the preview", p.ed.LineCount())))
	return m.box("Paste", b.String(), w)
}
