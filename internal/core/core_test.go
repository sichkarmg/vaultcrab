// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/cache"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

type fakeSource struct {
	mu       sync.Mutex
	status   vault.Status
	down     bool
	snap     model.Snapshot
	secrets  map[string]string
	syncErr  error // the server is unreachable, bw works from its local copy
	syncHang bool
	password string // "" = "master"
}

func (f *fakeSource) Status(context.Context) (vault.StatusInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return vault.StatusInfo{}, vault.ErrUnavailable
	}
	return vault.StatusInfo{Status: f.status, UserEmail: "u", ServerURL: "s"}, nil
}
func (f *fakeSource) Unlock(_ context.Context, pw []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := f.password
	if want == "" {
		want = "master"
	}
	ok := string(pw) == want
	for i := range pw {
		pw[i] = 0 // like vault.Client
	}
	if !ok {
		return errors.New("Invalid master password.")
	}
	f.status = vault.Unlocked
	return nil
}
func (f *fakeSource) Sync(ctx context.Context) error {
	f.mu.Lock()
	hang, err := f.syncHang, f.syncErr
	f.mu.Unlock()
	if hang {
		<-ctx.Done() // server packets dropped
		return ctx.Err()
	}
	return err
}
func (f *fakeSource) Snapshot(context.Context) (model.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return model.Snapshot{}, vault.ErrUnavailable
	}
	if f.status != vault.Unlocked {
		return model.Snapshot{}, vault.ErrLocked
	}
	s := f.snap
	s.Account = "u@s"
	s.SyncedAt = time.Now()
	return s, nil
}
func (f *fakeSource) Secret(_ context.Context, id string) (secret.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return secret.Secret{}, vault.ErrUnavailable
	}
	if f.status != vault.Unlocked {
		return secret.Secret{}, vault.ErrLocked
	}
	return secret.FromString(f.secrets[id]), nil
}

func waitState(t *testing.T, s *Service, want VaultState) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for s.Status().State != want {
		select {
		case <-s.Events():
		case <-deadline:
			t.Fatalf("state = %v, want %v", s.Status().State, want)
		}
	}
}

func TestLockedUnlockOnline(t *testing.T) {
	src := &fakeSource{status: vault.Locked, snap: model.Snapshot{Hosts: []model.Host{{ID: "h1", Name: "one"}}}, secrets: map[string]string{"h1": "pw"}}
	c := &cache.Cache{Dir: t.TempDir()}
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return src, nil, nil }, Cache: c})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Locked)
	if _, err := s.Secret(ctx, "h1"); !errors.Is(err, ErrNotOnline) {
		t.Fatalf("secret while locked: %v", err)
	}
	if err := s.Unlock(ctx, []byte("bad")); err == nil {
		t.Fatal("bad password accepted")
	}
	if err := s.Unlock(ctx, []byte("master")); err != nil {
		t.Fatal(err)
	}
	waitState(t, s, Online)
	if h, ok := s.Host("h1"); !ok || h.Name != "one" {
		t.Fatal("host missing")
	}
	sec, err := s.Secret(ctx, "h1")
	if err != nil || string(sec.Value) != "pw" {
		t.Fatal(err)
	}
	sec.Wipe()
	// cache was written
	if snap, err := c.Load("u@s"); err != nil || len(snap.Hosts) != 1 {
		t.Fatalf("cache: %v %v", snap, err)
	}
}

func TestOfflineFromCache(t *testing.T) {
	c := &cache.Cache{Dir: t.TempDir()}
	c.Save(model.Snapshot{Account: "u@s", Hosts: []model.Host{{ID: "h1", Name: "cached"}}})
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return nil, nil, errors.New("bw not found") }, Cache: c})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Offline)
	st := s.Status()
	if !st.FromCache || st.Err == nil {
		t.Fatalf("status %+v", st)
	}
	if h, ok := s.Host("h1"); !ok || h.Name != "cached" {
		t.Fatal("cached host missing")
	}
}

func TestGoesOfflineAndBack(t *testing.T) {
	src := &fakeSource{status: vault.Unlocked, snap: model.Snapshot{Hosts: []model.Host{{ID: "h1"}}}}
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	src.mu.Lock()
	src.down = true
	src.mu.Unlock()
	if _, err := s.Secret(ctx, "h1"); !errors.Is(err, ErrNotOnline) {
		t.Fatal(err)
	}
	waitState(t, s, Offline)
	if len(s.Snapshot().Hosts) != 1 {
		t.Fatal("snapshot must survive going offline")
	}
	src.mu.Lock()
	src.down = false
	src.mu.Unlock()
	s.Refresh(ctx)
	waitState(t, s, Online)
}

type dyingSource struct {
	*fakeSource
	done chan struct{}
}

func (d dyingSource) Done() <-chan struct{} { return d.done }

func (d dyingSource) ExitReason() string {
	return "FetchError: request to https://vw.test/identity/connect/token failed"
}

// A bw serve that does not answer (but did not exit) is checked again with
// a growing delay, and the vault comes back without a restart.
func TestUnreachableServeRecovers(t *testing.T) {
	src := &fakeSource{status: vault.Unlocked}
	var connects atomic.Int32
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { connects.Add(1); return src, nil, nil },
		RetryInterval: 20 * time.Millisecond, SyncInterval: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	src.mu.Lock()
	src.down = true
	src.mu.Unlock()
	waitState(t, s, Offline)
	src.mu.Lock()
	src.down = false
	src.mu.Unlock()
	waitState(t, s, Online)
	if n := connects.Load(); n != 1 {
		t.Fatalf("bw serve restarted %d times; it was alive", n-1)
	}
}

// With the server unreachable bw still unlocks and serves its local copy:
// the list and the passwords work, the status says so, and the sync is
// retried until the server is back.
func TestServerUnreachableUsesLocalCopy(t *testing.T) {
	src := &fakeSource{status: vault.Unlocked, syncHang: true,
		snap:    model.Snapshot{Hosts: []model.Host{{ID: "h", Name: "a", Scheme: model.SSH, Addr: "x"}}},
		secrets: map[string]string{"h": "pw"}}
	s := New(Options{
		Connect:       func(context.Context) (Source, func(), error) { return src, nil, nil },
		SyncTimeout:   50 * time.Millisecond,
		RetryInterval: 50 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	st := s.Status()
	if st.SyncErr == nil || len(s.Snapshot().Hosts) != 1 {
		t.Fatalf("status %+v", st)
	}
	sec, err := s.Secret(ctx, "h")
	if err != nil || string(sec.Value) != "pw" {
		t.Fatalf("secret from the local copy: %v", err)
	}
	src.mu.Lock()
	src.syncHang = false
	src.mu.Unlock()
	deadline := time.After(2 * time.Second)
	for s.Status().SyncErr != nil {
		select {
		case <-s.Events():
		case <-deadline:
			t.Fatal("sync not retried after the server came back")
		}
	}
}

// Newer bw refuses to start `bw serve` without a logged in account.
func TestServeNotLoggedIn(t *testing.T) {
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return nil, nil, vault.ErrNotLoggedIn }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, NeedLogin)
}
