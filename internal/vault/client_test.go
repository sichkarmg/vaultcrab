// SPDX-License-Identifier: MIT

package vault

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

func TestSnapshot(t *testing.T) {
	_, c := newFake(t, Unlocked)
	s, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Account != "t@test@https://vw.test" {
		t.Errorf("account = %q", s.Account)
	}
	got := map[string]model.Host{}
	for _, h := range s.Hosts {
		got[h.ID] = h
	}
	want := map[string]model.Host{
		"i1": {ID: "i1", Name: "user test", Scheme: model.SSH, User: "test", Addr: "192.0.2.133", Port: 22, FolderID: "f3",
			Fields:  map[string]string{"ssh": "true"},
			Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Revised: time.Date(2026, 10, 20, 10, 0, 0, 123e6, time.UTC)},
		"i2": {ID: "i2", Name: "org host", Scheme: model.SSH, User: "root", Addr: "fe80::1", Port: 2222, OrgID: "o1",
			CollectionIDs: []string{"c1", "c2"}, Favorite: true, Tags: []string{"db", "prod", "linux"},
			Fields: map[string]string{"ssh": "true", "tags": "db, prod;linux", "port": "2222"}},
		"i3": {ID: "i3", Name: "uri host", Scheme: model.SSH, User: "admin", Addr: "10.0.0.5", Port: 2200},
		"i4": {ID: "i4", Name: "vnc", Scheme: model.VNC, Addr: "10.0.0.6", Port: 5900},
		"i5": {ID: "i5", Name: "smb", Scheme: model.SMB, User: `DOM\user`, Addr: "fileserver", Port: 445, Path: "share/sub"},
	}
	if len(got) != len(want) {
		t.Errorf("hosts: got %d, want %d: %+v", len(got), len(want), s.Hosts)
	}
	for id, w := range want {
		g := got[id]
		if g.CollectionIDs == nil {
			g.CollectionIDs = nil
		}
		if len(g.CollectionIDs) == 0 {
			g.CollectionIDs = nil
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s:\n got %+v\nwant %+v", id, g, w)
		}
	}
	paths := map[string][]string{}
	for _, f := range s.Folders {
		paths[f.ID] = f.Path
	}
	if !reflect.DeepEqual(paths["f3"], []string{"buisness", "grup2"}) || !reflect.DeepEqual(paths["f4"], []string{"A", "B", "C"}) {
		t.Errorf("folder paths: %v", paths)
	}
	if len(s.Folders) != 4 {
		t.Errorf("No Folder must be skipped: %v", s.Folders)
	}
	if c := s.Collection("c2"); c == nil || !reflect.DeepEqual(c.Path, []string{"dev", "backend"}) || c.OrgID != "o1" {
		t.Errorf("collection: %+v", c)
	}
	// No secrets in the snapshot, whatever the serialization.
	b, _ := json.Marshal(s)
	if leak := hasSubstr(b, "TOPSECRET", "api-token"); leak != "" {
		t.Fatalf("snapshot leaks %q: %s", leak, b)
	}
}

func TestLockedAndUnlock(t *testing.T) {
	f, c := newFake(t, Locked)
	ctx := context.Background()
	if _, err := c.Snapshot(ctx); !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	if _, err := c.Secret(ctx, "i1"); !errors.Is(err, ErrLocked) {
		t.Fatalf("want ErrLocked, got %v", err)
	}
	bad := []byte("wrong")
	if err := c.Unlock(ctx, bad); err == nil {
		t.Fatal("unlock with wrong password succeeded")
	}
	if string(bad) != "\x00\x00\x00\x00\x00" {
		t.Error("password buffer not wiped")
	}
	if err := c.Unlock(ctx, []byte(`p"w\x`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(ctx); err != nil || f.syncs != 1 {
		t.Fatal(err, f.syncs)
	}
	if _, err := c.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSecret(t *testing.T) {
	_, c := newFake(t, Unlocked)
	ctx := context.Background()
	s, err := c.Secret(ctx, "i1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != secret.Password || string(s.Value) != "pa\"ss\\wörd😀" {
		t.Errorf("got %v %q", s.Kind, s.Value)
	}
	s.Wipe()
	k, err := c.Secret(ctx, "key")
	if err != nil {
		t.Fatal(err)
	}
	if k.Kind != secret.PrivateKey || string(k.Value) != "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAABBBB\n-----END OPENSSH PRIVATE KEY-----\n" {
		t.Errorf("key: %q", k.Value)
	}
	if _, err := c.Secret(ctx, "nope"); err == nil {
		t.Error("expected error for missing item")
	}
}

func TestUnavailable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1")
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestToHostUsernameForms(t *testing.T) {
	tests := []struct {
		user string
		u, a string
		port int
		ok   bool
	}{
		{"root@host", "root", "host", 22, true},
		{"root@host:2222", "root", "host", 2222, true},
		{"a@b@host", "a@b", "host", 22, true},
		{"root@[::1]:23", "root", "::1", 23, true},
		{"root@::1", "root", "::1", 22, true},
		{" root@host ", "root", "host", 22, true},
		{"@host", "", "", 0, false},
		{"root@", "", "", 0, false},
		{"host", "", "", 0, false},
	}
	for _, tt := range tests {
		u := tt.user
		it := bwItem{ID: "x", Type: 1, Fields: []bwField{{Name: "ssh", Value: ptr("true"), Type: 2}}, Login: &bwLogin{Username: &u}}
		h, ok := toHost(&it)
		if ok != tt.ok || h.User != tt.u || h.Addr != tt.a || h.Port != tt.port {
			t.Errorf("%q: got ok=%v %q %q %d", tt.user, ok, h.User, h.Addr, h.Port)
		}
	}
}

func TestToHostSchemes(t *testing.T) {
	tests := []struct {
		field, user, uri string
		scheme           model.Scheme
		addr             string
		port             int
		path             string
	}{
		{"rdp", "admin@win", "", model.RDP, "win", 3389, ""},
		{"ftp", "anon@files:2121", "", model.FTP, "files", 2121, ""},
		{"sftp", "bob@srv", "", model.SFTP, "srv", 22, ""},
		{"", "admin", "rdp://win:3390", model.RDP, "win", 3390, ""},
		{"", "bob", "ftp://files/pub/x", model.FTP, "files", 21, "pub/x"},
		{"", "bob", "SFTP://srv:2222/home", model.SFTP, "srv", 2222, "home"},
		{"", "bob", "https://example.com", "", "", 0, ""},
	}
	for _, tt := range tests {
		u := tt.user
		it := bwItem{ID: "x", Type: 1, Login: &bwLogin{Username: &u}}
		if tt.field != "" {
			it.Fields = []bwField{{Name: tt.field, Value: ptr("true"), Type: 2}}
		}
		if tt.uri != "" {
			it.Login.URIs = []bwURI{{URI: tt.uri}}
		}
		h, ok := toHost(&it)
		if ok != (tt.scheme != "") || h.Scheme != tt.scheme || h.Addr != tt.addr || h.Port != tt.port || h.Path != tt.path {
			t.Errorf("%s %s %s: got ok=%v %+v", tt.field, tt.user, tt.uri, ok, h)
		}
	}
}

func ptr(s string) *string { return &s }

func TestNotes(t *testing.T) {
	_, c := newFake(t, Unlocked)
	n, err := c.Notes(context.Background(), "i1")
	if err != nil || n != "TOPSECRET-NOTES" {
		t.Fatalf("notes %q %v", n, err)
	}
	if n, err := c.Notes(context.Background(), "i2"); err != nil || n != "" {
		t.Fatalf("no notes: %q %v", n, err)
	}
}
