// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// modal is a dialog drawn over the body; it receives all keys.
type modal interface {
	update(m *Model, msg tea.Msg) tea.Cmd
	view(m *Model, w, h int) []string
}

func (m *Model) box(title, body string, w int) []string {
	body = m.ascii(body)
	s := lipgloss.NewStyle().Border(m.g.border).Padding(0, 1).Width(min(w-4, max(40, lipgloss.Width(body)+4)))
	if title != "" {
		body = stBold.Render(title) + "\n\n" + body
	}
	return strings.Split(s.Render(body), "\n")
}

// overlay centers dialog lines over body lines.
func overlay(body, dlg []string, w, h int) []string {
	dw := 0
	for _, l := range dlg {
		dw = max(dw, ansi.StringWidth(l))
	}
	x0 := max(0, (w-dw)/2)
	y0 := max(0, (h-len(dlg))/2)
	out := append([]string(nil), body...)
	for i, l := range dlg {
		y := y0 + i
		if y >= len(out) {
			break
		}
		left := ansi.Truncate(out[y], x0, "")
		left = padRight(left, x0)
		right := ansi.TruncateLeft(out[y], x0+ansi.StringWidth(l), "")
		out[y] = left + "\x1b[0m" + l + "\x1b[0m" + right
	}
	return out
}

// --- unlock ---

type unlockModal struct {
	in    textinput.Model
	vault core.VaultEntry // the locked vault this dialog unlocks
	many  bool            // several vaults: name it
	busy  bool
	err   string
	frame int // spinner frame while unlocking and syncing

	pin     bool   // asking for the PIN (Tab: the master password)
	pinOK   bool   // the PIN can unlock this vault
	pinNote string // why the master password is needed although the PIN is on
}

// spinTickMsg advances the spinner of the unlock dialog.
type spinTickMsg struct{}

func spinTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinTickMsg{} })
}

var (
	spinUnicode = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinASCII   = []string{"|", "/", "-", "\\"}
)

type unlockResultMsg struct{ err error }

func newUnlock(m *Model) *unlockModal {
	in := textinput.New()
	in.Placeholder = "master password"
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	if m.opt.Icons.ASCII() {
		in.EchoCharacter = '*'
	}
	in.Prompt = "> "
	in.Focus()
	v, _ := m.svc.LockedVault()
	u := &unlockModal{in: in, vault: v, many: len(m.svc.Vaults()) > 1}
	q := m.svc.QuickUnlock(v.ID)
	u.pinOK, u.pinNote = q.PIN, q.Why
	u.setPIN(q.PIN)
	return u
}

// setPIN switches the input between the PIN and the master password.
func (u *unlockModal) setPIN(on bool) {
	u.pin = on && u.pinOK
	u.in.SetValue("")
	u.in.Placeholder = "master password"
	if u.pin {
		u.in.Placeholder = "PIN"
	}
}

type pinResultMsg struct{ err error }

func (u *unlockModal) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case spinTickMsg:
		if !u.busy {
			return nil
		}
		u.frame++
		return spinTick()
	case pinResultMsg:
		u.busy = false
		switch {
		case msg.err == nil:
			return u.update(m, unlockResultMsg{})
		case errors.Is(msg.err, core.ErrWrongPIN):
			u.err = msg.err.Error()
		default:
			// too many wrong PINs, the master password is required, bw
			// refused the stored one: on to the master password
			u.err = msg.err.Error()
			u.pinOK = false
			u.setPIN(false)
		}
		return nil
	case unlockResultMsg:
		u.busy = false
		if msg.err != nil {
			u.err = msg.err.Error()
			return nil
		}
		m.modal = nil
		m.notify(false, "vault %s unlocked", u.vault.Label())
		if _, ok := m.svc.LockedVault(); ok {
			m.modal = newUnlock(m) // the next locked vault
		}
		return nil
	case tea.KeyMsg:
		if u.busy {
			return nil
		}
		switch msg.String() {
		case "esc":
			u.in.SetValue("")
			m.modal = nil
			return nil
		case "tab":
			if u.pinOK {
				u.setPIN(!u.pin)
				u.err = ""
			}
			return nil
		case "enter":
			pw := []byte(u.in.Value())
			u.in.SetValue("")
			if len(pw) == 0 {
				return nil
			}
			u.busy, u.err, u.frame = true, "", 0
			svc, ctx, id := m.svc, m.ctx, u.vault.ID
			if u.pin {
				return tea.Batch(spinTick(), func() tea.Msg {
					c, cancel := context.WithTimeout(ctx, 2*time.Minute)
					defer cancel()
					return pinResultMsg{err: svc.UnlockWithPIN(c, id, pw)} // wipes pw
				})
			}
			return tea.Batch(spinTick(), func() tea.Msg {
				c, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				return unlockResultMsg{err: svc.UnlockVault(c, id, pw)} // wipes pw and syncs
			})
		}
	}
	var cmd tea.Cmd
	u.in, cmd = u.in.Update(msg)
	return cmd
}

