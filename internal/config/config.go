// SPDX-License-Identifier: MIT

// Package config loads the user configuration
// (<user config dir>/vaultcrab/config.yaml). The file never contains secrets.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/scripts"
	"github.com/sichkarmg/vaultcrab/internal/widget"
)

// Config is the full configuration.
type Config struct {
	BW       BW                `yaml:"bw"`
	UI       UI                `yaml:"ui"`
	Icons    Icons             `yaml:"icons"`
	Terminal Terminal          `yaml:"terminal"`
	SSH      SSH               `yaml:"ssh"`
	Vault    VaultView         `yaml:"vault"`
	Filters  []FilterSpec      `yaml:"filters"`
	Local    Local             `yaml:"local"`
	Scripts  []ScriptSpec      `yaml:"scripts"`
	Widgets  Widgets           `yaml:"widgets"`
	External map[string]Launch `yaml:"external"` // keys: vnc, smb, ftp, sftp, rdp
	Log      Log               `yaml:"log"`
	// Vaults are further Bitwarden/Vaultwarden vaults (accounts or servers),
	// next to the one of bw:.
	Vaults []VaultSpec `yaml:"vaults"`

	// Path is where the config was loaded from (not serialized).
	Path string `yaml:"-"`
	// Dir is the config directory (known_hosts etc. live here).
	Dir string `yaml:"-"`
}

// VaultSpec is a further vault: its own bw data directory (so its own
// server and login) and bw serve. bw.path and bw.sync_interval are shared.
type VaultSpec struct {
	ID      string   `yaml:"id"`       // short name: a-z, 0-9, - (prefix of its item IDs, cache directory)
	Name    string   `yaml:"name"`     // shown in the UI (default: the ID)
	Server  string   `yaml:"server"`   // server URL for bw config server (empty: bitwarden.com)
	DataDir string   `yaml:"data_dir"` // BITWARDENCLI_APPDATA_DIR (default: <config dir>/bw-<id>)
	Env     []string `yaml:"env"`      // extra environment for bw, e.g. NODE_EXTRA_CA_CERTS=/path (no secrets!)
	URL     string   `yaml:"url"`      // use an already running bw serve instead of starting one
}

// Key identifies the settings of the vault: a vault whose key changed is
// reconnected.
func (v VaultSpec) Key() string {
	return strings.Join(append([]string{v.ID, v.Name, v.Server, v.DataDir, v.URL}, v.Env...), "\x00")
}

// Label is the name of the vault (its ID if it has none).
func (v VaultSpec) Label() string {
	if v.Name != "" {
		return v.Name
	}
	return v.ID
}

// validVaultID: lower-case letters, digits and -, 1..32 characters.
func validVaultID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// checkBWEnv rejects secrets in the environment given to bw.
func checkBWEnv(env []string, what string) error {
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		switch strings.ToUpper(k) {
		case "BW_SESSION", "BW_PASSWORD", "BW_CLIENTSECRET":
			return fmt.Errorf("%s must not contain secrets (%s)", what, k)
		}
	}
	return nil
}

// Log configures the event log (Settings → Behavior → Event log).
type Log struct {
	Enabled bool   `yaml:"enabled"`
	File    string `yaml:"file"` // default: vaultcrab.log in the cache directory
}

// LogFile returns the file of the event log: log.file, or vaultcrab.log in
// the user cache directory.
func (c Config) LogFile() string {
	if c.Log.File != "" {
		return c.Log.File
	}
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "vaultcrab", "vaultcrab.log")
}

// BW configures access to the Bitwarden CLI.
type BW struct {
	Path         string        `yaml:"path"`          // bw executable (default "bw")
	Name         string        `yaml:"name"`          // name of this vault in the UI when there are several (vaults:)
	URL          string        `yaml:"url"`           // use an already running `bw serve` instead of starting one
	DataDir      string        `yaml:"data_dir"`      // BITWARDENCLI_APPDATA_DIR for the started bw
	Env          []string      `yaml:"env"`           // extra environment for bw, e.g. NODE_EXTRA_CA_CERTS=/path (no secrets!)
	SyncInterval time.Duration `yaml:"sync_interval"` // background sync period
}

// UI settings.
// The width of the tab titles (ui.tab_width).
const (
	DefaultTabWidth = 24
	MinTabWidth     = 6
	MaxTabWidth     = 120
)

