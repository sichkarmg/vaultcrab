// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomColor(t *testing.T) {
	for in, want := range map[string]string{"#FF8800": "#ff8800", "#f80": "#ff8800", "208": "208", " 7 ": "7", "0": "0"} {
		if got, ok := CustomColor(in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "256", "-1", "#ff88", "#gggggg", "ff8800", "orange"} {
		if got, ok := CustomColor(in); ok {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
	for c, want := range map[string]string{"#ffffff": "0", "#000000": "15", "#ffaf00": "0", "#1e3a8a": "15", "15": "0", "4": "15", "231": "0", "16": "15", "250": "0", "236": "15"} {
		if got := ContrastText(c); got != want {
			t.Errorf("text on %s: %s, want %s", c, got, want)
		}
	}
	// every named color is unique and valid
	seen := map[string]bool{}
	for _, a := range Accents {
		if _, ok := CustomColor(a.Color); !ok || seen[a.Name] {
			t.Errorf("accent %+v", a)
		}
		seen[a.Name] = true
	}
	seen = map[string]bool{}
	for _, s := range Selections[1:] {
		_, okb := CustomColor(s.Bg)
		_, okf := CustomColor(s.Fg)
		if !okb || !okf || seen[s.Name] {
			t.Errorf("selection %+v", s)
		}
		seen[s.Name] = true
	}
}

func TestColorsInConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ui:\n  accent: orange\n  selection_color: \"#FFFFFF\"\n")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if AccentColor(c.UI.Accent) != "208" {
		t.Fatalf("accent %q", c.UI.Accent)
	}
	if bg, fg, ok := SelectionColors(c.UI.Selection); !ok || bg != "#ffffff" || fg != "0" {
		t.Fatalf("selection %q %q %v", bg, fg, ok)
	}
	write("ui:\n  accent: 208\n")
	if c, err = Load(path); err != nil || AccentColor(c.UI.Accent) != "208" {
		t.Fatalf("number: %q %v", c.UI.Accent, err)
	}
	write("ui:\n  accent: \"#12345\"\n")
	if _, err = Load(path); err == nil || !strings.Contains(err.Error(), "#rrggbb") {
		t.Fatalf("bad color: %v", err)
	}
}

func TestHostColor(t *testing.T) {
	c := Default()
	c.UI.FolderColors = map[string]string{"Prod": "red", "prod/db": "#ff8800", "Lab": "sky"}
	c.UI.TagColors = map[string]string{"Critical": "magenta"}
	for _, tc := range []struct {
		field  string
		tags   []string
		folder []string
		want   string
	}{
		{"", nil, nil, ""},
		{"", nil, []string{"prod"}, "1"},
		{"", nil, []string{"Prod", "web"}, "1"},           // a parent covers its subfolders
		{"", nil, []string{"PROD", "db", "x"}, "#ff8800"}, // the deepest folder with a color
		{"", []string{"linux", "critical"}, []string{"prod"}, "5"},
		{"Orange", []string{"critical"}, []string{"prod"}, "208"}, // the field first
		{"#ABC", nil, nil, "#aabbcc"},
		{"no-such", nil, []string{"lab"}, "75"}, // a bad field is ignored
		{"", nil, []string{"Production"}, ""},
	} {
		if got := c.HostColor(tc.field, tc.tags, tc.folder); got != tc.want {
			t.Errorf("%+v: %q", tc, got)
		}
	}
}

func TestHostColorsInConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("ui:\n  folder_colors:\n    Prod: red\n    Lab: \"#00ff00\"\n  tag_colors:\n    critical: 196\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.HostColor("", []string{"critical"}, nil) != "196" || c.HostColor("", nil, []string{"Lab"}) != "#00ff00" {
		t.Fatalf("%+v %+v", c.UI.FolderColors, c.UI.TagColors)
	}
	if err := os.WriteFile(path, []byte("ui:\n  folder_colors:\n    Prod: crimson\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "ui.folder_colors") {
		t.Fatalf("bad color: %v", err)
	}
}
