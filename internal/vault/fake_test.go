// SPDX-License-Identifier: MIT

package vault

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeServe emulates the subset of the bw serve API used by Client.
type fakeServe struct {
	mu       sync.Mutex
	status   Status
	password string
	unlocks  int
	syncs    int
}

func (f *fakeServe) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data string) {
		io.WriteString(w, `{"success":true,"data":`+data+`}`)
	}
	fail := func(w http.ResponseWriter, msg string) {
		w.WriteHeader(400)
		b, _ := json.Marshal(map[string]any{"success": false, "message": msg})
		w.Write(b)
	}
	locked := func(w http.ResponseWriter) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status != Unlocked {
			fail(w, "Vault is locked.")
			return true
		}
		return false
	}
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		reply(w, `{"object":"template","template":{"serverUrl":"https://vw.test","lastSync":"2026-10-21T10:00:00Z","userEmail":"t@test","status":"`+string(f.status)+`"}}`)
	})
	mux.HandleFunc("POST /unlock", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Password string }
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.unlocks++
		if body.Password != f.password {
			fail(w, "Invalid master password.")
			return
		}
		f.status = Unlocked
		reply(w, `{"object":"message","title":"Your vault is now unlocked!","raw":"SESSIONKEY"}`)
	})
	mux.HandleFunc("POST /sync", func(w http.ResponseWriter, r *http.Request) {
		if locked(w) {
			return
		}
		f.mu.Lock()
		f.syncs++
		f.mu.Unlock()
		reply(w, `{"object":"message","title":"Syncing complete."}`)
	})
	for _, obj := range []string{"items", "folders", "organizations", "collections"} {
		obj := obj
		mux.HandleFunc("GET /list/object/"+obj, func(w http.ResponseWriter, r *http.Request) {
			if locked(w) {
				return
			}
			b, err := os.ReadFile("testdata/" + obj + ".json")
			if err != nil {
				t.Error(err)
			}
			w.Write(b)
		})
	}
	mux.HandleFunc("GET /object/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		if locked(w) {
			return
		}
		b, _ := os.ReadFile("testdata/items.json")
		var list struct {
			Data struct {
				Data []json.RawMessage `json:"data"`
			} `json:"data"`
		}
		json.Unmarshal(b, &list)
		for _, it := range list.Data.Data {
			var x struct{ ID string }
			json.Unmarshal(it, &x)
			if x.ID == r.PathValue("id") {
				reply(w, string(it))
				return
			}
		}
		w.WriteHeader(404)
		io.WriteString(w, `{"success":false,"message":"Not found."}`)
	})
	mux.HandleFunc("GET /object/password/{id}", func(w http.ResponseWriter, r *http.Request) {
		if locked(w) {
			return
		}
		switch r.PathValue("id") {
		case "i1":
			reply(w, `{"object":"string","data":"pa\"ss\\w\u00f6rd\ud83d\ude00"}`)
		case "key":
			reply(w, `{"object":"string","data":"-----BEGIN OPENSSH PRIVATE KEY----- AAAA BBBB -----END OPENSSH PRIVATE KEY-----"}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"success":false,"message":"Not found."}`)
		}
	})
	return mux
}

func newFake(t *testing.T, st Status) (*fakeServe, *Client) {
	f := &fakeServe{status: st, password: `p"w\x`}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	return f, NewClient(srv.URL)
}

func hasSubstr(b []byte, subs ...string) string {
	for _, s := range subs {
		if strings.Contains(string(b), s) {
			return s
		}
	}
	return ""
}
