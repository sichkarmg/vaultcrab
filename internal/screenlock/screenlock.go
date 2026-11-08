// SPDX-License-Identifier: MIT

// Package screenlock reports when the user's screen is locked
// (unlock.lock_on_screen_lock):
//
//   - Linux: D-Bus signals: ActiveChanged(true) of the screensaver on the
//     session bus (org.freedesktop.ScreenSaver, GNOME, Cinnamon, MATE, …) and
//     Lock of the logind session of this process on the system bus
//     (loginctl lock-session, lock on lid close);
//   - Windows: WTS_SESSION_LOCK of the session (WTSRegisterSessionNotification);
//   - macOS: the com.apple.screenIsLocked distributed notification (also a
//     closed lid that locks the screen); needs cgo, so a build without cgo
//     cannot detect it.
//
// Watch returns an error when locks cannot be detected (no graphical
// session, e.g. over ssh): the caller shows it in the settings.
package screenlock

import (
	"context"
	"errors"
)

// ErrUnsupported means this build or system cannot detect screen locks.
var ErrUnsupported = errors.New("screen lock detection is not available on this system")

// Watch calls onLock (from another goroutine) whenever the screen is locked,
// until ctx ends.
func Watch(ctx context.Context, onLock func()) error { return watch(ctx, onLock) }
