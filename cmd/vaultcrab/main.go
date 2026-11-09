// SPDX-License-Identifier: MIT

// Command vaultcrab is a cross-platform terminal session manager for SSH,
// VNC, SMB and custom protocols. Credentials stay in the vault (Bitwarden/
// Vaultwarden, gopass).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/applog"
	"github.com/sichkarmg/vaultcrab/internal/biometric"
	"github.com/sichkarmg/vaultcrab/internal/cache"
	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/hostlist"
	"github.com/sichkarmg/vaultcrab/internal/jump"
	"github.com/sichkarmg/vaultcrab/internal/keystore"
	"github.com/sichkarmg/vaultcrab/internal/quickunlock"
	"github.com/sichkarmg/vaultcrab/internal/screenlock"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
	"github.com/sichkarmg/vaultcrab/internal/spawn"
	"github.com/sichkarmg/vaultcrab/internal/tui"
	"github.com/sichkarmg/vaultcrab/internal/vault"
	"github.com/sichkarmg/vaultcrab/internal/widget"
)

// version is the release version (SemVer, no "v"). A release PR updates it
// together with CHANGELOG.md; builds from git override it with -ldflags
// (`git describe` for make, the tag for the release workflow).
var version = "0.7.0"

func main() {
	if spawn.Main() { // helper mode (see internal/spawn)
		return
	}
	showVersion := flag.Bool("version", false, "print version and exit")
	cfgPath := flag.String("config", "", "config file (default: <user config dir>/vaultcrab/config.yaml)")
	ascii := flag.Bool("ascii", false, "use ASCII icons")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: vaultcrab [flags]\n       vaultcrab forget          delete the PIN, Touch ID data and master passwords kept for the quick unlock\n       vaultcrab history clear   delete the history of the connections through jump hosts\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("vaultcrab", version)
		return
	}
	harden() // no core dumps (the master password is in memory while unlocking)
	if flag.Arg(0) == "forget" {
		os.Exit(forget(*cfgPath))
	}
	if flag.Arg(0) == "history" {
		os.Exit(historyCmd(flag.Args()[1:]))
	}
	if *cfgPath == "" {
		offerMigration()
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab: config:", err)
		os.Exit(2)
	}
	// the config is re-read while running (core): with the same flags
	load := func(path string) (config.Config, error) {
		cfg, err := config.Load(path)
		if *ascii {
			cfg.Icons.Mode = "ascii"
		}
		return cfg, err
	}
	if *ascii {
		cfg.Icons.Mode = "ascii"
	}
	if err := run(cfg, load); err != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab:", err)
		os.Exit(1)
	}
}

// offerMigration asks, on the first start after the rename, whether to copy
// the config directory of the former name (sshmgr) to the new one. The old
// directory is never changed or removed. Without a terminal nothing is asked.
func offerMigration() {
	dir, err := config.DefaultDir()
	if err != nil {
		return
	}
	old, ok := config.LegacyDir(dir)
	if !ok {
		return
	}
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Printf("Found the config of the former name: %s\nCopy it to %s? The old directory is kept. [Y/n] ", old, dir)
	var answer string
	fmt.Scanln(&answer)
	switch answer {
	case "", "y", "Y", "yes", "Yes":
	default:
		fmt.Println("Not copied; starting with a new config.")
		return
	}
	if err := config.CopyDir(old, dir); err != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab: copying the old config:", err)
		return
	}
	fmt.Println("Copied. Paths inside config.yaml that point to the old directory still work: it is kept.")
}

