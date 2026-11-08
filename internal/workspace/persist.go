// SPDX-License-Identifier: MIT

package workspace

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/sichkarmg/vaultcrab/internal/layout"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

// PaneState is what is remembered of a pane: which host it shows ("local"
// for the local shell). Never a secret or the terminal contents.
type PaneState struct {
	HostID string `json:"host"`
	// Jump: the pane connects through a jump host (names and addresses).
	Jump *JumpPane `json:"jump,omitempty"`
}

// JumpPane is how a pane connects through a jump host (core.JumpSpec).
type JumpPane struct {
	Via    string `json:"via"`
	Target string `json:"target,omitempty"`
	Item   string `json:"item,omitempty"`
	Cred   string `json:"cred,omitempty"` // a key reference, never a secret
}

// TabState is a saved tab: its layout and panes (in tree order).
type TabState struct {
	Layout  *layout.Shape `json:"layout"`
	Panes   []PaneState   `json:"panes"`
	Focused int           `json:"focused"` // index into Panes
	Zoomed  bool          `json:"zoomed,omitempty"`
	Title   string        `json:"title,omitempty"` // set by the user (rename)
}

// State is the saved set of tabs.
type State struct {
	Version int        `json:"version"`
	Tabs    []TabState `json:"tabs"`
	Active  int        `json:"active"` // 0 = host list, i = Tabs[i-1]
}

const stateVersion = 1

// Save returns the state of the tab; pane reports what to remember of a
// session (false = leave the pane out). ok is false if no pane is kept.
func (t *Tab) Save(pane func(*session.Session) (PaneState, bool)) (ts TabState, ok bool) {
	order := t.Panes()
	keep := make([]bool, len(order))
	for i, id := range order {
		ps, k := pane(t.panes[id])
		keep[i] = k
		if !k {
			continue
		}
		if id == t.focused {
			ts.Focused = len(ts.Panes)
		}
		ts.Panes = append(ts.Panes, ps)
	}
	ts.Layout = t.Layout.Shape().Prune(keep)
	ts.Zoomed = t.zoomed && len(ts.Panes) > 1
	return ts, ts.Layout != nil
}

// Restore rebuilds a tab; open returns the session of a pane (nil = the
// pane is left out, e.g. its host was deleted). It returns nil if no pane
// could be opened.
func Restore(ts TabState, open func(PaneState) *session.Session) *Tab {
	if ts.Layout == nil || ts.Layout.Leaves() != len(ts.Panes) {
		return nil
	}
	keep := make([]bool, len(ts.Panes))
	var sessions []*session.Session
	focus := 0
	for i, ps := range ts.Panes {
		if s := open(ps); s != nil {
			keep[i] = true
			if i == ts.Focused {
				focus = len(sessions)
			}
			sessions = append(sessions, s)
		}
	}
	shape := ts.Layout.Prune(keep)
	if shape == nil {
		return nil
	}
	l, ids := layout.FromShape(shape)
	t := &Tab{Layout: l, panes: map[layout.PaneID]*session.Session{}, focused: ids[focus]}
	for i, id := range ids {
		t.panes[id] = sessions[i]
	}
	t.zoomed = ts.Zoomed && len(ids) > 1
	return t
}

// SaveState writes the state atomically (0600).
func SaveState(path string, st State) error {
	st.Version = stateVersion
	return writeJSON(path, st)
}

// LoadState reads a saved state; a missing file is an empty state.
func LoadState(path string) (State, error) {
	var st State
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil || st.Version != stateVersion {
		return State{}, errors.New("unsupported workspace state")
	}
	return st, nil
}
