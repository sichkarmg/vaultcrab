// SPDX-License-Identifier: MIT

// Package scripts selects user-defined launch scripts for vault items. A
// script is a command line with placeholders, run either in a terminal tab
// (on a local pseudo-terminal) or as a detached program. No UI code.
package scripts

import (
	"strings"

	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/model"
)

// Mode is where a script runs.
type Mode string

const (
	// Terminal runs the command in a new terminal tab.
	Terminal Mode = "terminal"
	// External starts the command as a separate (GUI) program.
	External Mode = "external"
)

// Field is the item custom field that names the script run by Enter for
// that item (instead of the built-in action).
const Field = "script"

// Script is a launch script for the current OS.
type Script struct {
	Name          string
	Rule          filter.Rule // items the script applies to (empty = all)
	Command       []string    // with placeholders; empty = not available on this OS
	Mode          Mode
	PasswordStdin bool // external only: write the item password to stdin
	Default       bool // run by Enter instead of the built-in action
}

// For returns the scripts available for h, in configuration order.
func For(list []Script, h *model.Host, snap *model.Snapshot) []Script {
	var out []Script
	for _, s := range list {
		if len(s.Command) > 0 && s.Rule.Match(h, snap) {
			out = append(out, s)
		}
	}
	return out
}

// DefaultFor returns the script Enter runs for h: the one named by the item
// field "script", else the first available script marked default.
func DefaultFor(list []Script, h *model.Host, snap *model.Snapshot) (Script, bool) {
	avail := For(list, h, snap)
	if name := strings.TrimSpace(h.Fields[Field]); name != "" {
		for _, s := range avail {
			if strings.EqualFold(s.Name, name) {
				return s, true
			}
		}
	}
	for _, s := range avail {
		if s.Default {
			return s, true
		}
	}
	return Script{}, false
}
