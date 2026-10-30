// SPDX-License-Identifier: MIT

package clipboard

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestOSC52(t *testing.T) {
	s := OSC52("привет\nworld")
	if !strings.HasPrefix(s, "\x1b]52;c;") || !strings.HasSuffix(s, "\a") {
		t.Fatalf("%q", s)
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(s, "\x1b]52;c;"), "\a"))
	if err != nil || string(b) != "привет\nworld" {
		t.Errorf("payload %q %v", b, err)
	}
	if OSC52(strings.Repeat("x", maxOSC52+1)) != "" {
		t.Error("too long text must not be sent")
	}
}
