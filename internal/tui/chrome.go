// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

var (
	stTabActive   = lipgloss.NewStyle().Bold(true).Reverse(true)
	stTabInactive = lipgloss.NewStyle()
	stTabBar      = lipgloss.NewStyle().Faint(true)
	stStatus      = lipgloss.NewStyle().Reverse(true)
	stStatusErr   = lipgloss.NewStyle().Reverse(true).Foreground(lipgloss.Color("1"))
	stDim         = lipgloss.NewStyle().Faint(true)
	stBold        = lipgloss.NewStyle().Bold(true)
	stSelected    = lipgloss.NewStyle().Reverse(true)
	stGroup       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	stOrg         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	stErr         = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stOK          = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stWarn        = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stActivity    = lipgloss.NewStyle().Foreground(lipgloss.Color("12")) // new output in a hidden tab
	stBorder      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stBorderFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	stKey         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	stQuit        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
)

// sessionIcon returns the state icon of a session.
func (m *Model) sessionIcon(s *core.Session) string {
	if s.Exited() {
		return m.opt.Icons.Get(core.IconIdle) // finished cleanly
	}
	st, _ := s.State()
	return m.stateIcon(st)
}

// tabLabels returns the rendered labels of all tabs (index 0 = host list).
func (m *Model) tabLabels() []string {
	labels := make([]string, 0, len(m.tabs)+1)
	hosts := fmt.Sprintf(" 0:%s ", "Hosts")
	if m.parked != nil {
		hosts = " 0:Hosts " + m.ascii("✎") + " " // a dialog waits there
	}
	if m.active == 0 {
		labels = append(labels, stTabActive.Render(hosts))
	} else {
		labels = append(labels, stTabInactive.Render(hosts))
	}
	for i, t := range m.tabs {
		title := []rune(t.title(m.g))
		if tw := m.tabWidth(); ansi.StringWidth(string(title)) > tw {
			title = []rune(ansi.Truncate(string(title), tw, m.g.ell))
		}
		zoom := ""
		if t.zoomed() {
			zoom = m.opt.Icons.Get(core.IconZoom)
		}
		base := stTabInactive
		if i+1 == m.active {
			base = stTabActive
		}
		if m.opt.Appearance.TabStatus == "color" {
			// the number in the state color instead of an icon
			num := base
			if c, ok := t.stateColor(i+1 == m.active); ok {
				num = base.Foreground(c)
			}
			labels = append(labels, base.Render(" ")+num.Render(strconv.Itoa(i+1))+
				base.Render(fmt.Sprintf(":%s%s ", string(title), zoom)))
			continue
		}
		// the state icon first, outside the highlight of the active tab
		plain := fmt.Sprintf(" %d:%s%s ", i+1, string(title), zoom)
		labels = append(labels, " "+t.icon(m, i+1 == m.active)+base.Render(plain))
	}
	return labels
}

// tabWidth is the longest tab title (Settings → Tab width, ui.tab_width).
func (m *Model) tabWidth() int {
	if w := m.opt.Appearance.TabWidth; w > 0 {
		return w
	}
	return core.DefaultTabWidth
}

// quitButton is the close button at the right end of the tab bar.
func (m *Model) quitButton() string { return " " + m.opt.Icons.Get(core.IconQuit) + " " }

// tabsWidth is the width of the tab bar without the settings and close
// buttons.
func (m *Model) tabsWidth() int {
	return max(0, m.w-m.buttonsWidth())
}

// logButton opens the event log; shown while the log is on.
func (m *Model) logButton() string {
	if !m.svc.LogEnabled() {
		return ""
	}
	return " " + m.opt.Icons.Get(core.IconLog) + " "
}

// buttonsWidth is the width of the log (if shown), settings and close
// buttons at the right end of the tab bar, each after a separator.
func (m *Model) buttonsWidth() int {
	w := 2*ansi.StringWidth(m.g.v) + ansi.StringWidth(m.settingsButton()) + ansi.StringWidth(m.quitButton())
	if lb := m.logButton(); lb != "" {
		w += ansi.StringWidth(m.g.v) + ansi.StringWidth(lb)
	}
	return w
}

