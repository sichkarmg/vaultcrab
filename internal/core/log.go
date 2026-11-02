// SPDX-License-Identifier: MIT

package core

import (
	"github.com/sichkarmg/vaultcrab/internal/applog"
	"github.com/sichkarmg/vaultcrab/internal/config"
)

// The event log (log.enabled, Settings → Behavior → Event log): vault
// states, connections, external programs, config reloads and errors are
// written by internal/applog. Never secrets: only names, states and error
// texts are logged.

// applyLog opens or closes the event log as c says.
func applyLog(c config.Config) error {
	if !c.Log.Enabled {
		if applog.Enabled() {
			applog.Printf("log: off")
			applog.Close()
		}
		return nil
	}
	p := c.LogFile()
	if applog.Enabled() && applog.Path() == p {
		return nil
	}
	if err := applog.Open(p); err != nil {
		return err
	}
	applog.Printf("log: on")
	return nil
}

// SetLogging turns the event log on or off for the configuration in use
// (saving it to the config file is the caller's job).
func (s *Service) SetLogging(on bool) error {
	s.mu.Lock()
	s.conf.cfg.Log.Enabled = on
	c := s.conf.cfg
	s.mu.Unlock()
	return applyLog(c)
}

// LogEnabled reports whether the event log is being written.
func (s *Service) LogEnabled() bool { return applog.Enabled() }

// LogFile returns the file of the event log (also while it is off).
func (s *Service) LogFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conf.cfg.LogFile()
}

// LogTail returns the last n lines of the event log file.
func (s *Service) LogTail(n int) ([]string, error) { return applog.Tail(s.LogFile(), n) }

// logf writes an event to the log (a no-op while it is off).
func logf(format string, args ...any) { applog.Printf(format, args...) }
