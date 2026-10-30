// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// programsPage is the External programs page of the settings: the client
// of each external scheme and the command of each script, per OS. Edits
// go into a draft (kept while the settings are closed) until saved; saving
// checks the drafts, writes the changed keys into the config file and
// applies them at once.
type programsPage struct {
	ready    bool
	old      core.Launchers // as in the config
	draft    core.Launchers
	entries  []core.LauncherEntry
	entry    int
	goos     int // index in core.OSes
	focus    int // index in items()
	prog     textinput.Model
	args     []textinput.Model
	status   string
	err      string
	escArmed bool // Esc with unsaved changes: the next one discards them

	// geometry of the last view (page coordinates) for the mouse
	hits []progHit
}

type progHit struct {
	item  int
	y     int
	spans [][2]int // options, or one span of an input or a button
	del   [2]int   // the × of an argument
}

// Focusable items of the page, in order.
const (
	itEntry = iota
	itOS
	itClient
	itPreset
	itProgram
	itArg // itArg+i: argument i
	itAdd = 1000 + iota
	itMode
	itSave
	itTest
	itRevert
)

const progLabelW = 10

func (p *programsPage) init(m *Model) {
	cfg := m.svc.Config()
	p.old = cfg.Launchers()
	if !p.ready {
		p.goos = max(0, slices.Index(core.OSes, runtime.GOOS))
	}
	p.ready = true
	p.reset()
}

// reset makes the draft a copy of the config again.
func (p *programsPage) reset() {
	p.draft = p.old.Clone()
	p.entries = p.entries[:0]
	for _, sc := range core.Schemes {
		if sc.External() {
			p.entries = append(p.entries, core.LauncherEntry{Scheme: sc})
			if _, ok := p.draft.External[string(sc)]; !ok {
				p.draft.External[string(sc)] = core.Launch{}
			}
		}
	}
	for i := range p.draft.Scripts {
		p.entries = append(p.entries, core.LauncherEntry{Script: &p.draft.Scripts[i]})
	}
	p.entry = max(0, min(p.entry, len(p.entries)-1))
	p.load()
}

func (p *programsPage) cur() core.LauncherEntry { return p.entries[p.entry] }
func (p *programsPage) os() string              { return core.OSes[p.goos] }

// cmd returns the draft command of entry e on goos.
func (p *programsPage) cmd(e core.LauncherEntry, goos string) []string {
	if e.Script != nil {
		return e.Script.Run.Get(goos)
	}
	return p.draft.External[string(e.Scheme)].Get(goos)
}

func (p *programsPage) setCmd(cmd []string) {
	e := p.cur()
	if e.Script != nil {
		e.Script.Run.Set(p.os(), cmd)
		return
	}
	l := p.draft.External[string(e.Scheme)]
	l.Set(p.os(), cmd)
	p.draft.External[string(e.Scheme)] = l
}

// mode returns the password mode of entry e in the draft: a VNC mode, or
// none/stdin.
func (p *programsPage) mode(e core.LauncherEntry) string {
	if e.Script != nil {
		return stdinMode(e.Script.PasswordStdin)
	}
	l := p.draft.External[string(e.Scheme)]
	if e.Scheme == core.SchemeVNC {
		if l.Password == "" {
			if l.PasswordStdin {
				return "stdin"
			}
			return core.VNCProxyMode
		}
		return l.Password
	}
	return stdinMode(l.PasswordStdin)
}

func stdinMode(on bool) string {
	if on {
		return core.PasswordStdin
	}
	return core.PasswordNone
}

func (p *programsPage) modes(e core.LauncherEntry) []string {
	if e.Script == nil && e.Scheme == core.SchemeVNC {
		return core.VNCModes
	}
	return []string{core.PasswordNone, core.PasswordStdin}
}

func (p *programsPage) setMode(mode string) {
	e := p.cur()
	switch {
	case e.Script != nil:
		e.Script.PasswordStdin = mode == core.PasswordStdin
	case e.Scheme == core.SchemeVNC:
		l := p.draft.External["vnc"]
		l.PasswordStdin = false
		l.Password = mode
		if mode == core.VNCProxyMode && p.old.External["vnc"].Password == "" {
			l.Password = "" // the default: not written into the config
		}
		p.draft.External["vnc"] = l
	default:
		l := p.draft.External[string(e.Scheme)]
		l.PasswordStdin = mode == core.PasswordStdin
		p.draft.External[string(e.Scheme)] = l
	}
}

