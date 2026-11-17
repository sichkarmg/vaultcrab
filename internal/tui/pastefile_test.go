// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"
)

func TestPasteFileHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"/etc/app.conf": "'/etc/app.conf'", "~/a b.txt": "~/'a b.txt'", "~": "~",
		"dir/it's": `~/'dir/it'\''s'`, "x": "~/'x'",
	} {
		if got := shellPath(in); got != want {
			t.Errorf("shellPath(%q) = %s, want %s", in, got, want)
		}
	}
	if displayPath("a/b") != "~/a/b" || displayPath("/a") != "/a" || displayPath("~/a") != "~/a" {
		t.Error("displayPath")
	}
	if d, b := splitPath("/etc/ng"); d != "/etc/" || b != "ng" {
		t.Errorf("split %q %q", d, b)
	}
	if listCmd("") != "cd -- ~ 2>/dev/null && ls -1Ap" || listCmd("/etc/") != "cd -- '/etc/' 2>/dev/null && ls -1Ap" {
		t.Errorf("listCmd %q", listCmd("/etc/"))
	}
	ls := []string{"./", "../", ".bashrc", ".config/", "nginx/", "netplan/", "networks", "hosts"}
	if got := strings.Join(completions(ls, "n"), " "); got != "nginx/ netplan/ networks" {
		t.Errorf("n: %s", got)
	}
	if got := strings.Join(completions(ls, "."), " "); got != ".bashrc .config/" {
		t.Errorf("dot: %s", got)
	}
	if commonPrefix([]string{"nginx/", "netplan/", "networks"}) != "n" || commonPrefix([]string{"файл1", "файл2"}) != "файл" {
		t.Error("commonPrefix")
	}
	if commonPrefix([]string{"мир", "мяч"}) != "м" {
		t.Errorf("cyrillic %q", commonPrefix([]string{"мир", "мяч"}))
	}
	// the marker is not a line of the text; a final line break is not doubled
	doc := hereDoc("f.txt", "a\r\nVAULTCRAB_EOF\nb\n", false)
	want := "cat > ~/'f.txt' << 'VAULTCRAB_EOF_2'\na\nVAULTCRAB_EOF\nb\nVAULTCRAB_EOF_2"
	if doc != want {
		t.Errorf("hereDoc:\n%s\nwant\n%s", doc, want)
	}
	if doc := hereDoc("/x", "", true); doc != "cat >> '/x' << 'VAULTCRAB_EOF'\nVAULTCRAB_EOF" {
		t.Errorf("empty: %q", doc)
	}
}
