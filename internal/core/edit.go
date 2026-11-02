// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/vault"
)

// Editor is implemented by sources that can change the vault (vault.Client).
type Editor interface {
	CreateHost(ctx context.Context, e vault.HostEdit) (string, error)
	UpdateHost(ctx context.Context, id string, e vault.HostEdit) error
	CreateFolder(ctx context.Context, name string) (string, error)
	DeleteItem(ctx context.Context, id string) error // moves it to the trash
}

// ErrReadOnly is returned when the vault cannot be changed right now.
var ErrReadOnly = errors.New("adding, editing and deleting need the vault online (unlocked, server reachable)")

// CanEdit reports why items cannot be added or edited right now (nil = they
// can, in at least one vault).
func (s *Service) CanEdit() error {
	for _, v := range s.vaultList() {
		if v.editable() {
			if _, ok := v.source().(Editor); ok {
				return nil
			}
		}
	}
	return ErrReadOnly
}

// editor returns the editor of vault v if it can be changed now.
func (v *vaultConn) editor() (Editor, error) {
	if !v.editable() {
		return nil, ErrReadOnly
	}
	ed, ok := v.source().(Editor)
	if !ok {
		return nil, ErrReadOnly
	}
	return ed, nil
}

// SaveHost creates (id == "") or updates a host item. folderPath ("A/B",
// "" = no folder) is resolved to an existing folder or created. With
// several vaults the path of an item of a further vault starts with the
// name of that vault (as in the folder tree); a new item whose path starts
// with the name of a further vault is created there. The vault is refreshed
// afterwards; the new or updated item ID is returned.
func (s *Service) SaveHost(ctx context.Context, id string, e HostEdit, folderPath string) (string, error) {
	folderPath = strings.Join(model.SplitPath(folderPath), "/")
	v, rawID := s.route(id)
	if id == "" {
		v = s.vaultOfPath(folderPath)
	}
	if e.CopyOf != "" {
		cv, raw := s.route(e.CopyOf)
		if cv != v {
			return "", errors.New("an item cannot be copied into another vault")
		}
		e.CopyOf = raw
	}
	folderPath, ok := v.localPath(folderPath)
	if !ok {
		return "", errors.New("an item cannot be moved to another vault: keep the vault name at the start of the folder")
	}
	var err error
	if e.OrgID, err = v.localID(e.OrgID); err != nil {
		return "", err
	}
	for i, c := range e.CollectionIDs {
		if e.CollectionIDs[i], err = v.localID(c); err != nil {
			return "", err
		}
	}
	ed, err := v.editor()
	if err != nil {
		return "", err
	}
	e.FolderID = ""
	if folderPath != "" {
		s.mu.Lock()
		folders := v.snap.Folders
		s.mu.Unlock()
		for _, f := range folders {
			if strings.EqualFold(strings.Join(f.Path, "/"), folderPath) {
				e.FolderID = f.ID
				break
			}
		}
		if e.FolderID == "" {
			fid, err := ed.CreateFolder(ctx, folderPath)
			if err != nil {
				return "", err
			}
			e.FolderID = fid
		}
	}
	if id == "" {
		rawID, err = ed.CreateHost(ctx, e)
	} else {
		err = ed.UpdateHost(ctx, rawID, e)
	}
	if err != nil {
		return "", err
	}
	v.refresh(ctx)
	if v.opts.ID != "" {
		return v.opts.ID + "/" + rawID, nil
	}
	return rawID, nil
}

// vaultOfPath returns the further vault whose name starts the folder path,
// or the default vault.
func (s *Service) vaultOfPath(path string) *vaultConn {
	vs := s.vaultList()
	first, _, _ := strings.Cut(path, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range vs[1:] {
		if strings.EqualFold(first, v.info().Label()) {
			return v
		}
	}
	return vs[0]
}

// localPath turns a folder path of the merged tree into one of vault v;
// ok is false if the path is in another vault.
func (v *vaultConn) localPath(path string) (string, bool) {
	if v.opts.ID == "" {
		return path, v.s.vaultOfPath(path) == v
	}
	v.s.mu.Lock()
	name := v.info().Label()
	v.s.mu.Unlock()
	first, rest, _ := strings.Cut(path, "/")
	if !strings.EqualFold(first, name) {
		return "", false
	}
	return rest, true
}

// localID turns an ID of the merged snapshot (org, collection) into one of
// vault v.
func (v *vaultConn) localID(id string) (string, error) {
	if id == "" {
		return "", nil
	}
	w, raw := v.s.route(id)
	if w != v {
		return "", fmt.Errorf("the organization or collection belongs to another vault")
	}
	return raw, nil
}

// DeleteHost moves a host item to the vault trash (it can be restored from
// there in Bitwarden) and refreshes the vault.
func (s *Service) DeleteHost(ctx context.Context, id string) error {
	v, raw := s.route(id)
	ed, err := v.editor()
	if err != nil {
		return err
	}
	if err := ed.DeleteItem(ctx, raw); err != nil {
		return err
	}
	v.refresh(ctx)
	return nil
}

// NotesReader is implemented by sources that can read item notes
// (vault.Client).
type NotesReader interface {
	Notes(ctx context.Context, id string) (string, error)
}

// CanReadNotes reports whether notes can be read now (from at least one
// vault).
func (s *Service) CanReadNotes() bool {
	for _, v := range s.vaultList() {
		if _, ok := v.source().(NotesReader); ok && v.online() {
			return true
		}
	}
	return false
}

func (v *vaultConn) online() bool {
	v.s.mu.Lock()
	defer v.s.mu.Unlock()
	return v.state == Online
}

// Notes returns the notes of a host item, read from its vault on request:
// notes may hold secrets, so they are neither in the snapshot nor cached.
func (s *Service) Notes(ctx context.Context, id string) (string, error) {
	v, raw := s.route(id)
	nr, ok := v.source().(NotesReader)
	if !ok || !v.online() {
		return "", ErrNotOnline
	}
	n, err := nr.Notes(ctx, raw)
	if errors.Is(err, vault.ErrLocked) || errors.Is(err, vault.ErrUnavailable) {
		v.fail(err)
		return "", ErrNotOnline
	}
	return n, err
}
