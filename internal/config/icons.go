// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"runtime"
	"strings"
)

// Icon names used by the UI.
const (
	IconConnected    = "connected"
	IconConnecting   = "connecting"
	IconDisconnected = "disconnected"
	IconIdle         = "idle"
	IconSSH          = "ssh"
	IconVNC          = "vnc"
	IconSMB          = "smb"
	IconFTP          = "ftp"
	IconSFTP         = "sftp"
	IconRDP          = "rdp"
	IconLocal        = "local"
	IconQuit         = "quit"
	IconSettings     = "settings"
	IconFolder       = "folder"
	IconFolderOpen   = "folder_open"
	IconOrg          = "org"
	IconCollection   = "collection"
	IconOnline       = "online"
	IconOffline      = "offline"
	IconLocked       = "locked"
	IconSyncing      = "syncing"
	IconFav          = "fav"
	IconZoom         = "zoom"
	IconHistory      = "history"
	IconCached       = "cached"
)

// DefaultIcons is the built-in icon set. Every icon has an ASCII variant.
var DefaultIcons = map[string]Icon{
	IconConnected:    {"●", "*"},
	IconConnecting:   {"◌", "~"},
	IconDisconnected: {"✗", "x"},
	IconIdle:         {"○", "o"},
	IconSSH:          {"❯", ">"}, // a shell prompt (choices: SSHIcons)
	IconVNC:          {"▣", "#"},
	IconSMB:          {"⛁", "$"},
	IconFTP:          {"⇅", "F"},
	IconSFTP:         {"⇵", "S"},
	IconRDP:          {"▤", "R"},
	IconLocal:        {"⌂", "L"},
	IconQuit:         {"⊗", "Q"}, // unlike ✗ of a broken session
	IconSettings:     {"⚙", "="},
	IconFolder:       {"▸", "+"},
	IconFolderOpen:   {"▾", "-"},
	IconOrg:          {"◆", "@"},
	IconCollection:   {"◇", "%"},
	IconOnline:       {"●", "ON"},
	IconOffline:      {"○", "OFF"},
	IconLocked:       {"🔒", "LOCK"},
	IconSyncing:      {"↻", "SYNC"},
	IconFav:          {"★", "*"},
	IconZoom:         {"⤢", "Z"},
	IconHistory:      {"↑", "^"},
	IconCached:       {"⌛", "C"},
}

// IconSet resolves icons for the chosen mode.
type IconSet struct {
	ascii bool
	set   map[string]Icon
}

// IconSet returns the resolved icon set (mode auto is decided from the environment).
func (c *Config) IconSet() IconSet {
	set := make(map[string]Icon, len(DefaultIcons))
	for k, v := range DefaultIcons {
		set[k] = v
	}
	for k, v := range c.Icons.Set {
		d := set[k]
		if v.Unicode != "" {
			d.Unicode = v.Unicode
		}
		if v.ASCII != "" {
			d.ASCII = v.ASCII
		}
		set[k] = d
	}
	return IconSet{ascii: asciiMode(c.Icons.Mode), set: set}
}

func asciiMode(mode string) bool {
	return mode == "ascii" || mode == "auto" && !UnicodeCapable(os.Getenv, runtime.GOOS)
}

// With returns a copy of the set with another mode (auto | unicode |
// ascii) and the icon name replaced by ic.
func (s IconSet) With(mode, name string, ic Icon) IconSet {
	set := make(map[string]Icon, len(s.set))
	for k, v := range s.set {
		set[k] = v
	}
	set[name] = ic
	return IconSet{ascii: asciiMode(mode), set: set}
}

// Icon returns both variants of the icon name.
func (s IconSet) Icon(name string) Icon { return s.set[name] }

// ASCII reports whether ASCII icons are used.
func (s IconSet) ASCII() bool { return s.ascii }

// Get returns the icon for name.
func (s IconSet) Get(name string) string {
	ic := s.set[name]
	if s.ascii {
		return ic.ASCII
	}
	return ic.Unicode
}

// Pick chooses between a unicode and an ASCII variant (e.g. filter icons).
func (s IconSet) Pick(unicode, ascii string) string {
	if s.ascii || unicode == "" {
		return ascii
	}
	return unicode
}

// UnicodeCapable guesses whether the terminal renders Unicode symbols:
// legacy Windows console (outside Windows Terminal) and the Linux virtual
// console get ASCII; elsewhere a UTF-8 locale is required.
func UnicodeCapable(getenv func(string) string, goos string) bool {
	if goos == "windows" {
		return getenv("WT_SESSION") != "" || getenv("TERM_PROGRAM") != "" || strings.Contains(getenv("TERM"), "xterm")
	}
	if getenv("TERM") == "linux" {
		return false
	}
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := getenv(k); v != "" {
			v = strings.ToLower(v)
			return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
		}
	}
	return goos == "darwin" // macOS terminals are UTF-8 even without LANG
}
