// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// fakeVNC is a VNC client that runs until done is closed.
type fakeVNC struct {
	done chan struct{}
}

func (f *fakeVNC) Read(p []byte) (int, error) {
	<-f.done
	return 0, errors.New("eof")
}
func (f *fakeVNC) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeVNC) Resize(int, int) error       { return nil }
func (f *fakeVNC) Done() <-chan struct{}       { return f.done }
func (f *fakeVNC) Err() error                  { return errors.New("vncviewer closed") }
func (f *fakeVNC) Close() error                { return nil }

// A VNC host opens in a tab: the client gets the password fetched for this
// start; closing the client leaves the tab with ✗, Enter starts it again
// with the password fetched anew.
func TestVNCSession(t *testing.T) {
	vncHost := model.Host{ID: "v1", Name: "desk", Scheme: model.VNC, Addr: "10.0.0.5", Fields: map[string]string{"jump": "Bastion"}}
	src := &fakeSource{status: vault.Unlocked, secrets: map[string]string{"v1": "first"},
		snap: model.Snapshot{Hosts: []model.Host{vncHost, {ID: "j1", Name: "bastion", Scheme: model.SSH}}}}
	var mu sync.Mutex
	var got []string
	var clients []*fakeVNC
	var jumps int
	s := New(Options{
		Connect: func(context.Context) (Source, func(), error) { return src, nil, nil },
		VNC: func(ctx context.Context, h model.Host, sec *secret.Secret, jump func(context.Context) (*connect.Tunnel, error)) (session.Remote, error) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, string(sec.Value))
			if jump != nil {
				jumps++
			}
			c := &fakeVNC{done: make(chan struct{})}
			clients = append(clients, c)
			return c, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	if !s.InTab(vncHost) {
		t.Fatal("VNC does not open in a tab")
	}
	sess, err := s.OpenSession(vncHost, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	waitSession(t, sess, session.Connected)
	mu.Lock()
	close(clients[0].done) // the user closes the client window
	mu.Unlock()
	waitSession(t, sess, session.Disconnected)
	if _, err := sess.State(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("state error %v", err)
	}
	if sess.Exited() {
		t.Error("a closed client counts as a clean exit: the tab would close")
	}
	src.mu.Lock()
	src.secrets["v1"] = "second" // changed in the vault meanwhile
	src.mu.Unlock()
	sess.Reconnect()
	waitSession(t, sess, session.Connected)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, ",") != "first,second" || jumps != 2 {
		t.Errorf("passwords %q, jumps %d", got, jumps)
	}
	close(clients[1].done)
}

func waitSession(t *testing.T, s *session.Session, want session.State) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		st, err := s.State()
		if st == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %v (%v), want %v", st, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestJumpHost(t *testing.T) {
	src := &fakeSource{status: vault.Unlocked, snap: model.Snapshot{Hosts: []model.Host{
		{ID: "j1", Name: "bastion", Scheme: model.SSH},
		{ID: "j2", Name: "twin", Scheme: model.SSH},
		{ID: "j3", Name: "twin", Scheme: model.SSH},
		{ID: "v9", Name: "bastion-vnc", Scheme: model.VNC},
	}}}
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	for jump, want := range map[string]string{
		"":            "",
		"BASTION":     "j1",
		"j3":          "j3",
		"twin":        "error: 2 SSH items",
		"nowhere":     "error: no SSH item",
		"bastion-vnc": "error: no SSH item",
	} {
		h := model.Host{Name: "desk", Scheme: model.VNC, Fields: map[string]string{"jump": jump}}
		j, err := s.jumpHost(h)
		switch {
		case strings.HasPrefix(want, "error: "):
			if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(want, "error: ")) {
				t.Errorf("%q: err %v", jump, err)
			}
		case want == "":
			if j != nil || err != nil {
				t.Errorf("no jump: %v %v", j, err)
			}
		case err != nil || j == nil || j.ID != want:
			t.Errorf("%q: %v %v, want %s", jump, j, err, want)
		}
	}
}
