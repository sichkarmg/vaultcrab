// SPDX-License-Identifier: MIT

package screenlock

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestWatchLive waits for a real screen lock: run it in a graphical session
// with VAULTCRAB_SCREENLOCK_LIVE=<seconds> and lock the screen meanwhile
// (Linux: loginctl lock-session).
func TestWatchLive(t *testing.T) {
	secs := os.Getenv("VAULTCRAB_SCREENLOCK_LIVE")
	if secs == "" {
		t.Skip("VAULTCRAB_SCREENLOCK_LIVE not set")
	}
	d, err := time.ParseDuration(secs + "s")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	locked := make(chan struct{}, 4)
	if err := Watch(ctx, func() { locked <- struct{}{} }); err != nil {
		t.Fatalf("watch: %v", err)
	}
	t.Log("watching; lock the screen now")
	select {
	case <-locked:
		t.Log("screen lock seen")
	case <-ctx.Done():
		t.Fatal("no screen lock seen")
	}
}