func (u *unlockModal) view(m *Model, w, h int) []string {
	if u.busy {
		// The password was sent: no input any more, just progress (unlocking
		// takes a few seconds, then the vault is synced).
		frames := spinUnicode
		if m.opt.Icons.ASCII() {
			frames = spinASCII
		}
		phase := "Unlocking " + u.what() + "…"
		if u.pin {
			phase = "Checking the PIN and unlocking " + u.what() + "…"
		}
		if m.svc.Status().State == core.Syncing {
			phase = "Syncing " + u.what() + "…"
		}
		body := "\n" + stKey.Render(frames[u.frame%len(frames)]) + " " + phase + "\n"
		return m.box("Unlock", body, w)
	}
	body := fmt.Sprintf("The vault %s is locked.\n%s\n", stBold.Render(u.vault.Label()), stDim.Render(u.vault.Where()))
	if u.vault.Err != nil {
		// why: bw serve was restarted, or bw locked it by itself
		body += stWarn.Render(errText(u.vault.Err)) + "\n"
	}
	if u.pinNote != "" && !u.pin {
		body += stDim.Render("PIN: "+u.pinNote) + "\n"
	}
	body += "\n" + u.in.View()
	hint := "Enter: unlock · Esc: browse offline"
	if u.pinOK {
		other := "PIN"
		if u.pin {
			other = "master password"
		}
		hint = "Enter: unlock · Tab: " + other + " · Esc: browse offline"
	}
	switch {
	case u.err != "":
		body += "\n\n" + stErr.Render(u.err) + "\n" + stDim.Render(hint)
	default:
		body += "\n\n" + stDim.Render(hint)
	}
	return m.box("Unlock", body, w)
}

// what names the vault in progress messages.
func (u *unlockModal) what() string {
	if u.many {
		return u.vault.Label()
	}
	return "the vault"
}

// --- confirm ---

// confirmModal asks yes/no with two buttons (keys or mouse); the yes button
// is focused, so Enter confirms as before.
type confirmModal struct {
	text  string
	yes   func(m *Model) tea.Cmd
	focus int // 0 = yes, 1 = no
	spans [][2]int
	row   int
}

func newConfirm(text string, yes func(m *Model) tea.Cmd) *confirmModal {
	return &confirmModal{text: text, yes: yes}
}

// labels names the yes button after the question ("Close …?" gives Close).
func (c *confirmModal) labels() []string {
	yes := "Yes"
	for _, w := range []string{"Close", "Quit", "Delete"} {
		if strings.HasPrefix(c.text, w) {
			yes = w
		}
	}
	return []string{yes, "Cancel"}
}

func (c *confirmModal) choose(m *Model, i int) tea.Cmd {
	m.modal = nil
	if i == 0 {
		return c.yes(m)
	}
	return nil
}

func (c *confirmModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch strings.ToLower(k.String()) {
	case "left", "right", "tab", "shift+tab", "h", "l":
		c.focus = 1 - c.focus
	case "enter", " ":
		return c.choose(m, c.focus)
	case "y", "д":
		return c.choose(m, 0)
	case "n", "esc", "q", "н":
		return c.choose(m, 1)
	}
	return nil
}

func (c *confirmModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || y != c.row {
		return nil
	}
	if i := spanAt(c.spans, x-boxPadX); i >= 0 {
		return c.choose(m, i)
	}
	return nil
}

func (c *confirmModal) view(m *Model, w, h int) []string {
	var btn string
	btn, c.spans = buttonRow(c.labels(), c.focus)
	body := c.text + "\n\n" + btn + "\n\n" +
		stDim.Render("y/Enter: "+strings.ToLower(c.labels()[0])+" · n/Esc: cancel · ←/→: choose · or click")
	c.row = boxPadY + strings.Count(c.text, "\n") + 2 // no title
	return m.box("", body, w)
}

// --- message / login ---

type messageModal struct {
	title, text string
	onKey       func(m *Model, k tea.KeyMsg) tea.Cmd
}

func (c *messageModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if c.onKey != nil {
		return c.onKey(m, k)
	}
	m.modal = nil
	return nil
}

func (c *messageModal) view(m *Model, w, h int) []string { return m.box(c.title, c.text, w) }

