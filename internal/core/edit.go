// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
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

// CanEdit reports why items cannot be added or edited right now (nil = they can).
func (s *Service) CanEdit() error {
	st := s.Status()
	if st.State != Online || st.SyncErr != nil || st.FromCache {
		return ErrReadOnly
	}
	if _, ok := s.source().(Editor); !ok {
		return ErrReadOnly
	}
	return nil
}

// SaveHost creates (id == "") or updates a host item. folderPath ("A/B",
// "" = no folder) is resolved to an existing folder or created. The vault is
// refreshed afterwards; the new or updated item ID is returned.
func (s *Service) SaveHost(ctx context.Context, id string, e HostEdit, folderPath string) (string, error) {
	if err := s.CanEdit(); err != nil {
		return "", err
	}
	ed := s.source().(Editor)
	folderPath = strings.Join(model.SplitPath(folderPath), "/")
	e.FolderID = ""
	if folderPath != "" {
		snap := s.Snapshot()
		for _, f := range snap.Folders {
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
	var err error
	if id == "" {
		id, err = ed.CreateHost(ctx, e)
	} else {
		err = ed.UpdateHost(ctx, id, e)
	}
	if err != nil {
		return "", err
	}
	s.Refresh(ctx)
	return id, nil
}

// DeleteHost moves a host item to the vault trash (it can be restored from
// there in Bitwarden) and refreshes the vault.
func (s *Service) DeleteHost(ctx context.Context, id string) error {
	if err := s.CanEdit(); err != nil {
		return err
	}
	if err := s.source().(Editor).DeleteItem(ctx, id); err != nil {
		return err
	}
	s.Refresh(ctx)
	return nil
}

// NotesReader is implemented by sources that can read item notes
// (vault.Client).
type NotesReader interface {
	Notes(ctx context.Context, id string) (string, error)
}

// CanReadNotes reports whether notes can be read now.
func (s *Service) CanReadNotes() bool {
	_, ok := s.source().(NotesReader)
	return ok && s.Status().State == Online
}

// Notes returns the notes of a host item, read from the vault on request:
// notes may hold secrets, so they are neither in the snapshot nor cached.
func (s *Service) Notes(ctx context.Context, id string) (string, error) {
	nr, ok := s.source().(NotesReader)
	if !ok || s.Status().State != Online {
		return "", ErrNotOnline
	}
	n, err := nr.Notes(ctx, id)
	if errors.Is(err, vault.ErrLocked) || errors.Is(err, vault.ErrUnavailable) {
		s.fail(err)
		return "", ErrNotOnline
	}
	return n, err
}
