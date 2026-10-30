// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// mouseModal is a dialog that handles mouse presses; x, y are relative to
// the top-left corner of the dialog box.
type mouseModal interface {
	modal
	mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd
}

// dialog content starts inside the border and the padding of box(): a
// title and an empty line come first when the box has a title.
const (
	boxPadX     = 2
	boxPadY     = 1
	boxTitleGap = 2
)

// buttonRow renders buttons in one line, the focused one highlighted, and
// returns the x range of each (relative to the line).
func buttonRow(labels []string, focus int) (string, [][2]int) {
	var b strings.Builder
	spans := make([][2]int, len(labels))
	x := 0
	for i, l := range labels {
		if i > 0 {
			b.WriteString("  ")
			x += 2
		}
		s := "[ " + l + " ]"
		if i == focus {
			b.WriteString(stSelected.Render(s))
		} else {
			b.WriteString(s)
		}
		w := ansi.StringWidth(s)
		spans[i] = [2]int{x, x + w}
		x += w
	}
	return b.String(), spans
}

func spanAt(spans [][2]int, x int) int {
	for i, s := range spans {
		if x >= s[0] && x < s[1] {
			return i
		}
	}
	return -1
}

// --- quit ---

// quitModal asks whether to keep the open tabs for the next start (the
// default) or to quit without saving them.
type quitModal struct {
	live  int // active sessions that will be closed
	focus int // quitSave, quitDiscard or quitCancel
	spans [][2]int
	row   int // line of the buttons in the dialog
}

const (
	quitSave = iota
	quitDiscard
	quitCancel
)

var quitLabels = []string{"Save tabs and quit", "Quit without saving", "Cancel"}

func (q *quitModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch strings.ToLower(k.String()) {
	case "left", "shift+tab", "h":
		q.focus = (q.focus + len(quitLabels) - 1) % len(quitLabels)
	case "right", "tab", "l":
		q.focus = (q.focus + 1) % len(quitLabels)
	case "enter", " ":
		return q.choose(m, q.focus)
	case "s", "y", "д":
		return q.choose(m, quitSave)
	case "d", "в":
		return q.choose(m, quitDiscard)
	case "esc", "n", "q", "c", "н":
		return q.choose(m, quitCancel)
	}
	return nil
}

func (q *quitModal) choose(m *Model, i int) tea.Cmd {
	m.modal = nil
	switch i {
	case quitSave:
		return m.quit()
	case quitDiscard:
		m.discardTabs = true
		return m.quit()
	}
	return nil
}

func (q *quitModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || y != q.row {
		return nil
	}
	if i := spanAt(q.spans, x-boxPadX); i >= 0 {
		return q.choose(m, i)
	}
	return nil
}

func (q *quitModal) view(m *Model, w, h int) []string {
	lines := []string{fmt.Sprintf("Quit Vaultcrab? %d open tab(s) can be reopened on the next start", len(m.tabs))}
	lines = append(lines, stDim.Render("(not connected; Enter in a tab connects)."))
	if q.live > 0 {
		lines = append(lines, "", stWarn.Render(fmt.Sprintf("%d active session(s) will be closed.", q.live)))
	}
	var btn string
	btn, q.spans = buttonRow(quitLabels, q.focus)
	lines = append(lines, "", btn, "",
		stDim.Render("Enter/s: save · d: don't save · Esc: cancel · ←/→: choose"))
	q.row = boxPadY + boxTitleGap + len(lines) - 3
	return m.box("Quit", strings.Join(lines, "\n"), w)
}
