// SPDX-License-Identifier: MIT

package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/layout"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

func newSess(name string, fail bool) *session.Session {
	return session.New(model.Host{Name: name}, session.Options{
		Cols: 10, Rows: 5,
		Secret: func(context.Context) (secret.Secret, error) { return secret.FromString("x"), nil },
		Dial: func(context.Context, model.Host, *secret.Secret, int, int) (session.Remote, error) {
			return nil, errors.New("refused")
		},
	})
}

func TestSplitResizeTake(t *testing.T) {
	a := newSess("a", false)
	tab := New(a)
	tab.SetSize(81, 25)
	if w, h := a.Term.Size(); w != 81 || h != 25 {
		t.Fatalf("single pane term %dx%d (no header)", w, h)
	}
	b := newSess("b", false)
	idB := tab.Split(layout.Horizontal, b)
	if tab.Focused() != idB || tab.Len() != 2 || !tab.Headers() {
		t.Fatal("split")
	}
	if w, h := a.Term.Size(); w != 40 || h != 24 {
		t.Fatalf("a term %dx%d", w, h)
	}
	if w, h := b.Term.Size(); w != 40 || h != 24 {
		t.Fatalf("b term %dx%d", w, h)
	}
	// focus by direction
	if !tab.FocusDir(layout.Left) || tab.FocusedSession() != a {
		t.Fatal("focus left")
	}
	// keyboard resize: a grows right by 10
	if !tab.ResizeFocused(layout.Right, 10) {
		t.Fatal("resize")
	}
	if w, _ := a.Term.Size(); w != 50 {
		t.Fatalf("a width %d", w)
	}
	if w, _ := b.Term.Size(); w != 30 {
		t.Fatalf("b width %d", w)
	}
	// mouse drag of the border to x=20
	tab.DragBorder(tab.Layout.Root(), 20)
	if w, _ := a.Term.Size(); w != 20 {
		t.Fatalf("after drag a width %d", w)
	}
	// zoom
	tab.ToggleZoom()
	if !tab.Zoomed() || tab.Headers() {
		t.Fatal("zoom")
	}
	if w, h := a.Term.Size(); w != 81 || h != 25 {
		t.Fatalf("zoomed a %dx%d", w, h)
	}
	if _, ok := tab.TermRect(idB); ok {
		t.Error("hidden pane has a rect while zoomed")
	}
	tab.Focus(idB) // focusing another pane unzooms
	if tab.Zoomed() {
		t.Fatal("still zoomed")
	}
	s, last := tab.Take(idB)
	if s != b || last || tab.Len() != 1 || tab.FocusedSession() != a {
		t.Fatal("take")
	}
	if w, h := a.Term.Size(); w != 81 || h != 25 {
		t.Fatalf("a after take %dx%d", w, h)
	}
	if s, last := tab.Take(tab.Focused()); s != a || !last {
		t.Fatal("take last")
	}
}

func TestAggregatedState(t *testing.T) {
	a, b := newSess("a", false), newSess("b", true)
	tab := New(a)
	tab.SetSize(40, 10)
	tab.Split(layout.Vertical, b)
	if tab.State() != session.Idle || tab.Live() != 0 {
		t.Fatal("idle")
	}
	b.Connect() // fails -> Disconnected
	deadline := time.Now().Add(2 * time.Second)
	for tab.State() != session.Disconnected {
		if time.Now().After(deadline) {
			t.Fatal("no disconnect")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st, _ := a.State(); st != session.Idle {
		t.Error("broken pane affected its neighbour")
	}
}