// load fills the inputs from the draft of the current entry and OS.
func (p *programsPage) load() {
	cmd := p.cmd(p.cur(), p.os())
	p.prog = newProgInput("program: a path or a name in PATH; empty = auto-detect")
	p.args = nil
	if len(cmd) > 0 {
		p.prog.SetValue(cmd[0])
		for _, a := range cmd[1:] {
			in := newProgInput("")
			in.SetValue(a)
			p.args = append(p.args, in)
		}
	}
	p.focusItem(p.focus)
}

func newProgInput(placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 4096
	in.Placeholder = placeholder
	return in
}

// store writes the inputs into the draft.
func (p *programsPage) store() {
	cmd := []string{p.prog.Value()}
	for _, a := range p.args {
		cmd = append(cmd, a.Value())
	}
	if strings.TrimSpace(cmd[0]) == "" && len(cmd) == 1 {
		cmd = nil
	}
	p.setCmd(cmd)
	p.status, p.escArmed = "", false
}

func (p *programsPage) items() []int {
	out := []int{itEntry, itOS}
	if p.cur().Script == nil {
		out = append(out, itClient, itPreset)
	}
	out = append(out, itProgram)
	for i := range p.args {
		out = append(out, itArg+i)
	}
	return append(out, itAdd, itMode, itSave, itTest, itRevert)
}

func (p *programsPage) item() int {
	its := p.items()
	return its[min(p.focus, len(its)-1)]
}

// focusItem focuses the item at index i of items().
func (p *programsPage) focusItem(i int) {
	its := p.items()
	p.focus = max(0, min(i, len(its)-1))
	p.prog.Blur()
	for j := range p.args {
		p.args[j].Blur()
	}
	switch it := its[p.focus]; {
	case it == itProgram:
		p.prog.Focus()
	case it >= itArg && it < itAdd:
		p.args[it-itArg].Focus()
	}
}

func (p *programsPage) focusOn(item int) {
	if i := slices.Index(p.items(), item); i >= 0 {
		p.focusItem(i)
	}
}

// clients returns the known clients of the current entry on the current
// OS: those found here are marked.
func (p *programsPage) clients(m *Model) (names []string, paths []string, cur int) {
	e := p.cur()
	names, paths = []string{"auto"}, []string{""}
	found := map[*core.ClientInfo]string{}
	if p.os() == runtime.GOOS {
		for _, f := range m.svc.DetectClients(e.Scheme) {
			found[f.Client] = f.Path
		}
	}
	prog := p.prog.Value()
	for _, c := range core.KnownClients(p.os(), e.Scheme) {
		name, path := c.Name, c.DefaultPath()
		if fp, ok := found[c]; ok {
			name += " " + m.g.check
			path = fp
		}
		names, paths = append(names, name), append(paths, path)
	}
	cur = -1
	if prog == "" {
		cur = 0
	} else if c, _ := m.svc.ClientPresets(p.os(), e.Scheme, prog); c != nil {
		for i, kc := range core.KnownClients(p.os(), e.Scheme) {
			if kc == c {
				cur = i + 1
			}
		}
	}
	if cur < 0 {
		names, paths, cur = append(names, "other"), append(paths, prog), len(names)
	}
	return names, paths, cur
}

// presets returns the argument presets of the client in the program field.
func (p *programsPage) presets(m *Model) ([]core.ClientPreset, int) {
	_, ps := m.svc.ClientPresets(p.os(), p.cur().Scheme, p.prog.Value())
	args := p.cmd(p.cur(), p.os())
	if len(args) > 0 {
		args = args[1:]
	}
	for i, pr := range ps {
		if slices.Equal(pr.Args, args) && (p.cur().Scheme != core.SchemeVNC || pr.Password == p.mode(p.cur())) {
			return ps, i
		}
	}
	return ps, -1
}

func (p *programsPage) applyPreset(pr core.ClientPreset) {
	cmd := append([]string{p.prog.Value()}, pr.Args...)
	p.setCmd(cmd)
	if p.cur().Scheme == core.SchemeVNC && pr.Password != "" {
		p.setMode(pr.Password)
	}
	p.load()
}

