// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTunnelsConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`# my config
tunnels:
  - name: Proxmox web
    jump: Workstations
    target: 192.168.1.10:8006
    local_port: auto
    open: "https://localhost:{port}"
  - name: db
    jump: lab-jump
    target: "[fd00::5]:5432"
    local_port: 15432
  - name: proxy
    jump: lab-jump
    type: socks
    local_port: 1080
tunnel:
  browser: [firefox, "{url}"]
`), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tunnels) != 3 || c.Tunnels[0].LocalPort != 0 || c.Tunnels[1].LocalPort != 15432 || c.Tunnels[2].Kind() != TunnelSOCKS {
		t.Fatalf("%+v", c.Tunnels)
	}
	if c.Tunnel.Attempts != DefaultTunnelAttempts || len(c.Tunnel.Browser) != 2 || c.Tunnel.Browser[1] != "{url}" {
		t.Fatalf("%+v", c.Tunnel)
	}
	if Default().Tunnel.Attempts != DefaultTunnelAttempts || Default().UI.Keys[ActTunnels] != "T" {
		t.Fatal("defaults")
	}

	// saved: the rest of the file is kept, and it loads the same
	ts := append(c.Tunnels, Tunnel{Name: "ssh", Jump: "h", Target: "10.0.0.5:22", LocalPort: 2222, Bind: "0.0.0.0"})
	if err := SaveTunnels(path, ts); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "# my config") || !strings.Contains(string(b), "local_port: auto") {
		t.Fatalf("file:\n%s", b)
	}
	c2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c2.Tunnels) != 4 || c2.Tunnels[3].Bind != "0.0.0.0" || c2.Tunnels[3].LocalPort != 2222 || c2.Tunnels[0].Open != "https://localhost:{port}" {
		t.Fatalf("%+v", c2.Tunnels)
	}

	for _, bad := range []Tunnel{
		{Jump: "j", Target: "h:1"},
		{Name: "n", Target: "h:1"},
		{Name: "n", Jump: "j", Target: "h"},
		{Name: "n", Jump: "j", Target: "h:0"},
		{Name: "n", Jump: "j", Target: "h:70000"},
		{Name: "n", Jump: "j", Type: "socks", Target: "h:1"},
		{Name: "n", Jump: "j", Type: "remote", Target: "h:1"},
		{Name: "n", Jump: "j", Target: "h:1", LocalPort: 70000},
		{Name: "n", Jump: "j", Target: "h:1", Bind: "my-laptop"},
	} {
		if bad.Check() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := SaveTunnels(path, []Tunnel{{Name: "a", Jump: "j", Target: "h:1"}, {Name: "A", Jump: "j", Target: "h:2"}}); err == nil {
		t.Error("a name twice accepted")
	}
	for in, want := range map[string]LocalPort{"": 0, "auto": 0, "AUTO": 0, "8006": 8006} {
		if p, err := ParseLocalPort(in); err != nil || p != want {
			t.Errorf("%q: %v %v", in, p, err)
		}
	}
	for _, in := range []string{"x", "-1", "65536"} {
		if _, err := ParseLocalPort(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	os.WriteFile(path, []byte("tunnels:\n  - {name: x, jump: j, target: 'h:1', local_port: soon}\n"), 0o600)
	if _, err := Load(path); err == nil {
		t.Error("local_port: soon accepted")
	}
}
