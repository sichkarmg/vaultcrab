// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The first key after the lock timeout (here after a "sleep" of 9 h with
// the default 8 h) locks the vault and opens the unlock dialog; the key
// itself does nothing else.
func TestKeyAfterLockTimeout(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	cfg := config.Default()
	svc := core.New(core.Options{Config: &cfg, ConfigPoll: -1, Now: clock,
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
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	mu.Lock()
	now = now.Add(4 * time.Hour)
	mu.Unlock()
	keys(m, "down")
	if m.modal != nil || svc.Status().State != core.Online {
		t.Fatal("locked within the timeout")
	}
	cur := m.list.cursor
	mu.Lock()
	now = now.Add(9 * time.Hour)
	mu.Unlock()
	keys(m, "down")
	if _, ok := m.modal.(*unlockModal); !ok || svc.Status().State != core.Locked {
		t.Fatalf("modal %T, state %v", m.modal, svc.Status().State)
	}
	if m.list.cursor != cur {
		t.Fatal("the key moved the cursor")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "locked by Vaultcrab after 9h") {
		t.Fatalf("no reason in the dialog:\n%s", v)
	}
}