// options returns the choices of an option item and the chosen one.
func (p *programsPage) options(m *Model, it int) ([]string, int) {
	switch it {
	case itEntry:
		var out []string
		for _, e := range p.entries {
			if e.Script != nil {
				out = append(out, "script "+e.Script.Name)
			} else {
				out = append(out, string(e.Scheme))
			}
		}
		return out, p.entry
	case itOS:
		var out []string
		for _, o := range core.OSes {
			if o == runtime.GOOS {
				o += " (here)"
			}
			out = append(out, o)
		}
		return out, p.goos
	case itClient:
		names, _, cur := p.clients(m)
		return names, cur
	case itPreset:
		ps, cur := p.presets(m)
		var out []string
		for _, pr := range ps {
			out = append(out, pr.Name)
		}
		if cur < 0 && len(ps) > 0 {
			out, cur = append(out, "custom"), len(out)
		}
		return out, cur
	case itMode:
		ms := p.modes(p.cur())
		return ms, slices.Index(ms, p.mode(p.cur()))
	}
	return nil, -1
}

// choose selects option i of an option item.
func (p *programsPage) choose(m *Model, it, i int) {
	opts, cur := p.options(m, it)
	if i < 0 || i >= len(opts) || i == cur {
		return
	}
	p.status, p.err, p.escArmed = "", "", false
	switch it {
	case itEntry:
		p.entry = i
		p.load()
	case itOS:
		p.goos = i
		p.load()
	case itClient:
		_, paths, _ := p.clients(m)
		if paths[i] == "" { // auto
			p.setCmd(nil)
			p.load()
			return
		}
		p.prog.SetValue(paths[i])
		p.store()
		if ps, _ := p.presets(m); len(ps) > 0 {
			p.applyPreset(ps[0])
		}
	case itPreset:
		if ps, _ := p.presets(m); i < len(ps) {
			p.applyPreset(ps[i])
		}
	case itMode:
		p.setMode(p.modes(p.cur())[i])
	}
}

// testHost is the item the preview and the test start use: the one selected
// in the host list if it fits, else the first that does.
func (p *programsPage) testHost(m *Model) (core.Host, string) {
	e := p.cur()
	fits := func(h core.Host) bool {
		return h.Scheme != core.SchemeLocal && (e.Script != nil || h.Scheme == e.Scheme)
	}
	if r := m.list.current(); r != nil && r.Kind == core.HostRow && fits(*r.Host) {
		return *r.Host, "selected in the list"
	}
	for _, h := range m.svc.Snapshot().Hosts {
		if fits(h) {
			return h, "first fitting item; select another in the list"
		}
	}
	sc := e.Scheme
	if e.Script != nil {
		sc = core.SchemeSSH
	}
	return core.Host{Name: "example", Scheme: sc, Addr: "host.example", User: "user", Path: "share"}, "an example: no such item"
}

// check returns what is wrong with the command of entry e on goos.
func (p *programsPage) check(m *Model, e core.LauncherEntry, goos string) error {
	cmd, mode := p.cmd(e, goos), p.mode(e)
	if len(cmd) == 0 {
		// another OS keeps auto-detection: told when started there
		if ph := core.PlaceholderOf(mode); e.Script == nil && ph != "" && goos == runtime.GOOS {
			return fmt.Errorf("the password mode %s needs a configured client with %s", mode, ph)
		}
		if e.Script != nil && goos == runtime.GOOS {
			return fmt.Errorf("the script has no command for %s", goos)
		}
		return nil
	}
	if strings.TrimSpace(cmd[0]) == "" {
		return fmt.Errorf("no program (the arguments need one)")
	}
	if err := core.CheckArgs(cmd[1:], e.Kind(), mode); err != nil {
		return err
	}
	if goos == runtime.GOOS {
		if _, err := m.svc.CheckProgram(cmd[0]); err != nil {
			return err
		}
	}
	return nil
}

func (p *programsPage) entryName(e core.LauncherEntry) string {
	if e.Script != nil {
		return "script " + e.Script.Name
	}
	return string(e.Scheme)
}

// changed reports whether the draft of entry e differs from the config.
func (p *programsPage) changed(e core.LauncherEntry, goos string) bool {
	var old []string
	oldMode := ""
	if e.Script != nil {
		for _, sc := range p.old.Scripts {
			if sc.Name == e.Script.Name {
				old, oldMode = sc.Run.Get(goos), stdinMode(sc.PasswordStdin)
			}
		}
	} else {
		ol := p.old.External[string(e.Scheme)]
		old = ol.Get(goos)
		oldMode = stdinMode(ol.PasswordStdin)
		if e.Scheme == core.SchemeVNC {
			oldMode = ol.Password
			if oldMode == "" {
				oldMode = core.VNCProxyMode
			}
		}
	}
	return !slices.Equal(old, p.cmd(e, goos)) || oldMode != p.mode(e)
}

