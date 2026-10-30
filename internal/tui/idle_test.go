// SPDX-License-Identifier: MIT

//go:build !windows

package tui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/core"
)

// idleCounter counts what the program does: messages handled, frames built.
type idleCounter struct {
	m              *Model
	updates, views atomic.Int64
}

func (c *idleCounter) Init() tea.Cmd { return c.m.Init() }
func (c *idleCounter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	c.updates.Add(1)
	_, cmd := c.m.Update(msg)
	return c, cmd
}
func (c *idleCounter) View() string { c.views.Add(1); return c.m.View() }

// frameWriter counts the writes to the terminal.
type frameWriter struct {
	mu     sync.Mutex
	writes int
}

func (w *frameWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	w.mu.Unlock()
	return len(p), nil
}

func (w *frameWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes
}

// An idle vaultcrab with the widget column and two session tabs without output
// does (almost) nothing: no periodic ticks or redraws, no widget commands
// beyond their interval. Before the fix 10 s of idling took 10 messages and
// views (a 1 s tick) and 1912 widget commands with this instant fake command
// (the widgets passed a wake-up signal to each other without end; real
// commands ran ~4 times a second), and the renderer woke up 60 times a
// second (not counted here).
func TestIdleWakeups(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 12 s")
	}
	const idle = 10 * time.Second
	var execs atomic.Int64
	// the config file is watched (a stat every 5 s): no wake-up of the UI
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(cfgPath, []byte("ui: {accent: cyan}\n"), 0o600)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := core.New(core.Options{
		Config:  &cfg,
		Connect: func(context.Context) (core.Source, func(), error) { return &fakeSrc{}, nil, nil },
		Local:   core.LocalOptions{Command: []string{"sh", "-c", "sleep 600"}}, // no output
		Widgets: []core.WidgetSpec{ // as the defaults
			{Title: "Clock", Builtin: core.WidgetClock},
			{Title: "System", Command: []string{"system-info"}, Interval: 30 * time.Second},
		},
		WidgetExec: func(context.Context, []string, time.Duration) ([]byte, error) {
			execs.Add(1)
			return []byte("host   test\n"), nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(svc.Shutdown)
	svc.Start(ctx)
	m := New(ctx, svc, Options{Prefix: "ctrl+b", Keys: map[string]string{core.ActLocal: "t", core.ActList: "l"},
		Icons: testIcons(false), ShowTree: true, Widgets: true, WidgetsWidth: 30, WidgetsMin: 130})
	c := &idleCounter{m: m}
	out := &frameWriter{}
	p := tea.NewProgram(c, tea.WithInput(nil), tea.WithOutput(out), tea.WithoutSignalHandler())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := p.Run(); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() { p.Quit(); <-done })
	p.Send(tea.WindowSizeMsg{Width: 160, Height: 40})
	for _, k := range []string{"t", "ctrl+b", "l", "t", "ctrl+b", "l"} { // two tabs, back to the list
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		if k == "ctrl+b" {
			msg = tea.KeyMsg{Type: tea.KeyCtrlB}
		}
		p.Send(msg)
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(time.Second) // the shells start, the widgets show
	if len(m.tabs) != 2 || m.active != 0 {
		t.Fatalf("tabs=%d active=%d", len(m.tabs), m.active)
	}

	u0, v0, w0, e0 := c.updates.Load(), c.views.Load(), out.count(), execs.Load()
	time.Sleep(idle)
	u, v, w, e := c.updates.Load()-u0, c.views.Load()-v0, out.count()-w0, execs.Load()-e0
	t.Logf("in %s idle: %d messages, %d views, %d terminal writes, %d widget commands", idle, u, v, w, e)
	// the clock may change its minute once; the vault health check may run
	if u > 4 || v > 4 || w > 4 {
		t.Errorf("idle redraws: %d messages, %d views, %d writes in %s (want <= 4)", u, v, w, idle)
	}
	if st := svc.ConfigStatus(); st.Seq != 0 {
		t.Errorf("an unchanged config re-read %d times", st.Seq)
	}
	if e > 1 {
		t.Errorf("idle widget commands: %d in %s (want <= 1)", e, idle)
	}
}

// A message schedules one redraw when it expires; nothing else does.
func TestWakeSchedule(t *testing.T) {
	m, _ := newTestModel(t, false)
	if !m.wakeAt.IsZero() {
		t.Fatalf("redraw scheduled while idle: %v", m.wakeAt)
	}
	m.notify(false, "hello")
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if d := time.Until(m.wakeAt); d < 4*time.Second || d > 5*time.Second {
		t.Errorf("redraw in %v, want when the message expires (5 s)", d)
	}
	m.toastUntil = time.Now().Add(-time.Second)
	m.Update(tickMsg{})
	if !m.wakeAt.IsZero() {
		t.Errorf("redraw scheduled after the message expired: %v", m.wakeAt)
	}
	// focus reports
	m.Update(tea.BlurMsg{})
	if !m.blurred {
		t.Error("blur not recorded")
	}
	m.Update(tea.FocusMsg{})
	if m.blurred {
		t.Error("focus not recorded")
	}
}
