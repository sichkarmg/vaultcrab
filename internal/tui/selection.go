// SPDX-License-Identifier: MIT

package tui

import (
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// selDrag is a mouse selection in progress.
type selDrag struct {
	pane   core.PaneID
	anchor core.TermPos
	moved  bool
}

// kbSel is a line selection made with the keyboard in history mode.
type kbSel struct {
	anchor, cur int // absolute lines
}

// copyText puts text on the clipboards and remembers it for middle-click
// paste.
func (m *Model) copyText(text string) {
	if text == "" {
		return
	}
	m.clip = text
	if m.opt.CopyOSC52 {
		if seq := core.ClipboardOSC52(text); seq != "" {
			// Emitted with the frames of the next half second, so that a
			// coalesced redraw cannot drop it.
			m.osc, m.oscUntil = seq, time.Now().Add(500*time.Millisecond)
		}
	}
	if m.opt.CopySystem {
		go func() { _ = core.CopyToSystem(text) }() // best effort (no X/xclip over SSH)
	}
	m.notify(false, "copied %d characters", utf8.RuneCountInString(text))
}

// paste sends text to the session as a (bracketed) paste.
func paste(s *core.Session, text string) {
	if text == "" {
		return
	}
	s.Term.ScrollToBottom()
	s.Term.Paste(text)
}

// selectionMouse handles a press in the terminal area of a pane that is not
// forwarded to the remote application: left button selects (double click a
// word, triple click the line), middle button pastes the last copied text,
// right button pastes the system clipboard. x, y are relative to the
// terminal area.
func (t *tab) selectionMouse(m *Model, msg tea.MouseMsg, id core.PaneID, s *core.Session, x, y int) {
	switch msg.Button {
	case tea.MouseButtonMiddle:
		m.pasteInto(s, m.clip)
		return
	case tea.MouseButtonRight:
		text, err := core.PasteFromSystem()
		if err != nil || text == "" {
			text = m.clip
		}
		m.pasteInto(s, text)
		return
	case tea.MouseButtonLeft:
	default:
		return
	}
	p := s.Term.ViewPos(x, y)
	now := time.Now()
	if now.Sub(m.selClickAt) < 400*time.Millisecond && p == m.selClickPos {
		m.selClicks++
	} else {
		m.selClicks = 1
	}
	m.selClickAt, m.selClickPos = now, p
	switch m.selClicks {
	case 1:
		s.Term.ClearSelection()
		t.sel = &selDrag{pane: id, anchor: p}
	case 2:
		a, b := s.Term.WordAt(p)
		s.Term.SetSelection(a, b)
		m.copyText(s.Term.SelectedText())
	default:
		a, b := s.Term.LineBounds(p)
		s.Term.SetSelection(a, b)
		m.copyText(s.Term.SelectedText())
	}
}

// dragSelection extends the selection while the left button is held; x, y
// are relative to the tab body. Dragging past the top or bottom edge, or
// turning the wheel, scrolls the history and extends the selection.
func (t *tab) dragSelection(m *Model, msg tea.MouseMsg, x, y int) {
	d := t.sel
	s := t.ws.Session(d.pane)
	tr, ok := t.ws.TermRect(d.pane)
	if s == nil || !ok {
		t.sel = nil
		return
	}
	wheel := 0
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		wheel = 3
	case tea.MouseButtonWheelDown:
		wheel = -3
	}
	switch {
	case msg.Action == tea.MouseActionMotion || wheel != 0:
		cx := max(0, min(x-tr.X, tr.W-1))
		cy := y - tr.Y
		switch {
		case wheel != 0:
			// the wheel with the button held scrolls the history and extends
			// the selection to the pointer
			s.Term.Scroll(wheel)
			cy = max(0, min(cy, tr.H-1))
		case cy < 0:
			s.Term.Scroll(1)
			cy = 0
		case cy >= tr.H:
			s.Term.Scroll(-1)
			cy = tr.H - 1
		}
		p := s.Term.ViewPos(cx, cy)
		if !d.moved {
			d.moved = true
			s.Term.SetSelection(d.anchor, p)
		} else {
			s.Term.ExtendSelection(p)
		}
	case msg.Action == tea.MouseActionRelease:
		if d.moved {
			m.copyText(s.Term.SelectedText())
		}
		t.sel = nil
	}
}

// historySelectKey handles keys of the keyboard line selection in history
// mode: v starts it at the bottom visible line, Up/Down/PgUp/PgDn extend it,
// y or Enter copies, Esc cancels. It reports whether the key was used.
func (t *tab) historySelectKey(m *Model, id core.PaneID, s *core.Session, key string) bool {
	h := max(1, t.termHeight())
	ks := t.ksel[id]
	if ks == nil {
		if key != "v" {
			return false
		}
		l := s.Term.ViewPos(0, h-1).Line
		ks = &kbSel{anchor: l, cur: l}
		t.ksel[id] = ks
		t.applyKbSel(s, ks)
		return true
	}
	move := 0
	switch key {
	case "up", "k":
		move = -1
	case "down", "j":
		move = 1
	case "pgup":
		move = -(h - 1)
	case "pgdown", " ":
		move = h - 1
	case "y", "enter":
		m.copyText(s.Term.SelectedText())
		fallthrough
	case "esc", "v", "q":
		delete(t.ksel, id)
		s.Term.ClearSelection()
		return true
	default:
		return true
	}
	top := s.Term.ViewPos(0, 0).Line
	last := s.Term.ViewPos(0, h-1).Line + s.Term.ScrollOffset() // last line of the live screen
	ks.cur = max(0, min(ks.cur+move, last))
	if ks.cur < top {
		s.Term.Scroll(top - ks.cur)
	} else if bottom := top + h - 1; ks.cur > bottom {
		s.Term.Scroll(-(ks.cur - bottom))
	}
	t.applyKbSel(s, ks)
	return true
}

func (t *tab) applyKbSel(s *core.Session, ks *kbSel) {
	lo, hi := min(ks.anchor, ks.cur), max(ks.anchor, ks.cur)
	w, _ := s.Term.Size()
	s.Term.SetSelection(core.TermPos{Line: lo}, core.TermPos{Line: hi, Col: w - 1})
}
