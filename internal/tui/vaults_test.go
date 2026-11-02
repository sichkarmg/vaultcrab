// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
)

// Two vaults: both in the status bar, the unlock dialog names the locked
// one, the card shows the vault of an item, and Settings → Vaults adds a
// third one to the config and connects it.
func TestSeveralVaultsUI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("vaults:\n  - {id: work, name: Work}\n"), 0o600)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	work := &lockedSrc{release: make(chan struct{})}
	close(work.release)
	var added []string
	newVault := func(sp config.VaultSpec) core.VaultOptions {
		added = append(added, sp.ID)
		var src core.Source = &fakeSrc{}
		if sp.ID == "work" {
			src = work
		}
		return core.VaultOptions{ID: sp.ID, Name: sp.Label(), Server: sp.Server,
			Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil },
			Login:   func() *exec.Cmd { return exec.Command("true") }}
	}
	svc := core.New(core.Options{Config: &cfg, ConfigPoll: -1, NewVault: newVault, VaultName: "Home",
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Stop)
	svc.Start(ctx)
	for svc.Status().State != core.Locked || svc.Vaults()[0].State != core.Online {
		select {
		case <-svc.Events():
		case <-time.After(2 * time.Second):
			t.Fatalf("vaults %+v", svc.Vaults())
		}
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true, ConfigPath: path})
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 34})
	m.onEvent()
	if st := ansi.Strip(m.statusBar()); !strings.Contains(st, "Home") || !strings.Contains(st, "Work locked (u)") {
		t.Fatalf("status bar %q", st)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "The vault Work is locked.") || !strings.Contains(v, "t · s") {
		t.Fatalf("unlock dialog:\n%s", v)
	}
	typeText(m, "pw")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(cmd().(tea.BatchMsg)[1]()) // the unlock itself
	for svc.Status().State != core.Online {
		select {
		case <-svc.Events():
		case <-time.After(2 * time.Second):
			t.Fatalf("not online: %+v", svc.Vaults())
		}
	}
	m.onEvent()
	if m.modal != nil {
		t.Fatalf("modal %T after the unlock", m.modal)
	}
	// the hosts of Work are under its folder; the card names the vault
	keys(m, "/", "w", "o", "r", "k", "enter")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "vault") || !strings.Contains(v, "Work · t · s") {
		t.Fatalf("card:\n%s", v)
	}

	// Settings → Vaults → a: add "lab"
	m.openSettings()
	s := m.modal.(*settingsModal)
	s.page = pageVaults
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Home") || !strings.Contains(v, "Work (work)") || !strings.Contains(v, "Add vault") {
		t.Fatalf("vaults page:\n%s", v)
	}
	checkFrame(t, m.View(), 130, 34)
	keys(m, "a")
	typeText(m, "lab")
	keys(m, "enter")
	typeText(m, "Lab")
	keys(m, "enter")
	typeText(m, "vault.example.org")
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("no bw login: %q", s.vp.err)
	}
	c, err := config.Load(path)
	if err != nil || len(c.Vaults) != 2 || c.Vaults[1].ID != "lab" || c.Vaults[1].Server != "https://vault.example.org" {
		t.Fatalf("config %+v %v", c.Vaults, err)
	}
	if vs := svc.Vaults(); len(vs) != 3 || vs[2].Label() != "Lab" {
		t.Fatalf("vaults %+v", vs)
	}
	// D twice removes it again
	keys(m, "D")
	if len(svc.Vaults()) != 3 {
		t.Fatal("removed on the first D")
	}
	keys(m, "D")
	if c, _ := config.Load(path); len(c.Vaults) != 1 || len(svc.Vaults()) != 2 {
		t.Fatalf("after remove: config %+v, %d vaults", c.Vaults, len(svc.Vaults()))
	}
}
