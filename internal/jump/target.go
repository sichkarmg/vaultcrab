// SPDX-License-Identifier: MIT

// Package jump holds what the quick connections through a jump host need
// besides the SSH client: parsing what the user types (the target, the jump
// host of a template, the key reference), the local history of the
// connections (frecency) and the fuzzy search of the picker. Nothing here
// holds a secret.
package jump

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Target is where to connect: [user@]host[:port].
type Target struct {
	User string
	Host string // name or IP address (IPv6 without brackets)
	Port int    // 0 = the default
}

// Addr returns host:port with the given default port.
func (t Target) Addr(def int) string {
	p := t.Port
	if p == 0 {
		p = def
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(p))
}

// String returns [user@]host[:port] (IPv6 in brackets when a port follows).
func (t Target) String() string {
	var b strings.Builder
	if t.User != "" {
		b.WriteString(t.User + "@")
	}
	b.WriteString(t.HostPort())
	return b.String()
}

// HostPort returns host[:port] without the user.
func (t Target) HostPort() string {
	if t.Port == 0 {
		if strings.Contains(t.Host, ":") {
			return "[" + t.Host + "]"
		}
		return t.Host
	}
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

// ErrEmpty is returned for an empty input.
var ErrEmpty = errors.New("enter an IP address or a host name ([user@]host[:port])")

// ParseTarget parses [user@]host[:port]: host is a name, an IPv4 address or
// an IPv6 address (in brackets when a port follows: [fe80::1]:2222).
func ParseTarget(s string) (Target, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Target{}, ErrEmpty
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return Target{}, fmt.Errorf("%q: no spaces allowed", s)
	}
	var t Target
	if i := strings.LastIndex(s, "@"); i >= 0 {
		t.User, s = s[:i], s[i+1:]
		if t.User == "" {
			return Target{}, errors.New("empty user name before @")
		}
		if strings.ContainsAny(t.User, "/\\:") {
			return Target{}, fmt.Errorf("user %q: invalid characters", t.User)
		}
	}
	host, port := s, ""
	switch {
	case strings.HasPrefix(s, "["):
		end := strings.Index(s, "]")
		if end < 0 {
			return Target{}, fmt.Errorf("%q: missing ]", s)
		}
		host, port = s[1:end], strings.TrimPrefix(s[end+1:], ":")
		if s[end+1:] != "" && !strings.HasPrefix(s[end+1:], ":") {
			return Target{}, fmt.Errorf("%q: expected :port after ]", s)
		}
	case strings.Count(s, ":") == 1:
		host, port, _ = strings.Cut(s, ":")
	case strings.Count(s, ":") > 1:
		// a bare IPv6 address, no port
	}
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return Target{}, fmt.Errorf("port %q: want 1..65535", port)
		}
		t.Port = p
	} else if strings.HasSuffix(s, ":") {
		return Target{}, errors.New("empty port after :")
	}
	if err := checkHost(host); err != nil {
		return Target{}, err
	}
	t.Host = host
	return t, nil
}

// checkHost accepts an IP address or a DNS name (letters, digits, -, _, .).
func checkHost(h string) error {
	if h == "" {
		return errors.New("empty host")
	}
	if net.ParseIP(h) != nil {
		return nil
	}
	if strings.Contains(h, ":") {
		return fmt.Errorf("%q: not a valid IPv6 address", h)
	}
	if len(h) > 253 {
		return fmt.Errorf("host name too long")
	}
	labels := strings.Split(strings.TrimSuffix(h, "."), ".")
	if last := labels[len(labels)-1]; last != "" && strings.Trim(last, "0123456789") == "" {
		// no top-level domain is numeric: a mistyped IPv4 address
		return fmt.Errorf("%q: not a valid IPv4 address", h)
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("%q: not a valid host name", h)
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return fmt.Errorf("%q: not a valid host name or IP address", h)
			}
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%q: not a valid host name", h)
		}
	}
	return nil
}
