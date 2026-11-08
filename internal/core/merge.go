// SPDX-License-Identifier: MIT

package core

import (
	"slices"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// remerge builds the snapshot of all vaults (with s.mu held). The default
// vault is taken as it is. The items, folders, organizations and
// collections of a further vault get IDs "<vault id>/<id>" (route finds the
// vault again), and its folders are put under a folder named after the
// vault, which also holds its items without a folder: the folder tree shows
// each further vault as a branch.
func (s *Service) remerge() {
	if len(s.vaults) == 1 {
		s.snap = s.vaults[0].snap
		return
	}
	d := s.vaults[0].snap
	out := model.Snapshot{Account: d.Account, SyncedAt: d.SyncedAt,
		Hosts: slices.Clone(d.Hosts), Folders: slices.Clone(d.Folders),
		Orgs: slices.Clone(d.Orgs), Collections: slices.Clone(d.Collections), Credentials: slices.Clone(d.Credentials)}
	for _, v := range s.vaults[1:] {
		p := v.opts.ID + "/"
		name := v.info().Label()
		root := p // the folder of the vault itself
		out.Folders = append(out.Folders, model.Folder{ID: root, Name: name, Path: []string{name}})
		for _, f := range v.snap.Folders {
			path := append([]string{name}, f.Path...)
			out.Folders = append(out.Folders, model.Folder{ID: p + f.ID, Name: name + "/" + f.Name, Path: path})
		}
		for _, o := range v.snap.Orgs {
			out.Orgs = append(out.Orgs, model.Org{ID: p + o.ID, Name: o.Name})
		}
		for _, c := range v.snap.Collections {
			c.ID, c.OrgID = p+c.ID, p+c.OrgID
			out.Collections = append(out.Collections, c)
		}
		for _, h := range v.snap.Hosts {
			h.ID = p + h.ID
			if h.FolderID == "" {
				h.FolderID = root
			} else {
				h.FolderID = p + h.FolderID
			}
			if h.OrgID != "" {
				h.OrgID = p + h.OrgID
			}
			if len(h.CollectionIDs) > 0 {
				ids := make([]string, len(h.CollectionIDs))
				for i, c := range h.CollectionIDs {
					ids[i] = p + c
				}
				h.CollectionIDs = ids
			}
			out.Hosts = append(out.Hosts, h)
		}
		for _, c := range v.snap.Credentials {
			c.ID = p + c.ID
			out.Credentials = append(out.Credentials, c)
		}
		if v.snap.SyncedAt.After(out.SyncedAt) {
			out.SyncedAt = v.snap.SyncedAt
		}
	}
	s.snap = out
}
