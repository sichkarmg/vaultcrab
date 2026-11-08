// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// vaultsPage is Settings → Vaults: the Bitwarden/Vaultwarden vaults (the
// default one of bw: and the vaults: of the config) with their state; a
// vault can be added (bw config server + bw login), logged in, unlocked,
// synced and removed.
type vaultsPage struct {
	row    int
	adding bool
	in     [3]textinput.Model // ID, name, server of a new vault
	field  int
	remove string // the ID to remove on a second D
	err    string
	status string

	rowY  []int    // dialog rows of the vault lines
	btnY  int      // dialog row of the buttons
	btns  [][2]int // their spans
	names []string // their actions
}

var vaultFieldLabels = []string{"ID", "Name", "Server URL"}

func (p *vaultsPage) startAdd(m *Model) {
	p.adding, p.field, p.err, p.status = true, 0, "", ""
	ph := []string{"work (a-z, 0-9, -)", "Work", "https://vault.example.com (empty: bitwarden.com)"}
	for i := range p.in {
		p.in[i] = textinput.New()
		p.in[i].Prompt = ""
		p.in[i].Placeholder = ph[i]
		p.in[i].CharLimit = 200
	}
	p.in[0].Focus()
}

func (p *vaultsPage) focus(i int) {
	p.field = (i + len(p.in)) % len(p.in)
	for j := range p.in {
		if j == p.field {
			p.in[j].Focus()
		} else {
			p.in[j].Blur()
		}
	}
}

// update handles keys of the page; handled is false for keys the settings
// dialog handles (Tab between pages, Esc).
func (p *vaultsPage) update(m *Model, k tea.KeyMsg) (tea.Cmd, bool) {
	if p.adding {
		switch k.String() {
		case "esc":
			p.adding = false
		case "tab", "down":
			p.focus(p.field + 1)
		case "shift+tab", "up":
			p.focus(p.field - 1)
		case "enter":
			if p.field < len(p.in)-1 {
				p.focus(p.field + 1)
				return nil, true
			}
			return p.save(m), true
		default:
			var cmd tea.Cmd
			p.in[p.field], cmd = p.in[p.field].Update(k)
			return cmd, true
		}
		return nil, true
	}
	vs := m.svc.Vaults()
	p.row = max(0, min(p.row, len(vs)-1))
	if k.String() != "D" {
		p.remove = ""
	}
	switch k.String() {
	case "up", "k":
		p.row = (p.row + len(vs) - 1) % len(vs)
	case "down", "j":
		p.row = (p.row + 1) % len(vs)
	case "a":
		p.startAdd(m)
	default:
		if a := vaultKeys[k.String()]; a != "" {
			return p.act(m, a, vs[p.row]), true
		}
		return nil, false
	}
	return nil, true
}

var vaultKeys = map[string]string{"L": "login", "u": "unlock", "s": "sync", "D": "remove"}

// act runs an action on vault v.
func (p *vaultsPage) act(m *Model, action string, v core.VaultEntry) tea.Cmd {
	p.err, p.status = "", ""
	switch action {
	case "add":
		p.startAdd(m)
	case "login":
		if v.State != core.NeedLogin {
			p.status = v.Label() + " is logged in (" + v.Where() + ")"
			return nil
		}
		return m.loginVault(v.ID)
	case "unlock":
		if v.State != core.Locked {
			p.status = v.Label() + " is " + v.State.String()
			return nil
		}
		u := newUnlock(m)
		u.vault = v
		m.modal = u
	case "sync":
		m.svc.SyncVault(v.ID) // never restarts a running bw serve (it would lock)
		p.status = "checking and syncing " + v.Label() + "…"
	case "remove":
		switch {
		case v.ID == "":
			p.err = "the default vault (bw: in the config) cannot be removed"
		case m.opt.ConfigPath == "":
			p.err = "no config file"
		case p.remove != v.ID:
			p.remove = v.ID
			p.status = "press D again to remove " + v.Label() + " (its bw data stays on disk)"
		default:
			p.remove = ""
			if err := core.RemoveVaultConfig(m.opt.ConfigPath, v.ID); err != nil {
				p.err = err.Error()
				return nil
			}
			m.reloadConfig()
			p.status = v.Label() + " removed"
		}
	}
	return nil
}

