// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

func tunnelWeb(t *testing.T) (*httptest.Server, string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "pve") }))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	return srv, port
}

func fetch(t *testing.T, addr string) string {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	r, err := c.Get("http://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// waitTunnelUI waits for the only tunnel (or the one named) to be in state.
func waitTunnelUI(t *testing.T, m *Model, svc *core.Service, want core.TunnelState) core.TunnelInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.onEvent()
		for _, ti := range svc.Tunnels() {
			if ti.State == want {
				return ti
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no tunnel %v: %+v", want, svc.Tunnels())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// From the jump picker: Ctrl+T in step 2 opens the form of a tunnel to the
// typed host; the port typed, Ctrl+S starts it; the tab bar shows it, and
// it forwards. S saves it under a name, x stops it (the saved line stays),
// s starts it again, o opens it in the browser.
func TestTunnelFromPicker(t *testing.T) {
	jt := newJumpTest(t, "")
	m, svc := jt.m, jt.svc
	if err := svc.SaveJumpTemplates(svc.Config().JumpTemplates); err != nil { // in the file: saving reloads it
		t.Fatal(err)
	}
	_, port := tunnelWeb(t)
	keys(m, "j", "enter") // the template Workstations
	typeKeys(m, "admin@127.0.0.1")
	keys(m, "ctrl+t")
	tm, ok := m.modal.(*tunnelsModal)
	if !ok || tm.p.form == nil {
		t.Fatalf("no tunnel form: %T", m.modal)
	}
	f := tm.p.form
	if f.field != tfTarget || f.in[tfTarget].Value() != "127.0.0.1:" || f.in[tfJump].Value() != "Workstations" {
		t.Fatalf("form: field %d target %q jump %q", f.field, f.in[tfTarget].Value(), f.in[tfJump].Value())
	}
	typeKeys(m, port)
	keys(m, "ctrl+s")
	if tm.p.form != nil {
		t.Fatalf("not started: %s", f.err)
	}
	ti := waitTunnelUI(t, m, svc, core.TunnelActive)
	if ti.Via != "Workstations" || ti.Spec.Target != "127.0.0.1:"+port || !strings.HasPrefix(ti.Addr, "127.0.0.1:") {
		t.Fatalf("%+v", ti)
	}
	if got := fetch(t, ti.Addr); got != "pve" {
		t.Fatalf("got %q", got)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "● active") || !strings.Contains(v, "→ Workstations →") || !strings.Contains(v, "⇄1") ||
		!strings.Contains(v, "[open] [edit] [del] [x]") {
		t.Fatalf("view:\n%s", v)
	}

	// S: save it as "web"; the tunnel goes on under the name, on the same port
	keys(m, "S")
	if tm.p.form == nil || tm.p.form.field != tfName {
		t.Fatal("no save form")
	}
	typeKeys(m, "web")
	keys(m, "ctrl+s")
	if tm.p.form != nil {
		t.Fatalf("not saved: %s", tm.p.form.err)
	}
	saved := svc.SavedTunnels()
	if len(saved) != 1 || saved[0].Name != "web" || saved[0].Jump != "Workstations" || saved[0].Target != "127.0.0.1:"+port {
		t.Fatalf("saved %+v", saved)
	}
	ti2 := waitTunnelUI(t, m, svc, core.TunnelActive)
	if ti2.Spec.Name != "web" || ti2.Port != ti.Port || len(svc.Tunnels()) != 1 {
		t.Fatalf("after save %+v (%d tunnels)", ti2, len(svc.Tunnels()))
	}

	// o: the browser; y: the address copied
	keys(m, "o")
	jt.mu.Lock()
	urls := append([]string(nil), jt.urls...)
	jt.mu.Unlock()
	if len(urls) != 1 || urls[0] != "http://localhost:"+strconv.Itoa(ti.Port) {
		t.Fatalf("opened %v", urls)
	}

	// x: stopped, the saved line stays; s: started again
	keys(m, "x")
	if len(svc.Tunnels()) != 0 {
		t.Fatal("not stopped")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "○ stopped") || !strings.Contains(v, "web") || strings.Contains(v, "⇄") {
		t.Fatalf("after stop:\n%s", v)
	}
	keys(m, "s")
	waitTunnelUI(t, m, svc, core.TunnelActive)

	// prefix T from anywhere opens it; Esc closes
	keys(m, "esc")
	if m.modal != nil {
		t.Fatal("Esc did not close")
	}
	keys(m, "ctrl+b", "T")
	if _, ok := m.modal.(*tunnelsModal); !ok {
		t.Fatalf("prefix T: %T", m.modal)
	}
	keys(m, "esc")
	// the button of the tab bar
	m.Update(tea.MouseMsg{X: m.tabsWidth() + 2, Y: m.tabBarY(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if _, ok := m.modal.(*tunnelsModal); !ok {
		t.Fatalf("tab bar button: %T", m.modal)
	}

	// D D: removed from the config and stopped
	keys(m, "D", "D")
	if len(svc.SavedTunnels()) != 0 || len(svc.Tunnels()) != 0 {
		t.Fatalf("saved %d running %d", len(svc.SavedTunnels()), len(svc.Tunnels()))
	}
}

// A busy local port: the form offers a free one, Ctrl+S again uses it. A
// lost jump connection is told in the status bar, and so is the
// reconnection. T on an SSH host opens a tunnel through it to itself.
func TestTunnelBusyPortAndNotices(t *testing.T) {
	jt := newJumpTest(t, "")
	m, svc := jt.m, jt.svc
	_, port := tunnelWeb(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	bp := busy.Addr().(*net.TCPAddr).Port

	m.openTunnels(nil)
	keys(m, "a")
	tm := m.modal.(*tunnelsModal)
	f := tm.p.form
	if f == nil || f.field != tfJump {
		t.Fatal("no form on the jump field")
	}
	typeKeys(m, "Workstations")
	keys(m, "tab")
	typeKeys(m, "127.0.0.1:"+port)
	keys(m, "tab")
	typeKeys(m, strconv.Itoa(bp))
	keys(m, "ctrl+s")
	if tm.p.form == nil || !strings.Contains(f.err, fmt.Sprintf("port %d is busy", bp)) {
		t.Fatalf("busy port accepted: %q", f.err)
	}
	free, _ := strconv.Atoi(f.in[tfPort].Value())
	if free == bp || free == 0 {
		t.Fatalf("offered %d", free)
	}
	keys(m, "ctrl+s")
	if tm.p.form != nil {
		t.Fatalf("not started: %s", f.err)
	}
	ti := waitTunnelUI(t, m, svc, core.TunnelActive)
	if ti.Port != free {
		t.Fatalf("port %d, offered %d", ti.Port, free)
	}

	// the jump connection is lost: told, reconnected, told again
	m.toast = ""
	for _, c := range jt.jumpConns() {
		c.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(m.toast, "connected again") && !strings.Contains(m.toast, "connection lost") {
		if time.Now().After(deadline) {
			t.Fatalf("no notice: %q", m.toast)
		}
		m.onEvent()
		time.Sleep(time.Millisecond)
	}
	waitTunnelUI(t, m, svc, core.TunnelActive)
	if n := len(jt.jumpConns()); n != 2 {
		t.Fatalf("%d jump connections", n)
	}
	if got := fetch(t, ti.Addr); got != "pve" {
		t.Fatalf("after the reconnect %q", got)
	}

	// T on an SSH host of the list
	keys(m, "esc")
	keys(m, "/")
	typeKeys(m, "alpha")
	keys(m, "enter")
	m.modal = nil
	keys(m, "T")
	tm, ok := m.modal.(*tunnelsModal)
	if !ok || tm.p.form == nil {
		t.Fatalf("T: %T", m.modal)
	}
	if f := tm.p.form; f.in[tfJump].Value() != "alpha" || f.in[tfTarget].Value() != "localhost:" || f.field != tfTarget {
		t.Fatalf("form jump %q target %q", f.in[tfJump].Value(), f.in[tfTarget].Value())
	}
}

// The Tunnels dialog stays with the host list: other tabs can be used.
func TestTunnelsDocked(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	m.tabs = append(m.tabs, newTab(jt.svc.OpenLocal(80, 20)))
	m.openTunnels(nil)
	tm := m.modal.(*tunnelsModal)
	keys(m, "ctrl+b", "1")
	if m.active != 1 || m.parked != tm {
		t.Fatalf("active %d parked %T", m.active, m.parked)
	}
	keys(m, "ctrl+b", "T")
	if m.active != 0 || m.modal != tm {
		t.Fatalf("back: active %d modal %T", m.active, m.modal)
	}
}

// The ✕ of the dialog closes it, also with the form open.
func TestTunnelsCloseButton(t *testing.T) {
	jt := newJumpTest(t, "")
	m := jt.m
	m.openTunnels(nil)
	keys(m, "a")
	clickClose(t, m)
	if m.modal != nil {
		t.Fatalf("dialog not closed: %T", m.modal)
	}
}

// The buttons of a row: [x] stops, [start] starts, [edit] opens the form,
// [del] twice removes the saved tunnel.
func TestTunnelRowButtons(t *testing.T) {
	jt := newJumpTest(t, "")
	m, svc := jt.m, jt.svc
	if err := svc.SaveJumpTemplates(svc.Config().JumpTemplates); err != nil {
		t.Fatal(err)
	}
	_, port := tunnelWeb(t)
	if err := svc.SaveTunnels([]core.TunnelTemplate{{Name: "web", Jump: "Workstations", Target: "127.0.0.1:" + port}}); err != nil {
		t.Fatal(err)
	}
	m.openTunnels(nil)
	tm := m.modal.(*tunnelsModal)
	click := func(name string) {
		t.Helper()
		m.View()
		for _, b := range tm.p.btns[0] {
			if b.name == name {
				m.Update(tea.MouseMsg{X: m.dlgX + boxPadX + b.x0 + 1, Y: m.dlgY + tm.p.rowY[0], Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
				return
			}
		}
		t.Fatalf("no button %s: %+v", name, tm.p.btns[0])
	}
	click("start")
	waitTunnelUI(t, m, svc, core.TunnelActive)
	click("x")
	if len(svc.Tunnels()) != 0 {
		t.Fatal("[x] did not stop")
	}
	click("edit")
	if tm.p.form == nil || tm.p.form.edit != 0 {
		t.Fatal("[edit] did not open the form")
	}
	keys(m, "esc")
	click("del")
	if len(svc.SavedTunnels()) != 1 {
		t.Fatal("removed at the first click")
	}
	click("del")
	if len(svc.SavedTunnels()) != 0 {
		t.Fatal("[del] twice did not remove")
	}
}
