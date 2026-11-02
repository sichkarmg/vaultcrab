// SPDX-License-Identifier: MIT

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/widget"
)

func write(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsWhenMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.TabsPosition != "top" || c.Terminal.Scrollback != 10000 || len(c.Filters) == 0 || c.UI.Keys[ActSplitRight] != "|" {
		t.Errorf("defaults: %+v", c)
	}
	if !strings.HasSuffix(c.SSH.KnownHosts, "known_hosts") {
		t.Error(c.SSH.KnownHosts)
	}
	if len(c.CompiledFilters()) != len(Default().Filters) {
		t.Error("compiled filters")
	}
}

// The default filter tabs are "All" plus one per connection type, each with
// the type's icon.
func TestDefaultSchemeFilters(t *testing.T) {
	var names []string
	for _, f := range Default().Filters {
		names = append(names, f.Name)
		if f.Name != "All" && (f.Icon != DefaultIcons[strings.ToLower(f.Name)].Unicode || f.ASCII == "") {
			t.Errorf("%s: icon %q/%q", f.Name, f.Icon, f.ASCII)
		}
	}
	if got := strings.Join(names, " "); got != "All SSH VNC SMB FTP SFTP RDP" {
		t.Errorf("filters: %s", got)
	}
	if _, err := Load(write(t, "external:\n  rdp: {linux: [xfreerdp, '/v:{host}']}\n  sftp: {windows: [winscp]}\n")); err != nil {
		t.Error(err)
	}
	for _, ok := range []string{
		"external:\n  vnc: {password: file, linux: [vncviewer, -passwd, '{passfile}', '{host}::{port}']}\n",
		"external:\n  vnc: {password: url, darwin: [open, '{url}']}\n",
		"external:\n  vnc: {password: proxy}\n",
	} {
		if _, err := Load(write(t, ok)); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if _, err := Load(write(t, "external:\n  ssh: {linux: [ssh]}\n")); err == nil {
		t.Error("external ssh accepted")
	}
}

func TestScripts(t *testing.T) {
	if d := Default(); len(d.Scripts) != 1 || d.Scripts[0].Name != "ping" || len(d.CompiledScripts()[0].Command) == 0 {
		t.Errorf("default scripts: %+v", d.Scripts)
	}
	c, err := Load(write(t, "scripts:\n  - {name: winbox, rule: 'tag:mikrotik', mode: external, password_stdin: true, run: {linux: [winbox, '{host}']}}\n  - {name: top, run: {linux: [ssh, '{user}@{host}', top]}, default: true}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Scripts) != 2 || c.Scripts[1].Mode != "terminal" {
		t.Fatalf("scripts replace the defaults: %+v", c.Scripts)
	}
	for name, body := range map[string]string{
		"no name":    "scripts:\n  - {run: {linux: [x]}}\n",
		"bad mode":   "scripts:\n  - {name: a, mode: gui, run: {linux: [x]}}\n",
		"pw in tab":  "scripts:\n  - {name: a, password_stdin: true, run: {linux: [x]}}\n",
		"bad rule":   "scripts:\n  - {name: a, rule: 'scheme:gopher', run: {linux: [x]}}\n",
		"unknown os": "scripts:\n  - {name: a, run: {plan9: [x]}}\n",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestEmptyFile(t *testing.T) {
	if _, err := Load(write(t, "")); err != nil {
		t.Fatal(err)
	}
}

func TestOverrides(t *testing.T) {
	c, err := Load(write(t, `
ui:
  tabs_position: bottom
  keys:
    split_right: v
icons:
  mode: ascii
  set:
    connected: {unicode: "✓"}
terminal:
  scrollback: 500
ssh:
  keepalive: 5s
bw:
  env: ["NODE_EXTRA_CA_CERTS=/x.pem"]
filters:
  - {name: Mine, icon: "M", ascii: m, rule: "tag:mine"}
external:
  vnc: {linux: [remmina, "vnc://{host}"]}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.TabsPosition != "bottom" || c.UI.Keys[ActSplitRight] != "v" || c.UI.Keys[ActSplitDown] != "-" {
		t.Errorf("ui %+v", c.UI)
	}
	if c.Terminal.Scrollback != 500 || c.SSH.Keepalive != 5*time.Second {
		t.Error("terminal/ssh")
	}
	if len(c.Filters) != 1 || c.Filters[0].Name != "Mine" {
		t.Errorf("filters must replace defaults: %+v", c.Filters)
	}
	if c.External["vnc"].Linux[0] != "remmina" {
		t.Errorf("external merge: %+v", c.External)
	}
	is := c.IconSet()
	if !is.ASCII() || is.Get(IconConnected) != "*" {
		t.Error("ascii icons")
	}
	c.Icons.Mode = "unicode"
	if c.IconSet().Get(IconConnected) != "✓" {
		t.Error("icon override")
	}
}

func TestErrors(t *testing.T) {
	for name, body := range map[string]string{
		"typo":          "ui:\n  tabs_positon: top\n",
		"position":      "ui:\n  tabs_position: left\n",
		"icons":         "icons:\n  mode: emoji\n",
		"filter":        "filters:\n  - {name: x, rule: 'scheme:gopher'}\n",
		"noname":        "filters:\n  - {rule: 'tag:x'}\n",
		"secret env":    "bw:\n  env: [BW_SESSION=abc]\n",
		"bad action":    "ui:\n  keys: {explode: e}\n",
		"vnc mode":      "external:\n  vnc: {password: clipboard}\n",
		"rdp mode":      "external:\n  rdp: {password: proxy}\n",
		"tab width":     "ui: {tab_width: 3}\n",
		"no {passfile}": "external:\n  vnc: {password: file, linux: [vncviewer, '{host}::{port}']}\n",
		"no {password}": "external:\n  vnc: {password: argv, darwin: [x, '{host}']}\n",
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestEveryIconHasASCII(t *testing.T) {
	for k, v := range DefaultIcons {
		if v.ASCII == "" || v.Unicode == "" {
			t.Errorf("icon %s incomplete", k)
		}
		for _, r := range v.ASCII {
			if r > 127 {
				t.Errorf("icon %s ascii variant is not ASCII", k)
			}
		}
	}
	for _, f := range Default().Filters {
		for _, r := range f.ASCII {
			if r > 127 {
				t.Errorf("filter %s ascii icon", f.Name)
			}
		}
		if f.Icon != "" && f.ASCII == "" {
			t.Errorf("filter %s has no ASCII icon", f.Name)
		}
	}
}

func TestUnicodeCapable(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		m    map[string]string
		goos string
		want bool
	}{
		{map[string]string{"LANG": "ru_RU.UTF-8"}, "linux", true},
		{map[string]string{"LANG": "C"}, "linux", false},
		{map[string]string{"LANG": "en_US.UTF-8", "TERM": "linux"}, "linux", false},
		{map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, "linux", false},
		{map[string]string{}, "windows", false},
		{map[string]string{"WT_SESSION": "x"}, "windows", true},
		{map[string]string{}, "darwin", true},
	}
	for i, c := range cases {
		if got := UnicodeCapable(env(c.m), c.goos); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := Load("../../docs/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Filters) != len(Default().Filters) {
		t.Errorf("example filters differ from defaults: %d", len(c.Filters))
	}
}

// The default widget scripts run on this OS and print something.
func TestDefaultWidgetsRun(t *testing.T) {
	c := Default()
	for _, w := range c.CompiledWidgets() {
		if w.Builtin != "" {
			continue // no command
		}
		// generous: the first PowerShell start on a CI runner can take >10 s
		lines, err := widget.Run(context.Background(), w.Command, 30*time.Second)
		if errors.Is(err, widget.ErrTimeout) && runtime.GOOS == "windows" && os.Getenv("CI") != "" {
			// WMI (Get-CimInstance) on GitHub's Windows runners sometimes
			// takes longer than any sane limit; the VM and real machines
			// answer in a second or two.
			t.Skipf("%s: %v on the CI runner", w.Title, err)
		}
		if err != nil || len(lines) < 2 {
			t.Errorf("%s: %v %q", w.Title, err, lines)
		}
		if w.Title == "System" && runtime.GOOS == "linux" && len(lines) != 5 {
			t.Errorf("system info lines: %q", lines) // host, up, load, mem, disk
		}
		t.Logf("%s: %q", w.Title, lines)
	}
	if _, err := Load(write(t, "widgets:\n  items:\n    - {title: x, interval: 10ms, run: {linux: [true]}}\n")); err == nil {
		t.Error("tiny interval accepted")
	}
	if _, err := Load(write(t, "widgets:\n  items:\n    - {title: x, builtin: calendar}\n")); err == nil {
		t.Error("unknown builtin accepted")
	}
	c2, err := Load(write(t, "widgets:\n  width: 50\n  items:\n    - {title: mine, run: {linux: [echo, hi]}}\n"))
	if err != nil || len(c2.Widgets.Items) != 1 || c2.Widgets.Width != 50 || !c2.Widgets.Show {
		t.Errorf("custom widgets: %+v %v", c2.Widgets, err)
	}
}
