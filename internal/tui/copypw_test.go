// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

type pwSrc struct{ fakeSrc }

func (pwSrc) Secret(context.Context, string) (core.Secret, error) {
	return core.SecretFromString("s3cr3t-pw"), nil
}

func TestCopyPasswordButton(t *testing.T) {
	svc := core.New(core.Options{Connect: func(context.Context) (core.Source, func(), error) { return &pwSrc{}, nil, nil }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.Start(ctx)
	for svc.Status().State != core.Online {
		<-svc.Events()
	}
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{}, Icons: testIcons(false), ShowTree: true, CopyOSC52: true})
	m.onEvent()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 34})
	keys(m, "/", "a", "l", "p", "h", "a", "enter")
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Copy password (p)") || strings.Contains(v, "Copied to clipboard") {
		t.Fatalf("button:\n%s", v)
	}
	// click on the button (body starts at y=1)
	l := &m.list
	_, cmd := m.Update(tea.MouseMsg{X: l.pwButtonX + 3, Y: 1 + l.listTop + l.pwButtonRow, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil {
		t.Fatal("click did not copy")
	}
	m.Update(cmd())
	frame := m.View()
	if !strings.Contains(frame, base64.StdEncoding.EncodeToString([]byte("s3cr3t-pw"))) {
		t.Error("password not sent to the terminal clipboard (OSC 52)")
	}
	// only the terminal was asked: no green check (it may ignore OSC 52)
	if !strings.Contains(ansi.Strip(frame), "! sent to the terminal only (OSC 52)") {
		t.Errorf("no confirmation:\n%s", ansi.Strip(frame))
	}
	if strings.Contains(ansi.Strip(frame), "s3cr3t-pw") {
		t.Error("password shown on screen")
	}
	checkFrame(t, frame, 120, 34)
	// the hotkey
	m.pwCopiedHost = ""
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if cmd == nil {
		t.Fatal("p did not copy")
	}
	m.Update(cmd())
	if m.pwCopiedHost != "1" {
		t.Error("p: not copied")
	}
	// the system clipboard worked: the green check
	m.passwordCopied(passwordCopiedMsg{hostID: "1", text: "x"})
	m.opt.CopySystem = true
	m.passwordCopied(passwordCopiedMsg{hostID: "1", text: "x"})
	if v := ansi.Strip(m.View()); !strings.Contains(v, "✓ Copied to clipboard") {
		t.Errorf("no check:\n%s", v)
	}
	// it failed and OSC 52 is off: an error, no confirmation
	m.opt.CopyOSC52 = false
	m.passwordCopied(passwordCopiedMsg{hostID: "1", text: "x", sysErr: errors.New("no system clipboard")})
	if v := ansi.Strip(m.View()); strings.Contains(v, "Copied to clipboard") || !strings.Contains(v, "no system clipboard") {
		t.Errorf("failure not shown:\n%s", v)
	}
}
