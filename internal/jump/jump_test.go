// SPDX-License-Identifier: MIT

package jump

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseTarget(t *testing.T) {
	ok := []struct {
		in   string
		want Target
		str  string
	}{
		{"10.0.0.5", Target{Host: "10.0.0.5"}, "10.0.0.5"},
		{" 10.0.0.5 ", Target{Host: "10.0.0.5"}, "10.0.0.5"},
		{"root@10.0.0.5", Target{User: "root", Host: "10.0.0.5"}, "root@10.0.0.5"},
		{"root@10.0.0.5:2222", Target{User: "root", Host: "10.0.0.5", Port: 2222}, "root@10.0.0.5:2222"},
		{"ws-12.lab.local:22", Target{Host: "ws-12.lab.local", Port: 22}, "ws-12.lab.local:22"},
		{"fe80::1", Target{Host: "fe80::1"}, "[fe80::1]"},
		{"admin@[fe80::1]:2200", Target{User: "admin", Host: "fe80::1", Port: 2200}, "admin@[fe80::1]:2200"},
		{"[::1]", Target{Host: "::1"}, "[::1]"},
		{"user@host", Target{User: "user", Host: "host"}, "user@host"},
		{"a@b@host", Target{User: "a@b", Host: "host"}, "a@b@host"}, // the last @ splits
	}
	for _, c := range ok {
		got, err := ParseTarget(c.in)
		if err != nil || got != c.want || got.String() != c.str {
			t.Errorf("ParseTarget(%q) = %+v %q, %v; want %+v %q", c.in, got, got.String(), err, c.want, c.str)
		}
	}
	bad := []string{"", "  ", "@host", "host:", "host:0", "host:70000", "host:x", "two words",
		"[fe80::1", "[fe80::1]x", "bad_host!", "-lead.example", "fe80::zz", "10.0.0.300", "10.0.0", "1234", "user/x@host", strings.Repeat("a", 64) + ".com"}
	for _, in := range bad {
		if got, err := ParseTarget(in); err == nil {
			t.Errorf("ParseTarget(%q) = %+v, want an error", in, got)
		}
	}
	if a := (Target{Host: "fe80::1"}).Addr(22); a != "[fe80::1]:22" {
		t.Errorf("Addr = %q", a)
	}
}

func TestParseKey(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		in   string
		want Key
	}{
		{"", Key{}},
		{"agent", Key{Kind: KeyAgent}},
		{"password", Key{Kind: KeyPassword}},
		{"Agent", Key{Kind: KeyAgent}},
		{"vault: jump key", Key{Kind: KeyVault, Value: "jump key"}},
		{"~/.ssh/id_ed25519", Key{Kind: KeyFile, Value: filepath.Join(home, ".ssh", "id_ed25519")}},
		{"/etc/key", Key{Kind: KeyFile, Value: "/etc/key"}},
	}
	for _, c := range cases {
		got, err := ParseKey(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseKey(%q) = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	if _, err := ParseKey("vault:  "); err == nil {
		t.Error("vault: without an item accepted")
	}
}

// Frecency: frequent and recent entries first; an old frequent entry loses
// to a fresh one after enough half-lives; the limit drops the worst.
func TestHistoryOrder(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "h", "jump-history.json")
	h := NewHistory(path, 3, true)
	add := func(jump, target string, at time.Time, n int) {
		for range n {
			if err := h.Add(jump, target, "", "", "", at); err != nil {
				t.Fatal(err)
			}
		}
	}
	add("t:ws", "10.0.0.1", now.Add(-time.Hour), 3)       // frequent, recent
	add("t:ws", "10.0.0.2", now.Add(-time.Minute), 1)     // once, most recent
	add("t:ws", "10.0.0.3", now.Add(-60*24*time.Hour), 9) // frequent long ago
	targets := func(j string) []string {
		var out []string
		for _, e := range h.Targets(j, now) {
			out = append(out, e.Target)
		}
		return out
	}
	if got := targets("t:ws"); !slices.Equal(got, []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}) {
		t.Fatalf("order %v", got)
	}
	// a fourth entry: the limit 3 drops the one with the lowest score
	add("h:abc", "10.9.9.9", now, 1)
	if got := targets("t:ws"); !slices.Equal(got, []string{"10.0.0.1", "10.0.0.2"}) {
		t.Fatalf("after the limit %v", got)
	}
	if got := h.Jumps(now); !slices.Equal(got, []string{"t:ws", "h:abc"}) {
		t.Fatalf("jumps %v", got)
	}
	// persisted, and only names and addresses
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("mode %v", fi.Mode())
	}
	h2 := NewHistory(path, 3, true)
	if got := h2.Targets("h:abc", now); len(got) != 1 || got[0].Count != 1 {
		t.Fatalf("reloaded %+v (file %s)", got, b)
	}
	// the same target as another user is its own entry; the same user counts up
	h2.Add("h:abc", "10.9.9.9", "root", "", "", now)
	h2.Add("h:abc", "10.9.9.9", "root", "", "", now)
	if got := h2.Targets("h:abc", now); len(got) != 2 || got[0].User != "root" || got[0].Count != 2 {
		t.Fatalf("users %+v", got)
	}
	// the credential chosen last through a jump host is offered again
	h3 := NewHistory(filepath.Join(t.TempDir(), "h3.json"), 0, true)
	h3.Add("h:abc", "10.9.9.1", "", "", "password", now.Add(time.Minute))
	h3.Add("h:abc", "10.9.9.2", "", "", "vault:cred1", now.Add(2*time.Minute))
	h3.Add("h:abc", "10.9.9.3", "", "item9", "", now.Add(3*time.Minute)) // a vault target: not a choice
	if c := h3.LastCred("h:abc"); c != "vault:cred1" {
		t.Fatalf("last cred %q", c)
	}
	if c := h3.LastCred("t:none"); c != "" {
		t.Fatalf("last cred of an unused jump %q", c)
	}
	if err := h2.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("history file not deleted")
	}
	if had, err := ClearHistory(path); had || err != nil {
		t.Fatal(had, err)
	}
}

// A disabled history neither writes nor reads.
func TestHistoryDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jump-history.json")
	h := NewHistory(path, 0, false)
	if err := h.Add("t:ws", "10.0.0.1", "", "", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disabled history wrote the file")
	}
	h.Configure(0, true)
	h.Add("t:ws", "10.0.0.1", "", "", "", time.Now())
	h.Configure(0, false)
	if got := h.Targets("t:ws", time.Now()); len(got) != 0 {
		t.Fatalf("disabled history read %v", got)
	}
}

func TestMatch(t *testing.T) {
	if _, ok := Match("wks", "Workstations"); !ok {
		t.Error("subsequence not matched")
	}
	if _, ok := Match("xyz", "Workstations"); ok {
		t.Error("non-match matched")
	}
	if s, ok := Match("", "anything"); !ok || s != 0 {
		t.Error("empty query")
	}
	// a substring and word starts beat a scattered match
	a, _ := Match("jump", "lab-jump-01")
	b, _ := Match("jump", "j-u-m-p")
	if a <= b {
		t.Errorf("substring %d <= scattered %d", a, b)
	}
	c, _ := Match("10.0", "10.0.0.5")
	d, _ := Match("10.0", "1x0y.z0")
	if c <= d {
		t.Errorf("prefix %d <= scattered %d", c, d)
	}
}
