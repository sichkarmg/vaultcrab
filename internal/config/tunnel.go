// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tunnel types (tunnels[].type).
const (
	TunnelLocal = "local" // forward the local port to target (ssh -L), the default
	TunnelSOCKS = "socks" // a SOCKS5 proxy on the local port (ssh -D)
)

// Tunnel is a saved port forwarding through a jump host or an SSH host
// (tunnels:). No secrets: the keys are those of the jump host.
type Tunnel struct {
	Name string `yaml:"name"`
	// Jump forwards: a jump template (its name) or an SSH host of the
	// vault (name or ID).
	Jump string `yaml:"jump"`
	// Target is host:port as seen from the jump host (not for socks).
	Target string `yaml:"target,omitempty"`
	// LocalPort is the port on this computer (0 = auto: any free one).
	LocalPort LocalPort `yaml:"local_port,omitempty"`
	// Open is the URL opened in the browser ({port}, {host}); empty:
	// http://localhost:{port}.
	Open string `yaml:"open,omitempty"`
	Type string `yaml:"type,omitempty"` // local (default) | socks
	// Bind is the local address (default 127.0.0.1: this computer only).
	Bind string `yaml:"bind,omitempty"`
}

// LocalPort is local_port: a number or auto (0).
type LocalPort int

// UnmarshalYAML accepts a number, "auto" or nothing.
func (p *LocalPort) UnmarshalYAML(n *yaml.Node) error {
	v := strings.TrimSpace(n.Value)
	if v == "" || strings.EqualFold(v, "auto") {
		*p = 0
		return nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("local_port %q: want a port number or auto", v)
	}
	*p = LocalPort(i)
	return nil
}

// String is "auto" or the number.
func (p LocalPort) String() string {
	if p == 0 {
		return "auto"
	}
	return strconv.Itoa(int(p))
}

// ParseLocalPort reads a typed local port ("", "auto" or 1..65535).
func ParseLocalPort(s string) (LocalPort, error) {
	var p LocalPort
	if err := p.UnmarshalYAML(&yaml.Node{Value: s}); err != nil {
		return 0, err
	}
	if p < 0 || p > 65535 {
		return 0, fmt.Errorf("local_port %d: want 1..65535 or auto", p)
	}
	return p, nil
}

// Kind returns type with the default.
func (t Tunnel) Kind() string {
	if t.Type == "" {
		return TunnelLocal
	}
	return t.Type
}

// Check returns why t is not valid (nil if it is).
func (t Tunnel) Check() error {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(t.Jump) == "" {
		return fmt.Errorf("%s: jump is required (a jump template or an SSH host of the vault)", name)
	}
	switch t.Kind() {
	case TunnelLocal:
		if err := CheckHostPort(t.Target); err != nil {
			return fmt.Errorf("%s: target: %w", name, err)
		}
	case TunnelSOCKS:
		if strings.TrimSpace(t.Target) != "" {
			return fmt.Errorf("%s: a socks tunnel has no target", name)
		}
	default:
		return fmt.Errorf("%s: type %q (want local|socks)", name, t.Type)
	}
	if t.LocalPort < 0 || t.LocalPort > 65535 {
		return fmt.Errorf("%s: local_port %d (want 1..65535 or auto)", name, t.LocalPort)
	}
	if b := strings.TrimSpace(t.Bind); b != "" && b != "localhost" && net.ParseIP(b) == nil {
		return fmt.Errorf("%s: bind %q: want an IP address of this computer", name, t.Bind)
	}
	return nil
}

// CheckHostPort checks host:port (an IPv6 address in brackets).
func CheckHostPort(s string) error {
	h, p, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil || h == "" {
		return fmt.Errorf("%q: want host:port", s)
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q: port %q (want 1..65535)", s, p)
	}
	return nil
}

// TunnelSettings configure all tunnels (tunnel:).
type TunnelSettings struct {
	// Browser is the command opening a URL, like the external programs: a
	// list of arguments, {url} replaced (added at the end if missing);
	// empty: open (macOS), xdg-open (Linux), rundll32 url.dll (Windows).
	Browser []string `yaml:"browser"`
	// Attempts is the number of reconnection attempts after a loss of the
	// connection before a tunnel fails (default 8).
	Attempts int `yaml:"attempts"`
}

// DefaultTunnelAttempts is the default of tunnel.attempts.
const DefaultTunnelAttempts = 8

func (c *Config) finishTunnels() error {
	if c.Tunnel.Attempts < 0 {
		return fmt.Errorf("tunnel.attempts: %d (want 0 for the default, or more)", c.Tunnel.Attempts)
	}
	if c.Tunnel.Attempts == 0 {
		c.Tunnel.Attempts = DefaultTunnelAttempts
	}
	return checkTunnels(c.Tunnels)
}

func checkTunnels(ts []Tunnel) error {
	names := map[string]bool{}
	for i, t := range ts {
		if err := t.Check(); err != nil {
			return fmt.Errorf("tunnels[%d]: %w", i, err)
		}
		k := strings.ToLower(strings.TrimSpace(t.Name))
		if names[k] {
			return fmt.Errorf("tunnels: name %q twice", t.Name)
		}
		names[k] = true
	}
	return nil
}

// SaveTunnels writes ts as the tunnels: of the config file at path
// (created if missing); the rest of the file, comments included, is kept.
func SaveTunnels(path string, ts []Tunnel) error {
	if err := checkTunnels(ts); err != nil {
		return err
	}
	return editConfig(path, func(root *yaml.Node) {
		str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, t := range ts {
			item := &yaml.Node{Kind: yaml.MappingNode}
			add := func(k, v string) {
				if v != "" {
					item.Content = append(item.Content, str(k), str(v))
				}
			}
			add("name", strings.TrimSpace(t.Name))
			add("jump", strings.TrimSpace(t.Jump))
			add("target", strings.TrimSpace(t.Target))
			if t.LocalPort == 0 {
				add("local_port", "auto")
			} else {
				item.Content = append(item.Content, str("local_port"),
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(int(t.LocalPort))})
			}
			add("open", strings.TrimSpace(t.Open))
			if t.Kind() != TunnelLocal {
				add("type", t.Type)
			}
			add("bind", strings.TrimSpace(t.Bind))
			seq.Content = append(seq.Content, item)
		}
		setPath(root, []string{"tunnels"}, seq)
	})
}
