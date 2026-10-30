// SPDX-License-Identifier: MIT

package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// special maps Bubble Tea key types to terminal keys with modifiers.
var special = map[tea.KeyType]core.Key{
	tea.KeyUp:     {Code: core.KeyUp},
	tea.KeyDown:   {Code: core.KeyDown},
	tea.KeyRight:  {Code: core.KeyRight},
	tea.KeyLeft:   {Code: core.KeyLeft},
	tea.KeyHome:   {Code: core.KeyHome},
	tea.KeyEnd:    {Code: core.KeyEnd},
	tea.KeyPgUp:   {Code: core.KeyPgUp},
	tea.KeyPgDown: {Code: core.KeyPgDown},
	tea.KeyDelete: {Code: core.KeyDelete},
	tea.KeyInsert: {Code: core.KeyInsert},
	tea.KeySpace:  {Text: " "},

	tea.KeyShiftTab: {Code: core.KeyTab, Shift: true},

	tea.KeyCtrlPgUp:   {Code: core.KeyPgUp, Ctrl: true},
	tea.KeyCtrlPgDown: {Code: core.KeyPgDown, Ctrl: true},
	tea.KeyCtrlUp:     {Code: core.KeyUp, Ctrl: true},
	tea.KeyCtrlDown:   {Code: core.KeyDown, Ctrl: true},
	tea.KeyCtrlRight:  {Code: core.KeyRight, Ctrl: true},
	tea.KeyCtrlLeft:   {Code: core.KeyLeft, Ctrl: true},
	tea.KeyCtrlHome:   {Code: core.KeyHome, Ctrl: true},
	tea.KeyCtrlEnd:    {Code: core.KeyEnd, Ctrl: true},

	tea.KeyShiftUp:    {Code: core.KeyUp, Shift: true},
	tea.KeyShiftDown:  {Code: core.KeyDown, Shift: true},
	tea.KeyShiftRight: {Code: core.KeyRight, Shift: true},
	tea.KeyShiftLeft:  {Code: core.KeyLeft, Shift: true},
	tea.KeyShiftHome:  {Code: core.KeyHome, Shift: true},
	tea.KeyShiftEnd:   {Code: core.KeyEnd, Shift: true},

	tea.KeyCtrlShiftUp:    {Code: core.KeyUp, Ctrl: true, Shift: true},
	tea.KeyCtrlShiftDown:  {Code: core.KeyDown, Ctrl: true, Shift: true},
	tea.KeyCtrlShiftLeft:  {Code: core.KeyLeft, Ctrl: true, Shift: true},
	tea.KeyCtrlShiftRight: {Code: core.KeyRight, Ctrl: true, Shift: true},
	tea.KeyCtrlShiftHome:  {Code: core.KeyHome, Ctrl: true, Shift: true},
	tea.KeyCtrlShiftEnd:   {Code: core.KeyEnd, Ctrl: true, Shift: true},
}

func init() {
	// Both libraries declare F1..F20 consecutively (Bubble Tea counts down).
	for n := 0; n < 20; n++ {
		special[tea.KeyF1-tea.KeyType(n)] = core.Key{Code: core.KeyF1 + rune(n)}
	}
}

// TermKey converts a Bubble Tea key event into a terminal key. ok is false
// for events that have no terminal representation.
func TermKey(m tea.KeyMsg) (core.Key, bool) {
	switch {
	case m.Type == tea.KeyRunes:
		if len(m.Runes) == 0 {
			return core.Key{}, false
		}
		return core.Key{Text: string(m.Runes), Alt: m.Alt}, true
	case m.Type >= 0 && m.Type < 32 || m.Type == 127:
		// Control characters (Enter, Tab, Backspace, Esc, Ctrl+letter) are
		// sent as the raw byte, ESC-prefixed with Alt.
		raw := string(rune(m.Type))
		if m.Alt {
			raw = "\x1b" + raw
		}
		return core.Key{Raw: raw}, true
	}
	k, ok := special[m.Type]
	if !ok {
		return core.Key{}, false
	}
	k.Alt = k.Alt || m.Alt
	return k, true
}
