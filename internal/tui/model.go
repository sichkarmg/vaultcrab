// SPDX-License-Identifier: MIT

// Package tui is the Bubble Tea user interface. It talks to the rest of the
// application only through internal/core.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// Options configures the UI (built from the config by main).
type Options struct {
	TabsBottom   bool
	ForwardMouse bool
	CloseOnExit  bool
	CopyOSC52    bool              // copy selections to the outer terminal's clipboard
	CopySystem   bool              // copy selections to the system clipboard
	ConfirmPaste bool              // ask before pasting several lines
	Prefix       string            // e.g. "ctrl+b"
	Keys         map[string]string // action -> key pressed after the prefix
	Icons        core.IconSet
	ShowTree     bool
	ShowNotes    bool // notes of the selected item in its card
	ListWidth    int
	Widgets      bool   // show the widget column on the host list
	WidgetsWidth int    // its width
	WidgetsMin   int    // minimum screen width to show it
	ConfigPath   string // shown in hints
	// RestoreTabs saves the open tabs (hosts and pane layout) on exit to
	// WorkspacePath and reopens them, not connected, on the next start.
	RestoreTabs   bool
	WorkspacePath string
	// RestoreView keeps the host list filters (type, org, collection,
	// grouping, search, folded groups, selection) in ViewPath.
	RestoreView bool
	ViewPath    string
	Version     string
	// Appearance is what the settings dialog changes (filled from the
	// options above when empty); Info is shown on its About page.
	Appearance core.Appearance
	Info       []InfoItem
}

// Model is the root Bubble Tea model.
type Model struct {
	svc    *core.Service
	opt    Options
	keymap map[string]string // key -> action
	ctx    context.Context
	g      glyphs

	w, h int

	tabs    []*tab // session tabs
	active  int    // 0 = host list, i = tabs[i-1]
	lastUse *tab   // last active session tab (target for "open in pane")

	list hostList

	prefix bool
	modal  modal
	parked modal // a docked dialog of the host list while another tab is active

	toast      string
	toastErr   bool
	toastUntil time.Time

	clip string // last copied text (middle click pastes it)

	pendingRestore *core.WorkspaceState // saved tabs waiting for the host list
	pendingView    *core.ViewState      // saved org/collection waiting for the vault

	notes noteState // notes of the selected item (memory only)

	pwCopiedHost string    // the password of this host was copied ...
	pwCopiedAt   time.Time // ... at this time (confirmation under the button)
	pwCopiedWarn string    // the copy reached only the terminal (shown instead)
	osc          string    // OSC 52 sequence emitted with the frame until oscUntil
	oscUntil     time.Time

	selClickAt  time.Time // multi-click detection in panes
	selClickPos core.TermPos
	selClicks   int

	lastClick     time.Time
	lastClickKey  string
	unlockOffered bool
	quitting      bool
	discardTabs   bool // quit without saving the open tabs

	dlgX, dlgY, dlgW, dlgH int // where the dialog was drawn (mouse)

	programs     *programsPage // the External programs page of the settings (its draft)
	settingsPage int           // the settings open on the page used last
	cfgSeq       int           // the config reload last reported

	wake    *time.Timer // the redraw at wakeAt (idle.go)
	wakeAt  time.Time
	blurred bool // the terminal window lost the focus
}

// New creates the root model.
func New(ctx context.Context, svc *core.Service, opt Options) *Model {
	if opt.Prefix == "" {
		opt.Prefix = "ctrl+b"
	}
	m := &Model{svc: svc, opt: opt, ctx: ctx, keymap: map[string]string{}, g: unicodeGlyphs, wake: time.NewTimer(time.Hour)}
	m.wake.Stop()
	if opt.Icons.ASCII() {
		m.g = asciiGlyphs
	}
	if m.opt.Appearance.IconMode == "" {
		mode := "unicode"
		if opt.Icons.ASCII() {
			mode = "ascii"
		}
		m.opt.Appearance = core.Appearance{TabsBottom: opt.TabsBottom, IconMode: mode, SSHIcon: opt.Icons.Icon(core.IconSSH),
			Accent: core.Accents[0].Name, Selection: core.Selections[0].Name, ShowTree: opt.ShowTree, ShowNotes: opt.ShowNotes, Widgets: opt.Widgets, ListWidth: opt.ListWidth, TabWidth: core.DefaultTabWidth, TabStatus: "icon",
			ConfirmPaste: opt.ConfirmPaste, RestoreTabs: opt.RestoreTabs, RestoreView: opt.RestoreView, LockTimeout: "8h",
			PINAttempts: 5, MasterAfterBoot: true, MasterEveryDays: 14}
	}
	setAccent(core.AccentColor(m.opt.Appearance.Accent))
	setSelection(m.opt.Appearance.Selection)
	for action, key := range opt.Keys {
		m.keymap[key] = action
	}
	m.cfgSeq = svc.ConfigStatus().Seq
	m.list.init(m)
	m.loadView()
	m.loadWorkspace()
	return m
}

