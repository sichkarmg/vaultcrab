// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Debug logging of UI geometry events (window size, mouse) to the file named
// by VAULTCRAB_DEBUG_LOG. Keyboard input is never logged: it may contain secrets.
var (
	debugOnce sync.Once
	debugFile *os.File
)

func debugf(format string, args ...any) {
	debugOnce.Do(func() {
		if p := os.Getenv("VAULTCRAB_DEBUG_LOG"); p != "" {
			debugFile, _ = os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		}
	})
	if debugFile != nil {
		fmt.Fprintf(debugFile, time.Now().Format("15:04:05.000 ")+format+"\n", args...)
	}
}
