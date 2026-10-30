// SPDX-License-Identifier: MIT

package term

import (
	"strings"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
)

// Pos is a position in the terminal history: Line is absolute (it does not
// change when new output scrolls the screen), Col is a cell column.
type Pos struct {
	Line, Col int
}

func (p Pos) before(q Pos) bool { return p.Line < q.Line || p.Line == q.Line && p.Col < q.Col }

// selection is a stream (character-wise) selection between two positions.
type selection struct {
	a, b   Pos
	active bool
}

func (s selection) ordered() (Pos, Pos) {
	if s.b.before(s.a) {
		return s.b, s.a
	}
	return s.a, s.b
}

func (s selection) contains(line, col int) bool {
	if !s.active {
		return false
	}
	a, b := s.ordered()
	if line < a.Line || line > b.Line {
		return false
	}
	if line == a.Line && col < a.Col {
		return false
	}
	if line == b.Line && col > b.Col {
		return false
	}
	return true
}

// ViewPos converts a position in the rendered view (row y, column x) to a
// history position.
func (t *Terminal) ViewPos(x, y int) Pos {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Pos{Line: t.viewLine(y), Col: max(x, 0)}
}

// viewLine maps view row y to an absolute line; called with mu held.
// Scrollback line i is Total-Len+i, screen row r is Total+r.
func (t *Terminal) viewLine(y int) int {
	off := t.scrollOff
	if t.emu.IsAltScreen() {
		off = 0
	}
	return t.emu.Scrollback().Total() + y - off
}

// lineCells returns the cells of an absolute line (nil if it is gone);
// called with mu held.
func (t *Terminal) lineCells(line int) func(x int) *uv.Cell {
	sb := t.emu.Scrollback()
	total := sb.Total()
	if line < total {
		i := line - (total - sb.Len())
		l := sb.Line(i) // nil when dropped from the scrollback
		return func(x int) *uv.Cell {
			if x >= 0 && x < len(l) {
				return &l[x]
			}
			return nil
		}
	}
	r := line - total
	if r >= t.h {
		return nil
	}
	return func(x int) *uv.Cell {
		if x >= 0 && x < t.w {
			return t.emu.CellAt(x, r)
		}
		return nil
	}
}

// SetSelection selects from a to b (inclusive, in either order).
func (t *Terminal) SetSelection(a, b Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sel = selection{a: a, b: b, active: true}
	t.dirty.Store(true)
}

// ExtendSelection moves the end of the selection.
func (t *Terminal) ExtendSelection(b Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sel.active {
		t.sel.b = b
		t.dirty.Store(true)
	}
}

// ClearSelection removes the selection.
func (t *Terminal) ClearSelection() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sel.active {
		t.sel = selection{}
		t.dirty.Store(true)
	}
}

// HasSelection reports whether there is a selection.
func (t *Terminal) HasSelection() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sel.active
}

// SelectedText returns the selected text: trailing blanks of every line are
// removed and lines are joined with "\n".
func (t *Terminal) SelectedText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.sel.active {
		return ""
	}
	a, b := t.sel.ordered()
	var out []string
	for line := a.Line; line <= b.Line; line++ {
		cell := t.lineCells(line)
		if cell == nil {
			continue
		}
		from, to := 0, t.w-1
		if line == a.Line {
			from = a.Col
		}
		if line == b.Line {
			to = b.Col
		}
		var sb strings.Builder
		for x := from; x <= to; x++ {
			c := cell(x)
			switch {
			case c == nil:
				sb.WriteByte(' ')
			case c.Content == "" && c.Width == 0:
				// continuation of a wide character
			case c.Content == "":
				sb.WriteByte(' ')
			default:
				sb.WriteString(c.Content)
			}
		}
		out = append(out, strings.TrimRight(sb.String(), " "))
	}
	return strings.Join(out, "\n")
}

// WordAt returns the bounds of the word under p (non-blank characters
// except quotes and brackets), for double-click selection.
func (t *Terminal) WordAt(p Pos) (Pos, Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cell := t.lineCells(p.Line)
	if cell == nil {
		return p, p
	}
	isWord := func(x int) bool {
		c := cell(x)
		if c == nil || c.Content == "" {
			return false
		}
		r := []rune(c.Content)[0]
		return !unicode.IsSpace(r) && !strings.ContainsRune("\"'`()[]{}<>|,;", r)
	}
	if !isWord(p.Col) {
		return p, p
	}
	a, b := p.Col, p.Col
	for a > 0 && isWord(a-1) {
		a--
	}
	for b < t.w-1 && isWord(b+1) {
		b++
	}
	return Pos{p.Line, a}, Pos{p.Line, b}
}

// LineBounds returns the whole line of p (for triple-click selection).
func (t *Terminal) LineBounds(p Pos) (Pos, Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Pos{p.Line, 0}, Pos{p.Line, t.w - 1}
}
