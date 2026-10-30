// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// editSrc is a fake vault that also implements core.Editor.
type editSrc struct {
	fakeSrc
	mu      sync.Mutex
	created []core.HostEdit
	updated map[string]core.HostEdit
	folders []string
	deleted []string
}

func (e *editSrc) CreateHost(_ context.Context, h core.HostEdit) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h.Password = append([]byte(nil), h.Password...)
	e.created = append(e.created, h)
	return "new", nil
}
func (e *editSrc) UpdateHost(_ context.Context, id string, h core.HostEdit) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if h.Password != nil {
		h.Password = append([]byte(nil), h.Password...)
	}
	e.updated[id] = h
	return nil
}
func (e *editSrc) CreateFolder(_ context.Context, name string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.folders = append(e.folders, name)
	return "fnew", nil
}

func typeText(m *Model, s string) {
	for _, r := range s {
		keys(m, string(r))
	}
}

// runCmd runs cmd like Bubble Tea, the commands of a batch too, and passes
// their messages to m (the commands those return are not run).
func runCmd(m *Model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			runCmd(m, c)
		}
		return
	}
	m.Update(msg)
}

// enterAndRun presses Enter and runs the returned command like Bubble Tea.
func enterAndRun(t *testing.T, m *Model) {
	t.Helper()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("no save command: %s", m.modal.(*hostForm).err)
	}
	runCmd(m, cmd)
	if m.modal != nil {
		t.Fatalf("form still open: %s", m.modal.(*hostForm).err)
	}
}

func TestEditAndAddItem(t *testing.T) {
	src := &editSrc{updated: map[string]core.HostEdit{}}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "e")
	f, ok := m.modal.(*hostForm)
	if !ok {
		t.Fatal("no edit form")
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"Edit alpha", "10.0.0.1", "Lab/Linux", "unchanged"} {
		if !strings.Contains(v, want) {
			t.Errorf("form misses %q:\n%s", want, v)
		}
	}
	checkFrame(t, m.View(), 120, 34)
	// change the port, keep the password
	f.setFocus(fPort)
	typeText(m, "2222")
	enterAndRun(t, m)
	src.mu.Lock()
	u, ok := src.updated["1"]
	src.mu.Unlock()
	if !ok || u.Port != 2222 || u.Addr != "10.0.0.1" || u.Password != nil || u.FolderID != "f" {
		t.Fatalf("update %+v", u)
	}
	if !strings.Contains(ansi.Strip(m.View()), "saved alpha") {
		t.Error("no confirmation")
	}
	// add: type rdp, new folder, personal
	keys(m, "a")
	f = m.modal.(*hostForm)
	typeText(m, "new-rdp")
	keys(m, "tab")
	for core.Schemes[f.scheme] != core.SchemeRDP {
		m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	f.setFocus(fHost)
	typeText(m, "win")
	f.setFocus(fFolder)
	f.in[fFolder].SetValue("New/Sub")
	f.setFocus(fPassword)
	typeText(m, "s3cret")
	enterAndRun(t, m)
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.created) != 1 || src.created[0].Scheme != core.SchemeRDP || src.created[0].Addr != "win" ||
		string(src.created[0].Password) != "s3cret" || src.created[0].FolderID != "fnew" {
		t.Fatalf("created %+v", src.created)
	}
	if len(src.folders) == 0 || src.folders[0] != "New/Sub" {
		t.Errorf("folder not created: %v", src.folders)
	}
}

func TestEditNeedsOnline(t *testing.T) {
	m, svc := newTestModel(t, false) // fakeSrc does not implement Editor
	_ = svc
	keys(m, "e")
	if m.modal != nil {
		t.Fatal("form opened without an editable vault")
	}
	if !strings.Contains(ansi.Strip(m.View()), "need the vault online") {
		t.Error("no explanation")
	}
}

// C opens the new-item form prefilled from the selected item, named
// "<name> copyN"; the vault copies the password and the rest.
func TestCopyItem(t *testing.T) {
	src := &editSrc{updated: map[string]core.HostEdit{}}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "C")
	f, ok := m.modal.(*hostForm)
	if !ok || f.in[fName].Value() != "alpha copy1" || f.in[fHost].Value() != "10.0.0.1" || f.in[fFolder].Value() != "Lab/Linux" {
		t.Fatalf("copy form: %+v", ok)
	}
	if !strings.Contains(ansi.Strip(m.View()), "copied from alpha") {
		t.Error("password hint missing")
	}
	enterAndRun(t, m)
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.created) != 1 || src.created[0].CopyOf != "1" || src.created[0].Name != "alpha copy1" || src.created[0].Password != nil {
		t.Fatalf("created %+v", src.created)
	}
}

