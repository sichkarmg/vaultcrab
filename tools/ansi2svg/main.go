// SPDX-License-Identifier: MIT

// Command ansi2svg renders a terminal capture with ANSI styles (for example
// `tmux capture-pane -e -p`) into an SVG image. Used to make the README
// screenshots:
//
//	tmux capture-pane -e -p -t s | go run ./tools/ansi2svg -w 130 -h 36 > docs/screenshot.svg
package main

import (
	"bufio"
	"flag"
	"fmt"
	"html"
	"image/color"
	"io"
	"os"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

const (
	cellW    = 8.4
	cellH    = 18.0
	fontSize = 14.0
	pad      = 12.0
)

var (
	defFg = color.RGBA{0xd8, 0xdc, 0xe4, 0xff}
	defBg = color.RGBA{0x1d, 0x20, 0x26, 0xff}
	// A softer 16-colour palette than the xterm default.
	palette = [16]color.RGBA{
		{0x2e, 0x34, 0x40, 0xff}, {0xe0, 0x6c, 0x75, 0xff}, {0x98, 0xc3, 0x79, 0xff}, {0xe5, 0xc0, 0x7b, 0xff},
		{0x61, 0xaf, 0xef, 0xff}, {0xc6, 0x78, 0xdd, 0xff}, {0x56, 0xb6, 0xc2, 0xff}, {0xd8, 0xdc, 0xe4, 0xff},
		{0x5c, 0x63, 0x70, 0xff}, {0xff, 0x7b, 0x86, 0xff}, {0xb5, 0xe8, 0x90, 0xff}, {0xff, 0xd6, 0x8f, 0xff},
		{0x7c, 0xc4, 0xff, 0xff}, {0xde, 0x95, 0xf0, 0xff}, {0x7f, 0xd6, 0xe0, 0xff}, {0xff, 0xff, 0xff, 0xff},
	}
)

func main() {
	w := flag.Int("w", 120, "columns")
	h := flag.Int("h", 35, "rows")
	title := flag.String("title", "Vaultcrab", "window title")
	flag.Parse()

	emu := vt.NewEmulator(*w, *h)
	go io.Copy(io.Discard, emu) // drain replies
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	first := true
	for sc.Scan() {
		if !first {
			emu.WriteString("\r\n")
		}
		first = false
		emu.WriteString("\x1b[0m" + sc.Text())
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()
	width := float64(*w)*cellW + 2*pad
	height := float64(*h)*cellH + 2*pad + 28
	fmt.Fprintf(out, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`+"\n", width, height, width, height)
	fmt.Fprintf(out, `<rect width="100%%" height="100%%" rx="8" fill="%s"/>`+"\n", hex(defBg))
	fmt.Fprintf(out, `<circle cx="20" cy="15" r="5.5" fill="#ff5f57"/><circle cx="38" cy="15" r="5.5" fill="#febc2e"/><circle cx="56" cy="15" r="5.5" fill="#28c840"/>`+"\n")
	fmt.Fprintf(out, `<text x="%.0f" y="19" fill="#8b919c" font-family="DejaVu Sans, sans-serif" font-size="12" text-anchor="middle">%s</text>`+"\n", width/2, html.EscapeString(*title))
	fmt.Fprintf(out, `<g transform="translate(%.1f %.1f)" font-family="DejaVu Sans Mono, Menlo, Consolas, monospace" font-size="%.1f" xml:space="preserve">`+"\n", pad, pad+28, fontSize)

	for y := 0; y < *h; y++ {
		// backgrounds
		for x := 0; x < *w; {
			_, bg := colors(emu.CellAt(x, y))
			n := 1
			for x+n < *w {
				_, bg2 := colors(emu.CellAt(x+n, y))
				if bg2 != bg {
					break
				}
				n++
			}
			if bg != defBg {
				fmt.Fprintf(out, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, float64(x)*cellW, float64(y)*cellH, float64(n)*cellW+0.5, cellH+0.5, hex(bg))
			}
			x += n
		}
		// text: one <text> per cell run with equal style, positioned per cell
		// so wide characters keep the grid.
		for x := 0; x < *w; x++ {
			c := emu.CellAt(x, y)
			if c == nil || c.Content == "" || c.Content == " " {
				continue
			}
			fg, _ := colors(c)
			attrs := ""
			if c.Style.Attrs&uv.AttrBold != 0 {
				attrs += ` font-weight="bold"`
			}
			if c.Style.Attrs&uv.AttrFaint != 0 {
				attrs += ` opacity="0.6"`
			}
			if c.Style.Attrs&uv.AttrItalic != 0 {
				attrs += ` font-style="italic"`
			}
			if c.Style.Underline != 0 {
				attrs += ` text-decoration="underline"`
			}
			cw := cellW * float64(max(c.Width, 1))
			fmt.Fprintf(out, `<text x="%.1f" y="%.1f" fill="%s"%s textLength="%.1f" lengthAdjust="spacingAndGlyphs">%s</text>`,
				float64(x)*cellW, float64(y)*cellH+fontSize, hex(fg), attrs, cw, html.EscapeString(c.Content))
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "</g></svg>")
}

func colors(c *uv.Cell) (fg, bg color.RGBA) {
	fg, bg = defFg, defBg
	if c == nil {
		return
	}
	if c.Style.Fg != nil {
		fg = rgba(c.Style.Fg)
	}
	if c.Style.Bg != nil {
		bg = rgba(c.Style.Bg)
	}
	if c.Style.Attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
	}
	return
}

func rgba(c color.Color) color.RGBA {
	switch v := c.(type) {
	case ansi.BasicColor:
		if v < 16 {
			return palette[v]
		}
	case ansi.IndexedColor:
		if v < 16 {
			return palette[v]
		}
	}
	r, g, b, _ := c.RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff}
}

func hex(c color.RGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
