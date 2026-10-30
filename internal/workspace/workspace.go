// SPDX-License-Identifier: MIT

// Package workspace models a tab: a layout of panes, each showing a session.
// It keeps PTY sizes in sync with pane rectangles and aggregates the state of
// the panes. No UI code.
package workspace

import (
	"github.com/sichkarmg/vaultcrab/internal/layout"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

// HeaderRows is the height of a pane title line in multi-pane tabs.
const HeaderRows = 1

// Tab is a set of panes laid out in a tree.
type Tab struct {
	Layout  *layout.Layout
	panes   map[layout.PaneID]*session.Session
	focused layout.PaneID
	zoomed  bool

	w, h int
	geo  layout.Geometry
}

// New creates a tab with one pane showing s.
func New(s *session.Session) *Tab {
	l, id := layout.New()
	t := &Tab{Layout: l, panes: map[layout.PaneID]*session.Session{id: s}, focused: id}
	return t
}

// Focused returns the focused pane.
func (t *Tab) Focused() layout.PaneID { return t.focused }

// FocusedSession returns the session of the focused pane.
func (t *Tab) FocusedSession() *session.Session { return t.panes[t.focused] }

// Session returns the session of a pane.
func (t *Tab) Session(id layout.PaneID) *session.Session { return t.panes[id] }

// Panes returns pane IDs in layout order.
func (t *Tab) Panes() []layout.PaneID { return t.Layout.Panes() }

// Sessions returns sessions in layout order.
func (t *Tab) Sessions() []*session.Session {
	var out []*session.Session
	for _, id := range t.Panes() {
		out = append(out, t.panes[id])
	}
	return out
}

// Len returns the number of panes.
func (t *Tab) Len() int { return len(t.panes) }

// Zoomed reports whether the focused pane fills the tab.
func (t *Tab) Zoomed() bool { return t.zoomed && len(t.panes) > 1 }

// ToggleZoom zooms/unzooms the focused pane.
func (t *Tab) ToggleZoom() {
	t.zoomed = !t.zoomed
	t.relayout()
}

// Focus focuses pane id (unzooming if another pane is chosen).
func (t *Tab) Focus(id layout.PaneID) bool {
	if _, ok := t.panes[id]; !ok || id == t.focused {
		return false
	}
	t.focused = id
	if t.zoomed {
		t.zoomed = false
		t.relayout()
	}
	return true
}

// FocusDir moves the focus to the neighbor on side.
func (t *Tab) FocusDir(side layout.Side) bool {
	g := t.Layout.Rects(t.w, t.h)
	if n := g.Neighbor(t.focused, side); n != 0 {
		return t.Focus(n)
	}
	return false
}

// Split splits the focused pane, shows s in the new pane and focuses it.
func (t *Tab) Split(dir layout.Dir, s *session.Session) layout.PaneID {
	id := t.Layout.Split(t.focused, dir)
	t.panes[id] = s
	t.focused = id
	t.zoomed = false
	t.relayout()
	return id
}

// Take removes a pane without closing its session and returns the session.
// last reports that it was the only pane (the tab is then empty and must be
// discarded; the layout keeps its single leaf).
func (t *Tab) Take(id layout.PaneID) (s *session.Session, last bool) {
	s, ok := t.panes[id]
	if !ok {
		return nil, false
	}
	if len(t.panes) == 1 {
		delete(t.panes, id)
		return s, true
	}
	order := t.Panes()
	t.Layout.Close(id)
	delete(t.panes, id)
	if t.focused == id {
		// Focus the previous pane in layout order (or the next one).
		for i, p := range order {
			if p == id {
				if i > 0 {
					t.focused = order[i-1]
				} else {
					t.focused = order[i+1]
				}
			}
		}
	}
	if len(t.panes) == 1 {
		t.zoomed = false
	}
	t.relayout()
	return s, false
}

// SetSize lays the tab out in w×h cells and resizes the sessions' PTYs.
func (t *Tab) SetSize(w, h int) {
	if w == t.w && h == t.h {
		return
	}
	t.w, t.h = w, h
	t.relayout()
}

// Size returns the tab size.
func (t *Tab) Size() (int, int) { return t.w, t.h }

func (t *Tab) relayout() {
	if t.Zoomed() {
		t.geo = layout.Geometry{Panes: map[layout.PaneID]layout.Rect{t.focused: {X: 0, Y: 0, W: t.w, H: t.h}}}
	} else {
		t.geo = t.Layout.Rects(t.w, t.h)
	}
	for id, s := range t.panes {
		if r, ok := t.TermRect(id); ok {
			s.Resize(r.W, r.H)
		}
	}
}

// Geometry returns the current geometry (only the focused pane when zoomed).
func (t *Tab) Geometry() layout.Geometry { return t.geo }

// Headers reports whether panes show a title line.
func (t *Tab) Headers() bool { return len(t.panes) > 1 && !t.Zoomed() }

// TermRect returns the terminal area of a pane (pane minus its header).
func (t *Tab) TermRect(id layout.PaneID) (layout.Rect, bool) {
	r, ok := t.geo.Panes[id]
	if !ok {
		return r, false
	}
	if t.Headers() && r.H > HeaderRows {
		r.Y += HeaderRows
		r.H -= HeaderRows
	}
	return r, true
}

// DragBorder moves a separator to pos (x or y within the tab).
func (t *Tab) DragBorder(split *layout.Node, pos int) {
	if t.Zoomed() {
		return
	}
	t.Layout.SetBorder(t.geo, split, pos)
	t.relayout()
}

// ResizeFocused grows the focused pane towards side by delta cells.
func (t *Tab) ResizeFocused(side layout.Side, delta int) bool {
	if t.Zoomed() {
		return false
	}
	if t.Layout.Resize(t.geo, t.focused, side, delta) {
		t.relayout()
		return true
	}
	return false
}

// State aggregates the pane states: Disconnected if any pane dropped,
// otherwise Connecting/Connected/Idle; cleanly finished panes count as idle.
func (t *Tab) State() session.State {
	agg := session.Idle
	for _, s := range t.panes {
		st, _ := s.State()
		switch st {
		case session.Disconnected:
			if s.Exited() { // finished cleanly (e.g. a script): not an error
				continue
			}
			return session.Disconnected
		case session.Connecting:
			agg = session.Connecting
		case session.Connected:
			if agg != session.Connecting {
				agg = session.Connected
			}
		}
	}
	return agg
}

// Live returns the number of connected or connecting panes.
func (t *Tab) Live() int {
	n := 0
	for _, s := range t.panes {
		if st, _ := s.State(); st == session.Connected || st == session.Connecting {
			n++
		}
	}
	return n
}
