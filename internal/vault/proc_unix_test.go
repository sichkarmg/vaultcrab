// SPDX-License-Identifier: MIT

//go:build !windows

package vault

import (
	"os/exec"
	"testing"
)

// bw serve runs in its own session without a controlling terminal, so
// terminals (macOS Terminal: "vaultcrab ▸ node") do not show it in the title.
func TestServeOwnSession(t *testing.T) {
	cmd := exec.Command("true")
	setProcAttrs(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid || cmd.SysProcAttr.Setpgid {
		t.Fatalf("attrs %+v", cmd.SysProcAttr)
	}
}
