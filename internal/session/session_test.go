// SPDX-License-Identifier: MIT

package session

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// fakeRemote echoes input back as output.
type fakeRemote struct {
	pr   *io.PipeReader
	pw   *io.PipeWriter
	done chan struct{}
	once sync.Once
	err  error
	size atomic.Value
}

func newFakeRemote() *fakeRemote {
	pr, pw := io.Pipe()
	return &fakeRemote{pr: pr, pw: pw, done: make(chan struct{})}
}
func (f *fakeRemote) Read(p []byte) (int, error)  { return f.pr.Read(p) }
func (f *fakeRemote) Write(p []byte) (int, error) { return f.pw.Write(p) }
func (f *fakeRemote) Resize(c, r int) error       { f.size.Store([2]int{c, r}); return nil }
func (f *fakeRemote) Done() <-chan struct{}       { return f.done }
func (f *fakeRemote) Err() error                  { return f.err }
func (f *fakeRemote) Close() error                { return f.drop(nil) }
func (f *fakeRemote) drop(err error) error {
	f.once.Do(func() { f.err = err; f.pw.CloseWithError(io.EOF); close(f.done) })
	return nil
}

func wait(t *testing.T, s *Session, want State) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if st, _ := s.State(); st == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, err := s.State()
	t.Fatalf("state %v (%v), want %v", st, err, want)
}

func TestReconnectKeepsScrollbackAndRefetchesSecret(t *testing.T) {
	var secrets atomic.Int32
	var mu sync.Mutex
	var remotes []*fakeRemote
	var seen []string
	s := New(model.Host{Name: "h", User: "u", Addr: "a"}, Options{
		Cols: 40, Rows: 5,
		Secret: func(context.Context) (secret.Secret, error) {
			secrets.Add(1)
			return secret.FromString("pw"), nil
		},
		Dial: func(_ context.Context, _ model.Host, sec *secret.Secret, c, r int) (Remote, error) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, string(sec.Value))
			f := newFakeRemote()
			remotes = append(remotes, f)
			return f, nil
		},
	})
	s.Connect()
	wait(t, s, Connected)
	mu.Lock()
	r1 := remotes[0]
	mu.Unlock()
	go r1.pw.Write([]byte("first-output\r\n"))
	for !strings.Contains(s.Term.PlainText(), "first-output") {
		time.Sleep(5 * time.Millisecond)
	}
	r1.drop(errors.New("connection lost"))
	wait(t, s, Disconnected)
	if s.Exited() {
		t.Error("a dropped connection is not a clean exit")
	}
	if _, err := s.State(); err == nil || err.Error() != "connection lost" {
		t.Fatalf("err %v", err)
	}
	s.Reconnect()
	wait(t, s, Connected)
	if secrets.Load() != 2 {
		t.Errorf("secret fetched %d times, want 2", secrets.Load())
	}
	mu.Lock()
	if len(seen) != 2 || seen[0] != "pw" || seen[1] != "pw" {
		t.Errorf("dial secrets %v", seen)
	}
	mu.Unlock()
	if !strings.Contains(s.Term.History(), "first-output") {
		t.Error("history lost after reconnect")
	}
	s.Resize(50, 6)
	mu.Lock()
	r2 := remotes[1]
	mu.Unlock()
	if v, _ := r2.size.Load().([2]int); v != [2]int{50, 6} {
		t.Errorf("resize not propagated: %v", v)
	}
	s.Close()
	if st, _ := s.State(); st != Closed {
		t.Error("not closed")
	}
	s.Connect() // no-op after close
	if st, _ := s.State(); st != Closed {
		t.Error("connect after close")
	}
}

func TestSecretError(t *testing.T) {
	dialed := false
	s := New(model.Host{Name: "h"}, Options{
		Cols: 60, Rows: 5,
		Secret: func(context.Context) (secret.Secret, error) { return secret.Secret{}, errors.New("vault locked") },
		Dial: func(context.Context, model.Host, *secret.Secret, int, int) (Remote, error) {
			dialed = true
			return nil, nil
		},
	})
	s.Connect()
	wait(t, s, Disconnected)
	if dialed {
		t.Error("dialed without secret")
	}
	if !strings.Contains(s.Term.History(), "vault locked") {
		t.Errorf("error not shown in terminal: %q", s.Term.History())
	}
}

func TestSecretWipedAfterDial(t *testing.T) {
	var kept []byte
	s := New(model.Host{}, Options{
		Cols: 20, Rows: 5,
		Secret: func(context.Context) (secret.Secret, error) { return secret.FromString("hunter2"), nil },
		Dial: func(_ context.Context, _ model.Host, sec *secret.Secret, _, _ int) (Remote, error) {
			kept = sec.Value
			return nil, errors.New("refused")
		},
	})
	s.Connect()
	wait(t, s, Disconnected)
	if string(kept) != "\x00\x00\x00\x00\x00\x00\x00" {
		t.Errorf("secret not wiped: %q", kept)
	}
}

func TestCleanExit(t *testing.T) {
	var r *fakeRemote
	s := New(model.Host{}, Options{Cols: 20, Rows: 5,
		Secret: func(context.Context) (secret.Secret, error) { return secret.FromString("x"), nil },
		Dial: func(context.Context, model.Host, *secret.Secret, int, int) (Remote, error) {
			r = newFakeRemote()
			return r, nil
		}})
	s.Connect()
	wait(t, s, Connected)
	r.drop(nil)
	wait(t, s, Disconnected)
	if !s.Exited() {
		t.Error("clean exit not reported")
	}
}
