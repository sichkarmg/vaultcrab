// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/cache"
	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// Several vaults: the default vault (the bw: section of the config) and the
// vaults: of the config, each with its own bw data directory, bw serve,
// state and cache. Their snapshots are merged (merge.go); secrets, notes and
// edits go to the vault an item belongs to.

// VaultOptions configures one vault.
type VaultOptions struct {
	ID     string // "" for the default vault; a prefix of the IDs of its items otherwise
	Name   string // shown in the UI (unlock dialog, host card, folder tree)
	Server string // the server URL as configured (shown; `bw config server`)
	// Connect returns the source (e.g. starting bw serve) and a function
	// releasing it; Login the interactive `bw login` command (optional).
	Connect func(ctx context.Context) (Source, func(), error)
	Login   func() *exec.Cmd
	Cache   *cache.Cache // optional

	spec string // the config it was made from (VaultSpec.Key): a change reconnects it
}

// VaultEntry describes a vault for the UI.
type VaultEntry struct {
	ID, Name, Server string
	Email            string // of the logged in account (known once bw answered)
	ServerURL        string // as reported by bw
	VaultStatus
}

// Label is the name of the vault, or its server, or "Bitwarden".
func (v VaultEntry) Label() string {
	switch {
	case v.Name != "":
		return v.Name
	case v.Server != "":
		return strings.TrimPrefix(strings.TrimPrefix(v.Server, "https://"), "http://")
	case v.ServerURL != "":
		return strings.TrimPrefix(strings.TrimPrefix(v.ServerURL, "https://"), "http://")
	}
	return "Bitwarden"
}

// Where is "email · server" of the vault, as far as known.
func (v VaultEntry) Where() string {
	srv := v.ServerURL
	if srv == "" {
		srv = v.Server
	}
	if srv == "" {
		srv = "bitwarden.com"
	}
	srv = strings.TrimPrefix(strings.TrimPrefix(srv, "https://"), "http://")
	if v.Email == "" {
		return srv
	}
	return v.Email + " · " + srv
}

// vaultConn is the connection to one vault. Its state is guarded by
// Service.mu.
type vaultConn struct {
	s    *Service
	opts VaultOptions

	kick chan struct{} // check now (s, a wake-up); buffered

	src       Source
	release   func()
	cancelRun context.CancelFunc
	since     time.Time     // the current bw serve is up since
	retries   int           // checks since the server or bw serve became unreachable
	crashed   bool          // bw serve exited by itself and is being restarted
	changed   chan struct{} // closed (and replaced) on every change
	state     VaultState
	lastErr   error
	snap      model.Snapshot // as the vault has it (unprefixed IDs)
	fromCache bool
	syncErr   error // last sync with the server failed: working from bw's local copy
	account   string
	email     string
	serverURL string
}

func newVaultConn(s *Service, o VaultOptions) *vaultConn {
	return &vaultConn{s: s, opts: o, kick: make(chan struct{}, 1), changed: make(chan struct{})}
}

// status: with s.mu held.
func (v *vaultConn) status() VaultStatus {
	return VaultStatus{State: v.state, Err: v.lastErr, FromCache: v.fromCache, SyncedAt: v.snap.SyncedAt,
		Account: v.account, SyncErr: v.syncErr}
}

// info: with s.mu held.
func (v *vaultConn) info() VaultEntry {
	return VaultEntry{ID: v.opts.ID, Name: v.opts.Name, Server: v.opts.Server, Email: v.email,
		ServerURL: v.serverURL, VaultStatus: v.status()}
}

// set changes the state of the vault (fn runs with s.mu held), logs state
// changes, merges the snapshots again and signals the UI.
func (v *vaultConn) set(fn func()) {
	s := v.s
	s.mu.Lock()
	st, syncErr := v.state, v.syncErr
	fn()
	close(v.changed)
	v.changed = make(chan struct{})
	who := "vault"
	if len(s.vaults) > 1 {
		who = "vault " + v.info().Label()
	}
	if v.state != st {
		if v.lastErr != nil {
			logf("%s: %s -> %s: %v", who, st, v.state, v.lastErr)
		} else {
			logf("%s: %s -> %s", who, st, v.state)
		}
	}
	if v.syncErr != nil && (syncErr == nil || v.syncErr.Error() != syncErr.Error()) {
		logf("%s: sync with the server failed, using the local copy of bw: %v", who, v.syncErr)
	}
	s.remerge()
	up := v.state == Online && st != Online
	s.mu.Unlock()
	if up {
		s.kickAutoLock() // arm the auto-lock timer
	}
	s.emit()
}

