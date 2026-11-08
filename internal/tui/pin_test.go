// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/keystore"
	"github.com/sichkarmg/vaultcrab/internal/quickunlock"
)

// run sends k and runs the commands it returns (one level: the result of
// a background unlock).
func run(m *Model, k tea.KeyMsg) {
	_, cmd := m.Update(k)
	for cmd != nil {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				if r := c(); r != nil {
					if _, tick := r.(spinTickMsg); !tick {
						_, cmd = m.Update(r)
					}
				}
			}
			return
		}
		_, cmd = m.Update(msg)
		if _, ok := msg.(pinSetMsg); ok {
			return
		}
	}
}

func enter(m *Model) { run(m, tea.KeyMsg{Type: tea.KeyEnter}) }

// PIN unlock is turned on in Settings → Security: the PIN twice and the
// master password; then the unlock dialog asks for the PIN (Tab: the
// master password), counts a wrong one and unlocks with the right one.
func TestPINSettingsAndUnlock(t *testing.T) {
	cfg := config.Default()
	cfg.Unlock.LockTimeout = "never"
	store := &keystore.Memory{}
	svc := core.New(core.Options{Config: &cfg, ConfigPoll: -1, Keystore: store,
		PINKDF:  quickunlock.KDF{Time: 1, MemKiB: 64, Threads: 1},
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
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Icons: testIcons(false), ShowTree: true,
		Appearance: cfg.Appearance(), ConfigPath: filepath.Join(t.TempDir(), "config.yaml")})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	keys(m, ",", "tab", "tab") // Security
	s := m.modal.(*settingsModal)
	if s.page != pageSecurity {
		t.Fatalf("page %d", s.page)
	}
	keys(m, "down", "down", "down", "left") // PIN unlock: on (after Touch ID)
	p, ok := m.modal.(*pinModal)
	if !ok {
		t.Fatalf("modal %T:\n%s", m.modal, ansi.Strip(m.View()))
	}
	typeText(m, "482913")
	enter(m)
	typeText(m, "482914")
	enter(m)
	if p.err == "" || p.step != 1 {
		t.Fatalf("different PINs accepted: step %d", p.step)
	}
	typeText(m, "482913")
	enter(m)
	typeText(m, "482913")
	enter(m)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Master password of") {
		t.Fatalf("no master password step:\n%s", v)
	}
	typeText(m, "master")
	enter(m)
	if m.modal != s || !svc.HasPIN() || !m.opt.Appearance.PIN {
		t.Fatalf("modal %T, PIN %v, appearance %v, err %q", m.modal, svc.HasPIN(), m.opt.Appearance.PIN, p.err)
	}
	if b := string(must(os.ReadFile(m.opt.ConfigPath))); !strings.Contains(b, "pin: true") || strings.Contains(b, "482913") {
		t.Fatalf("config:\n%s", b)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "weaker than the master password") || !strings.Contains(v, "Wrong PINs allowed") {
		t.Fatalf("security page:\n%s", v)
	}
	keys(m, "esc")

	// locked: the dialog asks for the PIN
	svc.LockAll(ctx, nil)
	m.onEvent()
	u, ok := m.modal.(*unlockModal)
	if !ok || !u.pin {
		t.Fatalf("modal %T", m.modal)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Tab: master password") {
		t.Fatalf("unlock dialog:\n%s", v)
	}
	typeText(m, "000001")
	enter(m)
	if !strings.Contains(u.err, "4 attempt(s) left") || !u.pin {
		t.Fatalf("wrong PIN: %q", u.err)
	}
	run(m, tea.KeyMsg{Type: tea.KeyTab})
	if u.pin {
		t.Fatal("Tab did not switch to the master password")
	}
	run(m, tea.KeyMsg{Type: tea.KeyTab})
	typeText(m, "482913")
	enter(m)
	if m.modal != nil || svc.Status().State == core.Locked {
		t.Fatalf("not unlocked: modal %T, err %q", m.modal, u.err)
	}
}
