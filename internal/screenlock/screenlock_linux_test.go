// SPDX-License-Identifier: MIT

package screenlock

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestSignals(t *testing.T) {
	sig := func(name string, path dbus.ObjectPath, body ...any) *dbus.Signal {
		return &dbus.Signal{Name: name, Path: path, Body: body}
	}
	for _, c := range []struct {
		s    *dbus.Signal
		want bool
	}{
		{sig("org.freedesktop.ScreenSaver.ActiveChanged", "/ScreenSaver", true), true},
		{sig("org.cinnamon.ScreenSaver.ActiveChanged", "/org/cinnamon/ScreenSaver", true), true},
		{sig("org.gnome.ScreenSaver.ActiveChanged", "/org/gnome/ScreenSaver", false), false}, // unlocked
		{sig("org.example.Player.ActiveChanged", "/", true), false},
		{sig("org.freedesktop.ScreenSaver.ActiveChanged", "/ScreenSaver"), false},
	} {
		if got := isScreensaverLock(c.s); got != c.want {
			t.Errorf("%s %v: %v", c.s.Name, c.s.Body, got)
		}
	}
	mine := isSessionLock("/org/freedesktop/login1/session/_32")
	if !mine(sig("org.freedesktop.login1.Session.Lock", "/org/freedesktop/login1/session/_32")) {
		t.Error("own session lock missed")
	}
	if mine(sig("org.freedesktop.login1.Session.Lock", "/org/freedesktop/login1/session/_33")) ||
		mine(sig("org.freedesktop.login1.Session.Unlock", "/org/freedesktop/login1/session/_32")) {
		t.Error("other session or unlock taken as a lock")
	}
}
