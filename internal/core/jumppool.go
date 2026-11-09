// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/jump"
)

// One connection to a jump host is shared by the shells and the tunnels
// going through it: the first use connects (asking for a passphrase or a
// host key as usual), the next ones take the same connection, the last one
// to give it back closes it. A lost connection is forgotten, and the next
// use connects again.

// JumpConn is a connection to a jump host (connect.JumpClient; tests
// replace it).
type JumpConn interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	Done() <-chan struct{}
	Close() error
}

type jumpPool struct {
	mu sync.Mutex
	m  map[string]*poolEntry
}

type poolEntry struct {
	dial sync.Mutex // one attempt at a time: the questions are asked once
	mu   sync.Mutex
	c    JumpConn
	refs int
}

// poolKey identifies the connection of a plan: the jump host, its user,
// where its key comes from and the host key check.
func poolKey(p *jumpPlan) string {
	k := p.jumpKey
	return fmt.Sprintf("%s@%s|%d|%s|%s|%s|%d", p.jump.User, p.jump.Addr, k.kind, k.path, k.item, k.scope, p.mode)
}

func isDone(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// leaseJump returns the connection to the jump host of p, connecting if
// there is none; release gives it back (once).
func (s *Service) leaseJump(ctx context.Context, p *jumpPlan) (JumpConn, func(), error) {
	key := poolKey(p)
	s.pool.mu.Lock()
	if s.pool.m == nil {
		s.pool.m = map[string]*poolEntry{}
	}
	e := s.pool.m[key]
	if e == nil {
		e = &poolEntry{}
		s.pool.m[key] = e
	}
	s.pool.mu.Unlock()

	e.dial.Lock()
	defer e.dial.Unlock()
	e.mu.Lock()
	if e.c != nil && !isDone(e.c.Done()) {
		e.refs++
		c, n := e.c, e.refs
		e.mu.Unlock()
		connect.TraceOf(ctx).Add("jump host %s (%s): the open connection is used (%d users)", p.jump.Name, p.jump.Addr, n)
		return c, s.releaser(e, c), nil
	}
	e.mu.Unlock()

	role := "jump host " + p.jump.Name
	a, done, desc, err := s.hopAuth(ctx, p.jumpKey, role)
	if done != nil {
		defer done()
	}
	if err != nil {
		connect.TraceOf(ctx).Add("%s: credential: %v", role, err)
		return nil, nil, fmt.Errorf("jump host %s: %w", p.jump.Name, err)
	}
	hop := p.jump
	hop.Auth, hop.AuthInfo = a, desc
	hop.Keys = connect.HostKeyPolicy{File: s.opts.SSH.KnownHosts, Mode: p.mode, Ask: s.askHostKey(p.mode)}
	dial := s.opts.JumpConnect
	if dial == nil {
		dial = func(ctx context.Context, h connect.Hop, o connect.SSHOptions) (JumpConn, error) {
			c, err := connect.DialJumpHost(ctx, h, o)
			if err != nil {
				return nil, err // not a nil *JumpClient in an interface
			}
			return c, nil
		}
	}
	c, err := dial(ctx, hop, s.opts.SSH)
	if err != nil {
		var je *connect.JumpError
		if errors.As(err, &je) && je.Stage == connect.StageAuth && p.jumpKey.kind == jump.KeyPassword {
			s.forgetPassword(p.jumpKey.scope) // asked again next time
		}
		return nil, nil, err
	}
	logf("jump: connected to %s (%s@%s)", p.jump.Name, p.jump.User, p.jump.Addr)
	e.mu.Lock()
	e.c, e.refs = c, 1
	e.mu.Unlock()
	go func() {
		<-c.Done()
		e.mu.Lock()
		if e.c == c {
			e.c, e.refs = nil, 0
		}
		e.mu.Unlock()
		logf("jump: connection to %s ended", p.jump.Name)
	}()
	return c, s.releaser(e, c), nil
}

func (s *Service) releaser(e *poolEntry, c JumpConn) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			last := false
			if e.c == c {
				e.refs--
				if e.refs <= 0 {
					e.c, e.refs, last = nil, 0, true
				}
			}
			e.mu.Unlock()
			if last {
				c.Close()
			}
		})
	}
}

// closeJumps closes every connection to a jump host (at the end).
func (s *Service) closeJumps() {
	s.pool.mu.Lock()
	es := make([]*poolEntry, 0, len(s.pool.m))
	for _, e := range s.pool.m {
		es = append(es, e)
	}
	s.pool.mu.Unlock()
	for _, e := range es {
		e.mu.Lock()
		c := e.c
		e.c, e.refs = nil, 0
		e.mu.Unlock()
		if c != nil {
			c.Close()
		}
	}
}

// closerFunc makes a release function an io.Closer.
type closerFunc func()

func (f closerFunc) Close() error { f(); return nil }
