package vt

import (
	"strings"
	"testing"
)

// screenRows returns the rows of the main screen without trailing blanks.
func screenRows(e *Emulator) []string {
	var out []string
	for y := 0; y < e.Height(); y++ {
		var b strings.Builder
		for x := 0; x < e.Width(); x++ {
			if c := e.CellAt(x, y); c != nil && c.Content != "" {
				b.WriteString(c.Content)
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func historyRows(e *Emulator) []string {
	var out []string
	sb := e.Scrollback()
	for i := 0; i < sb.Len(); i++ {
		var b strings.Builder
		for _, c := range sb.Line(i) {
			b.WriteString(c.Content)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// A long line wraps; narrower it wraps again, wider it joins: nothing is
// lost, the cursor stays after the text, hard line breaks stay.
func TestReflowWidth(t *testing.T) {
	e := NewEmulator(10, 5)
	e.WriteString("abcdefghijklmnopqrstuvwxyz\r\nshort\r\n$ ")
	eq(t, "start", screenRows(e), []string{"abcdefghij", "klmnopqrst", "uvwxyz", "short", "$"})
	if p := e.CursorPosition(); p.X != 2 || p.Y != 4 {
		t.Fatalf("cursor %v", p)
	}
	e.Resize(7, 5)
	all := append(historyRows(e), screenRows(e)...)
	eq(t, "narrow", all, []string{"abcdefg", "hijklmn", "opqrstu", "vwxyz", "short", "$", "", ""}[:len(all)])
	if p := e.CursorPosition(); screenRows(e)[p.Y] != "$" || p.X != 2 {
		t.Fatalf("narrow cursor %v %q", p, screenRows(e))
	}
	e.Resize(40, 5)
	eq(t, "wide", screenRows(e), []string{"abcdefghijklmnopqrstuvwxyz", "short", "$", "", ""})
	if p := e.CursorPosition(); p.X != 2 || p.Y != 2 {
		t.Fatalf("wide cursor %v", p)
	}
	if n := e.Scrollback().Len(); n != 0 {
		t.Fatalf("history %d: %q", n, historyRows(e))
	}
	// typing goes on where it was
	e.WriteString("ls")
	if got := screenRows(e)[2]; got != "$ ls" {
		t.Fatalf("typed %q", got)
	}
}

// Spaces at a wrap point are kept: words do not glue together.
func TestReflowKeepsSpaces(t *testing.T) {
	e := NewEmulator(6, 3)
	e.WriteString("hello world")
	e.Resize(20, 3)
	if got := screenRows(e)[0]; got != "hello world" {
		t.Fatalf("%q", got)
	}
}

// Lower height sends the top to the history, more height brings it back
// when the screen was full.
func TestReflowHeight(t *testing.T) {
	e := NewEmulator(10, 4)
	e.WriteString("1\r\n2\r\n3\r\n4")
	e.Resize(10, 2)
	eq(t, "low", screenRows(e), []string{"3", "4"})
	eq(t, "history", historyRows(e), []string{"1", "2"})
	if p := e.CursorPosition(); p.Y != 1 || p.X != 1 {
		t.Fatalf("cursor %v", p)
	}
	e.Resize(10, 4)
	eq(t, "back", screenRows(e), []string{"1", "2", "3", "4"})
	if p := e.CursorPosition(); p.Y != 3 {
		t.Fatalf("cursor %v", p)
	}
	// a screen that is not full does not take lines back
	e.WriteString("\x1b[2J\x1b[H$ ")
	e.Resize(10, 6)
	if got := screenRows(e)[0]; got != "$" {
		t.Fatalf("after clear: %q", screenRows(e))
	}
}

// The history is rewrapped too, and the chain that continues from the
// history into the screen is joined.
func TestReflowHistory(t *testing.T) {
	e := NewEmulator(5, 2)
	e.WriteString("0123456789ABCDE\r\nx")
	// 01234|56789|ABCDE|x : the first two in the history
	eq(t, "history", historyRows(e), []string{"01234", "56789"})
	e.Resize(8, 2)
	all := append(historyRows(e), screenRows(e)...)
	eq(t, "rewrapped", all, []string{"01234567", "89ABCDE", "x"})
	e.Resize(20, 2)
	eq(t, "joined", screenRows(e), []string{"0123456789ABCDE", "x"})
}

// A cursor at the end of a full row waits there (phantom) and the next
// character goes to the next row, as before the resize.
func TestReflowPhantom(t *testing.T) {
	e := NewEmulator(5, 3)
	e.WriteString("abcde")
	e.Resize(5, 4)
	e.WriteString("f")
	eq(t, "rows", screenRows(e)[:2], []string{"abcde", "f"})
}

// Wide characters are not cut in two.
func TestReflowWide(t *testing.T) {
	e := NewEmulator(6, 3)
	e.WriteString("ab界界界")
	e.Resize(5, 3)
	rows := screenRows(e)
	if strings.Join(rows[:2], "|") != "ab界|界界" {
		t.Fatalf("%q", rows)
	}
}

// The alternate screen is not reflowed; the main screen behind it is.
func TestReflowAltScreen(t *testing.T) {
	e := NewEmulator(10, 3)
	e.WriteString("abcdefghijkl\r\n$ ")
	e.WriteString("\x1b[?1049h")
	e.WriteString("mc mc mc mc")
	e.Resize(6, 3)
	if !e.IsAltScreen() {
		t.Fatal("left the alt screen")
	}
	e.WriteString("\x1b[?1049l")
	all := append(historyRows(e), screenRows(e)...)
	if !strings.Contains(strings.Join(all, "|"), "abcdef|ghijkl|$") {
		t.Fatalf("main after alt: %q", all)
	}
}

// Erasing the end of a wrapped line ends the wrap.
func TestReflowEraseEndsWrap(t *testing.T) {
	e := NewEmulator(5, 3)
	e.WriteString("abcdefg")
	e.WriteString("\x1b[1;3H\x1b[K") // erase from column 3 of the first row
	e.Resize(10, 3)
	eq(t, "rows", screenRows(e)[:2], []string{"ab", "fg"})
}

// A resize with a full history (10 000 lines, half of them wrapped) stays
// fast enough for every step of a window drag.
func BenchmarkReflowHistory(b *testing.B) {
	e := NewEmulator(200, 50)
	line := strings.Repeat("x", 150) + "\r\n"
	long := strings.Repeat("y", 350) + "\r\n"
	for i := 0; i < 5000; i++ {
		e.WriteString(line)
		e.WriteString(long)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			e.Resize(120, 50)
		} else {
			e.Resize(200, 50)
		}
	}
}