type eventMsg struct{}

// listen waits for the next core event or the scheduled redraw (idle.go).
func (m *Model) listen() tea.Cmd {
	ch, wake := m.svc.Events(), m.wake.C
	return func() tea.Msg {
		select {
		case <-ch:
			return eventMsg{}
		case <-wake:
			return tickMsg{}
		}
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	// A fixed title: terminals otherwise may show the names of processes
	// started by vaultcrab.
	return tea.Batch(m.listen(), tea.SetWindowTitle("Vaultcrab"))
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	if c := m.maybeFetchNotes(); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	if c := m.startTouch(); c != nil {
		cmd = tea.Batch(cmd, c)
	}
	m.scheduleWake()
	return m, cmd
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		// user input: the auto-lock counts from here; after the lock timeout
		// (a sleep counts) the vaults are locked first and the key opens
		// the unlock dialog instead of doing what it would
		if m.svc.Activity() {
			if _, ok := msg.(tea.KeyMsg); ok {
				if _, open := m.modal.(*unlockModal); !open {
					m.unlockOffered = true
					m.modal = newUnlock(m)
				}
				return m, nil
			}
		}
	}
	switch msg := msg.(type) {
	case notesWaitMsg:
		return m, m.notesWait(msg)
	case notesMsg:
		m.notesRead(msg)
		return m, nil
	case eventMsg:
		m.onEvent()
		// Throttle redraws to ~60 fps under heavy output.
		return m, tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg { return m.listen()() })
	case tickMsg:
		m.wakeAt = time.Time{}
		return m, m.listen() // redraw: a message or mark expired
	case tea.FocusMsg, tea.BlurMsg:
		m.focus(msg)
		return m, nil
	case tea.WindowSizeMsg:
		debugf("window %dx%d", msg.Width, msg.Height)
		m.w, m.h = msg.Width, msg.Height
		m.resizeSessions()
		m.restoreWorkspace()
		return m, nil
	case unlockResultMsg, pinResultMsg, touchResultMsg, pinSetMsg, loginDoneMsg, launchResultMsg:
		return m, m.handleResult(msg)
	case vaultLoginDoneMsg:
		if msg.err != nil {
			m.notify(true, "bw login: %v", msg.err)
		}
		m.svc.ReconnectVault(m.ctx, msg.id)
		return m, nil
	case scriptResultMsg:
		m.scriptResult(msg)
		return m, nil
	case progTestMsg:
		if m.programs != nil {
			m.programs.testResult(msg)
		}
		if m.modal == nil && msg.err != nil {
			m.notify(true, "test start of %s: %v", msg.name, msg.err)
		}
		return m, nil
	case passwordCopiedMsg:
		m.passwordCopied(msg)
		return m, nil
	case tea.MouseMsg:
		debugf("mouse x=%d y=%d action=%d button=%d", msg.X, msg.Y, msg.Action, msg.Button)
		if m.modal != nil {
			if isDocked(m.modal) && msg.Y == m.tabBarY() {
				// another tab, the quit button: the dialog waits
				m.parked, m.modal = m.modal, nil
				cmd := m.mouse(msg)
				m.unpark()
				return m, cmd
			}
			cmd := m.modalMouse(msg)
			m.unpark()
			return m, cmd
		}
		return m, m.mouse(msg)
	case tea.KeyMsg:
		cmd := m.key(msg)
		m.unpark()
		return m, cmd
	}
	if m.modal != nil {
		cmd := m.modal.update(m, msg)
		m.unpark()
		return m, cmd
	}
	if m.parked != nil {
		return m, m.toParked(msg)
	}
	return m, nil
}

// onEvent reacts to core state changes.
func (m *Model) onEvent() {
	m.configReloaded()
	m.list.rebuild()
	m.restoreView()
	m.restoreWorkspace()
	if m.opt.CloseOnExit {
		m.closeExited()
	}
	st := m.svc.Status()
	if st.State == core.Locked && !m.unlockOffered && m.modal == nil {
		m.unlockOffered = true
		m.modal = newUnlock(m)
	}
	if st.State == core.Online {
		m.unlockOffered = false
		if u, ok := m.modal.(*unlockModal); ok && !u.busy {
			m.modal = nil
		}
	}
}

func (m *Model) notify(err bool, format string, args ...any) {
	m.toast = fmt.Sprintf(format, args...)
	m.toastErr = err
	m.toastUntil = time.Now().Add(5 * time.Second)
}

