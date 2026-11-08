// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// pinModal sets or changes the PIN (Settings → Security): the current PIN
// when changing, the new PIN twice, then the master password of an unlocked
// vault (checked by bw) that is stored with it.
type pinModal struct {
	back   *settingsModal
	change bool // a PIN is set: ask for it first
	step   int  // 0 current PIN, 1 new PIN, 2 repeat, 3 master password
	in     textinput.Model
	pin    []byte
	vault  core.VaultEntry
	busy   bool
	err    string
}

type pinSetMsg struct{ err error }

func newPINModal(m *Model, back *settingsModal) *pinModal {
	in := textinput.New()
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	if m.opt.Icons.ASCII() {
		in.EchoCharacter = '*'
	}
	in.Prompt = "> "
	in.Focus()
	p := &pinModal{back: back, change: m.svc.HasPIN(), in: in}
	vs := m.svc.Vaults()
	p.vault = vs[0]
	for _, v := range vs {
		if v.State == core.Online {
			p.vault = v
			break
		}
	}
	if !p.change {
		p.step = 1
	}
	return p
}

func (p *pinModal) wipe() {
	for i := range p.pin {
		p.pin[i] = 0
	}
	p.pin = nil
	p.in.SetValue("")
}

func (p *pinModal) close(m *Model) {
	p.wipe()
	m.modal = p.back
}

func (p *pinModal) update(m *Model, msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case pinSetMsg:
		p.busy = false
		if msg.err != nil {
			p.err = msg.err.Error()
			if p.step == 3 && !errors.Is(msg.err, core.ErrWeakPIN) {
				return nil // the master password: try again
			}
			p.wipe()
			p.step = 1
			return nil
		}
		p.wipe()
		a := m.opt.Appearance
		a.PIN = true
		p.back.apply(m, a)
		p.back.status = "PIN set; the master password of " + p.vault.Label() + " is in " + m.svc.KeystoreName()
		m.modal = p.back
		return nil
	case tea.KeyMsg:
		if p.busy {
			return nil
		}
		switch msg.String() {
		case "esc":
			p.close(m)
			return nil
		case "enter":
			v := []byte(p.in.Value())
			p.in.SetValue("")
			if len(v) == 0 {
				return nil
			}
			p.err = ""
			switch p.step {
			case 0:
				if err := m.svc.CheckPIN(v); err != nil { // wipes v; a wrong PIN counts
					p.err = err.Error()
					if errors.Is(err, core.ErrTooManyPINs) {
						p.change, p.step = false, 1
					}
					return nil
				}
				p.step = 1
			case 1:
				p.pin = v
				p.step = 2
			case 2:
				same := string(v) == string(p.pin)
				for i := range v {
					v[i] = 0
				}
				if !same {
					p.wipe()
					p.err = "the PINs differ: enter the new PIN again"
					p.step = 1
					return nil
				}
				p.step = 3
			case 3:
				p.busy = true
				pin := append([]byte(nil), p.pin...)
				svc, ctx, id := m.svc, m.ctx, p.vault.ID
				return func() tea.Msg {
					c, cancel := context.WithTimeout(ctx, 2*time.Minute)
					defer cancel()
					return pinSetMsg{err: svc.SetPIN(c, id, pin, v)} // wipes pin and v
				}
			}
			return nil
		}
	}
	var cmd tea.Cmd
	p.in, cmd = p.in.Update(msg)
	return cmd
}

func (p *pinModal) view(m *Model, w, h int) []string {
	title := "Set PIN"
	if p.change {
		title = "Change PIN"
	}
	var b strings.Builder
	digits := "at least 6 digits"
	if m.opt.Appearance.PINAlphanumeric {
		digits = "at least 6 letters or digits"
	}
	switch p.step {
	case 0:
		b.WriteString("Current PIN:")
	case 1:
		b.WriteString("New PIN (" + digits + "):")
	case 2:
		b.WriteString("The new PIN again:")
	case 3:
		b.WriteString("Master password of " + stBold.Render(p.vault.Label()) + "\n" +
			stDim.Render("checked by bw, then kept in "+m.svc.KeystoreName()+" so that the PIN unlocks the vault"))
	}
	b.WriteString("\n\n" + p.in.View() + "\n\n")
	switch {
	case p.busy:
		b.WriteString(stDim.Render("checking the master password, deriving the key of the PIN…"))
	case p.err != "":
		b.WriteString(stErr.Render(p.err))
	default:
		b.WriteString(stDim.Render("Enter: next · Esc: cancel"))
	}
	return m.box(title, b.String(), w)
}
