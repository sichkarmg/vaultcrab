// SPDX-License-Identifier: MIT

// Package widget runs the commands of the host list side column
// periodically and keeps their latest output. No UI code.
//
// The widgets must not keep the process busy while idle: one timer for all
// of them, no runs while paused, no redraw requests for unchanged output.
package widget

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Spec is a widget: a command run every Interval, or a built-in widget.
type Spec struct {
	Title    string
	Command  []string
	Builtin  string // BuiltinClock: drawn by vaultcrab itself, no command
	Interval time.Duration
	Height   int // maximum lines shown (0 = all)
}

// BuiltinClock is the clock widget: the time (hours and minutes) and the
// date, updated when the minute changes.
const BuiltinClock = "clock"

// Builtins are the known built-in widgets.
var Builtins = []string{BuiltinClock}

// Output is the latest result of a widget.
type Output struct {
	Title string
	Lines []string // printable text with SGR color sequences only
	Err   error
	At    time.Time
}

// maxOutput bounds what is read from a widget command.
const maxOutput = 64 << 10

const (
	// minStep is the shortest interval of a widget and the shortest time
	// between two wake-ups of the scheduler.
	minStep = time.Second
	// defaultInterval is used for widgets without an interval.
	defaultInterval = 5 * time.Second
)

// Runner runs the widgets while active. One scheduler goroutine with one
// timer serves all widgets: it sleeps until the next widget is due, at most
// once a second, and not at all while paused.
type Runner struct {
	specs  []Spec
	notify func()
	exec   Exec

	mu     sync.Mutex
	out    []Output
	active bool
	wake   chan struct{}

	wakeups atomic.Int64 // timer wake-ups of the scheduler (for tests)
	runs    atomic.Int64 // widget updates (for tests)
}

// NewRunner creates a runner; notify is called after each change. run
// executes the commands (nil = LocalExec).
func NewRunner(specs []Spec, notify func(), run Exec) *Runner {
	if run == nil {
		run = LocalExec
	}
	r := &Runner{specs: specs, notify: notify, exec: run, out: make([]Output, len(specs)), wake: make(chan struct{}, 1)}
	for i, s := range specs {
		r.out[i].Title = s.Title
	}
	if r.notify == nil {
		r.notify = func() {}
	}
	return r
}

// Start runs the widgets until ctx ends.
func (r *Runner) Start(ctx context.Context) {
	if len(r.specs) > 0 {
		go r.schedule(ctx)
	}
}

// SetActive pauses (false) or resumes the widgets, e.g. while the host list
// is not visible or the terminal window is not focused. Resuming updates
// the widgets that became due while paused right away.
func (r *Runner) SetActive(on bool) {
	r.mu.Lock()
	was := r.active
	r.active = on
	r.mu.Unlock()
	if on && !was {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}

// Outputs returns the latest outputs in configuration order.
func (r *Runner) Outputs() []Output {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Output(nil), r.out...)
}

func (r *Runner) isActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// schedule starts the widgets that are due and sleeps until the next one
// is. A widget runs at most once at a time; it is next due one interval
// after it started (a built-in clock: when the minute changes).
func (r *Runner) schedule(ctx context.Context) {
	due := make([]time.Time, len(r.specs)) // zero: right away
	busy := make([]bool, len(r.specs))
	done := make(chan int)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var woke time.Time // last timer wake-up
	for {
		var next time.Time
		if r.isActive() {
			now := time.Now()
			for i := range r.specs {
				switch {
				case busy[i]:
				case !due[i].After(now):
					busy[i] = true
					go func(i int) {
						r.run(ctx, i)
						select {
						case done <- i:
						case <-ctx.Done():
						}
					}(i)
				case next.IsZero() || due[i].Before(next):
					next = due[i]
				}
			}
		}
		var fire <-chan time.Time
		if !next.IsZero() {
			if earliest := woke.Add(minStep); next.Before(earliest) {
				next = earliest
			}
			timer.Reset(time.Until(next))
			fire = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-fire:
			woke = time.Now()
			r.wakeups.Add(1)
		case <-r.wake:
		case i := <-done:
			busy[i] = false
			// a command slower than its interval does not run back to back
			due[i] = r.nextRun(i, r.outAt(i))
			if soon := time.Now().Add(minStep); due[i].Before(soon) {
				due[i] = soon
			}
		}
		timer.Stop()
	}
}

func (r *Runner) outAt(i int) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.out[i].At
}

// nextRun is when widget i is due after a run that started at started.
func (r *Runner) nextRun(i int, started time.Time) time.Time {
	s := r.specs[i]
	if s.Builtin == BuiltinClock {
		// wall clock (Round(0)): the minute changes by the wall clock
		return started.Round(0).Truncate(time.Minute).Add(time.Minute)
	}
	return started.Add(interval(s)).Round(0)
}