func (e *editSrc) DeleteItem(_ context.Context, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deleted = append(e.deleted, id)
	return nil
}

func TestDeleteItem(t *testing.T) {
	src := &editSrc{updated: map[string]core.HostEdit{}}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "D")
	d, ok := m.modal.(*deleteModal)
	if !ok || d.host.Name != "alpha" {
		t.Fatalf("modal %T", m.modal)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Delete alpha?") || !strings.Contains(v, "[ Cancel ]") {
		t.Fatalf("view:\n%s", v)
	}
	// Enter on the default button cancels
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || m.modal != nil {
		t.Fatal("Enter did not cancel")
	}
	keys(m, "D")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("y did not delete")
	}
	m.Update(cmd())
	if m.modal != nil || len(src.deleted) != 1 || src.deleted[0] != "1" {
		t.Fatalf("deleted %v", src.deleted)
	}
}

// orgSrc is an editable vault with an organization: alpha is personal,
// beta is in collection c1 of the organization.
type orgSrc struct{ editSrc }

func (o *orgSrc) Snapshot(ctx context.Context) (core.Snapshot, error) {
	s, _ := o.editSrc.Snapshot(ctx)
	s.Orgs = []core.Org{{ID: "o1", Name: "Team"}}
	s.Collections = []core.Collection{{ID: "c1", OrgID: "o1", Name: "Ops", Path: []string{"Ops"}}, {ID: "c2", OrgID: "o1", Name: "Dev", Path: []string{"Dev"}}}
	s.Hosts = append(s.Hosts, core.Host{ID: "3", Name: "beta", Scheme: core.SchemeSSH, Addr: "10.0.0.3", OrgID: "o1", CollectionIDs: []string{"c1"}})
	return s, nil
}

// Editing moves a personal item into an organization (with a warning) or
// changes the collection of an organization item; saving shows progress.
func TestEditOrganization(t *testing.T) {
	src := &orgSrc{editSrc{updated: map[string]core.HostEdit{}}}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 130, Height: 40})
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "e")
	f := m.modal.(*hostForm)
	if !slices.Contains(f.fields(), fOrg) {
		t.Fatal("no organization field for a personal item")
	}
	f.setFocus(fOrg)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Team") || !strings.Contains(v, "cannot move it back") || !strings.Contains(v, "Ops") {
		t.Fatalf("move not offered:\n%s", v)
	}
	// saving: the progress instead of the fields
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Saving and moving to Team") || strings.Contains(v, "Password") {
		t.Errorf("no progress:\n%s", v)
	}
	runCmd(m, cmd)
	if u := src.updated["1"]; u.OrgID != "o1" || len(u.CollectionIDs) != 1 || u.CollectionIDs[0] != "c1" {
		t.Fatalf("move %+v", u)
	}
	// an organization item: the collection only
	keys(m, "esc", "/")
	typeText(m, "beta")
	keys(m, "enter", "e")
	f = m.modal.(*hostForm)
	if slices.Contains(f.fields(), fOrg) || !slices.Contains(f.fields(), fCollection) || f.cols[f.col].ID != "c1" {
		t.Fatalf("fields %v, collection %d", f.fields(), f.col)
	}
	if !strings.Contains(ansi.Strip(m.View()), "cannot move items out of an organization") {
		t.Error("no explanation")
	}
	enterAndRun(t, m) // unchanged: the item stays where it is
	if u := src.updated["3"]; u.OrgID != "" {
		t.Fatalf("unchanged collection sent: %+v", u)
	}
	keys(m, "e")
	f = m.modal.(*hostForm)
	f.setFocus(fCollection)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	enterAndRun(t, m)
	if u := src.updated["3"]; u.OrgID != "o1" || len(u.CollectionIDs) != 1 || u.CollectionIDs[0] == "c1" {
		t.Fatalf("collection change %+v", u)
	}
}
