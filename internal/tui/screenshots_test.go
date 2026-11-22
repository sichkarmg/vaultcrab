// SPDX-License-Identifier: MIT

//go:build screenshots

package tui

// The README screenshots: scenes of the TUI with a demo vault (test data:
// documentation addresses only), written as ANSI captures that
// scripts/screenshots.sh renders into docs/screenshot-*.svg and .png.
//
//	VAULTCRAB_SCREENSHOTS=/tmp/shots go test -tags screenshots -run TestScreenshots ./internal/tui

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/jump"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/widget"
)

const shotW, shotH = 150, 40

// demoSrc is the demo vault.
type demoSrc struct{}

func (demoSrc) Status(context.Context) (core.VaultInfo, error) {
	return core.VaultInfo{Status: core.VaultUnlocked, UserEmail: "demo@example.com", ServerURL: "https://vault.example.com"}, nil
}
func (demoSrc) Unlock(context.Context, []byte) error { return nil }
func (demoSrc) Sync(context.Context) error           { return nil }
func (demoSrc) Secret(context.Context, string) (core.Secret, error) {
	return core.Secret{Value: []byte("demo")}, nil
}
func (demoSrc) Snapshot(context.Context) (core.Snapshot, error) {
	ts := time.Date(2026, 10, 20, 12, 30, 0, 0, time.Local)
	h := func(id, name string, sc core.Scheme, user, addr string, port int, folder string, tags ...string) core.Host {
		return core.Host{ID: id, Name: name, Scheme: sc, User: user, Addr: addr, Port: port, FolderID: folder, Tags: tags,
			Created: ts.AddDate(0, -1, 0), Revised: ts}
	}
	return core.Snapshot{
		Folders: []core.Folder{
			{ID: "lab", Name: "Lab", Path: []string{"Lab"}},
			{ID: "lin", Name: "Lab/Linux", Path: []string{"Lab", "Linux"}},
			{ID: "win", Name: "Lab/Windows", Path: []string{"Lab", "Windows"}},
			{ID: "prod", Name: "Prod", Path: []string{"Prod"}},
			{ID: "web", Name: "Prod/Web", Path: []string{"Prod", "Web"}},
		},
		Hosts: []core.Host{
			colored(h("1", "build-server", core.SchemeSSH, "ci", "192.0.2.30", 22, "lin", "linux"), "teal"),
			h("2", "dev-box", core.SchemeSSH, "user", "192.0.2.12", 22, "lin", "linux"),
			h("3", "desk-vnc", core.SchemeVNC, "", "192.0.2.40", 5900, "lin"),
			h("4", "fileserver", core.SchemeSMB, "user", "192.0.2.50", 445, "win", "windows"),
			h("5", "win-rdp", core.SchemeRDP, "Administrator", "192.0.2.70", 3389, "win", "windows"),
			h("6", "router-core", core.SchemeSSH, "admin", "192.0.2.1", 22, "lab", "network"),
			h("7", "web-01", core.SchemeSSH, "deploy", "192.0.2.10", 22, "web", "linux", "prod"),
			h("8", "web-02", core.SchemeSSH, "deploy", "192.0.2.11", 22, "web", "linux", "prod"),
			h("9", "bastion", core.SchemeSSH, "admin", "198.51.100.5", 2222, "prod", "linux", "jump"),
			h("10", "pve-01", core.SchemeSSH, "root", "192.0.2.20", 22, "prod", "proxmox"),
			h("11", "db-primary", core.SchemeSSH, "postgres", "192.0.2.21", 22, "prod", "linux", "prod"),
			h("12", "sftp-backup", core.SchemeSFTP, "backup", "192.0.2.61", 22, ""),
			h("13", "api-staging", core.SchemeSSH, "app", "192.0.2.80", 22, ""),
		},
		SyncedAt: time.Now(),
	}, nil
}

// colored gives h the color label c (the item field "color").
func colored(h core.Host, c string) core.Host {
	h.Fields = map[string]string{core.FieldColor: c}
	return h
}

