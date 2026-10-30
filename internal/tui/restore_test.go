// SPDX-License-Identifier: MIT

//go:build !windows

package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

func restoreModel(t *testing.T, path string) (*Model, *core.Service) {
	t.Helper()
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Local:   core.LocalOptions{Command: []string{"/bin/sh"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Shutdown)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActSplitRight: "|", core.ActList: "l"},
		Icons: testIcons(false), ShowTree: true, RestoreTabs: true, WorkspacePath: path})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.onEvent()
	return m, svc
}

func TestTabsRestoredNotConnected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m, _ := restoreModel(t, path)
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "enter")  // tab 1: alpha
	keys(m, "ctrl+b", "|", "l", "o", "c", "a", "l", "enter") // + local shell on the right
	keys(m, "ctrl+b", "l", "t")                              // tab 2: local shell
	if len(m.tabs) != 2 || m.tabs[0].ws.Len() != 2 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	m.setActive(1)
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}

	m2, svc2 := restoreModel(t, path)
	if len(m2.tabs) != 2 || m2.active != 1 {
		t.Fatalf("restored tabs %d active %d", len(m2.tabs), m2.active)
	}
	t1 := m2.tabs[0]
	if t1.ws.Len() != 2 || t1.ws.Sessions()[0].Host.Name != "alpha" || t1.ws.Sessions()[1].Host.Scheme != core.SchemeLocal {
		t.Fatalf("layout %d", t1.ws.Len())
	}
	for _, s := range svc2.Sessions() {
		if st, _ := s.State(); st != core.SessionIdle {
			t.Errorf("%s connected on start: %v", s.Host.Name, st)
		}
	}
	waitView(t, m2, "press Enter to connect")
	// Enter in the focused pane (the local shell) starts it
	keys(m2, "enter")
	deadline := time.Now().Add(3 * time.Second)
	for st, _ := t1.focused().State(); st == core.SessionIdle; st, _ = t1.focused().State() {
		if time.Now().After(deadline) {
			t.Fatal("Enter did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	checkFrame(t, m2.View(), 120, 30)
}

func TestQuitDialogSavesOrDiscardsTabs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	m, _ := restoreModel(t, path)
	keys(m, "t", "ctrl+b", "l", "q")
	q, ok := m.modal.(*quitModal)
	if !ok || q.focus != quitSave {
		t.Fatalf("quit dialog %T", m.modal)
	}
	checkFrame(t, m.View(), 120, 30)
	keys(m, "esc")
	if m.modal != nil || m.quitting {
		t.Fatal("Esc did not cancel")
	}

	// Enter: the default is to save
	keys(m, "q", "enter")
	if !m.quitting || m.discardTabs {
		t.Fatal("Enter did not quit with saving")
	}
	if err := m.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if st, _ := core.LoadWorkspace(path); len(st.Tabs) != 1 {
		t.Fatalf("saved tabs %d", len(st.Tabs))
	}

	// a click on "Quit without saving"
	m2, _ := restoreModel(t, path)
	if len(m2.tabs) != 1 {
		t.Fatalf("restored %d", len(m2.tabs))
	}
	keys(m2, "q")
	frame := strings.Split(m2.View(), "\n")
	q = m2.modal.(*quitModal)
	if !strings.Contains(frame[m2.dlgY+q.row], "Quit without saving") {
		t.Fatalf("buttons not at row %d:\n%s", q.row, strings.Join(frame, "\n"))
	}
	x := m2.dlgX + boxPadX + (q.spans[quitDiscard][0]+q.spans[quitDiscard][1])/2
	m2.Update(tea.MouseMsg{X: x, Y: m2.dlgY + q.row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !m2.quitting || !m2.discardTabs {
		t.Fatal("the click did not quit without saving")
	}
	if err := m2.SaveWorkspace(); err != nil {
		t.Fatal(err)
	}
	if st, _ := core.LoadWorkspace(path); len(st.Tabs) != 0 {
		t.Fatalf("tabs kept: %d", len(st.Tabs))
	}
}