type UI struct {
	TabsPosition string            `yaml:"tabs_position"` // top | bottom
	Mouse        bool              `yaml:"mouse"`
	Prefix       string            `yaml:"prefix"` // prefix key for window commands, e.g. ctrl+b
	Keys         map[string]string `yaml:"keys"`   // action -> key after prefix
	ListWidth    int               `yaml:"list_width"`
	TabWidth     int               `yaml:"tab_width"`       // longest tab title (columns), cut with … beyond
	TabStatus    string            `yaml:"tab_status"`      // session state in a tab: icon (before the number) | color (of the number)
	RestoreTabs  bool              `yaml:"restore_tabs"`    // reopen the tabs of the last run (not connected)
	RestoreView  bool              `yaml:"restore_filters"` // keep the type filter, org, collection, grouping, search
	ShowTree     bool              `yaml:"show_tree"`
	ShowNotes    bool              `yaml:"show_notes"`      // notes of the selected item in its card (read from the vault)
	Accent       string            `yaml:"accent"`          // accent color: cyan | blue | green | magenta | yellow | red
	Selection    string            `yaml:"selection_color"` // selected item: reverse | cyan | blue | green | magenta | yellow | red | gray
}

// Icons settings: mode and per-icon overrides.
type Icons struct {
	Mode string          `yaml:"mode"` // auto | unicode | ascii
	Set  map[string]Icon `yaml:"set"`
}

// Icon has a unicode glyph and an ASCII fallback.
type Icon struct {
	Unicode string `yaml:"unicode"`
	ASCII   string `yaml:"ascii"`
}

// Terminal settings.
type Terminal struct {
	Scrollback   int    `yaml:"scrollback"`
	Term         string `yaml:"term"`
	ForwardMouse bool   `yaml:"forward_mouse"` // pass mouse to apps that request it (mc, htop)
	CloseOnExit  bool   `yaml:"close_on_exit"` // close the pane when the remote shell exits with status 0
	// Selected text goes to the outer terminal's clipboard (OSC 52) and/or
	// the system clipboard of this machine.
	CopyOSC52  bool `yaml:"copy_osc52"`
	CopySystem bool `yaml:"copy_system"`
	// ConfirmPaste shows text of several lines before it is pasted into a
	// session and asks for confirmation.
	ConfirmPaste bool `yaml:"confirm_paste"`
}

// SSH settings.
type SSH struct {
	KnownHosts     string        `yaml:"known_hosts"` // default <config dir>/known_hosts
	StrictHostKeys bool          `yaml:"strict_host_keys"`
	Timeout        time.Duration `yaml:"timeout"`           // TCP connect
	Handshake      time.Duration `yaml:"handshake_timeout"` // key exchange + authentication
	Keepalive      time.Duration `yaml:"keepalive"`
	// LegacyAlgorithms allows weak algorithms as a last resort (old network
	// devices, e.g. MikroTik RouterOS 6).
	LegacyAlgorithms bool `yaml:"legacy_algorithms"`
}

// VaultView limits which organizations/collections are shown.
type VaultView struct {
	Orgs         []string `yaml:"orgs"`        // names or IDs; empty = all (personal items are always shown)
	Collections  []string `yaml:"collections"` // names/paths or IDs; empty = all
	HidePersonal bool     `yaml:"hide_personal"`
}

// FilterSpec is a filter as written in the config.
type FilterSpec struct {
	Name  string `yaml:"name"`
	Icon  string `yaml:"icon"`
	ASCII string `yaml:"ascii"`
	Rule  string `yaml:"rule"`
	Badge *bool  `yaml:"badge"` // default true when an icon is set
	// IconName takes the icon from the icon set (the built-in type
	// filters: they follow icons.set).
	IconName string `yaml:"-"`
}

// Launch describes an external program per OS. Args may use {host}, {port},
// {user}, {path}, {url}. The password is never substituted. An empty command
// means auto-detection of a known client.
type Launch struct {
	PerOS `yaml:",inline"`
	// PasswordStdin writes the vault password to the program's stdin
	// (only for clients that support it, e.g. TightVNC `vncviewer -autopass`).
	PasswordStdin bool `yaml:"password_stdin"`
	// Password (VNC only) is how the client gets the password: proxy
	// (default: vaultcrab signs in and lets the client in without one), stdin,
	// file ({passfile}), url (in {url}) or argv ({password}).
	Password string `yaml:"password"`
}