// demoOutput is what the shell of a demo host shows after the login.
var demoOutput = map[string][]string{
	"web-01": {
		"\x1b[1;32mdeploy@web-01\x1b[0m:\x1b[1;34m~\x1b[0m$ systemctl status nginx --no-pager | head -4",
		"\x1b[1;32m●\x1b[0m nginx.service - A high performance web server and a reverse proxy server",
		"     Loaded: loaded (/usr/lib/systemd/system/nginx.service; enabled)",
		"     Active: \x1b[1;32mactive (running)\x1b[0m since Mon 2026-11-16 08:02:11 UTC; 6 days ago",
		"   Main PID: 1123 (nginx)",
	},
	"web-02": {
		"\x1b[1;32mdeploy@web-02\x1b[0m:\x1b[1;34m~\x1b[0m$ tail -n 5 /var/log/nginx/access.log | cut -c1-60",
		"192.0.2.100 - - [22/Nov/2026:10:14:02] \"GET / HTTP/2.0\" 200",
		"192.0.2.101 - - [22/Nov/2026:10:14:05] \"GET /api HTTP/2.0\" 200",
		"192.0.2.100 - - [22/Nov/2026:10:14:09] \"GET /login HTTP/2.0\" 302",
		"192.0.2.102 - - [22/Nov/2026:10:14:11] \"POST /api HTTP/2.0\" 201",
	},
	"db-primary": {
		"\x1b[1;32mpostgres@db-primary\x1b[0m:\x1b[1;34m~\x1b[0m$ df -h /var/lib/postgresql",
		"Filesystem      Size  Used Avail Use% Mounted on",
		"/dev/sdb1       492G  187G  280G  41% /var/lib/postgresql",
	},
}

// demoDial opens the shell of a demo host.
func demoDial(_ context.Context, h model.Host, _ *secret.Secret, _, _ int) (core.Remote, error) {
	return newDemoShell(h.User+"@"+h.Name, demoOutput[h.Name]...), nil
}

// demoShell is a shell on a demo host: a prompt and a few lines.
type demoShell struct {
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func newDemoShell(prompt string, lines ...string) *demoShell {
	s := &demoShell{out: make(chan []byte, 8), done: make(chan struct{})}
	for _, l := range lines {
		s.out <- []byte(l + "\r\n")
	}
	s.out <- []byte("\x1b[1;32m" + prompt + "\x1b[0m:\x1b[1;34m~\x1b[0m$ ")
	return s
}

func (s *demoShell) Read(p []byte) (int, error) {
	select {
	case b := <-s.out:
		return copy(p, b), nil
	case <-s.done:
		return 0, errors.New("closed")
	}
}
func (s *demoShell) Write(p []byte) (int, error) { return len(p), nil }
func (s *demoShell) Resize(int, int) error       { return nil }
func (s *demoShell) Done() <-chan struct{}       { return s.done }
func (s *demoShell) Err() error                  { return nil }
func (s *demoShell) Close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}

// demoJump is a connection to a jump host (dialing nothing real).
type demoJump struct{ done chan struct{} }

func (d *demoJump) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return nil, errors.New("demo")
}
func (d *demoJump) Done() <-chan struct{} { return d.done }
func (d *demoJump) Close() error          { return nil }

