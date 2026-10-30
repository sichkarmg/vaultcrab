// SPDX-License-Identifier: MIT

package vault

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// Bitwarden wire types (subset). Secret-bearing fields (login.password,
// hidden custom fields, notes, totp) are deliberately absent so that listing
// items never materializes secrets in our structs.
type bwItem struct {
	ID             string    `json:"id"`
	OrganizationID *string   `json:"organizationId"`
	FolderID       *string   `json:"folderId"`
	Type           int       `json:"type"`
	Name           string    `json:"name"`
	Favorite       bool      `json:"favorite"`
	Fields         []bwField `json:"fields"`
	Login          *bwLogin  `json:"login"`
	CollectionIDs  []string  `json:"collectionIds"`
	DeletedDate    *string   `json:"deletedDate"`
	CreationDate   string    `json:"creationDate"`
	RevisionDate   string    `json:"revisionDate"`
}

type bwLogin struct {
	Username *string `json:"username"`
	URIs     []bwURI `json:"uris"`
}

type bwURI struct {
	URI string `json:"uri"`
}

// Custom field types.
const (
	fieldText    = 0
	fieldHidden  = 1
	fieldBoolean = 2
	fieldLinked  = 3
)

type bwField struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
	Type  int     `json:"type"`
}

type bwFolder struct {
	ID   *string `json:"id"`
	Name string  `json:"name"`
}

type bwOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type bwCollection struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	Name           string `json:"name"`
}

const itemTypeLogin = 1

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on", "да":
		return true
	}
	return false
}

// toHost converts a vault item to a Host. ok is false when the item is not a
// connectable host.
//
// Recognized formats:
//   - boolean (or text "true") custom field named after a scheme (ssh, vnc,
//     smb, ftp, sftp, rdp) plus username "user@host[:port]";
//   - URI with such a scheme, e.g. ssh://host, rdp://host:3389 (username
//     field is the login).
//
// A "port" text field overrides the port; a "tags" field (comma/space
// separated) gives tags.
func toHost(it *bwItem) (model.Host, bool) {
	if it.Type != itemTypeLogin || it.Login == nil || it.DeletedDate != nil {
		return model.Host{}, false
	}
	h := model.Host{
		ID:            it.ID,
		Name:          it.Name,
		FolderID:      str(it.FolderID),
		OrgID:         str(it.OrganizationID),
		CollectionIDs: append([]string(nil), it.CollectionIDs...),
		Favorite:      it.Favorite,
	}
	h.Created, _ = time.Parse(time.RFC3339, it.CreationDate)
	h.Revised, _ = time.Parse(time.RFC3339, it.RevisionDate)
	var flagScheme model.Scheme
	for _, f := range it.Fields {
		if f.Type == fieldHidden || f.Type == fieldLinked || f.Name == "" {
			continue // hidden fields may hold secrets
		}
		v := str(f.Value)
		name := strings.ToLower(strings.TrimSpace(f.Name))
		if h.Fields == nil {
			h.Fields = map[string]string{}
		}
		h.Fields[name] = v
		switch name {
		case "tags", "tag":
			h.Tags = append(h.Tags, splitTags(v)...)
		default:
			if sc, ok := model.ParseScheme(name); ok && flagScheme == "" && truthy(v) {
				flagScheme = sc
			}
		}
	}
	username := strings.TrimSpace(str(it.Login.Username))

	// 1. URI with a known scheme.
	for _, u := range it.Login.URIs {
		pu, err := url.Parse(strings.TrimSpace(u.URI))
		if err != nil || pu.Host == "" {
			continue
		}
		sc, ok := model.ParseScheme(pu.Scheme)
		if !ok {
			continue
		}
		if flagScheme != "" && sc != flagScheme {
			continue
		}
		h.Scheme = sc
		h.Addr = pu.Hostname()
		if p, err := strconv.Atoi(pu.Port()); err == nil {
			h.Port = p
		}
		h.User = username
		if pu.User != nil && pu.User.Username() != "" {
			h.User = pu.User.Username()
		}
		h.Path = strings.Trim(pu.Path, "/")
		break
	}
	// 2. Flag field + user@host[:port] in username.
	if h.Scheme == "" && flagScheme != "" {
		at := strings.LastIndex(username, "@")
		if at <= 0 || at == len(username)-1 {
			return model.Host{}, false
		}
		h.Scheme = flagScheme
		h.User = username[:at]
		h.Addr, h.Port = splitHostPort(username[at+1:])
	}
	if h.Scheme == "" || h.Addr == "" {
		return model.Host{}, false
	}
	if p, err := strconv.Atoi(strings.TrimSpace(h.Fields["port"])); err == nil && p > 0 && p < 65536 {
		h.Port = p
	}
	if h.Port == 0 {
		h.Port = h.Scheme.DefaultPort()
	}
	return h, true
}

func splitHostPort(s string) (string, int) {
	if host, port, err := net.SplitHostPort(s); err == nil {
		if p, err := strconv.Atoi(port); err == nil {
			return host, p
		}
		return host, 0
	}
	return strings.Trim(s, "[]"), 0
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func buildSnapshot(items []bwItem, folders []bwFolder, orgs []bwOrg, cols []bwCollection) model.Snapshot {
	var s model.Snapshot
	for _, f := range folders {
		if f.ID == nil || *f.ID == "" {
			continue // "No Folder"
		}
		s.Folders = append(s.Folders, model.Folder{ID: *f.ID, Name: f.Name, Path: model.SplitPath(f.Name)})
	}
	for _, o := range orgs {
		s.Orgs = append(s.Orgs, model.Org{ID: o.ID, Name: o.Name})
	}
	for _, c := range cols {
		s.Collections = append(s.Collections, model.Collection{ID: c.ID, OrgID: c.OrganizationID, Name: c.Name, Path: model.SplitPath(c.Name)})
	}
	for i := range items {
		if h, ok := toHost(&items[i]); ok {
			s.Hosts = append(s.Hosts, h)
		}
	}
	s.Sort()
	return s
}