// vncPasswordModes are the values of external.vnc.password and the
// placeholder each needs in the command line.
var vncPasswordModes = map[string]string{"proxy": "", "stdin": "", "file": "{passfile}", "url": "{url}", "argv": "{password}"}

// PerOS is a command line per operating system.
type PerOS struct {
	Linux   []string `yaml:"linux"`
	Windows []string `yaml:"windows"`
	Darwin  []string `yaml:"darwin"`
}

// ForOS returns the command for the current OS.
func (l PerOS) ForOS() []string {
	switch runtime.GOOS {
	case "windows":
		return l.Windows
	case "darwin":
		return l.Darwin
	}
	return l.Linux
}

// ScriptSpec is a user launch script as written in the config.
type ScriptSpec struct {
	Name          string `yaml:"name"`
	Rule          string `yaml:"rule"` // filter syntax; empty = every item
	Run           PerOS  `yaml:"run"`  // command per OS with placeholders
	Mode          string `yaml:"mode"` // terminal (default) | external
	PasswordStdin bool   `yaml:"password_stdin"`
	Default       bool   `yaml:"default"` // Enter runs it instead of the built-in action
}

// Widgets is the side column of the host list with the output of commands.
type Widgets struct {
	Show     bool         `yaml:"show"`      // w in the host list toggles it
	Width    int          `yaml:"width"`     // columns
	MinWidth int          `yaml:"min_width"` // hidden on narrower screens
	Items    []WidgetSpec `yaml:"items"`
}

// WidgetSpec is one widget.
type WidgetSpec struct {
	Title    string        `yaml:"title"`
	Run      PerOS         `yaml:"run"`
	Builtin  string        `yaml:"builtin"` // instead of run: clock
	Interval time.Duration `yaml:"interval"`
	Height   int           `yaml:"height"` // max lines, 0 = all
}

// defaultWidgets are demo widgets: the built-in clock (updated when the
// minute changes, no command) and a small shell script showing system
// information (PowerShell on Windows), run every 30 s.
func defaultWidgets() []WidgetSpec {
	return []WidgetSpec{
		{Title: "Clock", Builtin: widget.BuiltinClock},
		{Title: "System", Interval: 30 * time.Second, Run: PerOS{
			Linux: []string{"bash", "-c", `echo "host   $(hostname)"
echo "up     $(LC_ALL=C uptime -p 2>/dev/null | sed 's/^up //')"
read l1 l5 l15 _ < /proc/loadavg; echo "load   $l1 $l5 $l15 ($(nproc) cpu)"
LC_ALL=C free -m | awk '/^Mem:/{printf "mem    %d/%d MiB (%d%%)\n", $3, $2, $3*100/$2}'
LC_ALL=C df -h / | awk 'NR==2{print "disk   " $3 "/" $2 " (" $5 ")"}'`},
			Darwin: []string{"bash", "-c", `echo "host   $(hostname -s)"
echo "up     $(uptime | sed 's/.*up \([^,]*\),.*/\1/')"
echo "load   $(sysctl -n vm.loadavg | tr -d '{}')"
echo "mem    $(( $(sysctl -n hw.memsize) / 1048576 )) MiB"
df -h / | awk 'NR==2{print "disk   " $3 "/" $2 " (" $5 ")"}'`},
			Windows: []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
				`$o = Get-CimInstance Win32_OperatingSystem; $up = (Get-Date) - $o.LastBootUpTime; ` +
					`"host   $env:COMPUTERNAME"; "up     {0}d {1}h {2}m" -f $up.Days, $up.Hours, $up.Minutes; ` +
					`"mem    {0:N0}/{1:N0} MiB" -f (($o.TotalVisibleMemorySize - $o.FreePhysicalMemory)/1KB), ($o.TotalVisibleMemorySize/1KB); ` +
					`$d = Get-PSDrive C; "disk   {0:N1}/{1:N1} GiB" -f ($d.Used/1GB), (($d.Used + $d.Free)/1GB)`},
		}},
	}
}

// Local configures the local terminal.
type Local struct {
	Shell PerOS  `yaml:"shell"` // empty = $SHELL; Windows: pwsh, powershell, %ComSpec%
	Dir   string `yaml:"dir"`   // working directory (default: home)
}

