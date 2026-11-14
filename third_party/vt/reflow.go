package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// vaultcrab patch: reflow of the main screen on resize.
//
// Lines that reached the right margin and wrapped are marked (Screen.wrap,
// packedLine.wrapped). On a resize the main screen and its scrollback are
// rebuilt from these logical lines at the new width, so that nothing is cut
// off when the window gets narrower and the text joins again when it gets
// wider. The cursor (and the saved cursor) keep their place in the text.
// When the height shrinks the top lines go to the scrollback; when the
// screen was full and gets more room, lines come back from the scrollback.
// The alternate screen is not reflowed: full-screen programs redraw.

// glyphs splits cells into units that stay together: a cell and the
// continuation cells of a wide character.
func glyphEnd(cells []uv.Cell, i int) int {
	j := i + 1
	for j < len(cells) && cells[j].Width == 0 && cells[j].Content == "" {
		j++
	}
	return j
}

// trimBlank drops the trailing blank cells.
func trimBlank(cells []uv.Cell) []uv.Cell {
	n := len(cells)
	for n > 0 && blank(&cells[n-1]) {
		n--
	}
	return cells[:n]
}

// splitCells cuts a logical line into rows of width w (a wide character
// that does not fit moves to the next row whole). at maps the cell
// indexes in want to (row, col); an index past the end of the cells is at
// the column after the last cell.
func splitCells(cells []uv.Cell, w int, want []int) (rows []uv.Line, at [][2]int) {
	at = make([][2]int, len(want))
	row := make(uv.Line, 0, w)
	col := 0
	flush := func() {
		for len(row) < w {
			row = append(row, uv.EmptyCell)
		}
		rows = append(rows, row)
		row = make(uv.Line, 0, w)
		col = 0
	}
	for i := 0; i < len(cells); {
		j := glyphEnd(cells, i)
		gw := max(cells[i].Width, 1)
		if gw > w { // wider than the whole row: a blank
			gw = 1
		}
		if col+gw > w {
			flush()
		}
		for k, idx := range want {
			if idx >= i && idx < j {
				at[k] = [2]int{len(rows), col}
			}
		}
		if cells[i].Width > w {
			row = append(row, uv.EmptyCell)
		} else {
			row = append(row, cells[i:j]...)
			for len(row) < col+gw { // a wide cell without its continuation
				row = append(row, uv.Cell{})
			}
		}
		col += gw
		i = j
	}
	// positions past the last cell
	for k, idx := range want {
		if idx < len(cells) {
			continue
		}
		c := col + (idx - len(cells))
		r := len(rows) + c/w
		at[k] = [2]int{r, c % w}
	}
	if len(row) > 0 || len(rows) == 0 {
		flush()
	}
	return rows, at
}

// rewrap rebuilds the scrollback at width w: a chain of wrapped lines is
// joined and cut again; other lines that fit stay as they are.
func (s *Scrollback) rewrap(w int) {
	if s == nil || s.n == 0 || w <= 0 {
		return
	}
	old := make([]packedLine, s.n)
	for i := range old {
		old[i] = s.ring[(s.head+i)%len(s.ring)]
	}
	out := make([]packedLine, 0, len(old))
	for i := 0; i < len(old); {
		j := i
		for j < len(old)-1 && old[j].wrapped {
			j++
		}
		if i == j && !old[i].wrapped && old[i].ncell <= w {
			out = append(out, old[i])
			i++
			continue
		}
		var cells []uv.Cell
		for k := i; k <= j; k++ {
			cells = append(cells, old[k].unpack()...)
		}
		last := old[j].wrapped // the chain goes on (into the screen)
		if !last {
			cells = trimBlank(cells)
		}
		rows, _ := splitCells(cells, w, nil)
		for r, row := range rows {
			wr := r < len(rows)-1 || last
			if !wr {
				row = trimBlank(row)
			}
			out = append(out, pack(row, wr))
		}
		i = j + 1
	}
	if len(out) > s.maxLines {
		out = out[len(out)-s.maxLines:]
	}
	s.ring, s.head, s.n = out, 0, len(out) // the next Push grows the ring
}

// popTail removes the last n lines and returns them, oldest first.
func (s *Scrollback) popTail(n int) []packedLine {
	if s == nil {
		return nil
	}
	n = min(n, s.n)
	out := make([]packedLine, n)
	for i := range out {
		out[i] = s.ring[(s.head+s.n-n+i)%len(s.ring)]
	}
	s.n -= n
	return out
}

// tailChain is the number of lines at the end of the scrollback that
// continue into the screen (the last one is wrapped).
func (s *Scrollback) tailChain() int {
	if s == nil || s.n == 0 || !s.wrappedAt(s.n-1) {
		return 0
	}
	k := 1
	for k < s.n && s.wrappedAt(s.n-1-k) {
		k++
	}
	return k
}

// popRows removes the last n rows at width w from the end (lines still at
// another width are rewrapped whole chains at a time; what is not needed
// goes back) and returns them, oldest first, with their wrap flags.
func (s *Scrollback) popRows(n, w int) (rows []uv.Line, wraps []bool) {
	for len(rows) < n && s != nil && s.n > 0 {
		// the last logical line: the last line and the wrapped ones before
		k := 1
		for k < s.n && s.wrappedAt(s.n-1-k) {
			k++
		}
		var cells []uv.Cell
		for _, p := range s.popTail(k) {
			cells = append(cells, p.unpack()...)
		}
		lrows, _ := splitCells(trimBlank(cells), w, nil)
		lwraps := make([]bool, len(lrows))
		for i := range lwraps {
			lwraps[i] = i < len(lrows)-1
		}
		if need := n - len(rows); len(lrows) > need {
			for i := 0; i < len(lrows)-need; i++ { // back, at the new width
				r := lrows[i]
				if !lwraps[i] {
					r = trimBlank(r)
				}
				s.pushPacked(pack(r, lwraps[i]))
			}
			lrows, lwraps = lrows[len(lrows)-need:], lwraps[len(lwraps)-need:]
		}
		rows = append(lrows, rows...)
		wraps = append(lwraps, wraps...)
	}
	return rows, wraps
}

