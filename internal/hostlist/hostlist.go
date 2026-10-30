// SPDX-License-Identifier: MIT

// Package hostlist builds the rows of the host list: filtering (filter tab,
// search query, organization/collection selection, configured visibility)
// and grouping into a collapsible tree of folders or collections.
package hostlist

import (
	"sort"
	"strings"

	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/model"
)

// Grouping selects how hosts are arranged.
type Grouping int

const (
	ByFolder Grouping = iota
	ByCollection
	Flat
)

func (g Grouping) String() string {
	return [...]string{"folders", "collections", "flat"}[g]
}

// Next cycles through groupings.
func (g Grouping) Next() Grouping { return (g + 1) % 3 }

// Special organization selectors.
const (
	OrgAll      = ""
	OrgPersonal = "personal"
)

// Visibility restricts which items are shown at all (from the config).
type Visibility struct {
	Orgs         []string // names or IDs; empty = all
	Collections  []string // paths/names or IDs; empty = all
	HidePersonal bool
}

// Options for Build.
type Options struct {
	Filter     filter.Rule // active filter tab
	Query      filter.Rule // search
	Org        string      // OrgAll, OrgPersonal or an org ID
	Collection string      // "" = all, or a collection ID
	Grouping   Grouping
	Collapsed  map[string]bool // group keys that are collapsed
	Visibility Visibility
	Badges     []filter.Filter // filters whose icons are shown next to hosts
}

// RowKind distinguishes group and host rows.
type RowKind int

const (
	GroupRow RowKind = iota
	HostRow
)

// Row is one line of the host list.
type Row struct {
	Kind      RowKind
	Key       string // stable key: "g:<grouping>:<path>" or "h:<id>"
	Depth     int
	Label     string
	Host      *model.Host
	Count     int  // hosts below a group (after filtering)
	Collapsed bool // group is collapsed
	IsOrg     bool // group row is an organization (collections view)
	Badges    []*filter.Filter
}

// Visible reports whether h passes the configured visibility.
func (v Visibility) Visible(h *model.Host, snap *model.Snapshot) bool {
	if h.OrgID == "" {
		return !v.HidePersonal
	}
	if len(v.Orgs) > 0 {
		o := snap.Org(h.OrgID)
		ok := false
		for _, want := range v.Orgs {
			if want == h.OrgID || o != nil && strings.EqualFold(want, o.Name) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(v.Collections) > 0 {
		for _, id := range h.CollectionIDs {
			c := snap.Collection(id)
			for _, want := range v.Collections {
				if want == id || c != nil && (strings.EqualFold(want, c.Name) || strings.EqualFold(want, strings.Join(c.Path, "/"))) {
					return true
				}
			}
		}
		return false
	}
	return true
}

// Hosts returns the hosts passing all filters, in snapshot order.
func Hosts(snap *model.Snapshot, o Options) []*model.Host {
	var out []*model.Host
	for i := range snap.Hosts {
		h := &snap.Hosts[i]
		if !o.Visibility.Visible(h, snap) {
			continue
		}
		switch o.Org {
		case OrgAll:
		case OrgPersonal:
			if h.OrgID != "" {
				continue
			}
		default:
			if h.OrgID != o.Org {
				continue
			}
		}
		if o.Collection != "" && !contains(h.CollectionIDs, o.Collection) {
			continue
		}
		if !o.Filter.Match(h, snap) || !o.Query.Match(h, snap) {
			continue
		}
		out = append(out, h)
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// node is a tree node during building.
type node struct {
	name     string
	key      string
	isOrg    bool
	children map[string]*node
	order    []string
	hosts    []*model.Host
	count    int
}

func newNode(name, key string) *node {
	return &node{name: name, key: key, children: map[string]*node{}}
}

func (n *node) child(name, key string) *node {
	c, ok := n.children[name]
	if !ok {
		c = newNode(name, key)
		n.children[name] = c
		n.order = append(n.order, name)
	}
	return c
}

func (n *node) add(path []string, prefix string, h *model.Host) *node {
	cur := n
	for i, seg := range path {
		cur = cur.child(seg, prefix+strings.Join(path[:i+1], "/"))
	}
	cur.hosts = append(cur.hosts, h)
	return cur
}

// countHosts sets count to the number of distinct hosts in the subtree (a
// host may appear in several collections) and returns their IDs.
func (n *node) countHosts() map[string]bool {
	ids := map[string]bool{}
	for _, h := range n.hosts {
		ids[h.ID] = true
	}
	for _, c := range n.children {
		for id := range c.countHosts() {
			ids[id] = true
		}
	}
	n.count = len(ids)
	return ids
}

// Build returns the rows to display.
func Build(snap *model.Snapshot, o Options) []Row {
	hosts := Hosts(snap, o)
	var rows []Row
	badges := func(h *model.Host) []*filter.Filter {
		var out []*filter.Filter
		for _, f := range filter.Hits(o.Badges, h, snap) {
			if f.Badge {
				out = append(out, f)
			}
		}
		return out
	}
	hostRow := func(h *model.Host, depth int) Row {
		return Row{Kind: HostRow, Key: "h:" + h.ID, Depth: depth, Label: h.Name, Host: h, Badges: badges(h)}
	}
	if o.Grouping == Flat {
		for _, h := range hosts {
			rows = append(rows, hostRow(h, 0))
		}
		return rows
	}

	root := newNode("", "")
	const noGroup = "\x00"
	switch o.Grouping {
	case ByFolder:
		for _, h := range hosts {
			p := snap.FolderPath(h)
			if len(p) == 0 {
				ng := root.child(noGroup, "g:folders:"+noGroup)
				ng.hosts = append(ng.hosts, h)
				continue
			}
			root.add(p, "g:folders:", h)
		}
	case ByCollection:
		for _, h := range hosts {
			if h.OrgID == "" {
				ng := root.child(noGroup, "g:collections:"+noGroup)
				ng.hosts = append(ng.hosts, h)
				continue
			}
			orgName := h.OrgID
			if org := snap.Org(h.OrgID); org != nil {
				orgName = org.Name
			}
			on := root.child("org:"+h.OrgID, "g:collections:"+h.OrgID)
			on.name = orgName
			on.isOrg = true
			placed := false
			for _, cid := range h.CollectionIDs {
				c := snap.Collection(cid)
				if c == nil {
					continue
				}
				on.add(c.Path, "g:collections:"+h.OrgID+"/", h)
				placed = true
			}
			if !placed {
				on.hosts = append(on.hosts, h)
			}
		}
	}
	root.countHosts()

	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		names := append([]string(nil), n.order...)
		sort.SliceStable(names, func(i, j int) bool {
			a, b := names[i], names[j]
			if (a == noGroup) != (b == noGroup) {
				return b == noGroup // "no folder" last
			}
			return strings.ToLower(n.children[a].name) < strings.ToLower(n.children[b].name)
		})
		for _, name := range names {
			c := n.children[name]
			label := c.name
			if name == noGroup {
				if o.Grouping == ByFolder {
					label = "(no folder)"
				} else {
					label = "(personal)"
				}
			}
			collapsed := o.Collapsed[c.key]
			rows = append(rows, Row{Kind: GroupRow, Key: c.key, Depth: depth, Label: label, Count: c.count, Collapsed: collapsed, IsOrg: c.isOrg})
			if !collapsed {
				walk(c, depth+1)
			}
		}
		for _, h := range n.hosts {
			rows = append(rows, hostRow(h, depth))
		}
	}
	walk(root, 0)
	return rows
}
