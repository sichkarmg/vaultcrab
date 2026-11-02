// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

type fakeSrc struct{ status string }

func (f *fakeSrc) Status(context.Context) (core.VaultInfo, error) {
	return core.VaultInfo{Status: core.VaultUnlocked, UserEmail: "t", ServerURL: "s"}, nil
}
func (f *fakeSrc) Unlock(context.Context, []byte) error { return nil }
func (f *fakeSrc) Sync(context.Context) error           { return nil }
func (f *fakeSrc) Snapshot(context.Context) (core.Snapshot, error) {
	return core.Snapshot{
		Folders: []core.Folder{{ID: "f", Name: "Lab/Linux", Path: []string{"Lab", "Linux"}}},
		Hosts: []core.Host{
			{ID: "1", Name: "alpha", Scheme: core.SchemeSSH, User: "u", Addr: "10.0.0.1", Port: 22, FolderID: "f", Tags: []string{"linux"}},
			{ID: "2", Name: "vnc-box", Scheme: core.SchemeVNC, Addr: "10.0.0.2", Port: 5900},
		},
		SyncedAt: time.Now(),
	}, nil
}
func (f *fakeSrc) Secret(context.Context, string) (core.Secret, error) {
	return core.Secret{}, errors.New("no secrets in tests")
}

func newTestModel(t *testing.T, ascii bool) (*Model, *core.Service) {
	t.Helper()
	cfgIcons := testIcons(ascii)
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		select {
		case <-svc.Events():
		case <-time.After(2 * time.Second):
			t.Fatal("not online")
		}
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActNextTab: "n", core.ActList: "l", core.ActCloseTab: "&"}, Icons: cfgIcons, ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return m, svc
}

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "ctrl+b":
			msg = tea.KeyMsg{Type: tea.KeyCtrlB}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func checkFrame(t *testing.T, v string, w, h int) {
	t.Helper()
	lines := strings.Split(v, "\n")
	if len(lines) != h {
		t.Fatalf("frame has %d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw != w {
			t.Errorf("line %d width %d, want %d: %q", i, lw, w, ansi.Strip(l))
		}
	}
}

func TestHostListRendersTree(t *testing.T) {
	m, _ := newTestModel(t, false)
	v := m.View()
	checkFrame(t, v, 120, 30)
	plain := ansi.Strip(v)
	for _, want := range []string{"Hosts", "Lab", "Linux", "alpha", "u@10.0.0.1", "vnc-box", "Online"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q in\n%s", want, plain)
		}
	}
}

func TestSmallAndOddSizes(t *testing.T) {
	m, _ := newTestModel(t, false)
	for _, sz := range [][2]int{{20, 5}, {1, 1}, {80, 3}, {200, 60}, {99, 24}} {
		m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		checkFrame(t, m.View(), sz[0], sz[1])
	}
}

func TestASCIIModeHasNoUnicodeChrome(t *testing.T) {
	m, _ := newTestModel(t, true)
	keys(m, "?") // help modal with a border
	plain := ansi.Strip(m.View())
	for _, r := range plain {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q in ASCII mode:\n%s", r, plain)
		}
	}
}

func TestOpenSessionTabAndClose(t *testing.T) {
	m, svc := newTestModel(t, false)
	keys(m, "/")
	keys(m, "a", "l", "p", "h", "a", "enter", "enter")
	if len(m.tabs) != 1 || m.active != 1 {
		t.Fatalf("tabs=%d active=%d", len(m.tabs), m.active)
	}
	// session fails (no secret) -> disconnected, tab stays with an error
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, _ := m.tabs[0].focused().State()
		if st == core.SessionDisconnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session did not fail")
		}
		time.Sleep(10 * time.Millisecond)
	}
	checkFrame(t, m.View(), 120, 30)
	if !strings.Contains(ansi.Strip(m.View()), "no secrets in tests") {
		t.Error("error not shown")
	}
	keys(m, "ctrl+b", "l")
	if m.active != 0 {
		t.Error("prefix l must go to the host list")
	}
	keys(m, "ctrl+b", "n")
	if m.active != 1 {
		t.Error("prefix n must go to the next tab")
	}
	keys(m, "ctrl+b", "&") // disconnected: closes without confirmation
	if len(m.tabs) != 0 || len(svc.Sessions()) != 0 {
		t.Fatalf("tab not closed: %d %d", len(m.tabs), len(svc.Sessions()))
	}
}