// buttonAt names the tab bar button at column x (x >= tabsWidth): "log",
// "settings" or "quit"; the separator before a button belongs to it.
func (m *Model) buttonAt(x int) string {
	sep := ansi.StringWidth(m.g.v)
	x0 := m.tabsWidth()
	if lb := m.logButton(); lb != "" {
		if x < x0+sep+ansi.StringWidth(lb) {
			return "log"
		}
		x0 += sep + ansi.StringWidth(lb)
	}
	if x < x0+sep+ansi.StringWidth(m.settingsButton()) {
		return "settings"
	}
	return "quit"
}

// tabSpans returns the x ranges of tab labels as displayed (starts[i] < 0:
// scrolled out). The host list tab (0) always stays at the left; the
// session tabs after it scroll, first being the first one shown (a ‹ marks
// hidden ones).
func (m *Model) tabSpans() (labels []string, starts []int, first int) {
	labels = m.tabLabels()
	widths := make([]int, len(labels))
	for i, l := range labels {
		widths[i] = ansi.StringWidth(l) + 1 // separator
	}
	more := ansi.StringWidth(m.g.more)
	// Scroll so the active tab fits.
	first = 1
	for first < m.active {
		x := widths[0]
		if first > 1 {
			x += more
		}
		for i := first; i <= m.active; i++ {
			x += widths[i]
		}
		if x <= m.tabsWidth() {
			break
		}
		first++
	}
	starts = make([]int, len(labels))
	x := 0
	for i := range labels {
		switch {
		case i == 0:
		case i < first:
			starts[i] = -1
			continue
		case i == first && first > 1:
			x += more
		}
		starts[i] = x
		x += widths[i]
	}
	return labels, starts, first
}

func (m *Model) tabBar() string {
	labels, starts, first := m.tabSpans()
	var b strings.Builder
	for i, l := range labels {
		if starts[i] < 0 {
			continue
		}
		if starts[i] >= m.tabsWidth() {
			break
		}
		if i == first && first > 1 {
			b.WriteString(m.g.more)
		}
		b.WriteString(l)
		b.WriteString(stTabBar.Render(m.g.v))
	}
	sep := stTabBar.Render(m.g.v)
	logBtn := ""
	if lb := m.logButton(); lb != "" {
		logBtn = sep + stKey.Render(lb)
	}
	return padRight(b.String(), m.tabsWidth()) + logBtn + sep + stKey.Render(m.settingsButton()) + sep + stQuit.Render(m.quitButton())
}

// tabAt returns the tab index at column x of the tab bar, or -1.
func (m *Model) tabAt(x int) int {
	labels, starts, _ := m.tabSpans()
	for i := range labels {
		if starts[i] < 0 {
			continue
		}
		if x >= starts[i] && x < starts[i]+ansi.StringWidth(labels[i]) {
			return i
		}
	}
	return -1
}

func (m *Model) tabBarY() int {
	if m.opt.TabsBottom {
		return m.h - 1
	}
	return 0
}

func (m *Model) statusY() int {
	if m.opt.TabsBottom {
		return m.h - 2
	}
	return m.h - 1
}

