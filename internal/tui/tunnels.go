// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The tunnels: the list of the saved tunnels (tunnels: of the config) and
// of the running ones, with their state, started, stopped, restarted,
// copied, opened in the browser, added, edited and removed here. The same
// pane is the Tunnels dialog (prefix T, the ⇄ button of the tab bar, T in
// the host list, Ctrl+T in the jump picker) and Settings → Tunnels.

// tunnelPane is the list of the tunnels, or the form of one.
type tunnelPane struct {
	row    int
	form   *tunnelForm
	remove int // the saved tunnel to remove on a second D (-1 = none)
	err    string
	status string
	warn   bool // status is a warning

	rowY []int // dialog rows of the tunnel lines
}

func newTunnelPane() tunnelPane { return tunnelPane{remove: -1} }

// tunnelRow is a line of the list: a saved tunnel (running or not) or a
// running one that is not saved.
type tunnelRow struct {
	saved int // index in tunnels: (-1 = not saved)
	tpl   core.TunnelTemplate
	info  *core.TunnelInfo
}

func (r tunnelRow) name() string {
	if r.saved >= 0 {
		return r.tpl.Name
	}
	return r.info.Label()
}

func tunnelRows(m *Model) []tunnelRow {
	saved := m.svc.SavedTunnels()
	running := m.svc.Tunnels()
	used := map[int]bool{}
	var rows []tunnelRow
	for i, t := range saved {
		r := tunnelRow{saved: i, tpl: t}
		for j := range running {
			if !used[j] && running[j].Spec.Name != "" && strings.EqualFold(running[j].Spec.Name, t.Name) {
				r.info, used[j] = &running[j], true
				break
			}
		}
		rows = append(rows, r)
	}
	for j := range running {
		if !used[j] {
			rows = append(rows, tunnelRow{saved: -1, info: &running[j]})
		}
	}
	return rows
}

func (p *tunnelPane) say(err bool, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if err {
		p.err, p.status = msg, ""
	} else {
		p.err, p.status, p.warn = "", msg, false
	}
}

func (p *tunnelPane) warnf(format string, args ...any) {
	p.err, p.status, p.warn = "", fmt.Sprintf(format, args...), true
}

// update handles a key; false: not used (Esc, Tab of the settings).
func (p *tunnelPane) update(m *Model, k tea.KeyMsg) (tea.Cmd, bool) {
	if p.form != nil {
		return p.form.update(m, p, k), true
	}
	rows := tunnelRows(m)
	p.row = max(0, min(p.row, len(rows)-1))
	key := k.String()
	if key != "D" {
		p.remove = -1
	}
	var cur *tunnelRow
	if p.row < len(rows) {
		cur = &rows[p.row]
	}
	switch key {
	case "up", "k":
		if len(rows) > 0 {
			p.row = (p.row + len(rows) - 1) % len(rows)
		}
	case "down", "j":
		if len(rows) > 0 {
			p.row = (p.row + 1) % len(rows)
		}
	case "a":
		p.form = newTunnelForm(m, -1, core.TunnelTemplate{}, 0)
	case "enter":
		if cur == nil {
			p.form = newTunnelForm(m, -1, core.TunnelTemplate{}, 0)
		} else if cur.info == nil {
			p.start(m, *cur)
		} else if cur.info.Spec.SOCKS {
			p.copy(m, *cur.info)
		} else {
			p.open(m, *cur.info)
		}
	case "s":
		if cur == nil {
			break
		}
		if cur.info == nil {
			p.start(m, *cur)
		} else {
			p.stop(m, *cur.info)
		}
	case "x":
		if cur != nil && cur.info != nil {
			p.stop(m, *cur.info)
		}
	case "r":
		if cur != nil && cur.info != nil {
			m.svc.RestartTunnel(cur.info.ID)
			p.say(false, "%s: connecting again", cur.name())
		}
	case "y":
		if cur != nil && cur.info != nil {
			p.copy(m, *cur.info)
		}
	case "o":
		if cur != nil && cur.info != nil {
			p.open(m, *cur.info)
		}
	case "e", "S":
		if cur == nil {
			break
		}
		if cur.saved >= 0 {
			id := 0
			if cur.info != nil {
				id = cur.info.ID
			}
			p.form = newTunnelForm(m, cur.saved, cur.tpl, id)
		} else {
			p.form = newTunnelForm(m, -1, templateOf(m, *cur.info), cur.info.ID)
			p.form.status = "type a name to save the tunnel in the config"
			p.form.focus(0)
		}
	case "D":
		if cur == nil || cur.saved < 0 {
			break
		}
		if p.remove != cur.saved {
			p.remove = cur.saved
			p.warnf("press D again to remove the saved tunnel %s", cur.tpl.Name)
			break
		}
		p.remove = -1
		rest := slices.Delete(slices.Clone(m.svc.SavedTunnels()), cur.saved, cur.saved+1)
		if err := m.svc.SaveTunnels(rest); err != nil {
			p.say(true, "%v", err)
			break
		}
		msg := "removed from the config"
		if cur.info != nil {
			msg += "; it runs until stopped (x)"
		}
		p.say(false, "%s %s", cur.tpl.Name, msg)
	default:
		return nil, false
	}
	return nil, true
}

