// SPDX-License-Identifier: MIT

//go:build integration

package vault

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// Integration tests run against a real `bw serve` with a TEST vault seeded by
// scripts/seed-vault.sh. They are built only with `-tags integration` and need
// bw on PATH (or VAULTCRAB_IT_BW_PATH), logged in, and BW_SESSION in the
// environment. bw settings such as BITWARDENCLI_APPDATA_DIR are inherited.
// scripts/e2e.sh runs them on the test VMs.

func itServeOptions() ServeOptions {
	return ServeOptions{BW: os.Getenv("VAULTCRAB_IT_BW_PATH")}
}

// TestIntegrationBwServe checks the snapshot of the seeded test vault.
func TestIntegrationBwServe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	srv, err := StartServer(ctx, itServeOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	c := NewClient(srv.URL)
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != Unlocked {
		t.Fatalf("status %s (export BW_SESSION)", st.Status)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for i, h := range snap.Hosts {
		byName[h.Name] = i
	}
	for _, n := range []string{"user test", "vaultcrab-test-mint-password", "vaultcrab-test-mint-key-rsa-uri", "vaultcrab-test-org-mint", "vaultcrab-test-mint-vnc", "vaultcrab-test-win-smb"} {
		if _, ok := byName[n]; !ok {
			t.Errorf("host %q not found", n)
		}
	}
	for _, n := range []string{"vaultwarden", "vaultcrab-test-not-a-host"} {
		if _, ok := byName[n]; ok {
			t.Errorf("%q must not be a host", n)
		}
	}
	org := snap.Hosts[byName["vaultcrab-test-org-mint"]]
	if org.OrgID == "" || len(org.CollectionIDs) != 2 {
		t.Errorf("org host: %+v", org)
	}
	if f := snap.FolderPath(&snap.Hosts[byName["vaultcrab-test-mint-password"]]); strings.Join(f, "/") != "vaultcrab-test/Lab/Linux/Mint" {
		t.Errorf("folder path %v", f)
	}
	b, _ := json.Marshal(snap)
	key, err := c.Secret(ctx, snap.Hosts[byName["vaultcrab-test-mint-key-rsa-uri"]].ID)
	if err != nil {
		t.Fatal(err)
	}
	if key.Kind != 1 {
		t.Error("RSA key not detected")
	}
	if strings.Contains(string(b), "PRIVATE KEY") {
		t.Error("snapshot contains a key")
	}
	key.Wipe()
}

// TestIntegrationEdit creates, edits and deletes a vaultcrab-test-it-* item in
// the test vault (same requirements as TestIntegrationBwServe).
func TestIntegrationEdit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	srv, err := StartServer(ctx, itServeOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	c := NewClient(srv.URL)
	before, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fid := ""
	for _, f := range before.Folders {
		if f.Name == "vaultcrab-test/IT" {
			fid = f.ID
		}
	}
	if fid == "" {
		if fid, err = c.CreateFolder(ctx, "vaultcrab-test/IT"); err != nil {
			t.Fatal(err)
		}
	}
	id, err := c.CreateHost(ctx, HostEdit{Name: "vaultcrab-test-it-edit", Scheme: model.SSH, User: "u", Addr: "192.0.2.133",
		Tags: []string{"it"}, FolderID: fid, Password: []byte("pw-1")})
	if err != nil {
		t.Fatal(err)
	}
	defer c.DeleteItem(context.Background(), id)
	if err := c.UpdateHost(ctx, id, HostEdit{Name: "vaultcrab-test-it-edit2", Scheme: model.RDP, User: "admin", Addr: "192.0.2.133", Port: 3390, FolderID: fid}); err != nil {
		t.Fatal(err)
	}
	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got *model.Host
	for i := range snap.Hosts {
		if snap.Hosts[i].ID == id {
			got = &snap.Hosts[i]
		}
	}
	if got == nil || got.Name != "vaultcrab-test-it-edit2" || got.Scheme != model.RDP || got.Port != 3390 || got.User != "admin" || got.FolderID != fid {
		t.Fatalf("after edit: %+v", got)
	}
	sec, err := c.Secret(ctx, id)
	if err != nil || string(sec.Value) != "pw-1" {
		t.Errorf("password not kept: %v", err)
	}
	sec.Wipe()
	// copy: the password comes from the original
	cid, err := c.CreateHost(ctx, HostEdit{CopyOf: id, Name: "vaultcrab-test-it-edit2 copy1", Scheme: model.RDP, User: "admin", Addr: "192.0.2.133", FolderID: fid})
	if err != nil {
		t.Fatal(err)
	}
	defer c.DeleteItem(context.Background(), cid)
	sec, err = c.Secret(ctx, cid)
	if err != nil || string(sec.Value) != "pw-1" {
		t.Errorf("password not copied: %v", err)
	}
	sec.Wipe()
}
