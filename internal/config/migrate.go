// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// legacyDirName is the config directory of the project's former name
// (sshmgr). Only the first-run migration below knows about it.
const legacyDirName = "sshmgr"

// LegacyDir returns the former config directory (<user config dir>/sshmgr)
// when it exists and the current one (dir) does not: the case where the
// user is offered to copy it. ok is false otherwise.
func LegacyDir(dir string) (old string, ok bool) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", false
	}
	old = filepath.Join(base, legacyDirName)
	if fi, err := os.Stat(old); err != nil || !fi.IsDir() {
		return "", false
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		return "", false
	}
	return old, true
}

// CopyDir copies the tree src to dst (which must not exist), keeping file
// modes and symlinks. src is left untouched. On error the partial copy is
// removed so the next start offers the migration again.
func CopyDir(src, dst string) (err error) {
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		return fmt.Errorf("%s already exists", dst)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dst)
		}
	}()
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm())
		default: // sockets, pipes (a running bw may leave them): not config
			return nil
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