// templateOf turns a running tunnel into a saved one.
func templateOf(m *Model, t core.TunnelInfo) core.TunnelTemplate {
	tp := core.TunnelTemplate{Name: t.Spec.Name, Jump: m.svc.JumpRef(t.Spec.Via), Target: t.Spec.Target,
		LocalPort: core.LocalPort(t.Port), Open: t.Spec.Open, Bind: t.Spec.Bind}
	if t.Spec.SOCKS {
		tp.Type = core.TunnelSOCKS
	}
	return tp
}

func (p *tunnelPane) start(m *Model, r tunnelRow) {
	sp, err := m.svc.TunnelSpecOf(r.tpl)
	if err != nil {
		p.say(true, "%v", err)
		return
	}
	t, err := m.svc.StartTunnel(sp)
	if err != nil {
		p.say(true, "%s: %v", r.tpl.Name, err)
		return
	}
	p.started(m, t)
}

// started reports a tunnel just started (and a busy port it avoided).
func (p *tunnelPane) started(m *Model, t core.TunnelInfo) {
	m.tunnelSeen(t)
	msg := t.Label() + ": " + t.LocalAddr()
	if t.Note != "" {
		p.warnf("%s (%s)", msg, t.Note)
		return
	}
	p.say(false, "%s, connecting", msg)
}

func (p *tunnelPane) stop(m *Model, t core.TunnelInfo) {
	m.svc.StopTunnel(t.ID)
	p.say(false, "%s stopped", t.Label())
}

func (p *tunnelPane) copy(m *Model, t core.TunnelInfo) {
	a := t.LocalAddr()
	if !t.Spec.SOCKS {
		a = t.TunnelURL()
	}
	m.copyText(a)
	p.say(false, "copied %s", a)
}

func (p *tunnelPane) open(m *Model, t core.TunnelInfo) {
	if t.Spec.SOCKS {
		p.say(false, "a SOCKS5 proxy: set %s as the SOCKS proxy of the program", t.LocalAddr())
		return
	}
	url, err := m.svc.OpenTunnel(t.ID)
	if err != nil {
		p.say(true, "%v", err)
		return
	}
	if core.SelfSigned(url) {
		p.warnf("opened %s — the page probably has a self-signed certificate (Proxmox, routers): "+
			"the browser warns; check the certificate before you accept it", url)
		return
	}
	p.say(false, "opened %s", url)
}

