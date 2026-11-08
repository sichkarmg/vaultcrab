// SPDX-License-Identifier: MIT

package jump

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// KeyKind says where the key of a hop comes from.
type KeyKind int

const (
	KeyNone  KeyKind = iota // not set
	KeyFile                 // a private key file on this computer
	KeyAgent                // the ssh-agent (SSH_AUTH_SOCK, Windows OpenSSH agent)
	KeyVault                // the password or key of a vault item
)

// Key is a parsed key reference of a template (key:, jump_key:):
//
//	~/.ssh/id_ed25519   a file (~ is the home directory)
//	agent               the ssh-agent
//	vault:<item>        the credential of a vault item (name or ID)
type Key struct {
	Kind  KeyKind
	Value string // the expanded path, or the vault item name/ID
}

// ParseKey parses a key reference ("" = KeyNone).
func ParseKey(s string) (Key, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return Key{}, nil
	case strings.EqualFold(s, "agent"):
		return Key{Kind: KeyAgent}, nil
	case strings.HasPrefix(strings.ToLower(s), "vault:"):
		v := strings.TrimSpace(s[len("vault:"):])
		if v == "" {
			return Key{}, errors.New("vault: needs the name or ID of an item")
		}
		return Key{Kind: KeyVault, Value: v}, nil
	}
	return Key{Kind: KeyFile, Value: ExpandHome(s)}, nil
}

// String returns the reference as written in the config.
func (k Key) String() string {
	switch k.Kind {
	case KeyAgent:
		return "agent"
	case KeyVault:
		return "vault:" + k.Value
	case KeyFile:
		return k.Value
	}
	return ""
}

// ExpandHome replaces a leading ~ with the home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
