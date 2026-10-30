// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// runScript runs a launch script for h: terminal scripts open a tab,
// external ones start a program.
func (m *Model) runScript(h core.Host, sc core.Script) tea.Cmd {
	if sc.Mode == core.ScriptExternal {
		m.notify(false, "starting %s for %s…", sc.Name, h.Name)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
			defer cancel()
			_, err := m.svc.RunScript(ctx, h, sc, 0, 0)
			return scriptResultMsg{host: h, script: sc.Name, err: err}
		}
	}
	r := m.bodyRect()
	s, err := m.svc.RunScript(m.ctx, h, sc, r.w, r.h)
	if err != nil {
		m.notify(true, "%s: %v", sc.Name, err)
		return nil
	}
	m.tabs = append(m.tabs, newTab(s))
	m.setActive(len(m.tabs))
	return nil
}

type scriptResultMsg struct {
	host   core.Host
	script string
	err    error
}

func (m *Model) scriptResult(msg scriptResultMsg) {
	if msg.err != nil {
		m.notify(true, "%s: %v", msg.script, msg.err)
	} else {
		m.notify(false, "%s started for %s", msg.script, msg.host.Name)
	}
}

// activateHost is Enter/double click on a host: its default script, if one
// is configured, otherwise the built-in action.
func (m *Model) activateHost(h core.Host) tea.Cmd {
	if sc, ok := m.svc.DefaultScript(h); ok {
		return m.runScript(h, sc)
	}
	return m.openHost(h)
}

// scriptMenu lists the actions for a host: the built-in one and the scripts.
type scriptMenu struct {
	host    core.Host
	scripts []core.Script
	cursor  int
}

func newScriptMenu(m *Model, h core.Host) *scriptMenu {
	return &scriptMenu{host: h, scripts: m.svc.Scripts(h)}
}

func (s *scriptMenu) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	n := len(s.scripts) + 1
	switch k.String() {
	case "esc", "q", "x":
		m.modal = nil
	case "up", "k":
		s.cursor = (s.cursor + n - 1) % n
	case "down", "j", "tab":
		s.cursor = (s.cursor + 1) % n
	case "enter", " ":
		m.modal = nil
		return s.run(m, s.cursor)
	default:
		if r := k.String(); len(r) == 1 && r[0] >= '0' && r[0] <= '9' && int(r[0]-'0') < n {
			m.modal = nil
			return s.run(m, int(r[0]-'0'))
		}
	}
	return nil
}

func (s *scriptMenu) run(m *Model, i int) tea.Cmd {
	if i == 0 {
		return m.openHost(s.host)
	}
	return m.runScript(s.host, s.scripts[i-1])
}

func builtinAction(h core.Host) string {
	if h.Scheme == core.SchemeSSH {
		return "connect (built-in SSH)"
	}
	return "open in the " + string(h.Scheme) + " client"
}

func (s *scriptMenu) view(m *Model, w, h int) []string {
	var b strings.Builder
	def, hasDef := m.svc.DefaultScript(s.host)
	items := []string{builtinAction(s.host)}
	for _, sc := range s.scripts {
		where := "tab"
		if sc.Mode == core.ScriptExternal {
			where = "program"
		}
		item := fmt.Sprintf("%s  %s", sc.Name, stDim.Render("("+where+": "+strings.Join(sc.Command, " ")+")"))
		if hasDef && def.Name == sc.Name {
			item += " " + stKey.Render("Enter")
		}
		items = append(items, item)
	}
	if !hasDef {
		items[0] += " " + stKey.Render("Enter")
	}
	for i, it := range items {
		line := fmt.Sprintf("%d  %s", i, it)
		if i == s.cursor {
			line = stSelected.Render(fmt.Sprintf("%d  ", i)) + it
		}
		b.WriteString(line + "\n")
	}
	if len(s.scripts) == 0 {
		b.WriteString("\n" + stDim.Render("no scripts for this item: add them to scripts: in "+configHint))
	}
	b.WriteString("\n" + stDim.Render("Enter/0-9: run · Esc: cancel"))
	return m.box("Run for "+s.host.Name, b.String(), w)
}

// configHint names the config file in messages.
const configHint = "config.yaml"