func (v *vaultConn) loadCache() {
	if v.opts.Cache == nil {
		return
	}
	if snap, err := v.opts.Cache.Load(""); err == nil {
		v.set(func() { v.snap, v.fromCache, v.account = snap, true, snap.Account })
	}
}

func (v *vaultConn) startRun(ctx context.Context) {
	rctx, cancel := context.WithCancel(ctx)
	v.s.mu.Lock()
	v.cancelRun = cancel
	v.s.mu.Unlock()
	go v.run(rctx)
}

func (v *vaultConn) reconnect(ctx context.Context) {
	v.stop()
	v.set(func() { v.state, v.lastErr = Starting, nil })
	v.startRun(ctx)
}

// stop closes the connection on purpose: the session key of the source is
// forgotten (a later start is locked).
func (v *vaultConn) stop() {
	v.s.mu.Lock()
	cancel, release, src := v.cancelRun, v.release, v.src
	v.cancelRun, v.release, v.src = nil, nil, nil
	v.s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if w, ok := src.(interface{ WipeSession() }); ok {
		w.WipeSession()
	}
	if release != nil {
		release()
	}
}

func (v *vaultConn) loginCommand() *exec.Cmd {
	if v.opts.Login == nil {
		return nil
	}
	return v.opts.Login()
}

func (v *vaultConn) source() Source {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	return v.src
}

// kickRun makes the run loop check the vault now (s, a wake-up).
func (v *vaultConn) kickRun() {
	select {
	case v.kick <- struct{}{}:
	default:
	}
}

// backoff returns the delay before retry n (0, 1, ...): base doubled each
// time up to limit.
func backoff(base, limit time.Duration, n int) time.Duration {
	d := base
	for i := 0; i < n && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// run connects to the vault and keeps the connection: a bw serve that
// exited is started again (with the session key of the old one, so it is
// unlocked), a sync runs every SyncInterval, and while the server or bw
// serve is unreachable the check is retried with a growing delay. One
// timer, set to the next moment something is due: no periodic wake-ups
// in between.
func (v *vaultConn) run(ctx context.Context) {
	if v.opts.Connect == nil {
		return
	}
	o := v.s.opts
	for restarts := 0; ; restarts++ {
		src, release, err := v.opts.Connect(ctx)
		if ctx.Err() != nil {
			if err == nil && release != nil {
				release()
			}
			return
		}
		if err != nil {
			st := Offline
			if errors.Is(err, vault.ErrNotLoggedIn) {
				v.set(func() { v.state, v.lastErr = NeedLogin, err })
				return // L runs bw login, then Reconnect
			}
			wait := backoff(o.RestartDelay, o.RetryLimit, restarts)
			logf("%s: bw serve did not start: %v; next attempt in %s", v.who(), err, wait)
			v.set(func() { v.state, v.lastErr = st, err })
			if !v.sleep(ctx, wait) {
				return
			}
			continue
		}
		v.s.mu.Lock()
		v.src, v.release = src, release
		v.s.mu.Unlock()
		if v.serve(ctx, src) {
			return
		}
		// bw serve exited by itself
		reason := ""
		if r, ok := src.(interface{ ExitReason() string }); ok {
			reason = r.ExitReason()
		}
		v.s.mu.Lock()
		if v.src == src {
			v.src, v.release = nil, nil
		}
		v.s.mu.Unlock()
		if release != nil {
			release()
		}
		if reason == "" {
			reason = "no error output"
		}
		if time.Since(v.servedSince()) > o.RetryLimit {
			restarts = 0 // it ran a good while: not a crash loop
		}
		wait := time.Duration(0)
		if restarts > 0 {
			wait = backoff(o.RestartDelay, o.RetryLimit, restarts-1)
		}
		logf("%s: bw serve exited (%s); restarting it", v.who(), reason)
		v.set(func() {
			v.state, v.lastErr, v.crashed = Crashed, fmt.Errorf("%w: bw serve exited: %s", errServeDown, reason), true
		})
		if !v.sleep(ctx, wait) {
			return
		}
	}
}

// serve watches a connected source until ctx ends (true) or the bw serve
// process exits (false).
func (v *vaultConn) serve(ctx context.Context, src Source) bool {
	v.s.mu.Lock()
	v.since = time.Now()
	v.s.mu.Unlock()
	v.refresh(ctx)
	var died <-chan struct{}
	if d, ok := src.(interface{ Done() <-chan struct{} }); ok {
		died = d.Done()
	}
	t := time.NewTimer(v.nextCheck())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return true
		case <-died:
			return false
		case <-v.kick:
		case <-t.C:
		}
		v.refresh(ctx)
		t.Reset(v.nextCheck())
	}
}

