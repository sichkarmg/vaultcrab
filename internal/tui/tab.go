// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// tab is a session tab: a layout of panes (see internal/workspace).
type tab struct {
	ws      *core.Tab
	history map[core.PaneID]bool // panes whose keyboard drives the scrollback
	rect    rect                 // last body rect the tab was laid out in
	drag    *core.LayoutNode     // separator being dragged with the mouse
	sel     *selDrag             // mouse selection in progress
	ksel    map[core.PaneID]*kbSel

	name string // set by the user (rename_tab); "" = the host of the focused pane

	seen      uint64    // output of the tab's sessions when it was last shown
	resizedAt time.Time // shells redraw after a resize: not activity
}

func newTab(s *core.Session) *tab {
	return &tab{ws: core.NewTab(s), history: map[core.PaneID]bool{}, ksel: map[core.PaneID]*kbSel{}}
}

func (t *tab) focused() *core.Session { return t.ws.FocusedSession() }

func (t *tab) title(g glyphs) string {
	if t.name != "" {
		return t.name
	}
	title := sessionTitle(t.focused(), g)
	if n := t.ws.Len(); n > 1 {
		title += fmt.Sprintf(" +%d", n-1)
	}
	return title
}

// sessionTitle names a session: its host, "↪ jump → target" through a
// jump host.
func sessionTitle(s *core.Session, g glyphs) string {
	if core.IsJump(s.Host) {
		return g.jump + " " + s.Host.Fields[core.FieldVia] + " " + g.arrow + " " + s.Host.Name
	}
	return s.Host.Name
}

func (t *tab) zoomed() bool { return t.ws.Zoomed() }

func (m *Model) stateIcon(st core.SessionState) string {
	switch st {
	case core.SessionConnected:
		return stOK.Render(m.opt.Icons.Get(core.IconConnected))
	case core.SessionConnecting:
		return stWarn.Render(m.opt.Icons.Get(core.IconConnecting))
	case core.SessionDisconnected:
		return stErr.Render(m.opt.Icons.Get(core.IconDisconnected))
	}
	return m.opt.Icons.Get(core.IconIdle)
}

// icon is the tab's aggregated state icon (✗ if any pane is broken); a
// connected tab with output it has not shown yet gets the icon in blue.
func (t *tab) icon(m *Model, shown bool) string {
	st := t.ws.State()
	if t.activity(shown) && st == core.SessionConnected {
		return stActivity.Render(m.opt.Icons.Get(core.IconConnected))
	}
	return m.stateIcon(st)
}

// stateColor is the color of the tab's aggregated state (Settings → Tab
// status: color): green connected, blue connected with unseen output,
// yellow connecting, red broken; ok is false for idle tabs.
func (t *tab) stateColor(shown bool) (lipgloss.TerminalColor, bool) {
	st := t.ws.State()
	switch {
	case st == core.SessionConnected && t.activity(shown):
		return stActivity.GetForeground(), true
	case st == core.SessionConnected:
		return stOK.GetForeground(), true
	case st == core.SessionConnecting:
		return stWarn.GetForeground(), true
	case st == core.SessionDisconnected:
		return stErr.GetForeground(), true
	}
	return nil, false
}

// output sums the output of the tab's sessions.
func (t *tab) output() uint64 {
	var n uint64
	for _, s := range t.ws.Sessions() {
		n += s.Term.Output()
	}
	return n
}

// activity reports new output since the tab was last shown (shown: it is
// on screen now, which marks its output as seen). Output right after a
// resize does not count: shells redraw their prompt then.
func (t *tab) activity(shown bool) bool {
	n := t.output()
	if shown || time.Since(t.resizedAt) < activityQuiet {
		t.seen = n
	}
	return n != t.seen
}

// activityQuiet: output this long after a resize is not activity.
const activityQuiet = time.Second

func (t *tab) liveSessions() int { return t.ws.Live() }

func (t *tab) resize(r rect) {
	if t.rect != r {
		t.resizedAt = time.Now()
	}
	t.rect = r
	t.ws.SetSize(r.w, r.h)
}