func (p *programsPage) dirty() bool {
	for _, e := range p.entries {
		for _, o := range core.OSes {
			if p.changed(e, o) {
				return true
			}
		}
	}
	return false
}

// save checks the changed commands and saves them.
func (p *programsPage) save(m *Model) {
	p.status, p.err, p.escArmed = "", "", false
	if !p.dirty() {
		p.status = "nothing changed"
		return
	}
	for i, e := range p.entries {
		for g, o := range core.OSes {
			if !p.changed(e, o) {
				continue
			}
			if err := p.check(m, e, o); err != nil {
				p.entry, p.goos = i, g
				p.load()
				p.err = fmt.Sprintf("not saved: %s on %s: %v", p.entryName(e), o, err)
				return
			}
		}
	}
	if err := m.svc.SaveLaunchers(p.old, p.draft); err != nil {
		p.err = "not saved: " + err.Error()
		return
	}
	m.cfgSeq = m.svc.ConfigStatus().Seq // our own reload: no message
	p.init(m)
	p.status = "saved to " + filepath.Base(configName(m)) + " and applied: the next start uses it"
}

func configName(m *Model) string {
	if c := m.svc.ConfigStatus().Path; c != "" {
		return c
	}
	return configHint
}

func (p *programsPage) revert() {
	p.reset()
	p.status, p.err, p.escArmed = "changes discarded", "", false
}

// progTestMsg is the outcome of a test start of an external program.
type progTestMsg struct {
	name string
	err  error
}

// test starts the draft command of the current entry for the test item.
func (p *programsPage) test(m *Model) tea.Cmd {
	p.status, p.err = "", ""
	if p.os() != runtime.GOOS {
		p.err = fmt.Sprintf("only the %s command can be started here", runtime.GOOS)
		return nil
	}
	e := p.cur()
	if err := p.check(m, e, p.os()); err != nil {
		p.err = err.Error()
		return nil
	}
	h, why := p.testHost(m)
	if strings.HasPrefix(why, "an example") {
		p.err = fmt.Sprintf("no %s item to start it for", e.Scheme)
		return nil
	}
	cmd, mode := p.cmd(e, p.os()), p.mode(e)
	if e.Scheme == core.SchemeVNC || e.Script != nil && e.Script.Mode != string(core.ScriptExternal) {
		// opens in a tab
		r := m.bodyRect()
		s, err := m.svc.TestLaunch(m.ctx, h, e, cmd, mode, r.w, r.h)
		if err != nil {
			p.err = err.Error()
			return nil
		}
		if s != nil {
			m.modal = nil
			m.tabs = append(m.tabs, newTab(s))
			m.setActive(len(m.tabs))
			m.notify(false, "test start of %s for %s (the settings keep the draft)", p.entryName(e), h.Name)
		}
		return nil
	}
	p.status = "starting for " + h.Name + m.g.ell
	name := p.entryName(e)
	return func() tea.Msg {
		_, err := m.svc.TestLaunch(m.ctx, h, e, cmd, mode, 0, 0)
		return progTestMsg{name: name + " for " + h.Name, err: err}
	}
}

func (p *programsPage) testResult(msg progTestMsg) {
	if msg.err != nil {
		p.status, p.err = "", "test start of "+msg.name+": "+msg.err.Error()
		return
	}
	p.status, p.err = "started "+msg.name, ""
}

// activate is Enter/Space/click on a button item.
func (p *programsPage) activate(m *Model, it int) tea.Cmd {
	switch it {
	case itAdd:
		p.args = append(p.args, newProgInput(""))
		p.store()
		p.focusOn(itArg + len(p.args) - 1)
	case itSave:
		p.save(m)
	case itTest:
		return p.test(m)
	case itRevert:
		p.revert()
	}
	return nil
}

func (p *programsPage) deleteArg(i int) {
	if i < 0 || i >= len(p.args) {
		return
	}
	p.args = append(p.args[:i], p.args[i+1:]...)
	p.store()
	p.focusOn(itArg + min(i, len(p.args)-1))
	if len(p.args) == 0 {
		p.focusOn(itAdd)
	}
}

