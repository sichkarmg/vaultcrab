// SPDX-License-Identifier: MIT

// Package vault talks to a local `bw serve` (Bitwarden CLI REST API) and
// converts vault items into model metadata. Secrets are fetched one at a time
// via Secret and are never part of a Snapshot.
package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// Status of the vault as reported by bw.
type Status string

const (
	Unauthenticated Status = "unauthenticated"
	Locked          Status = "locked"
	Unlocked        Status = "unlocked"
)

// StatusInfo is the /status response.
type StatusInfo struct {
	Status    Status `json:"status"`
	ServerURL string `json:"serverUrl"`
	UserEmail string `json:"userEmail"`
	LastSync  string `json:"lastSync"`
}

// Account identifies the vault owner for cache separation.
func (s StatusInfo) Account() string {
	if s.UserEmail == "" {
		return ""
	}
	return s.UserEmail + "@" + s.ServerURL
}

// ErrUnavailable means bw serve could not be reached.
var ErrUnavailable = errors.New("bw serve unavailable")

// ErrNotLoggedIn means the bw CLI has no logged in account (run bw login).
var ErrNotLoggedIn = errors.New("bw is not logged in")

// ErrLocked means the vault must be unlocked first.
var ErrLocked = errors.New("vault is locked")

// Client is a bw serve REST client.
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:8087
	HTTP    *http.Client
	done    <-chan struct{}
}

// WithDone attaches a channel closed when the server goes away (e.g.
// Server.Done for a managed bw serve) and returns c.
func (c *Client) WithDone(done <-chan struct{}) *Client {
	c.done = done
	return c
}

// Done is closed when the managed server exits; nil (never) otherwise.
func (c *Client) Done() <-chan struct{} { return c.done }

// NewClient returns a client for the given base URL.
func NewClient(base string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 60 * time.Second}}
}

type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		secret.Zero(raw)
		return nil, fmt.Errorf("bw %s %s: HTTP %d: invalid response", method, path, resp.StatusCode)
	}
	if !env.Success {
		secret.Zero(raw)
		msg := env.Message
		if strings.Contains(strings.ToLower(msg), "locked") {
			return nil, ErrLocked
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("bw %s: %s", path, msg)
	}
	// env.Data aliases raw; callers wipe it when it holds a secret.
	return env.Data, nil
}

func (c *Client) list(ctx context.Context, object string, q url.Values, out any) error {
	path := "/list/object/" + object
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	var l struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &l); err != nil {
		return fmt.Errorf("list %s: %w", object, err)
	}
	if err := json.Unmarshal(l.Data, out); err != nil {
		return fmt.Errorf("list %s: %w", object, err)
	}
	return nil
}