// tunnelBadge is the state of a row, 13 columns.
func (m *Model) tunnelBadge(t *core.TunnelInfo) string {
	var s string
	var st lipgloss.Style
	switch {
	case t == nil:
		s, st = "○ stopped", stDim
	case t.State == core.TunnelActive:
		s, st = "● active", stOK
	case t.State == core.TunnelConnecting:
		s, st = "◌ connecting", stWarn
	case t.State == core.TunnelReconnecting:
		s, st = "↻ retrying", stWarn
	case t.State == core.TunnelFailed:
		s, st = "✗ failed", stErr
	default:
		s, st = "○ stopped", stDim
	}
	return st.Render(padRight(m.ascii(s), 13))
}

// shortDur is a duration for the list: 45s, 12m, 3h05m, 2d.
func shortDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}

func (p *tunnelPane) view(m *Model, w, line int) string {
	if p.form != nil {
		return p.form.view(m, p, w, line)
	}
	var b strings.Builder
	p.rowY = p.rowY[:0]
	write := func(s string) {
		b.WriteString(s + "\n")
		line += strings.Count(s, "\n") + 1
	}
	rows := tunnelRows(m)
	p.row = max(0, min(p.row, len(rows)-1))
	arrow := m.g.arrow
	if len(rows) == 0 {
		write(stDim.Render("No tunnels yet: a adds one; Ctrl+T in the jump picker (j) and T on an SSH host start one."))
	}
	for i, r := range rows {
		mark := "  "
		if i == p.row {
			mark = stKey.Render("▸ ")
		}
		var desc, tail string
		if r.info != nil {
			t := r.info
			desc = t.Forward(arrow)
			if t.State == core.TunnelActive {
				tail = shortDur(time.Since(t.Since)) + " · " + strconv.Itoa(t.Open) + "/" + strconv.Itoa(t.Total) + " conn"
			}
			if t.Exposed() {
				desc = stWarn.Render(m.ascii("⚠ ")) + desc
			}
		} else {
			to := r.tpl.Target
			if r.tpl.Kind() == core.TunnelSOCKS {
				to = "SOCKS5"
			}
			desc = ":" + r.tpl.LocalPort.String() + " " + arrow + " " + r.tpl.Jump + " " + arrow + " " + to
		}
		name := r.name()
		if r.saved < 0 {
			name = stDim.Render(trunc(name, 20))
		} else {
			name = trunc(name, 20)
		}
		l := mark + m.tunnelBadge(r.info) + " " + padRight(name, 20) + " " + desc
		if tail != "" {
			l += "  " + stDim.Render(tail)
		}
		p.rowY = append(p.rowY, line)
		write(ansi.Truncate(l, w, m.g.ell))
		if i == p.row && r.info != nil {
			for _, d := range m.tunnelDetails(*r.info) {
				write(ansi.Truncate("    "+d, w, m.g.ell))
			}
		}
	}
	write("")
	if p.err != "" {
		write(stErr.Render(ansi.Wordwrap(p.err, w, " ")))
	} else if p.status != "" {
		st := stDim
		if p.warn {
			st = stWarn
		}
		write(st.Render(ansi.Wordwrap(p.status, w, " ")))
	}
	write(stDim.Render(ansi.Wordwrap("↑/↓ tunnel · Enter start/open · s start/stop · r restart · y copy · o open in the browser · "+
		"a add · e edit · S save · D remove", w, " ")))
	return strings.TrimRight(b.String(), "\n")
}

// tunnelDetails are the lines under the selected running tunnel.
func (m *Model) tunnelDetails(t core.TunnelInfo) []string {
	var out []string
	switch t.State {
	case core.TunnelConnecting:
		out = append(out, stDim.Render("connecting to "+t.Via+m.ascii("…")))
	case core.TunnelReconnecting:
		s := "connection lost"
		if t.Err != nil {
			s = t.Err.Error()
		}
		if !t.Retry.IsZero() {
			s += "; next attempt at " + t.Retry.Format("15:04:05")
		}
		out = append(out, stWarn.Render(s))
	case core.TunnelFailed:
		s := "failed"
		if t.Err != nil {
			s = t.Err.Error()
		}
		out = append(out, stErr.Render(s), stDim.Render("r tries again; so does a new connection to the port"))
	case core.TunnelActive:
		if !t.Spec.SOCKS {
			out = append(out, stDim.Render(t.TunnelURL()))
		} else {
			out = append(out, stDim.Render("SOCKS5 proxy at "+t.LocalAddr()))
		}
	}
	if t.Note != "" {
		out = append(out, stWarn.Render(t.Note))
	}
	if t.Exposed() {
		out = append(out, stWarn.Render(m.ascii("⚠ listens on "+t.Spec.Bind+": other computers can use this tunnel")))
	}
	return out
}

