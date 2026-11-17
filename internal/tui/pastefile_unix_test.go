// SPDX-License-Identifier: MIT

//go:build !windows

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The "To a file" page in a local shell: Tab completes the path from the
// home directory, Enter writes the text by a here-document, an existing
// file is replaced after a confirmation, Ctrl+O appends.
func TestPasteToFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "proj", "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "proj", "alpha.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Local:   core.LocalOptions{Command: []string{"/bin/sh"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Shutdown)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActLocal: "t"}, Icons: testIcons(false), ConfirmPaste: true})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	keys(m, "t")
	waitView(t, m, "$ ")

	text := "line one\nit's $HOME `x`\nVAULTCRAB_EOF\nстрока\n"
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: true})
	p, ok := m.modal.(*pasteModal)
	if !ok {
		t.Fatalf("modal %T", m.modal)
	}
	keys(m, "f")
	if p.page != pageFile {
		t.Fatal("no file page")
	}
	key := func(k tea.KeyType) {
		t.Helper()
		_, cmd := m.Update(tea.KeyMsg{Type: k})
		runCmd(m, cmd)
	}
	typeText(m, "pr")
	key(tea.KeyTab)
	if v := p.file.in.Value(); v != "proj/" {
		t.Fatalf("completed %q (%s)", v, p.file.err)
	}
	key(tea.KeyTab) // two candidates: their list
	if len(p.file.cands) != 2 || !strings.Contains(ansi.Strip(m.View()), "alpha.txt") {
		t.Fatalf("candidates %v", p.file.cands)
	}
	key(tea.KeyTab) // the next Tab takes the first
	if v := p.file.in.Value(); v != "proj/alpha.txt" {
		t.Fatalf("cycled %q", v)
	}
	checkFrame(t, m.View(), 120, 40)

	// the file exists: Enter asks, the next Enter replaces it
	key(tea.KeyEnter)
	if m.modal == nil || !p.file.confirm || !strings.Contains(p.file.warn, "exists") {
		t.Fatalf("no confirmation: %q %q", p.file.warn, p.file.err)
	}
	key(tea.KeyEnter)
	if m.modal != nil {
		t.Fatal("dialog still open")
	}
	file := filepath.Join(home, "proj", "alpha.txt")
	waitFile := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			b, _ := os.ReadFile(file)
			if string(b) == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("file %q, want %q\n%s", b, want, ansi.Strip(m.View()))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFile(text)

	// append
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("more"), Paste: true})
	if _, ok := m.modal.(*pasteModal); ok { // one line: pasted at once, no dialog
		t.Fatal("dialog for one line")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x\ny\n"), Paste: true})
	p = m.modal.(*pasteModal)
	keys(m, "f")
	typeText(m, "proj/alpha.txt")
	key(tea.KeyCtrlO)
	key(tea.KeyEnter)
	if m.modal != nil {
		t.Fatalf("append asked: %q %q", p.file.warn, p.file.err)
	}
	waitFile(text + "x\ny\n")

	// a directory is refused
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\nb\n"), Paste: true})
	p = m.modal.(*pasteModal)
	keys(m, "f")
	typeText(m, "proj/beta")
	key(tea.KeyEnter)
	if m.modal == nil || !strings.Contains(p.file.err, "directory") {
		t.Fatalf("directory: %q", p.file.err)
	}
	key(tea.KeyEsc)
	if p.page != pagePreview {
		t.Fatal("Esc did not go back")
	}
}
