// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"
)

// configReloaded reports a reload of the config file done by core (the
// file changed, r, a save from the settings) and applies the appearance.
func (m *Model) configReloaded() {
	st := m.svc.ConfigStatus()
	if st.Seq == m.cfgSeq {
		return
	}
	m.cfgSeq = st.Seq
	if st.Err != nil {
		m.notify(true, "config not applied, the previous one stays in use: %v", st.Err)
		return
	}
	cfg := m.svc.Config()
	if a := cfg.Appearance(); a != m.opt.Appearance {
		m.applyAppearance(a)
	}
	if p := m.programs; p != nil && p.ready && !p.dirty() {
		p.init(m) // show what the file has now
	}
	msg := "config re-read: external programs, scripts and appearance applied"
	if len(st.Restart) > 0 {
		msg += fmt.Sprintf("; %s apply after a restart", strings.Join(st.Restart, ", "))
	}
	m.notify(false, "%s", msg)
}

// reloadConfig is r in the host list.
func (m *Model) reloadConfig() {
	if m.svc.ConfigStatus().Path == "" {
		m.notify(false, "no config file")
		return
	}
	_ = m.svc.ReloadConfig()
	m.configReloaded()
}

// configError is the status bar mark of a config file that does not load.
func (m *Model) configError() string {
	if m.svc.ConfigStatus().Err == nil {
		return ""
	}
	return " " + m.ascii("⚠") + " config error (r: re-read) "
}
