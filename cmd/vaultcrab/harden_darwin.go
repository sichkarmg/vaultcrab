// SPDX-License-Identifier: MIT

package main

import "golang.org/x/sys/unix"

// harden keeps secrets in memory out of core dumps.
func harden() { _ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}) }
