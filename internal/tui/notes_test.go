// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// notesSrc is an editable fake vault with notes and item dates.
type notesSrc struct {
	editSrc
	notes map[string]string
	reads int
}

func (n *notesSrc) Snapshot(ctx context.Context) (core.Snapshot, error) {
	s, err := n.editSrc.Snapshot(ctx)
	for i := range s.Hosts {
		s.Hosts[i].Created = time.Date(2026, 1, 2, 3, 4, 0, 0, time.Local)
		s.Hosts[i].Revised = time.Date(2026, 10, 20, 10, 30, 0, 0, time.Local)
	}
	return s, err
}

func (n *notesSrc) Notes(_ context.Context, id string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reads++
	return n.notes[id], nil
}

// runNotes executes cmd like Bubble Tea, following only the notes messages.
func runNotes(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runNotes(m, c)
		}
	case notesWaitMsg, notesMsg:
		_, next := m.Update(msg)
		runNotes(m, next)
	}
}

func notesModel(t *testing.T, src *notesSrc) *Model {
	t.Helper()
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true, ShowNotes: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 34})
	return m
}

func TestDetailsNotesDatesButtons(t *testing.T) {
	long := "first line\n\tsecond \x1b[31mred\x1b[0m"
	for i := 3; i <= 60; i++ {
		long += fmt.Sprintf("\nnote-%d", i)
	}
	src := &notesSrc{editSrc: editSrc{updated: map[string]core.HostEdit{}}, notes: map[string]string{"1": long}}
	m := notesModel(t, src)
	typeText(m, "/alpha")
	keys(m, "enter")
	m.notes = noteState{} // the commands of the keys above were not run
	runNotes(m, m.maybeFetchNotes())
	v := ansi.Strip(m.View())
	for _, want := range []string{"alpha", "[edit] [copy]", "created     02.01.2026 03:04", "modified    20.10.2026 10:30",
		"notes", "first line", "second red", "of 60 · wheel: scroll"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "note-60") {
		t.Error("long notes not cut")
	}
	// the notes are below the password button (they load later)
	if strings.Index(v, "Copy password") > strings.Index(v, "first line") {
		t.Error("notes above the password button")
	}
	checkFrame(t, m.View(), 130, 34)
	if src.reads != 1 {
		t.Errorf("notes read %d times", src.reads)
	}
	// redraws do not read again
	m.Update(tickMsg{})
	if c := m.maybeFetchNotes(); c != nil || src.reads != 1 {
		t.Error("read again for the same item")
	}

	// clicks in the card do not select list rows; the wheel there scrolls the notes
	sel := m.list.selected
	cx := m.list.pwButtonX + 3
	for y := m.bodyRect().y + m.list.listTop + 2; y < m.bodyRect().y+m.list.listTop+12; y++ {
		m.Update(tea.MouseMsg{X: cx, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	}
	if m.list.selected != sel || m.modal != nil {
		t.Fatalf("a click in the card changed the selection to %q", m.list.selected)
	}
	for i := 0; i < 30; i++ {
		m.Update(tea.MouseMsg{X: cx, Y: 20, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "note-60") || strings.Contains(v, "first line") || m.list.selected != sel {
		t.Fatalf("wheel did not scroll the notes:\n%s", v)
	}
	checkFrame(t, m.View(), 130, 34)

	// the buttons opposite the name
	frame := strings.Split(ansi.Strip(m.View()), "\n")
	y := m.bodyRect().y + m.list.listTop
	if !strings.Contains(frame[y], "[edit] [copy]") {
		t.Fatalf("buttons not on row %d: %q", y, frame[y])
	}
	click := func(i int) {
		x := m.list.pwButtonX + m.list.cardBtns[i][0] + 1
		m.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	}
	click(0)
	if f, ok := m.modal.(*hostForm); !ok || f.id != "1" {
		t.Fatalf("[edit]: %T", m.modal)
	}
	m.modal = nil
	click(1)
	if f, ok := m.modal.(*hostForm); !ok || f.copyOf != "1" {
		t.Fatalf("[copy]: %T", m.modal)
	}
	m.modal = nil

	// notes turned off in the settings: neither shown nor read
	a := m.opt.Appearance
	a.ShowNotes = false
	m.applyAppearance(a)
	m.notes = noteState{}
	if c := m.maybeFetchNotes(); c != nil || strings.Contains(ansi.Strip(m.View()), "first line") {
		t.Fatal("notes read or shown while turned off")
	}
}

func TestEditNotes(t *testing.T) {
	src := &notesSrc{editSrc: editSrc{updated: map[string]core.HostEdit{}}, notes: map[string]string{"1": "old note"}}
	m := notesModel(t, src)
	typeText(m, "/alpha")
	keys(m, "enter")
	m.notes = noteState{}
	// e reads the notes into the form
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	f := m.modal.(*hostForm)
	if cmd == nil {
		t.Fatal("notes not requested")
	}
	m.Update(cmd())
	if !f.notesLoaded || f.notes.Value() != "old note" {
		t.Fatalf("notes %q loaded %v", f.notes.Value(), f.notesLoaded)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Notes") || !strings.Contains(v, "old note") {
		t.Fatalf("form:\n%s", v)
	}
	checkFrame(t, m.View(), 130, 34)
	// to the notes field; Enter adds a line there
	for f.focus != fNotes {
		keys(m, "tab")
	}
	keys(m, "end", "enter")
	typeText(m, "new line")
	if m.modal == nil {
		t.Fatal("Enter in the notes saved the form")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	runCmd(m, cmd)
	if m.modal != nil {
		t.Fatalf("not saved: %s", f.err)
	}
	if n := src.updated["1"].Notes; n == nil || *n != "old note\nnew line" {
		t.Fatalf("saved notes %v", n)
	}

	// unchanged notes are not sent (the vault keeps them)
	m.notes = noteState{id: "1", text: "old note"} // as the details panel has them
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	f = m.modal.(*hostForm)
	if !f.notesLoaded {
		t.Fatal("notes of the details panel not used")
	}
	enterAndRun(t, m)
	if src.updated["1"].Notes != nil {
		t.Fatal("unchanged notes sent")
	}
}