// Actions bound after the prefix key.
const (
	ActNextTab    = "next_tab"
	ActPrevTab    = "prev_tab"
	ActCloseTab   = "close_tab"
	ActCloneTab   = "clone_tab"
	ActShowLog    = "show_log"
	ActList       = "host_list"
	ActHistory    = "history"
	ActSplitRight = "split_right"
	ActSplitDown  = "split_down"
	ActClosePane  = "close_pane"
	ActZoom       = "zoom"
	ActFocusLeft  = "focus_left"
	ActFocusRight = "focus_right"
	ActFocusUp    = "focus_up"
	ActFocusDown  = "focus_down"
	ActGrowLeft   = "resize_left"
	ActGrowRight  = "resize_right"
	ActGrowUp     = "resize_up"
	ActGrowDown   = "resize_down"
	ActBreakPane  = "break_pane"
	ActReconnect  = "reconnect"
	ActQuit       = "quit"
	ActHelp       = "help"
	ActLocal      = "local_shell"
	ActSettings   = "settings"
)

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		BW: BW{Path: "bw", SyncInterval: 10 * time.Minute},
		UI: UI{
			TabsPosition: "top", Accent: "cyan", Selection: "reverse", Mouse: true, Prefix: "ctrl+b", ListWidth: 0, TabWidth: DefaultTabWidth, TabStatus: "icon", ShowTree: true, ShowNotes: true, RestoreTabs: true, RestoreView: true,
			Keys: map[string]string{
				ActNextTab: "n", ActPrevTab: "p", ActCloseTab: "&", ActCloneTab: "c", ActList: "l",
				ActHistory: "[", ActSplitRight: "|", ActSplitDown: "-", ActClosePane: "x",
				ActZoom: "z", ActFocusLeft: "left", ActFocusRight: "right", ActFocusUp: "up",
				ActFocusDown: "down", ActGrowLeft: "H", ActGrowRight: "L", ActGrowUp: "K",
				ActGrowDown: "J", ActBreakPane: "!", ActReconnect: "r", ActQuit: "q", ActHelp: "?", ActLocal: "t", ActSettings: ",", ActShowLog: "g",
			},
		},
		Icons: Icons{Mode: "auto"},
		Terminal: Terminal{Scrollback: 10000, Term: "xterm-256color", ForwardMouse: true, CloseOnExit: true,
			CopyOSC52: true, CopySystem: true, ConfirmPaste: true},
		SSH: SSH{Timeout: 15 * time.Second, Handshake: 60 * time.Second, Keepalive: 15 * time.Second, LegacyAlgorithms: true},
		// One filter tab per connection type (with the type's icon); more
		// examples (tags, fields, favorites) are in docs/config.example.yaml.
		Filters:  schemeFilters(),
		External: map[string]Launch{},
		Widgets:  Widgets{Show: true, Width: 36, MinWidth: 130, Items: defaultWidgets()},
		Scripts: []ScriptSpec{{
			Name: "ping", Mode: "terminal",
			Run: PerOS{Linux: []string{"ping", "-c", "4", "{host}"}, Darwin: []string{"ping", "-c", "4", "{host}"},
				Windows: []string{"ping", "{host}"}},
		}},
	}
}

// schemeFilters returns "All" plus a filter per connection type.
func schemeFilters() []FilterSpec {
	out := []FilterSpec{{Name: "All", Rule: ""}}
	for _, sc := range model.Schemes {
		ic := DefaultIcons[string(sc)]
		out = append(out, FilterSpec{Name: strings.ToUpper(string(sc)), Icon: ic.Unicode, ASCII: ic.ASCII,
			Rule: "scheme:" + string(sc), Badge: ptr(false), IconName: string(sc)})
	}
	return out
}

func ptr[T any](v T) *T { return &v }

// DefaultDir returns <user config dir>/vaultcrab.
func DefaultDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "vaultcrab"), nil
}

// Load reads the config at path (empty = default location). A missing file
// yields the defaults. Unknown keys are an error (to catch typos).
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		dir, err := DefaultDir()
		if err != nil {
			return cfg, err
		}
		path = filepath.Join(dir, "config.yaml")
	}
	cfg.Path = path
	cfg.Dir = filepath.Dir(path)
	b, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, cfg.finish()
	}
	if err != nil {
		return cfg, err
	}
	// Filters, scripts and widget items replace the defaults wholesale when
	// present; maps are merged.
	var probe struct {
		Filters *yaml.Node `yaml:"filters"`
		Scripts *yaml.Node `yaml:"scripts"`
		Widgets struct {
			Items *yaml.Node `yaml:"items"`
		} `yaml:"widgets"`
	}
	_ = yaml.Unmarshal(b, &probe)
	if probe.Filters != nil {
		cfg.Filters = nil
	}
	if probe.Scripts != nil {
		cfg.Scripts = nil
	}
	if probe.Widgets.Items != nil {
		cfg.Widgets.Items = nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	return cfg, cfg.finish()
}

