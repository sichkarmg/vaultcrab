// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// jumpPage is Settings → Jump hosts: the templates of the quick connections
// through a jump host (jump_templates:), added, edited and removed here
// (saved to the config and applied at once), and the history of the jump
// connections.
type jumpPage struct {
	row     int
	editing bool
	edit    int // the template being edited, -1 = a new one
	in      [8]textinput.Model
	field   int
	remove  int  // the template to remove on a second D (-1 = none)
	clear   bool // c pressed once
	err     string
	status  string

	rowY []int // dialog rows of the template lines
}

var jumpFieldLabels = []string{"Name", "Jump host", "User", "Port", "Key", "Jump key", "Host keys", "Knock"}

var jumpFieldHints = []string{
	"Workstations",
	"a vault item (name or ID) or [user@]host[:port]",
	"on the target (empty: typed as user@ip, else the jump user)",
	"22",
	"~/.ssh/id_ed25519 | agent | vault:<item> (empty: as the jump host)",
	"empty: the vault item's credential, else Key",
	"ask | accept-new | strict",
	"port knocking at the jump host: 7000,8000/udp,9000 (empty: its item's knock field)",
}

func (p *jumpPage) startEdit(m *Model, i int, t core.JumpTemplate) {
	p.editing, p.edit, p.field, p.err, p.status = true, i, 0, "", ""
	vals := []string{t.Name, t.Jump, t.User, "", t.Key, t.JumpKey, t.HostKeyCheck, t.Knock}
	if t.Port != 0 {
		vals[3] = strconv.Itoa(t.Port)
	}
	for j := range p.in {
		p.in[j] = textinput.New()
		p.in[j].Prompt = ""
		p.in[j].Placeholder = jumpFieldHints[j]
		p.in[j].CharLimit = 255
		p.in[j].SetValue(vals[j])
		p.in[j].Blur()
	}
	p.in[0].Focus()
}

func (p *jumpPage) focus(i int) {
	p.field = (i + len(p.in)) % len(p.in)
	for j := range p.in {
		if j == p.field {
			p.in[j].Focus()
		} else {
			p.in[j].Blur()
		}
	}
}

// newJumpTemplateFrom opens Settings → Jump hosts with a new template whose
// jump host is h (J in the host list).
func (m *Model) newJumpTemplateFrom(h core.Host) {
	if h.Scheme != core.SchemeSSH {
		m.notify(true, "%s is not an SSH host: only SSH hosts can be jump hosts", h.Name)
		return
	}
	ref := h.Name
	n := 0
	for _, o := range m.svc.Snapshot().Hosts {
		if o.Scheme == core.SchemeSSH && strings.EqualFold(o.Name, h.Name) {
			n++
		}
	}
	if n != 1 {
		ref = h.ID // the name is not unique
	}
	s := newSettings(m)
	s.page = pageJump
	m.settingsPage = pageJump
	s.jp.startEdit(m, -1, core.JumpTemplate{Name: "via " + h.Name, Jump: ref})
	s.jp.status = "a new template through " + h.Name + ": type a name and the key of the targets"
	m.modal = s
}

func (p *jumpPage) update(m *Model, k tea.KeyMsg) (tea.Cmd, bool) {
	if p.editing {
		switch k.String() {
		case "esc":
			p.editing = false
		case "tab", "down":
			p.focus(p.field + 1)
		case "shift+tab", "up":
			p.focus(p.field - 1)
		case "enter":
			if p.field < len(p.in)-1 {
				p.focus(p.field + 1)
				return nil, true
			}
			p.save(m)
		default:
			var cmd tea.Cmd
			p.in[p.field], cmd = p.in[p.field].Update(k)
			return cmd, true
		}
		return nil, true
	}
	ts := m.svc.Config().JumpTemplates
	if k.String() != "D" {
		p.remove = -1
	}
	if k.String() != "c" {
		p.clear = false
	}
	switch k.String() {
	case "up", "k":
		if len(ts) > 0 {
			p.row = (p.row + len(ts) - 1) % len(ts)
		}
	case "down", "j":
		if len(ts) > 0 {
			p.row = (p.row + 1) % len(ts)
		}
	case "a":
		p.startEdit(m, -1, core.JumpTemplate{})
	case "e", "enter":
		if p.row < len(ts) {
			p.startEdit(m, p.row, ts[p.row])
		}
	case "D":
		if p.row >= len(ts) {
			return nil, true
		}
		if p.remove != p.row {
			p.remove = p.row
			p.err, p.status = "", "press D again to remove the template "+ts[p.row].Name
			return nil, true
		}
		p.remove = -1
		rest := slices.Delete(slices.Clone(ts), p.row, p.row+1)
		if err := m.svc.SaveJumpTemplates(rest); err != nil {
			p.err = err.Error()
			return nil, true
		}
		p.status, p.err = "template removed", ""
		p.row = max(0, min(p.row, len(rest)-1))
	case "H":
		on := !m.svc.JumpHistoryEnabled()
		if err := m.svc.SetJumpHistory(on); err != nil {
			p.err = err.Error()
			return nil, true
		}
		p.err, p.status = "", map[bool]string{true: "history on", false: "history off (the file is kept: c deletes it)"}[on]
	case "c":
		if !p.clear {
			p.clear = true
			p.err, p.status = "", "press c again to delete the history of the jump connections"
			return nil, true
		}
		p.clear = false
		if err := m.svc.ClearJumpHistory(); err != nil {
			p.err = err.Error()
			return nil, true
		}
		p.err, p.status = "", "history deleted"
	default:
		return nil, false
	}
	return nil, true
}

