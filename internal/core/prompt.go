// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// A connection in the background sometimes needs the user: the passphrase
// of a key file, a decision about a new host key. It queues a Prompt and
// waits; the UI shows the oldest one (Service.Prompt) and answers it.

// PromptKind says what a Prompt asks.
type PromptKind int

const (
	PromptPassphrase PromptKind = iota // a secret line (hidden input)
	PromptHostKey                      // accept a new host key?
)

// Prompt is a question of a connection to the user.
type Prompt struct {
	Kind  PromptKind
	Title string
	Text  string // the question, may span lines
	// HostKey describes the key (PromptHostKey).
	HostKey connect.HostKeyQuestion
	// Warning is shown with the question (accept-new templates, a retry
	// after a wrong passphrase).
	Warning string

	reply chan promptReply
	once  sync.Once
	s     *Service
}

type promptReply struct {
	secret []byte
	answer connect.HostKeyAnswer
	err    error
}

// errPromptCanceled: the user closed the question.
var errPromptCanceled = errors.New("canceled")

type prompts struct {
	mu   sync.Mutex
	list []*Prompt
}

// Prompt returns the oldest open question, nil if none.
func (s *Service) Prompt() *Prompt {
	s.prompts.mu.Lock()
	defer s.prompts.mu.Unlock()
	if len(s.prompts.list) == 0 {
		return nil
	}
	return s.prompts.list[0]
}

func (p *Prompt) answer(r promptReply) {
	sent := false
	p.once.Do(func() {
		p.reply <- r // buffered
		sent = true
	})
	if !sent {
		secret.Zero(r.secret)
	}
	p.s.dropPrompt(p)
}

// AnswerSecret answers a passphrase question; b belongs to the connection
// from now on (it wipes it).
func (p *Prompt) AnswerSecret(b []byte) { p.answer(promptReply{secret: b}) }

// AnswerHostKey answers a host key question.
func (p *Prompt) AnswerHostKey(a connect.HostKeyAnswer) { p.answer(promptReply{answer: a}) }

// Cancel closes the question: the connection fails.
func (p *Prompt) Cancel() { p.answer(promptReply{err: errPromptCanceled}) }

func (s *Service) dropPrompt(p *Prompt) {
	s.prompts.mu.Lock()
	s.prompts.list = slices.DeleteFunc(s.prompts.list, func(x *Prompt) bool { return x == p })
	s.prompts.mu.Unlock()
	s.emit()
}

// ask queues p and waits for the answer (or the end of ctx: the question
// is withdrawn).
func (s *Service) ask(ctx context.Context, p *Prompt) (promptReply, error) {
	p.reply = make(chan promptReply, 1)
	p.s = s
	s.prompts.mu.Lock()
	s.prompts.list = append(s.prompts.list, p)
	s.prompts.mu.Unlock()
	s.emit()
	select {
	case r := <-p.reply:
		return r, r.err
	case <-ctx.Done():
		p.once.Do(func() {}) // a late answer is dropped (and wiped)
		s.dropPrompt(p)
		return promptReply{}, ctx.Err()
	}
}
