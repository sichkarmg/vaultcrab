// SPDX-License-Identifier: MIT

package config

import (
	"errors"

	"gopkg.in/yaml.v3"
)

// AddVault appends v to the vaults: of the config file at path (created if
// missing); the rest of the file, comments included, is kept.
func AddVault(path string, v VaultSpec) error {
	if !validVaultID(v.ID) {
		return errors.New("the ID may only have a-z, 0-9 and - (up to 32 characters)")
	}
	return editConfig(path, func(root *yaml.Node) {
		str := func(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
		item := &yaml.Node{Kind: yaml.MappingNode}
		add := func(k, val string) {
			if val != "" {
				item.Content = append(item.Content, str(k), str(val))
			}
		}
		add("id", v.ID)
		add("name", v.Name)
		add("server", v.Server)
		add("data_dir", v.DataDir)
		add("url", v.URL)
		seq := vaultsNode(root)
		seq.Content = append(seq.Content, item)
	})
}

// RemoveVault removes the vault id from the vaults: of the config file at
// path (its bw data directory is left on disk).
func RemoveVault(path, id string) error {
	return editConfig(path, func(root *yaml.Node) {
		seq := vaultsNode(root)
		out := seq.Content[:0]
		for _, it := range seq.Content {
			if mappingValue(it, "id") != id {
				out = append(out, it)
			}
		}
		seq.Content = out
	})
}

// vaultsNode returns the vaults: sequence of root, created if missing.
func vaultsNode(root *yaml.Node) *yaml.Node {
	for j := 0; j+1 < len(root.Content); j += 2 {
		if root.Content[j].Value == "vaults" {
			if root.Content[j+1].Kind != yaml.SequenceNode {
				root.Content[j+1] = &yaml.Node{Kind: yaml.SequenceNode}
			}
			return root.Content[j+1]
		}
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "vaults"}, seq)
	return seq
}

func mappingValue(m *yaml.Node, key string) string {
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key {
			return m.Content[j+1].Value
		}
	}
	return ""
}
