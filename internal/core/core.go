// SPDX-License-Identifier: MIT

// Package core is the application service used by the TUI: it owns the vault
// connection state, the metadata snapshot (live or cached) and the sessions.
package core

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
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
	// Crashed: bw serve exited by itself and is being started again (with
	// the session key of the old one, so the vault stays unlocked).
	Crashed
)

func (s VaultState) String() string {
	return [...]string{"starting", "offline", "need login", "locked", "syncing", "online", "restarting bw serve"}[s]
}

// Options configures a Service.
type Options struct {
	// Connect returns the vault source, e.g. starting bw serve, and a function
	// releasing it. Called in the background by Start/Reconnect; may be slow.
	Connect func(ctx context.Context) (Source, func(), error)
	// Login returns an interactive command that logs the bw CLI in (run by
	// the UI with the terminal handed over). Optional.
	Login func() *exec.Cmd
	// VaultName and VaultServer describe the default vault (shown when there
	// are several). Optional.
	VaultName, VaultServer string
	// Vaults are further vaults (each its own bw data directory and bw
	// serve), next to the default one above.
	Vaults []VaultOptions
	// NewVault builds the options of a vault added to the config while
	// running (Settings → Vaults, a config reload). Optional: without it,
	// new vaults need a restart.
	NewVault func(config.VaultSpec) VaultOptions
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
	// RetryInterval is the first delay of the checks while the server or
	// bw serve is unreachable (0 = 15 s); it doubles up to RetryLimit
	// (0 = 5 min).
	RetryInterval, RetryLimit time.Duration
	// RestartDelay is the first delay before restarting a bw serve that
	// exited again soon after a restart (0 = 2 s; doubles up to RetryLimit).
	RestartDelay time.Duration
	// RestartWait is how long a connection waits for a restarting bw serve
	// before it fails (0 = 20 s).
	RestartWait time.Duration
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

	// ScreenLock calls onLock whenever the user's screen is locked, until
	// ctx ends (internal/screenlock); an error says why it cannot.
	// Optional: without it unlock.lock_on_screen_lock has no effect.
	ScreenLock func(ctx context.Context, onLock func()) error
	// Now is the wall clock (tests); time.Now by default.
	Now func() time.Time
}

// Service is safe for concurrent use.
type Service struct {
	opts   Options
	events chan struct{}

	mu     sync.Mutex
	ctx    context.Context // of Start: vaults added later run in it
	vaults []*vaultConn    // [0] is the default vault (bw:), then the vaults: of the config
	snap   model.Snapshot  // the snapshots of all vaults merged (see merge.go)

	sessions sessions
	widgets  *widget.Runner

	conf    configState      // guarded by mu
	scripts []scripts.Script // guarded by mu: replaced on a config reload

	lock autoLock // see autolock.go
}

// New creates a service. Call Start to begin.
func New(o Options) *Service {
	if o.SyncInterval == 0 {
		o.SyncInterval = 10 * time.Minute
	}
	if o.RetryLimit == 0 {
		o.RetryLimit = 5 * time.Minute
	}
	if o.RestartDelay == 0 {
		o.RestartDelay = 2 * time.Second
	}
	if o.RestartWait == 0 {
		o.RestartWait = 20 * time.Second
	}
	if o.SyncTimeout == 0 {
		o.SyncTimeout = 20 * time.Second
	}
	if o.RetryInterval == 0 {
		o.RetryInterval = 15 * time.Second
	}
	s := &Service{opts: o, events: make(chan struct{}, 1)}
	s.vaults = []*vaultConn{newVaultConn(s, VaultOptions{Name: o.VaultName, Server: o.VaultServer,
		Connect: o.Connect, Login: o.Login, Cache: o.Cache})}
	for _, vo := range o.Vaults {
		s.vaults = append(s.vaults, newVaultConn(s, vo))
	}
	if o.NewVault != nil && o.Config != nil {
		for _, sp := range o.Config.Vaults {
			vo := o.NewVault(sp)
			vo.spec = sp.Key()
			s.vaults = append(s.vaults, newVaultConn(s, vo))
		}
	}
	s.sessions.init(s)
	s.initConfig()
	s.initAutoLock()
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

// Status returns the vault status; with several vaults, a summary (see
// Vaults for each): the state that needs the user most (locked, not logged
// in, starting, syncing), else online if any vault is, else offline.
func (s *Service) Status() VaultStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.vaults) == 1 {
		return s.vaults[0].status()
	}
	var out VaultStatus
	best := -1
	for _, v := range s.vaults {
		st := v.status()
		if p := statePriority[st.State]; p > best {
			best, out.State, out.Err = p, st.State, st.Err
		}
		out.FromCache = out.FromCache || st.FromCache
		if st.SyncedAt.After(out.SyncedAt) {
			out.SyncedAt = st.SyncedAt
		}
		if st.SyncErr != nil && out.SyncErr == nil {
			out.SyncErr = st.SyncErr
		}
	}
	out.Account = s.vaults[0].account
	return out
}

