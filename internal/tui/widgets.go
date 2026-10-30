// SPDX-License-Identifier: MIT

package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// widgetsShown reports whether the host list shows the widget column at
// screen width w; the widgets update only then, and only while the terminal
// window has the focus.
func (l *hostList) widgetsShown(w int) bool {
	m := l.m
	show := l.widgets && w >= max(m.opt.WidgetsMin, m.opt.WidgetsWidth+40) && m.active == 0
	m.svc.SetWidgetsActive(show && !m.blurred)
	return show
}

// widgetLines renders the widget column: each widget is a title and its
// output, separated by lines; the hint how to add your own is at the bottom.
func (l *hostList) widgetLines(w, h int) []string {
	m := l.m
	var out []string
	for i, o := range m.svc.Widgets() {
		if i > 0 {
			out = append(out, stBorder.Render(strings.Repeat(m.g.h, w)))
		}
		out = append(out, " "+stBold.Render(ansi.Truncate(o.Title, w-1, m.g.ell)))
		lines := o.Lines
		if o.Err != nil {
			lines = append(append([]string(nil), lines...), stErr.Render(o.Err.Error()))
		}
		if len(lines) == 0 && o.At.IsZero() {
			lines = []string{stDim.Render(m.ascii("…"))}
		}
		for _, ln := range lines {
			out = append(out, " "+ansi.Truncate(ln, w-1, m.g.ell))
		}
	}
	cfg := "config.yaml"
	if m.opt.ConfigPath != "" {
		cfg = filepath.Base(m.opt.ConfigPath)
	}
	hint := []string{
		stBorder.Render(strings.Repeat(m.g.h, w)),
		stDim.Render(" Your own scripts here:"),
		stDim.Render(" widgets.items in " + cfg),
		stDim.Render(" (w hides this column)"),
	}
	for len(out)+len(hint) < h {
		out = append(out, "")
	}
	if len(out)+len(hint) > h {
		out = out[:max(0, h-len(hint))]
	}
	out = append(out, hint...)
	return fitLines(out, w, h)
}
