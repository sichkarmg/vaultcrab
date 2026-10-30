// SPDX-License-Identifier: MIT

package hostlist

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/filter"
	"github.com/sichkarmg/vaultcrab/internal/model"
)

func testSnap() *model.Snapshot {
	s := &model.Snapshot{
		Folders: []model.Folder{
			{ID: "f1", Path: []string{"Lab", "Linux"}},
			{ID: "f2", Path: []string{"Lab", "Windows"}},
			{ID: "f3", Path: []string{"Prod"}},
		},
		Orgs:        []model.Org{{ID: "o1", Name: "Org One"}, {ID: "o2", Name: "Other"}},
		Collections: []model.Collection{{ID: "c1", OrgID: "o1", Path: []string{"ops"}}, {ID: "c2", OrgID: "o1", Path: []string{"dev", "be"}}, {ID: "c3", OrgID: "o2", Path: []string{"x"}}},
		Hosts: []model.Host{
			{ID: "a", Name: "alpha", Scheme: model.SSH, FolderID: "f1", Tags: []string{"linux"}},
			{ID: "b", Name: "bravo", Scheme: model.SSH, FolderID: "f2", Tags: []string{"windows"}},
			{ID: "c", Name: "charlie", Scheme: model.VNC, FolderID: "f3", OrgID: "o1", CollectionIDs: []string{"c1", "c2"}},
			{ID: "d", Name: "delta", Scheme: model.SSH, OrgID: "o1"},
			{ID: "e", Name: "echo", Scheme: model.SSH, OrgID: "o2", CollectionIDs: []string{"c3"}},
			{ID: "f", Name: "foxtrot", Scheme: model.SSH},
		},
	}
	return s
}

func render(rows []Row) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Repeat("  ", r.Depth))
		if r.Kind == GroupRow {
			mark := "-"
			if r.Collapsed {
				mark = "+"
			}
			fmt.Fprintf(&b, "%s%s(%d)", mark, r.Label, r.Count)
		} else {
			b.WriteString(r.Label)
			for _, f := range r.Badges {
				b.WriteString(" [" + f.Name + "]")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestByFolder(t *testing.T) {
	rows := Build(testSnap(), Options{Badges: []filter.Filter{{Name: "L", Rule: filter.MustParse("tag:linux"), Badge: true}}})
	want := `-Lab(2)
  -Linux(1)
    alpha [L]
  -Windows(1)
    bravo
-Prod(1)
  charlie
-(no folder)(3)
  delta
  echo
  foxtrot
`
	if got := render(rows); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestCollapsed(t *testing.T) {
	rows := Build(testSnap(), Options{Collapsed: map[string]bool{"g:folders:Lab": true}})
	if got := render(rows); !strings.HasPrefix(got, "+Lab(2)\n-Prod(1)\n") {
		t.Errorf("got\n%s", got)
	}
}

func TestByCollection(t *testing.T) {
	rows := Build(testSnap(), Options{Grouping: ByCollection})
	want := `-Org One(2)
  -dev(1)
    -be(1)
      charlie
  -ops(1)
    charlie
  delta
-Other(1)
  -x(1)
    echo
-(personal)(3)
  alpha
  bravo
  foxtrot
`
	if got := render(rows); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func names(hs []*model.Host) string {
	var out []string
	for _, h := range hs {
		out = append(out, h.ID)
	}
	return strings.Join(out, ",")
}

func TestSelectionAndVisibility(t *testing.T) {
	s := testSnap()
	cases := []struct {
		o    Options
		want string
	}{
		{Options{}, "a,b,c,d,e,f"},
		{Options{Org: OrgPersonal}, "a,b,f"},
		{Options{Org: "o1"}, "c,d"},
		{Options{Org: "o1", Collection: "c1"}, "c"},
		{Options{Filter: filter.MustParse("scheme:ssh")}, "a,b,d,e,f"},
		{Options{Filter: filter.MustParse("scheme:ssh"), Query: filter.MustParse("o")}, "b,e,f"}, // bravo, echo, foxtrot
		{Options{Visibility: Visibility{Orgs: []string{"org one"}}}, "a,b,c,d,f"},
		{Options{Visibility: Visibility{Collections: []string{"dev/be"}}}, "a,b,c,f"},
		{Options{Visibility: Visibility{HidePersonal: true}}, "c,d,e"},
	}
	for i, c := range cases {
		if got := names(Hosts(s, c.o)); got != c.want {
			t.Errorf("case %d: got %s want %s", i, got, c.want)
		}
	}
	if rows := Build(s, Options{Grouping: Flat}); len(rows) != 6 || rows[0].Depth != 0 {
		t.Error("flat")
	}
}