// reflow resizes the screen to w×h, rewrapping its lines and the
// scrollback. phantom: the cursor waits at the right margin for the next
// character (its column is one past the cell it is on); it returns the
// same state for the new cursor.
func (s *Screen) reflow(w, h int, phantom bool) bool {
	w, h = max(w, 1), max(h, 1)
	oldW, oldH := s.buf.Width(), s.buf.Height()
	if oldW == w && oldH == h {
		return phantom
	}
	cur, saved := s.cur.Position, s.saved.Position

	// the rows in use
	used := cur.Y + 1
	for y := oldH - 1; y >= used; y-- {
		if !s.isLineEmpty(s.buf.Line(y)) || s.Wrapped(y) {
			used = y + 1
			break
		}
	}
	used = min(used, oldH)
	full := used == oldH

	sb := s.scrollback
	var prefix []uv.Cell // the start of the first screen line, in the scrollback
	for _, p := range sb.popTail(sb.tailChain()) {
		prefix = append(prefix, p.unpack()...)
	}
	if w != oldW && sb != nil {
		sb.want = w // rewrapped when it is read (rewrap)
	}

	// the logical lines of the screen, and where the cursors are in them
	type logical struct {
		cells []uv.Cell
		want  []int // cell indexes: cursor, saved cursor (-1 = not here)
	}
	var lines []logical
	open := false
	curLine, savedLine := -1, -1
	for y := 0; y < used; y++ {
		row := s.buf.Line(y)
		if !open {
			lines = append(lines, logical{want: []int{-1, -1}})
			if y == 0 && len(prefix) > 0 {
				lines[0].cells = append(lines[0].cells, prefix...)
			}
		}
		l := &lines[len(lines)-1]
		base := len(l.cells)
		if y == cur.Y {
			x := cur.X
			if phantom {
				x++
			}
			l.want[0], curLine = base+x, len(lines)-1
		}
		if y == saved.Y && saved.Y < oldH {
			l.want[1], savedLine = base+saved.X, len(lines)-1
		}
		l.cells = append(l.cells, row...)
		open = s.Wrapped(y)
		if !open {
			l.cells = trimBlank(l.cells)
		}
	}
	if len(lines) > 0 && open {
		l := &lines[len(lines)-1]
		l.cells = trimBlank(l.cells)
	}

	// cut them at the new width
	var rows []uv.Line
	var wraps []bool
	curRow, curCol, savedRow, savedCol := 0, 0, -1, 0
	newPhantom := false
	for li, l := range lines {
		want := []int{max(l.want[0], 0), max(l.want[1], 0)}
		lrows, at := splitCells(l.cells, w, want)
		if li == curLine {
			r, c := at[0][0], at[0][1]
			// the cursor right after a row that is full: at its last
			// column, waiting to wrap (as before the resize)
			if c == 0 && r > 0 && r >= len(lrows) && want[0] == len(l.cells) {
				r, c, newPhantom = r-1, w-1, true
			}
			for r >= len(lrows) {
				lrows = append(lrows, blankLine(w))
			}
			curRow, curCol = len(rows)+r, c
		}
		if li == savedLine {
			r, c := at[1][0], at[1][1]
			savedRow, savedCol = len(rows)+min(r, len(lrows)-1), c
		}
		for r := range lrows {
			rows = append(rows, lrows[r])
			wraps = append(wraps, r < len(lrows)-1)
		}
	}
	if curLine < 0 { // the cursor below the last line
		for len(rows) <= cur.Y {
			rows = append(rows, blankLine(w))
			wraps = append(wraps, false)
		}
		curRow, curCol = len(rows)-1, min(cur.X, w-1)
	}

	// a full screen with more room takes lines back from the scrollback
	if full && len(rows) < h && sb != nil && sb.n > 0 {
		brows, bwraps := sb.popRows(h-len(rows), w)
		rows = append(brows, rows...)
		wraps = append(bwraps, wraps...)
		curRow += len(brows)
		if savedRow >= 0 {
			savedRow += len(brows)
		}
	}

	// the last h rows that keep the cursor on the screen
	start := max(0, len(rows)-h)
	if curRow < start {
		start = curRow
	}
	for i := 0; i < start; i++ {
		sb.PushWrapped(rows[i], wraps[i])
	}

	s.buf.Resize(w, h)
	s.buf.Touched = nil
	s.wrap = make([]bool, h)
	for y := 0; y < h; y++ {
		i := start + y
		if i < len(rows) {
			line := rows[i]
			if len(line) > w {
				line = line[:w]
			}
			for len(line) < w {
				line = append(line, uv.EmptyCell)
			}
			s.buf.Lines[y] = line
			s.wrap[y] = wraps[i]
		} else {
			s.buf.Lines[y] = blankLine(w)
		}
	}
	s.scroll = s.buf.Bounds()
	s.cur.X, s.cur.Y = min(curCol, w-1), clampInt(curRow-start, 0, h-1)
	if savedRow >= 0 {
		s.saved.X, s.saved.Y = min(savedCol, w-1), clampInt(savedRow-start, 0, h-1)
	} else {
		s.saved.X, s.saved.Y = min(saved.X, w-1), clampInt(saved.Y, 0, h-1)
	}
	return newPhantom
}

func blankLine(w int) uv.Line {
	l := make(uv.Line, w)
	for i := range l {
		l[i] = uv.EmptyCell
	}
	return l
}

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }
