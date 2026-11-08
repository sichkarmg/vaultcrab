// SPDX-License-Identifier: MIT

//go:build !linux && !darwin

package main

// harden: Windows writes no crash dumps of a process unless configured
// (Windows Error Reporting LocalDumps).
func harden() {}
