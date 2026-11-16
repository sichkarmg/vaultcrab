// SPDX-License-Identifier: MIT

package config

// A Theme colors the whole interface at once: the accent (keys, focused
// borders, folder names), the selection (cursor, active tab, status bar,
// buttons), pane borders, session states and organizations. It does not
// paint the terminal background: pick a theme that suits it (the *-light
// ones are for a light background). Colors are ANSI numbers or "#rrggbb";
// "" in SelBg is the reverse of the terminal colors.
type Theme struct {
	Name         string
	Accent       string
	SelBg, SelFg string
	Border       string // pane separators that are not focused
	OK, Warn     string // connected; connecting
	Err          string // broken, errors
	Activity     string // new output in a hidden tab
	Org          string // organization names
}

// FromTheme as ui.accent or ui.selection_color takes the color of the theme.
const FromTheme = "theme"

// Themes are the themes of ui.theme; the first is the default (the
// terminal colors, as before themes existed). The palettes of nord,
// gruvbox, dracula, solarized and catppuccin follow those published color
// schemes (MIT licensed).
var Themes = []Theme{
	{Name: "default", Accent: "6", Border: "8", OK: "2", Warn: "3", Err: "1", Activity: "12", Org: "5"},
	{Name: "ocean", Accent: "39", SelBg: "25", SelFg: "15", Border: "24", OK: "37", Warn: "214", Err: "203", Activity: "81", Org: "141"},
	{Name: "forest", Accent: "114", SelBg: "28", SelFg: "15", Border: "65", OK: "112", Warn: "178", Err: "167", Activity: "150", Org: "179"},
	{Name: "sunset", Accent: "208", SelBg: "204", SelFg: "0", Border: "95", OK: "150", Warn: "220", Err: "196", Activity: "213", Org: "177"},
	{Name: "mono", Accent: "252", SelBg: "240", SelFg: "15", Border: "238", OK: "250", Warn: "246", Err: "203", Activity: "255", Org: "248"},
	{Name: "nord", Accent: "#88c0d0", SelBg: "#5e81ac", SelFg: "#eceff4", Border: "#4c566a", OK: "#a3be8c", Warn: "#ebcb8b", Err: "#bf616a", Activity: "#81a1c1", Org: "#b48ead"},
	{Name: "gruvbox", Accent: "#8ec07c", SelBg: "#d79921", SelFg: "#282828", Border: "#665c54", OK: "#b8bb26", Warn: "#fabd2f", Err: "#fb4934", Activity: "#83a598", Org: "#d3869b"},
	{Name: "gruvbox-light", Accent: "#427b58", SelBg: "#b57614", SelFg: "#fbf1c7", Border: "#bdae93", OK: "#79740e", Warn: "#b57614", Err: "#9d0006", Activity: "#076678", Org: "#8f3f71"},
	{Name: "dracula", Accent: "#8be9fd", SelBg: "#bd93f9", SelFg: "#282a36", Border: "#6272a4", OK: "#50fa7b", Warn: "#f1fa8c", Err: "#ff5555", Activity: "#ff79c6", Org: "#ffb86c"},
	{Name: "solarized", Accent: "#2aa198", SelBg: "#268bd2", SelFg: "#fdf6e3", Border: "#586e75", OK: "#859900", Warn: "#b58900", Err: "#dc322f", Activity: "#6c71c4", Org: "#d33682"},
	{Name: "solarized-light", Accent: "#2aa198", SelBg: "#268bd2", SelFg: "#fdf6e3", Border: "#93a1a1", OK: "#859900", Warn: "#b58900", Err: "#dc322f", Activity: "#6c71c4", Org: "#d33682"},
	{Name: "catppuccin", Accent: "#89dceb", SelBg: "#cba6f7", SelFg: "#1e1e2e", Border: "#585b70", OK: "#a6e3a1", Warn: "#f9e2af", Err: "#f38ba8", Activity: "#f5c2e7", Org: "#fab387"},
	{Name: "catppuccin-latte", Accent: "#04a5e5", SelBg: "#8839ef", SelFg: "#eff1f5", Border: "#9ca0b0", OK: "#40a02b", Warn: "#df8e1d", Err: "#d20f39", Activity: "#ea76cb", Org: "#fe640b"},
}

// ThemeByName returns a theme of Themes.
func ThemeByName(name string) (Theme, bool) {
	for _, t := range Themes {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

// Palette returns the colors in use: theme (the default one if unknown)
// with the accent and the selection replaced unless they are FromTheme.
func Palette(theme, accent, selection string) Theme {
	t, ok := ThemeByName(theme)
	if !ok {
		t = Themes[0]
	}
	if accent != FromTheme {
		if c := AccentColor(accent); c != "" {
			t.Accent = c
		}
	}
	if selection != FromTheme {
		if bg, fg, ok := SelectionColors(selection); ok {
			t.SelBg, t.SelFg = bg, fg
		}
	}
	return t
}
