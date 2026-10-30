// SPDX-License-Identifier: MIT

package tui

import (
	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
)

// testIcons builds an icon set the way main does (config is test-only here).
func testIcons(ascii bool) core.IconSet {
	c := config.Default()
	c.Icons.Mode = "unicode"
	if ascii {
		c.Icons.Mode = "ascii"
	}
	return c.IconSet()
}
