// SPDX-License-Identifier: MIT

package term

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timeout")
}

func TestRenderSizeAndStyles(t *testing.T) {
	tm := New(10, 3, 100)
	tm.WriteString("ab\x1b[31mcd\x1b[0m\r\n界x")
	lines := tm.Render(12, 4, RenderOptions{})
	if len(lines) != 4 {
		t.Fatalf("lines %d", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 12 {
			t.Errorf("line %d width %d: %q", i, w, l)
		}
	}
	if !strings.Contains(lines[0], "\x1b[31m") || ansi.Strip(lines[0]) != "abcd        " {
		t.Errorf("line0 %q", lines[0])
	}
	if ansi.Strip(lines[1]) != "界x         " {
		t.Errorf("line1 %q", ansi.Strip(lines[1]))
	}
	// clipping
	small := tm.Render(3, 1, RenderOptions{})
	if ansi.Strip(small[0]) != "abc" {
		t.Errorf("clip %q", small[0])
	}
}

func TestScrollback(t *testing.T) {
	tm := New(20, 5, 50)
	for i := 0; i < 200; i++ {
		fmt.Fprintf(tm, "line %d\r\n", i)
	}
	if n := tm.ScrollbackLen(); n != 50 {
		t.Fatalf("scrollback len %d", n)
	}
	tm.Scroll(3)
	top := ansi.Strip(tm.Render(20, 5, RenderOptions{})[0])
	if !strings.HasPrefix(top, "line 193") {
		t.Fatalf("after scroll top = %q", top)
	}
	// New output keeps the view anchored.
	tm.WriteString("line 200\r\n")
	if top2 := ansi.Strip(tm.Render(20, 5, RenderOptions{})[0]); top2 != top {
		t.Errorf("view moved: %q -> %q", top, top2)
	}
	tm.Scroll(1 << 20)
	if off := tm.ScrollOffset(); off != 50 {
		t.Errorf("max offset %d", off)
	}
	tm.ScrollToBottom()
	if live := ansi.Strip(tm.Render(20, 5, RenderOptions{})[3]); !strings.HasPrefix(live, "line 200") {
		t.Errorf("live %q", live)
	}
}

func TestAltScreenDisablesScroll(t *testing.T) {
	tm := New(20, 5, 50)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(tm, "%d\r\n", i)
	}
	tm.WriteString("\x1b[?1049h")
	if !tm.AltScreen() {
		t.Fatal("alt screen not detected")
	}
	tm.Scroll(5)
	if tm.ScrollOffset() != 0 {
		t.Error("scrolling in alt screen")
	}
}

func TestKeysAndModes(t *testing.T) {
	tm := New(20, 5, 50)
	var out syncBuf
	tm.SetSink(&out)
	tm.SendKey(Key{Code: KeyUp})
	tm.SendKey(Key{Code: 'c', Ctrl: true})
	tm.SendKey(Key{Text: "ж"})
	tm.SendKey(Key{Text: "x", Alt: true})
	waitFor(t, func() bool { return strings.HasSuffix(out.String(), "\x1bx") })
	if got := out.String(); got != "\x1b[A\x03ж\x1bx" {
		t.Fatalf("normal mode: %q", got)
	}
	// application cursor keys (DECCKM), as set by vim/htop
	tm.WriteString("\x1b[?1h")
	tm.SendKey(Key{Code: KeyUp})
	waitFor(t, func() bool { return strings.HasSuffix(out.String(), "\x1bOA") })

	// mouse tracking
	if tm.SendMouse(Mouse{X: 1, Y: 2, Button: MouseLeft}) {
		t.Fatal("mouse consumed without tracking")
	}
	tm.WriteString("\x1b[?1000h\x1b[?1006h")
	if !tm.MouseTracking() || !tm.SendMouse(Mouse{X: 1, Y: 2, Button: MouseLeft}) {
		t.Fatal("mouse not forwarded")
	}
	waitFor(t, func() bool { return strings.HasSuffix(out.String(), "\x1b[<0;2;3M") })

	// bracketed paste
	tm.WriteString("\x1b[?2004h")
	tm.Paste("hi")
	waitFor(t, func() bool { return strings.HasSuffix(out.String(), "\x1b[200~hi\x1b[201~") })
}

func TestDeviceStatusReply(t *testing.T) {
	tm := New(20, 5, 50)
	var out syncBuf
	tm.SetSink(&out)
	tm.WriteString("ab\x1b[6n")
	waitFor(t, func() bool { return out.String() == "\x1b[1;3R" })
}

