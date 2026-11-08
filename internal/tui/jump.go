// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// jumpPicker is the quick connection through a jump host (ui.keys.jump, j):
// step 1 picks the jump host (a template or any SSH host; the ones used
// before first, fuzzy search by typing), step 2 the target (an address
// typed, a target used before through this jump host, or a host of the
// list).
type jumpPicker struct {
	step int // 0: the jump host, 1: the target
	in   textinput.Model

	choices []core.JumpChoice // step 1, all
	shown   []core.JumpChoice // step 1, matching the search
	via     core.JumpChoice   // picked in step 1

	sugg []jumpSugg // step 2
	cur  int        // the highlighted row (step 2: -1 = the typed address)
	top  int        // first row shown
	err  string

	listY int // dialog row of the first list row (mouse)
	rows  int // list rows shown

	cred string      // step 2: the credential of a typed address (JumpSpec.Cred)
	cp   *credPicker // step 2: choosing it (Ctrl+K)
}

// jumpSugg is a target suggestion of step 2.
type jumpSugg struct {
	spec   core.JumpSpec
	label  string
	detail string
}

const jumpListRows = 10

func newJumpPicker(m *Model) *jumpPicker {
	p := &jumpPicker{choices: m.svc.JumpChoices()}
	p.in = textinput.New()
	p.in.Prompt = "> "
	p.in.CharLimit = 255
	p.in.Focus()
	p.toStep(m, 0)
	return p
}

// openJumpPicker opens the picker (the hosts must be known).
func (m *Model) openJumpPicker() {
	m.modal = newJumpPicker(m)
}

func (p *jumpPicker) toStep(m *Model, step int) {
	p.step, p.err, p.top = step, "", 0
	p.in.SetValue("")
	if step == 0 {
		p.in.Placeholder = "search jump hosts and templates"
		p.filter()
		return
	}
	p.in.Placeholder = "IP or [user@]host[:port]"
	p.cred = m.svc.JumpLastCred(p.via.Key) // the choice of the last time
	p.suggest(m)
}

// filter applies the search of step 1: the order of JumpChoices (used
// before first) without a query, the best matches first with one.
func (p *jumpPicker) filter() {
	q := p.in.Value()
	type scored struct {
		c core.JumpChoice
		s int
	}
	var out []scored
	for _, c := range p.choices {
		s, ok := core.FuzzyMatch(q, c.Label+" "+c.Detail)
		if ok {
			if ls, lok := core.FuzzyMatch(q, c.Label); lok {
				s = max(s, ls+50) // the name counts more than the address
			}
			out = append(out, scored{c, s})
		}
	}
	if strings.TrimSpace(q) != "" {
		slices.SortStableFunc(out, func(a, b scored) int { return b.s - a.s })
	}
	p.shown = p.shown[:0]
	for _, o := range out {
		p.shown = append(p.shown, o.c)
	}
	p.cur = 0
	p.top = 0
}

// suggest builds step 2: the targets used through this jump host (best
// first) that match the input, then the SSH hosts of the list that match
// it (only while something is typed).
func (p *jumpPicker) suggest(m *Model) {
	q := strings.TrimSpace(p.in.Value())
	p.sugg = p.sugg[:0]
	for _, t := range m.svc.JumpTargets(p.via.Key) {
		if _, ok := core.FuzzyMatch(q, t.Label); ok {
			d := "used " + ago(t.Last)
			if t.Spec.Cred != "" {
				d += " · " + m.svc.CredLabel(t.Spec.Cred)
			}
			p.sugg = append(p.sugg, jumpSugg{spec: t.Spec, label: t.Label, detail: d})
		}
	}
	if q != "" {
		type scored struct {
			h core.Host
			s int
		}
		var hs []scored
		for _, h := range m.svc.Snapshot().Hosts {
			if h.Scheme != core.SchemeSSH || "h:"+h.ID == p.via.Key {
				continue
			}
			if s, ok := core.FuzzyMatch(q, h.Name+" "+h.Target()); ok {
				hs = append(hs, scored{h, s})
			}
		}
		slices.SortStableFunc(hs, func(a, b scored) int { return b.s - a.s })
		for _, h := range hs {
			sp := core.JumpSpec{Via: p.via.Key, Item: h.h.ID}
			if slices.ContainsFunc(p.sugg, func(s jumpSugg) bool { return s.spec == sp }) {
				continue
			}
			p.sugg = append(p.sugg, jumpSugg{spec: sp, label: h.h.Name, detail: h.h.Target() + " (vault)"})
		}
	}
	p.cur, p.top = -1, 0
	if q == "" && len(p.sugg) > 0 {
		p.cur = 0 // Enter on the empty input connects to the best one
	}
}

