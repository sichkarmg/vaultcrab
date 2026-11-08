// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// serveSim simulates bw serve processes: each Connect starts one; a crash
// ends it. The session key of an unlocked process survives the restart
// unless keep is false (like a session key that no longer opens the vault).
type serveSim struct {
	mu       sync.Mutex
	procs    []dyingSource
	keep     bool
	unlocked bool
	connects atomic.Int32
}

func (s *serveSim) connect(context.Context) (Source, func(), error) {
	s.connects.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	st := vault.Locked
	if s.unlocked && s.keep || len(s.procs) == 0 && s.unlocked {
		st = vault.Unlocked
	}
	p := dyingSource{&fakeSource{status: st, snap: model.Snapshot{Hosts: []model.Host{{ID: "h"}}}, secrets: map[string]string{"h": "pw"}}, make(chan struct{})}
	s.procs = append(s.procs, p)
	return p, nil, nil
}

func (s *serveSim) crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.procs[len(s.procs)-1].done)
}

func waitFor(t *testing.T, s *Service, cond func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !cond() {
		select {
		case <-s.Events():
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("condition not met; status %+v", s.Status())
		}
	}
}

// bw serve exits by itself (bw crashes when it cannot refresh its token
// while the server is unreachable): it is restarted with the session of the
// old one, the vault stays unlocked and no password is asked.
func TestServeCrashRestartsUnlocked(t *testing.T) {
	sim := &serveSim{keep: true, unlocked: true}
	s := New(Options{Connect: sim.connect})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	sim.crash()
	waitFor(t, s, func() bool { return sim.connects.Load() == 2 && s.Status().State == Online })
	sec, err := s.Secret(ctx, "h")
	if err != nil {
		t.Fatal(err)
	}
	sec.Wipe()
}

// Without a session that opens the vault the restarted bw serve is locked:
// the unlock dialog says why.
func TestServeCrashRestartsLocked(t *testing.T) {
	sim := &serveSim{keep: false, unlocked: true}
	s := New(Options{Connect: sim.connect})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	sim.crash()
	waitState(t, s, Locked)
	if st := s.Status(); !errors.Is(st.Err, errLockedAfterCrash) {
		t.Fatalf("reason = %v", st.Err)
	}
	if _, err := s.Secret(ctx, "h"); !errors.Is(err, ErrVaultLocked) || !errors.Is(err, ErrNotOnline) {
		t.Fatalf("secret: %v", err)
	}
}

