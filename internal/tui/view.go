// SPDX-License-Identifier: MIT

package tui

import (
	"slices"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// loadView restores the host list filters of the last run. The type
// filter, grouping, search and folded groups apply at once; organization
// and collection when the vault (or the cache) shows they still exist.
func (m *Model) loadView() {
	if !m.opt.RestoreView || m.opt.ViewPath == "" {
		return
	}
	v, err := core.LoadView(m.opt.ViewPath)
	if err != nil {
		return
	}
	l := &m.list
	for i, f := range m.svc.Filters() {
		if f.Name == v.Filter {
			l.filterIdx = i
		}
	}
	for _, g := range []core.Grouping{core.ByFolder, core.ByCollection, core.Flat} {
		if g.String() == v.Grouping {
			l.grouping = g
		}
	}
	for _, k := range v.Collapsed {
		l.collapsed[k] = true
	}
	if v.Query != "" {
		l.search.SetValue(v.Query)
	}
	l.selected = v.Selected
	if v.Org != "" && v.Org != core.OrgAll || v.Collection != "" {
		m.pendingView = &v
	}
	l.applyQuery() // rebuilds
	m.restoreView()
}

// restoreView applies the saved organization and collection once they are
// known; ones that no longer exist are dropped.
func (m *Model) restoreView() {
	v := m.pendingView
	if v == nil || len(m.svc.Snapshot().Hosts) == 0 {
		return
	}
	m.pendingView = nil
	l := &m.list
	if slices.Contains(l.orgChoices(), v.Org) {
		l.org = v.Org
	}
	if v.Collection != "" && slices.Contains(l.collectionChoices(), v.Collection) {
		l.collection = v.Collection
	}
	l.selected = v.Selected
	l.rebuild()
}

// SaveView stores the host list filters; called on exit.
func (m *Model) SaveView() error {
	if !m.opt.RestoreView || m.opt.ViewPath == "" {
		return nil
	}
	l := &m.list
	v := core.ViewState{
		Org:        l.org,
		Collection: l.collection,
		Grouping:   l.grouping.String(),
		Query:      l.search.Value(),
		Selected:   l.selected,
	}
	if p := m.pendingView; p != nil { // never applied: keep the saved ones
		v.Org, v.Collection = p.Org, p.Collection
	}
	if fs := m.svc.Filters(); l.filterIdx < len(fs) {
		v.Filter = fs[l.filterIdx].Name
	}
	for k := range l.collapsed {
		v.Collapsed = append(v.Collapsed, k)
	}
	slices.Sort(v.Collapsed)
	return core.SaveView(m.opt.ViewPath, v)
}
