// SPDX-License-Identifier: MIT

// Package applog is the optional event log of vaultcrab (log.enabled in the
// config, Settings → Behavior): vault states, connections, external
// programs, config reloads and errors, one timestamped line each, in a file
// the UI can show. It never receives secrets, typed keys or terminal
// output: callers log names, states and error texts only.
package applog

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxSize is the size at which the log is rotated to <file>.1 (the
// previous .1 is replaced).
const MaxSize = 1 << 20

var (
	mu   sync.Mutex
	file *os.File
	path string
	size int64
)

// Open starts logging to p (appended, created 0600 with its directory).
// An open log is closed first.
func Open(p string) error {
	mu.Lock()
	defer mu.Unlock()
	closeLocked()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	file, path, size = f, p, st.Size()
	return nil
}

// Close stops logging.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	closeLocked()
}

func closeLocked() {
	if file != nil {
		file.Close()
	}
	file, path, size = nil, "", 0
}

// Enabled reports whether the log is open.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return file != nil
}

// Path returns the file of the open log ("" when off).
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// Printf writes one line (a no-op while the log is off). Line breaks in the
// message are flattened so every event stays one line.
func Printf(format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	msg := strings.NewReplacer("\r\n", " ⏎ ", "\n", " ⏎ ", "\r", " ").Replace(fmt.Sprintf(format, args...))
	line := time.Now().Format("2006-01-02 15:04:05.000 ") + msg + "\n"
	if size+int64(len(line)) > MaxSize {
		rotateLocked()
		if file == nil {
			return
		}
	}
	n, _ := io.WriteString(file, line)
	size += int64(n)
}

// rotateLocked moves the full log to <file>.1 and starts a new one.
func rotateLocked() {
	p := path
	file.Close()
	file = nil
	_ = os.Remove(p + ".1")
	_ = os.Rename(p, p+".1")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		path, size = "", 0
		return
	}
	file, size = f, 0
}

// Tail returns the last n lines of the log file p (the rotated part
// included when the current one is shorter).
func Tail(p string, n int) ([]string, error) {
	cur, err := readLines(p)
	if err != nil {
		return nil, err
	}
	if len(cur) < n {
		if old, err := readLines(p + ".1"); err == nil {
			cur = append(old, cur...)
		}
	}
	if len(cur) > n {
		cur = cur[len(cur)-n:]
	}
	return cur, nil
}

func readLines(p string) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out, sc.Err()
}