func (t *tab) close(m *Model) {
	for _, s := range t.ws.Sessions() {
		m.svc.CloseSession(s)
	}
}

func (t *tab) sendKey(k core.Key) {
	s := t.focused()
	s.Term.ScrollToBottom()
	s.Term.SendKey(k)
}

func (t *tab) reconnect() {
	s := t.focused()
	s.Term.ScrollToBottom()
	s.Reconnect()
}

func (t *tab) termHeight() int {
	if r, ok := t.ws.TermRect(t.ws.Focused()); ok {
		return r.H
	}
	return t.rect.h
}

func (t *tab) enterHistory(m *Model, page bool) {
	s := t.focused()
	if s.Term.AltScreen() {
		m.notify(false, "no output scrolling while a full-screen program is running")
		return
	}
	t.history[t.ws.Focused()] = true
	if page {
		s.Term.Scroll(max(1, t.termHeight()-1))
	}
}

var sides = map[string]core.Side{
	core.ActFocusLeft: core.SideLeft, core.ActFocusRight: core.SideRight,
	core.ActFocusUp: core.SideUp, core.ActFocusDown: core.SideDown,
	core.ActGrowLeft: core.SideLeft, core.ActGrowRight: core.SideRight,
	core.ActGrowUp: core.SideUp, core.ActGrowDown: core.SideDown,
}

// paneAction handles window (pane) actions after the prefix.
func (t *tab) paneAction(m *Model, action string, msg tea.KeyMsg) tea.Cmd {
	switch action {
	case core.ActSplitRight, core.ActSplitDown:
		dir := core.SplitRight
		if action == core.ActSplitDown {
			dir = core.SplitDown
		}
		m.modal = newPicker(m, t.focused().Host.ID, func(m *Model, h core.Host) tea.Cmd {
			return m.splitWith(t, h, dir)
		})
	case core.ActClosePane:
		m.confirmClosePane(t)
	case core.ActCloneTab:
		// a new tab with a new connection to the host of the focused pane
		return m.openHost(t.focused().Host)
	case core.ActZoom:
		if t.ws.Len() > 1 {
			t.ws.ToggleZoom()
		}
	case core.ActFocusLeft, core.ActFocusRight, core.ActFocusUp, core.ActFocusDown:
		t.ws.FocusDir(sides[action])
	case core.ActGrowLeft, core.ActGrowRight, core.ActGrowUp, core.ActGrowDown:
		d := 2
		if action == core.ActGrowLeft || action == core.ActGrowRight {
			d = 4
		}
		t.ws.ResizeFocused(sides[action], d)
	case core.ActBreakPane:
		if t.ws.Len() < 2 {
			m.notify(false, "only one pane in this tab")
			return nil
		}
		id := t.ws.Focused()
		s, _ := t.ws.Take(id)
		delete(t.history, id)
		m.tabs = append(m.tabs, newTab(s))
		m.setActive(len(m.tabs))
	}
	return nil
}

// splitWith opens h in a new pane of t.
func (m *Model) splitWith(t *tab, h core.Host, dir core.SplitDir) tea.Cmd {
	s, cmd := m.openSession(h, max(1, t.rect.w/2), max(1, t.rect.h/2))
	if s == nil {
		return cmd
	}
	t.ws.Split(dir, s)
	for i, x := range m.tabs {
		if x == t {
			m.setActive(i + 1)
		}
	}
	return nil
}

func (m *Model) confirmClosePane(t *tab) {
	s := t.focused()
	closePane := func(m *Model) tea.Cmd {
		id := t.ws.Focused()
		if t.ws.Len() == 1 {
			m.closeTab(t)
			return nil
		}
		t.ws.Take(id)
		delete(t.history, id)
		m.svc.CloseSession(s)
		return nil
	}
	if st, _ := s.State(); st != core.SessionConnected && st != core.SessionConnecting {
		closePane(m)
		return
	}
	m.modal = newConfirm(fmt.Sprintf("Close pane %q?", s.Host.Name), closePane)
}

