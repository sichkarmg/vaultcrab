// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const launchersYAML = `# my vaultcrab
ui:
  prefix: ctrl+a # the screen habit
external:
  # the VNC client
  vnc:
    linux: [xtigervncviewer, "{host}::{port}"] # proxy
    password: proxy
  smb: {windows: [explorer.exe, '\\{host}\{path}']}
scripts:
  - name: ping
    run: {linux: [ping, -c, "4", "{host}"]}
  - name: web # opens the browser
    mode: external
    run:
      linux: [xdg-open, "http://{host}/"]
`

func TestSaveLaunchersKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(launchersYAML), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	old := cfg.Launchers()
	l := cfg.Launchers()
	vnc := l.External["vnc"]
	vnc.Set("linux", []string{"/opt/TigerVNC/bin/vncviewer", "-passwd", "{passfile}", "{host}::{port}"})
	vnc.Set("darwin", []string{"/Applications/TigerVNC.app/Contents/MacOS/vncviewer", "-passwd", "{passfile}", "{host}::{port}"})
	vnc.Password = "file"
	l.External["vnc"] = vnc
	l.External["rdp"] = Launch{PerOS: PerOS{Linux: []string{"xfreerdp", "/v:{host}:{port}"}}}
	smb := l.External["smb"]
	smb.Windows = nil // back to auto-detection
	l.External["smb"] = smb
	l.Scripts[1].Run.Set("windows", []string{"cmd", "/c", "start", "http://{host}/"})
	if slices.Equal(old.External["vnc"].Linux, vnc.Linux) {
		t.Fatal("Launchers shares the slices of the config")
	}
	if err := SaveLaunchers(path, old, l); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{"# my vaultcrab", "prefix: ctrl+a # the screen habit", "# the VNC client", "name: web # opens the browser",
		`linux: [/opt/TigerVNC/bin/vncviewer, -passwd, '{passfile}', '{host}::{port}']`, "password: file",
		"rdp:", `windows: [cmd, /c, start, 'http://{host}/']`, `run: {linux: [ping, -c, "4", "{host}"]}`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "explorer.exe") {
		t.Errorf("the emptied command is still there:\n%s", s)
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Launchers()
	if fmt.Sprint(got) != fmt.Sprint(l) {
		t.Errorf("reloaded\n%v\nwant\n%v", got, l)
	}
	if cfg.UI.Prefix != "ctrl+a" {
		t.Error("other settings lost")
	}
}

// Without scripts: in the file the built-in ones are in use: a change
// writes them out.
func TestSaveLaunchersDefaultScripts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := Load(path)
	old, l := cfg.Launchers(), cfg.Launchers()
	l.Scripts[0].Run.Set("linux", []string{"ping", "-c", "2", "{host}"})
	if err := SaveLaunchers(path, old, l); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Scripts; len(got) != 1 || got[0].Name != "ping" || strings.Join(got[0].Run.Linux, " ") != "ping -c 2 {host}" ||
		strings.Join(got[0].Run.Windows, " ") != "ping {host}" {
		t.Errorf("scripts %+v", got)
	}
}

// A change that would not load leaves the file as it was.
func TestSaveLaunchersInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(launchersYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load(path)
	old, l := cfg.Launchers(), cfg.Launchers()
	vnc := l.External["vnc"]
	vnc.Password = "argv" // no {password} in the command
	l.External["vnc"] = vnc
	err := SaveLaunchers(path, old, l)
	if err == nil || !strings.Contains(err.Error(), "needs {password}") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != launchersYAML {
		t.Errorf("file changed:\n%s", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// Readers (another vaultcrab, the reload of this one) never see a half
// written file: it is replaced by a rename.
func TestSaveLaunchersAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(launchersYAML+strings.Repeat("# padding to make the writes slower\n", 2000)), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var reads, bad atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// whole: the start, the end and everything in between
				b, err := readFile(path) // as Load does
				if err == nil && (!strings.HasPrefix(string(b), "# my vaultcrab") || !strings.HasSuffix(string(b), "slower\n") || len(b) < 70000) {
					err = fmt.Errorf("%d bytes", len(b))
				}
				if err != nil {
					bad.Add(1)
					t.Log(err)
				}
				reads.Add(1)
			}
		}()
	}
	cfg, _ := Load(path)
	cur := cfg.Launchers()
	for i := range 50 {
		next := cfg.Launchers()
		next.Scripts[0].Run.Set("linux", []string{"ping", "-c", fmt.Sprint(i + 1), "{host}"})
		if err := SaveLaunchers(path, cur, next); err != nil {
			t.Fatal(err)
		}
		cur = next
	}
	close(stop)
	wg.Wait()
	if bad.Load() > 0 {
		t.Errorf("%d of %d reads saw a broken file", bad.Load(), reads.Load())
	}
	cfg, _ = Load(path)
	if got := strings.Join(cfg.Scripts[0].Run.Linux, " "); got != "ping -c 50 {host}" {
		t.Errorf("last save %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".config-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}
