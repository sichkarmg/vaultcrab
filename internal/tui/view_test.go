// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

func viewModel(t *testing.T, path string) *Model {
	t.Helper()
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Filters: []core.Filter{{Name: "All"}, {Name: "SSH", Rule: must(core.ParseRule("scheme:ssh"))}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true,
		RestoreView: true, ViewPath: path})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		select {
		case <-svc.Events():
		case <-time.After(2 * time.Second):
			t.Fatal("not online")
		}
	}
	m.onEvent()
	return m
}

func TestFiltersKeptBetweenRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "view.json")
	m := viewModel(t, path)
	keys(m, "tab", "g", "o") // SSH filter, by collections, org: personal
	typeText(m, "/alp")
	keys(m, "enter")
	if m.list.org != core.OrgPersonal || m.list.grouping != core.ByCollection {
		t.Fatalf("org %q grouping %v", m.list.org, m.list.grouping)
	}
	sel := m.list.selected
	if err := m.SaveView(); err != nil {
		t.Fatal(err)
	}

	m2 := viewModel(t, path)
	l := &m2.list
	if l.filterIdx != 1 || l.grouping != core.ByCollection || l.org != core.OrgPersonal || l.search.Value() != "alp" {
		t.Fatalf("restored filter %d grouping %v org %q query %q", l.filterIdx, l.grouping, l.org, l.search.Value())
	}
	if l.selected != sel || len(l.rows) == 0 {
		t.Fatalf("selected %q, want %q (%d rows)", l.selected, sel, len(l.rows))
	}
	checkFrame(t, m2.View(), 120, 30)
}