func (v *vaultConn) servedSince() time.Time {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	return v.since
}

// sleep waits d (or a kick); false when ctx ended.
func (v *vaultConn) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
	case <-v.kick:
	}
	return true
}

// nextCheck is the delay to the next refresh: the sync interval, or the
// retry delay while bw serve or the server is unreachable.
func (v *vaultConn) nextCheck() time.Duration {
	o := v.s.opts
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	switch {
	case v.state == Offline || v.state == Online && v.syncErr != nil:
		d := backoff(o.RetryInterval, o.RetryLimit, v.retries)
		v.retries++
		return d
	}
	v.retries = 0
	return o.SyncInterval
}

func (v *vaultConn) who() string {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	if len(v.s.vaults) > 1 {
		return "vault " + v.info().Label()
	}
	return "vault"
}

// refresh checks the vault status and, if unlocked, syncs (when the server
// is reachable) and reloads the snapshot.
func (v *vaultConn) refresh(ctx context.Context) {
	src := v.source()
	if src == nil {
		return
	}
	st, err := src.Status(ctx)
	if err != nil {
		v.fail(err)
		return
	}
	v.set(func() { v.email, v.serverURL = st.UserEmail, st.ServerURL })
	switch st.Status {
	case vault.Unauthenticated:
		v.set(func() { v.state, v.lastErr = NeedLogin, nil })
		v.dropSealed("bw is logged out")
		return
	case vault.Locked:
		v.set(func() {
			v.setLocked()
			v.useCacheFor(st.Account())
		})
		return
	}
	v.set(func() { v.state, v.lastErr = Syncing, nil })
	// A sync while the server is unreachable would crash bw serve when its
	// access token has to be refreshed (vault/session.go): it is not tried
	// then. bw's local copy stays usable: unlock, list and passwords work
	// offline.
	var syncErr error
	if p, ok := src.(interface{ Reachable(context.Context) error }); ok {
		syncErr = p.Reachable(ctx)
	}
	if syncErr == nil {
		sctx, cancel := context.WithTimeout(ctx, v.s.opts.SyncTimeout)
		syncErr = src.Sync(sctx)
		cancel()
		if syncErr != nil && ctx.Err() == nil && !errors.Is(syncErr, vault.ErrLocked) {
			if _, err := src.Status(ctx); err != nil { // bw serve itself is gone
				v.fail(err)
				return
			}
		}
	}
	snap, err := src.Snapshot(ctx)
	if err != nil {
		v.fail(err)
		return
	}
	if v.opts.Cache != nil {
		_ = v.opts.Cache.Save(snap)
	}
	v.set(func() {
		if v.syncErr != nil && syncErr == nil {
			logf("%s: the server is reachable again, synced", v.who0())
		}
		if v.crashed {
			logf("%s: bw serve restarted unlocked", v.who0())
		}
		v.snap, v.fromCache, v.account = snap, false, snap.Account
		v.state, v.lastErr, v.syncErr, v.crashed = Online, nil, syncErr, false
	})
}

// setLocked moves the vault to Locked with the reason when vaultcrab did
// not lock it; with s.mu held.
func (v *vaultConn) setLocked() {
	switch {
	case v.crashed:
		// restarted without a session key that still opens it
		logf("%s: bw serve was restarted locked", v.who0())
		v.lastErr = errLockedAfterCrash
	case v.state == Online || v.state == Syncing:
		// not by vaultcrab: `bw lock`, or a vault timeout of bw itself (bw
		// serve 2026.10 has none)
		logf("%s: bw reports the vault locked (bw lock, or a vault timeout of bw)", v.who0())
		v.lastErr = errLockedByBW
	case v.state != Locked:
		v.lastErr = nil
	}
	v.state, v.crashed = Locked, false
}

