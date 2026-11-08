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

type fakeTouch struct {
	answer error
	asked  int
}

func (*fakeTouch) Available() error             { return nil }
func (*fakeTouch) DomainState() ([]byte, error) { return []byte("fingers"), nil }
func (f *fakeTouch) Authenticate(context.Context, string) error {
	f.asked++
	return f.answer
}

var errTouchCanceled = errors.New("Touch ID was canceled")

// The unlock dialog asks for Touch ID at once; canceled, it falls back to
// the PIN; accepted, the vault is unlocked.
func TestUnlockTouchIDThenPIN(t *testing.T) {
	m, svc, ft, ctx := touchModel(t)

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

// A Touch ID prompt that timed out (the Mac was asleep) is asked again from
// the same dialog: Enter on the empty input, or Ctrl+T.
func TestUnlockTouchIDRetry(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyCtrlT}} {
		t.Run(key.String(), func(t *testing.T) {
			m, svc, ft, ctx := touchModel(t)
			ft.answer = core.ErrTouchTimeout
			svc.LockAll(ctx, nil)
			m.onEvent()
			runUpdate(m, nil)
			u, ok := m.modal.(*unlockModal)
			if !ok || ft.asked != 1 || !strings.Contains(u.err, "timed out") || !u.touchOK {
				t.Fatalf("after the timeout: modal %T asked %d err %q", m.modal, ft.asked, u.err)
			}
			if v := strings.Join(u.view(m, 80, 20), "\n"); !strings.Contains(v, "Enter: Touch ID") {
				t.Fatalf("no retry hint:\n%s", v)
			}
			ft.answer = nil
			runUpdate(m, key)
			if ft.asked != 2 || m.modal != nil || svc.Status().State == core.Locked {
				t.Fatalf("not retried: asked %d modal %T state %v", ft.asked, m.modal, svc.Status().State)
			}
		})
	}
}

// A dialog that opens on its own long after the last input (the auto-lock
// fired while the Mac slept) waits for Enter before asking for Touch ID;
// typing a password is not taken for it.
func TestUnlockTouchIDWaitsWhenAway(t *testing.T) {
	m, svc, ft, ctx := touchModel(t)
	m.lastInput = time.Now().Add(-10 * time.Minute)
	svc.LockAll(ctx, nil)
	m.onEvent()
	runUpdate(m, nil)
	u, ok := m.modal.(*unlockModal)
	if !ok || ft.asked != 0 || u.busy || !u.touchOK {
		t.Fatalf("asked while away: modal %T asked %d", m.modal, ft.asked)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // (its cursor blink would tick forever in runUpdate)
	if ft.asked != 0 {
		t.Fatal("a typed key asked for Touch ID")
	}
	u.in.SetValue("")
	runUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})
	if ft.asked != 1 || m.modal != nil || svc.Status().State == core.Locked {
		t.Fatalf("Enter did not unlock: asked %d modal %T", ft.asked, m.modal)
	}
}

func touchModel(t *testing.T) (*Model, *core.Service, *fakeTouch, context.Context) {
	t.Helper()
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
	ft.asked = 0 // EnableTouchID asked once
	return m, svc, ft, ctx
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
