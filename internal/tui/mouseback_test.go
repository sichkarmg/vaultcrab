// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// After bw login had the terminal, the mouse reports are turned on again
// (Bubble Tea restores the rest of the terminal, not the mouse); without
// ui.mouse they stay off.
func TestMouseBackAfterLogin(t *testing.T) {
	m, _ := newTestModel(t, false)
	m.opt.Mouse = true
	cmd := m.afterLogin(loginDoneMsg{})
	if cmd == nil {
		t.Fatal("no command after bw login")
	}
	if msg := fmt.Sprintf("%T", cmd()); !strings.Contains(strings.ToLower(msg), "mouse") {
		t.Fatalf("not the mouse: %s", msg)
	}
	m2, _ := newTestModel(t, false)
	if m2.afterLogin(loginDoneMsg{}) != nil {
		t.Fatal("mouse turned on with ui.mouse off")
	}
}
