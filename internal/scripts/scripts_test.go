// SPDX-License-Identifier: MIT

package scripts

import (
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/model"
)

func rule(t *testing.T, s string) filter.Rule {
	r, err := filter.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestForAndDefault(t *testing.T) {
	list := []Script{
		{Name: "ping", Command: []string{"ping", "{host}"}},
		{Name: "winbox", Rule: rule(t, "tag:mikrotik"), Command: []string{"winbox", "{host}"}, Mode: External},
		{Name: "rdp-only-win", Command: nil}, // no command for this OS
		{Name: "ssh", Rule: rule(t, "scheme:ssh"), Command: []string{"ssh", "{user}@{host}"}, Default: true},
	}
	ros := &model.Host{Scheme: model.SSH, Tags: []string{"mikrotik"}}
	names := func(l []Script) (s string) {
		for _, x := range l {
			s += x.Name + " "
		}
		return
	}
	if got := names(For(list, ros, nil)); got != "ping winbox ssh " {
		t.Errorf("for: %q", got)
	}
	if got := names(For(list, &model.Host{Scheme: model.RDP}, nil)); got != "ping " {
		t.Errorf("rdp: %q", got)
	}
	if s, ok := DefaultFor(list, ros, nil); !ok || s.Name != "ssh" {
		t.Errorf("default: %v %v", s.Name, ok)
	}
	ros.Fields = map[string]string{"script": "WinBox"}
	if s, ok := DefaultFor(list, ros, nil); !ok || s.Name != "winbox" {
		t.Errorf("item field: %v %v", s.Name, ok)
	}
	if _, ok := DefaultFor(list, &model.Host{Scheme: model.VNC}, nil); ok {
		t.Error("no default for vnc")
	}
}
