// SPDX-License-Identifier: MIT

//go:build !windows

package spawn

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if Main() { // the test binary doubles as the helper
		return
	}
	os.Exit(m.Run())
}

func newClient(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// ppidOf reads the parent PID with ps (works on Linux and macOS).
func ppidOf(t *testing.T, c *Client, pid string) int {
	t.Helper()
	out, err := c.Run(context.Background(), []string{"ps", "-o", "ppid=", "-p", pid}, 5*time.Second)
	if err != nil {
		t.Fatalf("ps: %v %s", err, out)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func TestRunOutsideOurProcessTree(t *testing.T) {
	c := newClient(t)
	out, err := c.Run(context.Background(), []string{"sh", "-c", "echo hello; echo $PPID"}, 5*time.Second)
	if err != nil {
		t.Fatal(err, string(out))
	}
	lines := strings.Fields(string(out))
	if len(lines) != 2 || lines[0] != "hello" {
		t.Fatalf("output %q", out)
	}
	// the command's parent is the helper, whose parent is not us
	helper := lines[1]
	if helper == strconv.Itoa(os.Getpid()) {
		t.Fatal("command is our child")
	}
	if pp := ppidOf(t, c, helper); pp == os.Getpid() || pp == 0 {
		t.Errorf("helper %s has parent %d (we are %d)", helper, pp, os.Getpid())
	}
}

func TestRunErrorsAndTimeout(t *testing.T) {
	c := newClient(t)
	if _, err := c.Run(context.Background(), []string{"no-such-cmd-xyz"}, time.Second); err == nil {
		t.Error("missing command: no error")
	}
	out, err := c.Run(context.Background(), []string{"sh", "-c", "echo partial; exit 3"}, time.Second)
	if err == nil || string(out) != "partial\n" {
		t.Errorf("exit 3: %q %v", out, err)
	}
	if _, err := c.Run(context.Background(), []string{"sleep", "5"}, 200*time.Millisecond); err != ErrTimeout {
		t.Errorf("timeout: %v", err)
	}
}

func TestStartKillAndExit(t *testing.T) {
	c := newClient(t)
	p, err := c.Start([]string{"sh", "-c", "echo oops >&2; echo $X; sleep 30"}, []string{"X=from-env"})
	if err != nil {
		t.Fatal(err)
	}
	if pp := ppidOf(t, c, strconv.Itoa(p.Pid)); pp == os.Getpid() {
		t.Error("started process is our child")
	}
	p.Kill()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("not killed")
	}
	if !strings.Contains(string(p.Stderr()), "oops") {
		t.Errorf("stderr %q", p.Stderr())
	}
	q, err := c.Start([]string{"sh", "-c", "exit 0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-q.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("quick exit lost")
	}
	if _, err := c.Start([]string{"no-such-cmd-xyz"}, nil); err == nil {
		t.Error("missing command started")
	}
}

// When vaultcrab goes away (the pipe closes), the helper kills what it started.
func TestCloseKillsStarted(t *testing.T) {
	c := newClient(t)
	p, err := c.Start([]string{"sleep", "30"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(p.Pid, 0) == nil {
		if time.Now().After(deadline) {
			syscall.Kill(p.Pid, syscall.SIGKILL)
			t.Fatal("process survived the helper")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
