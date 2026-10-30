// SPDX-License-Identifier: MIT

// Package layout is the pane layout of a tab: a binary tree of horizontal
// and vertical splits with panes in the leaves, as in tmux. It computes
// rectangles (with 1-cell separators), hit-tests mouse positions, moves focus
// by direction and resizes splits. No UI code.
package layout

import "math"

// PaneID identifies a pane within a layout.
type PaneID int

// Dir is the split direction.
type Dir int

const (
	// Horizontal places children side by side (left | right).
	Horizontal Dir = iota
	// Vertical stacks children (top / bottom).
	Vertical
)

// Side is a direction for focus moves and resizing.
type Side int

const (
	Left Side = iota
	Right
	Up
	Down
)

// Rect is a cell rectangle.
type Rect struct{ X, Y, W, H int }

// Contains reports whether (x, y) is inside r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Node is a layout tree node: a leaf (pane) or a split.
type Node struct {
	Pane   PaneID
	Dir    Dir
	Ratio  float64 // share of the first child, 0..1
	A, B   *Node
	parent *Node
}

// IsLeaf reports whether n is a pane.
func (n *Node) IsLeaf() bool { return n.A == nil }

// Layout is a pane tree.
type Layout struct {
	root *Node
	next PaneID
}

// MinSize is the minimum width/height of a pane in cells.
const MinSize = 2

// New creates a layout with a single pane.
func New() (*Layout, PaneID) {
	l := &Layout{next: 1}
	id := l.alloc()
	l.root = &Node{Pane: id}
	return l, id
}

func (l *Layout) alloc() PaneID {
	id := l.next
	l.next++
	return id
}

// Root returns the root node (for rendering).
func (l *Layout) Root() *Node { return l.root }

func (l *Layout) find(n *Node, id PaneID) *Node {
	if n == nil {
		return nil
	}
	if n.IsLeaf() {
		if n.Pane == id {
			return n
		}
		return nil
	}
	if f := l.find(n.A, id); f != nil {
		return f
	}
	return l.find(n.B, id)
}

// Has reports whether the pane exists.
func (l *Layout) Has(id PaneID) bool { return l.find(l.root, id) != nil }

// Panes returns pane IDs in tree order (left-to-right, top-to-bottom).
func (l *Layout) Panes() []PaneID {
	var out []PaneID
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.IsLeaf() {
			out = append(out, n.Pane)
			return
		}
		walk(n.A)
		walk(n.B)
	}
	walk(l.root)
	return out
}

// Len returns the number of panes.
func (l *Layout) Len() int { return len(l.Panes()) }

// Split splits pane id; the new pane goes right (Horizontal) or below
// (Vertical) and gets half of the space. It returns the new pane, or 0 if id
// does not exist.
func (l *Layout) Split(id PaneID, dir Dir) PaneID {
	leaf := l.find(l.root, id)
	if leaf == nil {
		return 0
	}
	nid := l.alloc()
	a := &Node{Pane: id}
	b := &Node{Pane: nid}
	leaf.Pane = 0
	leaf.Dir = dir
	leaf.Ratio = 0.5
	leaf.A, leaf.B = a, b
	a.parent, b.parent = leaf, leaf
	return nid
}

// Close removes pane id; its sibling takes the parent's place. It reports
// whether the pane existed. The last pane cannot be closed (returns false).
func (l *Layout) Close(id PaneID) bool {
	leaf := l.find(l.root, id)
	if leaf == nil || leaf == l.root {
		return false
	}
	p := leaf.parent
	sib := p.A
	if sib == leaf {
		sib = p.B
	}
	// Replace p by sib in place (keeps p's parent links valid).
	p.Pane, p.Dir, p.Ratio, p.A, p.B = sib.Pane, sib.Dir, sib.Ratio, sib.A, sib.B
	if p.A != nil {
		p.A.parent, p.B.parent = p, p
	}
	return true
}

// Border is a separator between the two children of a split.
type Border struct {
	Rect  Rect
	Split *Node
}

// Geometry is the result of laying out a tree in a rectangle.
type Geometry struct {
	Panes   map[PaneID]Rect
	Borders []Border
	area    map[*Node]Rect
}

// Rects lays the tree out in a w×h area.
func (l *Layout) Rects(w, h int) Geometry {
	g := Geometry{Panes: map[PaneID]Rect{}, area: map[*Node]Rect{}}
	l.layout(&g, l.root, Rect{0, 0, w, h})
	return g
}

// splitSizes returns the sizes of the first and second child for a total
// of n cells, leaving 1 cell for the separator.
func splitSizes(n int, ratio float64) (int, int) {
	avail := n - 1
	if avail < 2 {
		return max(avail, 0), 0
	}
	a := int(math.Round(float64(avail) * ratio))
	lo, hi := min(MinSize, avail/2), avail-min(MinSize, avail/2)
	a = max(lo, min(a, hi))
	return a, avail - a
}

