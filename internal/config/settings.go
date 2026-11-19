// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Appearance is the part of the configuration changed from the settings
// dialog of the UI (its Appearance and Behavior pages).
type Appearance struct {
	TabsBottom bool
	IconMode   string // auto | unicode | ascii
	SSHIcon    Icon
	Theme      string // one of Themes
	Accent     string // FromTheme, one of Accents or a color of its own
	Selection  string // FromTheme, one of Selections or a color of its own
	ShowTree   bool
	ShowNotes  bool
	Widgets    bool
	ListWidth  int    // 0 = automatic
	TabWidth   int    // the longest tab title
	TabFit     bool   // tabs share the width of the window (ui.tab_fit)
	TabStatus  string // icon | color: how a tab shows the state of its sessions
	// TabCloseButton, TabStatusClose, GroupTabs: ui.tab_close_button,
	// ui.tab_status_close, ui.group_tabs
	TabCloseButton, TabStatusClose, GroupTabs bool
	// JumpTabsMenu, VNCTabsMenu: ui.jump_tabs_menu, ui.vnc_tabs_menu.
	JumpTabsMenu, VNCTabsMenu bool

	ConfirmPaste bool // ask before pasting several lines into a session
	ConnDebug    bool // ssh.debug: the steps of a failed connection in its pane
	Log          bool // the event log (log.enabled)
	RestoreTabs  bool
	RestoreView  bool

	// Security page
	LockTimeout      string // unlock.lock_timeout
	LockOnScreenLock bool   // unlock.lock_on_screen_lock
	PIN              bool   // unlock.pin
	PINAlphanumeric  bool   // unlock.pin_alphanumeric
	PINAttempts      int    // unlock.pin_attempts
	MasterAfterBoot  bool   // unlock.master_after_reboot
	MasterEveryDays  int    // unlock.master_every_days
	TouchID          bool   // unlock.touch_id
}

// Unlock is the unlock: section the Security page edits.
func (a Appearance) Unlock() Unlock {
	return Unlock{LockTimeout: a.LockTimeout, LockOnScreenLock: a.LockOnScreenLock, PIN: a.PIN,
		PINAlphanumeric: a.PINAlphanumeric, PINAttempts: a.PINAttempts, MasterAfterReboot: a.MasterAfterBoot,
		MasterEveryDays: a.MasterEveryDays, TouchID: a.TouchID}
}

// SSHIcons are the icons offered for SSH items in the settings (the first
// is the default).
var SSHIcons = []Icon{
	{"❯", ">"}, {"›", ">"}, {"$", "$"}, {"#", "#"}, {"▶", ">"}, {"»", ">>"},
	{"λ", "L"}, {"⌨", "K"}, {">_", ">_"},
}

// Accents are the accent colors (keys, focused borders, folder names) with
// their color: an ANSI number, 0-15 follow the terminal theme, 16-255 are
// the fixed 256-color palette. The first is the default. ui.accent also
// takes a color of its own: "#rrggbb" or a number 0-255.
var Accents = []struct{ Name, Color string }{
	{"cyan", "6"}, {"blue", "4"}, {"green", "2"}, {"magenta", "5"}, {"yellow", "3"}, {"red", "1"},
	{"bright-cyan", "14"}, {"bright-blue", "12"}, {"bright-green", "10"},
	{"bright-magenta", "13"}, {"bright-yellow", "11"}, {"bright-red", "9"},
	{"orange", "208"}, {"amber", "214"}, {"gold", "220"}, {"coral", "203"}, {"pink", "205"},
	{"purple", "99"}, {"violet", "141"}, {"teal", "37"}, {"sky", "75"}, {"lime", "112"}, {"slate", "67"},
}

// Selections are the colors of the selected item (host list cursor, focused
// buttons, chosen options): background and text color; "reverse" swaps the
// terminal colors. The first is the default. ui.selection_color also takes
// a background of its own ("#rrggbb" or 0-255); its text is black or white,
// whichever reads better.
var Selections = []struct{ Name, Bg, Fg string }{
	{"reverse", "", ""}, {"cyan", "6", "0"}, {"blue", "4", "15"}, {"green", "2", "0"},
	{"magenta", "5", "15"}, {"yellow", "3", "0"}, {"red", "1", "15"}, {"gray", "8", "15"},
	{"bright-cyan", "14", "0"}, {"bright-blue", "12", "0"}, {"bright-green", "10", "0"},
	{"bright-magenta", "13", "0"}, {"bright-yellow", "11", "0"}, {"bright-red", "9", "0"},
	{"orange", "208", "0"}, {"amber", "214", "0"}, {"gold", "220", "0"}, {"coral", "203", "0"}, {"pink", "205", "0"},
	{"purple", "99", "15"}, {"violet", "141", "0"}, {"teal", "30", "15"}, {"sky", "75", "0"}, {"lime", "112", "0"},
	{"slate", "60", "15"},
}

