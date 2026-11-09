// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// logLines is how much of the event log the viewer shows.
const logLines = 2000

// logModal shows the end of the event log (prefix g, the ≡ button). It is
// read when opened, shown again and on r: no timer runs while it is open.
// It stays with the host list: the other tabs can be used meanwhile.
type logModal struct {
	lines []string
	err   error
	top   int // first shown line
	shown int // lines shown in the last view
	end   bool
}

func newLogView(m *Model) *logModal {
	l := &logModal{}
	l.load(m)
	return l
}

// openLog opens the event log over the host list (prefix g, the ≡
// button); it stays there while other tabs are used, and one left waiting
// is shown again (read anew).
func (m *Model) openLog() {
	if m.parked != nil {
		m.switchTab(0)
		return
	}
	if m.active != 0 && m.modal == nil {
		m.setActive(0)
	}
	if l, ok := m.modal.(*logModal); ok {
		l.reshow(m)
		return
	}
	if m.modal == nil {
		m.modal = newLogView(m)
	}
}

// reshow reads the log again when the view follows its end.
func (l *logModal) reshow(m *Model) {
	if l.end {
		l.load(m)
	}
}

func (l *logModal) load(m *Model) {
	l.lines, l.err = m.svc.LogTail(logLines)
	l.end = true
}

func (l *logModal) scroll(d int) {
	l.end = false
	l.top = max(0, min(l.top+d, len(l.lines)-max(1, l.shown)))
}

func (l *logModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	page := max(1, l.shown-1)
	switch k.String() {
	case "up", "k":
		l.scroll(-1)
	case "down", "j":
		l.scroll(1)
	case "pgup":
		l.scroll(-page)
	case "pgdown", " ":
		l.scroll(page)
	case "home", "g":
		l.top, l.end = 0, false
	case "end", "G":
		l.end = true
	case "r", "к":
		l.load(m)
	case "esc", "q", "enter", "й":
		m.modal = nil
	}
	return nil
}

func (l *logModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		l.scroll(-3)
	case tea.MouseButtonWheelDown:
		l.scroll(3)
	}
	return nil
}

func (l *logModal) view(m *Model, w, h int) []string {
	textW := max(20, w-10)
	l.shown = max(3, h-12)
	if l.end {
		l.top = max(0, len(l.lines)-l.shown)
	}
	l.top = max(0, min(l.top, len(l.lines)-1))
	var b strings.Builder
	file := m.svc.LogFile()
	switch {
	case !m.svc.LogEnabled():
		b.WriteString(stWarn.Render("The event log is off: Settings → Behavior → Event log.") + "\n")
	default:
		b.WriteString(stDim.Render(file) + "\n")
	}
	b.WriteString("\n")
	switch {
	case l.err != nil && len(l.lines) == 0:
		b.WriteString(stDim.Render("no log yet ("+errText(l.err)+")") + "\n")
	case len(l.lines) == 0:
		b.WriteString(stDim.Render("the log is empty") + "\n")
	}
	for i := l.top; i < l.top+l.shown && i < len(l.lines); i++ {
		line := ansi.Truncate(l.lines[i], textW, m.g.ell)
		if strings.Contains(line, ": ") && (strings.Contains(line, "failed") || strings.Contains(line, "error") ||
			strings.Contains(line, "disconnected:") || strings.Contains(line, "does not load")) {
			line = stErr.Render(line)
		}
		b.WriteString(line + "\n")
	}
	pos := ""
	if n := len(l.lines); n > 0 {
		pos = fmt.Sprintf("lines %d-%d of %d · ", l.top+1, min(l.top+l.shown, n), n)
	}
	b.WriteString("\n" + stDim.Render(m.ascii(pos+"↑/↓ PgUp/PgDn Home/End: scroll · r: reload · Esc: close")))
	return m.box("Event log", b.String(), w)
}
