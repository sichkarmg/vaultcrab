// SPDX-License-Identifier: MIT

package screenlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
)

func watch(ctx context.Context, onLock func()) error {
	var errs []error
	n := 0
	if c, err := dbus.ConnectSessionBus(); err != nil {
		errs = append(errs, fmt.Errorf("session bus: %w", err))
	} else if err := watchBus(ctx, c, screensaverRules, isScreensaverLock, onLock); err != nil {
		c.Close()
		errs = append(errs, fmt.Errorf("session bus: %w", err))
	} else {
		n++
	}
	if c, err := dbus.ConnectSystemBus(); err != nil {
		errs = append(errs, fmt.Errorf("system bus: %w", err))
	} else if path, err := sessionPath(c); err != nil {
		c.Close()
		errs = append(errs, fmt.Errorf("logind: %w", err))
	} else if err := watchBus(ctx, c, logindRules(path), isSessionLock(path), onLock); err != nil {
		c.Close()
		errs = append(errs, fmt.Errorf("logind: %w", err))
	} else {
		n++
	}
	if n == 0 {
		return fmt.Errorf("%w: %v", ErrUnsupported, errors.Join(errs...))
	}
	return nil
}

var screensaverRules = []dbus.MatchOption{dbus.WithMatchMember("ActiveChanged")}

func logindRules(path dbus.ObjectPath) []dbus.MatchOption {
	return []dbus.MatchOption{dbus.WithMatchInterface("org.freedesktop.login1.Session"),
		dbus.WithMatchMember("Lock"), dbus.WithMatchObjectPath(path)}
}

// isScreensaverLock: ActiveChanged(true) of a *.ScreenSaver interface.
func isScreensaverLock(s *dbus.Signal) bool {
	if !strings.HasSuffix(s.Name, "ScreenSaver.ActiveChanged") || len(s.Body) == 0 {
		return false
	}
	on, ok := s.Body[0].(bool)
	return ok && on
}

func isSessionLock(path dbus.ObjectPath) func(*dbus.Signal) bool {
	return func(s *dbus.Signal) bool {
		return s.Name == "org.freedesktop.login1.Session.Lock" && s.Path == path
	}
}

// sessionPath is the logind session of this process (XDG_SESSION_ID, or
// the session of our PID).
func sessionPath(c *dbus.Conn) (dbus.ObjectPath, error) {
	mgr := c.Object("org.freedesktop.login1", "/org/freedesktop/login1")
	var p dbus.ObjectPath
	if id := os.Getenv("XDG_SESSION_ID"); id != "" {
		if err := mgr.Call("org.freedesktop.login1.Manager.GetSession", 0, id).Store(&p); err == nil {
			return p, nil
		}
	}
	err := mgr.Call("org.freedesktop.login1.Manager.GetSessionByPID", 0, uint32(os.Getpid())).Store(&p)
	return p, err
}

func watchBus(ctx context.Context, c *dbus.Conn, rules []dbus.MatchOption, match func(*dbus.Signal) bool, onLock func()) error {
	if err := c.AddMatchSignal(rules...); err != nil {
		return err
	}
	ch := make(chan *dbus.Signal, 8)
	c.Signal(ch)
	go func() {
		defer c.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-ch:
				if !ok {
					return
				}
				if match(s) {
					onLock()
				}
			}
		}
	}()
	return nil
}
