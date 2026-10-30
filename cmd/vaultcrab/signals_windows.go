// SPDX-License-Identifier: MIT

//go:build windows

package main

import "os"

var shutdownSignals = []os.Signal{os.Interrupt}
