// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/biometric"
)

// fakeTouch is Touch ID: the enrolled fingers, availability and the answer
// to the next prompt.
type fakeTouch struct {
	mu      sync.Mutex
	domain  string
	avail   error
	answer  error
	prompts []string
}

func (f *fakeTouch) Available() error { f.mu.Lock(); defer f.mu.Unlock(); return f.avail }
func (f *fakeTouch) DomainState() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []byte(f.domain), f.avail
}
func (f *fakeTouch) Authenticate(_ context.Context, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, reason)
	return f.answer
}

func newTouchTest(t *testing.T) (*pinTest, *fakeTouch) {
	t.Helper()
	p := newPINTest(t)
	ft := &fakeTouch{domain: "fingers-1"}
	p.s.opts.Biometric = ft
	u := p.s.Config().Unlock
	u.TouchID = true
	p.s.SetLockPolicy(u)
	return p, ft
}

// Touch ID first, then the PIN, then the master password.
func TestTouchIDUnlock(t *testing.T) {
	p, ft := newTouchTest(t)
	if err := p.s.EnableTouchID(p.ctx, "", []byte("wrong")); err == nil {
		t.Fatal("enabled with a wrong master password")
	}
	if err := p.s.EnableTouchID(p.ctx, "", []byte("master")); err != nil {
		t.Fatal(err)
	}
	if len(ft.prompts) != 1 {
		t.Fatalf("prompts %v", ft.prompts)
	}
	p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("master"))
	p.lock(t)
	if q := p.s.QuickUnlock(""); !q.TouchID || !q.PIN {
		t.Fatalf("quick %+v", q)
	}
	// canceled: still locked, the PIN still works
	ft.answer = biometric.ErrCanceled
	if err := p.s.UnlockWithTouchID(p.ctx, ""); !errors.Is(err, ErrTouchCanceled) {
		t.Fatalf("canceled: %v", err)
	}
	if p.s.Status().State != Locked || !p.s.QuickUnlock("").PIN {
		t.Fatal("cancel changed the state")
	}
	ft.answer = nil
	if err := p.s.UnlockWithTouchID(p.ctx, ""); err != nil {
		t.Fatal(err)
	}
	waitState(t, p.s, Online)
	if !strings.Contains(ft.prompts[len(ft.prompts)-1], "unlock the vault") {
		t.Fatalf("reason %q", ft.prompts[len(ft.prompts)-1])
	}
}

// A changed set of fingerprints deletes the Touch ID data; the PIN stays.
func TestTouchIDFingerprintsChanged(t *testing.T) {
	p, ft := newTouchTest(t)
	p.s.EnableTouchID(p.ctx, "", []byte("master"))
	p.s.SetPIN(p.ctx, "", []byte("482913"), []byte("master"))
	p.lock(t)
	ft.mu.Lock()
	ft.domain = "fingers-2"
	ft.mu.Unlock()
	if err := p.s.UnlockWithTouchID(p.ctx, ""); !errors.Is(err, ErrBiometryChanged) {
		t.Fatalf("changed fingers: %v", err)
	}
	q := p.s.QuickUnlock("")
	if q.TouchID || !q.PIN {
		t.Fatalf("quick %+v", q)
	}
	if _, ok := p.store.Items["touchid/default"]; ok {
		t.Fatal("Touch ID data kept")
	}
}

// Touch ID not available (no sensor, not macOS): not offered, with why.
func TestTouchIDUnavailable(t *testing.T) {
	p, ft := newTouchTest(t)
	p.s.EnableTouchID(p.ctx, "", []byte("master"))
	p.lock(t)
	ft.mu.Lock()
	ft.avail = biometric.ErrUnavailable
	ft.mu.Unlock()
	if q := p.s.QuickUnlock(""); q.TouchID || q.TouchWhy == "" {
		t.Fatalf("quick %+v", q)
	}
	if err := p.s.EnableTouchID(p.ctx, "", []byte("master")); !errors.Is(err, biometric.ErrUnavailable) {
		t.Fatalf("enable without a sensor: %v", err)
	}
}

// The master password renews the Touch ID copy; forget deletes it.
func TestTouchIDRenewAndForget(t *testing.T) {
	p, _ := newTouchTest(t)
	p.s.EnableTouchID(p.ctx, "", []byte("master"))
	p.lock(t)
	p.src.mu.Lock()
	p.src.password = "new master"
	p.src.mu.Unlock()
	if err := p.s.Unlock(p.ctx, []byte("new master")); err != nil {
		t.Fatal(err)
	}
	waitState(t, p.s, Online)
	p.lock(t)
	if err := p.s.UnlockWithTouchID(p.ctx, ""); err != nil {
		t.Fatalf("after renewal: %v", err)
	}
	waitState(t, p.s, Online)
	if err := p.s.ForgetPIN(); err != nil {
		t.Fatal(err)
	}
	if len(p.store.Items) != 0 {
		t.Fatalf("left %v", p.store.Items)
	}
}