// vaultStatus renders the vault state for the status bar; several vaults
// are listed each with its state icon (and the state when it is not online).
func (m *Model) vaultStatus() string {
	if vs := m.svc.Vaults(); len(vs) > 1 {
		ic := m.opt.Icons
		var parts []string
		for _, v := range vs {
			s := v.Label()
			switch {
			case v.State == core.Online && v.SyncErr != nil:
				s = ic.Get(core.IconCached) + " " + s + " (server unreachable, local copy)"
			case v.State == core.Crashed:
				s = ic.Get(core.IconSyncing) + " " + s + " restarting bw serve…"
			case v.State == core.Online:
				s = ic.Get(core.IconOnline) + " " + s
			case v.State == core.Locked:
				s = ic.Get(core.IconLocked) + " " + s + " locked (u)"
			case v.State == core.NeedLogin:
				s = ic.Get(core.IconOffline) + " " + s + " not logged in (L)"
			case v.State == core.Starting || v.State == core.Syncing:
				s = ic.Get(core.IconSyncing) + " " + s + " " + v.State.String() + "…"
			default:
				s = ic.Get(core.IconOffline) + " " + s + " offline"
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " · ")
	}
	st := m.svc.Status()
	ic := m.opt.Icons
	var s string
	switch {
	case st.State == core.Starting:
		s = ic.Get(core.IconSyncing) + " starting bw…"
	case st.State == core.Syncing:
		s = ic.Get(core.IconSyncing) + " syncing…"
	case st.State == core.Crashed:
		s = ic.Get(core.IconSyncing) + " bw serve stopped, restarting…"
	case st.State == core.Online && st.SyncErr != nil:
		// Unlocked, but the server is unreachable (no network, VPN down):
		// bw's local copy is used (passwords included); the server is
		// checked again with a growing delay (s: now).
		s = ic.Get(core.IconCached) + " Offline: server unreachable, local copy"
		if !st.SyncedAt.IsZero() {
			s += ", synced " + st.SyncedAt.Format("02.01 15:04")
		}
	case st.State == core.Online:
		s = ic.Get(core.IconOnline) + " Online"
		if !st.SyncedAt.IsZero() {
			s += " " + st.SyncedAt.Format("15:04")
		}
	case st.State == core.Locked:
		s = ic.Get(core.IconLocked) + " Locked (u: unlock)"
	case st.State == core.NeedLogin:
		s = ic.Get(core.IconOffline) + " bw not logged in (L: login)"
	case st.State == core.Offline:
		s = ic.Get(core.IconOffline) + " bw serve unreachable (s: retry)"
	}
	if st.FromCache && st.State != core.Online {
		s += " · cache"
		if !st.SyncedAt.IsZero() {
			s += " " + st.SyncedAt.Format("02.01 15:04")
		}
	}
	return s
}

func (m *Model) statusBar() string {
	left := " " + m.vaultStatus() + " " + m.g.v + " "
	var mid string
	if t := m.curTab(); t != nil {
		mid = t.status(m)
	} else {
		mid = m.list.status()
	}
	right := ""
	switch {
	case m.prefix:
		right = " [" + m.opt.Prefix + "] "
	case m.opt.Version != "" && m.w >= 60:
		right = " Vaultcrab " + m.opt.Version + " "
	}
	style := stStatus
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		mid = m.toast
		if m.toastErr {
			style = stStatusErr
		}
	}
	line := m.ascii(left + mid)
	// a config file that does not load: marked until it is fixed
	cfgErr := m.configError()
	if ansi.StringWidth(cfgErr)+ansi.StringWidth(right)+20 > m.w {
		cfgErr = ""
	}
	room := m.w - ansi.StringWidth(right) - ansi.StringWidth(cfgErr)
	line = padRight(ansi.Truncate(line, max(room, 0), m.g.ell), max(room, 0))
	if cfgErr == "" {
		return style.Render(padRight(line+right, m.w))
	}
	return style.Render(line) + stStatusErr.Render(cfgErr) + style.Render(right)
}

// ascii replaces the decorative punctuation used in UI texts when the icon
// set is ASCII.
func (m *Model) ascii(s string) string {
	if !m.opt.Icons.ASCII() {
		return s
	}
	return asciiPunct.Replace(s)
}

var asciiPunct = strings.NewReplacer("·", "|", "…", "...", "—", "-", "↑", "^", "↓", "v", "←", "<", "→", ">", "⚠", "!", "✗", "x", "✕", "x", "▸", ">", "✎", "*")
