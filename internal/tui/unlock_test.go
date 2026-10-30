// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// lockedSrc is locked until Unlock, which waits for release.
type lockedSrc struct {
	fakeSrc
	release  chan struct{}
	unlocked bool
}

func (l *lockedSrc) Status(ctx context.Context) (core.VaultInfo, error) {
	st := core.VaultLocked
	if l.unlocked {
		st = core.VaultUnlocked
	}
	return core.VaultInfo{Status: st, UserEmail: "t", ServerURL: "s"}, nil
}

func (l *lockedSrc) Unlock(context.Context, []byte) error {
	<-l.release
	l.unlocked = true
	return nil
}

// After Enter the password field disappears and a spinner shows progress
// until the vault is unlocked and synced.
func TestUnlockShowsProgress(t *testing.T) {
	src := &lockedSrc{release: make(chan struct{})}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Locked {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false)})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.onEvent()
	if !strings.Contains(ansi.Strip(m.View()), "master password") {
		t.Fatal("no unlock dialog")
	}
	typeText(m, "pw")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("no unlock command")
	}
	v := ansi.Strip(m.View())
	if strings.Contains(v, "master password") || strings.Contains(v, "> ") && strings.Contains(v, "Enter: unlock") {
		t.Errorf("input still shown:\n%s", v)
	}
	if !strings.Contains(v, "Unlocking the vault") {
		t.Errorf("no progress:\n%s", v)
	}
	first := m.View()
	m.Update(spinTickMsg{})
	if m.View() == first {
		t.Error("spinner does not move")
	}
	close(src.release)
}
