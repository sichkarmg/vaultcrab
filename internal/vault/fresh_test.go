// SPDX-License-Identifier: MIT

package vault

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// bw serve on Windows may answer "Not found" for a moment for an item it has
// just created. The client retries for items it created lately, and only
// for those: an unknown item (or one without a password) fails at once.
func TestNotFoundRetriedForNewItems(t *testing.T) {
	old := freshRetry
	freshRetry = 10 * time.Millisecond
	t.Cleanup(func() { freshRetry = old })
	var misses atomic.Int32
	misses.Store(3)
	var other atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/object/item":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"new1"}}`))
		case r.URL.Path == "/object/password/new1":
			if misses.Add(-1) >= 0 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"success":false,"message":"Not found."}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"object":"string","data":"pw"}}`))
		case strings.HasPrefix(r.URL.Path, "/object/password/"):
			other.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"message":"Not found."}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	ctx := context.Background()
	id, err := c.CreateHost(ctx, HostEdit{Name: "n", Scheme: model.SSH, Addr: "h", Password: []byte("pw")})
	if err != nil {
		t.Fatal(err)
	}
	sec, err := c.Secret(ctx, id)
	if err != nil || string(sec.Value) != "pw" {
		t.Fatalf("new item: %q %v", sec.Value, err)
	}
	if _, err := c.Secret(ctx, "old1"); err == nil || !strings.Contains(err.Error(), "Not found") {
		t.Fatalf("unknown item: %v", err)
	}
	if other.Load() != 1 {
		t.Errorf("an unknown item asked %d times, want 1", other.Load())
	}
}
