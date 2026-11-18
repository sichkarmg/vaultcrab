// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// loginSource is an editable fakeSource that records SetLogin.
type loginSource struct {
	editSource
	mu    sync.Mutex
	saved []string
}

func (l *loginSource) SetLogin(_ context.Context, id, user string, pw []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.saved = append(l.saved, id+" "+user+" "+string(pw))
	return nil
}

func waitPrompt(t *testing.T, s *Service, kind PromptKind) *Prompt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := s.Prompt(); p != nil && p.Kind == kind {
			return p
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no question of kind %d", kind)
	return nil
}

// nopRemote is a shell that prints nothing until it is closed.
type nopRemote struct {
	done chan struct{}
	once sync.Once
}

func (r *nopRemote) Read([]byte) (int, error)  { <-r.done; return 0, io.EOF }
func (*nopRemote) Write(p []byte) (int, error) { return len(p), nil }
func (*nopRemote) Resize(int, int) error       { return nil }
func (r *nopRemote) Done() <-chan struct{}     { return r.done }
func (*nopRemote) Err() error                  { return nil }
func (r *nopRemote) Close() error              { r.once.Do(func() { close(r.done) }); return nil }

// A refused vault password opens the login question with the login of the
// item; the typed pair connects and is offered to be saved; Save writes it
// to the item.
func TestReloginAndSave(t *testing.T) {
	src := &loginSource{editSource: editSource{fakeSource: fakeSource{status: vault.Unlocked, secrets: map[string]string{"h1": "old"},
		snap: model.Snapshot{Hosts: []model.Host{{ID: "h1", Name: "box", User: "root", Addr: "192.0.2.1", Port: 22, Scheme: model.SSH}}}}}}
	s := New(Options{Connect: func(context.Context) (Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	waitState(t, s, Online)
	s.sessions.dial = func(_ context.Context, h model.Host, sec *secret.Secret, _, _ int) (session.Remote, error) {
		if h.User == "admin" && string(sec.Value) == "new" {
			return &nopRemote{done: make(chan struct{})}, nil
		}
		return nil, connect.ErrAuthFailed
	}
	h, _ := s.Host("h1")
	sess, err := s.OpenSession(h, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseSession(sess)
	p := waitPrompt(t, s, PromptLogin)
	if p.User != "root" || p.Warning == "" {
		t.Fatalf("question %+v", p)
	}
	p.AnswerLogin("admin", []byte("new"))
	sp := waitPrompt(t, s, PromptSaveLogin)
	if st, _ := sess.State(); st != session.Connected {
		t.Fatalf("state %v", st)
	}
	if sp.Warning != "" || sp.User != "admin" {
		t.Fatalf("save question %+v", sp)
	}
	if err := sp.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Prompt() != nil {
		t.Fatal("question left open")
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if len(src.saved) != 1 || src.saved[0] != "h1 admin new" {
		t.Fatalf("saved %q", src.saved)
	}
}
