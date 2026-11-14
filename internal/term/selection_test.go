// SPDX-License-Identifier: MIT

package term

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSelectionText(t *testing.T) {
	tm := New(20, 4, 100)
	defer tm.Close()
	tm.WriteString("first line\r\nsecond  line  \r\nthird\r\n界x end")
	// from "line" on row 0 to "thi" on row 2
	tm.SetSelection(tm.ViewPos(6, 0), tm.ViewPos(2, 2))
	if got := tm.SelectedText(); got != "line\nsecond  line\nthi" {
		t.Errorf("text %q", got)
	}
	// reversed order, wide characters
	tm.SetSelection(tm.ViewPos(3, 3), tm.ViewPos(0, 3))
	if got := tm.SelectedText(); got != "界x" {
		t.Errorf("wide %q", got)
	}
}

// The selection is anchored to the history: new output that scrolls the
// screen does not move it.
func TestSelectionAnchoredToHistory(t *testing.T) {
	tm := New(20, 3, 100)
	defer tm.Close()
	tm.WriteString("aaa\r\nbbb\r\nccc")
	tm.SetSelection(tm.ViewPos(0, 1), tm.ViewPos(2, 1)) // "bbb"
	for i := 0; i < 5; i++ {
		tm.WriteString(fmt.Sprintf("\r\nnew%d", i))
	}
	if got := tm.SelectedText(); got != "bbb" {
		t.Errorf("after scroll %q", got)
	}
	// selecting while scrolled back into history
	tm.Scroll(3)
	a := tm.ViewPos(0, 0)
	tm.SetSelection(a, tm.ViewPos(19, 1))
	want := strings.Split(tm.History(), "\n")
	if got := tm.SelectedText(); got != want[a.Line]+"\n"+want[a.Line+1] {
		t.Errorf("history %q, want lines %d..: %q", got, a.Line, want)
	}
	// rendering highlights the selection (reverse video)
	if !strings.Contains(strings.Join(tm.Render(20, 3, RenderOptions{}), ""), "\x1b[7m") {
		t.Error("selection not highlighted")
	}
	tm.ClearSelection()
	if tm.HasSelection() || strings.Contains(strings.Join(tm.Render(20, 3, RenderOptions{}), ""), "\x1b[7m") {
		t.Error("selection not cleared")
	}
}

func TestWordAndLine(t *testing.T) {
	tm := New(40, 2, 10)
	defer tm.Close()
	tm.WriteString(`cat "/etc/host.conf" | less`)
	a, b := tm.WordAt(tm.ViewPos(8, 0))
	tm.SetSelection(a, b)
	if got := tm.SelectedText(); got != "/etc/host.conf" {
		t.Errorf("word %q", got)
	}
	a, b = tm.LineBounds(tm.ViewPos(3, 0))
	tm.SetSelection(a, b)
	if got := ansi.Strip(tm.SelectedText()); got != `cat "/etc/host.conf" | less` {
		t.Errorf("line %q", got)
	}
}

// A line that wrapped at the right margin is copied as one line, also from
// the history; a triple click takes the whole wrapped line.
func TestSelectionWrappedLines(t *testing.T) {
	tm := New(10, 3, 100)
	defer tm.Close()
	long := "hello world, this is long"
	tm.WriteString(long + "\r\nnext\r\n$ ")
	// rows: hello worl|d, this is| long|next|$ : the first two in the history
	tm.Scroll(2)
	tm.SetSelection(tm.ViewPos(0, 0), tm.ViewPos(9, 2))
	if got := tm.SelectedText(); got != long {
		t.Fatalf("history: %q", got)
	}
	tm.ScrollToBottom()
	tm.Scroll(100)
	a, b := tm.LineBounds(tm.ViewPos(3, 1))
	tm.SetSelection(a, b)
	if got := tm.SelectedText(); got != long {
		t.Fatalf("triple click: %q", got)
	}
	tm.SetSelection(tm.ViewPos(0, 0), tm.ViewPos(3, 3))
	if got := tm.SelectedText(); got != long+"\nnext" {
		t.Fatalf("with the next line: %q", got)
	}
}
