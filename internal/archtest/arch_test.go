// SPDX-License-Identifier: MIT

// Package archtest enforces the layering rules from docs/DEVELOPMENT.md.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var uiLibs = []string{"github.com/charmbracelet/bubbletea", "github.com/charmbracelet/lipgloss", "github.com/charmbracelet/bubbles"}

// imports returns the imports of non-test Go files per package directory.
func imports(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		for _, im := range f.Imports {
			out[pkg] = append(out[pkg], strings.Trim(im.Path.Value, `"`))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUILibrariesOnlyInTUI(t *testing.T) {
	for pkg, ims := range imports(t) {
		if pkg == "../tui" {
			continue
		}
		for _, im := range ims {
			for _, lib := range uiLibs {
				if strings.HasPrefix(im, lib) {
					t.Errorf("%s imports %s (only internal/tui may)", pkg, im)
				}
			}
		}
	}
}

func TestTUIUsesOnlyCore(t *testing.T) {
	for _, im := range imports(t)["../tui"] {
		if strings.HasPrefix(im, "github.com/sichkarmg/vaultcrab/internal/") && im != "github.com/sichkarmg/vaultcrab/internal/core" {
			t.Errorf("internal/tui imports %s (must go through internal/core)", im)
		}
	}
}

// The packages that hold the PIN, the stored master passwords and the
// secure store of the system run no programs: their secrets cannot end up
// on a command line or in the environment of a child.
func TestSecretStoresRunNoPrograms(t *testing.T) {
	all := imports(t)
	for _, pkg := range []string{"../keystore", "../quickunlock", "../biometric"} {
		ims, ok := all[pkg]
		if !ok {
			t.Fatalf("%s not found", pkg)
		}
		for _, im := range ims {
			if im == "os/exec" || im == "syscall" && pkg != "../keystore" {
				t.Errorf("%s imports %s", pkg, im)
			}
		}
	}
}
