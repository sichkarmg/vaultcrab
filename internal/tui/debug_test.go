// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// A failed connection shows its steps in the pane; the status bar has
// [Copy debug], y and a click copy them.
func TestConnectionDebugCopy(t *testing.T) {
	m, _ := newTestModel(t, false)
	keys(m, "enter") // alpha: the fake vault has no secrets, the attempt fails
	if len(m.tabs) != 1 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	s := m.tabs[0].focused()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if st, _ := s.State(); st == core.SessionDisconnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not failed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.onEvent()
	if txt := s.Term.PlainText(); !strings.Contains(txt, "connection debug") || !strings.Contains(txt, "credential: no secrets in tests") {
		t.Fatalf("no debug in the pane:\n%s", txt)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "[Copy debug]") {
		t.Fatalf("no button:\n%s", v)
	}
	m.Update(eventMsg{})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "connection debug") {
		t.Fatalf("pane not redrawn:\n%s", v)
	}
	keys(m, "y")
	if !strings.Contains(m.clip, "reading the credential from the vault") {
		t.Fatalf("clip %q", m.clip)
	}
	m.clip = ""
	m.Update(tea.MouseMsg{X: m.w - 3, Y: m.statusY(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !strings.Contains(m.clip, "credential:") {
		t.Fatalf("click: clip %q", m.clip)
	}
	// off: no debug at the next attempt
	a := m.opt.Appearance
	a.ConnDebug = false
	m.applyAppearance(a)
	keys(m, "enter") // reconnect
	for {
		if st, _ := s.State(); st == core.SessionDisconnected && s.Attempts() == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Debug() != "" && strings.Count(s.Term.PlainText(), "connection debug") > 1 {
		t.Fatal("traced while off")
	}
}
