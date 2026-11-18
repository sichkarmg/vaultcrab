// SPDX-License-Identifier: MIT

package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// A refused vault password asks for a login and a password, again after a
// wrong one; the accepted pair is handed to LoginOK.
func TestRelogin(t *testing.T) {
	var mu sync.Mutex
	var qs []LoginQuestion
	answers := [][2]string{{"admin", "wrong"}, {"admin", "right"}}
	ok := make(chan [2]string, 1)
	s := New(model.Host{Name: "h", User: "root", Addr: "a"}, Options{
		Cols: 40, Rows: 5,
		Secret: func(context.Context) (secret.Secret, error) { return secret.FromString("old"), nil },
		Dial: func(_ context.Context, h model.Host, sec *secret.Secret, _, _ int) (Remote, error) {
			if h.User == "admin" && string(sec.Value) == "right" {
				return newFakeRemote(), nil
			}
			return nil, connect.ErrAuthFailed
		},
		Login: func(_ context.Context, q LoginQuestion) (string, []byte, error) {
			mu.Lock()
			defer mu.Unlock()
			qs = append(qs, q)
			a := answers[len(qs)-1]
			return a[0], []byte(a[1]), nil
		},
		LoginOK: func(user string, pw []byte, key bool) {
			ok <- [2]string{user, string(pw)}
		},
	})
	s.Connect()
	wait(t, s, Connected)
	select {
	case got := <-ok:
		if got != [2]string{"admin", "right"} {
			t.Fatalf("LoginOK %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LoginOK not called")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(qs) != 2 || qs[0].User != "root" || qs[0].Retry || qs[1].User != "admin" || !qs[1].Retry {
		t.Fatalf("questions %+v", qs)
	}
	if s.Host.User != "root" {
		t.Error("the host of the session changed")
	}
	s.Close()
}

// Cancelling the question ends the attempt with the original error; other
// errors do not ask.
func TestReloginCancel(t *testing.T) {
	asked := 0
	dialErr := connect.ErrAuthFailed
	s := New(model.Host{Name: "h", User: "root", Addr: "a"}, Options{
		Cols: 40, Rows: 5,
		Secret: func(context.Context) (secret.Secret, error) { return secret.FromString("old"), nil },
		Dial: func(context.Context, model.Host, *secret.Secret, int, int) (Remote, error) {
			return nil, dialErr
		},
		Login: func(context.Context, LoginQuestion) (string, []byte, error) {
			asked++
			return "", nil, errors.New("canceled")
		},
	})
	s.Connect()
	wait(t, s, Disconnected)
	if _, err := s.State(); !errors.Is(err, connect.ErrAuthFailed) || asked != 1 {
		t.Fatalf("err %v, asked %d", err, asked)
	}
	dialErr = errors.New("connection refused")
	s.Connect()
	wait(t, s, Disconnected)
	if asked != 1 {
		t.Fatalf("asked on another error")
	}
	s.Close()
}