func (p *tunnelPane) mouse(m *Model, x, y int) tea.Cmd {
	if p.form != nil {
		return p.form.mouse(m, x, y)
	}
	for i, ry := range p.rowY {
		if y == ry {
			if i == p.row {
				cmd, _ := p.update(m, tea.KeyMsg{Type: tea.KeyEnter})
				return cmd
			}
			p.row = i
		}
	}
	return nil
}

// tunnelForm adds or edits a tunnel.
type tunnelForm struct {
	edit    int // the saved tunnel edited (-1 = a new one)
	runID   int // a running tunnel replaced by the result (0 = none)
	in      [7]textinput.Model
	field   int
	via     string // resolved already (from the picker) while Jump reads viaText
	viaText string
	suggest int // the free port offered after a busy one
	err     string
	status  string
	rowY    [7]int
}

const (
	tfName = iota
	tfJump
	tfTarget
	tfPort
	tfType
	tfOpen
	tfBind
)

var tunnelFieldLabels = []string{"Name", "Jump host", "Target", "Local port", "Type", "Open", "Bind"}

var tunnelFieldHints = []string{
	"empty: not saved in the config",
	"a jump template or an SSH host of the list",
	"host:port as seen from the jump host, e.g. 192.168.1.10:8006",
	"auto",
	"local | socks",
	"http://{host}:{port}",
	"127.0.0.1",
}

func newTunnelForm(m *Model, edit int, t core.TunnelTemplate, runID int) *tunnelForm {
	f := &tunnelForm{edit: edit, runID: runID}
	vals := []string{t.Name, t.Jump, t.Target, "", t.Type, t.Open, t.Bind}
	if t.LocalPort != 0 {
		vals[tfPort] = strconv.Itoa(int(t.LocalPort))
	}
	for i := range f.in {
		f.in[i] = textinput.New()
		f.in[i].Prompt = ""
		f.in[i].Placeholder = tunnelFieldHints[i]
		f.in[i].CharLimit = 255
		f.in[i].SetValue(vals[i])
	}
	first := tfName
	if edit < 0 && t.Jump == "" {
		first = tfJump
	}
	f.focus(first)
	return f
}

func (f *tunnelForm) focus(i int) {
	f.field = (i + len(f.in)) % len(f.in)
	for j := range f.in {
		if j == f.field {
			f.in[j].Focus()
			f.in[j].CursorEnd()
		} else {
			f.in[j].Blur()
		}
	}
}

func (f *tunnelForm) update(m *Model, p *tunnelPane, k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		p.form = nil
		return nil
	case "tab", "down":
		f.focus(f.field + 1)
		return nil
	case "shift+tab", "up":
		f.focus(f.field - 1)
		return nil
	case "ctrl+s":
		f.submit(m, p)
		return nil
	case "enter":
		if f.field < len(f.in)-1 {
			f.focus(f.field + 1)
			return nil
		}
		f.submit(m, p)
		return nil
	}
	var cmd tea.Cmd
	f.in[f.field], cmd = f.in[f.field].Update(k)
	f.err = ""
	return cmd
}

