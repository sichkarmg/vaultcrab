// SPDX-License-Identifier: MIT

// Package clipboard copies text to the clipboard: the system clipboard of
// this machine (xclip/xsel/wl-copy on Linux, pbcopy on macOS, the Win32 API
// on Windows) and, via an OSC 52 escape sequence, the clipboard of the
// terminal vaultcrab runs in (works over SSH, in tmux with set-clipboard on,
// Windows Terminal, iTerm2, kitty, ...).
package clipboard

import (
	"encoding/base64"
	"errors"

	"github.com/atotto/clipboard"
)

// maxOSC52 bounds the copied text: many terminals ignore longer sequences.
const maxOSC52 = 100 << 10

// OSC52 returns the escape sequence that asks the terminal to put text on
// its clipboard, or "" if the text is too long for it.
func OSC52(text string) string {
	if len(text) > maxOSC52 {
		return ""
	}
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

// WriteSystem puts text on the system clipboard of this machine. On X11
// without xclip/xsel the built-in X11 clipboard is used (vaultcrab serves the
// text while it runs).
func WriteSystem(text string) error {
	err := clipboard.WriteAll(text)
	if err == nil || !x11Available() {
		return clipboardErr(err)
	}
	return writeX11(text)
}

// ReadSystem returns the text on the system clipboard of this machine.
func ReadSystem() (string, error) {
	text, err := clipboard.ReadAll()
	if err == nil || !x11Available() {
		return text, clipboardErr(err)
	}
	return readX11()
}

// clipboardErr makes the error of a missing clipboard tool say what to do.
func clipboardErr(err error) error {
	if err != nil && clipboard.Unsupported {
		return errors.New("no system clipboard: install xclip or xsel (X11) or wl-clipboard (Wayland)")
	}
	return err
}