// update handles a key; handled is false for the keys of the settings
// dialog (page switch, close).
func (p *programsPage) update(m *Model, k tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	if !p.ready {
		p.init(m)
	}
	its := p.items()
	it := p.item()
	input := it == itProgram || it >= itArg && it < itAdd
	opts := it == itEntry || it == itOS || it == itClient || it == itPreset || it == itMode
	button := it == itAdd || it >= itSave
	key := k.String()
	if key != "esc" {
		p.escArmed = false
	}
	switch key {
	case "tab", "shift+tab":
		return nil, false
	case "esc", "q", ",":
		if (key == "q" || key == ",") && input {
			break
		}
		if p.dirty() && !p.escArmed {
			p.escArmed = true
			p.err = "unsaved changes: Esc again discards them, Ctrl+S saves"
			return nil, true
		}
		if p.escArmed {
			p.revert()
			p.status = ""
		}
		return nil, false
	case "ctrl+s":
		p.save(m)
		return nil, true
	case "up":
		p.focusItem((p.focus + len(its) - 1) % len(its))
		return nil, true
	case "down":
		p.focusItem((p.focus + 1) % len(its))
		return nil, true
	case "enter":
		if button {
			return p.activate(m, it), true
		}
		p.focusItem(p.focus + 1)
		return nil, true
	case "ctrl+n":
		i := len(p.args)
		if it >= itArg && it < itAdd {
			i = it - itArg + 1
		}
		p.args = slices.Insert(p.args, i, newProgInput(""))
		p.store()
		p.focusOn(itArg + i)
		return nil, true
	case "ctrl+d":
		if it >= itArg && it < itAdd {
			p.deleteArg(it - itArg)
		}
		return nil, true
	case "alt+up", "alt+down":
		if it >= itArg && it < itAdd {
			i, j := it-itArg, it-itArg+1
			if key == "alt+up" {
				j = i - 1
			}
			if j >= 0 && j < len(p.args) {
				p.args[i], p.args[j] = p.args[j], p.args[i]
				p.store()
				p.focusOn(itArg + j)
			}
		}
		return nil, true
	}
	switch {
	case opts && (key == "left" || key == "h" || key == "right" || key == "l" || key == " "):
		o, cur := p.options(m, it)
		if len(o) == 0 {
			return nil, true
		}
		d := 1
		if key == "left" || key == "h" {
			d = -1
		}
		p.choose(m, it, (max(cur, 0)+d+len(o))%len(o))
	case button && (key == "left" || key == "right"):
		d := 1
		if key == "left" {
			d = -1
		}
		p.focusItem(p.focus + d)
	case button && key == " ":
		return p.activate(m, it), true
	case input:
		var c tea.Cmd
		if it == itProgram {
			p.prog, c = p.prog.Update(k)
		} else {
			p.args[it-itArg], c = p.args[it-itArg].Update(k)
		}
		p.store()
		return c, true
	}
	return nil, true
}

func (p *programsPage) mouse(m *Model, x, y int) tea.Cmd {
	if !p.ready {
		p.init(m)
	}
	for _, h := range p.hits {
		if h.y != y {
			continue
		}
		if h.item >= itArg && h.item < itAdd && x >= h.del[0] && x < h.del[1] {
			p.deleteArg(h.item - itArg)
			return nil
		}
		i := spanAt(h.spans, x)
		if i < 0 {
			continue
		}
		p.focusOn(h.item)
		switch it := h.item; {
		case it == itProgram:
			p.prog.SetCursor(x - h.spans[0][0])
		case it >= itArg && it < itAdd:
			p.args[it-itArg].SetCursor(x - h.spans[0][0])
		case it == itAdd || it >= itSave:
			return p.activate(m, it)
		default:
			p.choose(m, it, i)
		}
		return nil
	}
	return nil
}

