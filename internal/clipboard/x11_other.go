// SPDX-License-Identifier: MIT

//go:build !(linux || freebsd || openbsd || netbsd)

package clipboard

import "errors"

var errNoX11 = errors.New("no X11 clipboard on this OS")

func writeX11(string) error    { return errNoX11 }
func readX11() (string, error) { return "", errNoX11 }
func x11Available() bool       { return false }
