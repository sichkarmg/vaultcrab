package vt

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestPackRoundTrip(t *testing.T) {
	red := uv.Style{Fg: ansi.Red, Attrs: uv.AttrBold}
	link := uv.Link{URL: "https://example.com"}
	line := uv.Line{
		{Content: "a", Width: 1},
		{Content: "ж", Width: 1, Style: red},
		{Content: "界", Width: 2, Style: red},
		{Content: "", Width: 0, Style: red},
		{Content: "b", Width: 1, Link: link},
		{Content: " ", Width: 1},
		uv.EmptyCell, uv.EmptyCell,
	}
	p := pack(line)
	got := p.unpack()
	if len(got) != 5 { // " " with no style equals EmptyCell and is trimmed too
		t.Fatalf("trailing blanks not trimmed: %d", len(got))
	}
	for i := range got {
		if !got[i].Equal(&line[i]) || got[i].Width != line[i].Width {
			t.Errorf("cell %d: got %+v want %+v", i, got[i], line[i])
		}
	}
	ascii := pack(uv.Line{{Content: "x", Width: 1}, {Content: "y", Width: 1}})
	if ascii.cells != nil || ascii.text != "xy" {
		t.Errorf("ascii fast path not used: %+v", ascii)
	}
}

func TestRing(t *testing.T) {
	s := NewScrollback(3)
	for _, c := range "abcde" {
		s.Push(uv.Line{{Content: string(c), Width: 1}})
	}
	if s.Len() != 3 || s.Line(0)[0].Content != "c" || s.Line(2)[0].Content != "e" {
		t.Fatalf("ring order wrong: %d %v %v", s.Len(), s.Line(0), s.Line(2))
	}
	s.SetMaxLines(2)
	if s.Len() != 2 || s.Line(0)[0].Content != "d" {
		t.Fatal("SetMaxLines must keep newest lines")
	}
	s.SetMaxLines(10)
	s.Push(uv.Line{{Content: "f", Width: 1}})
	if s.Len() != 3 || s.Line(2)[0].Content != "f" || s.CellAt(0, 0).Content != "d" {
		t.Fatal("grow after shrink")
	}
	s.Clear()
	if s.Len() != 0 || s.Line(0) != nil {
		t.Fatal("clear")
	}
}