// submit checks the form, saves a named tunnel and starts it (an edited
// saved tunnel that is not running is only saved).
func (f *tunnelForm) submit(m *Model, p *tunnelPane) {
	v := func(i int) string { return strings.TrimSpace(f.in[i].Value()) }
	t := core.TunnelTemplate{Name: v(tfName), Jump: v(tfJump), Target: v(tfTarget), Open: v(tfOpen), Bind: v(tfBind),
		Type: strings.ToLower(v(tfType))}
	if t.Type == core.TunnelLocal {
		t.Type = ""
	}
	if t.Bind == "127.0.0.1" {
		t.Bind = ""
	}
	port, err := core.ParseLocalPort(v(tfPort))
	if err != nil {
		f.err = err.Error()
		f.focus(tfPort)
		return
	}
	t.LocalPort = port
	chk := t
	if chk.Name == "" {
		chk.Name = "tunnel"
	}
	if err := chk.Check(); err != nil {
		f.err = strings.TrimPrefix(err.Error(), chk.Name+": ")
		switch {
		case strings.HasPrefix(f.err, "target"), strings.Contains(f.err, "no target"):
			f.focus(tfTarget)
		case strings.HasPrefix(f.err, "jump"):
			f.focus(tfJump)
		case strings.HasPrefix(f.err, "type"):
			f.focus(tfType)
		case strings.HasPrefix(f.err, "bind"):
			f.focus(tfBind)
		}
		return
	}
	via := f.via
	if via == "" || t.Jump != f.viaText {
		if via, err = m.svc.ResolveJump(t.Jump); err != nil {
			f.err = err.Error()
			f.focus(tfJump)
			return
		}
	}
	if f.via != "" && t.Jump == f.viaText {
		t.Jump = m.svc.JumpRef(via) // as saved
	}
	runningPort := 0
	if f.runID != 0 {
		for _, r := range m.svc.Tunnels() {
			if r.ID == f.runID {
				runningPort = r.Port
			}
		}
	}
	if port != 0 && int(port) != runningPort {
		if free := core.FreeTunnelPort(t.Bind, int(port)); free != int(port) {
			if f.suggest != free || free == 0 {
				f.suggest = free
				f.in[tfPort].SetValue(strconv.Itoa(free))
				f.focus(tfPort)
				f.err = fmt.Sprintf("port %d is busy: %d is free (Enter starts with it, or type another)", port, free)
				if free == 0 {
					f.err = fmt.Sprintf("port %d is busy and no free port was found", port)
				}
				return
			}
		}
	}
	if t.Name != "" {
		ts := slices.Clone(m.svc.SavedTunnels())
		if f.edit >= 0 && f.edit < len(ts) {
			ts[f.edit] = t
		} else {
			ts = append(ts, t)
		}
		if err := m.svc.SaveTunnels(ts); err != nil {
			f.err = err.Error()
			return
		}
	}
	if f.edit >= 0 && f.runID == 0 {
		p.form = nil
		p.say(false, "%s saved; s starts it", t.Name)
		p.selectName(m, t.Name)
		return
	}
	if f.runID != 0 {
		m.svc.StopTunnel(f.runID) // replaced by the new one
	}
	sp := core.TunnelSpec{Name: t.Name, Via: via, Target: t.Target, SOCKS: t.Kind() == core.TunnelSOCKS,
		Port: int(port), Bind: t.Bind, Open: t.Open}
	info, err := m.svc.StartTunnel(sp)
	if err != nil {
		f.err = err.Error()
		return
	}
	p.form = nil
	p.started(m, info)
	p.selectID(m, info.ID)
}

func (p *tunnelPane) selectName(m *Model, name string) {
	for i, r := range tunnelRows(m) {
		if r.saved >= 0 && strings.EqualFold(r.tpl.Name, name) {
			p.row = i
		}
	}
}

func (p *tunnelPane) selectID(m *Model, id int) {
	for i, r := range tunnelRows(m) {
		if r.info != nil && r.info.ID == id {
			p.row = i
		}
	}
}

