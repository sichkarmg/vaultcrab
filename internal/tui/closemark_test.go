// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// clickClose clicks the ✕ in the top border of the dialog.
func clickClose(t *testing.T, m *Model) {
	t.Helper()
	v := strings.Split(ansi.Strip(m.View()), "\n")
	y := m.dlgY
	if m.dlgClose < 0 || !strings.Contains(v[y], "✕") {
		t.Fatalf("no ✕ on the dialog's top border:\n%s", strings.Join(v, "\n"))
	}
	m.Update(tea.MouseMsg{X: m.dlgX + m.dlgClose, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
}

// Every dialog has a ✕ that closes it with the mouse: the help, the
// settings, the jump picker (from step 2: closed, not one step back), the
// item form.
func TestCloseMarkOnDialogs(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	for _, open := range []struct {
		name string
		do   func()
	}{
		{"help", func() { keys(m, "?") }},
		{"settings", func() { m.openSettings() }},
		{"jump picker", func() { keys(m, "j", "enter") }},
		{"tunnels", func() { m.openTunnels(nil) }},
	} {
		open.do()
		if m.modal == nil {
			t.Fatalf("%s: not open", open.name)
		}
		clickClose(t, m)
		if m.modal != nil {
			t.Fatalf("%s: still open (%T)", open.name, m.modal)
		}
	}
}