// CustomColor returns a color of the user's own in a canonical form:
// "#rrggbb" (lower case; "#rgb" is expanded) or an ANSI number 0-255.
// ok is false for anything else.
func CustomColor(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 || n > 255 {
			return "", false
		}
		return strconv.Itoa(n), true
	}
	if !strings.HasPrefix(s, "#") {
		return "", false
	}
	h := strings.ToLower(s[1:])
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if len(h) != 6 {
		return "", false
	}
	if _, err := strconv.ParseUint(h, 16, 32); err != nil {
		return "", false
	}
	return "#" + h, true
}

// SelectionColors returns the background and text colors of a selection
// color ("", "" for reverse); ok is false for an unknown name.
func SelectionColors(name string) (bg, fg string, ok bool) {
	for _, s := range Selections {
		if s.Name == name {
			return s.Bg, s.Fg, true
		}
	}
	if c, ok := CustomColor(name); ok {
		return c, ContrastText(c), true
	}
	return "", "", false
}

// AccentColor returns the color of an accent: a named one or a color of
// the user's own ("" if neither).
func AccentColor(name string) string {
	for _, a := range Accents {
		if a.Name == name {
			return a.Color
		}
	}
	c, _ := CustomColor(name)
	return c
}

// ContrastText returns the text color that reads on background c: "0"
// (black) on a light one, "15" (white) on a dark one. c is a canonical
// color (CustomColor); 0-15 use the usual xterm values.
func ContrastText(c string) string {
	r, g, b := colorRGB(c)
	// relative luminance, sRGB weights; light above the middle gray
	if 299*r+587*g+114*b > 140*1000 {
		return "0"
	}
	return "15"
}

// colorRGB returns the 8-bit components of a canonical color.
func colorRGB(c string) (r, g, b int) {
	if strings.HasPrefix(c, "#") {
		v, _ := strconv.ParseUint(c[1:], 16, 32)
		return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
	}
	n, _ := strconv.Atoi(c)
	switch {
	case n < 16:
		base := [16][3]int{{0, 0, 0}, {205, 0, 0}, {0, 205, 0}, {205, 205, 0}, {0, 0, 238}, {205, 0, 205}, {0, 205, 205}, {229, 229, 229},
			{127, 127, 127}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {92, 92, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255}}
		return base[n][0], base[n][1], base[n][2]
	case n < 232:
		lv := func(i int) int {
			if i == 0 {
				return 0
			}
			return 55 + 40*i
		}
		n -= 16
		return lv(n / 36), lv(n / 6 % 6), lv(n % 6)
	default:
		v := 8 + 10*(n-232)
		return v, v, v
	}
}

// HostColor returns the color label of a host (a canonical color, "" for
// none): field is its field color, then its tags and its folder path are
// looked up in ui.tag_colors and ui.folder_colors (the deepest folder that
// has a color). Names compare without case; a bad field value is ignored.
func (c *Config) HostColor(field string, tags, folder []string) string {
	if v := AccentColor(strings.ToLower(strings.TrimSpace(field))); v != "" {
		return v
	}
	for _, t := range tags {
		for k, v := range c.UI.TagColors {
			if strings.EqualFold(k, t) {
				return AccentColor(v)
			}
		}
	}
	for n := len(folder); n > 0; n-- {
		p := strings.Join(folder[:n], "/")
		for k, v := range c.UI.FolderColors {
			if strings.EqualFold(strings.Trim(k, "/ "), p) {
				return AccentColor(v)
			}
		}
	}
	return ""
}

// colorNames lists the accepted values for an error message.
func colorNames(names []string) string { return strings.Join(names, "|") + "|#rrggbb|0-255" }

