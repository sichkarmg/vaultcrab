// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyDir(t *testing.T) {
	src := filepath.Join(t.TempDir(), "old")
	if err := os.MkdirAll(filepath.Join(src, "bw", "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config.yaml"), []byte("tabs: top\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bw", "data", "data.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "new")
	if err := CopyDir(src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "config.yaml"))
	if err != nil || string(b) != "tabs: top\n" {
		t.Fatalf("config.yaml: %q %v", b, err)
	}
	fi, err := os.Stat(filepath.Join(dst, "bw", "data", "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(src, "config.yaml")); err != nil {
		t.Error("source changed:", err)
	}
	if err := CopyDir(src, dst); err == nil {
		t.Error("copied over an existing directory")
	}
}

func TestLegacyDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base) // Linux
	t.Setenv("HOME", base)            // macOS: ~/Library/Application Support
	t.Setenv("AppData", base)         // Windows
	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	dir := filepath.Join(cfg, "vaultcrab")
	if _, ok := LegacyDir(dir); ok {
		t.Fatal("no legacy dir yet")
	}
	if err := os.MkdirAll(filepath.Join(cfg, legacyDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if old, ok := LegacyDir(dir); !ok || old != filepath.Join(cfg, legacyDirName) {
		t.Fatalf("legacy dir %q %v", old, ok)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ok := LegacyDir(dir); ok {
		t.Error("offered although the new dir exists")
	}
}