func TestScreenshots(t *testing.T) {
	dir := os.Getenv("VAULTCRAB_SCREENSHOTS")
	if dir == "" {
		t.Skip("VAULTCRAB_SCREENSHOTS not set")
	}
	version := os.Getenv("VAULTCRAB_SCREENSHOT_VERSION")
	if version == "" {
		version = "dev"
	}
	prof := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor) // the themes use #rrggbb colors
	t.Cleanup(func() { lipgloss.SetColorProfile(prof) })
	t.Cleanup(resetColors)
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.UI.FolderColors = map[string]string{"Prod": "red"}
	cfg.Path = filepath.Join(tmp, "config.yaml")
	cfg.Dir = tmp
	cfg.JumpTemplates = []config.JumpTemplate{
		{Name: "Workstations", Jump: "bastion", User: "admin", Key: "vault:bastion"},
		{Name: "DC2", Jump: "admin@203.0.113.7", Key: "agent"},
	}
	cfg.Tunnels = []config.Tunnel{
		{Name: "Proxmox web", Jump: "Workstations", Target: "192.0.2.20:8006", LocalPort: 18006, Open: "https://localhost:{port}"},
		{Name: "Grafana", Jump: "bastion", Target: "localhost:3000", LocalPort: 13000},
		{Name: "DC2 iLO", Jump: "DC2", Target: "198.51.100.40:443", Open: "https://localhost:{port}"},
	}
	if err := config.SaveJumpTemplates(cfg.Path, cfg.JumpTemplates); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveTunnels(cfg.Path, cfg.Tunnels); err != nil {
		t.Fatal(err)
	}
	hist := jump.NewHistory(filepath.Join(tmp, "jump-history.json"), 0, true)
	now := time.Now()
	for i, tg := range []string{"10.20.0.5", "10.20.0.17", "10.20.1.42", "10.20.0.9"} {
		for range 4 - i {
			hist.Add("t:Workstations", tg, "", "", "", now.Add(-time.Duration(i*7)*time.Hour))
		}
	}
	hist.Add("h:9", "192.0.2.21", "postgres", "", "", now.Add(-26*time.Hour))
	svc := core.New(core.Options{
		Connect:     func(context.Context) (core.Source, func(), error) { return demoSrc{}, nil, nil },
		Filters:     cfg.CompiledFilters(),
		Config:      &cfg,
		ConfigPoll:  -1,
		JumpHistory: hist,
		SSH:         connect.SSHOptions{KnownHosts: filepath.Join(tmp, "known_hosts")},
		SSHDial:     demoDial,
		JumpConnect: func(_ context.Context, h connect.Hop, _ connect.SSHOptions) (core.JumpConn, error) {
			if h.Addr == "203.0.113.7:22" {
				return nil, &connect.JumpError{Jump: h.Name, Addr: h.Addr, Stage: connect.StageConnect, Err: errors.New("no route to host")}
			}
			return &demoJump{done: make(chan struct{})}, nil
		},
		JumpDial: func(ctx context.Context, j, tg connect.Hop, _, _ int, _ connect.SSHOptions) (core.Remote, error) {
			tr := connect.TraceOf(ctx)
			tr.Add("jump host %s (%s) as %q, %s", j.Name, j.Addr, j.User, j.AuthInfo)
			tr.Add("connecting to %s (timeout 15s)", j.Addr)
			tr.Add("connection established: 192.0.2.100:51022 -> %s", j.Addr)
			tr.Add("remote protocol version: SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5")
			tr.Add("server host key: ssh-ed25519 SHA256:q3bXv0Zk7m1t9dCwN4Lr8yPfS2aHjEo6uTnG5iVxW0c")
			tr.Add("host key found in ~/.config/vaultcrab/known_hosts")
			tr.Add("authenticated as %q; server SSH-2.0-OpenSSH_9.6p1", j.User)
			if tg.Addr == "10.20.1.42:22" {
				tr.Add("opening a direct-tcpip channel to %s through the jump host (timeout 15s)", tg.Addr)
				tr.Add("connect failed: connect failed (No route to host)")
				return nil, &connect.JumpError{Target: true, Stage: connect.StageConnect, Jump: j.Name, Addr: tg.Addr,
					Err: errors.New("No route to host")}
			}
			host, _, _ := net.SplitHostPort(tg.Addr)
			return newDemoShell(tg.User+"@ws-"+host[len(host)-2:], "Welcome to Ubuntu 24.04.1 LTS (GNU/Linux 6.8.0-51-generic x86_64)", "",
				"Last login: Mon Nov  9 09:12:44 2026 from 198.51.100.5"), nil
		},
		Widgets: []widget.Spec{
			{Title: "Clock", Builtin: widget.BuiltinClock},
			{Title: "System", Command: []string{"demo"}, Interval: time.Hour},
		},
		WidgetExec: func(context.Context, []string, time.Duration) ([]byte, error) {
			return []byte("demo-host\nup 3 days, 4 hours\n0.21 0.35 0.30\nMem: 15Gi total, 4.2Gi used\n/dev/sda1  98G  41G  57G  42% /\n"), nil
		},
		TunnelBackoff: time.Hour, // the retrying tunnel stays retrying
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer svc.Shutdown()
	svc.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for svc.Status().State != core.Online && time.Now().Before(deadline) {
		select {
		case <-svc.Events():
		case <-time.After(100 * time.Millisecond):
		}
	}
	a := cfg.Appearance()
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: cfg.UI.Keys, Icons: cfg.IconSet(), ShowTree: true, ShowNotes: true,
		Widgets: true, WidgetsWidth: 34, ConfigPath: cfg.Path, Appearance: a, Version: version, CloseOnExit: true})
	svc.SetWidgetsActive(true)
	m.Update(tea.WindowSizeMsg{Width: shotW, Height: shotH})
	settle := func() {
		for range 20 {
			m.onEvent()
			time.Sleep(10 * time.Millisecond)
		}
	}
	shot := func(name string) {
		settle()
		m.toast = ""
		if err := os.WriteFile(filepath.Join(dir, name+".ans"), []byte(m.View()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	settle()

	// sessions through the jump host: three tabs, two of them grouped
	for _, tg := range []string{"10.20.0.5", "10.20.0.17", "10.20.0.17"} {
		s, err := svc.OpenJump(core.JumpSpec{Via: "t:Workstations", Target: tg}, 100, 30)
		if err != nil {
			t.Fatal(err)
		}
		m.tabs = append(m.tabs, newTab(s))
	}
	// tunnels: active, active SOCKS, retrying (DC2 unreachable)
	for _, tp := range []config.Tunnel{cfg.Tunnels[0], cfg.Tunnels[2]} {
		sp, err := svc.TunnelSpecOf(tp)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.StartTunnel(sp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.StartTunnel(core.TunnelSpec{Via: "h:9", SOCKS: true, Port: 1080}); err != nil {
		t.Fatal(err)
	}
	settle()

	// a tab split into three panes: web-01 | web-02 over db-primary
	host := func(name string) core.Host {
		for _, h := range svc.Snapshot().Hosts {
			if h.Name == name {
				return h
			}
		}
		t.Fatalf("no host %s", name)
		return core.Host{}
	}
	for _, name := range []string{"build-server", "router-core", "web-01"} {
		s, err := svc.OpenSession(host(name), 100, 30)
		if err != nil {
			t.Fatal(err)
		}
		m.tabs = append(m.tabs, newTab(s))
	}
	pt := m.tabs[len(m.tabs)-1]
	m.setActive(len(m.tabs))
	m.splitWith(pt, host("web-02"), core.SplitRight)
	m.splitWith(pt, host("db-primary"), core.SplitDown)
	settle()

	// 1. the host list: two hosts marked, the color labels
	m.switchTab(0)
	keys(m, "down", "down", "down", "down", "down", "down", "down", "down", "down", " ", " ")
	shot("hosts")

	// the themes, on the host list
	for _, th := range core.Themes {
		m.opt.Appearance.Theme = th.Name
		setColors(core.Palette(th.Name, core.FromTheme, core.FromTheme))
		shot("theme-" + th.Name)
	}
	m.opt.Appearance.Theme = core.Themes[0].Name
	resetColors()
	keys(m, "esc")

	// 2. the jump picker, step 2
	keys(m, "j", "enter")
	typeText(m, "10.20.")
	shot("jump")
	keys(m, "esc", "esc")

	// 3. the tunnels
	m.openTunnels(nil)
	shot("tunnels")
	keys(m, "esc")

	// 4. the split tab
	for i, x := range m.tabs {
		if x == pt {
			m.switchTab(i + 1)
		}
	}
	shot("panes")

	// 5. a failed connection through the jump host, with its debug steps
	s, err := svc.OpenJump(core.JumpSpec{Via: "t:Workstations", Target: "10.20.1.42"}, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	m.tabs = append(m.tabs, newTab(s))
	m.setActive(len(m.tabs))
	time.Sleep(200 * time.Millisecond)
	shot("debug")

	// 6. Settings → Appearance and → External programs
	m.switchTab(0)
	m.openSettings()
	st := m.modal.(*settingsModal)
	st.page = pageAppearance
	shot("settings")
	st.page = pagePrograms
	shot("programs")
	keys(m, "esc")
}
