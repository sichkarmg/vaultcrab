// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// With log.enabled the vault states, unlock attempts and connections are
// written to the log file; the password never is.
func TestEventLog(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Log = config.Log{Enabled: true, File: filepath.Join(dir, "vaultcrab.log")}
	src := &fakeSource{status: vault.Locked, snap: model.Snapshot{Hosts: []model.Host{{ID: "h", Name: "box", Addr: "192.0.2.1", User: "bob", Scheme: model.SSH}}}}
	s := New(Options{Config: &cfg, ConfigPoll: -1, Connect: func(context.Context) (Source, func(), error) { return src, nil, nil }})
	t.Cleanup(func() { s.SetLogging(false) })
	if !s.LogEnabled() || s.LogFile() != cfg.Log.File {
		t.Fatalf("log not on: %v %q", s.LogEnabled(), s.LogFile())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Locked)
	s.Unlock(ctx, []byte("s3cret-wrong")) // the fake accepts "master"
	s.Unlock(ctx, []byte("master"))
	waitState(t, s, Online)
	var lines []string
	for range 100 {
		lines, _ = s.LogTail(100)
		if strings.Contains(strings.Join(lines, "\n"), "vault: syncing -> online") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"log: on", "vault: starting -> locked", "vault: unlock failed", "vault: unlocked", "-> online"} {
		if !strings.Contains(all, want) {
			t.Errorf("%q not logged:\n%s", want, all)
		}
	}
	if strings.Contains(all, "s3cret") || strings.Contains(all, "master\n") {
		t.Error("a password is in the log")
	}
	// off: nothing more is written, the file stays readable
	if err := s.SetLogging(false); err != nil || s.LogEnabled() {
		t.Fatalf("off: %v %v", err, s.LogEnabled())
	}
	logf("not written")
	if lines, _ := s.LogTail(100); !strings.HasSuffix(lines[len(lines)-1], "log: off") {
		t.Errorf("last line %q", lines[len(lines)-1])
	}
}