func (t *tab) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	id := t.ws.Focused()
	s := t.focused()
	term := s.Term
	if t.history[id] {
		if t.historySelectKey(m, id, s, msg.String()) {
			return nil
		}
		h := max(1, t.termHeight()-1)
		switch msg.String() {
		case "pgup", "ctrl+b":
			term.Scroll(h)
		case "pgdown", "ctrl+f", " ":
			term.Scroll(-h)
		case "up", "k":
			term.Scroll(1)
		case "down", "j":
			term.Scroll(-1)
		case "home", "g":
			term.Scroll(1 << 30)
		case "end", "G":
			term.ScrollToBottom()
		case "esc", "q", "enter":
			delete(t.history, id)
			term.ScrollToBottom()
		}
		return nil
	}
	term.ClearSelection() // typing ends the highlight (the text stays copied)
	switch msg.String() {
	case "shift+up":
		term.Scroll(1)
		return nil
	case "shift+down":
		term.Scroll(-1)
		return nil
	}
	if st, _ := s.State(); st == core.SessionDisconnected || st == core.SessionIdle {
		switch msg.Type {
		case tea.KeyEnter:
			t.reconnect()
		case tea.KeyPgUp:
			t.enterHistory(m, true)
		case tea.KeyCtrlD: // as EOF in a shell: close this pane (the tab if it is the last)
			m.confirmClosePane(t)
		case tea.KeyRunes:
			if msg.String() == "y" {
				m.copyDebug(s)
			}
		}
		return nil
	}
	if msg.Paste {
		m.pasteInto(s, string(msg.Runes)) // asks first if there are several lines
		return nil
	}
	if k, ok := TermKey(msg); ok {
		t.sendKey(k)
	}
	return nil
}

// mouse handles events inside the tab body whose origin is (x0, y0).
func (t *tab) mouse(m *Model, msg tea.MouseMsg, x0, y0 int) tea.Cmd {
	x, y := msg.X-x0, msg.Y-y0
	g := t.ws.Geometry()
	if t.sel != nil {
		t.dragSelection(m, msg, x, y)
		return nil
	}
	if t.drag != nil {
		switch msg.Action {
		case tea.MouseActionMotion:
			pos := x
			if t.drag.Dir == core.SplitDown {
				pos = y
			}
			t.ws.DragBorder(t.drag, pos)
		case tea.MouseActionRelease:
			t.drag = nil
		}
		return nil
	}
	hit := g.HitTest(x, y)
	if hit.Border != nil {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			t.drag = hit.Border
		}
		return nil
	}
	if hit.Pane == 0 {
		return nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		t.ws.Focus(hit.Pane)
		if t.closeButtonAt(m, hit.Pane, x, y) {
			m.confirmClosePane(t) // asks only for a live session
			return nil
		}
	}
	s := t.ws.Session(hit.Pane)
	tr, ok := t.ws.TermRect(hit.Pane)
	if !ok || s == nil {
		return nil
	}
	// Shift bypasses the remote application's mouse mode (for selecting text
	// in mc or htop), as in most terminals.
	if m.opt.ForwardMouse && !t.history[hit.Pane] && !msg.Shift && tr.Contains(x, y) {
		if ev, ok := TermMouse(msg, x0+tr.X, y0+tr.Y); ok && s.Term.SendMouse(ev) {
			return nil
		}
	}
	if msg.Action == tea.MouseActionPress && tr.Contains(x, y) {
		t.selectionMouse(m, msg, hit.Pane, s, x-tr.X, y-tr.Y)
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if !s.Term.AltScreen() {
			s.Term.Scroll(3)
		}
	case tea.MouseButtonWheelDown:
		s.Term.Scroll(-3)
		if s.Term.ScrollOffset() == 0 {
			delete(t.history, hit.Pane)
			delete(t.ksel, hit.Pane)
		}
	}
	return nil
}