// who0 is who with s.mu held.
func (v *vaultConn) who0() string {
	if len(v.s.vaults) > 1 {
		return "vault " + v.info().Label()
	}
	return "vault"
}

// useCacheFor replaces a cache of another account; called with s.mu held.
func (v *vaultConn) useCacheFor(account string) {
	if v.account == account || v.opts.Cache == nil {
		return
	}
	v.account = account
	if snap, err := v.opts.Cache.Load(account); err == nil {
		v.snap, v.fromCache = snap, true
	} else {
		v.snap, v.fromCache = model.Snapshot{}, false
	}
}

func (v *vaultConn) fail(err error) {
	v.set(func() {
		if errors.Is(err, vault.ErrLocked) {
			v.setLocked()
			return
		}
		v.lastErr = err
		switch {
		case errors.Is(err, vault.ErrUnavailable):
			if v.state != Crashed {
				v.state = Offline
			}
		default:
			if v.state == Syncing || v.state == Starting {
				v.state = Offline
			}
		}
	})
}

func (v *vaultConn) unlock(ctx context.Context, password []byte) error {
	src := v.source()
	if src == nil {
		return v.notOnline()
	}
	who := v.who()
	pw := slices.Clone(password) // for the PIN; src.Unlock wipes password
	defer secret.Zero(pw)
	if err := src.Unlock(ctx, password); err != nil {
		logf("%s: unlock failed: %v", who, err)
		v.set(func() { v.lastErr = err })
		return err
	}
	logf("%s: unlocked", who)
	v.s.touch() // the auto-lock counts from here
	v.sealAfterUnlock(pw)
	v.refresh(ctx)
	return nil
}

// lockNow locks the vault if it is unlocked: its state becomes Locked with
// reason right away (no secret is handed out any more), then bw is asked
// to lock (which also drops the session key).
func (v *vaultConn) lockNow(ctx context.Context, reason error) {
	src := v.source()
	if src == nil {
		return
	}
	locked := false
	v.set(func() {
		if v.state == Online || v.state == Syncing {
			v.state, v.lastErr, v.crashed, locked = Locked, reason, false, true
		}
	})
	if !locked {
		return
	}
	logf("%s: %v", v.who(), reason)
	if l, ok := src.(interface{ Lock(context.Context) error }); ok {
		if err := l.Lock(ctx); err != nil {
			logf("%s: bw lock: %v", v.who(), err)
		}
	} else if w, ok := src.(interface{ WipeSession() }); ok {
		w.WipeSession()
	}
}

// waitUp waits (up to d) while bw serve is being restarted.
func (v *vaultConn) waitUp(ctx context.Context, d time.Duration) {
	deadline := time.Now().Add(d)
	for {
		v.s.mu.Lock()
		st, ch := v.state, v.changed
		v.s.mu.Unlock()
		if st != Crashed && st != Starting || time.Now().After(deadline) {
			return
		}
		t := time.NewTimer(time.Until(deadline))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-ch:
		case <-t.C:
		}
		t.Stop()
	}
}

// notOnline is the error for an operation that needs the unlocked vault,
// saying why it is not available.
func (v *vaultConn) notOnline() error {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	switch v.state {
	case Locked:
		return ErrVaultLocked
	case NeedLogin:
		return ErrVaultNeedLogin
	case Crashed, Starting:
		return ErrServeRestarting
	}
	return ErrServeUnreachable
}

func (v *vaultConn) secret(ctx context.Context, itemID string) (secret.Secret, error) {
	v.waitUp(ctx, v.s.opts.RestartWait)
	src := v.source()
	v.s.mu.Lock()
	lockedByUs := v.state == Locked && errors.Is(v.lastErr, errAutoLocked)
	v.s.mu.Unlock()
	if src == nil || lockedByUs {
		return secret.Secret{}, v.notOnline()
	}
	sec, err := src.Secret(ctx, itemID)
	if err != nil {
		if errors.Is(err, vault.ErrLocked) || errors.Is(err, vault.ErrUnavailable) {
			v.fail(err)
			logf("%s: no credential: %v", v.who(), err)
			return secret.Secret{}, v.notOnline()
		}
		return secret.Secret{}, err
	}
	return sec, nil
}

// online reports whether items of the vault can be read and changed now.
func (v *vaultConn) editable() bool {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	return v.state == Online && v.syncErr == nil && !v.fromCache
}

// --- the vaults of the service ---