func (c *Config) finish() error {
	d := Default()
	if c.BW.Path == "" {
		c.BW.Path = d.BW.Path
	}
	if c.BW.SyncInterval <= 0 {
		c.BW.SyncInterval = d.BW.SyncInterval
	}
	ids := map[string]bool{}
	for i, v := range c.Vaults {
		if !validVaultID(v.ID) {
			return fmt.Errorf("vaults[%d].id: %q (want a-z, 0-9, -; 1..32 characters)", i, v.ID)
		}
		if ids[v.ID] {
			return fmt.Errorf("vaults: id %q twice", v.ID)
		}
		ids[v.ID] = true
		if v.DataDir == "" && c.Dir != "" {
			c.Vaults[i].DataDir = filepath.Join(c.Dir, "bw-"+v.ID)
		}
		if err := checkBWEnv(v.Env, fmt.Sprintf("vaults[%d].env", i)); err != nil {
			return err
		}
	}
	if err := checkBWEnv(c.BW.Env, "bw.env"); err != nil {
		return err
	}
	switch c.UI.TabsPosition {
	case "top", "bottom":
	case "":
		c.UI.TabsPosition = "top"
	default:
		return fmt.Errorf("ui.tabs_position: %q (want top|bottom)", c.UI.TabsPosition)
	}
	if c.UI.TabWidth == 0 {
		c.UI.TabWidth = DefaultTabWidth
	}
	if c.UI.TabWidth < MinTabWidth || c.UI.TabWidth > MaxTabWidth {
		return fmt.Errorf("ui.tab_width: %d (want %d..%d)", c.UI.TabWidth, MinTabWidth, MaxTabWidth)
	}
	switch c.UI.TabStatus {
	case "icon", "color":
	case "":
		c.UI.TabStatus = "icon"
	default:
		return fmt.Errorf("ui.tab_status: %q (want icon|color)", c.UI.TabStatus)
	}
	if c.UI.Accent == "" {
		c.UI.Accent = d.UI.Accent
	}
	if AccentColor(c.UI.Accent) == "" {
		return fmt.Errorf("ui.accent: %q (want cyan|blue|green|magenta|yellow|red)", c.UI.Accent)
	}
	if c.UI.Selection == "" {
		c.UI.Selection = d.UI.Selection
	}
	if _, _, ok := SelectionColors(c.UI.Selection); !ok {
		return fmt.Errorf("ui.selection_color: %q (want reverse|cyan|blue|green|magenta|yellow|red|gray)", c.UI.Selection)
	}
	if c.UI.Prefix == "" {
		c.UI.Prefix = d.UI.Prefix
	}
	for k, v := range d.UI.Keys {
		if _, ok := c.UI.Keys[k]; !ok {
			if c.UI.Keys == nil {
				c.UI.Keys = map[string]string{}
			}
			c.UI.Keys[k] = v
		}
	}
	for k := range c.UI.Keys {
		if _, ok := d.UI.Keys[k]; !ok {
			return fmt.Errorf("ui.keys: unknown action %q", k)
		}
	}
	switch c.Icons.Mode {
	case "auto", "unicode", "ascii":
	case "":
		c.Icons.Mode = "auto"
	default:
		return fmt.Errorf("icons.mode: %q (want auto|unicode|ascii)", c.Icons.Mode)
	}
	if c.Terminal.Scrollback <= 0 {
		c.Terminal.Scrollback = d.Terminal.Scrollback
	}
	if c.Terminal.Term == "" {
		c.Terminal.Term = d.Terminal.Term
	}
	if c.SSH.Timeout <= 0 {
		c.SSH.Timeout = d.SSH.Timeout
	}
	if c.SSH.Handshake <= 0 {
		c.SSH.Handshake = d.SSH.Handshake
	}
	if c.SSH.KnownHosts == "" {
		c.SSH.KnownHosts = filepath.Join(c.Dir, "known_hosts")
	}
	if c.External == nil {
		c.External = map[string]Launch{}
	}
	for k, l := range c.External {
		if sc, ok := model.ParseScheme(k); !ok || !sc.External() {
			return fmt.Errorf("external: unknown scheme %q (want vnc|smb|ftp|sftp|rdp)", k)
		}
		if l.Password == "" {
			continue
		}
		if k != string(model.VNC) {
			return fmt.Errorf("external.%s.password: only for vnc (use password_stdin)", k)
		}
		ph, ok := vncPasswordModes[l.Password]
		if !ok {
			return fmt.Errorf("external.vnc.password: %q (want proxy|stdin|file|url|argv)", l.Password)
		}
		for os, cmd := range map[string][]string{"linux": l.Linux, "windows": l.Windows, "darwin": l.Darwin} {
			if ph != "" && len(cmd) > 0 && !slices.ContainsFunc(cmd, func(a string) bool { return strings.Contains(a, ph) }) {
				return fmt.Errorf("external.vnc.%s: password %s needs %s in the command line", os, l.Password, ph)
			}
		}
	}
	if c.Widgets.Width < 16 {
		c.Widgets.Width = d.Widgets.Width
	}
	for i, w := range c.Widgets.Items {
		if w.Title == "" {
			return fmt.Errorf("widgets.items[%d]: title is required", i)
		}
		if w.Interval > 0 && w.Interval < time.Second {
			return fmt.Errorf("widgets.items[%d] %q: interval below 1s", i, w.Title)
		}
		if w.Builtin != "" && !slices.Contains(widget.Builtins, w.Builtin) {
			return fmt.Errorf("widgets.items[%d] %q: unknown builtin %q (want %s)", i, w.Title, w.Builtin, strings.Join(widget.Builtins, "|"))
		}
	}
	for i := range c.Scripts {
		sc := &c.Scripts[i]
		if sc.Name == "" {
			return fmt.Errorf("scripts[%d]: name is required", i)
		}
		switch sc.Mode {
		case "":
			sc.Mode = "terminal"
		case "terminal", "external":
		default:
			return fmt.Errorf("scripts[%d] %q: mode %q (want terminal|external)", i, sc.Name, sc.Mode)
		}
		if sc.PasswordStdin && sc.Mode != "external" {
			return fmt.Errorf("scripts[%d] %q: password_stdin needs mode: external", i, sc.Name)
		}
		if _, err := filter.Parse(sc.Rule); err != nil {
			return fmt.Errorf("scripts[%d] %q: %w", i, sc.Name, err)
		}
	}
	for i, f := range c.Filters {
		if f.Name == "" {
			return fmt.Errorf("filters[%d]: name is required", i)
		}
		if _, err := filter.Parse(f.Rule); err != nil {
			return fmt.Errorf("filters[%d] %q: %w", i, f.Name, err)
		}
	}
	return nil
}

