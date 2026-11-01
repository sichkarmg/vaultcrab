// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// replaceFile renames oldpath over newpath with POSIX semantics (Windows 10
// 1607+, NTFS): the replaced file is unlinked at once even while readers
// still have it open (readFile opens it with FILE_SHARE_DELETE), so a save
// does not fail with "Access is denied" while another vaultcrab reads the
// config. Where that is not supported (FAT, some network shares), it falls
// back to os.Rename (MoveFileEx).
func replaceFile(oldpath, newpath string) error {
	err := renamePOSIX(oldpath, newpath)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return os.Rename(oldpath, newpath)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
	return nil
}

func renamePOSIX(oldpath, newpath string) error {
	from, err := windows.UTF16PtrFromString(oldpath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16FromString(newpath)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(from, windows.DELETE|windows.SYNCHRONIZE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	// FILE_RENAME_INFO with Flags in place of ReplaceIfExists (FileRenameInfoEx)
	type renameInfo struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
		FileName       [1]uint16
	}
	nameBytes := (len(to) - 1) * 2 // without the terminating NUL
	size := int(unsafe.Offsetof(renameInfo{}.FileName)) + len(to)*2
	buf := make([]byte, size)
	info := (*renameInfo)(unsafe.Pointer(&buf[0]))
	info.Flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.FileNameLength = uint32(nameBytes)
	copy(unsafe.Slice(&info.FileName[0], len(to)), to)
	return windows.SetFileInformationByHandle(h, windows.FileRenameInfoEx, &buf[0], uint32(size))
}
