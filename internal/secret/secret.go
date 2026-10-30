// SPDX-License-Identifier: MIT

// Package secret holds credentials fetched from the vault for the duration of a
// single connection attempt. Secrets are never persisted; call Wipe as soon as
// the value is no longer needed.
package secret

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
)

// Kind is the type of credential stored in the password field.
type Kind int

const (
	// Password is a plain password.
	Password Kind = iota
	// PrivateKey is a PEM-encoded private key (possibly flattened in the vault).
	PrivateKey
)

func (k Kind) String() string {
	if k == PrivateKey {
		return "key"
	}
	return "password"
}

// Secret is a credential. Value holds the password bytes, or a normalized PEM
// block for keys. The zero value is an empty password.
type Secret struct {
	Kind  Kind
	Value []byte
}

// New classifies the raw vault field value. The input slice is not retained:
// the returned Secret owns a fresh copy, and the caller should wipe raw.
func New(raw []byte) Secret {
	if pem, ok := NormalizePEM(raw); ok {
		return Secret{Kind: PrivateKey, Value: pem}
	}
	return Secret{Kind: Password, Value: bytes.Clone(raw)}
}

// FromString is a convenience wrapper around New. Strings cannot be wiped, so
// prefer New where the raw bytes are available.
func FromString(s string) Secret { return New([]byte(s)) }

// IsZero reports whether the secret is empty.
func (s *Secret) IsZero() bool { return len(s.Value) == 0 }

// Wipe overwrites the secret value in memory.
func (s *Secret) Wipe() {
	Zero(s.Value)
	s.Value = nil
}

// Zero overwrites b with zeros.
func Zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// String never reveals the value.
func (s Secret) String() string { return "secret(" + s.Kind.String() + ")" }

// GoString never reveals the value.
func (s Secret) GoString() string { return s.String() }

var (
	beginRe = regexp.MustCompile(`-----BEGIN ([A-Z0-9 ]+)-----`)
	// headerRe matches RFC 1421 style headers inside traditional PEM blocks
	// (e.g. "Proc-Type: 4,ENCRYPTED").
	headerRe = regexp.MustCompile(`^[A-Za-z0-9-]+:`)
)

// ErrNotPEM is returned by ParseFlattened when no PEM block is present.
var ErrNotPEM = errors.New("no PEM block found")

// NormalizePEM detects a private key in raw and restores it to canonical PEM:
// BEGIN line, optional headers, base64 body wrapped at 64 columns, END line.
// It accepts keys whose newlines were replaced by spaces, CRLF line endings,
// surrounding whitespace and quoting. The result is a fresh buffer.
func NormalizePEM(raw []byte) ([]byte, bool) {
	out, err := normalize(raw)
	return out, err == nil
}

func normalize(raw []byte) ([]byte, error) {
	m := beginRe.FindSubmatchIndex(raw)
	if m == nil {
		return nil, ErrNotPEM
	}
	label := string(raw[m[2]:m[3]])
	if !strings.Contains(label, "PRIVATE KEY") {
		return nil, ErrNotPEM
	}
	endMarker := []byte("-----END " + label + "-----")
	rest := raw[m[1]:]
	e := bytes.Index(rest, endMarker)
	if e < 0 {
		return nil, errors.New("PEM END marker not found")
	}
	body := rest[:e]

	var out bytes.Buffer
	out.Grow(len(body) + 2*len(endMarker) + len(body)/64 + 8)
	out.WriteString("-----BEGIN " + label + "-----\n")

	// Split body into whitespace-separated tokens. Headers ("Key: value")
	// are rebuilt as separate lines; the base64 payload is joined and re-wrapped.
	fields := bytes.Fields(body)
	var payload []byte
	defer func() { Zero(payload) }()
	inHeader := false
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if headerRe.Match(f) && len(payload) == 0 {
			// Header: name and value(s) up to the next token that is not part of it.
			line := append([]byte{}, f...)
			inHeader = true
			for i+1 < len(fields) && !looksLikeBase64Line(fields[i+1]) && !headerRe.Match(fields[i+1]) {
				i++
				line = append(line, ' ')
				line = append(line, fields[i]...)
			}
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		if inHeader {
			// Blank line separates headers from the payload.
			out.WriteByte('\n')
			inHeader = false
		}
		payload = append(payload, f...)
	}
	if len(payload) == 0 {
		return nil, errors.New("empty PEM body")
	}
	for len(payload) > 0 {
		n := min(64, len(payload))
		out.Write(payload[:n])
		out.WriteByte('\n')
		payload = payload[n:]
	}
	out.Write(endMarker)
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// looksLikeBase64Line distinguishes the first payload token from a header
// value continuation. Header values in practice are short ("4,ENCRYPTED",
// "AES-128-CBC,HEX"), payload chunks are long base64 runs.
func looksLikeBase64Line(tok []byte) bool {
	if len(tok) < 32 {
		return false
	}
	for _, c := range tok {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}