func (l *Layout) layout(g *Geometry, n *Node, r Rect) {
	g.area[n] = r
	if n.IsLeaf() {
		g.Panes[n.Pane] = r
		return
	}
	if n.Dir == Horizontal {
		a, b := splitSizes(r.W, n.Ratio)
		l.layout(g, n.A, Rect{r.X, r.Y, a, r.H})
		g.Borders = append(g.Borders, Border{Rect: Rect{r.X + a, r.Y, 1, r.H}, Split: n})
		l.layout(g, n.B, Rect{r.X + a + 1, r.Y, b, r.H})
		return
	}
	a, b := splitSizes(r.H, n.Ratio)
	l.layout(g, n.A, Rect{r.X, r.Y, r.W, a})
	g.Borders = append(g.Borders, Border{Rect: Rect{r.X, r.Y + a, r.W, 1}, Split: n})
	l.layout(g, n.B, Rect{r.X, r.Y + a + 1, r.W, b})
}

// Area returns the rectangle a node occupies.
func (g Geometry) Area(n *Node) Rect { return g.area[n] }

// Hit is the result of a hit test.
type Hit struct {
	Pane   PaneID // non-zero if a pane was hit
	Border *Node  // non-nil if a separator was hit
}

// HitTest finds what is at (x, y).
func (g Geometry) HitTest(x, y int) Hit {
	for id, r := range g.Panes {
		if r.Contains(x, y) {
			return Hit{Pane: id}
		}
	}
	for _, b := range g.Borders {
		if b.Rect.Contains(x, y) {
			return Hit{Border: b.Split}
		}
	}
	return Hit{}
}

// Neighbor returns the pane adjacent to id in direction side, preferring the
// one overlapping most with id along the perpendicular axis; 0 if none.
func (g Geometry) Neighbor(id PaneID, side Side) PaneID {
	r, ok := g.Panes[id]
	if !ok {
		return 0
	}
	best, bestOverlap, bestDist := PaneID(0), -1, math.MaxInt
	for oid, o := range g.Panes {
		if oid == id {
			continue
		}
		var dist, overlap int
		switch side {
		case Left:
			dist = r.X - (o.X + o.W)
			overlap = span(r.Y, r.H, o.Y, o.H)
		case Right:
			dist = o.X - (r.X + r.W)
			overlap = span(r.Y, r.H, o.Y, o.H)
		case Up:
			dist = r.Y - (o.Y + o.H)
			overlap = span(r.X, r.W, o.X, o.W)
		case Down:
			dist = o.Y - (r.Y + r.H)
			overlap = span(r.X, r.W, o.X, o.W)
		}
		if dist < 0 || overlap <= 0 {
			continue
		}
		if dist < bestDist || dist == bestDist && overlap > bestOverlap || dist == bestDist && overlap == bestOverlap && oid < best {
			best, bestOverlap, bestDist = oid, overlap, dist
		}
	}
	return best
}

func span(a, al, b, bl int) int {
	return min(a+al, b+bl) - max(a, b)
}

// SetBorder moves the separator of split so it sits at absolute position pos
// (x for horizontal splits, y for vertical), within geometry g.
func (l *Layout) SetBorder(g Geometry, split *Node, pos int) {
	r, ok := g.area[split]
	if !ok || split.IsLeaf() {
		return
	}
	start, total := r.X, r.W
	if split.Dir == Vertical {
		start, total = r.Y, r.H
	}
	avail := total - 1
	if avail < 2 {
		return
	}
	a := max(MinSize, min(pos-start, avail-MinSize))
	split.Ratio = float64(a) / float64(avail)
}

// Resize grows pane id towards side by delta cells by moving the nearest
// enclosing separator on that side. It reports whether something changed.
func (l *Layout) Resize(g Geometry, id PaneID, side Side, delta int) bool {
	n := l.find(l.root, id)
	if n == nil {
		return false
	}
	want := Horizontal
	if side == Up || side == Down {
		want = Vertical
	}
	for c := n; c.parent != nil; c = c.parent {
		p := c.parent
		if p.Dir != want {
			continue
		}
		inFirst := p.A == c
		// The separator lies after A: growing towards Right/Down from A, or
		// towards Left/Up from B moves it.
		if inFirst && (side == Right || side == Down) || !inFirst && (side == Left || side == Up) {
			b := g.borderOf(p)
			pos := b.X
			if want == Vertical {
				pos = b.Y
			}
			if side == Right || side == Down {
				pos += delta
			} else {
				pos -= delta
			}
			old := p.Ratio
			l.SetBorder(g, p, pos)
			return p.Ratio != old
		}
	}
	return false
}

func (g Geometry) borderOf(split *Node) Rect {
	for _, b := range g.Borders {
		if b.Split == split {
			return b.Rect
		}
	}
	return Rect{}
}
