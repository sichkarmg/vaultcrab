// SPDX-License-Identifier: MIT

// Package term wraps a VT emulator (charmbracelet/x/vt) for one session:
// it renders the screen into ANSI-styled lines, keeps an in-memory scrollback
// and translates keyboard/mouse input into the byte sequences the remote
// application expects. Nothing is ever written to disk.
package term

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// DefaultScrollback is the default number of scrollback lines.
const DefaultScrollback = 10000

// writeChunk bounds a single emulator write so that scroll anchoring and
// locking granularity stay reasonable under bulk output.
const writeChunk = 32 << 10

// Terminal is safe for concurrent use: output is written from the network
// goroutine while the UI goroutine renders and sends input.
type Terminal struct {
	mu        sync.Mutex
	emu       *vt.Emulator
	w, h      int
	scrollOff int // lines scrolled back from the live view; 0 = live
	title     string
	mouse     map[ansi.Mode]bool
	bracketed bool
	sel       selection

	// sink receives bytes the terminal sends to the remote side (input and
	// emulator replies such as cursor position reports).
	sink atomic.Pointer[io.Writer]
	// send queues those bytes for the sink: writing to the sink blocks while
	// the remote side does not read (a paste of hundreds of lines into an
	// editor), and the emulator must not wait for it under mu, or the output
	// the remote side waits to get rid of could never be written.
	send sendQueue

	dirty atomic.Bool
	out   atomic.Uint64 // bytes of remote output so far (activity of hidden tabs)
}

// New creates a terminal of the given size with a scrollback limit (lines).
func New(w, h, scrollback int) *Terminal {
	if scrollback <= 0 {
		scrollback = DefaultScrollback
	}
	w, h = max(w, 1), max(h, 1)
	t := &Terminal{emu: vt.NewEmulator(w, h), w: w, h: h, mouse: map[ansi.Mode]bool{}}
	t.emu.SetScrollbackSize(scrollback)
	t.emu.SetCallbacks(vt.Callbacks{
		Title: func(s string) { t.title = s }, // called under t.mu (inside Write)
		EnableMode: func(m ansi.Mode) {
			t.setMode(m, true)
		},
		DisableMode: func(m ansi.Mode) {
			t.setMode(m, false)
		},
	})
	t.send.c = sync.NewCond(&t.send.mu)
	go t.pump()
	go t.deliver()
	return t
}

func (t *Terminal) setMode(m ansi.Mode, on bool) {
	switch m {
	case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight,
		ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent:
		t.mouse[m] = on
	case ansi.ModeBracketedPaste:
		t.bracketed = on
	}
}

// pump queues everything the emulator emits (key encodings, replies) for
// the current sink, dropping it while disconnected. It never blocks on the
// sink, so writing input into the emulator never waits for the remote side.
func (t *Terminal) pump() {
	defer t.send.close()
	buf := make([]byte, 4096)
	for {
		n, err := t.emu.Read(buf)
		if n > 0 {
			if w := t.sink.Load(); w != nil && *w != nil {
				t.send.put(buf[:n])
			}
		}
		if err != nil {
			return
		}
	}
}

// deliver writes the queued input to the current sink in order.
func (t *Terminal) deliver() {
	for {
		p, ok := t.send.take()
		if !ok {
			return
		}
		if w := t.sink.Load(); w != nil && *w != nil {
			_, _ = (*w).Write(p)
		}
	}
}

// sendQueue is an unbounded FIFO of bytes: input is small except for pastes,
// which are already in memory anyway.
type sendQueue struct {
	mu     sync.Mutex
	c      *sync.Cond
	buf    []byte
	closed bool
}

func (q *sendQueue) put(p []byte) {
	q.mu.Lock()
	q.buf = append(q.buf, p...)
	q.mu.Unlock()
	q.c.Signal()
}

// take waits for queued bytes and returns all of them; false once closed.
func (q *sendQueue) take() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.buf) == 0 && !q.closed {
		q.c.Wait()
	}
	if len(q.buf) == 0 {
		return nil, false
	}
	p := q.buf
	q.buf = nil
	return p, true
}

// drop discards the queued bytes (the session disconnected).
func (q *sendQueue) drop() {
	q.mu.Lock()
	q.buf = nil
	q.mu.Unlock()
}

func (q *sendQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.c.Signal()
}

// SetSink sets where input for the remote side goes (nil = discard).
func (t *Terminal) SetSink(w io.Writer) {
	if w == nil {
		t.sink.Store(nil)
		t.send.drop() // input typed for the lost connection is not replayed
		return
	}
	t.sink.Store(&w)
}

// Close releases the emulator.
func (t *Terminal) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.emu.Close()
}

// Write feeds remote output into the emulator.
func (t *Terminal) Write(p []byte) (int, error) {
	t.out.Add(uint64(len(p)))
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		c := p[:min(len(p), writeChunk)]
		before := t.emu.Scrollback().Total()
		if _, err := t.emu.Write(c); err != nil {
			return n - len(p), err
		}
		if t.scrollOff > 0 {
			// Keep the viewed region stable while new output arrives.
			t.scrollOff += t.emu.Scrollback().Total() - before
		}
		p = p[len(c):]
	}
	t.scrollOff = min(t.scrollOff, t.emu.ScrollbackLen())
	t.dirty.Store(true)
	return n, nil
}

// WriteString writes a local message (e.g. status lines) into the terminal.
func (t *Terminal) WriteString(s string) { _, _ = t.Write([]byte(s)) }

