// SPDX-License-Identifier: MIT

//go:build !windows

package connect

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func readAll(t *testing.T, c *LocalConn) string {
	t.Helper()
	var b strings.Builder
	buf := make([]byte, 1024)
	deadline := time.After(5 * time.Second)
	got := make(chan struct{})
	go func() {
		for {
			n, err := c.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				close(got)
				return
			}
		}
	}()
	select {
	case <-got:
	case <-deadline:
		t.Fatal("read timeout")
	}
	<-c.Done()
	return b.String()
}

func TestLocalExitStatusAndOutput(t *testing.T) {
	c, err := StartLocal(context.Background(), 80, 24, LocalOptions{Command: []string{"/bin/sh", "-c", "echo hello $TERM; exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	out := readAll(t, c)
	if !strings.Contains(out, "hello xterm-256color") {
		t.Errorf("output %q", out)
	}
	if c.Err() == nil || !strings.Contains(c.Err().Error(), "status 3") {
		t.Errorf("err %v", c.Err())
	}
}

func TestLocalSizeAndInput(t *testing.T) {
	c, err := StartLocal(context.Background(), 90, 23, LocalOptions{Command: []string{"/bin/sh", "-c", "stty size; read x; stty size; echo got-$x"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := c.Resize(100, 30); err != nil {
		t.Fatal(err)
	}
	io.WriteString(c, "abc\r")
	out := readAll(t, c)
	if !strings.Contains(out, "23 90") || !strings.Contains(out, "30 100") || !strings.Contains(out, "got-abc") {
		t.Errorf("output %q", out)
	}
	if c.Err() != nil {
		t.Errorf("clean exit: %v", c.Err())
	}
}

func TestLocalCloseKills(t *testing.T) {
	c, err := StartLocal(context.Background(), 80, 24, LocalOptions{Command: []string{"/bin/sh", "-c", "sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("not done after Close")
	}
}

func TestLocalNotFound(t *testing.T) {
	if _, err := StartLocal(context.Background(), 80, 24, LocalOptions{Command: []string{"no-such-shell-xyz"}}); err == nil {
		t.Fatal("expected error")
	}
}
