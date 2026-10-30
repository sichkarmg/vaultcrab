// SPDX-License-Identifier: MIT

// Package core is the application service used by the TUI: it owns the vault
// connection state, the metadata snapshot (live or cached) and the sessions.
package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/cache"
	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/hostlist"
	"github.com/sichkarmg/vaultcrab/internal/launchers"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/scripts"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
	"github.com/sichkarmg/vaultcrab/internal/vault"
	"github.com/sichkarmg/vaultcrab/internal/widget"
)

// Source is the vault provider: the extension point for credential stores.
// The Bitwarden/Vaultwarden provider (vault.Client over `bw serve`) is the
// one implemented; another store (for example gopass) plugs in here by
// implementing Source and being returned from Options.Connect. A provider
// hands out metadata in Snapshot and a secret only from Secret, on each
// connection attempt. See docs/extending.md.
type Source interface {
	Status(ctx context.Context) (vault.StatusInfo, error)
	Unlock(ctx context.Context, password []byte) error
	Sync(ctx context.Context) error
	Snapshot(ctx context.Context) (model.Snapshot, error)
	Secret(ctx context.Context, itemID string) (secret.Secret, error)
}

// VaultState is the connectivity state shown in the status bar.
type VaultState int

const (
	// Starting: bw serve is being launched / first contact pending.
	Starting VaultState = iota
	// Offline: bw serve unreachable; host list comes from cache (if any).
	Offline
	// NeedLogin: bw has no logged in account (run `bw login`).
	NeedLogin
	// Locked: vault must be unlocked with the master password.
	Locked
	// Syncing: unlocked, sync in progress.
	Syncing
	// Online: unlocked and snapshot is fresh.
	Online
)

func (s VaultState) String() string {
	return [...]string{"starting", "offline", "need login", "locked", "syncing", "online"}[s]
}

// Options configures a Service.
type Options struct {
	// Connect returns the vault source, e.g. starting bw serve, and a function
	// releasing it. Called in the background by Start/Reconnect; may be slow.
	Connect func(ctx context.Context) (Source, func(), error)
	// Login returns an interactive command that logs the bw CLI in (run by
	// the UI with the terminal handed over). Optional.
	Login func() *exec.Cmd
	// Filters are the configured filter tabs/badges.
	Filters []filter.Filter
	// Visibility restricts shown organizations/collections.
	Visibility hostlist.Visibility
	// Launch starts external programs for non-SSH hosts; secret fetches the
	// host credential if the program needs it. Optional.
	Launch func(ctx context.Context, h model.Host, secret func(context.Context) (secret.Secret, error)) error
	// VNC starts the VNC client for h with the credential sec (wiped by the
	// caller), through the tunnel from jump if not nil; the client is shown
	// as a session (a tab). Optional.
	VNC   func(ctx context.Context, h model.Host, sec *secret.Secret, jump func(context.Context) (*connect.Tunnel, error)) (session.Remote, error)
	Cache *cache.Cache // optional
	// SyncInterval is the period of background syncs (0 = 10 min).
	SyncInterval time.Duration
	// SyncTimeout bounds a sync with the server (0 = 20 s); after it the
	// local copy of bw is used.
	SyncTimeout time.Duration
	// RetryInterval is the sync period while the server is unreachable
	// (0 = 1 min).
	RetryInterval time.Duration
	// HealthInterval is the period of cheap status checks (0 = 30 s).
	HealthInterval time.Duration
	// SSH configures the built-in SSH client.
	SSH connect.SSHOptions
	// Start runs a detached program (external-mode scripts); secret fetches
	// the host credential if the program reads it from stdin. Optional.
	Start func(ctx context.Context, argv []string, pwStdin bool, secret func(context.Context) (secret.Secret, error)) error
	// Scripts are the user launch scripts for this OS.
	Scripts []scripts.Script
	// Widgets are the commands of the host list side column; WidgetExec runs
	// them (nil = as child processes).
	Widgets    []widget.Spec
	WidgetExec widget.Exec
	// Local configures the local shell (OpenLocal).
	Local connect.LocalOptions
	// Scrollback is the per-session scrollback limit in lines.
	Scrollback int

	// Config is the configuration loaded at start (its Path is watched
	// and re-read, see config.go). Optional.
	Config *config.Config
	// LoadConfig reads the configuration (config.Load by default; main
	// applies its command line flags).
	LoadConfig func(path string) (config.Config, error)
	// ApplyConfig applies a re-read configuration to what main built from
	// it (the external program launcher). Optional.
	ApplyConfig func(config.Config)
	// ConfigPoll is the period of the config file checks (0 = 5 s, < 0 =
	// off).
	ConfigPoll time.Duration
	// FS is where the external clients are looked for (the real file
	// system by default).
	FS launchers.FS
}