// Output counts the bytes of output written so far: a change means new
// output.
func (t *Terminal) Output() uint64 { return t.out.Load() }

// Dirty reports and clears the changed-since-last-call flag.
func (t *Terminal) Dirty() bool { return t.dirty.Swap(false) }

// Size returns the terminal size.
func (t *Terminal) Size() (w, h int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.w, t.h
}

// Resize changes the terminal size. It reports whether the size changed.
func (t *Terminal) Resize(w, h int) bool {
	w, h = max(w, 1), max(h, 1)
	t.mu.Lock()
	defer t.mu.Unlock()
	if w == t.w && h == t.h {
		return false
	}
	t.w, t.h = w, h
	t.emu.Resize(w, h)
	t.dirty.Store(true)
	return true
}

// Title returns the window title set by the remote side (OSC 0/2).
func (t *Terminal) Title() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.title
}

// AltScreen reports whether a full-screen application is active.
func (t *Terminal) AltScreen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.IsAltScreen()
}

// MouseTracking reports whether the remote application requested mouse events.
func (t *Terminal) MouseTracking() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, on := range t.mouse {
		if on {
			return true
		}
	}
	return false
}

// ScrollbackLen returns the number of lines in the scrollback.
func (t *Terminal) ScrollbackLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.ScrollbackLen()
}

// Scroll moves the view by n lines (positive = back in history).
func (t *Terminal) Scroll(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.emu.IsAltScreen() {
		return
	}
	t.scrollOff = max(0, min(t.scrollOff+n, t.emu.ScrollbackLen()))
	t.dirty.Store(true)
}

// ScrollToBottom returns to the live view.
func (t *Terminal) ScrollToBottom() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.scrollOff != 0 {
		t.scrollOff = 0
		t.dirty.Store(true)
	}
}

// ScrollOffset returns how many lines the view is scrolled back.
func (t *Terminal) ScrollOffset() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.scrollOff
}

// RenderOptions controls Render.
type RenderOptions struct {
	Cursor bool // draw the cursor (focused pane, live view)
}

// Render returns exactly h lines of exactly w columns each with ANSI styles.
// The terminal content is clipped or padded to w×h.
func (t *Terminal) Render(w, h int, o RenderOptions) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := make([]string, h)
	sbLen := t.emu.ScrollbackLen()
	off := t.scrollOff
	if t.emu.IsAltScreen() {
		off = 0
	}
	cur := t.emu.CursorPosition()
	showCursor := o.Cursor && off == 0 && !t.emu.CursorHidden()
	var b strings.Builder
	for y := 0; y < h; y++ {
		b.Reset()
		// Row y of the view maps to scrollback line (sbLen-off+y) if negative
		// relative to the screen, else screen row (y-off).
		sy := y - off
		var cell func(x int) *uv.Cell
		if sy < 0 {
			line := t.emu.Scrollback().Line(sbLen + sy)
			cell = func(x int) *uv.Cell {
				if x < len(line) {
					return &line[x]
				}
				return nil
			}
		} else if sy < t.h {
			cell = func(x int) *uv.Cell {
				if x < t.w {
					return t.emu.CellAt(x, sy)
				}
				return nil
			}
		} else {
			cell = func(int) *uv.Cell { return nil }
		}
		var selected func(x int) bool
		if t.sel.active {
			line := t.viewLine(y)
			selected = func(x int) bool { return t.sel.contains(line, x) }
		}
		renderLine(&b, w, cell, showCursor && sy == cur.Y, cur.X, selected)
		lines[y] = b.String()
	}
	return lines
}

func renderLine(b *strings.Builder, w int, cell func(x int) *uv.Cell, cursorRow bool, cx int, selected func(x int) bool) {
	var prev uv.Style
	styled := false
	setStyle := func(s uv.Style) {
		if styled && s.Equal(&prev) {
			return
		}
		if s.IsZero() {
			if styled {
				b.WriteString(ansi.ResetStyle)
			}
			styled = false
			prev = s
			return
		}
		b.WriteString(ansi.ResetStyle)
		b.WriteString(s.String())
		styled = true
		prev = s
	}
	col := 0
	for x := 0; col < w; x++ {
		c := cell(x)
		st := uv.Style{}
		content := " "
		width := 1
		if c != nil {
			if c.Width == 0 && c.Content == "" && x > 0 {
				continue // continuation of a wide cell
			}
			st = c.Style
			if c.Content != "" {
				content = c.Content
				width = max(c.Width, 1)
			}
		}
		if col+width > w {
			content, width = " ", 1
		}
		if cursorRow && x == cx {
			st.Attrs ^= uv.AttrReverse
		}
		if selected != nil && selected(x) {
			st.Attrs ^= uv.AttrReverse
		}
		setStyle(st)
		b.WriteString(content)
		col += width
	}
	if styled {
		b.WriteString(ansi.ResetStyle)
	}
}

// PlainText returns the visible screen as plain text (for tests/diagnostics).
func (t *Terminal) PlainText() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.emu.String()
}

// History returns scrollback plus screen as plain text (tests/diagnostics).
func (t *Terminal) History() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var b strings.Builder
	for _, l := range t.emu.Scrollback().Lines() {
		b.WriteString(strings.TrimRight(l.String(), " "))
		b.WriteByte('\n')
	}
	b.WriteString(t.emu.String())
	return b.String()
}