// save checks the form and writes the templates.
func (p *jumpPage) save(m *Model) {
	v := func(i int) string { return strings.TrimSpace(p.in[i].Value()) }
	t := core.JumpTemplate{Name: v(0), Jump: v(1), User: v(2), Key: v(4), JumpKey: v(5), HostKeyCheck: v(6), Knock: v(7)}
	if p.edit >= 0 && p.edit < len(m.svc.Config().JumpTemplates) {
		t.KnockDelay = m.svc.Config().JumpTemplates[p.edit].KnockDelay // only in the config
	}
	if s := v(3); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 65535 {
			p.err = "Port: want 1..65535"
			p.focus(3)
			return
		}
		t.Port = n
	}
	if t.HostKeyCheck == "ask" {
		t.HostKeyCheck = "" // the default
	}
	if err := t.Check(); err != nil {
		p.err = err.Error()
		return
	}
	ts := slices.Clone(m.svc.Config().JumpTemplates)
	if p.edit >= 0 && p.edit < len(ts) {
		ts[p.edit] = t
	} else {
		ts = append(ts, t)
		p.edit = len(ts) - 1
	}
	if err := m.svc.SaveJumpTemplates(ts); err != nil {
		p.err = err.Error()
		return
	}
	p.editing, p.err = false, ""
	p.row = p.edit
	p.status = "template " + t.Name + " saved; j connects through it"
}

func (p *jumpPage) view(m *Model, w, line int) string {
	var b strings.Builder
	p.rowY = p.rowY[:0]
	ts := m.svc.Config().JumpTemplates
	write := func(s string) {
		b.WriteString(s + "\n")
		line += strings.Count(s, "\n") + 1
	}
	if p.editing {
		title := "New template"
		if p.edit >= 0 {
			title = "Edit template"
		}
		write(stBold.Render(title))
		for i := range p.in {
			mark := "  "
			if i == p.field {
				mark = stKey.Render("▸ ")
			}
			write(mark + padRight(jumpFieldLabels[i], 11) + p.in[i].View())
		}
		if strings.TrimSpace(p.in[6].Value()) == "accept-new" {
			write(stWarn.Render("⚠ accept-new remembers a new host key without asking (a changed key is still refused)."))
		}
		write("")
		write(stDim.Render("Enter: next field / save · Tab: next field · Esc: cancel"))
	} else {
		if len(ts) == 0 {
			write(stDim.Render("No templates yet: a adds one, J on a host of the list makes one of it."))
		}
		for i, t := range ts {
			mark := "  "
			if i == p.row {
				mark = stKey.Render("▸ ")
			}
			key := t.Key
			if key == "" {
				key = "as the jump host"
			}
			hk := t.HostKeys()
			if hk == "accept-new" {
				hk = stWarn.Render(hk)
			}
			l := mark + padRight(trunc(t.Name, 20), 20) + " via " + padRight(trunc(t.Jump, 22), 22) + " " + stDim.Render(trunc("key "+key, 30)) + " " + hk
			p.rowY = append(p.rowY, line)
			write(ansi.Truncate(l, w, m.g.ell))
		}
		hist := "on"
		if !m.svc.JumpHistoryEnabled() {
			hist = "off"
		}
		write("")
		write(stDim.Render(fmt.Sprintf("History of the jump connections: %s (names and addresses only, in the cache directory).", hist)))
		write("")
		write(stDim.Render("↑/↓ template · a add · e/Enter edit · D remove · H history on/off · c clear history · Tab: next page"))
	}
	if p.err != "" {
		write("\n" + stErr.Render(p.err))
	} else if p.status != "" {
		write("\n" + stOK.Render(m.g.check) + " " + stDim.Render(p.status))
	}
	return b.String()
}

// mouse: a click selects a template, a click on the selected one edits it.
func (p *jumpPage) mouse(m *Model, x, y int) tea.Cmd {
	if p.editing {
		return nil
	}
	ts := m.svc.Config().JumpTemplates
	for i, ry := range p.rowY {
		if y == ry && i < len(ts) {
			if i == p.row {
				p.startEdit(m, i, ts[i])
			}
			p.row = i
		}
	}
	return nil
}
