// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// InfoItem is a line of the About page of the settings (built by main).
type InfoItem struct{ Label, Value string }

// settingsButton opens the settings; it is left of the close button.
func (m *Model) settingsButton() string { return " " + m.opt.Icons.Get(core.IconSettings) + " " }

// setAccent colors keys, focused pane borders and folder names.
func setAccent(color string) {
	if color == "" {
		return
	}
	c := lipgloss.Color(color)
	stKey = stKey.Foreground(c)
	stBorderFocus = stBorderFocus.Foreground(c)
	stGroup = stGroup.Foreground(c)
}

// setSelection colors what is selected or active: the host list cursor,
// the active tab, filter and pane title, focused buttons, chosen options
// and the status bar.
func setSelection(name string) {
	bg, fg, ok := core.SelectionColors(name)
	if !ok {
		return
	}
	sel := lipgloss.NewStyle().Reverse(true)
	stStatusErr = lipgloss.NewStyle().Reverse(true).Foreground(lipgloss.Color("1"))
	if bg != "" {
		sel = lipgloss.NewStyle().Background(lipgloss.Color(bg)).Foreground(lipgloss.Color(fg))
		errBg := "1" // errors: white on red (yellow if the selection is red)
		if bg == "1" {
			errBg = "3"
		}
		stStatusErr = lipgloss.NewStyle().Background(lipgloss.Color(errBg)).Foreground(lipgloss.Color("15")).Bold(true)
	}
	stSelected = sel
	stTabActive = sel.Bold(true)
	stStatus = sel
	stButton = sel.Bold(true)
}

// securityNotes explains the Security page and warns about weak choices.
func securityNotes(m *Model, w int) string {
	a := m.opt.Appearance
	p := m.svc.Policy()
	var b strings.Builder
	wrap := func(st lipgloss.Style, text string) {
		b.WriteString(st.Render(ansi.Wordwrap(text, w, " ")) + "\n")
	}
	b.WriteString("\n")
	wrap(stDim, "The vaults are locked after this long without keyboard or mouse input in Vaultcrab; "+
		"time asleep counts. A sleep of the computer by itself does not lock.")
	if t := p.Timeout; t == 0 || t > core.LongLockTimeout {
		what := "for " + core.LockTimeoutLabel(a.LockTimeout)
		if t == 0 {
			what = "until you lock it or quit"
		}
		wrap(stWarn, "! The vault stays unlocked "+what+". While it is unlocked, bw serve answers on "+
			"127.0.0.1 without authentication: any program of your user account can read every password.")
	}
	if a.LockOnScreenLock && p.ScreenLockErr != nil {
		wrap(stWarn, "! Screen locks are not detected here: "+p.ScreenLockErr.Error())
	}
	return b.String()
}

// applyAppearance changes the look of the running UI.
func (m *Model) applyAppearance(a core.Appearance) {
	if a.LockTimeout != m.opt.Appearance.LockTimeout || a.LockOnScreenLock != m.opt.Appearance.LockOnScreenLock {
		m.svc.SetLockPolicy(core.Unlock{LockTimeout: a.LockTimeout, LockOnScreenLock: a.LockOnScreenLock})
	}
	if a.Log != m.opt.Appearance.Log && a.Log != m.svc.LogEnabled() {
		if err := m.svc.SetLogging(a.Log); err != nil {
			m.notify(true, "event log: %v", err)
		}
	}
	m.opt.Appearance = a
	m.opt.TabsBottom = a.TabsBottom
	m.opt.Icons = m.opt.Icons.With(a.IconMode, core.IconSSH, a.SSHIcon)
	m.g = unicodeGlyphs
	if m.opt.Icons.ASCII() {
		m.g = asciiGlyphs
	}
	setAccent(core.AccentColor(a.Accent))
	setSelection(a.Selection)
	if m.opt.ShowTree != a.ShowTree {
		m.opt.ShowTree = a.ShowTree
		m.list.grouping = core.ByFolder
		if !a.ShowTree {
			m.list.grouping = core.Flat
		}
	}
	if w := a.Widgets && len(m.svc.Widgets()) > 0; w != m.opt.Widgets {
		m.opt.Widgets, m.list.widgets = w, w
	}
	m.opt.ListWidth = a.ListWidth
	if m.opt.ShowNotes = a.ShowNotes; !a.ShowNotes {
		m.notes = noteState{} // forget the notes shown
	}
	m.opt.ConfirmPaste, m.opt.RestoreTabs, m.opt.RestoreView = a.ConfirmPaste, a.RestoreTabs, a.RestoreView
	m.resizeSessions()
	m.list.rebuild()
}

