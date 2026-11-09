// SPDX-License-Identifier: MIT

package vault

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// fakeEditServer serves one stored item over the bw serve API.
type fakeEditServer struct {
	mu    sync.Mutex
	item  string
	puts  []string
	post  string
	move  []string // path and body of move / item-collections requests
	syncs int
}

func (f *fakeEditServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	ok := func(data string) { io.WriteString(w, `{"success":true,"data":`+data+`}`) }
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/object/item/i1":
		ok(f.item)
	case r.Method == http.MethodPut && r.URL.Path == "/object/item/i1":
		f.puts = append(f.puts, string(body))
		f.item = string(body)
		ok(f.item)
	case r.Method == http.MethodPost && r.URL.Path == "/object/item":
		f.post = string(body)
		ok(`{"id":"new1"}`)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/move/i1/"),
		r.Method == http.MethodPut && r.URL.Path == "/object/item-collections/i1":
		f.move = append(f.move, r.URL.Path+" "+string(body))
		var it map[string]any
		_ = json.Unmarshal([]byte(f.item), &it)
		if org := strings.TrimPrefix(r.URL.Path, "/move/i1/"); org != r.URL.Path {
			it["organizationId"] = org
		}
		var cols []string
		_ = json.Unmarshal(body, &cols)
		it["collectionIds"] = cols
		b, _ := json.Marshal(it)
		f.item = string(b)
		ok(f.item)
	case r.Method == http.MethodPost && r.URL.Path == "/sync":
		f.syncs++
		ok(`null`)
	case r.Method == http.MethodPost && r.URL.Path == "/object/folder":
		ok(`{"id":"fold1","name":"x"}`)
	default:
		w.WriteHeader(404)
		io.WriteString(w, `{"success":false,"message":"not found"}`)
	}
}

const storedItem = `{"object":"item","id":"i1","type":1,"name":"old","notes":"keep notes","favorite":true,
"folderId":"f0","organizationId":null,"collectionIds":[],"reprompt":0,
"fields":[{"name":"ssh","value":"true","type":2},{"name":"secret-token","value":"HIDDEN-VALUE","type":1},
{"name":"env","value":"prod","type":0},{"name":"tags","value":"a,b","type":0},{"name":"port","value":"2222","type":0}],
"login":{"username":"root@old:2222","password":"OLD-PASSWORD","totp":"TOTP-SEED","uris":[],"fido2Credentials":[]},
"passwordHistory":[{"password":"OLDER","lastUsedDate":"2020-01-01T00:00:00Z"}],"attachments":[{"id":"att"}]}`

func decodeItem(t *testing.T, s string) (map[string]any, *model.Host) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	var it bwItem
	_ = json.Unmarshal([]byte(s), &it)
	h, ok := toHost(&it)
	if !ok {
		return m, nil
	}
	return m, &h
}

func TestUpdateHostKeepsEverythingElse(t *testing.T) {
	f := &fakeEditServer{item: storedItem}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	e := HostEdit{Name: "new name", Scheme: model.SSH, User: "admin", Addr: "10.0.0.9", Port: 2200, Tags: []string{"x", "y"}, FolderID: "f1"}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	m, h := decodeItem(t, f.item)
	if h == nil || h.Name != "new name" || h.User != "admin" || h.Addr != "10.0.0.9" || h.Port != 2200 || strings.Join(h.Tags, ",") != "x,y" || h.FolderID != "f1" {
		t.Fatalf("host %+v", h)
	}
	login := m["login"].(map[string]any)
	if login["password"] != "OLD-PASSWORD" || login["totp"] != "TOTP-SEED" || m["notes"] != "keep notes" || m["favorite"] != true {
		t.Errorf("lost data: %v", f.item)
	}
	for _, want := range []string{`"HIDDEN-VALUE"`, `"env"`, `"OLDER"`, `"att"`} {
		if !strings.Contains(f.item, want) {
			t.Errorf("lost %s", want)
		}
	}
	if strings.Contains(f.item, `"2222"`) {
		t.Error("old port field kept")
	}
	// change the password and switch to a scheme with a path (URI format)
	e = HostEdit{Name: "files", Scheme: model.SFTP, User: "bob", Addr: "srv", Path: "home/bob", Password: []byte("NEW\"PW")}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	m, h = decodeItem(t, f.item)
	if h == nil || h.Scheme != model.SFTP || h.User != "bob" || h.Addr != "srv" || h.Path != "home/bob" || h.Port != 22 {
		t.Fatalf("sftp host %+v\n%s", h, f.item)
	}
	if m["login"].(map[string]any)["password"] != `NEW"PW` {
		t.Error("password not changed")
	}
	if e.Password[0] != 0 {
		t.Error("password not wiped")
	}
}

