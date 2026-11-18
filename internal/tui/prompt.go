// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// promptModal shows a question of a connection (core.Prompt): the
// passphrase of a key file, or whether to accept a new host key.
type promptModal struct {
	p     *core.Prompt
	in    textinput.Model
	focus int
	spans [][2]int
	row   int
	// PromptLogin: the login (in) and the password (pw); focus 0 = login,
	// 1 = password. inRow is the row of the login field in the box.
	pw    textinput.Model
	inRow int
}

var saveLoginButtons = []string{"Save to the vault", "Don't save"}

// loginSavedMsg reports the end of saving a typed login to the vault.
type loginSavedMsg struct {
	target string
	err    error
}

var hostKeyButtons = []string{"Accept and remember", "Accept once", "Reject"}

func newPromptModal(m *Model, p *core.Prompt) *promptModal {
	pm := &promptModal{p: p, focus: 2} // Reject is focused: Enter does not accept by accident
	if p.Kind == core.PromptPassphrase {
		pm.in = textinput.New()
		pm.in.Prompt = "> "
		pm.in.Placeholder = "passphrase"
		pm.in.EchoMode = textinput.EchoPassword
		pm.in.EchoCharacter = '•'
		if m.opt.Icons.ASCII() {
			pm.in.EchoCharacter = '*'
		}
		pm.in.Focus()
	}
	switch p.Kind {
	case core.PromptLogin:
		pm.in = textinput.New()
		pm.in.Prompt = "Login:    "
		pm.in.Placeholder = "user"
		pm.in.CharLimit = 256
		pm.in.SetValue(p.User)
		pm.in.CursorEnd()
		pm.pw = textinput.New()
		pm.pw.Prompt = "Password: "
		pm.pw.Placeholder = "password"
		pm.pw.EchoMode = textinput.EchoPassword
		pm.pw.EchoCharacter = '•'
		if m.opt.Icons.ASCII() {
			pm.pw.EchoCharacter = '*'
		}
		pm.focusLogin(1) // the password: the login is usually right
	case core.PromptSaveLogin:
		pm.focus = 0
		if p.Warning != "" {
			pm.focus = 1 // a key would be replaced: Enter does not do it by accident
		}
	}
	return pm
}

func (pm *promptModal) focusLogin(i int) {
	pm.focus = i
	if i == 0 {
		pm.pw.Blur()
		pm.in.Focus()
	} else {
		pm.in.Blur()
		pm.pw.Focus()
	}
}

// loginKey handles a key in a PromptLogin question.
func (pm *promptModal) loginKey(m *Model, k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		pm.pw.SetValue("")
		m.modal = nil
		pm.p.Cancel()
		return nil
	case "tab", "shift+tab", "up", "down":
		pm.focusLogin(1 - pm.focus)
		return nil
	case "enter":
		user := strings.TrimSpace(pm.in.Value())
		switch {
		case user == "":
			pm.focusLogin(0)
			return nil
		case pm.pw.Value() == "":
			pm.focusLogin(1)
			return nil
		}
		b := []byte(pm.pw.Value())
		pm.pw.SetValue("")
		m.modal = nil
		pm.p.AnswerLogin(user, b) // the connection wipes it
		return nil
	}
	var cmd tea.Cmd
	if pm.focus == 0 {
		pm.in, cmd = pm.in.Update(k)
	} else {
		pm.pw, cmd = pm.pw.Update(k)
	}
	return cmd
}

// saveLogin answers a PromptSaveLogin question.
func (pm *promptModal) saveLogin(m *Model, save bool) tea.Cmd {
	m.modal = nil
	if !save {
		pm.p.Cancel() // wipes the password
		return nil
	}
	p, ctx, target := pm.p, m.ctx, pm.p.User+"@"+pm.p.Target
	if i := strings.LastIndex(pm.p.Target, "@"); i >= 0 {
		target = pm.p.User + pm.p.Target[i:]
	}
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		return loginSavedMsg{target: target, err: p.Save(c)}
	}
}

// checkPrompt shows the oldest question of a connection when no other
// dialog is open, and closes a question the connection withdrew.
func (m *Model) checkPrompt() {
	cur := m.svc.Prompt()
	if pm, ok := m.modal.(*promptModal); ok && pm.p != cur {
		pm.in.SetValue("")
		pm.pw.SetValue("")
		m.modal = nil
	}
	if m.modal == nil && cur != nil {
		m.modal = newPromptModal(m, cur)
	}
}

func (pm *promptModal) answer(m *Model, a core.HostKeyAnswer) {
	m.modal = nil
	pm.p.AnswerHostKey(a)
}

