// SPDX-License-Identifier: MIT

// Command termproto is the step-4 prototype: SSH sessions from the vault as
// tabs with an embedded terminal emulator. Kept as a dev tool.
//
//	BW_SESSION=... termproto -hosts vaultcrab-test-mint-password,vaultcrab-test-win-password
//
// Keys: Ctrl+B is the prefix. Prefix n/p — next/prev tab, 1..9 — tab N,
// x — drop the connection (simulate a network failure), q — quit,
// [ or PgUp — history mode (PgUp/PgDn/↑/↓/Home/End scroll, Esc/q leave),
// Ctrl+B — send Ctrl+B. Shift+↑/↓ and the mouse wheel also scroll.
// Enter in a disconnected tab reconnects.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/core"
	"github.com/sichkarmg/vaultcrab/internal/tui"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

func main() {
	hosts := flag.String("hosts", "vaultcrab-test-mint-password,vaultcrab-test-win-password", "comma separated host names from the vault")
	noMouse := flag.Bool("no-mouse", false, "disable mouse support")
	flag.Parse()

	cfgDir, _ := os.UserConfigDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var srv *vault.Server
	svc := core.New(core.Options{
		Connect: func(ctx context.Context) (core.Source, func(), error) {
			var err error
			srv, err = vault.StartServer(ctx, vault.ServeOptions{})
			if err != nil {
				return nil, nil, err
			}
			return vault.NewClient(srv.URL), srv.Stop, nil
		},
		SSH: connect.SSHOptions{KnownHosts: filepath.Join(cfgDir, "vaultcrab", "known_hosts")},
	})
	svc.Start(ctx)
	defer func() { srv.Stop() }()

	fmt.Fprintln(os.Stderr, "waiting for vault…")
	deadline := time.After(90 * time.Second)
	for st := svc.Status(); st.State != core.Online; st = svc.Status() {
		if st.State == core.Offline || st.State == core.Locked || st.State == core.NeedLogin {
			fmt.Fprintf(os.Stderr, "vault is %s: %v\n", st.State, st.Err)
			os.Exit(1)
		}
		select {
		case <-svc.Events():
		case <-deadline:
			fmt.Fprintln(os.Stderr, "timeout")
			os.Exit(1)
		}
	}
	m := &model{svc: svc}
	snap := svc.Snapshot()
	for _, name := range strings.Split(*hosts, ",") {
		for _, h := range snap.Hosts {
			if h.Name == strings.TrimSpace(name) {
				s, err := svc.OpenSession(h, 80, 24)
				if err == nil {
					m.tabs = append(m.tabs, s)
				}
			}
		}
	}
	if len(m.tabs) == 0 {
		fmt.Fprintln(os.Stderr, "no matching hosts")
		os.Exit(1)
	}
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if !*noMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	if _, err := tea.NewProgram(m, opts...).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	svc.Shutdown()
}

type model struct {
	svc     *core.Service
	tabs    []*core.Session
	active  int
	w, h    int
	prefix  bool
	history bool
}

type eventMsg struct{}

func (m *model) listen() tea.Cmd {
	return func() tea.Msg {
		<-m.svc.Events()
		return eventMsg{}
	}
}

func (m *model) Init() tea.Cmd { return m.listen() }

func (m *model) bodySize() (int, int) { return max(m.w, 1), max(m.h-2, 1) }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		// Throttle redraws to ~60 fps under heavy output.
		return m, tea.Tick(16*time.Millisecond, func(time.Time) tea.Msg { return m.listen()() })
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		w, h := m.bodySize()
		for _, s := range m.tabs {
			s.Resize(w, h)
		}
	case tea.MouseMsg:
		return m, m.mouse(msg)
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *model) cur() *core.Session { return m.tabs[m.active] }

