// SPDX-License-Identifier: MIT

//go:build !windows

package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// A changed config file is picked up without a restart: the next client
// started uses the new arguments, the next script run the new command. A
// broken file keeps the previous config working and is reported.
func TestConfigHotReload(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	client := filepath.Join(dir, "client")
	if err := os.WriteFile(client, []byte("#!/bin/sh\necho \"$@\" >> "+out+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	write := func(body string) {
		t.Helper()
		// a different size: the change is seen even within the mtime resolution
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("external:\n  smb: {linux: [" + client + ", first, '{host}']}\nscripts:\n  - {name: hello, run: {linux: [echo, one]}}\n")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	l := &connect.Launcher{Specs: LaunchSpecs(cfg), Settle: 50 * time.Millisecond}
	h := model.Host{ID: "s1", Name: "files", Scheme: model.SMB, Addr: "fs1"}
	src := &fakeSource{status: vault.Unlocked, snap: model.Snapshot{Hosts: []model.Host{h}}}
	s := New(Options{
		Connect:     func(context.Context) (Source, func(), error) { return src, nil, nil },
		Launch:      l.Launch,
		Config:      &cfg,
		ApplyConfig: func(c config.Config) { l.SetSpecs(LaunchSpecs(c)) },
		ConfigPoll:  20 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	launched := func() []string {
		t.Helper()
		if err := s.LaunchExternal(ctx, h); err != nil {
			t.Fatal(err)
		}
		for range 200 {
			if b, _ := os.ReadFile(out); len(b) > 0 {
				os.Remove(out)
				return strings.Fields(string(b))
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the client did not run")
		return nil
	}
	if got := launched(); !slices.Equal(got, []string{"first", "fs1"}) {
		t.Fatalf("first launch %q", got)
	}
	seq := s.ConfigStatus().Seq

	// edited by hand: picked up by the watcher
	write("external:\n  smb: {linux: [" + client + ", second, '{host}', again]}\nscripts:\n  - {name: hello, run: {linux: [echo, two]}}\nssh: {timeout: 3s}\n")
	st := waitConfig(t, s, seq)
	if st.Err != nil || !slices.Equal(st.Restart, []string{"ssh"}) {
		t.Fatalf("status %+v", st)
	}
	if got := launched(); !slices.Equal(got, []string{"second", "fs1", "again"}) {
		t.Fatalf("after the reload %q", got)
	}
	if sc := s.Scripts(h); len(sc) != 1 || strings.Join(sc[0].Command, " ") != "echo two" {
		t.Fatalf("scripts %+v", sc)
	}

	// broken: reported, the previous config stays in use
	write("external:\n  smb: {linux: [" + client + ", third]}\n  nope: {}\n")
	st = waitConfig(t, s, st.Seq)
	if st.Err == nil || !strings.Contains(st.Err.Error(), `unknown scheme "nope"`) {
		t.Fatalf("status %+v", st)
	}
	if got := launched(); !slices.Equal(got, []string{"second", "fs1", "again"}) {
		t.Fatalf("after a broken config %q", got)
	}

	// the terminal window lost the focus: not checked until it is back
	s.SetConfigWatch(false)
	time.Sleep(50 * time.Millisecond) // a check under way ends
	seq = s.ConfigStatus().Seq
	write("external:\n  smb: {linux: [" + client + ", fourth, '{host}']}\n")
	time.Sleep(150 * time.Millisecond)
	if s.ConfigStatus().Seq != seq {
		t.Fatal("checked while the window has no focus")
	}
	s.SetConfigWatch(true)
	if st = waitConfig(t, s, seq); st.Err != nil {
		t.Fatal(st.Err)
	}
	if got := launched(); !slices.Equal(got, []string{"fourth", "fs1"}) {
		t.Fatalf("after the focus came back %q", got)
	}

	// r in the host list: ReloadConfig (the file did not change)
	if err := s.ReloadConfig(); err != nil || s.ConfigStatus().Seq != st.Seq+1 {
		t.Fatalf("reload: %v %+v", err, s.ConfigStatus())
	}
}

func waitConfig(t *testing.T, s *Service, seq int) ConfigStatus {
	t.Helper()
	for range 300 {
		if st := s.ConfigStatus(); st.Seq != seq {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the config was not reloaded")
	return ConfigStatus{}
}

// Save from the settings: written to the file and in use at once.
func TestSaveLaunchersApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, _ := config.Load(path)
	var applied config.Config
	s := New(Options{Config: &cfg, ConfigPoll: -1, ApplyConfig: func(c config.Config) { applied = c }})
	old, l := cfg.Launchers(), cfg.Launchers()
	l.External["vnc"] = config.Launch{PerOS: config.PerOS{Linux: []string{"xtigervncviewer", "{host}::{port}"}}}
	if err := s.SaveLaunchers(old, l); err != nil {
		t.Fatal(err)
	}
	if got := LaunchSpecs(applied)[model.VNC].Command; strings.Join(got, " ") != "xtigervncviewer {host}::{port}" {
		t.Fatalf("applied %q", got)
	}
	if got := s.Config().External["vnc"].Linux; len(got) != 2 {
		t.Fatalf("config in use %q", got)
	}
}

// A script with {password}: the password is in the started command line, the
// tab of a terminal script shows it masked.
func TestScriptPasswordArg(t *testing.T) {
	h := model.Host{ID: "h", Name: "box", Addr: "fs1", User: "bob"}
	src := &fakeSource{status: vault.Unlocked, snap: model.Snapshot{Hosts: []model.Host{h}}, secrets: map[string]string{"h": "p@ss"}}
	var got []string
	s := New(Options{
		Connect:    func(context.Context) (Source, func(), error) { return src, nil, nil },
		ConfigPoll: -1,
		Start: func(_ context.Context, argv []string, _ bool, _ func(context.Context) (Secret, error)) error {
			got = argv
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	cfg := config.Config{Scripts: []config.ScriptSpec{
		{Name: "ext", Mode: "external", Run: config.PerOS{Linux: []string{"x", "smb://{user}:{password}@{host}/", "{password}"}}},
		{Name: "term", Run: config.PerOS{Linux: []string{"echo", "{password}"}}},
	}}
	sc := cfg.CompiledScripts()
	if _, err := s.RunScript(ctx, h, sc[0], 80, 24); err != nil {
		t.Fatal(err)
	}
	if want := []string{"x", "smb://bob:p%40ss@fs1/", "p@ss"}; !slices.Equal(got, want) {
		t.Errorf("started %q", got)
	}
	sess, err := s.RunScript(ctx, h, sc[1], 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if c := sess.Host.Fields["command"]; c != "echo ******" {
		t.Errorf("shown %q", c)
	}
}