// Service is safe for concurrent use.
type Service struct {
	opts   Options
	events chan struct{}

	mu        sync.Mutex
	src       Source
	release   func()
	cancelRun context.CancelFunc
	state     VaultState
	lastErr   error
	snap      model.Snapshot
	fromCache bool
	syncErr   error // last sync with the server failed: working from bw's local copy
	account   string

	sessions sessions
	widgets  *widget.Runner

	conf    configState      // guarded by mu
	scripts []scripts.Script // guarded by mu: replaced on a config reload
}

// New creates a service. Call Start to begin.
func New(o Options) *Service {
	if o.SyncInterval == 0 {
		o.SyncInterval = 10 * time.Minute
	}
	if o.HealthInterval == 0 {
		o.HealthInterval = 30 * time.Second
	}
	if o.SyncTimeout == 0 {
		o.SyncTimeout = 20 * time.Second
	}
	if o.RetryInterval == 0 {
		o.RetryInterval = time.Minute
	}
	s := &Service{opts: o, events: make(chan struct{}, 1)}
	s.sessions.init(s)
	s.initConfig()
	s.widgets = widget.NewRunner(o.Widgets, s.emit, o.WidgetExec)
	return s
}

// Events signals that something changed (vault state, snapshot, session
// state or output). Signals are coalesced: consumers re-read what they need.
func (s *Service) Events() <-chan struct{} { return s.events }

func (s *Service) emit() {
	select {
	case s.events <- struct{}{}:
	default: // a signal is already pending
	}
}

// VaultStatus is a consistent view of the vault state.
type VaultStatus struct {
	State     VaultState
	Err       error // last error, if any
	FromCache bool  // snapshot comes from the on-disk cache
	SyncedAt  time.Time
	Account   string
	// SyncErr is set when the vault is usable but the last sync with the
	// server failed: bw works from its local encrypted copy (offline).
	SyncErr error
}

// Status returns the current vault status.
func (s *Service) Status() VaultStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return VaultStatus{State: s.state, Err: s.lastErr, FromCache: s.fromCache, SyncedAt: s.snap.SyncedAt,
		Account: s.account, SyncErr: s.syncErr}
}

// Snapshot returns the current metadata snapshot (shared; do not modify).
func (s *Service) Snapshot() model.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

// Host returns a host by ID from the current snapshot.
func (s *Service) Host(id string) (model.Host, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.snap.Hosts {
		if h.ID == id {
			return h, true
		}
	}
	return model.Host{}, false
}

func (s *Service) set(fn func()) {
	s.mu.Lock()
	fn()
	s.mu.Unlock()
	s.emit()
}

// Start loads the cache and connects to the vault in the background.
func (s *Service) Start(ctx context.Context) {
	s.widgets.Start(ctx)
	go s.watchConfig(ctx)
	if s.opts.Cache != nil {
		if snap, err := s.opts.Cache.Load(""); err == nil {
			s.set(func() { s.snap, s.fromCache, s.account = snap, true, snap.Account })
		}
	}
	s.startRun(ctx)
}

func (s *Service) startRun(ctx context.Context) {
	rctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancelRun = cancel
	s.mu.Unlock()
	go s.run(rctx)
}

// Reconnect releases the vault source (e.g. stops bw serve) and connects
// again; used after `bw login` or when bw serve died.
func (s *Service) Reconnect(ctx context.Context) {
	s.Stop()
	s.set(func() { s.state, s.lastErr = Starting, nil })
	s.startRun(ctx)
}

// Stop releases the vault source.
func (s *Service) Stop() {
	s.mu.Lock()
	cancel, release := s.cancelRun, s.release
	s.cancelRun, s.release, s.src = nil, nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if release != nil {
		release()
	}
}

// LoginCommand returns the interactive `bw login` command, or nil.
func (s *Service) LoginCommand() *exec.Cmd {
	if s.opts.Login == nil {
		return nil
	}
	return s.opts.Login()
}

func (s *Service) run(ctx context.Context) {
	src, release, err := s.opts.Connect(ctx)
	if err != nil {
		if ctx.Err() == nil {
			st := Offline
			if errors.Is(err, vault.ErrNotLoggedIn) {
				st = NeedLogin // L runs bw login, then Reconnect
			}
			s.set(func() { s.state, s.lastErr = st, err })
		}
		return
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		if release != nil {
			release()
		}
		return
	}
	s.src, s.release = src, release
	s.mu.Unlock()
	s.Refresh(ctx)

	t := time.NewTicker(s.opts.SyncInterval)
	defer t.Stop()
	health := time.NewTicker(s.opts.HealthInterval)
	defer health.Stop()
	retry := time.NewTicker(s.opts.RetryInterval)
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
			s.fail(fmt.Errorf("%w: bw serve exited", vault.ErrUnavailable))
		case <-t.C:
			s.Refresh(ctx)
		case <-retry.C:
			if st := s.Status(); st.State == Online && st.SyncErr != nil {
				s.Refresh(ctx) // the server may be back
			}
		case <-health.C:
			s.checkHealth(ctx)
		}
	}
}

