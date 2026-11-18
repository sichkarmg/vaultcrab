// SPDX-License-Identifier: MIT

package vault

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

// SetLogin changes only the login and the password: in "user@host" of the
// username, or in the username (and the URI user) of the URI format.
func TestSetLogin(t *testing.T) {
	f := &fakeEditServer{item: storedItem}
	srv := httptest.NewServer(f)
	defer srv.Close()
	c := NewClient(srv.URL)
	pw := []byte(`NEW"PW`)
	if err := c.SetLogin(context.Background(), "i1", "admin", pw); err != nil {
		t.Fatal(err)
	}
	if pw[0] != 0 {
		t.Error("password not wiped")
	}
	m, h := decodeItem(t, f.item)
	login := m["login"].(map[string]any)
	if h == nil || h.User != "admin" || h.Addr != "old" || h.Port != 2222 || login["username"] != "admin@old:2222" {
		t.Fatalf("host %+v, username %v", h, login["username"])
	}
	if login["password"] != `NEW"PW` {
		t.Errorf("password %v", login["password"])
	}
	for _, want := range []string{`"HIDDEN-VALUE"`, `"TOTP-SEED"`, `"keep notes"`, `"OLDER"`, `"att"`, `"name":"old"`, `"folderId":"f0"`, `"value":"a,b"`} {
		if !strings.Contains(f.item, want) {
			t.Errorf("lost %s: %s", want, f.item)
		}
	}

	// URI format, the user in the URI too
	f.item = `{"object":"item","id":"i1","type":1,"name":"u","fields":[],
"login":{"username":"bob","password":"x","uris":[{"match":null,"uri":"https://web"},{"match":null,"uri":"ssh://bob@srv:2200"}]}}`
	if err := c.SetLogin(context.Background(), "i1", "carol", []byte("pw2")); err != nil {
		t.Fatal(err)
	}
	m, h = decodeItem(t, f.item)
	login = m["login"].(map[string]any)
	if h == nil || h.User != "carol" || h.Addr != "srv" || h.Port != 2200 || login["username"] != "carol" || login["password"] != "pw2" {
		t.Fatalf("host %+v, login %v", h, login)
	}
	if !strings.Contains(f.item, `"https://web"`) {
		t.Errorf("other URI lost: %s", f.item)
	}

	for _, bad := range []string{"", "a@b", "a b"} {
		if err := c.SetLogin(context.Background(), "i1", bad, []byte("p")); err == nil {
			t.Errorf("login %q accepted", bad)
		}
	}
}
