// SPDX-License-Identifier: MIT

//go:build !windows

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

func waitView(t *testing.T, m *Model, want string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		v := ansi.Strip(m.View())
		if strings.Contains(v, want) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q not shown:\n%s", want, v)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLocalShellTab(t *testing.T) {
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Local:   core.LocalOptions{Command: []string{"/bin/sh"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Shutdown)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActLocal: "t", core.ActList: "l", core.ActSplitRight: "|"},
		Icons: testIcons(false), CloseOnExit: true})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	keys(m, "t") // from the host list, works without the vault
	if len(m.tabs) != 1 || m.active != 1 || m.tabs[0].focused().Host.Scheme != core.SchemeLocal {
		t.Fatalf("tabs=%d active=%d", len(m.tabs), m.active)
	}
	waitView(t, m, "$ ")
	for _, r := range "echo local-$((6*7))" {
		keys(m, string(r))
	}
	keys(m, "enter")
	waitView(t, m, "local-42")
	// prefix + | offers the local shell first in the picker
	keys(m, "ctrl+b", "|", "enter")
	if m.tabs[0].ws.Len() != 2 {
		t.Fatalf("panes: %d", m.tabs[0].ws.Len())
	}
	checkFrame(t, m.View(), 100, 30)
	// a clean exit closes the pane (close_on_exit)
	for st, _ := m.tabs[0].focused().State(); st != core.SessionConnected; st, _ = m.tabs[0].focused().State() {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // the shell reads the terminal settings first
	for _, r := range "exit" {
		keys(m, string(r))
	}
	keys(m, "enter")
	deadline := time.Now().Add(5 * time.Second)
	for m.tabs[0].ws.Len() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("pane not closed after exit")
		}
		time.Sleep(20 * time.Millisecond)
		m.onEvent()
	}
}

// Output in a tab that is not shown marks it (the icon turns blue) until
// the tab is shown; a shell redrawing its prompt after a resize does not.
func TestTabActivity(t *testing.T) {
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Local:   core.LocalOptions{Command: []string{"/bin/sh"}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Shutdown)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActLocal: "t", core.ActList: "l"}, Icons: testIcons(false)})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	keys(m, "t")
	waitView(t, m, "$ ")
	tb := m.tabs[0]
	time.Sleep(300 * time.Millisecond) // the shell reads the terminal settings first
	for _, r := range "sleep 1; echo act-$((6*7))" {
		keys(m, string(r))
	}
	keys(m, "enter")
	m.View()
	keys(m, "ctrl+b", "l") // to the host list: the tab is hidden
	m.View()
	if tb.activity(false) {
		t.Fatal("marked before new output")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(tb.focused().Term.PlainText(), "act-42") {
		if time.Now().After(deadline) {
			t.Fatal("no output")
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.View()
	if !tb.activity(false) {
		t.Error("output in the hidden tab not marked")
	}
	m.active = 1 // shown: seen
	m.View()
	m.active = 0
	if tb.activity(false) {
		t.Error("still marked after the tab was shown")
	}
	// a resize: the prompt redraw is not activity
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	tb.focused().Term.WriteString("$ ")
	if tb.activity(false) {
		t.Error("output right after a resize marked")
	}
}
