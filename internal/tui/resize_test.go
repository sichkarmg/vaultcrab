// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A resize clears the screen and paints it in full, and once more when no
// other resize followed for resizeSettled (the terminal may change its
// cells after the frame); an older settle does nothing.
func TestResizeRepaints(t *testing.T) {
	m, _ := newTestModel(t, false)
	clear := fmt.Sprintf("%T", tea.ClearScreen())
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 2 || fmt.Sprintf("%T", batch[0]()) != clear {
		t.Fatalf("resize: %#v", batch)
	}
	first := m.resizeGen
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 30})
	if _, cmd := m.Update(resizeSettledMsg{gen: first}); cmd != nil {
		t.Fatal("an older resize repaints")
	}
	_, cmd = m.Update(resizeSettledMsg{gen: m.resizeGen})
	if cmd == nil || fmt.Sprintf("%T", cmd()) != clear {
		t.Fatal("no repaint after the last resize")
	}
}