// A connection attempt during the restart waits for it instead of failing.
func TestSecretWaitsForRestart(t *testing.T) {
	sim := &serveSim{keep: true, unlocked: true}
	gate := make(chan struct{})
	var first atomic.Bool
	first.Store(true)
	s := New(Options{Connect: func(ctx context.Context) (Source, func(), error) {
		if !first.Swap(false) {
			<-gate // the restart takes a while
		}
		return sim.connect(ctx)
	}, RestartWait: 5 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	sim.crash()
	waitState(t, s, Crashed)
	time.AfterFunc(100*time.Millisecond, func() { close(gate) })
	sec, err := s.Secret(ctx, "h")
	if err != nil {
		t.Fatal(err)
	}
	sec.Wipe()
}

// bw serve that keeps crashing right after the start is restarted with a
// growing delay, not in a busy loop.
func TestServeCrashLoopBacksOff(t *testing.T) {
	var connects atomic.Int32
	s := New(Options{Connect: func(context.Context) (Source, func(), error) {
		connects.Add(1)
		d := dyingSource{&fakeSource{status: vault.Unlocked}, make(chan struct{})}
		close(d.done)
		return d, nil, nil
	}, RestartDelay: 50 * time.Millisecond, RetryLimit: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	time.Sleep(400 * time.Millisecond)
	// immediate, 50, 100, 200 ms: about 4-5 starts, not hundreds
	if n := connects.Load(); n < 3 || n > 6 {
		t.Fatalf("connects in 400 ms = %d", n)
	}
	if st := s.Status(); st.State != Crashed || !errors.Is(st.Err, errServeDown) || !strings.Contains(st.Err.Error(), "identity/connect/token") {
		t.Fatalf("status %+v", st)
	}
}

// reachSource is a source whose server can be made unreachable.
type reachSource struct {
	*fakeSource
	unreachable atomic.Bool
	probes      atomic.Int32
	syncs       atomic.Int32
}

func (r *reachSource) Reachable(context.Context) error {
	r.probes.Add(1)
	if r.unreachable.Load() {
		return vault.ErrServerUnreachable
	}
	return nil
}

func (r *reachSource) Sync(ctx context.Context) error {
	r.syncs.Add(1)
	return r.fakeSource.Sync(ctx)
}

// While the server is unreachable no sync is sent to bw (it would crash bw
// serve with an expired token); the local copy is used, the check is
// retried, and the sync runs when the server is back.
func TestUnreachableServerSkipsSync(t *testing.T) {
	src := &reachSource{fakeSource: &fakeSource{status: vault.Unlocked, snap: model.Snapshot{Hosts: []model.Host{{ID: "h"}}}, secrets: map[string]string{"h": "pw"}}}
	src.unreachable.Store(true)
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return src, nil, nil },
		RetryInterval: 20 * time.Millisecond, RetryLimit: 40 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	if st := s.Status(); !errors.Is(st.SyncErr, vault.ErrServerUnreachable) {
		t.Fatalf("status %+v", st)
	}
	if n := src.syncs.Load(); n != 0 {
		t.Fatalf("%d syncs while the server was unreachable", n)
	}
	sec, err := s.Secret(ctx, "h")
	if err != nil {
		t.Fatalf("secret from the local copy: %v", err)
	}
	sec.Wipe()
	src.unreachable.Store(false)
	deadline := time.After(2 * time.Second)
	for s.Status().SyncErr != nil {
		select {
		case <-s.Events():
		case <-deadline:
			t.Fatal("not synced after the server came back")
		}
	}
	if src.syncs.Load() == 0 || src.probes.Load() < 2 {
		t.Fatalf("syncs %d probes %d", src.syncs.Load(), src.probes.Load())
	}
}

// s (Sync) checks again but never restarts a running bw serve: that would
// lock the vault.
func TestSyncDoesNotRestartServe(t *testing.T) {
	src := &fakeSource{status: vault.Unlocked}
	var connects atomic.Int32
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { connects.Add(1); return src, nil, nil },
		RetryInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	src.mu.Lock()
	src.down = true
	src.mu.Unlock()
	s.Refresh(ctx)
	waitState(t, s, Offline)
	src.mu.Lock()
	src.down = false
	src.mu.Unlock()
	s.Sync(ctx)
	waitState(t, s, Online)
	if n := connects.Load(); n != 1 {
		t.Fatalf("connects = %d", n)
	}
}

func TestBackoff(t *testing.T) {
	got := []time.Duration{}
	for n := range 7 {
		got = append(got, backoff(15*time.Second, 5*time.Minute, n))
	}
	want := []time.Duration{15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff = %v, want %v", got, want)
		}
	}
}

// The errors of an unavailable vault say why, and all are ErrNotOnline.
func TestNotOnlineReasons(t *testing.T) {
	for _, e := range []error{ErrVaultLocked, ErrVaultNeedLogin, ErrServeRestarting, ErrServeUnreachable} {
		if !errors.Is(e, ErrNotOnline) {
			t.Errorf("%v is not ErrNotOnline", e)
		}
	}
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return &fakeSource{status: vault.Locked}, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Locked)
	if _, err := s.Secret(ctx, "x"); err != ErrVaultLocked {
		t.Fatalf("secret: %v", err)
	}
}

// bw locking the vault by itself is reported as such.
func TestLockedByBW(t *testing.T) {
	src := &fakeSource{status: vault.Unlocked}
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	src.mu.Lock()
	src.status = vault.Locked
	src.mu.Unlock()
	s.Refresh(ctx)
	waitState(t, s, Locked)
	if st := s.Status(); !errors.Is(st.Err, errLockedByBW) {
		t.Fatalf("reason %v", st.Err)
	}
}

// The log says why the vault was unavailable (the bw serve crash, the
// restart) and holds no secret.
func TestCrashLogged(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Log = config.Log{Enabled: true, File: filepath.Join(dir, "vaultcrab.log")}
	sim := &serveSim{keep: true}
	s := New(Options{Config: &cfg, ConfigPoll: -1, Connect: sim.connect})
	t.Cleanup(func() { s.SetLogging(false) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Locked)
	if err := s.Unlock(ctx, []byte("master")); err != nil {
		t.Fatal(err)
	}
	sim.mu.Lock()
	sim.unlocked = true
	sim.mu.Unlock()
	sim.crash()
	waitFor(t, s, func() bool { return sim.connects.Load() == 2 && s.Status().State == Online })
	var log string
	for range 100 {
		lines, _ := s.LogTail(100)
		if log = strings.Join(lines, "\n"); strings.Contains(log, "restarted unlocked") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, want := range []string{"bw serve exited (FetchError", "restarting it", "bw serve restarted unlocked"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "master") {
		t.Fatalf("password in the log:\n%s", log)
	}
}