// settingsModal shows the appearance and behavior settings (changed right
// away and saved to the config file) and information about the running
// vaultcrab.
type settingsModal struct {
	page   int // pageAppearance, pageBehavior or pageAbout
	row    int
	status string // where the last change was saved
	err    string

	// geometry of the last view (dialog coordinates) for the mouse
	pageSpans [][2]int
	rowY      []int
	optSpans  [][][2]int
	optX      int

	prog *programsPage // the External programs page
	vp   vaultsPage    // the Vaults page
}

const (
	pageAppearance = iota
	pageBehavior
	pageSecurity
	pagePrograms
	pageVaults
	pageAbout
)

var settingsPages = []string{"Appearance", "Behavior", "Security", "External programs", "Vaults", "About"}

// newSettings opens the settings; the External programs page keeps its
// draft between openings.
func newSettings(m *Model) *settingsModal {
	if m.programs == nil {
		m.programs = &programsPage{}
	}
	return &settingsModal{prog: m.programs, page: m.settingsPage}
}

// setting is a row of the Appearance or Behavior page: a choice among
// options.
type setting struct {
	label string
	opts  []string // as shown
	cur   int
	set   func(a *core.Appearance, i int)
}

var (
	listWidths = []int{0, 30, 40, 50, 60, 70, 80, 100}
	tabWidths  = []int{8, 12, 16, 20, 24, 32, 40, 60}
)

func onOff(v bool) int {
	if v {
		return 0
	}
	return 1
}

// settings returns the rows of the current page (none on About).
func (s *settingsModal) settings(m *Model) []setting {
	a := m.opt.Appearance
	switch s.page {
	case pageAbout, pagePrograms, pageVaults:
		return nil
	case pageSecurity:
		var labels []string
		cur := -1
		vals := []string{}
		for i, t := range core.LockTimeouts {
			labels, vals = append(labels, t.Label), append(vals, t.Value)
			if t.Value == a.LockTimeout {
				cur = i
			}
		}
		if cur < 0 { // another duration from the config file
			labels, vals = append(labels, core.LockTimeoutLabel(a.LockTimeout)), append(vals, a.LockTimeout)
			cur = len(vals) - 1
		}
		return []setting{
			{"Lock after no input for", labels, cur,
				func(a *core.Appearance, i int) { a.LockTimeout = vals[i] }},
			{"Lock with the screen", []string{"on", "off"}, onOff(a.LockOnScreenLock),
				func(a *core.Appearance, i int) { a.LockOnScreenLock = i == 0 }},
		}
	case pageBehavior:
		return []setting{
			{"Confirm multi-line paste", []string{"on", "off"}, onOff(a.ConfirmPaste),
				func(a *core.Appearance, i int) { a.ConfirmPaste = i == 0 }},
			{"Reopen tabs on start", []string{"on", "off"}, onOff(a.RestoreTabs),
				func(a *core.Appearance, i int) { a.RestoreTabs = i == 0 }},
			{"Keep list filters", []string{"on", "off"}, onOff(a.RestoreView),
				func(a *core.Appearance, i int) { a.RestoreView = i == 0 }},
			{"Event log", []string{"on", "off"}, onOff(a.Log),
				func(a *core.Appearance, i int) { a.Log = i == 0 }},
		}
	}
	var icons []string
	cur := -1
	for i, ic := range core.SSHIcons {
		icons = append(icons, m.opt.Icons.Pick(ic.Unicode, ic.ASCII))
		if ic == a.SSHIcon {
			cur = i
		}
	}
	sshIcons := slices.Clone(core.SSHIcons)
	if cur < 0 { // a custom icon from the config
		sshIcons = append(sshIcons, a.SSHIcon)
		icons = append(icons, m.opt.Icons.Pick(a.SSHIcon.Unicode, a.SSHIcon.ASCII))
		cur = len(sshIcons) - 1
	}
	var accents []string
	acc := 0
	for i, c := range core.Accents {
		accents = append(accents, lipgloss.NewStyle().Foreground(lipgloss.Color(c.Color)).Render(c.Name))
		if c.Name == a.Accent {
			acc = i
		}
	}
	var sels []string
	sel := 0
	for i, c := range core.Selections {
		st := lipgloss.NewStyle().Reverse(true)
		if c.Bg != "" {
			st = lipgloss.NewStyle().Background(lipgloss.Color(c.Bg)).Foreground(lipgloss.Color(c.Fg))
		}
		sels = append(sels, st.Render(c.Name))
		if c.Name == a.Selection {
			sel = i
		}
	}
	widths := slices.Clone(listWidths)
	if !slices.Contains(widths, a.ListWidth) {
		widths = append(widths, a.ListWidth)
		slices.Sort(widths)
	}
	var wl []string
	for _, w := range widths {
		if w == 0 {
			wl = append(wl, "auto")
		} else {
			wl = append(wl, strconv.Itoa(w))
		}
	}
	tws := slices.Clone(tabWidths)
	if !slices.Contains(tws, a.TabWidth) {
		tws = append(tws, a.TabWidth)
		slices.Sort(tws)
	}
	var twl []string
	for _, w := range tws {
		twl = append(twl, strconv.Itoa(w))
	}
	modes := []string{"auto", "unicode", "ascii"}
	tabStatuses := []string{"icon", "color"}
	return []setting{
		{"Tabs", []string{"top", "bottom"}, map[bool]int{false: 0, true: 1}[a.TabsBottom],
			func(a *core.Appearance, i int) { a.TabsBottom = i == 1 }},
		{"Icons", modes, max(0, slices.Index(modes, a.IconMode)),
			func(a *core.Appearance, i int) { a.IconMode = modes[i] }},
		{"SSH item icon", icons, cur,
			func(a *core.Appearance, i int) { a.SSHIcon = sshIcons[i] }},
		{"Accent color", accents, acc,
			func(a *core.Appearance, i int) { a.Accent = core.Accents[i].Name }},
		{"Selection color", sels, sel,
			func(a *core.Appearance, i int) { a.Selection = core.Selections[i].Name }},
		{"Folder tree", []string{"on", "off"}, onOff(a.ShowTree),
			func(a *core.Appearance, i int) { a.ShowTree = i == 0 }},
		{"Notes in the card", []string{"on", "off"}, onOff(a.ShowNotes),
			func(a *core.Appearance, i int) { a.ShowNotes = i == 0 }},
		{"Widget column", []string{"on", "off"}, onOff(a.Widgets),
			func(a *core.Appearance, i int) { a.Widgets = i == 0 }},
		{"Host list width", wl, slices.Index(widths, a.ListWidth),
			func(a *core.Appearance, i int) { a.ListWidth = widths[i] }},
		{"Tab width", twl, slices.Index(tws, a.TabWidth),
			func(a *core.Appearance, i int) { a.TabWidth = tws[i] }},
		{"Tab status", tabStatuses, max(0, slices.Index(tabStatuses, a.TabStatus)),
			func(a *core.Appearance, i int) { a.TabStatus = tabStatuses[i] }},
	}
}

