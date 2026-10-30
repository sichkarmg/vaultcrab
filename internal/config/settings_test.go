// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveAppearanceKeepsTheRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# my config\nui:\n  tabs_position: top # where the tabs are\n  prefix: ctrl+a\nicons:\n  set:\n    quit: {unicode: \"Q\", ascii: \"q\"}\n"
	if err := os.WriteFile(path, []byte(orig), 0o640); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	old := cfg.Appearance()
	if old.SSHIcon != SSHIcons[0] || old.Accent != "cyan" || old.IconMode != "auto" {
		t.Fatalf("defaults %+v", old)
	}
	a := old
	a.TabsBottom, a.SSHIcon, a.Accent, a.Widgets, a.ListWidth, a.Selection = true, Icon{"#", "#"}, "green", !old.Widgets, 50, "blue"
	if err := SaveAppearance(path, old, a); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{"# my config", "# where the tabs are", "prefix: ctrl+a", "tabs_position: bottom", "accent: green", `unicode: "#"`, "list_width: 50", "selection_color: blue"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Appearance(); got != a {
		t.Fatalf("reloaded %+v, want %+v", got, a)
	}
	if cfg.Icons.Set[IconQuit].Unicode != "Q" || cfg.UI.Prefix != "ctrl+a" {
		t.Fatal("other settings lost")
	}
}

func TestSaveAppearanceNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	cfg, _ := Load(path)
	old := cfg.Appearance()
	a := old
	a.IconMode = "ascii"
	if err := SaveAppearance(path, old, a); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.TrimSpace(string(b)) != "icons:\n  mode: ascii" {
		t.Fatalf("file:\n%s", b)
	}
	// unchanged settings are not written
	if err := SaveAppearance(path, a, a); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(path); string(b2) != string(b) {
		t.Fatalf("changed:\n%s", b2)
	}
}

func TestIconSetWith(t *testing.T) {
	cfg := Default()
	cfg.Icons.Mode = "unicode"
	s := cfg.IconSet().With("ascii", IconSSH, Icon{"λ", "L"})
	if !s.ASCII() || s.Get(IconSSH) != "L" || s.Get(IconQuit) != "Q" {
		t.Fatalf("%v %q", s.ASCII(), s.Get(IconSSH))
	}
	if cfg.IconSet().Get(IconSSH) != "❯" {
		t.Fatal("original changed")
	}
}