// CompiledFilters parses the filter rules (validated in Load).
func (c *Config) CompiledFilters() []filter.Filter {
	out := make([]filter.Filter, 0, len(c.Filters))
	for _, f := range c.Filters {
		r, _ := filter.Parse(f.Rule)
		badge := f.Icon != "" || f.ASCII != ""
		if f.Badge != nil {
			badge = *f.Badge
		}
		out = append(out, filter.Filter{Name: f.Name, Icon: f.Icon, ASCIIIcon: f.ASCII, IconName: f.IconName, Rule: r, Badge: badge})
	}
	return out
}

// CompiledScripts returns the scripts for the current OS (validated in Load).
func (c *Config) CompiledScripts() []scripts.Script {
	out := make([]scripts.Script, 0, len(c.Scripts))
	for _, sc := range c.Scripts {
		r, _ := filter.Parse(sc.Rule)
		out = append(out, scripts.Script{Name: sc.Name, Rule: r, Command: sc.Run.ForOS(),
			Mode: scripts.Mode(sc.Mode), PasswordStdin: sc.PasswordStdin, Default: sc.Default})
	}
	return out
}

// CompiledWidgets returns the widgets for the current OS.
func (c *Config) CompiledWidgets() []widget.Spec {
	var out []widget.Spec
	for _, w := range c.Widgets.Items {
		out = append(out, widget.Spec{Title: w.Title, Command: w.Run.ForOS(), Builtin: w.Builtin, Interval: w.Interval, Height: w.Height})
	}
	return out
}
