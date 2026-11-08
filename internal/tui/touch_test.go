// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/keystore"
	"github.com/sichkarmg/vaultcrab/internal/quickunlock"
)

type fakeTouch struct{ answer error }

func (fakeTouch) Available() error                              { return nil }
func (fakeTouch) DomainState() ([]byte, error)                  { return []byte("fingers"), nil }
func (f *fakeTouch) Authenticate(context.Context, string) error { return f.answer }

var errTouchCanceled = errors.New("Touch ID was canceled")

// The unlock dialog asks for Touch ID at once; canceled, it falls back to
// the PIN; accepted, the vault is unlocked.
func TestUnlockTouchIDThenPIN(t *testing.T) {
	cfg := config.Default()
	cfg.Unlock.LockTimeout, cfg.Unlock.PIN, cfg.Unlock.TouchID = "never", true, true
	ft := &fakeTouch{}
	svc := core.New(core.Options{Config: &cfg, ConfigPoll: -1, Keystore: &keystore.Memory{}, Biometric: ft,
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
	if err := svc.EnableTouchID(ctx, "", []byte("master")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPIN(ctx, "", []byte("482913"), []byte("master")); err != nil {
		t.Fatal(err)
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Icons: testIcons(false), ShowTree: true, Appearance: cfg.Appearance()})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	// canceled
	ft.answer = errTouchCanceled
	svc.LockAll(ctx, nil)
	m.onEvent()
	runUpdate(m, nil)
	u, ok := m.modal.(*unlockModal)
	if !ok || u.touch || !u.pin || !strings.Contains(u.err, "canceled") {
		t.Fatalf("after cancel: modal %T pin %v err %q", m.modal, ok && u.pin, u.err)
	}
	m.modal, m.unlockOffered = nil, false

	// accepted
	ft.answer = nil
	m.onEvent()
	runUpdate(m, nil)
	if m.modal != nil || svc.Status().State == core.Locked {
		t.Fatalf("not unlocked: %T %v", m.modal, svc.Status().State)
	}
}

// runUpdate sends msg (nil: a no-op key) and runs the commands it returns
// until none is left (the spinner ticks are dropped).
func runUpdate(m *Model, msg tea.Msg) {
	if msg == nil {
		msg = tea.FocusMsg{}
	}
	_, cmd := m.Update(msg)
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		r := c()
		switch r := r.(type) {
		case tea.BatchMsg:
			queue = append(queue, r...)
		case spinTickMsg, nil:
		default:
			_, next := m.Update(r)
			queue = append(queue, next)
		}
	}
}
