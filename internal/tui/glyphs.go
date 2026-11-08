// SPDX-License-Identifier: MIT

package tui

import "github.com/charmbracelet/lipgloss"

// glyphs are the box-drawing characters used by the UI, with ASCII fallbacks
// for terminals without Unicode support (icons.mode: ascii).
type glyphs struct {
	h, v, ell, more, check string
	jump, arrow            string // a session through a jump host: "↪ jump → target"
	border                 lipgloss.Border
}

var (
	unicodeGlyphs = glyphs{h: "─", v: "│", ell: "…", more: "‹", check: "✓", jump: "↪", arrow: "→", border: lipgloss.RoundedBorder()}
	asciiGlyphs   = glyphs{h: "-", v: "|", ell: "~", more: "<", check: "+", jump: "~>", arrow: "->", border: lipgloss.Border{
		Top: "-", Bottom: "-", Left: "|", Right: "|",
		TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
		MiddleLeft: "+", MiddleRight: "+", Middle: "+", MiddleTop: "+", MiddleBottom: "+",
	}}
)