// Status returns the vault status.
func (c *Client) Status(ctx context.Context) (StatusInfo, error) {
	data, err := c.do(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return StatusInfo{}, err
	}
	var t struct {
		Template StatusInfo `json:"template"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return StatusInfo{}, err
	}
	return t.Template, nil
}

// Unlock unlocks the vault with the master password. The password is sent in
// the request body to the local bw serve only; password is wiped on return.
func (c *Client) Unlock(ctx context.Context, password []byte) error {
	defer secret.Zero(password)
	body := make([]byte, 0, len(password)+32)
	body = append(body, `{"password":`...)
	body = appendJSONString(body, password)
	body = append(body, '}')
	defer secret.Zero(body)
	data, err := c.do(ctx, http.MethodPost, "/unlock", body)
	secret.Zero(data) // contains the session key
	return err
}

// Lock locks the vault.
func (c *Client) Lock(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/lock", nil)
	return err
}

// Sync pulls the latest vault data from the server.
func (c *Client) Sync(ctx context.Context) error {
	_, err := c.do(ctx, http.MethodPost, "/sync", nil)
	return err
}

// Snapshot fetches all metadata. It does not sync; call Sync first if needed.
func (c *Client) Snapshot(ctx context.Context) (model.Snapshot, error) {
	st, err := c.Status(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	if st.Status != Unlocked {
		return model.Snapshot{}, ErrLocked
	}
	var (
		items   []bwItem
		folders []bwFolder
		orgs    []bwOrg
		cols    []bwCollection
	)
	if err := c.list(ctx, "items", nil, &items); err != nil {
		return model.Snapshot{}, err
	}
	if err := c.list(ctx, "folders", nil, &folders); err != nil {
		return model.Snapshot{}, err
	}
	if err := c.list(ctx, "organizations", nil, &orgs); err != nil {
		return model.Snapshot{}, err
	}
	if err := c.list(ctx, "collections", nil, &cols); err != nil {
		return model.Snapshot{}, err
	}
	s := buildSnapshot(items, folders, orgs, cols)
	s.Account = st.Account()
	// The time of the last successful sync with the server (bw keeps working
	// from its local encrypted copy when the server is unreachable).
	s.SyncedAt = time.Now()
	if t, err := time.Parse(time.RFC3339, st.LastSync); err == nil {
		s.SyncedAt = t.Local()
	}
	return s, nil
}

// Secret fetches the password field of an item and classifies it as password
// or private key. The caller must Wipe the result.
func (c *Client) Secret(ctx context.Context, itemID string) (secret.Secret, error) {
	data, err := c.do(ctx, http.MethodGet, "/object/password/"+url.PathEscape(itemID), nil)
	if err != nil {
		return secret.Secret{}, err
	}
	defer secret.Zero(data)
	var s struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return secret.Secret{}, errors.New("bw: invalid password response")
	}
	raw, err := unquoteJSON(s.Data)
	if err != nil {
		return secret.Secret{}, err
	}
	defer secret.Zero(raw)
	return secret.New(raw), nil
}

// unquoteJSON decodes a JSON string literal into a fresh byte slice without
// going through an immutable Go string.
func unquoteJSON(lit []byte) ([]byte, error) {
	lit = bytes.TrimSpace(lit)
	if len(lit) < 2 || lit[0] != '"' || lit[len(lit)-1] != '"' {
		return nil, errors.New("bw: password is not a string")
	}
	lit = lit[1 : len(lit)-1]
	out := make([]byte, 0, len(lit))
	for i := 0; i < len(lit); i++ {
		c := lit[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(lit) {
			return nil, errors.New("bw: bad escape")
		}
		switch lit[i] {
		case '"', '\\', '/':
			out = append(out, lit[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r, n, err := hex4(lit[i+1:])
			if err != nil {
				return nil, err
			}
			i += n
			if utf16.IsSurrogate(r) && i+2 < len(lit) && lit[i+1] == '\\' && lit[i+2] == 'u' {
				r2, n2, err := hex4(lit[i+3:])
				if err == nil {
					r = utf16.DecodeRune(r, r2)
					i += 2 + n2
				}
			}
			out = utf8.AppendRune(out, r)
		default:
			return nil, errors.New("bw: bad escape")
		}
	}
	return out, nil
}

func hex4(b []byte) (rune, int, error) {
	if len(b) < 4 {
		return 0, 0, errors.New("bw: bad \\u escape")
	}
	var r rune
	for _, c := range b[:4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			r |= rune(c - 'A' + 10)
		default:
			return 0, 0, errors.New("bw: bad \\u escape")
		}
	}
	return r, 4, nil
}

// appendJSONString appends s as a JSON string literal.
func appendJSONString(dst, s []byte) []byte {
	const hexd = "0123456789abcdef"
	dst = append(dst, '"')
	for _, c := range s {
		switch {
		case c == '"' || c == '\\':
			dst = append(dst, '\\', c)
		case c < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hexd[c>>4], hexd[c&0xf])
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '"')
}

// Notes fetches the notes of an item. Notes are not part of the model (they
// may hold secrets): they are read for display or editing only.
func (c *Client) Notes(ctx context.Context, itemID string) (string, error) {
	data, err := c.do(ctx, http.MethodGet, "/object/item/"+url.PathEscape(itemID), nil)
	if err != nil {
		return "", err
	}
	defer secret.Zero(data) // the whole item: password, hidden fields
	var it struct {
		Notes *string `json:"notes"`
	}
	if err := json.Unmarshal(data, &it); err != nil {
		return "", errors.New("bw: invalid item response")
	}
	return str(it.Notes), nil
}
