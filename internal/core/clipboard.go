// SPDX-License-Identifier: MIT

package core

import "github.com/sichkarmg/vaultcrab/internal/clipboard"

// ClipboardOSC52 returns the escape sequence that puts text on the clipboard
// of the outer terminal ("" if too long).
func ClipboardOSC52(text string) string { return clipboard.OSC52(text) }

// CopyToSystem puts text on the system clipboard of this machine.
func CopyToSystem(text string) error { return clipboard.WriteSystem(text) }

// PasteFromSystem returns the text on the system clipboard of this machine.
func PasteFromSystem() (string, error) { return clipboard.ReadSystem() }
