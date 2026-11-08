// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sichkarmg/vaultcrab/internal/jump"
)

// Host key checks of a jump template (host_key_check).
const (
	HostKeyAsk       = "ask"        // a new key is shown and must be accepted (default)
	HostKeyAcceptNew = "accept-new" // a new key is remembered silently
	HostKeyStrict    = "strict"     // only keys already in known_hosts
)

// HostKeyChecks lists the valid values of host_key_check.
var HostKeyChecks = []string{HostKeyAsk, HostKeyAcceptNew, HostKeyStrict}

// JumpTemplate is a quick connection through a jump host to an address
// typed when connecting (jump_templates:). No secrets: keys are files, the
// agent or vault items.
type JumpTemplate struct {
	Name string `yaml:"name"`
	// Jump is the jump host: a vault item (name or ID) or [user@]host[:port].
	Jump string `yaml:"jump"`
	// User on the target (empty: typed as user@host, else the jump user).
	User string `yaml:"user,omitempty"`
	Port int    `yaml:"port,omitempty"` // of the target (0 = 22)
	// Key of the target: a file (~/.ssh/id_ed25519), "agent" or
	// "vault:<item>".
	Key string `yaml:"key,omitempty"`
	// JumpKey of the jump host (same forms; empty: the credential of the
	// vault item of jump, else key).
	JumpKey      string `yaml:"jump_key,omitempty"`
	HostKeyCheck string `yaml:"host_key_check,omitempty"` // ask (default) | accept-new | strict
}

// Check returns why t is not valid (nil if it is).
func (t JumpTemplate) Check() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(t.Jump) == "" {
		return fmt.Errorf("%s: jump is required (a vault item or [user@]host[:port])", t.Name)
	}
	if t.Port < 0 || t.Port > 65535 {
		return fmt.Errorf("%s: port %d (want 1..65535)", t.Name, t.Port)
	}
	if _, err := jump.ParseKey(t.Key); err != nil {
		return fmt.Errorf("%s: key: %w", t.Name, err)
	}
	if _, err := jump.ParseKey(t.JumpKey); err != nil {
		return fmt.Errorf("%s: jump_key: %w", t.Name, err)
	}
	switch t.HostKeyCheck {
	case "", HostKeyAsk, HostKeyAcceptNew, HostKeyStrict:
	default:
		return fmt.Errorf("%s: host_key_check %q (want ask|accept-new|strict)", t.Name, t.HostKeyCheck)
	}
	return nil
}

// HostKeys returns host_key_check with the default.
func (t JumpTemplate) HostKeys() string {
	if t.HostKeyCheck == "" {
		return HostKeyAsk
	}
	return t.HostKeyCheck
}

// JumpSettings configure the quick connections through jump hosts (jump:).
type JumpSettings struct {
	// History remembers the jump hosts and targets used (names and
	// addresses only) for the picker; off: nothing is written.
	History      bool `yaml:"history"`
	HistoryLimit int  `yaml:"history_limit"` // entries kept (default 200)
}

func (c *Config) finishJump() error {
	if c.Jump.HistoryLimit < 0 {
		return fmt.Errorf("jump.history_limit: %d (want 0 for the default, or more)", c.Jump.HistoryLimit)
	}
	if c.Jump.HistoryLimit == 0 {
		c.Jump.HistoryLimit = jump.DefaultHistoryLimit
	}
	names := map[string]bool{}
	for i, t := range c.JumpTemplates {
		if err := t.Check(); err != nil {
			return fmt.Errorf("jump_templates[%d]: %w", i, err)
		}
		k := strings.ToLower(strings.TrimSpace(t.Name))
		if names[k] {
			return fmt.Errorf("jump_templates: name %q twice", t.Name)
		}
		names[k] = true
	}
	return nil
}

// SaveJumpTemplates writes ts as the jump_templates: of the config file at
// path (created if missing); the rest of the file, comments included, is
// kept.
func SaveJumpTemplates(path string, ts []JumpTemplate) error {
	names := map[string]bool{}
	for _, t := range ts {
		if err := t.Check(); err != nil {
			return err
		}
		k := strings.ToLower(strings.TrimSpace(t.Name))
		if names[k] {
			return fmt.Errorf("the name %q is used twice", t.Name)
		}
		names[k] = true
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
			add("user", strings.TrimSpace(t.User))
			if t.Port != 0 {
				item.Content = append(item.Content, str("port"),
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(t.Port)})
			}
			add("key", strings.TrimSpace(t.Key))
			add("jump_key", strings.TrimSpace(t.JumpKey))
			add("host_key_check", t.HostKeyCheck)
			seq.Content = append(seq.Content, item)
		}
		setPath(root, []string{"jump_templates"}, seq)
	})
}

// SetJumpHistory writes jump.history (whether the connections through jump
// hosts are remembered) to the config file at path.
func SetJumpHistory(path string, on bool) error {
	return editConfig(path, func(root *yaml.Node) {
		setPath(root, []string{"jump", "history"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(on)})
	})
}
