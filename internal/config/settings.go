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
	"time"

	"gopkg.in/yaml.v3"
)

// Appearance is the part of the configuration changed from the settings
// dialog of the UI (its Appearance and Behavior pages).
type Appearance struct {
	TabsBottom bool
	IconMode   string // auto | unicode | ascii
	SSHIcon    Icon
	Accent     string // one of Accents
	Selection  string // one of Selections
	ShowTree   bool
	ShowNotes  bool
	Widgets    bool
	ListWidth  int    // 0 = automatic
	TabWidth   int    // the longest tab title
	TabStatus  string // icon | color: how a tab shows the state of its sessions

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
// their ANSI color numbers; the first is the default.
var Accents = []struct{ Name, Color string }{
	{"cyan", "6"}, {"blue", "4"}, {"green", "2"}, {"magenta", "5"}, {"yellow", "3"}, {"red", "1"},
}

// Selections are the colors of the selected item (host list cursor, focused
// buttons, chosen options): background and text color; "reverse" swaps the
// terminal colors. The first is the default.
var Selections = []struct{ Name, Bg, Fg string }{
	{"reverse", "", ""}, {"cyan", "6", "0"}, {"blue", "4", "15"}, {"green", "2", "0"},
	{"magenta", "5", "15"}, {"yellow", "3", "0"}, {"red", "1", "15"}, {"gray", "8", "15"},
}

// SelectionColors returns the background and text colors of a selection
// color ("", "" for reverse); ok is false for an unknown name.
func SelectionColors(name string) (bg, fg string, ok bool) {
	for _, s := range Selections {
		if s.Name == name {
			return s.Bg, s.Fg, true
		}
	}
	return "", "", false
}

// AccentColor returns the ANSI color number of an accent ("" if unknown).
func AccentColor(name string) string {
	for _, a := range Accents {
		if a.Name == name {
			return a.Color
		}
	}
	return ""
}

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
		Accent:     c.UI.Accent,
		Selection:  c.UI.Selection,
		ShowTree:   c.UI.ShowTree,
		ShowNotes:  c.UI.ShowNotes,
		Widgets:    c.Widgets.Show,
		ListWidth:  c.UI.ListWidth,
		TabWidth:   c.UI.TabWidth,
		TabStatus:  c.UI.TabStatus,

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
	if a.TabStatus != old.TabStatus {
		setPath(root, []string{"ui", "tab_status"}, str(a.TabStatus))
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
