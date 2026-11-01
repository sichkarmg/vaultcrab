// SPDX-License-Identifier: MIT

//go:build !windows

package config

import "os"

// replaceFile renames oldpath over newpath; readers that have newpath open
// keep the old contents.
func replaceFile(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
