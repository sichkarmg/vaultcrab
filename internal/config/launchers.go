// SPDX-License-Identifier: MIT

package config

import (
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Launchers is the part of the configuration edited on the External
// programs page of the settings: the clients of the external schemes and
// the command lines of the user scripts.
type Launchers struct {
	External map[string]Launch // vnc, smb, ftp, sftp, rdp
	Scripts  []ScriptSpec
}

// Launchers returns a copy of the external programs and the scripts.
func (c *Config) Launchers() Launchers {
	l := Launchers{External: map[string]Launch{}, Scripts: slices.Clone(c.Scripts)}
	for k, v := range c.External {
		l.External[k] = v.clone()
	}
	for i := range l.Scripts {
		l.Scripts[i].Run = l.Scripts[i].Run.clone()
	}
	return l
}

func (l Launch) clone() Launch {
	l.PerOS = l.PerOS.clone()
	return l
}

func (p PerOS) clone() PerOS {
	return PerOS{Linux: slices.Clone(p.Linux), Windows: slices.Clone(p.Windows), Darwin: slices.Clone(p.Darwin)}
}

// OSes are the operating systems of a PerOS command, as in the config.
var OSes = []string{"linux", "windows", "darwin"}

// Get returns the command for goos.
func (p PerOS) Get(goos string) []string {
	switch goos {
	case "windows":
		return p.Windows
	case "darwin":
		return p.Darwin
	}
	return p.Linux
}

// Set sets the command for goos.
func (p *PerOS) Set(goos string, cmd []string) {
	switch goos {
	case "windows":
		p.Windows = cmd
	case "darwin":
		p.Darwin = cmd
	default:
		p.Linux = cmd
	}
}

// SaveLaunchers writes what differs between old and l into the config file
// at path (created if missing): the changed command lines and password
// settings only. Everything else, comments included, is kept; the result
// must load, and replaces the file atomically (a temporary file next to it,
// renamed over it).
func SaveLaunchers(path string, old, l Launchers) error {
	return editConfig(path, func(root *yaml.Node) {
		for name, nl := range l.External {
			ol := old.External[name]
			for _, goos := range OSes {
				if !slices.Equal(ol.Get(goos), nl.Get(goos)) {
					setCommand(root, []string{"external", name, goos}, nl.Get(goos))
				}
			}
			if ol.Password != nl.Password {
				if nl.Password == "" {
					deletePath(root, []string{"external", name, "password"})
				} else {
					setPath(root, []string{"external", name, "password"}, scalar(nl.Password))
				}
			}
			if ol.PasswordStdin != nl.PasswordStdin {
				setFlag(root, []string{"external", name, "password_stdin"}, nl.PasswordStdin)
			}
		}
		saveScripts(root, old.Scripts, l.Scripts)
	})
}

// saveScripts writes the changed commands of the scripts. A config without
// scripts: uses the built-in ones; they are written out in full then.
func saveScripts(root *yaml.Node, old, cur []ScriptSpec) {
	changed := len(old) != len(cur)
	for i := 0; !changed && i < len(cur); i++ {
		changed = old[i].PasswordStdin != cur[i].PasswordStdin || !slices.EqualFunc(OSes, OSes, func(g, _ string) bool {
			return slices.Equal(old[i].Run.Get(g), cur[i].Run.Get(g))
		})
	}
	if !changed {
		return
	}
	seq := lookup(root, "scripts")
	if seq == nil || seq.Kind != yaml.SequenceNode || len(seq.Content) != len(cur) {
		items := &yaml.Node{Kind: yaml.SequenceNode}
		for _, sc := range cur {
			items.Content = append(items.Content, scriptNode(sc))
		}
		setPath(root, []string{"scripts"}, items)
		return
	}
	for i, sc := range cur {
		item := seq.Content[i]
		for _, goos := range OSes {
			if !slices.Equal(old[i].Run.Get(goos), sc.Run.Get(goos)) {
				setCommand(item, []string{"run", goos}, sc.Run.Get(goos))
			}
		}
		if old[i].PasswordStdin != sc.PasswordStdin {
			setFlag(item, []string{"password_stdin"}, sc.PasswordStdin)
		}
	}
}

func scriptNode(sc ScriptSpec) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	setPath(n, []string{"name"}, scalar(sc.Name))
	if sc.Rule != "" {
		setPath(n, []string{"rule"}, scalar(sc.Rule))
	}
	if sc.Mode != "" {
		setPath(n, []string{"mode"}, scalar(sc.Mode))
	}
	for _, goos := range OSes {
		if cmd := sc.Run.Get(goos); len(cmd) > 0 {
			setCommand(n, []string{"run", goos}, cmd)
		}
	}
	if sc.PasswordStdin {
		setFlag(n, []string{"password_stdin"}, true)
	}
	if sc.Default {
		setFlag(n, []string{"default"}, true)
	}
	return n
}

// setCommand sets a command line as a one-line list, or removes the key
// for an empty one (auto-detection for an external client).
func setCommand(m *yaml.Node, keys []string, cmd []string) {
	if len(cmd) == 0 {
		deletePath(m, keys)
		return
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, a := range cmd {
		seq.Content = append(seq.Content, scalar(a))
	}
	setPath(m, keys, seq)
}

// setFlag sets a boolean option, or removes the key when it is false.
func setFlag(m *yaml.Node, keys []string, v bool) {
	if !v {
		deletePath(m, keys)
		return
	}
	setPath(m, keys, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)})
}

func scalar(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

// lookup returns the value of key in mapping m, nil if absent.
func lookup(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key {
			return m.Content[j+1]
		}
	}
	return nil
}

// deletePath removes the key at a key path of a mapping, if present.
func deletePath(m *yaml.Node, keys []string) {
	for _, k := range keys[:len(keys)-1] {
		if m = lookup(m, k); m == nil {
			return
		}
	}
	if m.Kind != yaml.MappingNode {
		return
	}
	last := keys[len(keys)-1]
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == last {
			m.Content = append(m.Content[:j], m.Content[j+2:]...)
			return
		}
	}
}

// Clone returns a deep copy.
func (l Launchers) Clone() Launchers {
	c := Config{External: l.External, Scripts: l.Scripts}
	return c.Launchers()
}
