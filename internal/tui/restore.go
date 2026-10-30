// SPDX-License-Identifier: MIT

package tui

import (
	"github.com/sichkarmg/vaultcrab/internal/core"
)

// loadWorkspace reads the tabs of the last run; they are opened (without
// connecting) as soon as the host list is known.
func (m *Model) loadWorkspace() {
	if !m.opt.RestoreTabs || m.opt.WorkspacePath == "" {
		return
	}
	st, err := core.LoadWorkspace(m.opt.WorkspacePath)
	if err != nil || len(st.Tabs) == 0 {
		return
	}
	m.pendingRestore = &st
	m.restoreWorkspace()
}

// restoreWorkspace opens the saved tabs once the hosts they show are known
// (from the cache or the vault). Panes of deleted hosts are left out.
func (m *Model) restoreWorkspace() {
	st := m.pendingRestore
	if st == nil {
		return
	}
	needHosts := false
	for _, ts := range st.Tabs {
		for _, ps := range ts.Panes {
			needHosts = needHosts || ps.HostID != localPaneID
		}
	}
	if needHosts && len(m.svc.Snapshot().Hosts) == 0 {
		return // not yet: no cache, vault still locked or loading
	}
	if m.w == 0 {
		return // not yet: the terminals are created with the window size
	}
	m.pendingRestore = nil
	r := m.bodyRect()
	open := func(ps core.PaneState) *core.Session {
		h := m.svc.LocalHost()
		if ps.HostID != localPaneID {
			var ok bool
			if h, ok = m.svc.Host(ps.HostID); !ok {
				return nil
			}
		}
		s, err := m.svc.OpenIdle(h, r.w, r.h)
		if err != nil {
			return nil
		}
		return s
	}
	active := 0
	for i, ts := range st.Tabs {
		ws := core.RestoreTab(ts, open)
		if ws == nil {
			continue
		}
		m.tabs = append(m.tabs, &tab{ws: ws, history: map[core.PaneID]bool{}, ksel: map[core.PaneID]*kbSel{}})
		if i+1 == st.Active {
			active = len(m.tabs)
		}
	}
	m.setActive(active)
}

// localPaneID is the saved host ID of the local shell.
const localPaneID = "local"

// SaveWorkspace stores the open tabs: for each pane only the host (or the
// local shell) and the layout; script panes are not kept. Called on exit;
// after "Quit without saving" it stores no tabs.
func (m *Model) SaveWorkspace() error {
	if !m.opt.RestoreTabs || m.opt.WorkspacePath == "" {
		return nil
	}
	if m.pendingRestore != nil && !m.discardTabs {
		return nil // never restored (e.g. the vault was not unlocked): keep the file
	}
	var st core.WorkspaceState
	if m.discardTabs {
		return core.SaveWorkspace(m.opt.WorkspacePath, st) // nothing to reopen
	}
	pane := func(s *core.Session) (core.PaneState, bool) {
		switch {
		case s.Host.Scheme == core.SchemeSSH, s.Host.Scheme == core.SchemeVNC:
			return core.PaneState{HostID: s.Host.ID}, true
		case s.Host.Scheme == core.SchemeLocal && s.Host.ID == localPaneID:
			return core.PaneState{HostID: localPaneID}, true
		}
		return core.PaneState{}, false // scripts
	}
	for i, t := range m.tabs {
		ts, ok := t.ws.Save(pane)
		if !ok {
			continue
		}
		st.Tabs = append(st.Tabs, ts)
		if i+1 == m.active {
			st.Active = len(st.Tabs)
		}
	}
	return core.SaveWorkspace(m.opt.WorkspacePath, st)
}