type loginDoneMsg struct{ err error }

// login hands the terminal to `bw login` and reconnects afterwards.
func (m *Model) login() tea.Cmd {
	cmd := m.svc.LoginCommand()
	if cmd == nil {
		m.notify(true, "bw login is not available")
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return loginDoneMsg{err: err} })
}

func (m *Model) afterLogin(l loginDoneMsg) tea.Cmd {
	if l.err != nil {
		m.notify(true, "bw login: %v", l.err)
	} else {
		m.notify(false, "logged in, restarting bw serve…")
	}
	m.unlockOffered = false
	m.svc.Reconnect(m.ctx)
	return nil
}

// --- help ---

type helpModal struct{}

func newHelp(*Model) *helpModal { return &helpModal{} }

func (h *helpModal) update(m *Model, msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.KeyMsg); ok {
		m.modal = nil
	}
	return nil
}

func (h *helpModal) view(m *Model, w, hh int) []string {
	k := func(s string) string { return stKey.Render(s) }
	p := m.opt.Prefix
	var b strings.Builder
	b.WriteString(stBold.Render("Host list") + "\n")
	for _, l := range [][2]string{
		{"Up/Down PgUp/PgDn Home/End", "move"},
		{"Enter / double click", "connect (SSH: new tab; VNC/SMB/FTP/SFTP/RDP: external program) / fold group"},
		{"Right/Left Space", "expand / collapse"},
		{"| / -", "open in a new pane (right / below) of the last session tab"},
		{"t", "local terminal in a new tab (anywhere: " + m.opt.Prefix + " t)"},
		{"x", "run a script for the item (scripts: in the config; ping by default)"},
		{"e / a / C / D", "edit the item / add a new one / copy / delete it (to the trash; vault online only)"},
		{"w", "show / hide the widget column (widgets: in the config)"},
		{"p", "copy the password of the item to the clipboard (also the button in the details)"},
		{"Tab / Shift+Tab", "next / previous filter"},
		{"/", "search (filter syntax, e.g. tag:linux prod); / again edits it"},
		{"Esc", "in the search: leave the field, the search stays (list keys work); Esc again clears it"},
		{"F / [Reset filters]", "reset the type filter, organization, collection and search"},
		{"o / c", "organization / collection"},
		{"g", "grouping: folders / collections / flat"},
		{"s", "sync vault"},
		{"u / L", "unlock vault / bw login"},
		{", / " + m.opt.Icons.Get(core.IconSettings) + " (top right)", "settings: appearance, external programs (saved to the config), about (paths)"},
		{"r", "re-read the config file (also done when it changes)"},
		{"q, Ctrl+C", "quit"},
	} {
		b.WriteString("  " + k(l[0]) + "  " + l[1] + "\n")
	}
	b.WriteString("\n" + stBold.Render("Sessions") + " (after " + k(p) + ")\n")
	acts := make([]string, 0, len(m.opt.Keys))
	for a := range m.opt.Keys {
		acts = append(acts, a)
	}
	sort.Strings(acts)
	line := ""
	for _, a := range acts {
		name := strings.ReplaceAll(a, "_", " ")
		if a == core.ActHistory {
			name = "scroll output" // the config key stays "history"
		}
		item := k(m.opt.Keys[a]) + " " + name
		if ansi.StringWidth(line)+ansi.StringWidth(item)+3 > min(w-8, 100) {
			b.WriteString("  " + line + "\n")
			line = ""
		}
		if line != "" {
			line += " · "
		}
		line += item
	}
	if line != "" {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("  " + k("0-9") + " go to tab · " + k(p) + " " + k(p) + " send " + p + "\n")
	b.WriteString("  " + k("Ctrl+PgDn/PgUp") + " or " + k(m.ascii("Alt+→/←")) + " next / previous tab, without the prefix (everywhere)\n")
	b.WriteString("  " + "settings and the item form stay in 0:Hosts (" + m.ascii("✎") + "): switch tabs while they are open\n")
	b.WriteString("  " + k("Enter") + " in a broken session (" + m.opt.Icons.Get(core.IconDisconnected) + ") reconnects, " + k("Ctrl+D") + " closes it · mouse wheel scrolls the output\n")
	b.WriteString("  " + k(m.ascii("✕")) + " in a pane header (split tab) closes that pane · " + k(p+" "+m.opt.Keys[core.ActCloseTab]) + " closes the whole tab\n")
	b.WriteString("\n" + stDim.Render("Vaultcrab "+m.opt.Version+" · any key closes"))
	return m.box("Help", b.String(), w)
}
