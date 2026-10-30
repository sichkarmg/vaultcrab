// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

func mouse(m *Model, x, y int, b tea.MouseButton, a tea.MouseAction) {
	m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: a})
}

// failedSessionTab opens alpha (fails: no secret) so the pane shows
// "[vaultcrab] connecting to u@10.0.0.1…" on its second line.
func failedSessionTab(t *testing.T) *Model {
	m, _ := newTestModel(t, false)
	m.opt.CopyOSC52 = true
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "enter")
	deadline := time.Now().Add(2 * time.Second)
	for st, _ := m.tabs[0].focused().State(); st != core.SessionDisconnected; st, _ = m.tabs[0].focused().State() {
		if time.Now().After(deadline) {
			t.Fatal("session did not fail")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return m
}

func TestMouseSelectionCopies(t *testing.T) {
	m := failedSessionTab(t)
	// body starts at y=1 (tabs on top); line 2 of the terminal is y=2
	mouse(m, 1, 2, tea.MouseButtonLeft, tea.MouseActionPress)
	mouse(m, 10, 2, tea.MouseButtonLeft, tea.MouseActionMotion)
	mouse(m, 15, 2, tea.MouseButtonLeft, tea.MouseActionMotion)
	mouse(m, 15, 2, tea.MouseButtonLeft, tea.MouseActionRelease)
	if m.clip != "vaultcrab] conn" {
		t.Errorf("copied %q", m.clip)
	}
	v := m.View()
	if !strings.Contains(v, "\x1b]52;c;") {
		t.Error("no OSC 52 in the frame")
	}
	checkFrame(t, v, 120, 30)
	if !strings.Contains(ansi.Strip(v), "copied 15 characters") {
		t.Error("no toast")
	}
	// double click selects a word
	mouse(m, 28, 2, tea.MouseButtonLeft, tea.MouseActionPress)
	mouse(m, 28, 2, tea.MouseButtonLeft, tea.MouseActionRelease)
	mouse(m, 28, 2, tea.MouseButtonLeft, tea.MouseActionPress)
	mouse(m, 28, 2, tea.MouseButtonLeft, tea.MouseActionRelease)
	if !strings.HasPrefix(m.clip, "u@10.0.0.1") {
		t.Errorf("word %q", m.clip)
	}
	// a single click clears the highlight; typing keeps working
	time.Sleep(450 * time.Millisecond)
	mouse(m, 5, 5, tea.MouseButtonLeft, tea.MouseActionPress)
	mouse(m, 5, 5, tea.MouseButtonLeft, tea.MouseActionRelease)
	if m.tabs[0].focused().Term.HasSelection() {
		t.Error("click did not clear the selection")
	}
}

func TestKeyboardSelectionInHistory(t *testing.T) {
	m := failedSessionTab(t)
	tb := m.tabs[0]
	id := tb.ws.Focused()
	tb.history[id] = true // what prefix + [ does (the test keymap has no binding)
	s := tb.focused()
	// move the selection up from the bottom line to line 2 and copy
	keys(m, "v")
	if tb.ksel[id] == nil {
		t.Fatal("v did not start a selection")
	}
	for i := 0; i < 40; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	keys(m, "y")
	if !strings.Contains(m.clip, "[vaultcrab] connecting to u@10.0.0.1") || !strings.Contains(m.clip, "no secrets in tests") {
		t.Errorf("copied %q", m.clip)
	}
	if s.Term.HasSelection() || tb.ksel[id] != nil {
		t.Error("selection not finished")
	}
}

// Holding the left button and turning the wheel scrolls the history and
// extends the selection.
func TestWheelExtendsSelection(t *testing.T) {
	m := failedSessionTab(t)
	s := m.tabs[0].focused()
	for i := 0; i < 60; i++ {
		s.Term.WriteString(fmt.Sprintf("\r\nline-%02d", i))
	}
	mouse(m, 1, 20, tea.MouseButtonLeft, tea.MouseActionPress)
	mouse(m, 5, 20, tea.MouseButtonLeft, tea.MouseActionMotion)
	mouse(m, 5, 20, tea.MouseButtonWheelUp, tea.MouseActionPress)
	mouse(m, 5, 20, tea.MouseButtonWheelUp, tea.MouseActionPress)
	if s.Term.ScrollOffset() != 6 {
		t.Errorf("offset %d", s.Term.ScrollOffset())
	}
	mouse(m, 5, 20, tea.MouseButtonLeft, tea.MouseActionRelease)
	if n := strings.Count(m.clip, "\n"); n != 6 {
		t.Errorf("selected %d line breaks: %q", n, m.clip)
	}
	if m.tabs[0].sel != nil {
		t.Error("drag not finished")
	}
}