func TestCreateHost(t *testing.T) {
	f := &fakeEditServer{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	id, err := c.CreateHost(context.Background(), HostEdit{Name: "rdp box", Scheme: model.RDP, User: "u", Addr: "win",
		OrgID: "o1", CollectionIDs: []string{"c1"}, Password: []byte("pw")})
	if err != nil || id != "new1" {
		t.Fatal(id, err)
	}
	m, h := decodeItem(t, f.post)
	if h == nil || h.Scheme != model.RDP || h.Addr != "win" || h.Port != 3389 || m["organizationId"] != "o1" {
		t.Fatalf("created %s", f.post)
	}
	if _, err := c.CreateHost(context.Background(), HostEdit{Name: "x", Scheme: model.SSH, Addr: "h", OrgID: "o1"}); err == nil {
		t.Error("org item without collection accepted")
	}
	for _, bad := range []HostEdit{{Scheme: model.SSH, Addr: "h"}, {Name: "n", Scheme: model.SSH}, {Name: "n", Scheme: "gopher", Addr: "h"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if id, err := c.CreateFolder(context.Background(), "A/B"); err != nil || id != "fold1" {
		t.Error(id, err)
	}
}

func TestCopyHost(t *testing.T) {
	f := &fakeEditServer{item: storedItem}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	id, err := c.CreateHost(context.Background(), HostEdit{CopyOf: "i1", Name: "old copy1", Scheme: model.SSH, User: "root", Addr: "old", Port: 2222})
	if err != nil || id != "new1" {
		t.Fatal(id, err)
	}
	m, h := decodeItem(t, f.post)
	if h == nil || h.Name != "old copy1" || h.Addr != "old" || h.Port != 2222 {
		t.Fatalf("copy %s", f.post)
	}
	login := m["login"].(map[string]any)
	if login["password"] != "OLD-PASSWORD" || login["totp"] != "TOTP-SEED" || m["notes"] != "keep notes" {
		t.Errorf("content not copied: %s", f.post)
	}
	for _, k := range []string{"id", "passwordHistory", "attachments", "revisionDate"} {
		if _, ok := m[k]; ok {
			t.Errorf("copied %s", k)
		}
	}
	if !strings.Contains(f.post, "HIDDEN-VALUE") || !strings.Contains(f.post, `"env"`) {
		t.Error("custom fields not copied")
	}
}

func TestNotesEdit(t *testing.T) {
	f := &fakeEditServer{item: storedItem}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	n := "line 1\nline \"2\""
	e := HostEdit{Name: "old", Scheme: model.SSH, User: "root", Addr: "old", Port: 2222, Notes: &n}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	m, _ := decodeItem(t, f.item)
	if m["notes"] != n {
		t.Fatalf("notes %q", m["notes"])
	}
	empty := ""
	e.Notes = &empty
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	if m, _ := decodeItem(t, f.item); m["notes"] != nil {
		t.Fatalf("notes not removed: %q", m["notes"])
	}
}

// A personal item moves into an organization (with a collection); an item of
// the organization changes its collections; it cannot change organizations.
func TestUpdateHostOrganization(t *testing.T) {
	f := &fakeEditServer{item: storedItem}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	e := HostEdit{Name: "old", Scheme: model.SSH, User: "root", Addr: "old", Port: 2222}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil || len(f.move) != 0 {
		t.Fatalf("no organization: %v %v", err, f.move)
	}
	e.OrgID = "o1"
	if err := c.UpdateHost(context.Background(), "i1", e); err == nil {
		t.Error("moved without a collection")
	}
	e.CollectionIDs = []string{"c1"}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	if len(f.move) != 1 || f.move[0] != `/move/i1/o1 ["c1"]` || f.syncs != 1 {
		t.Fatalf("move: %v, %d syncs", f.move, f.syncs)
	}
	// same collections: nothing to do; others: replaced
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil || len(f.move) != 1 {
		t.Fatalf("unchanged collections: %v %v", err, f.move)
	}
	e.CollectionIDs = []string{"c2"}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	if len(f.move) != 2 || f.move[1] != `/object/item-collections/i1 ["c2"]` {
		t.Fatalf("collections: %v", f.move)
	}
	e.OrgID = "o2"
	if err := c.UpdateHost(context.Background(), "i1", e); err == nil || len(f.move) != 2 {
		t.Errorf("moved to another organization: %v %v", err, f.move)
	}
}

// An item without a user is saved with a URI, so that it reads back as a
// host (a bare "host" username would read as a user name).
func TestCreateHostWithoutUser(t *testing.T) {
	f := &fakeEditServer{item: storedItem}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	if _, err := c.CreateHost(context.Background(), HostEdit{Name: "n", Scheme: model.SSH, Addr: "10.9.9.9"}); err != nil {
		t.Fatal(err)
	}
	if _, h := decodeItem(t, f.post); h == nil || h.Addr != "10.9.9.9" || h.User != "" || h.Port != 22 {
		t.Fatalf("not a host: %+v\n%s", h, f.post)
	}
	e := HostEdit{Name: "old", Scheme: model.VNC, Addr: "box"}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	if _, h := decodeItem(t, f.item); h == nil || h.Addr != "box" || h.Scheme != model.VNC {
		t.Fatalf("update: %+v\n%s", h, f.item)
	}
}

// Knock sets, replaces and removes the knock field; nil keeps it.
func TestUpdateHostKnock(t *testing.T) {
	f := &fakeEditServer{item: storedItem}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	k := "7000,8000/udp"
	e := HostEdit{Name: "old", Scheme: model.SSH, User: "root", Addr: "old", Port: 2222, Knock: &k}
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	if _, h := decodeItem(t, f.item); h.Fields["knock"] != k || h.Fields["env"] != "prod" {
		t.Fatalf("set: %v", h.Fields)
	}
	e.Knock = nil
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	if _, h := decodeItem(t, f.item); h.Fields["knock"] != k {
		t.Fatalf("nil did not keep it: %v", h.Fields)
	}
	empty := ""
	e.Knock = &empty
	if err := c.UpdateHost(context.Background(), "i1", e); err != nil {
		t.Fatal(err)
	}
	if _, h := decodeItem(t, f.item); h.Fields["knock"] != "" || strings.Count(f.item, "knock") != 0 {
		t.Fatalf("not removed: %s", f.item)
	}
}
