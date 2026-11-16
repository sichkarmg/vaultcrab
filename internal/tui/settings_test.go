// SPDX-License-Identifier: MIT

package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

func TestSettings(t *testing.T) {
	m, _ := newTestModel(t, false)
	t.Cleanup(func() { resetColors() })
	m.opt.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	m.opt.Info = []InfoItem{{Label: "Config file", Value: m.opt.ConfigPath}}

	// the gear left of the close button opens the settings
	top := ansi.Strip(strings.Split(m.View(), "\n")[0])
	if !strings.HasSuffix(top, "│ ⚙ │ ⊗ ") {
		t.Fatalf("tab bar %q", top)
	}
	m.Update(tea.MouseMsg{X: 115, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	s, ok := m.modal.(*settingsModal)
	if !ok {
		t.Fatalf("modal %T", m.modal)
	}
	frame := strings.Split(ansi.Strip(m.View()), "\n")
	checkFrame(t, m.View(), 120, 30)

	// click the "$" SSH icon
	r := 2
	if !strings.Contains(frame[m.dlgY+s.rowY[r]], "SSH item icon") {
		t.Fatalf("row %d:\n%s", r, strings.Join(frame, "\n"))
	}
	sp := s.optSpans[r][2]
	m.Update(tea.MouseMsg{X: m.dlgX + boxPadX + s.optX + sp[0] + 1, Y: m.dlgY + s.rowY[r], Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if got := m.opt.Icons.Get(core.IconSSH); got != "$" {
		t.Fatalf("ssh icon %q", got)
	}
	b, err := os.ReadFile(m.opt.ConfigPath)
	if err != nil || !strings.Contains(string(b), `unicode: "$"`) {
		t.Fatalf("config %q %v (%s)", b, err, s.err)
	}

	// keys: tabs to the bottom
	keys(m, "up", "up", "right")
	if !m.opt.TabsBottom || !strings.Contains(string(must(os.ReadFile(m.opt.ConfigPath))), "tabs_position: bottom") {
		t.Fatal("tabs not at the bottom")
	}

	// selection color: green
	keys(m, "down", "down", "down", "down", "down", "right", "right", "right", "right")
	if m.opt.Appearance.Selection != "green" || !strings.Contains(string(must(os.ReadFile(m.opt.ConfigPath))), "selection_color: green") {
		t.Fatalf("selection %q", m.opt.Appearance.Selection)
	}
	for name, st := range map[string]lipgloss.Style{"selection": stSelected, "active tab": stTabActive, "status bar": stStatus, "button": stButton} {
		if bg := st.GetBackground(); bg != lipgloss.Color("2") {
			t.Errorf("%s: background %v", name, bg)
		}
	}

	// Behavior: multi-line paste confirmation off
	keys(m, "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Confirm multi-line paste") {
		t.Fatalf("behavior:\n%s", v)
	}
	m.opt.Appearance.ConfirmPaste, m.opt.ConfirmPaste = true, true
	keys(m, "right")
	if m.opt.ConfirmPaste || !strings.Contains(string(must(os.ReadFile(m.opt.ConfigPath))), "confirm_paste: false") {
		t.Fatal("confirm_paste not turned off")
	}

	// Security: the lock timeout (default 8 hours); a longer one warns that
	// the vault stays open to every program of the user
	keys(m, "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Lock after no input for") || !strings.Contains(v, "[") && !strings.Contains(v, "8 hours") {
		t.Fatalf("security:\n%s", v)
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "stays unlocked") {
		t.Fatalf("warning with 8 hours:\n%s", v)
	}
	keys(m, "right")
	if m.opt.Appearance.LockTimeout != "24h" || !strings.Contains(string(must(os.ReadFile(m.opt.ConfigPath))), "lock_timeout: 24h") {
		t.Fatalf("lock_timeout %q", m.opt.Appearance.LockTimeout)
	}
	if p := m.svc.Policy(); p.Timeout != 24*time.Hour {
		t.Fatalf("policy not applied: %+v", p)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "stays unlocked for 1 day") || !strings.Contains(v, "any program of your user account") {
		t.Fatalf("no warning for 1 day:\n%s", v)
	}
	keys(m, "left")

	// Vaults, Jump hosts, Tunnels, then About (after External programs)
	keys(m, "tab", "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Add vault") || !strings.Contains(v, "Online") && !strings.Contains(v, "online") {
		t.Fatalf("vaults:\n%s", v)
	}
	keys(m, "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "No templates yet") {
		t.Fatalf("jump hosts:\n%s", v)
	}
	keys(m, "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "No tunnels yet") || !strings.Contains(v, "reconnection attempts: 8") {
		t.Fatalf("tunnels:\n%s", v)
	}
	keys(m, "tab")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Config file") || !strings.Contains(v, m.opt.ConfigPath[:20]) {
		t.Fatalf("about:\n%s", v)
	}
	keys(m, "esc")
	if m.modal != nil {
		t.Fatal("not closed")
	}
	// the host list shows the new icon
	if v := ansi.Strip(m.View()); !strings.Contains(v, "$ alpha") {
		t.Fatalf("list:\n%s", v)
	}
	checkFrame(t, m.View(), 120, 30)
}

func must[T any](v T, _ error) T { return v }

// Settings → Tab width: longer tab titles are cut with … at that width,
// saved to ui.tab_width.
func TestTabWidth(t *testing.T) {
	m, svc := newTestModel(t, false)
	m.opt.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	// a terminal script tab is titled "<script>: <item>"
	s, err := svc.RunScript(m.ctx, svc.LocalHost(), core.Script{Name: "a-very-long-session-title-for-the-tab-bar", Command: []string{"sleep", "5"}}, 80, 20)
	if err != nil {
		t.Fatal(err)
	}
	m.tabs = append(m.tabs, newTab(s))
	t.Cleanup(func() { svc.Shutdown() })
	label := func() string { return ansi.Strip(m.tabLabels()[1]) }
	if l := label(); !strings.Contains(l, "1:a-very-long-session-tit…") {
		t.Fatalf("default width: %q", l)
	}
	keys(m, ",")
	st := m.modal.(*settingsModal)
	row := -1
	for i, r := range st.settings(m) {
		if r.label == "Tab width" {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("no Tab width row")
	}
	st.change(m, row, 1) // 12
	if l := label(); !strings.Contains(l, "1:a-very-long…") || strings.Contains(l, "long-") {
		t.Fatalf("width 12: %q", l)
	}
	if b, _ := os.ReadFile(m.opt.ConfigPath); !strings.Contains(string(b), "tab_width: 12") {
		t.Fatalf("not saved: %s", b)
	}
}

// A row with more options than fit shows a window around the chosen one;
// the hidden options are not clickable.
func TestSettingsLongRow(t *testing.T) {
	opts := []string{"aaaa", "bbbb", "cccc", "dddd", "eeee", "ffff", "gggg"}
	if lo, hi := optWindow(opts, 3, 100); lo != 0 || hi != len(opts) {
		t.Fatalf("all fit: %d %d", lo, hi)
	}
	lo, hi := optWindow(opts, 6, 24)
	if hi != 7 || lo < 1 || 6*(hi-lo)+4 > 24 {
		t.Fatalf("end: %d %d", lo, hi)
	}
	if lo, hi = optWindow(opts, 3, 24); lo > 3 || hi <= 3 || lo == 0 && hi == 7 {
		t.Fatalf("middle: %d %d", lo, hi)
	}
}

// A custom accent from the config is offered after the named ones, and
// the accent row cycles through all colors.
func TestSettingsCustomColors(t *testing.T) {
	m, _ := newTestModel(t, false)
	t.Cleanup(func() { resetColors() })
	m.opt.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	a := m.opt.Appearance
	a.Accent, a.Selection = "#ff8800", "#ffffff"
	m.applyAppearance(a)
	if stKey.GetForeground() != lipgloss.Color("#ff8800") {
		t.Fatalf("accent %v", stKey.GetForeground())
	}
	if stSelected.GetBackground() != lipgloss.Color("#ffffff") || stSelected.GetForeground() != lipgloss.Color("0") {
		t.Fatalf("selection %v on %v", stSelected.GetForeground(), stSelected.GetBackground())
	}
	m.openSettings()
	s := m.modal.(*settingsModal)
	rows := s.settings(m)
	for _, r := range rows {
		switch r.label {
		case "Accent color":
			if len(r.opts) != len(core.Accents)+2 || r.cur != len(core.Accents)+1 || !strings.Contains(ansi.Strip(r.opts[r.cur]), "#ff8800") {
				t.Fatalf("accent row %d/%d", r.cur, len(r.opts))
			}
		case "Selection color":
			if len(r.opts) != len(core.Selections)+2 || r.cur != len(core.Selections)+1 {
				t.Fatalf("selection row %d/%d", r.cur, len(r.opts))
			}
		}
	}
	v := ansi.Strip(m.View())
	checkFrame(t, m.View(), 120, 30)
	if !strings.Contains(v, "#ff8800") {
		t.Fatalf("custom accent not shown:\n%s", v)
	}
	// a long row: the chosen option is shown, the window is marked
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "Accent color") && !strings.Contains(l, "…") {
			t.Fatalf("no window mark: %q", l)
		}
	}
}

// The theme row colors the whole interface; an accent of its own stays.
func TestSettingsTheme(t *testing.T) {
	m, _ := newTestModel(t, false)
	t.Cleanup(resetColors)
	m.opt.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	m.openSettings()
	s := m.modal.(*settingsModal)
	row := -1
	for i, r := range s.settings(m) {
		if r.label == "Theme" {
			row = i
		}
	}
	if row < 0 {
		t.Fatal("no Theme row")
	}
	a := m.opt.Appearance
	a.Accent = "green"
	m.applyAppearance(a)
	nord := slices.IndexFunc(core.Themes, func(t core.Theme) bool { return t.Name == "nord" })
	s.change(m, row, nord)
	if m.opt.Appearance.Accent != core.FromTheme {
		t.Fatalf("a picked theme keeps the accent %q", m.opt.Appearance.Accent)
	}
	if m.opt.Appearance.Theme != "nord" || !strings.Contains(string(must(os.ReadFile(m.opt.ConfigPath))), "theme: nord") {
		t.Fatalf("theme %q", m.opt.Appearance.Theme)
	}
	for name, got := range map[string]lipgloss.TerminalColor{"key": stKey.GetForeground(), "ok": stOK.GetForeground(), "err": stErr.GetForeground(),
		"border": stBorder.GetForeground(), "selection": stSelected.GetBackground(), "org": stOrg.GetForeground()} {
		want := map[string]string{"key": "#88c0d0", "ok": "#a3be8c", "err": "#bf616a", "border": "#4c566a", "selection": "#5e81ac", "org": "#b48ead"}[name]
		if got != lipgloss.Color(want) {
			t.Errorf("%s: %v, want %s", name, got, want)
		}
	}
	// an accent of its own over the theme
	a = m.opt.Appearance
	a.Accent = "orange"
	m.applyAppearance(a)
	if stKey.GetForeground() != lipgloss.Color("208") || stOK.GetForeground() != lipgloss.Color("#a3be8c") {
		t.Fatalf("accent %v ok %v", stKey.GetForeground(), stOK.GetForeground())
	}
	checkFrame(t, m.View(), 120, 30)
}
