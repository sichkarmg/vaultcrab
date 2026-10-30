// SPDX-License-Identifier: MIT

//go:build !windows

package config

import "os"

// readFile reads the config (a rename over it never fails a read here).
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
