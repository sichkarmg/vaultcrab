// SPDX-License-Identifier: MIT

package applog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "vaultcrab.log")
	Printf("dropped while off")
	if Enabled() {
		t.Fatal("enabled before Open")
	}
	if err := Open(p); err != nil {
		t.Fatal(err)
	}
	defer Close()
	Printf("vault: %s", "online")
	Printf("two\nlines")
	lines, err := Tail(p, 10)
	if err != nil || len(lines) != 2 {
		t.Fatalf("lines %q %v", lines, err)
	}
	if !strings.HasSuffix(lines[0], " vault: online") || !strings.HasSuffix(lines[1], "two ⏎ lines") {
		t.Errorf("lines %q", lines)
	}
	if st, _ := os.Stat(p); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if Path() != p {
		t.Errorf("path %q", Path())
	}
	Close()
	Printf("after close")
	if lines, _ := Tail(p, 10); len(lines) != 2 {
		t.Errorf("written after Close: %q", lines)
	}
}

func TestRotate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vaultcrab.log")
	if err := Open(p); err != nil {
		t.Fatal(err)
	}
	defer Close()
	long := strings.Repeat("x", 1000)
	for i := range 2500 { // ~2.5 MiB: rotated twice
		Printf("%d %s", i, long)
	}
	for _, f := range []string{p, p + ".1"} {
		if st, err := os.Stat(f); err != nil || st.Size() > MaxSize {
			t.Fatalf("%s: %v %v", f, st, err)
		}
	}
	lines, err := Tail(p, 1000) // the current part and .1 hold more than that
	if err != nil || len(lines) != 1000 || !strings.Contains(lines[999], " 2499 ") {
		t.Fatalf("tail: %d lines, last %.40q, %v", len(lines), lines[len(lines)-1], err)
	}
}