// ago says how long ago t was, roughly.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d/time.Minute)) + " min ago"
	case d < 48*time.Hour:
		return itoa(int(d/time.Hour)) + " h ago"
	}
	return itoa(int(d/(24*time.Hour))) + " days ago"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func (p *jumpPicker) n() int {
	if p.step == 0 {
		return len(p.shown)
	}
	return len(p.sugg)
}

func (p *jumpPicker) move(d int) {
	n := p.n()
	if n == 0 {
		return
	}
	lo := 0
	if p.step == 1 {
		lo = -1 // back to the input
	}
	p.cur = max(lo, min(n-1, p.cur+d))
	if p.cur >= 0 {
		if p.cur < p.top {
			p.top = p.cur
		}
		if p.cur >= p.top+jumpListRows {
			p.top = p.cur - jumpListRows + 1
		}
	}
}

func (p *jumpPicker) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if p.cp != nil {
		return p.cp.update(k)
	}
	switch k.String() {
	case "ctrl+k":
		if p.step == 1 {
			p.cp = newCredPicker(m, p.via.Key, p.cred, func(ref string) {
				p.cred, p.cp = ref, nil
			}, func() { p.cp = nil })
		}
		return nil
	case "esc":
		if p.step == 1 {
			p.toStep(m, 0)
		} else {
			m.modal = nil
		}
		return nil
	case "up", "ctrl+p":
		p.move(-1)
		return nil
	case "down", "ctrl+n":
		p.move(1)
		return nil
	case "pgup":
		p.move(-jumpListRows)
		return nil
	case "pgdown":
		p.move(jumpListRows)
		return nil
	case "tab":
		// step 2: put the highlighted target into the input to edit it
		if p.step == 1 && p.cur >= 0 && p.cur < len(p.sugg) && p.sugg[p.cur].spec.Target != "" {
			p.in.SetValue(p.sugg[p.cur].spec.Target)
			p.in.CursorEnd()
			p.suggest(m)
		}
		return nil
	case "enter":
		return p.choose(m)
	}
	var cmd tea.Cmd
	old := p.in.Value()
	p.in, cmd = p.in.Update(k)
	if p.in.Value() != old {
		p.err = ""
		if p.step == 0 {
			p.filter()
		} else {
			p.suggest(m)
		}
	}
	return cmd
}

// choose acts on Enter: step 1 goes on to the target, step 2 connects.
func (p *jumpPicker) choose(m *Model) tea.Cmd {
	if p.step == 0 {
		if p.cur < 0 || p.cur >= len(p.shown) {
			return nil
		}
		p.via = p.shown[p.cur]
		p.toStep(m, 1)
		return nil
	}
	var spec core.JumpSpec
	if p.cur >= 0 && p.cur < len(p.sugg) {
		spec = p.sugg[p.cur].spec
	} else {
		t, err := core.NormalizeTarget(p.in.Value())
		if err != nil {
			p.err = err.Error()
			return nil
		}
		spec = core.JumpSpec{Via: p.via.Key, Target: t, Cred: p.cred}
	}
	return m.openJump(spec, p)
}

// openJump opens a tab connecting through the jump host; on an error the
// picker stays open with it.
func (m *Model) openJump(spec core.JumpSpec, p *jumpPicker) tea.Cmd {
	r := m.bodyRect()
	s, err := m.svc.OpenJump(spec, r.w, r.h)
	if err != nil {
		if p != nil {
			p.err = err.Error()
		} else {
			m.notify(true, "%v", err)
		}
		return nil
	}
	if p != nil {
		m.modal = nil
	}
	m.tabs = append(m.tabs, newTab(s))
	m.setActive(len(m.tabs))
	return nil
}