func run(cfg config.Config, load func(string) (config.Config, error)) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cacheDir, err := cache.DefaultDir()
	if err != nil {
		return err
	}
	l := launcher(cfg)
	workspacePath := filepath.Join(cfg.Dir, "workspace.json")
	serveOpts := vault.ServeOptions{BW: cfg.BW.Path, DataDir: cfg.BW.DataDir, Env: cfg.BW.Env}
	var widgetExec widget.Exec
	helper := "not used"
	// bw serve and widget commands are started by a helper outside our
	// process tree: macOS Terminal shows the names of our children in the
	// window title and redraws it on every fork. Windows does not need it.
	if runtime.GOOS != "windows" {
		if sp, err := spawn.NewClient(); err == nil {
			defer sp.Close()
			helper = "on (bw serve and widgets run outside vaultcrab's process tree)"
			serveOpts.Spawn = spawner{sp}
			widgetExec = func(ctx context.Context, argv []string, timeout time.Duration) ([]byte, error) {
				out, err := sp.Run(ctx, argv, timeout)
				if errors.Is(err, spawn.ErrTimeout) {
					err = widget.ErrTimeout
				}
				return out, err
			}
		}
	}
	ks, ksErr := keystore.Open()
	defVault := newVault(cfg.BW.Path, config.VaultSpec{Name: cfg.BW.Name, DataDir: cfg.BW.DataDir, Env: cfg.BW.Env, URL: cfg.BW.URL},
		serveOpts, cacheDir)
	svc := core.New(core.Options{
		Connect:      defVault.Connect,
		Login:        defVault.Login,
		VaultName:    cfg.BW.Name,
		NewVault:     func(sp config.VaultSpec) core.VaultOptions { return newVault(cfg.BW.Path, sp, serveOpts, cacheDir) },
		ScreenLock:   screenlock.Watch,
		Keystore:     ks,
		KeystoreErr:  ksErr,
		BootID:       quickunlock.BootID,
		Biometric:    biometric.Default,
		Cache:        defVault.Cache,
		SyncInterval: cfg.BW.SyncInterval,
		SSH: connect.SSHOptions{
			KnownHosts:  cfg.SSH.KnownHosts,
			Timeout:     cfg.SSH.Timeout,
			Handshake:   cfg.SSH.Handshake,
			Keepalive:   cfg.SSH.Keepalive,
			Term:        cfg.Terminal.Term,
			StrictHosts: cfg.SSH.StrictHostKeys,
			Legacy:      cfg.SSH.LegacyAlgorithms,
		},
		JumpHistory: jump.NewHistory(jumpHistoryPath(cacheDir), cfg.Jump.HistoryLimit, cfg.Jump.History),
		Scrollback:  cfg.Terminal.Scrollback,
		Local:       connect.LocalOptions{Command: cfg.Local.Shell.ForOS(), Dir: cfg.Local.Dir, Term: cfg.Terminal.Term},
		Filters:     cfg.CompiledFilters(),
		Visibility: hostlist.Visibility{
			Orgs:         cfg.Vault.Orgs,
			Collections:  cfg.Vault.Collections,
			HidePersonal: cfg.Vault.HidePersonal,
		},
		Launch: l.Launch,
		VNC: func(ctx context.Context, h core.Host, sec *secret.Secret, jump func(context.Context) (*connect.Tunnel, error)) (session.Remote, error) {
			return l.VNC(ctx, h, sec, jump)
		},
		Start:      l.Start,
		Scripts:    cfg.CompiledScripts(),
		Widgets:    cfg.CompiledWidgets(),
		WidgetExec: widgetExec,
		Config:     &cfg,
		LoadConfig: load,
		// a re-read config: the next client started uses its programs
		ApplyConfig: func(c config.Config) { l.SetSpecs(core.LaunchSpecs(c)) },
	})
	applog.Printf("vaultcrab %s started (%s/%s), config %s", version, runtime.GOOS, runtime.GOARCH, cfg.Path)
	defer applog.Printf("vaultcrab exited")
	svc.Start(ctx)
	defer svc.Stop()
	defer svc.Shutdown()

	m := tui.New(ctx, svc, tui.Options{
		TabsBottom:   cfg.UI.TabsPosition == "bottom",
		ForwardMouse: cfg.Terminal.ForwardMouse,
		CloseOnExit:  cfg.Terminal.CloseOnExit,
		CopyOSC52:    cfg.Terminal.CopyOSC52,
		CopySystem:   cfg.Terminal.CopySystem,
		ConfirmPaste: cfg.Terminal.ConfirmPaste,
		Prefix:       cfg.UI.Prefix,
		Keys:         cfg.UI.Keys,
		Icons:        cfg.IconSet(),
		ShowTree:     cfg.UI.ShowTree,
		ShowNotes:    cfg.UI.ShowNotes,
		ListWidth:    cfg.UI.ListWidth,
		Widgets:      cfg.Widgets.Show && len(cfg.Widgets.Items) > 0,
		WidgetsWidth: cfg.Widgets.Width,
		WidgetsMin:   cfg.Widgets.MinWidth,
		ConfigPath:   cfg.Path,
		RestoreTabs:  cfg.UI.RestoreTabs,
		// the host list filters of the last run (no secrets)
		ViewPath:    filepath.Join(cfg.Dir, "view.json"),
		RestoreView: cfg.UI.RestoreView,
		// tabs of the last run: host IDs and the pane layout, no secrets
		WorkspacePath: workspacePath,
		Version:       version,
		Appearance:    cfg.Appearance(),
		Info:          about(cfg, cacheDir, workspacePath, helper),
	})
	// Focus reports (where the terminal sends them) pause the widgets while
	// the window is in the background.
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithReportFocus()}
	if cfg.UI.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, opts...)
	// On SIGHUP (terminal closed), SIGTERM or SIGINT from outside: restore the
	// terminal and fall through to the deferred cleanup that stops bw serve.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, shutdownSignals...)
	defer signal.Stop(sigs)
	go func() {
		if _, ok := <-sigs; ok {
			p.Kill()
		}
	}()
	_, err = p.Run()
	if werr := m.SaveWorkspace(); werr != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab: saving the tabs:", werr)
	}
	if werr := m.SaveView(); werr != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab: saving the filters:", werr)
	}
	if errors.Is(err, tea.ErrProgramKilled) {
		err = nil
	}
	return err
}