// view composes the panes and separators of the tab.
func (t *tab) view(m *Model, r rect) []string {
	if t.rect != r {
		t.resize(r)
	}
	g := t.ws.Geometry()
	focus := t.ws.Focused()
	fr := g.Panes[focus]

	type seg struct {
		lines []string // per row of r
		r     core.Rect
	}
	var segs []seg
	for id, pr := range g.Panes {
		segs = append(segs, seg{r: pr, lines: t.paneLines(m, id, pr, id == focus)})
	}
	for _, b := range g.Borders {
		br := b.Rect
		lines := make([]string, br.H)
		for i := range lines {
			if br.W == 1 { // vertical separator
				st := stBorder
				if t.ws.Len() > 1 && adjacent(fr, br.X, br.Y+i) {
					st = stBorderFocus
				}
				lines[i] = st.Render(m.g.v)
				continue
			}
			var sb strings.Builder
			for x := 0; x < br.W; x++ {
				st := stBorder
				if adjacent(fr, br.X+x, br.Y) {
					st = stBorderFocus
				}
				sb.WriteString(st.Render(m.g.h))
			}
			lines[i] = sb.String()
		}
		segs = append(segs, seg{r: br, lines: lines})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].r.X < segs[j].r.X })
	out := make([]string, r.h)
	for y := 0; y < r.h; y++ {
		var b strings.Builder
		for _, s := range segs {
			if y < s.r.Y || y >= s.r.Y+s.r.H {
				continue
			}
			b.WriteString(s.lines[y-s.r.Y])
		}
		out[y] = b.String()
	}
	return out
}

// adjacent reports whether cell (x, y) touches rect r from outside.
func adjacent(r core.Rect, x, y int) bool {
	inX := x >= r.X-1 && x <= r.X+r.W
	inY := y >= r.Y-1 && y <= r.Y+r.H
	return inX && inY && !r.Contains(x, y)
}

// paneLines renders one pane: optional header + terminal, exactly pr.W×pr.H.
func (t *tab) paneLines(m *Model, id core.PaneID, pr core.Rect, focused bool) []string {
	s := t.ws.Session(id)
	out := make([]string, 0, pr.H)
	if t.ws.Headers() && pr.H > 1 {
		st, err := s.State()
		title := " " + sessionTitle(s, m.g) + " "
		info := s.Host.Target()
		if err != nil && st == core.SessionDisconnected {
			info = err.Error()
		}
		if off := s.Term.ScrollOffset(); off > 0 {
			info = fmt.Sprintf("%s %d", m.opt.Icons.Get(core.IconHistory), off) + "  " + info
		}
		head := m.sessionIcon(s) + " "
		if focused {
			head += stTabActive.Render(title) + " " + stDim.Render(info)
		} else {
			head += stDim.Render(title + " " + info)
		}
		btn := m.paneCloseButton()
		if bw := ansi.StringWidth(btn); pr.W > bw+4 {
			head = padRight(ansi.Truncate(head, pr.W-bw, m.g.ell), pr.W-bw) + stQuit.Render(btn)
		}
		out = append(out, padRight(ansi.Truncate(head, pr.W, m.g.ell), pr.W))
	}
	tr, _ := t.ws.TermRect(id)
	lines := s.Term.Render(pr.W, tr.H, core.RenderOpts{Cursor: focused && !t.history[id]})
	return append(out, lines...)
}

// paneCloseButton is drawn at the right end of each pane header.
func (m *Model) paneCloseButton() string { return " " + m.ascii("✕") + " " }

// closeButtonAt reports whether (x, y) of the tab body is the close button
// in the header of pane id.
func (t *tab) closeButtonAt(m *Model, id core.PaneID, x, y int) bool {
	pr, ok := t.ws.Geometry().Panes[id]
	bw := ansi.StringWidth(m.paneCloseButton())
	return ok && t.ws.Headers() && pr.H > 1 && pr.W > bw+4 && y == pr.Y && x >= pr.X+pr.W-bw && x < pr.X+pr.W
}

