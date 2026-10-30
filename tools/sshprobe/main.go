// SPDX-License-Identifier: MIT

// Command sshprobe connects with the built-in SSH client (as vaultcrab does),
// feeds the output through the terminal emulator and prints the screen.
// Used to debug servers such as MikroTik RouterOS. The password is read from
// stdin (first line), never from argv.
//
//	printf '%s\n' "$PW" | go run ./tools/sshprobe -addr 10.0.0.1:22 -user admin -send '/system resource print\r'
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/term"
)

func main() {
	addr := flag.String("addr", "", "host:port")
	user := flag.String("user", "admin", "user")
	send := flag.String("send", "", "input to send after connect (\\r, \\n, \\x1b escapes)")
	wait := flag.Duration("wait", 5*time.Second, "how long to read output")
	ka := flag.Duration("keepalive", 2*time.Second, "keepalive interval (<0 disables)")
	timeout := flag.Duration("timeout", 15*time.Second, "handshake timeout")
	kh := flag.String("known-hosts", "", "known_hosts file (empty = accept any)")
	keyFile := flag.String("key", "", "private key file (instead of a password on stdin)")
	raw := flag.Bool("raw", false, "also dump raw output bytes")
	flag.Parse()
	host, ps, err := net.SplitHostPort(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	port, _ := strconv.Atoi(ps)
	var pw []byte
	if *keyFile != "" {
		if pw, err = os.ReadFile(*keyFile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	} else {
		pw, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
		pw = []byte(strings.TrimRight(string(pw), "\r\n"))
	}
	sec := secret.New(pw)
	h := model.Host{Name: "probe", Scheme: model.SSH, User: *user, Addr: host, Port: port}
	ctx, cancel := context.WithTimeout(context.Background(), 2**timeout)
	defer cancel()
	c, err := connect.DialSSH(ctx, h, &sec, 100, 30, connect.SSHOptions{KnownHosts: *kh, Keepalive: *ka, Timeout: *timeout})
	sec.Wipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	t := term.New(100, 30, 1000)
	t.SetSink(writerFunc(func(p []byte) (int, error) {
		if *raw {
			fmt.Printf("== reply: %q\n", p)
		}
		return c.Write(p)
	}))
	var rawBuf strings.Builder
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				rawBuf.Write(buf[:n])
				t.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(*wait / 2)
	if *send != "" {
		s, _ := strconv.Unquote(`"` + *send + `"`)
		c.Write([]byte(s))
	}
	select {
	case <-c.Done():
		fmt.Println("== connection ended:", c.Err())
	case <-time.After(*wait / 2):
	}
	fmt.Println("== screen:")
	fmt.Println(t.History())
	if *raw {
		fmt.Printf("== raw: %q\n", rawBuf.String())
	}
	fmt.Println("== state err:", c.Err())
	c.Close()
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
