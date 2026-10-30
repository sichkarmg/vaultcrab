// SPDX-License-Identifier: MIT

// Package filter implements host filter rules used for filter tabs, icons
// and the search box.
//
// Grammar (case-insensitive):
//
//	rule   = group { "|" group }        OR of groups
//	group  = term { term }              AND of terms (space separated)
//	term   = [ "!" ] atom               negation
//	atom   = key ":" value | key | word
//
// Keys:
//
//	field:NAME           custom field NAME is set (booleans must be true)
//	field:NAME=GLOB      custom field NAME matches GLOB
//	scheme:ssh|vnc|smb|ftp|sftp|rdp   connection type
//	tag:GLOB             host has a matching tag
//	folder:PATH          host is in exactly this folder ("A/B")
//	folder:PATH/**       host is in this folder or below
//	folder:none          host has no folder
//	org:NAME|ID          host belongs to the organization ("org:none" = personal)
//	collection:PATH[/**] host is in the collection (path, name or ID)
//	name:GLOB host:GLOB user:GLOB   item name / address / login
//	fav                  favorite items
//	any other word       substring of name, user@host, folder or tags
//
// Values may be quoted with double quotes to include spaces.
package filter

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// Rule is a parsed filter rule. The zero Rule matches everything.
type Rule struct {
	src    string
	groups [][]term
}

type term struct {
	neg   bool
	key   string
	name  string // for field:
	value string
	hasEq bool
}

// String returns the rule source.
func (r Rule) String() string { return r.src }

// IsEmpty reports whether the rule matches everything.
func (r Rule) IsEmpty() bool { return len(r.groups) == 0 }

var keys = map[string]bool{
	"field": true, "scheme": true, "tag": true, "folder": true, "org": true,
	"collection": true, "name": true, "host": true, "user": true,
}