// view renders the page in width w; y0 is the dialog row of its first
// line (for the mouse).
func (p *programsPage) view(m *Model, w, y0 int) string {
	if !p.ready {
		p.init(m)
	}
	var b strings.Builder
	p.hits = p.hits[:0]
	y := y0
	nl := func() { b.WriteString("\n"); y++ }
	focused := p.item()
	marker := func(it int) string {
		if it != focused {
			return "  "
		}
		if m.opt.Icons.ASCII() {
			return stKey.Render("> ")
		}
		return stKey.Render("▸ ")
	}
	label := func(it int, s string) string {
		s = padRight(s, progLabelW)
		if it == focused {
			return marker(it) + stKey.Render(s)
		}
		return marker(it) + stDim.Render(s)
	}
	x0 := 2 + progLabelW
	inW := max(20, min(64, w-x0-4))
	optRow := func(it int, name string) {
		o, cur := p.options(m, it)
		b.WriteString(label(it, name))
		var spans [][2]int
		x := x0
		for i, s := range o {
			cell := " " + s + " "
			cw := ansi.StringWidth(cell)
			if x+cw > w {
				b.WriteString(m.g.ell)
				break
			}
			if i == cur {
				b.WriteString(stSelected.Render(cell))
			} else {
				b.WriteString(cell)
			}
			spans = append(spans, [2]int{x, x + cw})
			x += cw
		}
		p.hits = append(p.hits, progHit{item: it, y: y, spans: spans})
		nl()
	}
	e := p.cur()
	optRow(itEntry, "Item")
	optRow(itOS, "OS")
	if e.Script == nil {
		optRow(itClient, "Client")
		if o, _ := p.options(m, itPreset); len(o) > 0 {
			optRow(itPreset, "Preset")
		} else {
			b.WriteString(label(itPreset, "Preset") + stDim.Render("no presets for this program")) // still focusable
			p.hits = append(p.hits, progHit{item: itPreset, y: y})
			nl()
		}
	}
	p.prog.Width = inW
	b.WriteString(label(itProgram, "Program") + p.prog.View())
	p.hits = append(p.hits, progHit{item: itProgram, y: y, spans: [][2]int{{x0, x0 + inW + 1}}})
	nl()
	b.WriteString(strings.Repeat(" ", x0))
	switch {
	case p.prog.Value() == "" && e.Script == nil:
		b.WriteString(stDim.Render("auto: the first installed known client"))
	case p.prog.Value() == "":
		b.WriteString(stDim.Render("no command on " + p.os()))
	case p.os() != runtime.GOOS:
		b.WriteString(stDim.Render("not checked here (" + runtime.GOOS + ")"))
	default:
		if path, err := m.svc.CheckProgram(p.prog.Value()); err != nil {
			b.WriteString(stErr.Render(m.ascii("✗ ") + err.Error()))
		} else {
			b.WriteString(stOK.Render(m.g.check) + " " + stDim.Render(path))
		}
	}
	nl()
	for i := range p.args {
		it := itArg + i
		name := ""
		if i == 0 {
			name = "Args"
		}
		b.WriteString(label(it, fmt.Sprintf("%-7s%2d ", name, i+1)))
		p.args[i].Width = inW - 4
		v := padRight(p.args[i].View(), inW-3)
		del := " " + m.ascii("✕") + " "
		if m.opt.Icons.ASCII() {
			del = " x "
		}
		b.WriteString(v + stDim.Render(del))
		dx := x0 + inW - 3
		p.hits = append(p.hits, progHit{item: it, y: y, spans: [][2]int{{x0, dx}}, del: [2]int{dx, dx + 3}})
		nl()
	}
	add := "[+ argument]"
	if focused == itAdd {
		add = stSelected.Render(add)
	}
	b.WriteString(label(itAdd, ifStr(len(p.args) == 0, "Args", "")) + add)
	p.hits = append(p.hits, progHit{item: itAdd, y: y, spans: [][2]int{{x0, x0 + 12}}})
	nl()
	help := wrapWords(placeholderHelp(e), w-x0, x0)
	b.WriteString(strings.Repeat(" ", x0) + stDim.Render(help))
	y += strings.Count(help, "\n")
	nl()
	optRow(itMode, "Password")
	mh := modeHelp(e, p.mode(e))
	if pwArg := slices.ContainsFunc(p.cmd(e, p.os()), func(a string) bool { return strings.Contains(a, "{password}") }); pwArg && (e.Script != nil || e.Scheme != core.SchemeVNC) {
		mh = "the password is put in place of {password} in the command line" + ifStr(p.mode(e) == core.PasswordStdin, ", and written to stdin", "")
	}
	b.WriteString(strings.Repeat(" ", x0) + stDim.Render(fitWidth(mh, w-x0, m)))
	nl()
	nl()

	// preview and checks
	h, why := p.testHost(m)
	cmd := p.cmd(e, p.os())
	prev := core.PreviewCmd(cmd, e.Kind(), p.mode(e), h)
	if len(cmd) == 0 {
		prev = stDim.Render("(auto-detected at start)")
	}
	for i, l := range strings.Split(ansi.Hardwrap(prev, max(10, w-x0), true), "\n") {
		if i == 0 {
			b.WriteString("  " + stBold.Render(padRight("Preview", progLabelW)) + l)
		} else {
			b.WriteString(strings.Repeat(" ", x0) + l)
		}
		nl()
	}
	b.WriteString(strings.Repeat(" ", x0) + stDim.Render(fitWidth("for "+h.Name+" ("+why+"); passwords are masked", w-x0, m)))
	nl()
	if err := p.check(m, e, p.os()); err != nil {
		for _, l := range strings.Split(ansi.Wordwrap(err.Error(), max(10, w-x0), " "), "\n") {
			b.WriteString(strings.Repeat(" ", x0) + stErr.Render(l))
			nl()
		}
	} else if (e.Script != nil || e.Scheme != core.SchemeVNC) && slices.ContainsFunc(cmd, func(a string) bool { return strings.Contains(a, "{password}") }) {
		for _, l := range strings.Split(ansi.Wordwrap(m.ascii("⚠ ")+core.ArgvWarning, max(10, w-x0), " "), "\n") {
			b.WriteString(strings.Repeat(" ", x0) + stWarn.Render(l))
			nl()
		}
	}
	nl()

	labels := []string{"Save", "Test start", "Revert"}
	btnFocus := -1
	if focused >= itSave {
		btnFocus = focused - itSave
	}
	row, spans := buttonRow(labels, btnFocus)
	b.WriteString("  " + row)
	for i := range spans {
		p.hits = append(p.hits, progHit{item: itSave + i, y: y, spans: [][2]int{{spans[i][0] + 2, spans[i][1] + 2}}})
	}
	if p.dirty() {
		b.WriteString("  " + stWarn.Render("unsaved changes"))
	}
	nl()
	switch {
	case p.err != "":
		for _, l := range strings.Split(ansi.Wordwrap(p.err, max(10, w-2), " "), "\n") {
			b.WriteString("  " + stErr.Render(l))
			nl()
		}
	case p.status != "":
		b.WriteString("  " + stOK.Render(m.g.check) + " " + stDim.Render(fitWidth(p.status, w-4, m)))
		nl()
	default:
		b.WriteString("  " + stDim.Render(fitWidth("Saved changes apply at once; config.yaml edited by hand is re-read too.", w-2, m)))
		nl()
	}
	b.WriteString("  " + stDim.Render(fitWidth(m.ascii("↑/↓ field · ←/→ choose · Ctrl+N/Ctrl+D add/delete argument · Alt+↑/↓ move it · Ctrl+S save · Tab: page · Esc: close"), w-2, m)))
	return b.String()
}