// change selects option i of row r, applies it and saves it.
func (s *settingsModal) change(m *Model, r, i int) {
	rows := s.settings(m)
	if r < 0 || r >= len(rows) || i < 0 || i >= len(rows[r].opts) || i == rows[r].cur {
		return
	}
	old := m.opt.Appearance
	a := old
	rows[r].set(&a, i)
	m.applyAppearance(a)
	s.status, s.err = "", ""
	if m.opt.ConfigPath == "" {
		s.status = "applied (no config file: not saved)"
		return
	}
	if err := core.SaveAppearance(m.opt.ConfigPath, old, a); err != nil {
		s.err = "not saved: " + err.Error()
		return
	}
	s.status = "saved to " + filepath.Base(m.opt.ConfigPath) + " (the path: About)"
}

func (s *settingsModal) update(m *Model, msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if s.page == pagePrograms {
		if cmd, handled := s.prog.update(m, k); handled {
			return cmd
		}
	}
	if s.page == pageVaults {
		if cmd, handled := s.vp.update(m, k); handled {
			return cmd
		}
	}
	rows := s.settings(m)
	n := len(rows)
	switch k.String() {
	case "esc", "q", "enter", ",":
		m.modal = nil
	case "tab":
		s.page, s.row = (s.page+1)%len(settingsPages), 0
		m.settingsPage = s.page
	case "shift+tab":
		s.page, s.row = (s.page+len(settingsPages)-1)%len(settingsPages), 0
		m.settingsPage = s.page
	case "up", "k":
		if n > 0 {
			s.row = (s.row + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			s.row = (s.row + 1) % n
		}
	case "left", "h", "right", "l", " ":
		if n == 0 {
			return nil
		}
		r := rows[s.row]
		d := 1
		if k.String() == "left" || k.String() == "h" {
			d = -1
		}
		s.change(m, s.row, (r.cur+d+len(r.opts))%len(r.opts))
	}
	return nil
}

func (s *settingsModal) mouse(m *Model, msg tea.MouseMsg, x, y int) tea.Cmd {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil
	}
	x -= boxPadX
	if y == boxPadY+boxTitleGap {
		if i := spanAt(s.pageSpans, x); i >= 0 && i != s.page {
			s.page, s.row = i, 0
			m.settingsPage = i
		}
		return nil
	}
	switch s.page {
	case pageAbout:
		return nil
	case pagePrograms:
		return s.prog.mouse(m, x, y)
	case pageVaults:
		return s.vp.mouse(m, x, y)
	}
	for r, ry := range s.rowY {
		if y == ry {
			s.row = r
			if i := spanAt(s.optSpans[r], x-s.optX); i >= 0 {
				s.change(m, r, i)
			}
		}
	}
	return nil
}