func (m *model) key(msg tea.KeyMsg) tea.Cmd {
	s := m.cur()
	if m.prefix {
		m.prefix = false
		switch msg.String() {
		case "n", "right":
			m.active = (m.active + 1) % len(m.tabs)
		case "p", "left":
			m.active = (m.active + len(m.tabs) - 1) % len(m.tabs)
		case "x":
			s.Disconnect()
		case "[", "pgup":
			m.history = true
			if msg.String() == "pgup" {
				_, h := m.bodySize()
				s.Term.Scroll(h - 1)
			}
		case "q":
			return tea.Quit
		case "ctrl+b":
			s.Term.SendKey(core.Key{Raw: "\x02"})
		default:
			if r := msg.Runes; len(r) == 1 && r[0] >= '1' && r[0] <= '9' && int(r[0]-'1') < len(m.tabs) {
				m.active = int(r[0] - '1')
			}
		}
		return nil
	}
	if m.history {
		_, h := m.bodySize()
		switch msg.String() {
		case "pgup":
			s.Term.Scroll(h - 1)
		case "pgdown":
			s.Term.Scroll(-(h - 1))
		case "up", "k":
			s.Term.Scroll(1)
		case "down", "j":
			s.Term.Scroll(-1)
		case "home", "g":
			s.Term.Scroll(1 << 30)
		case "end", "G":
			s.Term.ScrollToBottom()
		case "esc", "q", "enter":
			m.history = false
			s.Term.ScrollToBottom()
		}
		return nil
	}
	switch msg.String() {
	case "ctrl+b":
		m.prefix = true
		return nil
	case "shift+up":
		s.Term.Scroll(1)
		return nil
	case "shift+down":
		s.Term.Scroll(-1)
		return nil
	}
	if st, _ := s.State(); st == core.SessionDisconnected {
		if msg.Type == tea.KeyEnter {
			s.Term.ScrollToBottom()
			s.Reconnect()
		}
		return nil
	}
	if msg.Paste {
		s.Term.Paste(string(msg.Runes))
		return nil
	}
	if k, ok := tui.TermKey(msg); ok {
		s.Term.ScrollToBottom()
		s.Term.SendKey(k)
	}
	return nil
}

func (m *model) mouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Y == 0 && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		// tab bar click
		x := 0
		for i, s := range m.tabs {
			w := lipgloss.Width(m.tabLabel(i, s))
			if msg.X >= x && msg.X < x+w {
				m.active = i
				return nil
			}
			x += w
		}
		return nil
	}
	s := m.cur()
	ev, ok := tui.TermMouse(msg, 0, 1)
	if !ok {
		return nil
	}
	if s.Term.SendMouse(ev) {
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		s.Term.Scroll(3)
	case tea.MouseButtonWheelDown:
		s.Term.Scroll(-3)
	}
	return nil
}

var (
	tabActive   = lipgloss.NewStyle().Bold(true).Reverse(true)
	tabInactive = lipgloss.NewStyle().Faint(true)
	statusStyle = lipgloss.NewStyle().Reverse(true)
)

func stateIcon(s *core.Session) string {
	st, _ := s.State()
	switch st {
	case core.SessionConnected:
		return "●"
	case core.SessionConnecting:
		return "…"
	case core.SessionDisconnected:
		return "✗"
	}
	return "○"
}

func (m *model) tabLabel(i int, s *core.Session) string {
	l := fmt.Sprintf(" %d:%s %s ", i+1, s.Host.Name, stateIcon(s))
	if i == m.active {
		return tabActive.Render(l)
	}
	return tabInactive.Render(l)
}

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range m.tabs {
		b.WriteString(m.tabLabel(i, s))
	}
	bar := b.String()
	if pad := m.w - lipgloss.Width(bar); pad > 0 {
		bar += strings.Repeat(" ", pad)
	}
	w, h := m.bodySize()
	s := m.cur()
	lines := s.Term.Render(w, h, core.RenderOpts{Cursor: true})
	st, err := s.State()
	status := fmt.Sprintf(" %s  %s", s.Host.Target(), st)
	if err != nil {
		status += ": " + err.Error()
	}
	if off := s.Term.ScrollOffset(); off > 0 {
		status += fmt.Sprintf("  [history -%d]", off)
	}
	if m.prefix {
		status += "  [prefix]"
	}
	if m.history {
		status += "  [HISTORY: PgUp/PgDn ↑/↓ Home/End, Esc — exit]"
	}
	status = truncate(status, m.w)
	status = statusStyle.Render(status + strings.Repeat(" ", max(0, m.w-lipgloss.Width(status))))
	return bar + "\n" + strings.Join(lines, "\n") + "\n" + status
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		return string(r[:w])
	}
	return s
}