// Appearance returns the current appearance settings.
func (c *Config) Appearance() Appearance {
	ssh := DefaultIcons[IconSSH]
	if v, ok := c.Icons.Set[IconSSH]; ok {
		if v.Unicode != "" {
			ssh.Unicode = v.Unicode
		}
		if v.ASCII != "" {
			ssh.ASCII = v.ASCII
		}
	}
	return Appearance{
		TabsBottom: c.UI.TabsPosition == "bottom",
		IconMode:   c.Icons.Mode,
		SSHIcon:    ssh,
		Theme:      c.UI.Theme,
		Accent:     c.UI.Accent,
		Selection:  c.UI.Selection,
		ShowTree:   c.UI.ShowTree,
		ShowNotes:  c.UI.ShowNotes,
		Widgets:    c.Widgets.Show,
		ListWidth:  c.UI.ListWidth,
		TabWidth:   c.UI.TabWidth,
		TabFit:     c.UI.TabFit,
		TabStatus:  c.UI.TabStatus,

		TabCloseButton: c.UI.TabCloseButton,
		TabStatusClose: c.UI.TabStatusClose,
		GroupTabs:      c.UI.GroupTabs,
		JumpTabsMenu:   c.UI.JumpTabsMenu,
		VNCTabsMenu:    c.UI.VNCTabsMenu,

		ConfirmPaste: c.Terminal.ConfirmPaste,
		ConnDebug:    c.SSH.Debug,
		Log:          c.Log.Enabled,
		RestoreTabs:  c.UI.RestoreTabs,
		RestoreView:  c.UI.RestoreView,

		LockTimeout:      c.Unlock.LockTimeout,
		LockOnScreenLock: c.Unlock.LockOnScreenLock,
		PIN:              c.Unlock.PIN,
		PINAlphanumeric:  c.Unlock.PINAlphanumeric,
		PINAttempts:      c.Unlock.PINAttempts,
		MasterAfterBoot:  c.Unlock.MasterAfterReboot,
		MasterEveryDays:  c.Unlock.MasterEveryDays,
		TouchID:          c.Unlock.TouchID,
	}
}

// SaveAppearance writes the settings of a that differ from old into the
// config file at path (created if missing). The rest of the file, comments
// included, is kept; the result is checked to load before it replaces the
// file.
func SaveAppearance(path string, old, a Appearance) error {
	return editConfig(path, func(root *yaml.Node) { setAppearance(root, old, a) })
}

func setAppearance(root *yaml.Node, old, a Appearance) {
	str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
	quoted := func(s string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.DoubleQuotedStyle}
	}
	boolean := func(v bool) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}
	}
	if a.TabsBottom != old.TabsBottom {
		pos := "top"
		if a.TabsBottom {
			pos = "bottom"
		}
		setPath(root, []string{"ui", "tabs_position"}, str(pos))
	}
	if a.IconMode != old.IconMode {
		setPath(root, []string{"icons", "mode"}, str(a.IconMode))
	}
	if a.SSHIcon != old.SSHIcon {
		setPath(root, []string{"icons", "set", IconSSH, "unicode"}, quoted(a.SSHIcon.Unicode))
		setPath(root, []string{"icons", "set", IconSSH, "ascii"}, quoted(a.SSHIcon.ASCII))
	}
	if a.Theme != old.Theme {
		setPath(root, []string{"ui", "theme"}, str(a.Theme))
	}
	if a.Accent != old.Accent {
		setPath(root, []string{"ui", "accent"}, str(a.Accent))
	}
	if a.Selection != old.Selection {
		setPath(root, []string{"ui", "selection_color"}, str(a.Selection))
	}
	if a.ShowTree != old.ShowTree {
		setPath(root, []string{"ui", "show_tree"}, boolean(a.ShowTree))
	}
	if a.ShowNotes != old.ShowNotes {
		setPath(root, []string{"ui", "show_notes"}, boolean(a.ShowNotes))
	}
	if a.Widgets != old.Widgets {
		setPath(root, []string{"widgets", "show"}, boolean(a.Widgets))
	}
	if a.ListWidth != old.ListWidth {
		setPath(root, []string{"ui", "list_width"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(a.ListWidth)})
	}
	if a.TabWidth != old.TabWidth {
		setPath(root, []string{"ui", "tab_width"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(a.TabWidth)})
	}
	if a.TabFit != old.TabFit {
		setPath(root, []string{"ui", "tab_fit"}, boolean(a.TabFit))
	}
	if a.TabStatus != old.TabStatus {
		setPath(root, []string{"ui", "tab_status"}, str(a.TabStatus))
	}
	if a.TabCloseButton != old.TabCloseButton {
		setPath(root, []string{"ui", "tab_close_button"}, boolean(a.TabCloseButton))
	}
	if a.TabStatusClose != old.TabStatusClose {
		setPath(root, []string{"ui", "tab_status_close"}, boolean(a.TabStatusClose))
	}
	if a.GroupTabs != old.GroupTabs {
		setPath(root, []string{"ui", "group_tabs"}, boolean(a.GroupTabs))
	}
	if a.JumpTabsMenu != old.JumpTabsMenu {
		setPath(root, []string{"ui", "jump_tabs_menu"}, boolean(a.JumpTabsMenu))
	}
	if a.VNCTabsMenu != old.VNCTabsMenu {
		setPath(root, []string{"ui", "vnc_tabs_menu"}, boolean(a.VNCTabsMenu))
	}
	if a.ConfirmPaste != old.ConfirmPaste {
		setPath(root, []string{"terminal", "confirm_paste"}, boolean(a.ConfirmPaste))
	}
	if a.ConnDebug != old.ConnDebug {
		setPath(root, []string{"ssh", "debug"}, boolean(a.ConnDebug))
	}
	if a.Log != old.Log {
		setPath(root, []string{"log", "enabled"}, boolean(a.Log))
	}
	if a.RestoreTabs != old.RestoreTabs {
		setPath(root, []string{"ui", "restore_tabs"}, boolean(a.RestoreTabs))
	}
	if a.RestoreView != old.RestoreView {
		setPath(root, []string{"ui", "restore_filters"}, boolean(a.RestoreView))
	}
	if a.LockTimeout != old.LockTimeout {
		setPath(root, []string{"unlock", "lock_timeout"}, str(a.LockTimeout))
	}
	if a.LockOnScreenLock != old.LockOnScreenLock {
		setPath(root, []string{"unlock", "lock_on_screen_lock"}, boolean(a.LockOnScreenLock))
	}
	integer := func(v int) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)} }
	if a.PIN != old.PIN {
		setPath(root, []string{"unlock", "pin"}, boolean(a.PIN))
	}
	if a.PINAlphanumeric != old.PINAlphanumeric {
		setPath(root, []string{"unlock", "pin_alphanumeric"}, boolean(a.PINAlphanumeric))
	}
	if a.PINAttempts != old.PINAttempts {
		setPath(root, []string{"unlock", "pin_attempts"}, integer(a.PINAttempts))
	}
	if a.MasterAfterBoot != old.MasterAfterBoot {
		setPath(root, []string{"unlock", "master_after_reboot"}, boolean(a.MasterAfterBoot))
	}
	if a.TouchID != old.TouchID {
		setPath(root, []string{"unlock", "touch_id"}, boolean(a.TouchID))
	}
	if a.MasterEveryDays != old.MasterEveryDays {
		setPath(root, []string{"unlock", "master_every_days"}, integer(a.MasterEveryDays))
	}
}