func (f *tunnelForm) view(m *Model, p *tunnelPane, w, line int) string {
	var b strings.Builder
	write := func(s string) {
		b.WriteString(s + "\n")
		line += strings.Count(s, "\n") + 1
	}
	title := "New tunnel"
	switch {
	case f.edit >= 0:
		title = "Edit tunnel"
	case f.runID != 0:
		title = "Save tunnel"
	}
	write(stBold.Render(title))
	for i := range f.in {
		mark := "  "
		if i == f.field {
			mark = stKey.Render("▸ ")
		}
		f.rowY[i] = line
		write(mark + padRight(tunnelFieldLabels[i], 12) + f.in[i].View())
	}
	if b := strings.TrimSpace(f.in[tfBind].Value()); b != "" && !core.IsLoopback(b) {
		write(stWarn.Render(ansi.Wordwrap(m.ascii("⚠ Bind "+b+": the tunnel listens for other computers too; "+
			"anyone who reaches this computer can use it to reach the target."), w, " ")))
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(f.in[tfOpen].Value())), "https://") {
		write(stDim.Render(ansi.Wordwrap("https: the page probably has a self-signed certificate (Proxmox): the browser will warn.", w, " ")))
	}
	write("")
	if f.err != "" {
		write(stErr.Render(ansi.Wordwrap(f.err, w, " ")))
	} else if f.status != "" {
		write(stDim.Render(f.status))
	}
	act := "start"
	if f.edit >= 0 && f.runID == 0 {
		act = "save"
	} else if strings.TrimSpace(f.in[tfName].Value()) != "" {
		act = "save and start"
	}
	write(stDim.Render("Enter: next field · Ctrl+S: " + act + " · Tab: next field · Esc: cancel"))
	return strings.TrimRight(b.String(), "\n")
}

func (f *tunnelForm) mouse(m *Model, x, y int) tea.Cmd {
	for i, ry := range f.rowY {
		if y == ry {
			f.focus(i)
		}
	}
	return nil
}

// tunnelsModal is the Tunnels dialog: it stays with the host list (the
// other tabs can be used while it is open).
type tunnelsModal struct{ p tunnelPane }

func (*tunnelsModal) docked() {}

// openTunnels opens the Tunnels dialog over the host list (a dialog left
// waiting there is shown instead); form, if not nil, opens with it.
func (m *Model) openTunnels(form *tunnelForm) {
	if m.parked != nil {
		m.switchTab(0)
		if tm, ok := m.modal.(*tunnelsModal); ok && form != nil {
			tm.p.form = form
		}
		return
	}
	if m.active != 0 && m.modal == nil {
		m.setActive(0)
	}
	if tm, ok := m.modal.(*tunnelsModal); ok {
		if form != nil {
			tm.p.form = form
		}
		return
	}
	if m.modal == nil {
		tm := &tunnelsModal{p: newTunnelPane()}
		tm.p.form = form
		m.modal = tm
	}
}

func (t *tunnelsModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if cmd, handled := t.p.update(m, k); handled {
		return cmd
	}
	switch k.String() {
	case "esc", "q":
		m.modal = nil
	}
	return nil
}

func (t *tunnelsModal) view(m *Model, w, h int) []string {
	width := max(60, min(w-8, 110))
	body := t.p.view(m, width, boxPadY+boxTitleGap)
	return m.box("Tunnels", body, w)
}

func (t *tunnelsModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	return t.p.mouse(m, x-boxPadX, y)
}

// tunnelFromHost opens a new tunnel through the SSH host h (T in the host
// list): to a port of h itself by default.
func (m *Model) tunnelFromHost(h core.Host) {
	if h.Scheme != core.SchemeSSH {
		m.notify(true, "%s is not an SSH host: tunnels go through SSH hosts", h.Name)
		return
	}
	f := newTunnelForm(m, -1, core.TunnelTemplate{Jump: m.svc.JumpRef("h:" + h.ID), Target: "localhost:"}, 0)
	f.via, f.viaText = "h:"+h.ID, f.in[tfJump].Value()
	f.status = "a port of " + h.Name + " itself (localhost:port) or of a host it reaches"
	f.focus(tfTarget)
	m.openTunnels(f)
}

