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

func TestWidgetColumn(t *testing.T) {
	svc := core.New(core.Options{
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Widgets: []core.WidgetSpec{
			{Title: "Demo", Command: []string{"sh", "-c", "printf '\\033[32mgreen-out\\033[0m\\nsecond line\\n'"}, Interval: time.Second},
			{Title: "Broken", Command: []string{"no-such-widget-cmd"}, Interval: time.Second},
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true,
		Widgets: true, WidgetsWidth: 30, WidgetsMin: 130})
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	m.View() // activates the widgets
	v := waitView(t, m, "green-out")
	for _, want := range []string{"Demo", "second line", "Broken", "no-such-widget-cmd", "Your own scripts here", "widgets.items in"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	checkFrame(t, m.View(), 160, 30)
	// clicks in the widget column do not select hosts
	m.Update(tea.MouseMsg{X: 150, Y: 6, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if m.modal != nil || len(m.tabs) != 0 {
		t.Error("click in the widget column acted on the list")
	}
	keys(m, "w")
	if strings.Contains(ansi.Strip(m.View()), "Your own scripts here") {
		t.Error("w did not hide the column")
	}
	keys(m, "w")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30}) // too narrow
	if strings.Contains(ansi.Strip(m.View()), "Your own scripts here") {
		t.Error("shown on a narrow screen")
	}
	checkFrame(t, m.View(), 120, 30)
}