// statePriority orders the summary state of several vaults.
var statePriority = map[VaultState]int{Offline: 0, Online: 1, Syncing: 2, Starting: 3, Crashed: 4, NeedLogin: 5, Locked: 6}

// Snapshot returns the current metadata snapshot of all vaults (shared; do
// not modify).
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

// Start loads the caches and connects to the vaults in the background.
func (s *Service) Start(ctx context.Context) {
	s.widgets.Start(ctx)
	go s.watchConfig(ctx)
	s.mu.Lock()
	s.ctx = ctx
	vs := slices.Clone(s.vaults)
	s.mu.Unlock()
	for _, v := range vs {
		v.loadCache()
		v.startRun(ctx)
	}
	go s.runAutoLock(ctx)
	s.lock.mu.Lock()
	s.lock.enabled = s.lock.onScreen
	screen := s.lock.onScreen
	s.lock.mu.Unlock()
	if screen {
		s.startScreenWatch()
	}
}

// Reconnect releases the vault sources (e.g. stops bw serve) and connects
// again; used after `bw login` or when bw serve died.
func (s *Service) Reconnect(ctx context.Context) {
	for _, v := range s.vaultList() {
		v.reconnect(ctx)
	}
}

// Sync checks every vault now (s in the host list): syncs those that are
// up and retries those that are unreachable. It never restarts a running
// bw serve (that would lock its vault).
func (s *Service) Sync(context.Context) {
	for _, v := range s.vaultList() {
		v.kickRun()
	}
}

// Stop releases the vault sources.
func (s *Service) Stop() {
	for _, v := range s.vaultList() {
		v.stop()
	}
}

// LoginCommand returns the interactive `bw login` command of the first vault
// that is not logged in (the default vault if all are), or nil.
func (s *Service) LoginCommand() *exec.Cmd {
	vs := s.vaultList()
	v := vs[0]
	for i, e := range s.Vaults() {
		if e.State == NeedLogin && i < len(vs) {
			v = vs[i]
			break
		}
	}
	return v.loginCommand()
}

// Refresh checks the vaults and, if unlocked, syncs and reloads their
// snapshots. Errors move a vault to Offline/Locked as appropriate while
// keeping its last known snapshot.
func (s *Service) Refresh(ctx context.Context) {
	for _, v := range s.vaultList() {
		v.refresh(ctx)
	}
}

// ErrNotOnline is returned when an operation needs the unlocked vault; the
// errors below say why (errors.Is(err, ErrNotOnline) holds for each).
var ErrNotOnline = errors.New("vault is not available")

// notOnlineError is an ErrNotOnline with the reason.
type notOnlineError string

func (e notOnlineError) Error() string        { return string(e) }
func (e notOnlineError) Is(target error) bool { return target == ErrNotOnline }

var (
	// ErrVaultLocked: the vault has to be unlocked (u).
	ErrVaultLocked error = notOnlineError("vault is locked: unlock it (u)")
	// ErrVaultNeedLogin: bw has no logged in account (L).
	ErrVaultNeedLogin error = notOnlineError("bw is not logged in (L: login)")
	// ErrServeRestarting: bw serve exited and is being started again.
	ErrServeRestarting error = notOnlineError("bw serve stopped and is being restarted: try again in a moment")
	// ErrServeUnreachable: bw serve does not answer (see the event log).
	ErrServeUnreachable error = notOnlineError("bw serve does not answer (see the event log)")
)

var (
	errServeDown        = errors.New("bw serve is down")
	errLockedByBW       = errors.New("bw locked the vault by itself (bw lock, or a vault timeout of bw)")
	errLockedAfterCrash = errors.New("bw serve exited and was restarted; the vault has to be unlocked again")
)

// Unlock unlocks the first locked vault (the default vault if none is
// locked) and refreshes it. password is wiped.
func (s *Service) Unlock(ctx context.Context, password []byte) error {
	id := ""
	if v, ok := s.LockedVault(); ok {
		id = v.ID
	}
	return s.UnlockVault(ctx, id, password)
}

// UnlockVault unlocks the vault id ("" = the default vault) and refreshes
// it. password is wiped.
func (s *Service) UnlockVault(ctx context.Context, id string, password []byte) error {
	defer secret.Zero(password)
	v := s.vault(id)
	if v == nil {
		return fmt.Errorf("no vault %q", id)
	}
	return v.unlock(ctx, password)
}

// Secret fetches the credential of a host for one connection attempt from
// its vault. The caller must Wipe it.
func (s *Service) Secret(ctx context.Context, hostID string) (secret.Secret, error) {
	s.lockExpired() // the timer may be late after a sleep
	v, id := s.route(hostID)
	return v.secret(ctx, id)
}

// Widgets returns the latest widget outputs.
func (s *Service) Widgets() []WidgetOutput { return s.widgets.Outputs() }

// SetWidgetsActive runs (true) or pauses the widget commands, e.g. while the
// side column is visible or not.
func (s *Service) SetWidgetsActive(on bool) { s.widgets.SetActive(on) }