func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	if isDocked(m.modal) {
		if cmd, ok := m.dockedKey(msg); ok {
			return cmd
		}
	}
	if m.modal != nil {
		return m.modal.update(m, msg)
	}
	k := msg.String()
	if m.prefix {
		m.prefix = false
		return m.prefixed(msg)
	}
	if k == m.opt.Prefix {
		m.prefix = true
		return nil
	}
	if isTabKey(k, m.active == 0 && m.list.searching) {
		return m.tabKey(k)
	}
	if m.active == 0 {
		return m.list.key(msg)
	}
	return m.curTab().key(m, msg)
}

// prefixed handles the key following the prefix.
func (m *Model) prefixed(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	if k == m.opt.Prefix {
		// Prefix twice sends the prefix key itself to the terminal.
		if t := m.curTab(); t != nil {
			if tk, ok := TermKey(msg); ok {
				t.sendKey(tk)
			}
		}
		return nil
	}
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		if n := int(k[0] - '0'); n <= len(m.tabs) {
			m.switchTab(n)
		}
		return nil
	}
	switch m.keymap[k] {
	case core.ActNextTab:
		m.switchTab((m.active + 1) % (len(m.tabs) + 1))
	case core.ActPrevTab:
		m.switchTab((m.active + len(m.tabs)) % (len(m.tabs) + 1))
	case core.ActList:
		m.switchTab(0)
	case core.ActCloseTab:
		if t := m.curTab(); t != nil {
			m.confirmCloseTab(t)
		}
	case core.ActHistory:
		if t := m.curTab(); t != nil {
			t.enterHistory(m, false)
		}
	case core.ActReconnect:
		if t := m.curTab(); t != nil {
			t.reconnect()
		}
	case core.ActQuit:
		return m.confirmQuit()
	case core.ActHelp:
		m.modal = newHelp(m)
	case core.ActSettings:
		m.openSettings()
	case core.ActShowLog:
		m.modal = newLogView(m)
	case core.ActLocal:
		return m.openHost(m.svc.LocalHost())
	default:
		if t := m.curTab(); t != nil {
			return t.paneAction(m, m.keymap[k], msg)
		}
	}
	return nil
}

func (m *Model) curTab() *tab {
	if m.active == 0 || m.active > len(m.tabs) {
		return nil
	}
	return m.tabs[m.active-1]
}

func (m *Model) setActive(i int) {
	m.active = max(0, min(i, len(m.tabs)))
	if t := m.curTab(); t != nil {
		t.resize(m.bodyRect())
		m.lastUse = t
	}
}

// lastTab returns the last used session tab that still exists.
func (m *Model) lastTab() *tab {
	for _, t := range m.tabs {
		if t == m.lastUse {
			return t
		}
	}
	if len(m.tabs) > 0 {
		return m.tabs[len(m.tabs)-1]
	}
	return nil
}

// confirmQuit asks before quitting: whether to keep the open tabs for the
// next start (when tabs are restored), or whether to close active sessions.
func (m *Model) confirmQuit() tea.Cmd {
	n := 0
	for _, t := range m.tabs {
		n += t.liveSessions()
	}
	if m.opt.RestoreTabs && m.opt.WorkspacePath != "" && len(m.tabs) > 0 {
		m.modal = &quitModal{live: n}
		return nil
	}
	if n == 0 {
		return m.quit()
	}
	m.modal = newConfirm(fmt.Sprintf("Quit and close %d active session(s)?", n), func(m *Model) tea.Cmd { return m.quit() })
	return nil
}

// modalMouse passes a press inside the dialog to dialogs that use the mouse.
func (m *Model) modalMouse(msg tea.MouseMsg) tea.Cmd {
	mm, ok := m.modal.(mouseModal)
	if !ok {
		return nil
	}
	x, y := msg.X-m.dlgX, msg.Y-m.dlgY
	if x < 0 || y < 0 || x >= m.dlgW || y >= m.dlgH {
		return nil
	}
	return mm.mouse(m, msg, x, y)
}

func (m *Model) quit() tea.Cmd {
	m.quitting = true
	return tea.Quit
}

func (m *Model) confirmCloseTab(t *tab) {
	if t.liveSessions() == 0 {
		m.closeTab(t)
		return
	}
	m.modal = newConfirm(fmt.Sprintf("Close %q?", t.title()), func(m *Model) tea.Cmd {
		m.closeTab(t)
		return nil
	})
}

func (m *Model) closeTab(t *tab) {
	for i, x := range m.tabs {
		if x == t {
			t.close(m)
			m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
			if m.active > len(m.tabs) || m.active == i+1 {
				m.setActive(min(i, len(m.tabs)))
			} else if m.active > i+1 {
				m.active--
			}
			return
		}
	}
}

// closeExited closes panes whose shell exited cleanly.
func (m *Model) closeExited() {
	for _, t := range append([]*tab(nil), m.tabs...) {
		for _, id := range t.ws.Panes() {
			s := t.ws.Session(id)
			if s == nil || !s.Exited() || s.Hold() {
				continue
			}
			if t.ws.Len() == 1 {
				m.closeTab(t)
				break
			}
			t.ws.Take(id)
			delete(t.history, id)
			m.svc.CloseSession(s)
		}
	}
}