// save writes the new vault to the config, connects it and runs bw login
// for it.
func (p *vaultsPage) save(m *Model) tea.Cmd {
	sp := core.VaultSpec{ID: strings.TrimSpace(p.in[0].Value()), Name: strings.TrimSpace(p.in[1].Value()),
		Server: strings.TrimSpace(p.in[2].Value())}
	switch {
	case m.opt.ConfigPath == "":
		p.err = "no config file: start vaultcrab with a config to add vaults"
		return nil
	case sp.ID == "":
		p.err = "the ID is required"
		return nil
	}
	if sp.Server != "" && !strings.Contains(sp.Server, "://") {
		sp.Server = "https://" + sp.Server
	}
	if err := core.AddVaultConfig(m.opt.ConfigPath, sp); err != nil {
		p.err = err.Error()
		return nil
	}
	p.adding = false
	m.reloadConfig() // connects it (no login yet: L)
	p.status = sp.Label() + " added; log in to it"
	for i, v := range m.svc.Vaults() {
		if v.ID == sp.ID {
			p.row = i
		}
	}
	return m.loginVault(sp.ID)
}

func (p *vaultsPage) view(m *Model, w, line int) string {
	var b strings.Builder
	p.rowY = p.rowY[:0]
	vs := m.svc.Vaults()
	p.row = max(0, min(p.row, len(vs)-1))
	for i, v := range vs {
		mark := "  "
		if i == p.row && !p.adding {
			mark = stKey.Render(m.ascii("▸ "))
		}
		name := v.Label()
		if v.ID == "" && len(vs) > 1 {
			name += stDim.Render(" (bw:)")
		} else if v.ID != "" {
			name += stDim.Render(" (" + v.ID + ")")
		}
		st := v.State.String()
		switch {
		case v.State == core.Online && v.SyncErr != nil:
			st = stWarn.Render("online, local copy")
		case v.State == core.Online:
			st = stOK.Render(st)
		case v.State == core.Locked || v.State == core.NeedLogin || v.State == core.Offline || v.State == core.Crashed:
			st = stWarn.Render(st)
		}
		b.WriteString(mark + padRight(name, 28) + " " + padRight(st, 20) + " " +
			stDim.Render(ansi.Truncate(v.Where(), max(10, w-56), m.g.ell)) + "\n")
		p.rowY = append(p.rowY, line)
		line++
	}
	b.WriteString("\n")
	line++
	if p.adding {
		b.WriteString(stBold.Render("New vault") + stDim.Render(" (its own bw data directory: <config dir>/bw-<ID>)") + "\n")
		for i, l := range vaultFieldLabels {
			mark := "  "
			if i == p.field {
				mark = stKey.Render(m.ascii("▸ "))
			}
			b.WriteString(mark + padRight(l, 12) + p.in[i].View() + "\n")
		}
		b.WriteString("\n" + stDim.Render("Enter: next field / save and log in · Tab: next field · Esc: cancel") + "\n")
		p.btns = nil
	} else {
		labels := []string{"Add vault", "Log in", "Unlock", "Sync", "Remove"}
		p.names = []string{"add", "login", "unlock", "sync", "remove"}
		var btn string
		btn, p.btns = buttonRow(labels, -1)
		p.btnY = line
		b.WriteString(btn + "\n\n")
		b.WriteString(stDim.Render("↑/↓ vault · a add · L log in · u unlock · s sync · D remove · Tab: next page · Esc: close") + "\n")
	}
	switch {
	case p.err != "":
		b.WriteString("\n" + stErr.Render(p.err))
	case p.status != "":
		b.WriteString("\n" + stDim.Render(p.status))
	default:
		b.WriteString("\n" + stDim.Render(fmt.Sprintf("%d vault(s). Items of further vaults are in a folder named after the vault.", len(vs))))
	}
	return b.String()
}

func (p *vaultsPage) mouse(m *Model, x, y int) tea.Cmd {
	for i, ry := range p.rowY {
		if y == ry {
			p.row = i
			return nil
		}
	}
	if y == p.btnY && p.btns != nil {
		if i := spanAt(p.btns, x); i >= 0 {
			vs := m.svc.Vaults()
			return p.act(m, p.names[i], vs[max(0, min(p.row, len(vs)-1))])
		}
	}
	return nil
}

// vaultLoginDoneMsg ends `bw login` of a vault (Settings → Vaults).
type vaultLoginDoneMsg struct {
	id  string
	err error
}

// loginVault hands the terminal to `bw login` of vault id and reconnects
// it afterwards.
func (m *Model) loginVault(id string) tea.Cmd {
	cmd := m.svc.LoginVault(id)
	if cmd == nil {
		m.notify(true, "bw login is not available for this vault")
		return nil
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return vaultLoginDoneMsg{id: id, err: err} })
}
