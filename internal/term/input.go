// SPDX-License-Identifier: MIT

package term

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// KeyCode identifies a non-printable key; printable keys use their rune.
type KeyCode = rune

// Special keys (values match ultraviolet key codes).
const (
	KeyUp        KeyCode = uv.KeyUp
	KeyDown      KeyCode = uv.KeyDown
	KeyRight     KeyCode = uv.KeyRight
	KeyLeft      KeyCode = uv.KeyLeft
	KeyHome      KeyCode = uv.KeyHome
	KeyEnd       KeyCode = uv.KeyEnd
	KeyPgUp      KeyCode = uv.KeyPgUp
	KeyPgDown    KeyCode = uv.KeyPgDown
	KeyInsert    KeyCode = uv.KeyInsert
	KeyDelete    KeyCode = uv.KeyDelete
	KeyEnter     KeyCode = uv.KeyEnter
	KeyTab       KeyCode = uv.KeyTab
	KeyBackspace KeyCode = uv.KeyBackspace
	KeyEscape    KeyCode = uv.KeyEscape
	KeySpace     KeyCode = uv.KeySpace
	KeyF1        KeyCode = uv.KeyF1
)

// Key is a keyboard event in terminal-neutral form.
type Key struct {
	Code  KeyCode // special key, or the base rune for Ctrl/Alt combos
	Text  string  // printable text (may be several runes, e.g. IME)
	Raw   string  // bytes sent as-is (control characters); overrides the rest
	Ctrl  bool
	Alt   bool
	Shift bool
}

func (k Key) mod() uv.KeyMod {
	var m uv.KeyMod
	if k.Ctrl {
		m |= uv.ModCtrl
	}
	if k.Alt {
		m |= uv.ModAlt
	}
	if k.Shift {
		m |= uv.ModShift
	}
	return m
}

// SendKey encodes a key for the remote application, honoring cursor/keypad
// application modes.
func (t *Terminal) SendKey(k Key) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if k.Raw != "" {
		t.emu.SendText(k.Raw)
		return
	}
	if k.Text != "" && !k.Ctrl {
		s := k.Text
		if k.Alt {
			s = "\x1b" + s
		}
		t.emu.SendText(s)
		return
	}
	t.emu.SendKey(uv.KeyPressEvent{Code: k.Code, Mod: k.mod()})
}

// Paste sends text, wrapped in bracketed-paste markers if the remote enabled them.
func (t *Terminal) Paste(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bracketed {
		t.emu.SendText(ansi.BracketedPasteStart + s + ansi.BracketedPasteEnd)
		return
	}
	t.emu.SendText(s)
}

// MouseButton values.
type MouseButton = uv.MouseButton

const (
	MouseNone      = uv.MouseNone
	MouseLeft      = uv.MouseLeft
	MouseMiddle    = uv.MouseMiddle
	MouseRight     = uv.MouseRight
	MouseWheelUp   = uv.MouseWheelUp
	MouseWheelDown = uv.MouseWheelDown
)

// MouseAction is the kind of mouse event.
type MouseAction int

const (
	MousePress MouseAction = iota
	MouseRelease
	MouseMotion
)

// Mouse is a mouse event with coordinates relative to the terminal (0-based).
type Mouse struct {
	X, Y   int
	Button MouseButton
	Action MouseAction
	Shift  bool
	Alt    bool
	Ctrl   bool
}

// SendMouse forwards a mouse event if the remote application tracks the mouse.
// It reports whether the event was consumed.
func (t *Terminal) SendMouse(m Mouse) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	tracking := false
	for _, on := range t.mouse {
		tracking = tracking || on
	}
	if !tracking {
		return false
	}
	var mod uv.KeyMod
	if m.Shift {
		mod |= uv.ModShift
	}
	if m.Alt {
		mod |= uv.ModAlt
	}
	if m.Ctrl {
		mod |= uv.ModCtrl
	}
	um := uv.Mouse{X: m.X, Y: m.Y, Button: m.Button, Mod: mod}
	switch {
	case m.Button == MouseWheelUp || m.Button == MouseWheelDown:
		t.emu.SendMouse(uv.MouseWheelEvent(um))
	case m.Action == MouseRelease:
		t.emu.SendMouse(uv.MouseReleaseEvent(um))
	case m.Action == MouseMotion:
		t.emu.SendMouse(uv.MouseMotionEvent(um))
	default:
		t.emu.SendMouse(uv.MouseClickEvent(um))
	}
	return true
}
