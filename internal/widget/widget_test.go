// SPDX-License-Identifier: MIT

package widget

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClean(t *testing.T) {
	got := Clean([]byte("a\tb\r\n\x1b[31mred\x1b[0m \x1b[2J\x1b[Hx\x1b]0;title\a\n10%\r50%\r100%\n\x07bell\n"))
	want := []string{"a    b", "\x1b[31mred\x1b[0m x\x1b[0m", "100%", "bell"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	if Clean(nil) != nil {
		t.Error("empty output")
	}
}

func TestRunnerUpdatesWhileActive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	notified := make(chan struct{}, 10)
	r := NewRunner([]Spec{
		{Title: "echo", Command: []string{"sh", "-c", "echo one; echo two; echo three"}, Interval: 50 * time.Millisecond, Height: 2},
		{Title: "missing", Command: []string{"no-such-command-xyz"}, Interval: time.Second},
		{Title: "slow", Command: []string{"sleep", "5"}, Interval: 100 * time.Millisecond},
	}, func() { notified <- struct{}{} }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	select {
	case <-notified:
		t.Fatal("ran while inactive")
	case <-time.After(150 * time.Millisecond):
	}
	r.SetActive(true)
	deadline := time.After(3 * time.Second)
	for {
		out := r.Outputs()
		if len(out[0].Lines) > 0 && out[1].Err != nil && out[2].Err != nil {
			if strings.Join(out[0].Lines, ",") != "one,two" {
				t.Errorf("height limit: %q", out[0].Lines)
			}
			if !strings.Contains(out[2].Err.Error(), "timed out") {
				t.Errorf("slow: %v", out[2].Err)
			}
			return
		}
		select {
		case <-notified:
		case <-deadline:
			t.Fatalf("outputs %+v", out)
		}
	}
}

// Unchanged output does not ask the UI to redraw (a redraw rewrites the
// changed terminal lines, which some terminals show as flicker).
func TestNoNotifyWhenUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	n := 0
	var mu sync.Mutex
	r := NewRunner([]Spec{{Title: "same", Command: []string{"sh", "-c", "echo 12:00"}, Interval: 30 * time.Millisecond}},
		func() { mu.Lock(); n++; mu.Unlock() }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	r.SetActive(true)
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Errorf("notified %d times for unchanged output", n)
	}
}

// Widgets do not wake each other up: each runs once per interval, and the
// scheduler wakes up at most once a second (it used to pass the wake-up
// signal of SetActive between the widgets without end).
func TestNoWakeLoop(t *testing.T) {
	var runs atomic.Int64
	run := func(context.Context, []string, time.Duration) ([]byte, error) {
		runs.Add(1)
		return []byte("x"), nil
	}
	r := NewRunner([]Spec{
		{Title: "a", Command: []string{"a"}, Interval: 10 * time.Second},
		{Title: "b", Command: []string{"b"}, Interval: 10 * time.Second},
		{Title: "c", Command: []string{"c"}, Interval: 100 * time.Millisecond}, // raised to 1 s
		{Title: "clock", Builtin: BuiltinClock},
	}, nil, run)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Start(ctx)
	r.SetActive(true)
	time.Sleep(2500 * time.Millisecond)
	// a, b once; c at 0, 1, 2 s
	if n := runs.Load(); n < 4 || n > 5 {
		t.Errorf("%d commands in 2.5 s, want 4-5", n)
	}
	if w := r.wakeups.Load(); w > 3 {
		t.Errorf("%d scheduler wake-ups in 2.5 s", w)
	}
	// paused: nothing runs; resumed: only what became due
	r.SetActive(false)
	time.Sleep(1500 * time.Millisecond)
	n := runs.Load()
	r.SetActive(true)
	time.Sleep(200 * time.Millisecond)
	if d := runs.Load() - n; d != 1 { // c only
		t.Errorf("resume ran %d commands, want 1", d)
	}
	if out := r.Outputs()[3]; len(out.Lines) != 2 || out.Lines[0] != out.At.Format("15:04") {
		t.Errorf("clock: %q", out.Lines)
	}
}
