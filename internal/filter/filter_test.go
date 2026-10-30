// SPDX-License-Identifier: MIT

package filter

import (
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

var snap = &model.Snapshot{
	Folders: []model.Folder{
		{ID: "f1", Path: []string{"Lab", "Linux", "Mint"}},
		{ID: "f2", Path: []string{"Prod", "DB"}},
	},
	Orgs:        []model.Org{{ID: "o1", Name: "organization"}},
	Collections: []model.Collection{{ID: "c1", OrgID: "o1", Path: []string{"dev", "backend"}}, {ID: "c2", OrgID: "o1", Path: []string{"ops"}}},
}

var mint = model.Host{ID: "1", Name: "Mint box", Scheme: model.SSH, User: "user", Addr: "192.0.2.133", Port: 22, FolderID: "f1",
	Tags: []string{"linux", "lab"}, Fields: map[string]string{"ssh": "true", "env": "lab"}}
var db = model.Host{ID: "2", Name: "pg-main", Scheme: model.SSH, User: "postgres", Addr: "db.prod", FolderID: "f2", OrgID: "o1",
	CollectionIDs: []string{"c1"}, Tags: []string{"db"}, Fields: map[string]string{"env": "prod", "ssh": "false"}, Favorite: true}
var vnc = model.Host{ID: "3", Name: "desk", Scheme: model.VNC, Addr: "192.0.2.6"}

func TestMatch(t *testing.T) {
	tests := []struct {
		rule string
		want [3]bool // mint, db, vnc
	}{
		{"", [3]bool{true, true, true}},
		{"scheme:ssh", [3]bool{true, true, false}},
		{"scheme:VNC", [3]bool{false, false, true}},
		{"field:ssh", [3]bool{true, false, false}}, // boolean false is not set
		{"field:env", [3]bool{true, true, false}},
		{"field:env=prod", [3]bool{false, true, false}},
		{"field:ENV=PR*", [3]bool{false, true, false}},
		{"tag:linux", [3]bool{true, false, false}},
		{"tag:l*", [3]bool{true, false, false}},
		{"!tag:linux", [3]bool{false, true, true}},
		{"tag:linux | tag:db", [3]bool{true, true, false}},
		{"scheme:ssh !fav", [3]bool{true, false, false}},
		{"fav", [3]bool{false, true, false}},
		{"folder:Lab/Linux/Mint", [3]bool{true, false, false}},
		{"folder:lab/linux", [3]bool{false, false, false}},
		{"folder:Lab/**", [3]bool{true, false, false}},
		{"folder:*/DB", [3]bool{false, true, false}},
		{"folder:none", [3]bool{false, false, true}},
		{"org:organization", [3]bool{false, true, false}},
		{"org:o1", [3]bool{false, true, false}},
		{"org:none", [3]bool{true, false, true}},
		{"collection:dev/backend", [3]bool{false, true, false}},
		{"collection:dev/**", [3]bool{false, true, false}},
		{"collection:ops", [3]bool{false, false, false}},
		{"name:pg-*", [3]bool{false, true, false}},
		{"host:192.0.2.*", [3]bool{true, false, true}},
		{"user:user", [3]bool{true, false, false}},
		{"mint", [3]bool{true, false, false}},            // substring of name
		{"postgres@", [3]bool{false, true, false}},       // substring of target
		{"prod", [3]bool{false, true, false}},            // folder path / address
		{`name:"Mint box"`, [3]bool{true, false, false}}, // quoted value
		{"unknownkey:x", [3]bool{false, false, false}},   // treated as a word
	}
	hosts := []model.Host{mint, db, vnc}
	for _, tt := range tests {
		r, err := Parse(tt.rule)
		if err != nil {
			t.Errorf("%q: %v", tt.rule, err)
			continue
		}
		for i := range hosts {
			if got := r.Match(&hosts[i], snap); got != tt.want[i] {
				t.Errorf("%q on %s: got %v", tt.rule, hosts[i].Name, got)
			}
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{`name:"open`, "scheme:gopher", "tag:", "field:=x"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("%q: expected error", s)
		}
	}
}

func TestHits(t *testing.T) {
	fs := []Filter{
		{Name: "Linux", Rule: MustParse("tag:linux")},
		{Name: "Prod", Rule: MustParse("field:env=prod")},
		{Name: "SSH", Rule: MustParse("scheme:ssh")},
	}
	h := Hits(fs, &db, snap)
	if len(h) != 2 || h[0].Name != "Prod" || h[1].Name != "SSH" {
		t.Errorf("hits %v", h)
	}
}
