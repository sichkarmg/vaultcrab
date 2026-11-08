// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// HostKeyMode says what happens with a host key not in known_hosts. A key
// that differs from the one remembered for the address is always refused
// (HostKeyError), whatever the mode.
type HostKeyMode int

const (
	HostKeyAcceptNew HostKeyMode = iota // remember it silently (trust on first use)
	HostKeyAsk                          // show it and let the user decide
	HostKeyStrict                       // refuse it
)

// HostKeyAnswer is the user's decision about a new host key.
type HostKeyAnswer int

const (
	HostKeyReject  HostKeyAnswer = iota
	HostKeyOnce                  // accept for this connection, do not remember
	HostKeyForever               // accept and add to known_hosts
)

// HostKeyQuestion describes a new host key to the user.
type HostKeyQuestion struct {
	Role        string // "jump host" or "target"
	Name        string // what the user knows the host as
	Host        string // host:port as connected
	KeyType     string
	Fingerprint string // SHA256:...
}

// HostKeyPolicy checks the host key of one hop.
type HostKeyPolicy struct {
	File string // known_hosts of Vaultcrab ("" = remember nothing)
	Mode HostKeyMode
	// Ask is called for a new key in HostKeyAsk mode (nil: refused).
	Ask func(ctx context.Context, q HostKeyQuestion) (HostKeyAnswer, error)
	// Role and Name describe the hop in the question.
	Role, Name string
}

// HostKeyRejectedError: the user refused a new host key (or could not be
// asked).
type HostKeyRejectedError struct {
	Host, Fingerprint string
	Reason            string
}

func (e *HostKeyRejectedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("host key of %s (%s) not accepted: %s", e.Host, e.Fingerprint, e.Reason)
	}
	return fmt.Sprintf("host key of %s (%s) not accepted", e.Host, e.Fingerprint)
}

// callback returns the ssh.HostKeyCallback of the policy; pause(true)
// stops the handshake timer while the user is asked.
func (p HostKeyPolicy) callback(ctx context.Context, pause func(bool)) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		tracef(ctx, "server host key: %s %s", key.Type(), fp)
		known, err := p.lookup(hostname, remote, key)
		switch {
		case err != nil:
			tracef(ctx, "host key check failed: %v", err)
			return err
		case known:
			tracef(ctx, "host key found in %s", p.File)
			return nil
		}
		tracef(ctx, "host key not in known_hosts (%s); mode %s", p.File, [...]string{"accept-new", "ask", "strict"}[p.Mode])
		switch p.Mode {
		case HostKeyStrict:
			return &UnknownHostError{Host: hostname, Fingerprint: fp}
		case HostKeyAsk:
			if p.Ask == nil {
				return &HostKeyRejectedError{Host: hostname, Fingerprint: fp, Reason: "nobody to ask"}
			}
			if pause != nil {
				pause(true)
				defer pause(false)
			}
			ans, err := p.Ask(ctx, HostKeyQuestion{Role: p.Role, Name: p.Name, Host: hostname, KeyType: key.Type(), Fingerprint: fp})
			if err != nil {
				return &HostKeyRejectedError{Host: hostname, Fingerprint: fp, Reason: err.Error()}
			}
			tracef(ctx, "host key answer: %s", [...]string{"reject", "accept once", "accept and remember"}[ans])
			switch ans {
			case HostKeyOnce:
				return nil
			case HostKeyForever:
			default:
				return &HostKeyRejectedError{Host: hostname, Fingerprint: fp}
			}
		}
		return p.remember(hostname, key)
	}
}

// lookup reports whether key is the one remembered for hostname; a
// different remembered key is a HostKeyError.
func (p HostKeyPolicy) lookup(hostname string, remote net.Addr, key ssh.PublicKey) (bool, error) {
	if p.File == "" {
		return false, nil
	}
	khMu.Lock()
	defer khMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(p.File), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(p.File, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return false, err
	}
	f.Close()
	cb, err := knownhosts.New(p.File)
	if err != nil {
		return false, fmt.Errorf("known_hosts: %w", err)
	}
	err = cb(hostname, remote, key)
	var ke *knownhosts.KeyError
	switch {
	case err == nil:
		return true, nil
	case !errors.As(err, &ke):
		return false, err
	case len(ke.Want) > 0:
		return false, &HostKeyError{Host: hostname, Fingerprint: ssh.FingerprintSHA256(key), File: p.File, Line: ke.Want[0].Line}
	}
	return false, nil
}

// remember appends key for hostname to known_hosts.
func (p HostKeyPolicy) remember(hostname string, key ssh.PublicKey) error {
	if p.File == "" {
		return nil
	}
	khMu.Lock()
	defer khMu.Unlock()
	af, err := os.OpenFile(p.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer af.Close()
	_, err = fmt.Fprintln(af, knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
	return err
}