// tunnelFromPicker opens a new tunnel to the target of the jump picker
// (Ctrl+T in step 2): its host, the port to type.
func (m *Model) tunnelFromPicker(via, host string) {
	f := newTunnelForm(m, -1, core.TunnelTemplate{Jump: m.svc.JumpRef(via), Target: host + ":"}, 0)
	f.via, f.viaText = via, f.in[tfJump].Value()
	f.status = "type the port of the target (e.g. 8006 for the Proxmox web interface)"
	f.focus(tfTarget)
	m.openTunnels(f)
}

// tunnelButton opens the Tunnels dialog; shown while tunnels run: ⇄ and
// their number, coloured by the worst state.
func (m *Model) tunnelButton() (string, lipgloss.Style) {
	ts := m.svc.Tunnels()
	if len(ts) == 0 {
		return "", stKey
	}
	st := stKey
	for _, t := range ts {
		switch t.State {
		case core.TunnelFailed:
			st = stErr
		case core.TunnelReconnecting, core.TunnelConnecting:
			if st.GetForeground() != stErr.GetForeground() {
				st = stWarn
			}
		}
	}
	return " " + m.ascii("⇄") + strconv.Itoa(len(ts)) + " ", st
}

// tunnelMark is a tunnel as last seen (for the notices).
type tunnelMark struct {
	state    core.TunnelState
	connects int
}

// tunnelSeen records the state of a tunnel.
func (m *Model) tunnelSeen(t core.TunnelInfo) {
	if m.tunState == nil {
		m.tunState = map[int]tunnelMark{}
	}
	m.tunState[t.ID] = tunnelMark{t.State, t.Connects}
}

// tunnelNotices tells about the changes of the tunnels in the status bar:
// a lost connection, a failure, connected again (also when that was too
// quick to be seen lost).
func (m *Model) tunnelNotices() {
	ts := m.svc.Tunnels()
	seen := map[int]bool{}
	for _, t := range ts {
		seen[t.ID] = true
		old, known := m.tunState[t.ID]
		m.tunnelSeen(t)
		if !known {
			if t.Note != "" {
				m.notify(true, "tunnel %s: %s", t.Label(), t.Note)
			}
			continue
		}
		switch {
		case t.State == core.TunnelActive && t.Connects > old.connects && t.Connects > 1:
			m.notify(false, "tunnel %s connected again", t.Label())
		case t.State == core.TunnelActive && old.state == core.TunnelConnecting && t.Connects == 1:
			m.notify(false, "tunnel %s: %s", t.Label(), t.LocalAddr())
		case old.state == t.State:
		case t.State == core.TunnelReconnecting && old.state == core.TunnelActive:
			m.notify(true, "tunnel %s: connection lost, reconnecting", t.Label())
		case t.State == core.TunnelFailed:
			why := ""
			if t.Err != nil {
				why = ": " + t.Err.Error()
			}
			m.notify(true, "tunnel %s failed%s (Tunnels: r retries)", t.Label(), why)
		}
	}
	for id := range m.tunState {
		if !seen[id] {
			delete(m.tunState, id)
		}
	}
}

// tunnelSettingsNote describes tunnel: of the config on the settings page.
func tunnelSettingsNote(m *Model) string {
	c := m.svc.Config().Tunnel
	if c.Attempts == 0 {
		c.Attempts = core.DefaultTunnelAttempts
	}
	br := "the system default"
	if len(c.Browser) > 0 {
		br = strings.Join(c.Browser, " ")
	}
	return stDim.Render(fmt.Sprintf("Browser: %s · reconnection attempts: %d (tunnel: in config.yaml). "+
		"Tunnels listen on 127.0.0.1 unless Bind says otherwise; they end with Vaultcrab.", br, c.Attempts))
}
