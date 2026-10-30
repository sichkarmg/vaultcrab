// SPDX-License-Identifier: MIT

package layout

import (
	"reflect"
	"testing"
)

func TestSplitRectsAndClose(t *testing.T) {
	l, a := New()
	b := l.Split(a, Horizontal) // a | b
	c := l.Split(b, Vertical)   // a | (b / c)
	if got := l.Panes(); !reflect.DeepEqual(got, []PaneID{a, b, c}) {
		t.Fatalf("panes %v", got)
	}
	g := l.Rects(81, 25)
	want := map[PaneID]Rect{
		a: {0, 0, 40, 25},
		b: {41, 0, 40, 12},
		c: {41, 13, 40, 12},
	}
	if !reflect.DeepEqual(g.Panes, want) {
		t.Fatalf("rects %v", g.Panes)
	}
	if len(g.Borders) != 2 || g.Borders[0].Rect != (Rect{40, 0, 1, 25}) || g.Borders[1].Rect != (Rect{41, 12, 40, 1}) {
		t.Fatalf("borders %+v", g.Borders)
	}
	// Panes and borders tile the area exactly.
	cells := map[[2]int]int{}
	for _, r := range g.Panes {
		mark(cells, r)
	}
	for _, b := range g.Borders {
		mark(cells, b.Rect)
	}
	if len(cells) != 81*25 {
		t.Fatalf("covered %d cells", len(cells))
	}
	for k, n := range cells {
		if n != 1 {
			t.Fatalf("cell %v covered %d times", k, n)
		}
	}

	if !l.Close(b) {
		t.Fatal("close b")
	}
	g = l.Rects(81, 25)
	if !reflect.DeepEqual(g.Panes, map[PaneID]Rect{a: {0, 0, 40, 25}, c: {41, 0, 40, 25}}) {
		t.Fatalf("after close %v", g.Panes)
	}
	l.Close(c)
	if l.Close(a) {
		t.Fatal("last pane closed")
	}
	if g := l.Rects(10, 5); !reflect.DeepEqual(g.Panes, map[PaneID]Rect{a: {0, 0, 10, 5}}) {
		t.Fatalf("single %v", g.Panes)
	}
	if l.Split(99, Horizontal) != 0 || l.Close(99) {
		t.Fatal("unknown pane")
	}
}

func mark(m map[[2]int]int, r Rect) {
	for x := r.X; x < r.X+r.W; x++ {
		for y := r.Y; y < r.Y+r.H; y++ {
			m[[2]int{x, y}]++
		}
	}
}

func TestHitTest(t *testing.T) {
	l, a := New()
	b := l.Split(a, Horizontal)
	g := l.Rects(21, 10)
	tests := []struct {
		x, y   int
		pane   PaneID
		border bool
	}{
		{0, 0, a, false}, {9, 9, a, false}, {10, 3, 0, true}, {11, 3, b, false}, {20, 9, b, false}, {21, 0, 0, false},
	}
	for _, tt := range tests {
		h := g.HitTest(tt.x, tt.y)
		if h.Pane != tt.pane || (h.Border != nil) != tt.border {
			t.Errorf("(%d,%d): %+v", tt.x, tt.y, h)
		}
	}
	if h := g.HitTest(10, 3); h.Border != l.Root() {
		t.Error("border must point at the split node")
	}
}

func TestNeighbor(t *testing.T) {
	//  a | b
	//  --+--
	//  c | d     (built as (a|b) / (c|d))
	l, a := New()
	c := l.Split(a, Vertical)
	b := l.Split(a, Horizontal)
	d := l.Split(c, Horizontal)
	g := l.Rects(41, 21)
	tests := []struct {
		from PaneID
		side Side
		want PaneID
	}{
		{a, Right, b}, {a, Down, c}, {a, Left, 0}, {a, Up, 0},
		{b, Left, a}, {b, Down, d}, {d, Up, b}, {d, Left, c}, {c, Right, d}, {c, Up, a},
	}
	for _, tt := range tests {
		if got := g.Neighbor(tt.from, tt.side); got != tt.want {
			t.Errorf("neighbor(%d,%d) = %d, want %d", tt.from, tt.side, got, tt.want)
		}
	}
}

func TestNeighborPrefersOverlap(t *testing.T) {
	// a | (b / c): from a going right picks the pane overlapping a's... both
	// overlap equally per row; a tall pane on the right of two small ones
	l, a := New()
	b := l.Split(a, Horizontal)
	c := l.Split(b, Vertical)
	l.Root().B.Ratio = 0.8 // b is taller than c
	g := l.Rects(41, 21)
	if got := g.Neighbor(a, Right); got != b {
		t.Errorf("got %d, want %d", got, b)
	}
	if got := g.Neighbor(c, Left); got != a {
		t.Errorf("got %d", got)
	}
}

func TestDragAndResize(t *testing.T) {
	l, a := New()
	b := l.Split(a, Horizontal)
	g := l.Rects(101, 10)
	l.SetBorder(g, l.Root(), 30)
	g = l.Rects(101, 10)
	if g.Panes[a].W != 30 || g.Panes[b].X != 31 || g.Panes[b].W != 70 {
		t.Fatalf("after drag %v", g.Panes)
	}
	// Clamp to the minimum size.
	l.SetBorder(g, l.Root(), 0)
	g = l.Rects(101, 10)
	if g.Panes[a].W != MinSize {
		t.Fatalf("min %v", g.Panes)
	}
	l.SetBorder(g, l.Root(), 1000)
	g = l.Rects(101, 10)
	if g.Panes[b].W != MinSize {
		t.Fatalf("max %v", g.Panes)
	}
	l.SetBorder(g, l.Root(), 50)
	g = l.Rects(101, 10)
	if !l.Resize(g, a, Right, 5) {
		t.Fatal("resize a right")
	}
	g = l.Rects(101, 10)
	if g.Panes[a].W != 55 {
		t.Fatalf("a %v", g.Panes[a])
	}
	if !l.Resize(g, b, Right, 0) && l.Resize(g, b, Up, 3) {
		t.Fatal("no vertical split to resize")
	}
	l.Resize(g, b, Left, 10) // b grows left: border moves left
	g = l.Rects(101, 10)
	if g.Panes[a].W != 45 {
		t.Fatalf("a %v", g.Panes[a])
	}
}

func TestTinyArea(t *testing.T) {
	l, a := New()
	b := l.Split(a, Horizontal)
	l.Split(b, Vertical)
	for _, sz := range [][2]int{{1, 1}, {2, 2}, {3, 3}, {0, 0}} {
		g := l.Rects(sz[0], sz[1])
		for id, r := range g.Panes {
			if r.W < 0 || r.H < 0 {
				t.Errorf("%v: pane %d %v", sz, id, r)
			}
		}
	}
}