// Parse parses a rule.
func Parse(src string) (Rule, error) {
	toks, err := tokenize(src)
	if err != nil {
		return Rule{}, err
	}
	r := Rule{src: src}
	var cur []term
	for _, tok := range toks {
		if tok == "|" {
			if len(cur) > 0 {
				r.groups = append(r.groups, cur)
			}
			cur = nil
			continue
		}
		t, err := parseTerm(tok)
		if err != nil {
			return Rule{}, err
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		r.groups = append(r.groups, cur)
	}
	return r, nil
}

// MustParse is Parse that panics; for tests and built-in defaults.
func MustParse(src string) Rule {
	r, err := Parse(src)
	if err != nil {
		panic(err)
	}
	return r
}

func tokenize(s string) ([]string, error) {
	var toks []string
	var b strings.Builder
	inQ := false
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
		case inQ:
			b.WriteRune(r)
		case r == '|':
			flush()
			toks = append(toks, "|")
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if inQ {
		return nil, errors.New("filter: unterminated quote")
	}
	flush()
	return toks, nil
}

func parseTerm(tok string) (term, error) {
	var t term
	if strings.HasPrefix(tok, "!") && len(tok) > 1 {
		t.neg = true
		tok = tok[1:]
	}
	low := strings.ToLower(tok)
	if low == "fav" || low == "favorite" {
		t.key = "fav"
		return t, nil
	}
	k, v, ok := strings.Cut(tok, ":")
	if !ok || !keys[strings.ToLower(k)] {
		t.key = "word"
		t.value = strings.ToLower(tok)
		return t, nil
	}
	t.key = strings.ToLower(k)
	if v == "" && t.key != "field" {
		return t, fmt.Errorf("filter: empty value in %q", tok)
	}
	if t.key == "field" {
		t.name, t.value, t.hasEq = strings.Cut(v, "=")
		t.name = strings.ToLower(strings.TrimSpace(t.name))
		if t.name == "" {
			return t, fmt.Errorf("filter: field name missing in %q", tok)
		}
	} else {
		t.value = v
	}
	if t.key == "scheme" {
		if _, ok := model.ParseScheme(v); !ok {
			return t, fmt.Errorf("filter: unknown scheme %q", v)
		}
	}
	return t, nil
}

// Match reports whether h matches the rule. snap resolves folder,
// organization and collection names; it may be nil.
func (r Rule) Match(h *model.Host, snap *model.Snapshot) bool {
	if len(r.groups) == 0 {
		return true
	}
	for _, g := range r.groups {
		ok := true
		for _, t := range g {
			if t.match(h, snap) == t.neg {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func glob(pattern, s string) bool {
	pattern, s = strings.ToLower(pattern), strings.ToLower(s)
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == s
	}
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "false", "0", "no", "off", "нет":
		return false
	}
	return true
}

func (t term) match(h *model.Host, snap *model.Snapshot) bool {
	switch t.key {
	case "fav":
		return h.Favorite
	case "field":
		v, ok := h.Fields[t.name]
		if !ok {
			return false
		}
		if !t.hasEq {
			return truthy(v)
		}
		return glob(t.value, v)
	case "scheme":
		return strings.EqualFold(string(h.Scheme), t.value)
	case "tag":
		for _, tag := range h.Tags {
			if glob(t.value, tag) {
				return true
			}
		}
		return false
	case "name":
		return glob(t.value, h.Name)
	case "host":
		return glob(t.value, h.Addr)
	case "user":
		return glob(t.value, h.User)
	case "folder":
		if strings.EqualFold(t.value, "none") {
			return h.FolderID == ""
		}
		var fp []string
		if snap != nil {
			fp = snap.FolderPath(h)
		}
		return pathMatch(t.value, fp)
	case "org":
		if strings.EqualFold(t.value, "none") {
			return h.OrgID == ""
		}
		if h.OrgID == "" {
			return false
		}
		if h.OrgID == t.value {
			return true
		}
		if snap != nil {
			if o := snap.Org(h.OrgID); o != nil {
				return glob(t.value, o.Name)
			}
		}
		return false
	case "collection":
		for _, id := range h.CollectionIDs {
			if id == t.value {
				return true
			}
			if snap != nil {
				if c := snap.Collection(id); c != nil && pathMatch(t.value, c.Path) {
					return true
				}
			}
		}
		return false
	case "word":
		if strings.Contains(strings.ToLower(h.Name), t.value) ||
			strings.Contains(strings.ToLower(h.Target()), t.value) {
			return true
		}
		for _, tag := range h.Tags {
			if strings.Contains(strings.ToLower(tag), t.value) {
				return true
			}
		}
		if snap != nil {
			if strings.Contains(strings.ToLower(strings.Join(snap.FolderPath(h), "/")), t.value) {
				return true
			}
		}
		return false
	}
	return false
}

// pathMatch matches "A/B" (exact) or "A/B/**" (subtree) against a path,
// segment by segment with globs.
func pathMatch(pattern string, p []string) bool {
	pat := model.SplitPath(pattern)
	sub := len(pat) > 0 && pat[len(pat)-1] == "**"
	if sub {
		pat = pat[:len(pat)-1]
		if len(p) < len(pat) {
			return false
		}
	} else if len(p) != len(pat) {
		return false
	}
	for i, seg := range pat {
		if !glob(seg, p[i]) {
			return false
		}
	}
	return true
}

// Filter is a named rule with an icon, shown as a filter tab and as a badge
// next to matching hosts.
type Filter struct {
	Name      string
	Icon      string // unicode icon (may be empty)
	ASCIIIcon string // ASCII fallback
	IconName  string // if set, the icon of this name from the icon set is used instead
	Rule      Rule
	Badge     bool // show the icon next to matching hosts
}

// Hits returns the filters matching h, in order.
func Hits(filters []Filter, h *model.Host, snap *model.Snapshot) []*Filter {
	var out []*Filter
	for i := range filters {
		if filters[i].Rule.Match(h, snap) {
			out = append(out, &filters[i])
		}
	}
	return out
}
