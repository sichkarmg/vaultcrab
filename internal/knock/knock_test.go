// SPDX-License-Identifier: MIT

package knock

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	s, err := Parse("7000, 8000/udp 9000/TCP", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.String() != "7000/tcp, 8000/udp, 9000/tcp" || s.Delay != DefaultDelay {
		t.Fatalf("%s %v", s, s.Delay)
	}
	for in, want := range map[string]time.Duration{"300ms": 300 * time.Millisecond, "1s": time.Second, "50": 50 * time.Millisecond, "0": 0} {
		s, err := Parse("1", in)
		if err != nil || s.Delay != want {
			t.Errorf("%q: %v %v", in, s.Delay, err)
		}
	}
	for _, bad := range [][2]string{{"0", ""}, {"70000", ""}, {"x", ""}, {"1/sctp", ""}, {"1", "soon"}, {"1", "1m"}} {
		if _, err := Parse(bad[0], bad[1]); err == nil {
			t.Errorf("%q %q accepted", bad[0], bad[1])
		}
	}
	if s, _ := Parse("  ", ""); !s.Empty() {
		t.Error("blank not empty")
	}
}

// The knocks arrive in order: TCP attempts (open or not) and UDP packets.
func TestDo(t *testing.T) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	got := make(chan string, 4)
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			got <- "tcp"
			c.Close()
		}
	}()
	go func() {
		b := make([]byte, 16)
		for {
			if _, _, err := udp.ReadFrom(b); err != nil {
				return
			}
			got <- "udp"
		}
	}()
	tp := tcp.Addr().(*net.TCPAddr).Port
	up := udp.LocalAddr().(*net.UDPAddr).Port
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	cp := closed.Addr().(*net.TCPAddr).Port
	closed.Close()
	s, err := Parse(strconv.Itoa(tp)+" "+strconv.Itoa(up)+"/udp "+strconv.Itoa(cp), "20ms")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	start := time.Now()
	if err := s.Do(context.Background(), "127.0.0.1", func(f string, a ...any) { lines = append(lines, f) }); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Errorf("no pauses: %v", d)
	}
	for _, want := range []string{"tcp", "udp"} {
		select {
		case g := <-got:
			if g != want {
				t.Fatalf("got %s, want %s", g, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no %s knock", want)
		}
	}
	if len(lines) != 3 || !strings.Contains(lines[0], "port knocking") {
		t.Fatalf("log %v", lines)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Do(ctx, "127.0.0.1", nil); err == nil {
		t.Fatal("canceled knocking succeeded")
	}
}
