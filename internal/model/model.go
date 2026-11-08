// SPDX-License-Identifier: MIT

// Package model contains vault metadata types. Nothing here ever holds a
// secret: passwords, keys, hidden fields and notes are not part of the model.
package model

import (
	"sort"
	"strings"
	"time"
)

// Scheme is the connection type of a host. Adding a protocol starts here
// (a constant and an entry in Schemes); see docs/extending.md.
type Scheme string

const (
	SSH  Scheme = "ssh"
	VNC  Scheme = "vnc"
	SMB  Scheme = "smb"
	FTP  Scheme = "ftp"
	SFTP Scheme = "sftp"
	RDP  Scheme = "rdp"
)

// Local is the pseudo-scheme of a local shell (not a vault item, not in
// Schemes).
const Local Scheme = "local"

// Schemes lists the known connection types in display order.
var Schemes = []Scheme{SSH, VNC, SMB, FTP, SFTP, RDP}

// ParseScheme returns the scheme for a (case-insensitive) name.
func ParseScheme(name string) (Scheme, bool) {
	s := Scheme(strings.ToLower(strings.TrimSpace(name)))
	for _, k := range Schemes {
		if k == s {
			return s, true
		}
	}
	return "", false
}

// External reports whether hosts of this scheme are opened by an external
// program (everything except the built-in terminal).
func (s Scheme) External() bool { return s != SSH && s != Local }

// DefaultPort returns the well-known port for the scheme.
func (s Scheme) DefaultPort() int {
	switch s {
	case SSH, SFTP:
		return 22
	case VNC:
		return 5900
	case SMB:
		return 445
	case FTP:
		return 21
	case RDP:
		return 3389
	}
	return 0
}

// Host is a connectable vault item.
type Host struct {
	ID            string            `json:"id"` // vault item ID
	Name          string            `json:"name"`
	Scheme        Scheme            `json:"scheme"`
	User          string            `json:"user,omitempty"`
	Addr          string            `json:"addr"`
	Port          int               `json:"port,omitempty"`
	Path          string            `json:"path,omitempty"` // SMB share path, without leading slash
	FolderID      string            `json:"folderId,omitempty"`
	OrgID         string            `json:"orgId,omitempty"`
	CollectionIDs []string          `json:"collectionIds,omitempty"`
	Fields        map[string]string `json:"fields,omitempty"` // non-hidden custom fields
	Tags          []string          `json:"tags,omitempty"`
	Favorite      bool              `json:"favorite,omitempty"`
	Created       time.Time         `json:"created,omitzero"` // when the item was created in the vault
	Revised       time.Time         `json:"revised,omitzero"` // when it was last changed
}

// Target returns "user@addr[:port]" for display.
func (h *Host) Target() string {
	var b strings.Builder
	if h.User != "" {
		b.WriteString(h.User)
		b.WriteByte('@')
	}
	if strings.Contains(h.Addr, ":") {
		b.WriteString("[" + h.Addr + "]")
	} else {
		b.WriteString(h.Addr)
	}
	if h.Port != 0 && h.Port != h.Scheme.DefaultPort() {
		b.WriteString(":")
		b.WriteString(itoa(h.Port))
	}
	if h.Path != "" {
		b.WriteString("/" + h.Path)
	}
	return b.String()
}

// HasTag reports whether the host has the tag (case-insensitive).
func (h *Host) HasTag(tag string) bool {
	for _, t := range h.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// Credential is a Login item that is not a host but marked as an SSH
// credential (a custom field "credential" = true): its password field holds
// a password or a private key used for hosts typed by hand (through a jump
// host). Metadata only.
type Credential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	User     string `json:"user,omitempty"`
	FolderID string `json:"folderId,omitempty"`
	OrgID    string `json:"orgId,omitempty"`
}

// CredentialField marks a Login item as an SSH credential.
const CredentialField = "credential"

// Folder is a vault folder. Nested folders use "/" in the name; Path is the
// split form ("A/B/C" → [A B C]).
type Folder struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Path []string `json:"path"`
}

// Org is a vault organization.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Collection belongs to an organization; nested via "/" like folders.
type Collection struct {
	ID    string   `json:"id"`
	OrgID string   `json:"orgId"`
	Name  string   `json:"name"`
	Path  []string `json:"path"`
}

// Snapshot is the full metadata set used by the UI and cached on disk.
type Snapshot struct {
	Account     string       `json:"account,omitempty"` // "email@server", to not mix caches
	SyncedAt    time.Time    `json:"syncedAt"`
	Hosts       []Host       `json:"hosts"`
	Folders     []Folder     `json:"folders"`
	Orgs        []Org        `json:"orgs"`
	Collections []Collection `json:"collections"`
	Credentials []Credential `json:"credentials,omitempty"`
}

// SplitPath splits a nested folder/collection name. Empty segments are dropped.
func SplitPath(name string) []string {
	var out []string
	for _, p := range strings.Split(name, "/") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Folder returns the folder by ID.
func (s *Snapshot) Folder(id string) *Folder {
	for i := range s.Folders {
		if s.Folders[i].ID == id {
			return &s.Folders[i]
		}
	}
	return nil
}

// Org returns the organization by ID.
func (s *Snapshot) Org(id string) *Org {
	for i := range s.Orgs {
		if s.Orgs[i].ID == id {
			return &s.Orgs[i]
		}
	}
	return nil
}

// Collection returns the collection by ID.
func (s *Snapshot) Collection(id string) *Collection {
	for i := range s.Collections {
		if s.Collections[i].ID == id {
			return &s.Collections[i]
		}
	}
	return nil
}

// FolderPath returns the folder path of a host, or nil.
func (s *Snapshot) FolderPath(h *Host) []string {
	if f := s.Folder(h.FolderID); f != nil {
		return f.Path
	}
	return nil
}

// Sort orders hosts by folder path then name, folders by path.
func (s *Snapshot) Sort() {
	sort.SliceStable(s.Folders, func(i, j int) bool {
		return strings.ToLower(strings.Join(s.Folders[i].Path, "/")) < strings.ToLower(strings.Join(s.Folders[j].Path, "/"))
	})
	sort.SliceStable(s.Collections, func(i, j int) bool {
		return strings.ToLower(strings.Join(s.Collections[i].Path, "/")) < strings.ToLower(strings.Join(s.Collections[j].Path, "/"))
	})
	sort.SliceStable(s.Hosts, func(i, j int) bool {
		return strings.ToLower(s.Hosts[i].Name) < strings.ToLower(s.Hosts[j].Name)
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
