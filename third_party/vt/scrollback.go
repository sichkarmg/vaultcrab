package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// DefaultScrollbackSize is the default maximum number of lines in the scrollback buffer.
const DefaultScrollbackSize = 10000

// Scrollback represents a scrollback buffer that stores lines scrolled off the screen.
//
// vaultcrab patch: lines are stored packed (text + 2 bytes per cell + style runs)
// in a ring buffer instead of as []uv.Cell (≈112 bytes per cell, pointer-rich)
// with an O(n) delete of the oldest line on every push. Line/CellAt unpack on
// demand; the UI only unpacks the few lines it displays.
type Scrollback struct {
	ring     []packedLine
	head     int // index of the oldest line in ring
	n        int // number of lines
	maxLines int
	total    int // lines ever pushed (monotonic; for anchoring scrolled views)
}

type packedLine struct {
	text  string   // cell contents concatenated
	cells []uint16 // per cell: content byte length<<2 | width; nil when every cell is 1 byte, width 1
	ncell int
	runs  []styleRun
}

type styleRun struct {
	start int // first cell index
	style uv.Style
	link  uv.Link
}

// NewScrollback creates a new scrollback buffer with the given maximum number of lines.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{maxLines: maxLines}
}

func pack(line uv.Line) packedLine {
	// Trim trailing empty cells. This helps with wrapping and window resizing.
	last := -1
	for i := len(line) - 1; i >= 0; i-- {
		if !blank(&line[i]) {
			last = i
			break
		}
	}
	line = line[:last+1]
	p := packedLine{ncell: len(line)}
	if len(line) == 0 {
		return p
	}
	size := 0
	simple := true
	for i := range line {
		c := &line[i]
		size += len(c.Content)
		if len(c.Content) != 1 || c.Width != 1 {
			simple = false
		}
	}
	buf := make([]byte, 0, size)
	if !simple {
		p.cells = make([]uint16, len(line))
	}
	for i := range line {
		c := &line[i]
		buf = append(buf, c.Content...)
		if !simple {
			l := min(len(c.Content), 1<<14-1)
			p.cells[i] = uint16(l<<2 | (c.Width & 3))
		}
		if i == 0 || !sameAttrs(c, &line[i-1]) {
			p.runs = append(p.runs, styleRun{start: i, style: c.Style, link: c.Link})
		}
	}
	p.text = string(buf)
	return p
}

// blank reports c.IsZero() || c.Equal(&uv.EmptyCell) without the generic
// (and comparatively slow) cell comparison.
func blank(c *uv.Cell) bool {
	if c.Link.URL != "" || c.Link.Params != "" || !c.Style.IsZero() {
		return false
	}
	return c.Content == " " && c.Width == 1 || c.Content == "" && c.Width == 0
}

func sameAttrs(a, b *uv.Cell) bool {
	az, bz := a.Style.IsZero(), b.Style.IsZero()
	if az != bz {
		return false
	}
	return (az || a.Style.Equal(&b.Style)) && a.Link == b.Link
}

func (p *packedLine) unpack() uv.Line {
	line := make(uv.Line, p.ncell)
	off, run := 0, 0
	for i := 0; i < p.ncell; i++ {
		for run+1 < len(p.runs) && p.runs[run+1].start <= i {
			run++
		}
		l, w := 1, 1
		if p.cells != nil {
			l, w = int(p.cells[i]>>2), int(p.cells[i]&3)
		}
		end := min(off+l, len(p.text))
		line[i] = uv.Cell{Content: p.text[off:end], Width: w}
		if run < len(p.runs) {
			line[i].Style = p.runs[run].style
			line[i].Link = p.runs[run].link
		}
		off = end
	}
	return line
}

// Push adds a line to the scrollback buffer.
// If the buffer is full, the oldest line is removed.
func (s *Scrollback) Push(line uv.Line) {
	if s == nil || s.maxLines <= 0 {
		return
	}
	p := pack(line)
	s.total++
	if s.n < s.maxLines {
		if s.n == len(s.ring) {
			s.grow()
		}
		s.ring[(s.head+s.n)%len(s.ring)] = p
		s.n++
		return
	}
	// Full: overwrite the oldest line.
	s.ring[s.head] = p
	s.head = (s.head + 1) % len(s.ring)
}

func (s *Scrollback) grow() {
	newCap := min(max(2*len(s.ring), 256), s.maxLines)
	r := make([]packedLine, newCap)
	for i := 0; i < s.n; i++ {
		r[i] = s.ring[(s.head+i)%len(s.ring)]
	}
	s.ring, s.head = r, 0
}

// PushN adds n lines from the buffer starting at line y to the scrollback.
func (s *Scrollback) PushN(buf *uv.RenderBuffer, y, n int) {
	if s == nil || buf == nil || n <= 0 {
		return
	}

	for i := range min(n, buf.Height()-y) {
		if line := buf.Line(y + i); line != nil {
			s.Push(line)
		}
	}
}

// Total returns the number of lines ever pushed (not reset by Clear).
func (s *Scrollback) Total() int {
	if s == nil {
		return 0
	}
	return s.total
}

// Len returns the number of lines in the scrollback buffer.
func (s *Scrollback) Len() int {
	if s == nil {
		return 0
	}
	return s.n
}

// MaxLines returns the maximum number of lines the scrollback buffer can hold.
func (s *Scrollback) MaxLines() int {
	if s == nil {
		return 0
	}
	return s.maxLines
}

// SetMaxLines sets the maximum number of lines for the scrollback buffer.
// If the current buffer exceeds the new limit, the oldest lines are removed.
func (s *Scrollback) SetMaxLines(maxLines int) {
	if s == nil || maxLines <= 0 {
		return
	}
	keep := min(s.n, maxLines)
	r := make([]packedLine, keep)
	for i := 0; i < keep; i++ {
		r[i] = s.ring[(s.head+s.n-keep+i)%len(s.ring)]
	}
	s.ring, s.head, s.n, s.maxLines = r, 0, keep, maxLines
}

// Line returns the line at the given index in the scrollback buffer.
// Index 0 is the oldest line. Returns nil if index is out of bounds.
// The returned line is a fresh copy.
func (s *Scrollback) Line(index int) uv.Line {
	if s == nil || index < 0 || index >= s.n {
		return nil
	}
	return s.ring[(s.head+index)%len(s.ring)].unpack()
}

// Lines returns copies of all lines in the scrollback buffer (oldest first).
func (s *Scrollback) Lines() []uv.Line {
	if s == nil {
		return nil
	}
	out := make([]uv.Line, s.n)
	for i := range out {
		out[i] = s.Line(i)
	}
	return out
}

// Clear removes all lines from the scrollback buffer.
func (s *Scrollback) Clear() {
	if s == nil {
		return
	}
	s.ring, s.head, s.n = nil, 0, 0
}

// CellAt returns the cell at the given position in the scrollback buffer.
// x is the column, y is the line index (0 = oldest).
// Returns nil if position is out of bounds.
func (s *Scrollback) CellAt(x, y int) *uv.Cell {
	line := s.Line(y)
	if line == nil || x < 0 || x >= len(line) {
		return nil
	}
	return &line[x]
}
