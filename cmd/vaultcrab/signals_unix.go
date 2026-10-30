// SPDX-License-Identifier: MIT

//go:build !windows

package main

import (
	"os"
	"syscall"
)

var shutdownSignals = []os.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT}
