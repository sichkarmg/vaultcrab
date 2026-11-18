// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

// When the server refuses the credential of the vault, the session asks for
// a login and a password (askLogin) and connects with them; after that
// succeeds the user is offered to save them to the vault item (offerSave).

// LoginSetter is implemented by sources that can change the login and the
// password of an item and keep the rest of it (vault.Client).
type LoginSetter interface {
	SetLogin(ctx context.Context, id, user string, password []byte) error
}

// askLogin asks for a login and a password for h (session.Options.Login).
func (s *Service) askLogin(h model.Host) func(context.Context, session.LoginQuestion) (string, []byte, error) {
	return func(ctx context.Context, q session.LoginQuestion) (string, []byte, error) {
		p := &Prompt{Kind: PromptLogin, Title: "SSH login", User: q.User, Target: h.Target(),
			Text: fmt.Sprintf("%s (%s)", h.Name, h.Target())}
		switch {
		case q.Retry:
			p.Warning = "Wrong login or password."
		case errors.Is(q.Reason, connect.ErrNoCredential):
			p.Warning = "The vault item has no password."
		case q.Key:
			p.Warning = "The server refused the key of the vault item."
		default:
			p.Warning = "The server refused the login and password of the vault item."
		}
		r, err := s.ask(ctx, p)
		if err != nil {
			return "", nil, err
		}
		user := strings.TrimSpace(r.user)
		if user == "" || len(r.secret) == 0 {
			secret.Zero(r.secret)
			return "", nil, errors.New("no login or password typed")
		}
		return user, r.secret, nil
	}
}

// loginSave is a typed login and password waiting for the answer to a
// PromptSaveLogin question.
type loginSave struct {
	mu     sync.Mutex
	hostID string
	user   string
	pw     []byte
}

func (l *loginSave) wipe() {
	l.mu.Lock()
	secret.Zero(l.pw)
	l.pw = nil
	l.mu.Unlock()
}

// offerSave asks whether to save a login and a password that the server
// accepted to the vault item of h (session.Options.LoginOK). The question
// is not asked when the vault cannot be changed now.
func (s *Service) offerSave(h model.Host) func(string, []byte, bool) {
	return func(user string, pw []byte, replacesKey bool) {
		v, _ := s.route(h.ID)
		ed, err := v.editor()
		if err == nil {
			if _, ok := ed.(LoginSetter); !ok {
				err = ErrReadOnly
			}
		}
		if err != nil {
			secret.Zero(pw)
			logf("login: %s: not offered to save the typed login: %v", h.Name, err)
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Connected to %s with the login and password you typed.\nSave them to the vault item %q?\n", h.Name, h.Name)
		if user != h.User {
			fmt.Fprintf(&b, "\n  login:    %s → %s", orNone(h.User), user)
		} else {
			fmt.Fprintf(&b, "\n  login:    %s (unchanged)", user)
		}
		b.WriteString("\n  password: replaced")
		p := &Prompt{Kind: PromptSaveLogin, Title: "Save the login", Text: b.String(),
			User: user, Target: h.Target(), save: &loginSave{hostID: h.ID, user: user, pw: pw}}
		if replacesKey {
			p.Warning = "The password field of the item holds a private key: saving replaces the key with the password."
		}
		p.reply = make(chan promptReply, 1)
		p.s = s
		s.prompts.mu.Lock()
		s.prompts.list = append(s.prompts.list, p)
		s.prompts.mu.Unlock()
		s.emit()
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// Save writes the login and the password of a PromptSaveLogin question to
// the vault item and closes the question (the password is wiped).
func (p *Prompt) Save(ctx context.Context) error {
	if p.save == nil {
		return errors.New("not a save question")
	}
	l := p.save
	l.mu.Lock()
	pw := l.pw
	l.pw = nil
	l.mu.Unlock()
	p.answer(promptReply{}) // closes the question
	if pw == nil {
		return errors.New("already answered")
	}
	return p.s.SaveLogin(ctx, l.hostID, l.user, pw)
}

// SaveLogin replaces the login and the password of the item of hostID and
// keeps the rest of the item. password is wiped.
func (s *Service) SaveLogin(ctx context.Context, hostID, user string, password []byte) error {
	defer secret.Zero(password)
	v, rawID := s.route(hostID)
	ed, err := v.editor()
	if err != nil {
		return err
	}
	ls, ok := ed.(LoginSetter)
	if !ok {
		return ErrReadOnly
	}
	if err := ls.SetLogin(ctx, rawID, user, password); err != nil {
		return err
	}
	logf("login: the login and password of %s were saved to the vault", hostID)
	v.refresh(ctx)
	return nil
}
