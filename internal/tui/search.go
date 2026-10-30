// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sichkarmg/vaultcrab/internal/core"
)

// The second header row of the host list: the organization, collection and
// grouping selectors, the button that resets the filters (when something is
// filtered), then the search with its button. Each part is clicked on its own.
//
// The search: / edits it; Esc leaves the field and keeps the search (the
// list keys e, s, … work again), a second Esc clears it; / goes back into
// the field with the text kept.

// parts of the second header row (hostList.selX)
const (
	selOrg = iota
	selColl
	selGroup
	selSearch    // the search text (or "/ search")
	selSearchBtn // [Esc to exit] / [Esc again to clear]
	selReset     // [Reset filters]
	selCount
)

const (
	searchExitLabel  = "[Esc to exit]"
	searchClearLabel = "[Esc again to clear]"
	resetLabel       = "[Reset filters]"
)

func (l *hostList) startSearch() tea.Cmd {
	l.searching = true
	l.search.Focus()
	l.search.CursorEnd()
	return textinput.Blink
}

func (l *hostList) clearSearch() {
	l.searching = false
	l.search.Blur()
	if l.search.Value() != "" {
		l.search.SetValue("")
		l.applyQuery()
	}
}

// filtered reports whether anything narrows the list: a type filter other
// than the first, an organization, a collection or a search.
func (l *hostList) filtered() bool {
	return l.filterIdx != 0 || l.org != core.OrgAll || l.collection != "" || l.search.Value() != ""
}

// resetFilters shows all items again: the first type filter, all
// organizations and collections, no search (the grouping stays).
func (l *hostList) resetFilters() {
	if !l.filtered() {
		return
	}
	l.filterIdx, l.org, l.collection = 0, core.OrgAll, ""
	l.searching = false
	l.search.Blur()
	l.search.SetValue("")
	l.applyQuery()
	l.m.notify(false, "filters reset: all items")
}

func (l *hostList) selectorRow() string {
	m := l.m
	ic := m.opt.Icons
	var b strings.Builder
	x := 0
	part := func(i int, s string) {
		w := ansi.StringWidth(s)
		l.selX[i] = [2]int{x, x + w}
		b.WriteString(s)
		x += w
	}
	gap := func(s string) {
		b.WriteString(s)
		x += ansi.StringWidth(s)
	}
	sep := func() { gap(" " + stBorder.Render(m.g.v) + " ") }
	for i := range l.selX {
		l.selX[i] = [2]int{-1, -1}
	}
	part(selOrg, stDim.Render(fmt.Sprintf("%s org: %s", ic.Get(core.IconOrg), trunc(l.orgName(), 16))))
	sep()
	part(selColl, stDim.Render(fmt.Sprintf("%s coll: %s", ic.Get(core.IconCollection), trunc(l.collectionName(), 20))))
	sep()
	part(selGroup, stDim.Render(fmt.Sprintf("group: %s", l.grouping)))
	if l.filtered() {
		sep()
		part(selReset, stKey.Render(resetLabel))
	}
	sep()
	switch {
	case l.searching:
		part(selSearch, l.search.View())
		gap(" ")
		part(selSearchBtn, stKey.Render(searchExitLabel))
	case l.search.Value() != "":
		part(selSearch, stBold.Render("/"+l.search.Value()))
		gap(" ")
		part(selSearchBtn, stKey.Render(searchClearLabel))
	default:
		part(selSearch, stDim.Render("/ search"))
	}
	if l.queryErr != "" && l.search.Value() != "" {
		gap(" " + stErr.Render(l.queryErr))
	}
	return b.String()
}

// selectorClick handles a click at column x of the second header row.
func (l *hostList) selectorClick(x int) tea.Cmd {
	at := -1
	for i, r := range l.selX {
		if x >= r[0] && x < r[1] {
			at = i
		}
	}
	switch at {
	case selOrg:
		l.cycleOrg()
	case selColl:
		l.cycleCollection()
	case selGroup:
		l.grouping = l.grouping.Next()
		l.rebuild()
	case selSearch:
		if !l.searching {
			return l.startSearch()
		}
	case selSearchBtn:
		if l.searching {
			l.searching = false // like Esc: the search stays
			l.search.Blur()
		} else {
			l.clearSearch()
		}
	case selReset:
		l.resetFilters()
	}
	return nil
}
