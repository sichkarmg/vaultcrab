// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// Notes of the selected item are read from the vault when the selection
// rests for a moment, kept in memory for that item only and never cached:
// they may hold secrets.

const notesDelay = 200 * time.Millisecond

type notesWaitMsg struct{ id string }

type notesMsg struct {
	id    string
	notes string
	err   error
}

// noteState is what the details panel shows for notes.
type noteState struct {
	id      string    // item the notes are for (or are being read for)
	rev     time.Time // its revision then (a newer one is read again)
	text    string
	loading bool
	err     error
	top     int // first shown line (the wheel scrolls long notes)
}

// maybeFetchNotes starts reading the notes when another item is selected
// in the host list (nil when nothing is to be done).
func (m *Model) maybeFetchNotes() tea.Cmd {
	if m.active != 0 || m.modal != nil || !m.opt.ShowNotes || !m.svc.CanReadNotes() {
		return nil
	}
	r := m.list.current()
	if r == nil || r.Kind != core.HostRow {
		return nil
	}
	h := r.Host
	if h.ID == m.notes.id && h.Revised.Equal(m.notes.rev) {
		return nil
	}
	m.notes = noteState{id: h.ID, rev: h.Revised, loading: true}
	id := h.ID
	return tea.Tick(notesDelay, func(time.Time) tea.Msg { return notesWaitMsg{id: id} })
}

// notesWait reads the notes if the item is still selected.
func (m *Model) notesWait(msg notesWaitMsg) tea.Cmd {
	if msg.id != m.notes.id {
		return nil
	}
	return m.readNotes(msg.id)
}

func (m *Model) readNotes(id string) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		n, err := svc.Notes(c, id)
		return notesMsg{id: id, notes: n, err: err}
	}
}

func (m *Model) notesRead(msg notesMsg) {
	if msg.id != m.notes.id {
		return
	}
	m.notes.loading, m.notes.text, m.notes.err = false, msg.notes, msg.err
}

// notesLines renders the notes section at the bottom of the details panel
// in at most room rows; longer notes scroll with the mouse wheel (from
// m.notes.top). maxTop is the last first line scrolling reaches.
func (m *Model) notesLines(h *core.Host, w, room int) (out []string, maxTop int) {
	n := &m.notes
	if n.id != h.ID || !m.opt.ShowNotes {
		return nil, 0
	}
	head := " " + stDim.Render("notes")
	switch {
	case n.loading:
		return []string{"", head + stDim.Render(" "+m.g.ell)}, 0
	case n.err != nil || n.text == "":
		return nil, 0
	}
	var lines []string
	for _, l := range plainLines(n.text) {
		lines = append(lines, strings.Split(ansi.Wrap(l, max(1, w-3), ""), "\n")...) // by words
	}
	show := max(3, room-2) // a blank line and the heading first
	if len(lines) > show {
		show-- // the position line
		maxTop = len(lines) - show
	}
	n.top = max(0, min(n.top, maxTop))
	out = []string{"", head}
	if maxTop > 0 {
		out[1] += stDim.Render(fmt.Sprintf("  %d-%d of %d · wheel: scroll", n.top+1, n.top+show, len(lines)))
	}
	for _, l := range lines[n.top:min(len(lines), n.top+show)] {
		out = append(out, "  "+l)
	}
	return out, maxTop
}