// openHost connects to h: SSH (or the local shell) in a new tab, other
// types via external programs.
func (m *Model) openHost(h core.Host) tea.Cmd {
	r := m.bodyRect()
	s, cmd := m.openSession(h, r.w, r.h)
	if s != nil {
		m.tabs = append(m.tabs, newTab(s))
		m.setActive(len(m.tabs))
	}
	return cmd
}

// openSession starts a terminal session for h, or launches the external
// program for hosts that are not shown in a terminal (then s is nil).
func (m *Model) openSession(h core.Host, cols, rows int) (*core.Session, tea.Cmd) {
	switch {
	case h.Scheme == core.SchemeLocal:
		return m.svc.OpenLocal(cols, rows), nil
	case m.svc.InTab(h): // SSH, VNC
		s, err := m.svc.OpenSession(h, cols, rows)
		if err != nil {
			m.notify(true, "%v", err)
		}
		return s, nil
	}
	return nil, m.launch(h)
}

type launchResultMsg struct {
	host core.Host
	err  error
}

func (m *Model) launch(h core.Host) tea.Cmd {
	m.notify(false, "starting %s for %s…", h.Scheme, h.Name)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		return launchResultMsg{host: h, err: m.svc.LaunchExternal(ctx, h)}
	}
}

func (m *Model) handleResult(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case launchResultMsg:
		if msg.err != nil {
			m.notify(true, "%s: %v", msg.host.Name, msg.err)
		} else {
			m.notify(false, "%s opened in external %s client", msg.host.Name, msg.host.Scheme)
		}
	case unlockResultMsg, pinResultMsg, touchResultMsg, pinSetMsg, loginDoneMsg:
		if m.modal != nil {
			return m.modal.update(m, msg)
		}
		if l, ok := msg.(loginDoneMsg); ok {
			return m.afterLogin(l)
		}
	}
	return nil
}

type rect struct{ x, y, w, h int }

// bodyRect is the area between the tab bar and the status bar.
func (m *Model) bodyRect() rect {
	y := 1
	if m.opt.TabsBottom {
		y = 0
	}
	return rect{x: 0, y: y, w: max(m.w, 1), h: max(m.h-2, 1)}
}

func (m *Model) resizeSessions() {
	r := m.bodyRect()
	for _, t := range m.tabs {
		t.resize(r)
	}
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.w == 0 || m.h == 0 || m.quitting {
		return ""
	}
	if m.h < 3 {
		return strings.Join(fitLines([]string{"vaultcrab: window too small"}, m.w, m.h), "\n")
	}
	r := m.bodyRect()
	var body []string
	if m.active == 0 {
		body = m.list.view(r.w, r.h)
	} else {
		m.svc.SetWidgetsActive(false) // hidden behind a session tab
		body = m.curTab().view(m, r)
	}
	body = fitLines(body, r.w, r.h)
	if m.modal != nil {
		dlg := m.modal.view(m, r.w, r.h)
		body = overlay(body, dlg, r.w, r.h)
		m.dlgW, m.dlgH = 0, len(dlg)
		for _, l := range dlg {
			m.dlgW = max(m.dlgW, ansi.StringWidth(l))
		}
		m.dlgX, m.dlgY = r.x+max(0, (r.w-m.dlgW)/2), r.y+max(0, (r.h-len(dlg))/2) // as overlay places it
	}
	tabs := m.tabBar()
	status := m.statusBar()
	if m.osc != "" {
		if time.Now().Before(m.oscUntil) {
			status = m.osc + status // zero-width: the terminal stores the copied text
		} else {
			m.osc = ""
		}
	}
	parts := make([]string, 0, r.h+2)
	if !m.opt.TabsBottom {
		parts = append(parts, tabs)
	}
	parts = append(parts, body...)
	parts = append(parts, status)
	if m.opt.TabsBottom {
		parts = append(parts, tabs)
	}
	return strings.Join(parts, "\n")
}

// fitLines pads/clips lines to exactly w×h.
func fitLines(lines []string, w, h int) []string {
	out := make([]string, h)
	for i := 0; i < h; i++ {
		var l string
		if i < len(lines) {
			l = lines[i]
		}
		out[i] = padRight(l, w)
	}
	return out
}

// padRight truncates or pads an ANSI string to exactly w columns.
func padRight(s string, w int) string {
	sw := ansi.StringWidth(s)
	if sw > w {
		s = ansi.Truncate(s, w, "")
		sw = ansi.StringWidth(s)
	}
	if sw < w {
		s += strings.Repeat(" ", w-sw)
	}
	return s
}
