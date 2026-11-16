// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemes(t *testing.T) {
	seen := map[string]bool{}
	for _, th := range Themes {
		if seen[th.Name] {
			t.Errorf("%s twice", th.Name)
		}
		seen[th.Name] = true
		cols := []string{th.Accent, th.Border, th.OK, th.Warn, th.Err, th.Activity, th.Org}
		if th.SelBg != "" {
			cols = append(cols, th.SelBg, th.SelFg)
		}
		for _, c := range cols {
			if got, ok := CustomColor(c); !ok || got != c {
				t.Errorf("%s: color %q", th.Name, c)
			}
		}
	}
	// the default theme is the look from before themes
	d := Palette("default", FromTheme, FromTheme)
	if d.Accent != "6" || d.SelBg != "" || d.OK != "2" || d.Err != "1" || d.Border != "8" {
		t.Fatalf("default %+v", d)
	}
	// an accent or selection of the settings replaces the theme's
	p := Palette("nord", "orange", FromTheme)
	if p.Accent != "208" || p.SelBg != "#5e81ac" || p.OK != "#a3be8c" {
		t.Fatalf("nord + orange %+v", p)
	}
	p = Palette("nord", FromTheme, "reverse")
	if p.Accent != "#88c0d0" || p.SelBg != "" {
		t.Fatalf("nord + reverse %+v", p)
	}
	if p := Palette("no-such", FromTheme, FromTheme); p.Name != "default" {
		t.Fatalf("unknown %+v", p)
	}
}

func TestThemeInConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ui:\n  theme: dracula\n")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.UI.Theme != "dracula" || c.UI.Accent != FromTheme || c.UI.Selection != FromTheme {
		t.Fatalf("%q %q %q", c.UI.Theme, c.UI.Accent, c.UI.Selection)
	}
	// an older config with an accent of its own keeps it over the theme
	write("ui:\n  theme: dracula\n  accent: green\n")
	if c, err = Load(path); err != nil || Palette(c.UI.Theme, c.UI.Accent, c.UI.Selection).Accent != "2" {
		t.Fatalf("accent: %v %+v", err, c.UI)
	}
	write("ui:\n  theme: neon\n")
	if _, err = Load(path); err == nil || !strings.Contains(err.Error(), "ui.theme") || !strings.Contains(err.Error(), "catppuccin") {
		t.Fatalf("bad theme: %v", err)
	}
}
