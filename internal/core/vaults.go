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

	src       Source
	release   func()
	cancelRun context.CancelFunc
	state     VaultState
	lastErr   error
	snap      model.Snapshot // as the vault has it (unprefixed IDs)
	fromCache bool
	syncErr   error // last sync with the server failed: working from bw's local copy
	account   string
	email     string
	serverURL string
}

func newVaultConn(s *Service, o VaultOptions) *vaultConn { return &vaultConn{s: s, opts: o} }

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
	s.mu.Unlock()
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

func (v *vaultConn) stop() {
	v.s.mu.Lock()
	cancel, release := v.cancelRun, v.release
	v.cancelRun, v.release, v.src = nil, nil, nil
	v.s.mu.Unlock()
	if cancel != nil {
		cancel()
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

func (v *vaultConn) run(ctx context.Context) {
	o := v.s.opts
	if v.opts.Connect == nil {
		return
	}
	src, release, err := v.opts.Connect(ctx)
	if err != nil {
		if ctx.Err() == nil {
			st := Offline
			if errors.Is(err, vault.ErrNotLoggedIn) {
				st = NeedLogin // L runs bw login, then Reconnect
			}
			v.set(func() { v.state, v.lastErr = st, err })
		}
		return
	}
	v.s.mu.Lock()
	if ctx.Err() != nil {
		v.s.mu.Unlock()
		if release != nil {
			release()
		}
		return
	}
	v.src, v.release = src, release
	v.s.mu.Unlock()
	v.refresh(ctx)

	t := time.NewTicker(o.SyncInterval)
	defer t.Stop()
	health := time.NewTicker(o.HealthInterval)
	defer health.Stop()
	retry := time.NewTicker(o.RetryInterval)
	defer retry.Stop()
	var died <-chan struct{}
	if d, ok := src.(interface{ Done() <-chan struct{} }); ok {
		died = d.Done()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-died:
			died = nil
			v.fail(fmt.Errorf("%w: bw serve exited", vault.ErrUnavailable))
		case <-t.C:
			v.refresh(ctx)
		case <-retry.C:
			v.s.mu.Lock()
			again := v.state == Online && v.syncErr != nil
			v.s.mu.Unlock()
			if again {
				v.refresh(ctx) // the server may be back
			}
		case <-health.C:
			v.checkHealth(ctx)
		}
	}
}

// checkHealth detects a vanished/locked vault between syncs and recovers
// from transient outages.
func (v *vaultConn) checkHealth(ctx context.Context) {
	src := v.source()
	if src == nil {
		return
	}
	st, err := src.Status(ctx)
	v.s.mu.Lock()
	cur := v.state
	v.s.mu.Unlock()
	switch {
	case err != nil:
		v.fail(err)
	case cur == Offline || cur == Online && st.Status != vault.Unlocked:
		v.refresh(ctx)
	}
}

// refresh checks the vault status and, if unlocked, syncs and reloads the
// snapshot.
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
		return
	case vault.Locked:
		v.set(func() {
			v.state, v.lastErr = Locked, nil
			v.useCacheFor(st.Account())
		})
		return
	}
	v.set(func() { v.state = Syncing })
	// A failed sync (server unreachable) still leaves bw's local copy
	// usable: unlock, list and passwords work offline.
	sctx, cancel := context.WithTimeout(ctx, v.s.opts.SyncTimeout)
	syncErr := src.Sync(sctx)
	cancel()
	if syncErr != nil && ctx.Err() == nil && !errors.Is(syncErr, vault.ErrLocked) {
		if _, err := src.Status(ctx); err != nil { // bw serve itself is gone
			v.fail(err)
			return
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
		v.snap, v.fromCache, v.account = snap, false, snap.Account
		v.state, v.lastErr, v.syncErr = Online, nil, syncErr
	})
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
		v.lastErr = err
		switch {
		case errors.Is(err, vault.ErrLocked):
			v.state = Locked
		case errors.Is(err, vault.ErrUnavailable):
			v.state = Offline
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
		return ErrNotOnline
	}
	who := "vault"
	if len(v.s.vaultList()) > 1 {
		v.s.mu.Lock()
		who = "vault " + v.info().Label()
		v.s.mu.Unlock()
	}
	if err := src.Unlock(ctx, password); err != nil {
		logf("%s: unlock failed: %v", who, err)
		v.set(func() { v.lastErr = err })
		return err
	}
	logf("%s: unlocked", who)
	v.refresh(ctx)
	return nil
}

func (v *vaultConn) secret(ctx context.Context, itemID string) (secret.Secret, error) {
	src := v.source()
	if src == nil {
		return secret.Secret{}, ErrNotOnline
	}
	sec, err := src.Secret(ctx, itemID)
	if err != nil {
		if errors.Is(err, vault.ErrLocked) || errors.Is(err, vault.ErrUnavailable) {
			v.fail(err)
			return secret.Secret{}, ErrNotOnline
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