// editConfig changes the config file at path (created if missing) with
// edit, which gets the top-level mapping. The rest of the file, comments
// included, is kept; the result is checked to load before it atomically
// replaces the file.
func editConfig(path string, edit func(root *yaml.Node)) error {
	b, err := readFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(b)) > 0 {
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a mapping", path)
	}
	edit(root)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	_ = enc.Close()
	if err := checkLoads(path, out.Bytes()); err != nil {
		return err
	}
	return writeAtomic(path, out.Bytes())
}

// setPath sets the value at a key path of a mapping, creating the missing
// mappings and keys.
func setPath(m *yaml.Node, keys []string, val *yaml.Node) {
	k := keys[0]
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value != k {
			continue
		}
		if len(keys) == 1 {
			val.LineComment = m.Content[j+1].LineComment
			m.Content[j+1] = val
			return
		}
		if m.Content[j+1].Kind != yaml.MappingNode {
			m.Content[j+1] = &yaml.Node{Kind: yaml.MappingNode} // e.g. "set:" without a value
		}
		setPath(m.Content[j+1], keys[1:], val)
		return
	}
	child := val
	if len(keys) > 1 {
		child = &yaml.Node{Kind: yaml.MappingNode}
		setPath(child, keys[1:], val)
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}, child)
}

// checkLoads verifies that b is a valid configuration.
func checkLoads(path string, b []byte) error {
	tmp, err := os.CreateTemp("", "vaultcrab-config-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if _, err := Load(tmp.Name()); err != nil {
		return fmt.Errorf("the changed config would not load: %w", err)
	}
	return nil
}

// writeAtomic replaces path with b, keeping the file mode (0600 for a new
// file).
func writeAtomic(path string, b []byte) error {
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err != nil {
		return err
	}
	return rename(tmp.Name(), path)
}

// rename replaces newpath; on Windows, a file that another program (an
// editor, another vaultcrab reading it) has open refuses that for a moment.
func rename(oldpath, newpath string) error {
	err := replaceFile(oldpath, newpath)
	for wait := time.Duration(0); err != nil && runtime.GOOS == "windows" && errors.Is(err, fs.ErrPermission) && wait < 2*time.Second; wait += 10 * time.Millisecond {
		time.Sleep(10 * time.Millisecond)
		err = replaceFile(oldpath, newpath)
	}
	return err
}