// modeHelp explains the password mode (one for all OSes).
func modeHelp(e core.LauncherEntry, mode string) string {
	var s string
	switch mode {
	case core.VNCProxyMode:
		s = "Vaultcrab signs in, the client connects to a local proxy without a password"
	case "stdin":
		s = "the password is written to the program's stdin"
	case "file":
		s = "a temporary vncpasswd file in {passfile}, removed after the start"
	case "url":
		s = "the password is in {url}: visible in the process list"
	case "argv":
		s = "the password is in {password}: visible in the process list"
	default:
		s = "the program gets no password"
	}
	if e.Script != nil && mode == core.PasswordStdin && e.Script.Mode != string(core.ScriptExternal) {
		s = "stdin needs a script with mode: external"
	}
	return s + " (for every OS)"
}

func placeholderHelp(e core.LauncherEntry) string {
	s := "placeholders: " + strings.Join(core.Placeholders, " ")
	if e.Script == nil && e.Scheme == core.SchemeVNC {
		s += " {passfile} (password file) {password} (argv)"
	} else {
		s += " {password} (seen in the process list)"
	}
	return s
}

// wrapWords wraps s at w columns, indenting the continuation lines.
func wrapWords(s string, w, indent int) string {
	return strings.ReplaceAll(ansi.Wordwrap(s, max(10, w), " "), "\n", "\n"+strings.Repeat(" ", indent))
}

func fitWidth(s string, w int, m *Model) string { return ansi.Truncate(s, max(1, w), m.g.ell) }

func ifStr(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
