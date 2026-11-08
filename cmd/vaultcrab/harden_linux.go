// SPDX-License-Identifier: MIT

package main

import "golang.org/x/sys/unix"

// harden keeps secrets in memory out of core dumps and away from other
// processes of the user: no core file, not dumpable (also no ptrace attach
// and no /proc/<pid>/mem for them). Programs vaultcrab starts are not
// affected (exec resets it).
func harden() {
	_ = unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{})
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
}
