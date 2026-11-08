// SPDX-License-Identifier: MIT

//go:build !linux && !windows && !(darwin && cgo)

package keystore

func open() (Store, error) { return nil, ErrUnsupported }