func TestMouseTabClick(t *testing.T) {
	m, _ := newTestModel(t, false)
	keys(m, "/", "a", "l", "p", "enter", "enter")
	if m.active != 1 {
		t.Fatal("no tab")
	}
	m.Update(tea.MouseMsg{X: 2, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.active != 0 {
		t.Error("click on Hosts tab")
	}
}

func TestPanes(t *testing.T) {
	m, svc := newTestModel(t, false)
	m.opt.Keys = map[string]string{core.ActSplitRight: "|", core.ActSplitDown: "-", core.ActClosePane: "x", core.ActZoom: "z", core.ActBreakPane: "!", core.ActFocusLeft: "left"}
	m.keymap = map[string]string{}
	for a, k := range m.opt.Keys {
		m.keymap[k] = a
	}
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "enter")
	keys(m, "ctrl+b", "|")
	if _, ok := m.modal.(*pickerModal); !ok {
		t.Fatal("no picker")
	}
	keys(m, "enter")
	tb := m.curTab()
	if tb.ws.Len() != 2 {
		t.Fatalf("panes %d", tb.ws.Len())
	}
	checkFrame(t, m.View(), 120, 30)
	// body is 120x28: panes of 60 and 59 + separator, minus 1 header row
	for i, s := range tb.ws.Sessions() {
		if w, h := s.Term.Size(); w != 60-i || h != 27 {
			t.Errorf("pane %d term %dx%d", i, w, h)
		}
	}
	keys(m, "ctrl+b", "-")
	keys(m, "enter")
	if tb.ws.Len() != 3 {
		t.Fatal("split down")
	}
	checkFrame(t, m.View(), 120, 30)
	// drag the vertical separator (x=60, body y=5 -> screen y=6) to x=40
	m.Update(tea.MouseMsg{X: 60, Y: 6, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if tb.drag == nil {
		t.Fatal("separator not grabbed")
	}
	m.Update(tea.MouseMsg{X: 40, Y: 6, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m.Update(tea.MouseMsg{X: 40, Y: 6, Action: tea.MouseActionRelease})
	if w, _ := tb.ws.Sessions()[0].Term.Size(); w != 40 {
		t.Errorf("left pane width after drag: %d", w)
	}
	checkFrame(t, m.View(), 120, 30)
	keys(m, "ctrl+b", "z")
	if !tb.ws.Zoomed() {
		t.Error("zoom")
	}
	checkFrame(t, m.View(), 120, 30)
	keys(m, "ctrl+b", "z")
	keys(m, "ctrl+b", "!")
	if len(m.tabs) != 2 || tb.ws.Len() != 2 {
		t.Fatalf("break pane: tabs=%d panes=%d", len(m.tabs), tb.ws.Len())
	}
	m.setActive(1)
	// the sessions fail to connect (no secret) asynchronously; only then does
	// closing a pane needs no confirmation (slow CI runners are not there yet)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if st, _ := tb.focused().State(); st != core.SessionConnected && st != core.SessionConnecting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session is still connecting")
		}
	}
	keys(m, "ctrl+b", "x") // sessions are disconnected (no secret): no confirmation
	if tb.ws.Len() != 1 || len(svc.Sessions()) != 2 {
		t.Fatalf("close pane: panes=%d sessions=%d", tb.ws.Len(), len(svc.Sessions()))
	}
	checkFrame(t, m.View(), 120, 30)
	if !strings.Contains(m.tabBar(), m.opt.Icons.Get(core.IconDisconnected)) {
		t.Error("tab bar must show the broken state")
	}
}

func TestQuitButton(t *testing.T) {
	m, _ := newTestModel(t, false)
	top := strings.Split(ansi.Strip(m.View()), "\n")[0]
	// separated from the tabs and the settings; unlike ✗ of a broken session
	if !strings.HasSuffix(top, "│ ⚙ │ ⊗ ") {
		t.Fatalf("no close button: %q", top)
	}
	// the separator before it belongs to the close button
	m0, _ := newTestModel(t, false)
	if _, cmd := m0.Update(tea.MouseMsg{X: 116, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}); cmd == nil {
		t.Fatal("click on the separator before ⊗")
	}
	// no sessions: quits right away
	_, cmd := m.Update(tea.MouseMsg{X: 118, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatal("click on ⊗ did not quit")
	}
	// with a live session: asks first
	m2, _ := newTestModel(t, false)
	m2.tabs = append(m2.tabs, newTab(m2.svc.OpenLocal(80, 20)))
	defer m2.svc.Shutdown()
	for st, _ := m2.tabs[0].focused().State(); st == core.SessionIdle; st, _ = m2.tabs[0].focused().State() {
		time.Sleep(10 * time.Millisecond)
	}
	m2.Update(tea.MouseMsg{X: 119, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if _, ok := m2.modal.(*confirmModal); !ok {
		t.Error("no confirmation with an open session")
	}
	m3, _ := newTestModel(t, true)
	if top := strings.Split(ansi.Strip(m3.View()), "\n")[0]; !strings.HasSuffix(top, "| = | Q ") {
		t.Errorf("ascii close button: %q", top)
	}
}

func TestVersionInStatusBar(t *testing.T) {
	m, _ := newTestModel(t, false)
	m.opt.Version = "v9.8.7"
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if st := lines[len(lines)-1]; !strings.HasSuffix(st, " Vaultcrab v9.8.7 ") {
		t.Errorf("status bar: %q", st)
	}
	checkFrame(t, m.View(), 120, 30)
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlB}) // the prefix indicator takes its place
	lines = strings.Split(ansi.Strip(m.View()), "\n")
	if st := lines[len(lines)-1]; !strings.HasSuffix(st, " [ctrl+b] ") {
		t.Errorf("prefix: %q", st)
	}
}

func TestConfirmButtonsMouse(t *testing.T) {
	m, _ := newTestModel(t, false)
	closed := 0
	for _, btn := range []int{1, 0} {
		m.modal = newConfirm(`Close "alpha"?`, func(*Model) tea.Cmd { closed++; return nil })
		frame := strings.Split(ansi.Strip(m.View()), "\n")
		c := m.modal.(*confirmModal)
		if !strings.Contains(frame[m.dlgY+c.row], "[ Close ]") || !strings.Contains(frame[m.dlgY+c.row], "[ Cancel ]") {
			t.Fatalf("buttons not at row %d:\n%s", c.row, strings.Join(frame, "\n"))
		}
		x := m.dlgX + boxPadX + c.spans[btn][0] + 1
		m.Update(tea.MouseMsg{X: x, Y: m.dlgY + c.row, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		if m.modal != nil {
			t.Fatal("click did not close the dialog")
		}
	}
	if closed != 1 {
		t.Fatalf("yes ran %d times", closed)
	}
}