func interval(s Spec) time.Duration {
	switch {
	case s.Interval <= 0:
		return defaultInterval
	case s.Interval < minStep:
		return minStep
	}
	return s.Interval
}

// run updates widget i and notifies the UI if its output changed.
func (r *Runner) run(ctx context.Context, i int) {
	s := r.specs[i]
	started := time.Now()
	var lines []string
	var err error
	switch {
	case s.Builtin != "":
		lines, err = builtin(s.Builtin, started)
	default:
		lines, err = RunWith(ctx, r.exec, s.Command, min(interval(s), 10*time.Second))
	}
	if ctx.Err() != nil {
		return
	}
	if s.Height > 0 && len(lines) > s.Height {
		lines = lines[:s.Height]
	}
	r.runs.Add(1)
	r.mu.Lock()
	old := r.out[i]
	r.out[i] = Output{Title: s.Title, Lines: lines, Err: err, At: started}
	r.mu.Unlock()
	if old.At.IsZero() || !sameOutput(old, lines, err) {
		r.notify() // unchanged output needs no redraw
	}
}

// builtin renders a built-in widget.
func builtin(name string, now time.Time) ([]string, error) {
	switch name {
	case BuiltinClock:
		return []string{now.Format("15:04"), now.Format("Monday, 02 January 2006")}, nil
	}
	return nil, fmt.Errorf("unknown built-in widget %q", name)
}

// Exec runs argv with a timeout and returns its combined output; it returns
// ErrTimeout when the command did not finish in time.
type Exec func(ctx context.Context, argv []string, timeout time.Duration) ([]byte, error)

// ErrTimeout is returned by an Exec on timeout.
var ErrTimeout = errors.New("timed out")

// LocalExec runs the command as a child process of vaultcrab.
func LocalExec(ctx context.Context, argv []string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf limitedBuffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	hideWindow(cmd)
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = ErrTimeout
	}
	return buf.Bytes(), err
}

// Run executes argv with a timeout as a child process and returns its
// cleaned output lines.
func Run(ctx context.Context, argv []string, timeout time.Duration) ([]string, error) {
	return RunWith(ctx, LocalExec, argv, timeout)
}

// RunWith is Run with the given way to execute the command.
func RunWith(ctx context.Context, run Exec, argv []string, timeout time.Duration) ([]string, error) {
	if len(argv) == 0 {
		return nil, errors.New("no command for this OS")
	}
	out, err := run(ctx, argv, timeout)
	lines := Clean(out)
	switch {
	case errors.Is(err, ErrTimeout):
		err = fmt.Errorf("timed out after %s", timeout)
	case err != nil && len(lines) == 0:
		err = fmt.Errorf("%s: %w", argv[0], err)
	case err != nil:
		err = nil // the output says what went wrong
	}
	return lines, err
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// Clean splits output into lines, keeps SGR color sequences and drops every
// other control or escape sequence (cursor movement, titles, ...). Tabs
// become spaces, CR-overwritten text keeps the last part.
func Clean(b []byte) []string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	var out []string
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if i := strings.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:]
		}
		out = append(out, cleanLine(line))
	}
	if len(out) == 1 && out[0] == "" {
		return nil
	}
	return out
}

func cleanLine(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\t':
			b.WriteString("    ")
		case c == 0x1b:
			end := escapeEnd(s, i)
			if seq := s[i:end]; strings.HasPrefix(seq, "\x1b[") && strings.HasSuffix(seq, "m") {
				b.WriteString(seq) // SGR: colors and attributes
			}
			i = end - 1
		case c < 0x20 || c == 0x7f:
			// other control characters
		default:
			b.WriteByte(c)
		}
	}
	if b.Len() > 0 && strings.Contains(b.String(), "\x1b[") {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// escapeEnd returns the index after the escape sequence starting at s[i].
func escapeEnd(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[': // CSI: parameters, then a final byte 0x40-0x7e
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
		return len(s)
	case ']': // OSC: until BEL or ST
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
		return len(s)
	}
	return min(i+2, len(s))
}

// sameOutput reports whether a new result shows exactly what o shows: then
// the UI is not asked to redraw (each redraw rewrites the changed lines of
// the terminal, which some terminals show as flicker).
func sameOutput(o Output, lines []string, err error) bool {
	if len(o.Lines) != len(lines) || (o.Err == nil) != (err == nil) || o.Err != nil && o.Err.Error() != err.Error() {
		return false
	}
	for i := range lines {
		if o.Lines[i] != lines[i] {
			return false
		}
	}
	return true
}
