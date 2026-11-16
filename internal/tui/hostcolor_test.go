// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
)

// A host in a folder of ui.folder_colors has its name in the list, and the
// title of its tabs, in that color (on that color when active).
func TestHostColorLabels(t *testing.T) {
	prof := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prof) })
	cfg := config.Default()
	cfg.UI.FolderColors = map[string]string{"Lab": "orange"}
	svc := core.New(core.Options{Config: &cfg, ConfigPoll: -1,
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		select {
		case <-svc.Events():
		case <-time.After(2 * time.Second):
			t.Fatal("not online")
		}
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActList: "l"}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	snap := svc.Snapshot()
	alpha, vnc := snap.Hosts[0], snap.Hosts[1]
	if c := m.hostColor(&alpha); c != "208" {
		t.Fatalf("alpha: %q", c)
	}
	if c := m.hostColor(&vnc); c != "" {
		t.Fatalf("vnc-box: %q", c)
	}
	orange := "\x1b[38;5;208m"
	// the list: alpha (not under the cursor) in orange, vnc-box plain
	m.list.cursor = len(m.list.rows) - 1
	v := m.View()
	if !strings.Contains(v, orange+"alpha") {
		t.Fatalf("list row not colored:\n%q", v)
	}
	if strings.Contains(v, orange+"vnc-box") {
		t.Fatal("vnc-box colored")
	}

	openTabs(t, m, 2)
	waitAllTabs(t, m, core.SessionDisconnected)
	m.setActive(1)
	items := m.tabItems()
	if !strings.Contains(items[2].label, orange+"alpha") {
		t.Fatalf("inactive tab: %q", items[2].label)
	}
	if !strings.Contains(items[1].label, "48;5;208") || !strings.Contains(ansi.Strip(items[1].label), "alpha") {
		t.Fatalf("active tab: %q", items[1].label)
	}
}
