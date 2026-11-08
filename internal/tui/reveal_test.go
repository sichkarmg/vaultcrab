// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// revealSrc is editSrc whose items have a password.
type revealSrc struct {
	editSrc
	pw string
}

func (p *revealSrc) Secret(context.Context, string) (core.Secret, error) {
	return core.Secret{Value: []byte(p.pw)}, nil
}

// Ctrl+R or [Show] in the item form shows what is typed and, while nothing
// is typed, the current password from the vault (a key by its first line);
// it is never put into the field (saving keeps it).
func TestFormPasswordReveal(t *testing.T) {
	src := &revealSrc{editSrc: editSrc{updated: map[string]core.HostEdit{}}, pw: "Curr3nt-PW"}
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return src, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	keys(m, "/", "a", "l", "p", "h", "a", "enter", "e")
	f := m.modal.(*hostForm)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "[Show]") || strings.Contains(v, "Curr3nt-PW") {
		t.Fatalf("form:\n%s", v)
	}
	f.setFocus(fPassword)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	runCmd(m, cmd)
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "current: Curr3nt-PW") || !strings.Contains(v, "[Hide]") || f.in[fPassword].Value() != "" {
		t.Fatalf("not shown:\n%s", v)
	}
	// typed text is shown too
	typeText(m, "new-one")
	if v := ansi.Strip(m.View()); !strings.Contains(v, "new-one") {
		t.Fatalf("typed text hidden:\n%s", v)
	}
	// hide: masked, the current one forgotten
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if v := ansi.Strip(m.View()); strings.Contains(v, "new-one") || strings.Contains(v, "Curr3nt-PW") || f.pwCur != "" {
		t.Fatalf("still shown:\n%s", v)
	}
	// the mouse: a click on [Show]
	m.View()
	_, cmd = m.Update(tea.MouseMsg{X: m.dlgX + f.pwBtnX + 1, Y: m.dlgY + f.pwRow, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	runCmd(m, cmd)
	if !f.pwShow || f.pwCur != "Curr3nt-PW" {
		t.Fatalf("click: show %v cur %q", f.pwShow, f.pwCur)
	}
	keys(m, "esc")
	if f.pwCur != "" {
		t.Fatal("kept after closing")
	}

	// a private key: its first line and the number of lines
	pk := "OPENSSH " + "PRIVATE KEY" // split: not a key to secret scanners
	src.pw = "-----BEGIN " + pk + "-----\nAAAA\nBBBB\n-----END " + pk + "-----\n"
	keys(m, "e")
	f = m.modal.(*hostForm)
	f.setFocus(fPassword)
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	runCmd(m, cmd)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "current: -----BEGIN "+pk+"----- … (a private key, 4 lines)") || strings.Contains(v, "AAAA") {
		t.Fatalf("key:\n%s", v)
	}
	// saving with nothing typed keeps the password
	enterAndRun(t, m)
	src.mu.Lock()
	u := src.updated["1"]
	src.mu.Unlock()
	if u.Password != nil {
		t.Fatalf("password changed: %q", u.Password)
	}
}
