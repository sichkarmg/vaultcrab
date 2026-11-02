// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// editSource is a fakeSource that records edits.
type editSource struct {
	fakeSource
	emu     sync.Mutex
	folders []string // created
	updated []string // item IDs
	created []vault.HostEdit
	deleted []string
}

func (e *editSource) CreateHost(_ context.Context, h vault.HostEdit) (string, error) {
	e.emu.Lock()
	defer e.emu.Unlock()
	e.created = append(e.created, h)
	return "new1", nil
}
func (e *editSource) UpdateHost(_ context.Context, id string, h vault.HostEdit) error {
	e.emu.Lock()
	defer e.emu.Unlock()
	e.updated = append(e.updated, id+" folder="+h.FolderID)
	return nil
}
func (e *editSource) CreateFolder(_ context.Context, name string) (string, error) {
	e.emu.Lock()
	defer e.emu.Unlock()
	e.folders = append(e.folders, name)
	return "fnew", nil
}
func (e *editSource) DeleteItem(_ context.Context, id string) error {
	e.emu.Lock()
	defer e.emu.Unlock()
	e.deleted = append(e.deleted, id)
	return nil
}

// Two vaults: their items are merged (the second under a folder named after
// it), states are shown per vault, unlock, secrets and edits go to the
// vault of an item.
func TestSeveralVaults(t *testing.T) {
	home := &editSource{fakeSource: fakeSource{status: vault.Unlocked, secrets: map[string]string{"h1": "pw-home"},
		snap: model.Snapshot{Hosts: []model.Host{{ID: "h1", Name: "home-box", Addr: "192.0.2.1", Scheme: model.SSH}}}}}
	work := &editSource{fakeSource: fakeSource{status: vault.Locked, secrets: map[string]string{"h1": "pw-work"},
		snap: model.Snapshot{
			Folders: []model.Folder{{ID: "f1", Name: "Lab", Path: []string{"Lab"}}},
			Orgs:    []model.Org{{ID: "o1", Name: "Corp"}},
			Hosts: []model.Host{ // the same item ID as in home: kept apart
				{ID: "h1", Name: "work-box", Addr: "192.0.2.2", Scheme: model.SSH, FolderID: "f1", OrgID: "o1"},
				{ID: "h2", Name: "loose", Addr: "192.0.2.3", Scheme: model.SSH},
			}}}}
	s := New(Options{
		Connect: func(context.Context) (Source, func(), error) { return home, nil, nil },
		Vaults: []VaultOptions{{ID: "work", Name: "Work", Server: "https://vault.example.com",
			Connect: func(context.Context) (Source, func(), error) { return work, nil, nil }}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Locked) // the summary: one vault needs the user
	for i := 0; s.Vaults()[0].State != Online; i++ {
		if i == 500 {
			t.Fatal("home not online")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Status().State != Locked {
		t.Fatalf("summary %s", s.Status().State)
	}
	vs := s.Vaults()
	if len(vs) != 2 || vs[0].State != Online || vs[1].State != Locked || vs[1].Label() != "Work" {
		t.Fatalf("vaults %+v", vs)
	}
	if v, ok := s.LockedVault(); !ok || v.ID != "work" || v.Where() != "u · s" { // as bw reports it
		t.Fatalf("locked vault %+v", v)
	}
	if err := s.Unlock(ctx, []byte("master")); err != nil { // the locked one: work
		t.Fatal(err)
	}
	waitState(t, s, Online)

	snap := s.Snapshot()
	byName := map[string]model.Host{}
	for _, h := range snap.Hosts {
		byName[h.Name] = h
	}
	wb, loose := byName["work-box"], byName["loose"]
	if byName["home-box"].ID != "h1" || wb.ID != "work/h1" || loose.ID != "work/h2" {
		t.Fatalf("IDs %+v", byName)
	}
	if p := snap.FolderPath(&wb); strings.Join(p, "/") != "Work/Lab" {
		t.Errorf("folder of work-box %v", p)
	}
	if p := snap.FolderPath(&loose); strings.Join(p, "/") != "Work" {
		t.Errorf("folder of loose %v", p)
	}
	if o := snap.Org(wb.OrgID); o == nil || o.Name != "Corp" {
		t.Errorf("org %q", wb.OrgID)
	}
	if v := s.VaultOf("work/h1"); v.ID != "work" || s.VaultOf("h1").ID != "" {
		t.Errorf("VaultOf %q", v.ID)
	}
	for id, want := range map[string]string{"h1": "pw-home", "work/h1": "pw-work"} {
		sec, err := s.Secret(ctx, id)
		if err != nil || string(sec.Value) != want {
			t.Errorf("secret of %s: %q %v", id, sec.Value, err)
		}
		sec.Wipe()
	}

	// edits go to the vault of the item; folders without the vault name
	if _, err := s.SaveHost(ctx, "work/h1", HostEdit{Name: "work-box", OrgID: "work/o1"}, "Work/Lab/New"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(work.folders, []string{"Lab/New"}) || !slices.Equal(work.updated, []string{"h1 folder=fnew"}) || len(home.updated) != 0 {
		t.Fatalf("work: folders %q updated %q; home %q", work.folders, work.updated, home.updated)
	}
	if _, err := s.SaveHost(ctx, "work/h1", HostEdit{Name: "x"}, "Elsewhere"); err == nil {
		t.Error("moved into another vault")
	}
	id, err := s.SaveHost(ctx, "", HostEdit{Name: "added"}, "Work/Lab")
	if err != nil || id != "work/new1" || len(work.created) != 1 || work.created[0].FolderID != "f1" {
		t.Fatalf("create in work: %q %v %+v", id, err, work.created)
	}
	if id, err := s.SaveHost(ctx, "", HostEdit{Name: "added"}, ""); err != nil || id != "new1" || len(home.created) != 1 {
		t.Fatalf("create in home: %q %v", id, err)
	}
	if err := s.DeleteHost(ctx, "work/h2"); err != nil || !slices.Equal(work.deleted, []string{"h2"}) {
		t.Fatalf("delete: %v %q", err, work.deleted)
	}

	// removed: its items go away
	s.RemoveVault("work")
	if len(s.Vaults()) != 1 || len(s.Snapshot().Hosts) != 1 {
		t.Fatalf("after remove: %d vaults, %d hosts", len(s.Vaults()), len(s.Snapshot().Hosts))
	}
}
