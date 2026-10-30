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

func TestScriptsMenuAndDefault(t *testing.T) {
	rule, _ := core.ParseRule("scheme:ssh")
	var started []string
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Scripts: []core.Script{
			{Name: "hello", Rule: rule, Command: []string{"/bin/sh", "-c", "echo hello-{name}-{port}"}, Mode: core.ScriptTerminal},
			{Name: "gui", Command: []string{"some-gui", "{host}"}, Mode: core.ScriptExternal},
		},
		Start: func(_ context.Context, argv []string, _ bool, _ func(context.Context) (core.Secret, error)) error {
			started = argv
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Shutdown)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true, CloseOnExit: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	keys(m, "/", "a", "l", "p", "h", "a", "enter")
	waitView(t, m, "scripts [x]") // details panel lists the scripts
	keys(m, "x")
	if _, ok := m.modal.(*scriptMenu); !ok {
		t.Fatal("no script menu")
	}
	waitView(t, m, "Run for alpha")
	keys(m, "1") // hello: terminal script in a new tab
	if len(m.tabs) != 1 {
		t.Fatalf("tabs %d", len(m.tabs))
	}
	waitView(t, m, "hello-alpha-22")
	waitView(t, m, "press Enter to run again")
	m.onEvent() // a finished script keeps its tab (unlike close_on_exit shells)
	if len(m.tabs) != 1 {
		t.Fatal("script tab closed after exit")
	}
	keys(m, "enter") // runs again
	deadline := time.Now().Add(5 * time.Second)
	for m.tabs[0].focused().Attempts() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.tabs[0].focused().Attempts() != 2 {
		t.Error("Enter did not rerun the script")
	}
	// external script with placeholders: started in the background
	keys(m, "ctrl+b", "0", "x")
	cmd := m.modal.update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if cmd == nil {
		t.Fatal("external script returned no command")
	}
	m.Update(cmd())
	if len(started) != 2 || started[0] != "some-gui" || started[1] != "10.0.0.1" {
		t.Errorf("started %v", started)
	}
	if !strings.Contains(ansi.Strip(m.View()), "gui started for alpha") {
		t.Error("no confirmation toast")
	}
}