func TestResize(t *testing.T) {
	tm := New(20, 5, 50)
	if tm.Resize(20, 5) {
		t.Error("same size reported as change")
	}
	if !tm.Resize(30, 10) {
		t.Error("resize not reported")
	}
	if w, h := tm.Size(); w != 30 || h != 10 {
		t.Error(w, h)
	}
}

func BenchmarkYes(b *testing.B) {
	tm := New(120, 40, DefaultScrollback)
	chunk := bytes.Repeat([]byte("y\r\n"), 32<<10/3)
	b.SetBytes(int64(len(chunk)))
	for i := 0; i < b.N; i++ {
		tm.Write(chunk)
	}
}

func BenchmarkRender(b *testing.B) {
	tm := New(200, 60, DefaultScrollback)
	for i := 0; i < 100; i++ {
		fmt.Fprintf(tm, "\x1b[3%dmline %d %s\x1b[0m\r\n", i%8, i, strings.Repeat("x", 150))
	}
	for i := 0; i < b.N; i++ {
		tm.Render(200, 60, RenderOptions{Cursor: true})
	}
}

func BenchmarkBase64Lines(b *testing.B) {
	tm := New(120, 40, DefaultScrollback)
	line := []byte(strings.Repeat("QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo", 3)[:76] + "\r\n")
	chunk := bytes.Repeat(line, 32<<10/len(line))
	b.SetBytes(int64(len(chunk)))
	for i := 0; i < b.N; i++ {
		tm.Write(chunk)
	}
}

// RouterOS probes the terminal on login (DECID, cursor reports, a scroll
// region of 1;9999) and then draws the prompt on the bottom line.
func TestRouterOSLogin(t *testing.T) {
	raw, err := os.ReadFile("testdata/routeros7-login.bin")
	if err != nil {
		t.Fatal(err)
	}
	tm := New(100, 30, 100)
	defer tm.Close()
	var replies syncBuf
	tm.SetSink(&replies)
	tm.Write(raw)
	waitFor(t, func() bool { return strings.Contains(replies.String(), "\x1b[?62;") })
	lines := strings.Split(tm.PlainText(), "\n")
	last := strings.TrimRight(lines[len(lines)-1], " ")
	if last != "[testusr@ros2207] >" {
		t.Errorf("bottom line %q", last)
	}
	if !strings.Contains(tm.History(), "MikroTik RouterOS 7") {
		t.Error("banner lost")
	}
	// The region must have been clamped: output scrolls the whole screen.
	tm.WriteString("\r\nline1\r\nline2")
	lines = strings.Split(tm.PlainText(), "\n")
	if got := strings.TrimRight(lines[len(lines)-1], " "); got != "line2" {
		t.Errorf("after scroll: %q", got)
	}
}

// slowSink is a remote application busy with its output: it reads no input
// until released (mcedit redrawing after each pasted line).
type slowSink struct {
	release chan struct{}
	mu      sync.Mutex
	got     bytes.Buffer
}

func (s *slowSink) Write(p []byte) (int, error) {
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got.Write(p)
}

func (s *slowSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got.String()
}

// A large paste into an application that does not read its input until
// its output is taken must not lock up the terminal: the paste returns, and
// the output (and the redraws of the UI) still go through. Pasting 300+
// lines into mcedit used to freeze vaultcrab.
func TestPasteDoesNotBlockOutput(t *testing.T) {
	tm := New(80, 24, 100)
	defer tm.Close()
	sink := &slowSink{release: make(chan struct{})}
	tm.SetSink(sink)
	tm.Write([]byte("\x1b[?2004h")) // bracketed paste on, as mcedit does
	var text strings.Builder
	for i := range 400 {
		fmt.Fprintf(&text, "line %d: the quick brown fox jumps over the lazy dog\n", i)
	}
	done := make(chan struct{})
	go func() {
		tm.Paste(text.String())
		tm.Write([]byte("redraw\x1b[6n")) // output, and a reply to send back
		tm.Render(80, 24, RenderOptions{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(sink.release) // let the deferred Close finish
		t.Fatal("paste blocked the terminal while the remote side does not read")
	}
	close(sink.release)
	want := ansi.BracketedPasteStart + text.String() + ansi.BracketedPasteEnd + "\x1b[1;7R"
	deadline := time.Now().Add(3 * time.Second)
	for sink.String() != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := sink.String(); got != want {
		t.Errorf("sent %d bytes, want %d in order (tail %q)", len(got), len(want), got[max(0, len(got)-20):])
	}
}