func (s *settingsModal) view(m *Model, w, h int) []string {
	var b strings.Builder
	// page tabs
	s.pageSpans = s.pageSpans[:0]
	x := 0
	for i, p := range settingsPages {
		l := " " + p + " "
		if i == s.page {
			b.WriteString(stTabActive.Render(l))
		} else {
			b.WriteString(stDim.Render(l))
		}
		b.WriteString(" ")
		s.pageSpans = append(s.pageSpans, [2]int{x, x + len(l)})
		x += len(l) + 1
	}
	b.WriteString("\n\n")
	line := boxPadY + boxTitleGap + 2 // dialog row of the next line
	switch s.page {
	case pagePrograms:
		b.WriteString(s.prog.view(m, max(40, w-4-2*boxPadX), line))
	case pageVaults:
		b.WriteString(s.vp.view(m, max(40, w-4-2*boxPadX), line))
	case pageAppearance, pageBehavior, pageSecurity:
		rows := s.settings(m)
		s.rowY, s.optSpans = s.rowY[:0], s.optSpans[:0]
		const labelW = 25
		s.optX = labelW + 2
		for r, row := range rows {
			mark := "  "
			if r == s.row {
				mark = stKey.Render("▸ ")
				if m.opt.Icons.ASCII() {
					mark = stKey.Render("> ")
				}
			}
			b.WriteString(mark + padRight(row.label, labelW))
			var spans [][2]int
			ox := 0
			for i, o := range row.opts {
				cell := " " + o + " "
				cw := ansi.StringWidth(cell)
				switch {
				case i == row.cur && strings.Contains(o, "\x1b"):
					b.WriteString(stBold.Render("[") + o + stBold.Render("]")) // colored options show their own color
				case i == row.cur:
					b.WriteString(stSelected.Render(cell))
				default:
					b.WriteString(cell)
				}
				spans = append(spans, [2]int{ox, ox + cw})
				ox += cw
			}
			b.WriteString("\n")
			s.rowY = append(s.rowY, line)
			s.optSpans = append(s.optSpans, spans)
			line++
		}
		if s.page == pageSecurity {
			b.WriteString(securityNotes(m, max(40, w-4-2*boxPadX)))
		}
		b.WriteString("\n")
		switch {
		case s.err != "":
			b.WriteString(stErr.Render(s.err))
		case s.status != "":
			b.WriteString(stOK.Render(m.g.check) + " " + stDim.Render(s.status))
		default:
			b.WriteString(stDim.Render("Changes apply at once and are saved to the config file."))
		}
		b.WriteString("\n\n" + stDim.Render("↑/↓ setting · ←/→ or click: change · Tab: next page · Esc: close"))
	default:
		lw := 0
		for _, it := range m.opt.Info {
			lw = max(lw, ansi.StringWidth(it.Label))
		}
		// long paths wrap under the value column
		vw := max(20, w-4-2*boxPadX-lw-2)
		for _, it := range m.opt.Info {
			for i, l := range strings.Split(ansi.Hardwrap(it.Value, vw, true), "\n") {
				label := ""
				if i == 0 {
					label = it.Label
				}
				b.WriteString(stBold.Render(padRight(label, lw)) + "  " + l + "\n")
			}
		}
		b.WriteString("\n" + stDim.Render("Tab: next page · Esc: close"))
	}
	return m.box(fmt.Sprintf("Settings %s Vaultcrab %s", m.g.v, m.opt.Version), b.String(), w)
}