// checkHealth detects a vanished/locked vault between syncs and recovers
// from transient outages.
func (s *Service) checkHealth(ctx context.Context) {
	src := s.source()
	if src == nil {
		return
	}
	st, err := src.Status(ctx)
	cur := s.Status().State
	switch {
	case err != nil:
		s.fail(err)
	case cur == Offline || cur == Online && st.Status != vault.Unlocked:
		s.Refresh(ctx)
	}
}

func (s *Service) source() Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src
}

// Refresh checks the vault status and, if unlocked, syncs and reloads the
// snapshot. Errors move the service to Offline/Locked as appropriate while
// keeping the last known snapshot.
func (s *Service) Refresh(ctx context.Context) {
	src := s.source()
	if src == nil {
		return
	}
	st, err := src.Status(ctx)
	if err != nil {
		s.fail(err)
		return
	}
	switch st.Status {
	case vault.Unauthenticated:
		s.set(func() { s.state, s.lastErr = NeedLogin, nil })
		return
	case vault.Locked:
		s.set(func() {
			s.state, s.lastErr = Locked, nil
			s.useCacheFor(st.Account())
		})
		return
	}
	s.set(func() { s.state = Syncing })
	// A failed sync (server unreachable) still leaves bw's local copy
	// usable: unlock, list and passwords work offline.
	sctx, cancel := context.WithTimeout(ctx, s.opts.SyncTimeout)
	syncErr := src.Sync(sctx)
	cancel()
	if syncErr != nil && ctx.Err() == nil && !errors.Is(syncErr, vault.ErrLocked) {
		if _, err := src.Status(ctx); err != nil { // bw serve itself is gone
			s.fail(err)
			return
		}
	}
	snap, err := src.Snapshot(ctx)
	if err != nil {
		s.fail(err)
		return
	}
	if s.opts.Cache != nil {
		_ = s.opts.Cache.Save(snap)
	}
	s.set(func() {
		s.snap, s.fromCache, s.account = snap, false, snap.Account
		s.state, s.lastErr, s.syncErr = Online, nil, syncErr
	})
}

// useCacheFor replaces a cache of another account; called with mu held.
func (s *Service) useCacheFor(account string) {
	if s.account == account || s.opts.Cache == nil {
		return
	}
	s.account = account
	if snap, err := s.opts.Cache.Load(account); err == nil {
		s.snap, s.fromCache = snap, true
	} else {
		s.snap, s.fromCache = model.Snapshot{}, false
	}
}

func (s *Service) fail(err error) {
	s.set(func() {
		s.lastErr = err
		switch {
		case errors.Is(err, vault.ErrLocked):
			s.state = Locked
		case errors.Is(err, vault.ErrUnavailable):
			s.state = Offline
		default:
			if s.state == Syncing || s.state == Starting {
				s.state = Offline
			}
		}
	})
}

// ErrNotOnline is returned when an operation needs the unlocked vault.
var ErrNotOnline = errors.New("vault is not available (offline or locked)")

// Unlock unlocks the vault and refreshes. password is wiped.
func (s *Service) Unlock(ctx context.Context, password []byte) error {
	defer secret.Zero(password)
	src := s.source()
	if src == nil {
		return ErrNotOnline
	}
	if err := src.Unlock(ctx, password); err != nil {
		s.set(func() { s.lastErr = err })
		return err
	}
	s.Refresh(ctx)
	return nil
}

// Secret fetches the credential of a host for one connection attempt.
// The caller must Wipe it.
func (s *Service) Secret(ctx context.Context, hostID string) (secret.Secret, error) {
	src := s.source()
	if src == nil {
		return secret.Secret{}, ErrNotOnline
	}
	sec, err := src.Secret(ctx, hostID)
	if err != nil {
		if errors.Is(err, vault.ErrLocked) || errors.Is(err, vault.ErrUnavailable) {
			s.fail(err)
			return secret.Secret{}, ErrNotOnline
		}
		return secret.Secret{}, err
	}
	return sec, nil
}

// Widgets returns the latest widget outputs.
func (s *Service) Widgets() []WidgetOutput { return s.widgets.Outputs() }

// SetWidgetsActive runs (true) or pauses the widget commands, e.g. while the
// side column is visible or not.
func (s *Service) SetWidgetsActive(on bool) { s.widgets.SetActive(on) }
