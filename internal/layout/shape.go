// SPDX-License-Identifier: MIT

package layout

// Shape is the serializable form of a layout tree: a pane (A == nil) or a
// split. Panes are identified by their position in tree order.
type Shape struct {
	Dir   Dir     `json:"dir,omitempty"`
	Ratio float64 `json:"ratio,omitempty"`
	A     *Shape  `json:"a,omitempty"`
	B     *Shape  `json:"b,omitempty"`
}

// IsLeaf reports whether s is a pane.
func (s *Shape) IsLeaf() bool { return s.A == nil }

// Shape returns the tree; its leaves are in the order of Panes.
func (l *Layout) Shape() *Shape {
	var conv func(n *Node) *Shape
	conv = func(n *Node) *Shape {
		if n.IsLeaf() {
			return &Shape{}
		}
		return &Shape{Dir: n.Dir, Ratio: n.Ratio, A: conv(n.A), B: conv(n.B)}
	}
	return conv(l.root)
}

// Leaves returns the number of panes of s.
func (s *Shape) Leaves() int {
	if s.IsLeaf() {
		return 1
	}
	return s.A.Leaves() + s.B.Leaves()
}

// Prune drops the panes whose keep flag (in tree order) is false; a split
// with one pane left is replaced by it. It returns nil if no pane is kept.
func (s *Shape) Prune(keep []bool) *Shape {
	i := 0
	var prune func(s *Shape) *Shape
	prune = func(s *Shape) *Shape {
		if s.IsLeaf() {
			k := i < len(keep) && keep[i]
			i++
			if !k {
				return nil
			}
			return &Shape{}
		}
		a, b := prune(s.A), prune(s.B)
		switch {
		case a == nil:
			return b
		case b == nil:
			return a
		}
		return &Shape{Dir: s.Dir, Ratio: s.Ratio, A: a, B: b}
	}
	return prune(s)
}

// FromShape builds a layout; panes are returned in tree order.
func FromShape(s *Shape) (*Layout, []PaneID) {
	l := &Layout{next: 1}
	var ids []PaneID
	var build func(s *Shape, parent *Node) *Node
	build = func(s *Shape, parent *Node) *Node {
		n := &Node{parent: parent}
		if s == nil || s.IsLeaf() || s.B == nil {
			n.Pane = l.alloc()
			ids = append(ids, n.Pane)
			return n
		}
		n.Dir, n.Ratio = s.Dir, s.Ratio
		if n.Ratio <= 0 || n.Ratio >= 1 {
			n.Ratio = 0.5
		}
		n.A, n.B = build(s.A, n), build(s.B, n)
		return n
	}
	l.root = build(s, nil)
	return l, ids
}
