// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddRemoveVault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("# mine\nui: {accent: red} # keep\n"), 0o600)
	if err := AddVault(path, VaultSpec{ID: "work", Name: "Work", Server: "https://vault.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := AddVault(path, VaultSpec{ID: "home"}); err != nil {
		t.Fatal(err)
	}
	if err := AddVault(path, VaultSpec{ID: "Bad ID"}); err == nil {
		t.Error("bad ID accepted")
	}
	if err := AddVault(path, VaultSpec{ID: "work"}); err == nil {
		t.Error("the same ID twice accepted")
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Vaults) != 2 || c.Vaults[0].Label() != "Work" || c.Vaults[0].Server != "https://vault.example.com" ||
		c.Vaults[1].Label() != "home" || c.Vaults[1].DataDir != filepath.Join(filepath.Dir(path), "bw-home") {
		t.Fatalf("vaults %+v", c.Vaults)
	}
	if c.UI.Accent != "red" {
		t.Error("the rest of the file was lost")
	}
	if err := RemoveVault(path, "work"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if c, _ := Load(path); len(c.Vaults) != 1 || c.Vaults[0].ID != "home" || !strings.Contains(string(b), "# keep") {
		t.Fatalf("after remove %+v\n%s", c.Vaults, b)
	}
}

func TestVaultValidation(t *testing.T) {
	for _, bad := range []string{
		"vaults: [{id: \"\"}]",
		"vaults: [{id: a}, {id: a}]",
		"vaults: [{id: x, env: [BW_SESSION=1]}]",
	} {
		p := filepath.Join(t.TempDir(), "c.yaml")
		os.WriteFile(p, []byte(bad+"\n"), 0o600)
		if _, err := Load(p); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}
