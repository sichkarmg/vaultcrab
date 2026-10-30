// SPDX-License-Identifier: MIT

//go:build linux || freebsd || openbsd || netbsd

package clipboard

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestX11 needs an X server (e.g. xvfb-run go test ./internal/clipboard).
func TestX11(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("no X display")
	}
	owner, other := newX11(), newX11()
	if owner.initErr != nil || other.initErr != nil {
		t.Fatal(owner.initErr, other.initErr)
	}
	const text = "pässwörd 123"
	if err := owner.write(text); err != nil {
		t.Fatal(err)
	}
	// another X client pastes it
	if got, err := other.read(); err != nil || got != text {
		t.Fatalf("read %q %v", got, err)
	}
	if path, err := exec.LookPath("xclip"); err == nil {
		out, err := exec.Command(path, "-o", "-selection", "clipboard").Output()
		if err != nil || string(out) != text {
			t.Fatalf("xclip %q %v", out, err)
		}
		// another program takes the clipboard: we drop our copy
		cmd := exec.Command(path, "-i", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader("other")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		var got string
		for i := 0; i < 50 && got != "other"; i++ { // SelectionClear arrives asynchronously
			got, _ = owner.read()
			time.Sleep(10 * time.Millisecond)
		}
		if got != "other" {
			t.Fatalf("after xclip: %q", got)
		}
	}
	// "" gives the clipboard up
	_ = owner.write(text)
	if err := owner.write(""); err != nil {
		t.Fatal(err)
	}
	if got, _ := other.read(); got != "" {
		t.Fatalf("not cleared: %q", got)
	}
}
