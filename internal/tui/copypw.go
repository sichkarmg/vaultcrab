// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// clipboardClearAfter: the system clipboard is emptied after this time if it
// still holds the copied password.
const clipboardClearAfter = 30 * time.Second

// pwCopiedShown: how long the card confirms the copied password.
const pwCopiedShown = 10 * time.Second

var stButton = lipgloss.NewStyle().Reverse(true).Bold(true)

const copyPasswordLabel = " Copy password (p) "

type passwordCopiedMsg struct {
	hostID string
	text   string
	err    error
	sysErr error // the system clipboard failed (nil also when it is off)
}

// copyPassword fetches the password (or key) of h from the vault and puts it
// on the clipboard (button in the details panel, or p).
func (m *Model) copyPassword(h core.Host) tea.Cmd {
	if h.Scheme == core.SchemeLocal {
		return nil
	}
	svc, ctx, system := m.svc, m.ctx, m.opt.CopySystem
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		sec, err := svc.Secret(c, h.ID)
		if err != nil {
			return passwordCopiedMsg{hostID: h.ID, err: err}
		}
		text := string(sec.Value) // the clipboard needs a string
		sec.Wipe()
		msg := passwordCopiedMsg{hostID: h.ID, text: text}
		if system && text != "" {
			msg.sysErr = core.CopyToSystem(text)
		}
		return msg
	}
}

func (m *Model) passwordCopied(msg passwordCopiedMsg) {
	if msg.err != nil {
		m.notify(true, "copy password: %v", msg.err)
		return
	}
	if msg.text == "" {
		m.notify(true, "the item has no password")
		return
	}
	if m.opt.CopyOSC52 {
		if seq := core.ClipboardOSC52(msg.text); seq != "" {
			m.osc, m.oscUntil = seq, time.Now().Add(500*time.Millisecond)
		}
	}
	m.pwCopiedHost, m.pwCopiedAt, m.pwCopiedWarn = msg.hostID, time.Now(), ""
	switch {
	case m.opt.CopySystem && msg.sysErr == nil:
		text := msg.text
		go func() {
			time.Sleep(clipboardClearAfter)
			if cur, err := core.PasteFromSystem(); err == nil && cur == text {
				_ = core.CopyToSystem("")
			}
		}()
		m.notify(false, "password copied to the clipboard (cleared in %s)", clipboardClearAfter)
	case m.opt.CopyOSC52:
		// Only the terminal was asked (OSC 52); whether it took the text is
		// unknown, many terminals (GNOME/MATE Terminal) ignore it.
		m.pwCopiedWarn = "sent to the terminal only (OSC 52)"
		if msg.sysErr != nil {
			m.notify(true, "system clipboard: %v; sent to the terminal clipboard (OSC 52) only", msg.sysErr)
		} else {
			m.notify(false, "password sent to the terminal clipboard (OSC 52)")
		}
	default:
		m.pwCopiedHost = ""
		if msg.sysErr != nil {
			m.notify(true, "copy password: %v", msg.sysErr)
		} else {
			m.notify(true, "copy password: clipboards are off (terminal.copy_system, terminal.copy_osc52)")
		}
	}
}

// passwordButton renders the button and, after a copy, the confirmation
// below it (for 10 s).
func (m *Model) passwordButton(hostID string) []string {
	out := []string{" " + stButton.Render(copyPasswordLabel)}
	if m.pwCopiedHost == hostID && time.Since(m.pwCopiedAt) < pwCopiedShown {
		if m.pwCopiedWarn != "" {
			out = append(out, " "+stWarn.Render("! "+m.pwCopiedWarn))
		} else {
			out = append(out, " "+stOK.Render(m.g.check+" Copied to clipboard"))
		}
	}
	return out
}
