// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJumpTemplatesLoadAndCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`# my config
jump_templates:
  - name: Workstations
    jump: lab-jump
    user: admin
    port: 2222
    key: ~/.ssh/id_ed25519
    jump_key: agent
    host_key_check: accept-new
jump:
  history: false
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.JumpTemplates) != 1 || c.JumpTemplates[0].Port != 2222 || c.JumpTemplates[0].HostKeys() != "accept-new" ||
		c.Jump.History || c.Jump.HistoryLimit != 200 {
		t.Fatalf("%+v %+v", c.JumpTemplates, c.Jump)
	}
	if d := Default(); !d.Jump.History || d.UI.Keys[ActJump] != "j" || d.UI.Keys[ActRenameTab] != "R" {
		t.Fatalf("defaults %+v %v", d.Jump, d.UI.Keys)
	}
	for _, bad := range []string{
		"jump_templates: [{name: x}]",                                 // no jump
		"jump_templates: [{jump: a}]",                                 // no name
		"jump_templates: [{name: x, jump: a, port: 70000}]",           // port
		"jump_templates: [{name: x, jump: a, host_key_check: maybe}]", // check
		"jump_templates: [{name: x, jump: a, key: 'vault:'}]",         // empty vault ref
		"jump_templates: [{name: x, jump: a}, {name: X, jump: b}]",    // duplicate name
		"jump_templates: [{name: x, jump: a, typo: 1}]",               // unknown key
		"jump: {history_limit: -1}",
	} {
		write(bad)
		if _, err := Load(path); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}

	// saving keeps the rest of the file and its comments
	write("# my config\nui:\n  mouse: false # no mouse\n")
	ts := []JumpTemplate{{Name: "WS", Jump: "root@10.0.0.1:2222", Key: "agent"}, {Name: "Lab", Jump: "lab", Port: 22}}
	if err := SaveJumpTemplates(path, ts); err != nil {
		t.Fatal(err)
	}
	if err := SetJumpHistory(path, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# my config") || !strings.Contains(string(b), "# no mouse") {
		t.Fatalf("comments lost:\n%s", b)
	}
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.JumpTemplates) != 2 || c.JumpTemplates[0] != ts[0] || c.JumpTemplates[1] != ts[1] || c.Jump.History || c.UI.Mouse {
		t.Fatalf("%+v %+v\n%s", c.JumpTemplates, c.Jump, b)
	}
	if err := SaveJumpTemplates(path, []JumpTemplate{{Name: "a", Jump: "x"}, {Name: "A", Jump: "y"}}); err == nil {
		t.Fatal("duplicate names saved")
	}
}