func (s *Service) vaultList() []*vaultConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.vaults)
}

// vault returns the vault with ID id ("" = default), or nil.
func (s *Service) vault(id string) *vaultConn {
	for _, v := range s.vaultList() {
		if v.opts.ID == id {
			return v
		}
	}
	return nil
}

// route returns the vault of a host (or folder, org, collection) ID of the
// merged snapshot and the ID within that vault.
func (s *Service) route(id string) (*vaultConn, string) {
	vs := s.vaultList()
	if vid, raw, ok := strings.Cut(id, "/"); ok {
		for _, v := range vs[1:] {
			if v.opts.ID == vid {
				return v, raw
			}
		}
	}
	return vs[0], id
}

// Vaults describes all vaults, the default one first.
func (s *Service) Vaults() []VaultEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]VaultEntry, len(s.vaults))
	for i, v := range s.vaults {
		out[i] = v.info()
	}
	return out
}

// LockedVault returns the first locked vault.
func (s *Service) LockedVault() (VaultEntry, bool) {
	for _, v := range s.Vaults() {
		if v.State == Locked {
			return v, true
		}
	}
	return VaultEntry{}, false
}

// VaultOf returns the vault of a host of the merged snapshot.
func (s *Service) VaultOf(hostID string) VaultEntry {
	v, _ := s.route(hostID)
	s.mu.Lock()
	defer s.mu.Unlock()
	return v.info()
}

// AddVault connects a further vault while running (Settings → Vaults, a
// config reload).
func (s *Service) AddVault(ctx context.Context, o VaultOptions) error {
	if o.ID == "" || s.vault(o.ID) != nil {
		return fmt.Errorf("vault %q exists", o.ID)
	}
	v := newVaultConn(s, o)
	s.mu.Lock()
	s.vaults = append(s.vaults, v)
	s.mu.Unlock()
	logf("vault %s: added", o.ID)
	v.loadCache()
	v.startRun(ctx)
	v.set(func() {})
	return nil
}

// RemoveVault disconnects a further vault (its bw data stays on disk).
func (s *Service) RemoveVault(id string) {
	v := s.vault(id)
	if v == nil || id == "" {
		return
	}
	v.stop()
	s.mu.Lock()
	s.vaults = slices.DeleteFunc(s.vaults, func(x *vaultConn) bool { return x == v })
	s.remerge()
	s.mu.Unlock()
	logf("vault %s: removed", id)
	s.emit()
}

// LoginVault returns the interactive `bw login` command of vault id.
func (s *Service) LoginVault(id string) *exec.Cmd {
	if v := s.vault(id); v != nil {
		return v.loginCommand()
	}
	return nil
}

// SyncVault checks vault id now: syncs it if it is up, retries it if it
// is unreachable; a running bw serve is not restarted.
func (s *Service) SyncVault(id string) {
	if v := s.vault(id); v != nil {
		v.kickRun()
	}
}

// ReconnectVault restarts the connection to vault id (after its login).
func (s *Service) ReconnectVault(ctx context.Context, id string) {
	if v := s.vault(id); v != nil {
		v.reconnect(ctx)
	}
}

// reconcileVaults connects the vaults added to the config and disconnects
// the removed ones (a changed vault is reconnected). Needs Options.NewVault
// and a started service.
func (s *Service) reconcileVaults(specs []config.VaultSpec) {
	s.mu.Lock()
	ctx := s.ctx
	cur := map[string]VaultOptions{}
	for _, v := range s.vaults[1:] {
		cur[v.opts.ID] = v.opts
	}
	s.mu.Unlock()
	if s.opts.NewVault == nil || ctx == nil {
		return
	}
	want := map[string]bool{}
	for _, sp := range specs {
		want[sp.ID] = true
		o := s.opts.NewVault(sp)
		if old, ok := cur[sp.ID]; ok {
			if old.Name == o.Name && old.Server == o.Server && s.specOf(sp.ID) == sp.Key() {
				continue
			}
			s.RemoveVault(sp.ID)
		}
		o.spec = sp.Key()
		_ = s.AddVault(ctx, o)
	}
	for id := range cur {
		if !want[id] {
			s.RemoveVault(id)
		}
	}
}

// specOf returns the config key of vault id (see VaultSpec.Key).
func (s *Service) specOf(id string) string {
	if v := s.vault(id); v != nil {
		return v.opts.spec
	}
	return ""
}
