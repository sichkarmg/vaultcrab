// SPDX-License-Identifier: MIT

//go:build !windows

package widget

import (
	"context"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
	"testing"
	"time"
)

// Widget commands must not join the terminal's foreground process group,
// or terminals show their names in the window title.
func TestRunsInOwnSession(t *testing.T) {
	lines, err := Run(context.Background(), []string{"sh", "-c", "ps -o sid= -p $$"}, 5*time.Second)
	if err != nil || len(lines) == 0 {
		t.Skipf("ps: %v %q", err, lines)
	}
	sid, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
	own, _ := unix.Getsid(0)
	if sid == own || sid == 0 {
		t.Errorf("widget in our session %d", sid)
	}
}
