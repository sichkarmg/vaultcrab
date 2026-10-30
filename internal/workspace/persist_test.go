// SPDX-License-Identifier: MIT

package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/layout"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

func names(t *Tab) string {
	var out []string
	for _, s := range t.Sessions() {
		out = append(out, s.Host.Name)
	}
	return strings.Join(out, ",")
}

// a | (b / c), focus on c, then saved, written, read and restored.
func TestSaveRestore(t *testing.T) {
	tab := New(newSess("a", false))
	tab.SetSize(80, 24)
	tab.Split(layout.Horizontal, newSess("b", false))
	tab.Split(layout.Vertical, newSess("c", false))
	tab.Layout.Root().Ratio = 0.3
	keepAll := func(s *session.Session) (PaneState, bool) { return PaneState{HostID: s.Host.Name}, true }
	ts, ok := tab.Save(keepAll)
	if !ok || len(ts.Panes) != 3 || ts.Focused != 2 {
		t.Fatalf("saved %+v", ts)
	}
	path := filepath.Join(t.TempDir(), "workspace.json")
	if err := SaveState(path, State{Tabs: []TabState{ts}, Active: 1}); err != nil {
		t.Fatal(err)
	}
	st, err := LoadState(path)
	if err != nil || len(st.Tabs) != 1 || st.Active != 1 {
		t.Fatalf("load %+v %v", st, err)
	}
	open := func(ps PaneState) *session.Session { return newSess(ps.HostID, false) }
	r := Restore(st.Tabs[0], open)
	if r == nil || names(r) != "a,b,c" || r.FocusedSession().Host.Name != "c" {
		t.Fatalf("restored %v", r)
	}
	if got := r.Layout.Root(); got.Dir != layout.Horizontal || got.Ratio != 0.3 || got.B.Dir != layout.Vertical {
		t.Errorf("layout %+v", got)
	}
	// a host that no longer exists is left out; its sibling takes its place
	r = Restore(st.Tabs[0], func(ps PaneState) *session.Session {
		if ps.HostID == "b" {
			return nil
		}
		return open(ps)
	})
	if r == nil || names(r) != "a,c" || r.Layout.Root().B != nil && !r.Layout.Root().B.IsLeaf() {
		t.Fatalf("pruned %v", names(r))
	}
	if Restore(st.Tabs[0], func(PaneState) *session.Session { return nil }) != nil {
		t.Error("tab without panes restored")
	}
	// panes that are not remembered (e.g. scripts) are left out when saving
	ts, ok = tab.Save(func(s *session.Session) (PaneState, bool) { return PaneState{HostID: s.Host.Name}, s.Host.Name != "a" })
	if !ok || len(ts.Panes) != 2 || ts.Layout.Leaves() != 2 {
		t.Errorf("save without a: %+v", ts)
	}
	if st, err := LoadState(filepath.Join(t.TempDir(), "none.json")); err != nil || len(st.Tabs) != 0 {
		t.Error("missing file")
	}
}

// Several copies of vaultcrab closed at once (the terminal sends SIGHUP to all)
// save to the same files: every save succeeds and the file holds one of the
// saved states, never a mix of them.
func TestConcurrentSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "view.json")
	var wg sync.WaitGroup
	errs := make(chan error, 8*50)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				v := ViewState{Query: fmt.Sprintf("copy %d save %d %s", g, i, strings.Repeat("x", 1000*g))}
				if err := SaveView(path, v); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	v, err := LoadView(path)
	if err != nil || !strings.HasPrefix(v.Query, "copy ") {
		t.Fatalf("saved state %q: %v", v.Query, err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}
