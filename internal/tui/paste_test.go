// SPDX-License-Identifier: MIT

//go:build !windows

package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

func pasteMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}
}

func TestMultiLinePasteAsks(t *testing.T) {
	m, _ := restoreModel(t, filepath.Join(t.TempDir(), "w.json")) // local shell: /bin/sh
	m.opt.ConfirmPaste = true
	keys(m, "t")
	s := m.curTab().focused()
	for st, _ := s.State(); st != core.SessionConnected; st, _ = s.State() {
		time.Sleep(10 * time.Millisecond)
	}

	// one line: pasted right away
	m.Update(pasteMsg("echo one-$((40+2))\n"))
	if m.modal != nil {
		t.Fatal("a single line asked")
	}
	waitView(t, m, "one-42")

	// several lines: shown first; Enter alone (Cancel is the default) pastes nothing
	m.Update(pasteMsg("echo two-$((1+1))\necho three-$((1+2))\n"))
	p, ok := m.modal.(*pasteModal)
	if !ok || len(p.lines) != 2 {
		t.Fatalf("modal %T", m.modal)
	}
	v := waitView(t, m, "Paste 2 lines into")
	if !strings.Contains(v, "echo three-$((1+2))") {
		t.Fatalf("text not shown:\n%s", v)
	}
	checkFrame(t, m.View(), 120, 30)
	keys(m, "enter")
	if m.modal != nil {
		t.Fatal("Enter did not cancel")
	}
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(ansi.Strip(m.View()), "two-2") {
		t.Fatal("pasted after Cancel")
	}

	// click on Paste
	m.Update(pasteMsg("echo two-$((1+1))\necho three-$((1+2))\n"))
	p = m.modal.(*pasteModal)
	frame := strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.Contains(frame[m.dlgY+p.row], "[ Paste ]") {
		t.Fatalf("buttons not at row %d:\n%s", p.row, strings.Join(frame, "\n"))
	}
	m.Update(tea.MouseMsg{X: m.dlgX + boxPadX + p.spans[pasteDo][0] + 1, Y: m.dlgY + p.row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.modal != nil {
		t.Fatal("the click did not paste")
	}
	waitView(t, m, "two-2")
	waitView(t, m, "three-3")

	// turned off: pasted at once
	m.opt.ConfirmPaste = false
	m.Update(pasteMsg("echo four-$((2+2))\necho five\n"))
	if m.modal != nil {
		t.Fatal("asked although turned off")
	}
	waitView(t, m, "four-4")
}

func TestPasteDialogScrolls(t *testing.T) {
	m, _ := newTestModel(t, false)
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line %d\t\x1b[31mred\x1b[0m\n", i)
	}
	p := &pasteModal{s: m.svc.OpenLocal(80, 20), text: b.String(), lines: plainLines(b.String()), focus: pasteCancel}
	defer m.svc.Shutdown()
	m.modal = p
	v := ansi.Strip(m.View())
	checkFrame(t, m.View(), 120, 30)
	if !strings.Contains(v, "Paste 100 lines") || !strings.Contains(v, "line 1    red") || strings.Contains(v, "line 100 ") {
		t.Fatalf("first page:\n%s", v)
	}
	keys(m, "end")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "line 100 ") || !strings.Contains(v, "of 100") {
		t.Fatalf("end:\n%s", v)
	}
	m.Update(tea.MouseMsg{X: m.dlgX + 3, Y: m.dlgY + 6, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if p.top != 100-p.shown-3 {
		t.Fatalf("wheel: top %d", p.top)
	}
	keys(m, "home", "up")
	if p.top != 0 {
		t.Fatalf("top %d", p.top)
	}
}
