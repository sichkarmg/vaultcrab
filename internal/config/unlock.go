// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"time"
)

// Unlock is the lock policy of the vaults (unlock: in the config, Settings →
// Security).
type Unlock struct {
	// LockTimeout locks the vaults after this long without keyboard or
	// mouse input in Vaultcrab: a Go duration of at least 1m, or "never".
	// The settings offer LockTimeouts. Time asleep counts.
	LockTimeout string `yaml:"lock_timeout"`
	// LockOnScreenLock locks the vaults when the screen is locked (also a
	// closed lid that locks the screen). A sleep by itself does not lock.
	LockOnScreenLock bool `yaml:"lock_on_screen_lock"`
}

// DefaultLockTimeout is the lock timeout without unlock.lock_timeout.
const DefaultLockTimeout = "8h"

// LockTimeouts are the lock timeouts offered in the settings: the value of
// unlock.lock_timeout and its label.
var LockTimeouts = []struct{ Value, Label string }{
	{"15m", "15 min"}, {"1h", "1 hour"}, {"8h", "8 hours"}, {"24h", "1 day"}, {"72h", "3 days"},
	{"120h", "5 days"}, {"never", "never"},
}

// LongLockTimeout is the longest lock timeout the settings accept without
// a warning.
const LongLockTimeout = 8 * time.Hour

// Timeout returns the lock timeout (0 = never).
func (u Unlock) Timeout() time.Duration {
	d, _ := parseLockTimeout(u.LockTimeout)
	return d
}

func parseLockTimeout(s string) (time.Duration, error) {
	switch s {
	case "":
		s = DefaultLockTimeout
	case "never":
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Minute {
		return 0, fmt.Errorf("unlock.lock_timeout: %q (want a duration of at least 1m, like 8h, or never)", s)
	}
	return d, nil
}

// LockTimeoutLabel is how the settings show a lock timeout.
func LockTimeoutLabel(v string) string {
	if v == "" {
		v = DefaultLockTimeout
	}
	for _, t := range LockTimeouts {
		if t.Value == v {
			return t.Label
		}
	}
	return v
}

func (u *Unlock) finish() error {
	if u.LockTimeout == "" {
		u.LockTimeout = DefaultLockTimeout
	}
	_, err := parseLockTimeout(u.LockTimeout)
	return err
}