// copyDebug copies the steps of the last connection attempt of s.
func (m *Model) copyDebug(s *core.Session) {
	d := s.Debug()
	if d == "" {
		m.notify(true, "no connection debug: Settings → Behavior → Connection debug (ssh.debug)")
		return
	}
	m.copyText(d + "\n")
}

// debugButton is the status bar button that copies the connection debug of
// a failed pane ("" when there is none).
func (m *Model) debugButton() string {
	t := m.curTab()
	if t == nil {
		return ""
	}
	s := t.focused()
	if st, _ := s.State(); st != core.SessionDisconnected || s.Debug() == "" {
		return ""
	}
	return " [Copy debug] "
}

func (t *tab) status(m *Model) string {
	id := t.ws.Focused()
	s := t.focused()
	st, err := s.State()
	out := s.Host.Target() + " · " + st.String()
	switch {
	case s.Exited() && s.Host.Scheme == core.SchemeLocal:
		out = s.Host.Fields["command"] + " · finished · Enter: run again · Ctrl+D: close"
	case st == core.SessionDisconnected && s.Host.Scheme == core.SchemeLocal:
		out += ": " + errText(err) + " · Enter: run again · Ctrl+D: close"
	case st == core.SessionDisconnected && s.Debug() != "":
		out += ": " + errText(err) + " · Enter: reconnect · y: copy debug · Ctrl+D: close"
	case st == core.SessionDisconnected:
		out += ": " + errText(err) + " · Enter: reconnect · Ctrl+D: close"
	case st == core.SessionIdle:
		out += " · not connected · Enter: connect · Ctrl+D: close"
	case err != nil:
		out += ": " + err.Error()
	}
	if n := t.ws.Len(); n > 1 {
		for i, p := range t.ws.Panes() {
			if p == id {
				out += fmt.Sprintf(" · pane %d/%d", i+1, n)
			}
		}
		if t.ws.Zoomed() {
			out += " " + m.opt.Icons.Get(core.IconZoom)
		}
	}
	if off := s.Term.ScrollOffset(); off > 0 {
		out += fmt.Sprintf(" · %s %d/%d", m.opt.Icons.Get(core.IconHistory), off, s.Term.ScrollbackLen())
	}
	if t.ksel[id] != nil {
		out += " · SELECT: Up/Down/PgUp/PgDn extend, y/Enter copy, Esc cancel"
	} else if t.history[id] {
		out += " · SCROLLING OUTPUT: PgUp/PgDn Up/Down Home/End, v select, Esc exits"
	} else {
		out += " · " + m.opt.Prefix + " ? help"
	}
	return out
}

// mouse routes a mouse event to the tab bar, host list or current tab.
func (m *Model) mouse(msg tea.MouseMsg) tea.Cmd {
	if t := m.curTab(); t != nil && (t.drag != nil || t.sel != nil) {
		r := m.bodyRect()
		return t.mouse(m, msg, r.x, r.y) // keep dragging even over the bars
	}
	if msg.Y == m.tabBarY() {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.X >= m.tabsWidth() {
			switch m.buttonAt(msg.X) {
			case "log":
				m.modal = newLogView(m)
			case "settings":
				m.openSettings()
			case "quit":
				return m.confirmQuit() // asks if sessions are open
			}
			return nil
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if i := m.tabAt(msg.X); i >= 0 {
				m.switchTab(i)
			}
		}
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonMiddle {
			if i := m.tabAt(msg.X); i > 0 {
				m.confirmCloseTab(m.tabs[i-1])
			}
		}
		return nil
	}
	if msg.Y == m.statusY() {
		// [Copy debug] at the right end of the status bar
		if b := m.debugButton(); b != "" && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft &&
			msg.X >= m.w-ansi.StringWidth(b) {
			m.copyDebug(m.curTab().focused())
		}
		return nil
	}
	r := m.bodyRect()
	x, y := msg.X-r.x, msg.Y-r.y
	if y < 0 || y >= r.h {
		return nil
	}
	if m.active == 0 {
		return m.list.mouse(msg, x, y)
	}
	return m.curTab().mouse(m, msg, r.x, r.y)
}