func (pm *promptModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch pm.p.Kind {
	case core.PromptLogin:
		return pm.loginKey(m, k)
	case core.PromptSaveLogin:
		switch strings.ToLower(k.String()) {
		case "left", "right", "tab", "shift+tab", "h", "l":
			pm.focus = 1 - pm.focus
		case "enter", " ":
			return pm.saveLogin(m, pm.focus == 0)
		case "y", "s":
			return pm.saveLogin(m, true)
		case "n", "esc":
			return pm.saveLogin(m, false)
		}
		return nil
	}
	if pm.p.Kind == core.PromptPassphrase {
		switch k.String() {
		case "esc":
			pm.in.SetValue("")
			m.modal = nil
			pm.p.Cancel()
			return nil
		case "enter":
			b := []byte(pm.in.Value())
			pm.in.SetValue("")
			m.modal = nil
			pm.p.AnswerSecret(b) // the connection wipes it
			return nil
		}
		var cmd tea.Cmd
		pm.in, cmd = pm.in.Update(k)
		return cmd
	}
	switch strings.ToLower(k.String()) {
	case "left", "shift+tab", "h":
		pm.focus = (pm.focus + len(hostKeyButtons) - 1) % len(hostKeyButtons)
	case "right", "tab", "l":
		pm.focus = (pm.focus + 1) % len(hostKeyButtons)
	case "enter", " ":
		pm.answer(m, []core.HostKeyAnswer{core.HostKeyForever, core.HostKeyOnce, core.HostKeyReject}[pm.focus])
	case "y", "a":
		pm.answer(m, core.HostKeyForever)
	case "o":
		pm.answer(m, core.HostKeyOnce)
	case "n", "r", "esc":
		pm.answer(m, core.HostKeyReject)
	}
	return nil
}

func (pm *promptModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	switch pm.p.Kind {
	case core.PromptLogin:
		if y == pm.inRow || y == pm.inRow+1 {
			pm.focusLogin(y - pm.inRow)
		}
		return nil
	case core.PromptSaveLogin:
		if y == pm.row {
			if i := spanAt(pm.spans, x-boxPadX); i >= 0 {
				return pm.saveLogin(m, i == 0)
			}
		}
		return nil
	}
	if pm.p.Kind != core.PromptHostKey || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft || y != pm.row {
		return nil
	}
	if i := spanAt(pm.spans, x-boxPadX); i >= 0 {
		pm.answer(m, []core.HostKeyAnswer{core.HostKeyForever, core.HostKeyOnce, core.HostKeyReject}[i])
	}
	return nil
}

func (pm *promptModal) view(m *Model, w, h int) []string {
	var b strings.Builder
	b.WriteString(pm.p.Text + "\n")
	if pm.p.Warning != "" {
		b.WriteString("\n" + stWarn.Render(pm.p.Warning) + "\n")
	}
	switch pm.p.Kind {
	case core.PromptLogin:
		b.WriteString("\n")
		pm.inRow = boxPadY + boxTitleGap + strings.Count(b.String(), "\n")
		b.WriteString(pm.in.View() + "\n" + pm.pw.View() + "\n\n")
		b.WriteString(stDim.Render("Enter: connect · Tab: login/password · Esc: cancel the connection"))
		return m.box(pm.p.Title, b.String(), w)
	case core.PromptSaveLogin:
		b.WriteString("\n")
		btns, spans := buttonRow(saveLoginButtons, pm.focus)
		pm.spans = spans
		pm.row = boxPadY + boxTitleGap + strings.Count(b.String(), "\n")
		b.WriteString(btns + "\n\n")
		b.WriteString(stDim.Render("y: save · n/Esc: don't save · ←/→ Enter"))
		return m.box(pm.p.Title, b.String(), w)
	}
	if pm.p.Kind == core.PromptPassphrase {
		b.WriteString("\n" + pm.in.View() + "\n\n")
		b.WriteString(stDim.Render("Enter: unlock the key · Esc: cancel the connection"))
		return m.box(pm.p.Title, b.String(), w)
	}
	b.WriteString("\n" + stDim.Render("Compare the fingerprint with the one of the server (ssh-keygen -lf\n/etc/ssh/ssh_host_ed25519_key.pub) before accepting it.") + "\n\n")
	btns, spans := buttonRow(hostKeyButtons, pm.focus)
	pm.spans = spans
	pm.row = boxPadY + boxTitleGap + strings.Count(b.String(), "\n")
	b.WriteString(btns + "\n\n")
	b.WriteString(stDim.Render("y: accept and remember · o: once · n/Esc: reject · ←/→ Enter"))
	return m.box(pm.p.Title, b.String(), w)
}

// renameModal names the current tab (ui.keys.rename_tab).
type renameModal struct {
	t  *tab
	in textinput.Model
}

func newRename(m *Model, t *tab) *renameModal {
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = sessionTitle(t.focused(), m.g)
	in.SetValue(t.name)
	in.CharLimit = 80
	in.Focus()
	return &renameModal{t: t, in: in}
}

func (r *renameModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "esc":
		m.modal = nil
		return nil
	case "enter":
		r.t.name = strings.TrimSpace(r.in.Value())
		m.modal = nil
		return nil
	}
	var cmd tea.Cmd
	r.in, cmd = r.in.Update(k)
	return cmd
}

func (r *renameModal) view(m *Model, w, h int) []string {
	body := r.in.View() + "\n\n" + stDim.Render("Enter: rename · empty: the host name again · Esc: cancel")
	return m.box("Rename the tab", body, w)
}
