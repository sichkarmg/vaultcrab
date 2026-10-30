// SPDX-License-Identifier: MIT

package vnc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// AcceptTimeout is how long the proxy waits for the client to connect.
const AcceptTimeout = 10 * time.Second

// Proxy lets one local VNC client in without a password: it listens on
// 127.0.0.1 on a random port for exactly one connection, offers the client
// no authentication and forwards everything between it and a server
// connection that is already signed in (SignIn).
type Proxy struct {
	ln     net.Listener
	server net.Conn

	mu     sync.Mutex
	client net.Conn
	closed bool
}

// Listen starts a proxy for server, a signed-in connection; the proxy owns
// it from now on.
func Listen(server net.Conn) (*Proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &Proxy{ln: ln, server: server}, nil
}

// Port is the local port the client connects to.
func (p *Proxy) Port() int { return p.ln.Addr().(*net.TCPAddr).Port }

// Accept waits for the client (at most timeout, or until ctx ends), closes
// the listener right after, and does the handshake with it.
func (p *Proxy) Accept(ctx context.Context, timeout time.Duration) (ClientInfo, error) {
	stop := context.AfterFunc(ctx, func() { p.ln.Close() })
	defer stop()
	if tl, ok := p.ln.(*net.TCPListener); ok {
		_ = tl.SetDeadline(time.Now().Add(timeout))
	}
	c, err := p.ln.Accept()
	p.ln.Close() // exactly one connection
	if err != nil {
		var ne net.Error
		switch {
		case ctx.Err() != nil:
			return ClientInfo{}, ctx.Err()
		case errors.As(err, &ne) && ne.Timeout():
			return ClientInfo{}, fmt.Errorf("the VNC client did not connect within %s", timeout)
		}
		return ClientInfo{}, err
	}
	p.mu.Lock()
	closed := p.closed
	p.client = c
	p.mu.Unlock()
	if closed {
		c.Close()
		return ClientInfo{}, net.ErrClosed
	}
	_ = c.SetDeadline(time.Now().Add(timeout))
	info, err := AcceptClient(c)
	_ = c.SetDeadline(time.Time{})
	if err != nil {
		return info, fmt.Errorf("VNC client handshake: %w", err)
	}
	return info, nil
}

// Forward copies between the client and the server until either side
// closes, then closes both. It returns the error that ended the
// connection (nil for a clean close).
func (p *Proxy) Forward() error {
	p.mu.Lock()
	c := p.client
	p.mu.Unlock()
	if c == nil {
		return errors.New("no client")
	}
	errc := make(chan error, 2)
	go func() { errc <- copyClose(p.server, c) }()
	go func() { errc <- copyClose(c, p.server) }()
	err := <-errc
	p.Close()
	<-errc
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

func copyClose(dst, src net.Conn) error {
	_, err := io.Copy(dst, src)
	return err
}

// Close ends the proxy: the listener and both connections.
func (p *Proxy) Close() {
	p.mu.Lock()
	p.closed = true
	c := p.client
	p.mu.Unlock()
	p.ln.Close()
	p.server.Close()
	if c != nil {
		c.Close()
	}
}