// newVault builds the connection to a vault: bw serve with its data
// directory (or a running bw serve at sp.URL), `bw login` (after `bw config
// server` when sp.Server is set) and its own cache. sp.ID is "" for the
// default vault.
func newVault(bw string, sp config.VaultSpec, base vault.ServeOptions, cacheDir string) core.VaultOptions {
	opts := base
	opts.DataDir, opts.Env = sp.DataDir, sp.Env
	who := "bw serve"
	cdir := cacheDir
	if sp.ID != "" {
		who = "bw serve (" + sp.ID + ")"
		cdir = filepath.Join(cacheDir, "vault-"+sp.ID)
	}
	env := func() []string {
		e := os.Environ()
		if sp.DataDir != "" {
			e = append(e, "BITWARDENCLI_APPDATA_DIR="+sp.DataDir)
		}
		return append(e, sp.Env...)
	}
	// The client of the last bw serve: a bw serve restarted after a crash
	// gets its session key and so starts unlocked (see vault/session.go).
	var (
		mu   sync.Mutex
		prev *vault.Client
	)
	return core.VaultOptions{
		ID: sp.ID, Name: sp.Label(), Server: sp.Server,
		Connect: func(ctx context.Context) (core.Source, func(), error) {
			if sp.URL != "" {
				c := vault.NewClient(sp.URL)
				c.Env = sp.Env
				return c, nil, nil
			}
			if sp.DataDir != "" {
				if err := os.MkdirAll(sp.DataDir, 0o700); err != nil {
					return nil, nil, err
				}
			}
			o := opts
			mu.Lock()
			if prev != nil {
				o.Session = prev.TakeSession()
				prev = nil
			}
			mu.Unlock()
			srv, err := vault.StartServer(ctx, o)
			if err != nil {
				secret.Zero(o.Session)
				applog.Printf("%s: %v", who, err)
				return nil, nil, err
			}
			c := vault.NewClient(srv.URL).WithServer(srv)
			c.Env = sp.Env
			if o.Session != nil {
				c.AdoptSession(o.Session)
				applog.Printf("%s: started at %s with the session of the previous bw serve", who, srv.URL)
			} else {
				applog.Printf("%s: started at %s", who, srv.URL)
			}
			mu.Lock()
			prev = c
			mu.Unlock()
			stop := func() {
				srv.Stop()
				applog.Printf("%s: stopped", who)
			}
			return c, stop, nil
		},
		Login: func() *exec.Cmd {
			if sp.Server != "" {
				// the server of this vault: only possible while logged out,
				// which is when the login is offered
				cfgCmd := exec.Command(bw, "config", "server", sp.Server)
				cfgCmd.Env = env()
				if out, err := cfgCmd.CombinedOutput(); err != nil {
					applog.Printf("bw config server %s: %v: %s", sp.Server, err, out)
				}
			}
			cmd := exec.Command(bw, "login")
			cmd.Env = env()
			return cmd
		},
		Cache: &cache.Cache{Dir: cdir},
	}
}

