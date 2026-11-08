// SPDX-License-Identifier: MIT

package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// The session key of bw serve.
//
// bw serve keeps the unlocked vault key in its memory only: when the process
// exits, the vault is locked. bw 2026.10 even exits by itself when it has to
// refresh its access token while the server is unreachable (an unhandled
// FetchError in identity/connect/token: e.g. the first sync after a sleep of
// more than the token lifetime, before the VPN is back).
//
// /unlock returns a session key (the BW_SESSION of the CLI): bw stores the
// vault key in its data.json encrypted with it, so a bw serve started with
// BW_SESSION=<key> is unlocked. The Client keeps that key in memory while the
// vault is unlocked to restart a crashed bw serve without asking for the
// master password again; it is dropped (wiped) when the vault is locked
// (bw /lock also removes the encrypted copy from data.json, after which the
// key opens nothing) or when the connection is closed on purpose.

// sessionKey is the session key of an unlocked vault (nil when unknown).
type sessionKey struct {
	mu  sync.Mutex
	key []byte
}

func (s *sessionKey) set(k []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	secret.Zero(s.key)
	s.key = k
}

func (s *sessionKey) take() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.key
	s.key = nil
	return k
}

func (s *sessionKey) known() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.key) > 0
}

// TakeSession moves the session key out of c (nil if none): for a bw serve
// restarted after a crash (ServeOptions.Session). The caller wipes it.
func (c *Client) TakeSession() []byte { return c.session.take() }

// AdoptSession gives c the session key its server was started with; c owns
// (and eventually wipes) it.
func (c *Client) AdoptSession(k []byte) { c.session.set(k) }

// HasSession reports whether c holds a session key.
func (c *Client) HasSession() bool { return c.session.known() }

// WipeSession forgets the session key: the vault cannot be reopened without
// a password any more (a deliberate stop, a lock).
func (c *Client) WipeSession() { c.session.set(nil) }

// sessionFrom extracts the session key ("raw") from the /unlock response.
func sessionFrom(data []byte) []byte {
	var r struct {
		Raw json.RawMessage `json:"raw"`
	}
	if json.Unmarshal(data, &r) != nil || len(r.Raw) == 0 {
		return nil
	}
	k, err := unquoteJSON(r.Raw)
	secret.Zero(r.Raw)
	if err != nil || len(k) == 0 {
		return nil
	}
	return k
}

// ErrServerUnreachable means the Bitwarden/Vaultwarden server cannot be
// reached (no network, VPN down): bw works from its local copy, and a sync
// is not tried (it would crash bw serve, see above).
var ErrServerUnreachable = errors.New("server unreachable")

// DefaultServer is probed when bw reports no server URL (bitwarden.com).
const DefaultServer = "https://vault.bitwarden.com"

// Reachable checks that the vault server (as bw last reported it, or
// DefaultServer) accepts a TCP connection, through the proxy of the bw
// environment if one is set. It does not talk to bw serve and sends nothing
// but a TCP handshake.
func (c *Client) Reachable(ctx context.Context) error {
	c.mu.Lock()
	srv := c.serverURL
	c.mu.Unlock()
	if srv == "" {
		srv = DefaultServer
	}
	addr, err := dialAddr(srv, c.Env)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrServerUnreachable, err)
	}
	timeout := c.ProbeTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrServerUnreachable, err)
	}
	_ = conn.Close()
	return nil
}

// dialAddr is host:port to probe for server URL srv: the proxy bw would use
// (HTTPS_PROXY/HTTP_PROXY of env, then of the process, unless NO_PROXY
// matches), else the server.
func dialAddr(srv string, env []string) (string, error) {
	u, err := url.Parse(srv)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("bad server URL %q", srv)
	}
	if p := proxyFor(u, env); p != nil {
		u = p
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port), nil
}

func proxyFor(u *url.URL, env []string) *url.URL {
	get := func(k string) string {
		for i := len(env) - 1; i >= 0; i-- {
			if name, v, ok := strings.Cut(env[i], "="); ok && (name == k || name == strings.ToLower(k)) {
				return v
			}
		}
		return ""
	}
	cfg := struct{ https, http, no string }{get("HTTPS_PROXY"), get("HTTP_PROXY"), get("NO_PROXY")}
	if cfg.https == "" && cfg.http == "" {
		// the environment of the process (bw inherits it)
		req := &http.Request{URL: u}
		if p, err := http.ProxyFromEnvironment(req); err == nil {
			return p
		}
		return nil
	}
	for _, n := range strings.Split(cfg.no, ",") {
		n = strings.TrimPrefix(strings.TrimSpace(n), ".")
		if n == "*" || n != "" && (u.Hostname() == n || strings.HasSuffix(u.Hostname(), "."+n)) {
			return nil
		}
	}
	p := cfg.https
	if u.Scheme == "http" || p == "" {
		p = cfg.http
	}
	if p == "" {
		return nil
	}
	if !strings.Contains(p, "://") {
		p = "http://" + p
	}
	pu, err := url.Parse(p)
	if err != nil || pu.Host == "" {
		return nil
	}
	return pu
}

// ExitReason is a short description of why a bw serve process ended: the
// "SomeError: message" line of its stderr (node prints the quoted source line
// before it), else the first line mentioning an error; at most 200 bytes, ""
// if none. bw prints no secrets there; the line is cut anyway.
func ExitReason(stderr []byte) string {
	var first []byte
	for _, l := range bytes.Split(stderr, []byte("\n")) {
		l = bytes.TrimSpace(l)
		if name, _, ok := bytes.Cut(l, []byte(": ")); ok && bytes.HasSuffix(name, []byte("Error")) && !bytes.ContainsAny(name, " (") {
			first = l
			break
		}
		if first == nil && bytes.Contains(bytes.ToLower(l), []byte("error")) && !bytes.HasPrefix(l, []byte("at ")) {
			first = l
		}
	}
	if len(first) > 200 {
		first = first[:200]
	}
	return string(first)
}