func (p *jumpPicker) view(m *Model, w, h int) []string {
	var b strings.Builder
	lines := 0
	line := func(s string) {
		b.WriteString(s + "\n")
		lines++
	}
	title := "Connect through a jump host"
	if p.step == 0 {
		line(stDim.Render("Step 1 of 2: the jump host"))
	} else {
		line(stDim.Render("Step 2 of 2: the target through ") + stBold.Render(p.via.Label) + stDim.Render(" ("+p.via.Detail+")"))
		if tpl, ok := m.jumpTemplate(p.via.Key); ok && tpl.HostKeys() == "accept-new" {
			line(stWarn.Render("⚠ accept-new: new host keys are remembered without asking"))
		}
	}
	width := max(40, min(w-8, 90))
	if p.cp != nil {
		p.cp.view(m, &b, boxPadY+boxTitleGap+lines, width)
		return m.box(title, strings.TrimRight(b.String(), "\n"), w)
	}
	line("")
	line(p.in.View())
	if p.step == 1 {
		line(stDim.Render("Credentials: ") + m.svc.CredLabel(p.cred) + stDim.Render("   (Ctrl+K: change)"))
	}
	line("")
	p.listY = boxPadY + boxTitleGap + lines
	p.rows = 0
	rowLine := func(i int, mark, label, detail, tag string) {
		sel := i == p.cur
		l := padRight(trunc(label, 28), 28) + " " + stDim.Render(trunc(detail, max(10, width-44)))
		if tag != "" {
			l += " " + stDim.Render(tag)
		}
		if sel {
			l = stTabActive.Render(mark+" ") + l
		} else {
			l = "  " + l
		}
		line(ansi.Truncate(l, width, m.g.ell))
		p.rows++
	}
	switch p.step {
	case 0:
		if len(p.shown) == 0 {
			line(stDim.Render("  nothing matches"))
		}
		for i := p.top; i < len(p.shown) && i < p.top+jumpListRows; i++ {
			c := p.shown[i]
			tag := ""
			switch {
			case c.Recent:
				tag = "recent"
			case c.Template:
				tag = "template"
			}
			if c.Recent && c.Template {
				tag = "recent template"
			}
			rowLine(i, "▸", c.Label, c.Detail, tag)
		}
	case 1:
		if len(p.sugg) == 0 {
			if m.svc.JumpHistoryEnabled() {
				line(stDim.Render("  no targets used through this jump host yet: type an address"))
			} else {
				line(stDim.Render("  the history is off (jump.history): type an address"))
			}
		}
		for i := p.top; i < len(p.sugg) && i < p.top+jumpListRows; i++ {
			s := p.sugg[i]
			rowLine(i, "▸", s.label, s.detail, "")
		}
	}
	if n := p.n(); n > p.top+jumpListRows {
		line(stDim.Render("  " + m.g.ell + " " + itoa(n-p.top-jumpListRows) + " more"))
	}
	if p.err != "" {
		line("")
		line(stErr.Render(p.err))
	}
	line("")
	if p.step == 0 {
		line(stDim.Render("type to search · ↑/↓ choose · Enter: next · Esc: close"))
	} else {
		line(stDim.Render("Enter: connect · ↑/↓ suggestions · Tab: edit the suggestion · Ctrl+K: credentials · Esc: back"))
	}
	_ = h
	return m.box(title, strings.TrimRight(b.String(), "\n"), w)
}

// mouse: a click selects a row, a click on the selected row (or a double
// click) chooses it; the wheel scrolls.
func (p *jumpPicker) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if p.cp != nil {
		p.cp.mouse(msg, y)
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		p.move(-1)
		return nil
	case tea.MouseButtonWheelDown:
		p.move(1)
		return nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	r := y - p.listY
	if r < 0 || r >= p.rows {
		return nil
	}
	i := p.top + r
	if i >= p.n() {
		return nil
	}
	again := i == p.cur
	p.cur = i
	if again {
		return p.choose(m)
	}
	return nil
}

// jumpTemplate returns the template of a choice key "t:<name>".
func (m *Model) jumpTemplate(key string) (core.JumpTemplate, bool) {
	name, ok := strings.CutPrefix(key, "t:")
	if !ok {
		return core.JumpTemplate{}, false
	}
	for _, t := range m.svc.Config().JumpTemplates {
		if t.Name == name {
			return t, true
		}
	}
	return core.JumpTemplate{}, false
}