// about lists where the running vaultcrab and its files are (Settings → About).
func about(cfg config.Config, cacheDir, workspacePath, helper string) []tui.InfoItem {
	exe, err := os.Executable()
	if err != nil {
		exe = "unknown: " + err.Error()
	} else if real, err := filepath.EvalSymlinks(exe); err == nil && real != exe {
		exe += " -> " + real
	}
	wd, _ := os.Getwd()
	exists := func(p string) string {
		if _, err := os.Stat(p); err != nil {
			return p + " (not created yet)"
		}
		return p
	}
	cfgFile := cfg.Path
	if _, err := os.Stat(cfg.Path); err != nil {
		cfgFile += " (not created yet: built-in defaults; Appearance changes create it)"
	}
	bw := cfg.BW.Path
	if p, err := exec.LookPath(cfg.BW.Path); err == nil {
		bw = p
	} else {
		bw += " (not found in PATH)"
	}
	if cfg.BW.URL != "" {
		bw = "bw serve at " + cfg.BW.URL + " (started elsewhere)"
	}
	data := cfg.BW.DataDir
	if data == "" {
		data = "bw default"
	}
	items := []tui.InfoItem{
		{Label: "Version", Value: fmt.Sprintf("Vaultcrab %s (%s/%s, %s)", version, runtime.GOOS, runtime.GOARCH, runtime.Version())},
		{Label: "Executable", Value: exe},
		{Label: "Started in", Value: wd},
		{Label: "Process", Value: fmt.Sprintf("pid %d · helper %s", os.Getpid(), helper)},
		{Label: "Config file", Value: cfgFile},
		{Label: "Config dir", Value: cfg.Dir},
		{Label: "Saved tabs", Value: exists(workspacePath)},
		{Label: "known_hosts", Value: exists(cfg.SSH.KnownHosts)},
		{Label: "Cache", Value: exists(cacheDir)},
		{Label: "Jump history", Value: exists(jumpHistoryPath(cacheDir))},
		{Label: "Bitwarden CLI", Value: bw},
		{Label: "bw data dir", Value: data},
	}
	logFile := cfg.LogFile()
	if !cfg.Log.Enabled {
		logFile += " (off: Settings → Behavior → Event log)"
	}
	items = append(items, tui.InfoItem{Label: "Event log", Value: logFile})
	if p := os.Getenv("VAULTCRAB_DEBUG_LOG"); p != "" {
		items = append(items, tui.InfoItem{Label: "Debug log", Value: p})
	}
	return items
}

// spawner adapts spawn.Client to vault.Spawner.
type spawner struct{ c *spawn.Client }

func (s spawner) Start(argv, env []string) (vault.Process, error) {
	p, err := s.c.Start(argv, env)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// launcher returns the external-program launcher configured for this OS.
func launcher(cfg config.Config) *connect.Launcher {
	return &connect.Launcher{Specs: core.LaunchSpecs(cfg), Fallbacks: connect.DefaultFallbacks()}
}

// jumpHistoryPath is the history of the connections through jump hosts,
// next to the metadata cache (names and addresses only).
func jumpHistoryPath(cacheDir string) string { return filepath.Join(cacheDir, "jump-history.json") }

// historyCmd runs `vaultcrab history clear`.
func historyCmd(args []string) int {
	if len(args) != 1 || args[0] != "clear" {
		fmt.Fprintln(os.Stderr, "usage: vaultcrab history clear")
		return 2
	}
	dir, err := cache.DefaultDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab history:", err)
		return 1
	}
	path := jumpHistoryPath(dir)
	had, err := jump.ClearHistory(path)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "vaultcrab history:", err)
		return 1
	case had:
		fmt.Println("Deleted the history of the jump connections:", path)
	default:
		fmt.Println("No history of the jump connections:", path)
	}
	return 0
}

// forget deletes the PIN and every stored master password (vaultcrab
// forget): also for vaults no longer in the config, and the data of Touch
// ID.
func forget(cfgPath string) int {
	ks, err := keystore.Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab forget:", err)
		return 1
	}
	ids := []string{"default"}
	if cfg, err := config.Load(cfgPath); err == nil {
		for _, v := range cfg.Vaults {
			ids = append(ids, v.ID)
		}
	}
	var keys []string
	for _, id := range ids {
		keys = append(keys, "vault/"+id)
	}
	m := quickunlock.Manager{Store: ks}
	had := m.HasPIN() || len(m.Vaults()) > 0
	for _, id := range ids {
		had = had || m.HasTouch(id)
	}
	if err := errors.Join(m.Forget(keys...), m.ForgetTouch(ids...)); err != nil {
		fmt.Fprintln(os.Stderr, "vaultcrab forget:", err)
		return 1
	}
	if had {
		fmt.Printf("Deleted the PIN, the stored master passwords and the Touch ID data from %s.\n", ks.Name())
	} else {
		fmt.Printf("Nothing stored in %s (checked the PIN and the vaults of the config).\n", ks.Name())
	}
	fmt.Println("Unlock with the master password from now on; Settings → Security sets a new PIN.")
	return 0
}
