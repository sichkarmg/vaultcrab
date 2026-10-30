// SPDX-License-Identifier: MIT

package connect

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/vnc"
)

const vncTestPassword = "Pw0rd!xy" // 8 bytes: all of it counts

// vncServer accepts VNC connections that sign in with password (RFB 3.8,
// VNC authentication) and answers ClientInit with "SERVERINIT".
func vncServer(t *testing.T, password string) (model.Host, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.WriteString(c, "RFB 003.008\n")
				v := make([]byte, 12)
				io.ReadFull(c, v)
				c.Write([]byte{2, 19, 2}) // VeNCrypt, VNC authentication
				typ := make([]byte, 1)
				io.ReadFull(c, typ)
				ch := make([]byte, 16)
				rand.Read(ch)
				c.Write(ch)
				resp := make([]byte, 16)
				io.ReadFull(c, resp)
				if !bytes.Equal(resp, vnc.Response([]byte(password), ch)) {
					binary.Write(c, binary.BigEndian, uint32(1))
					binary.Write(c, binary.BigEndian, uint32(6))
					io.WriteString(c, "failed")
					got <- "auth failed"
					return
				}
				binary.Write(c, binary.BigEndian, uint32(0))
				init := make([]byte, 1)
				io.ReadFull(c, init)
				io.WriteString(c, "SERVERINIT")
				got <- "signed in"
				io.Copy(io.Discard, c)
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	return model.Host{ID: "v1", Name: "desk", Scheme: model.VNC, User: "bob", Addr: "127.0.0.1", Port: port}, got
}

// TestVNCHelper is the VNC client the tests start (the test binary itself):
// it records its command line and environment, then connects. Arguments
// after "--": mode host port [secret-arg].
func TestVNCHelper(t *testing.T) {
	dir := os.Getenv("VAULTCRAB_VNC_HELPER")
	if dir == "" {
		t.Skip("helper process only")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	var rec strings.Builder
	fmt.Fprintf(&rec, "argv: %q\nenv: %q\n", os.Args, os.Environ())
	if b, err := os.ReadFile("/proc/self/cmdline"); err == nil {
		fmt.Fprintf(&rec, "cmdline: %q\n", b)
	}
	if b, err := os.ReadFile("/proc/self/environ"); err == nil {
		fmt.Fprintf(&rec, "environ: %q\n", b)
	}
	mode, host, port := args[0], args[1], args[2]
	var password []byte
	switch mode {
	case VNCStdin:
		line, _ := io.ReadAll(io.LimitReader(os.Stdin, 64))
		password = bytes.TrimRight(line, "\n")
	case VNCFile:
		b, err := os.ReadFile(args[3])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, vnc.PasswdFile([]byte(vncTestPassword))) {
			t.Fatalf("passwd file %x", b)
		}
		password = []byte(vncTestPassword)
	case VNCArgv:
		password = []byte(args[3])
	case VNCURL:
		password = []byte(args[3]) // checked by the test
	}
	os.WriteFile(filepath.Join(dir, "record-"+mode), []byte(rec.String()), 0o600)
	fmt.Println("helper: connecting to", host, port)
	c, err := net.Dial("tcp", net.JoinHostPort(strings.Trim(host, "[]"), port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if mode == VNCProxy {
		v := make([]byte, 12)
		io.ReadFull(c, v)
		io.WriteString(c, "RFB 003.008\n")
		n := make([]byte, 2)
		io.ReadFull(c, n) // [1 None]
		c.Write([]byte{1})
		res := make([]byte, 4)
		io.ReadFull(c, res)
	} else {
		if _, err := vnc.SignIn(c, password); err != nil {
			t.Fatal(err)
		}
	}
	c.Write([]byte{1})
	init := make([]byte, 10)
	if _, err := io.ReadFull(c, init); err != nil || string(init) != "SERVERINIT" {
		t.Fatalf("ServerInit %q: %v", init, err)
	}
	fmt.Println("helper: got", string(init))
}

func helperLauncher(t *testing.T, mode string, extra ...string) *Launcher {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := append([]string{exe, "-test.run=^TestVNCHelper$", "--", mode, "{host}", "{port}"}, extra...)
	return &Launcher{Specs: map[model.Scheme]LaunchSpec{model.VNC: {Command: cmd, Password: mode}},
		LookPath: func(p string) (string, error) { return p, nil }}
}

// runVNC starts the helper client for h and returns its session output.
func runVNC(t *testing.T, l *Launcher, h model.Host) (string, error) {
	t.Helper()
	sec := secret.Secret{Value: []byte(vncTestPassword)}
	c, err := l.VNC(context.Background(), h, &sec, nil)
	sec.Wipe()
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&out, c); close(done) }()
	select {
	case <-c.Done():
	case <-time.After(20 * time.Second):
		c.Close()
		t.Fatal("the client did not finish")
	}
	<-done
	return out.String(), c.Err()
}

// The default mode: the client gets in through the proxy, and the password
// is in none of its arguments, its environment or any file under the
// temporary directory, the home directory or the config directories.
func TestVNCProxyPasswordNowhere(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"TMPDIR", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(v, dir)
	}
	t.Setenv("VAULTCRAB_VNC_HELPER", dir)
	h, got := vncServer(t, vncTestPassword)
	out, err := runVNC(t, helperLauncher(t, VNCProxy), h)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("end: %v", err)
	}
	if s := <-got; s != "signed in" {
		t.Fatalf("server: %s", s)
	}
	for _, want := range []string{"signed in to 127.0.0.1:" + strconv.Itoa(h.Port), "VNC authentication",
		"connected (RFB 003.008)", "helper: connecting to 127.0.0.1", "helper: got SERVERINIT"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	rec, err := os.ReadFile(filepath.Join(dir, "record-proxy"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "linux" && !strings.Contains(string(rec), "cmdline:") {
		t.Error("/proc/self/cmdline not recorded")
	}
	// every file left or written in the directories the client and vaultcrab use
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		if bytes.Contains(b, []byte(vncTestPassword)) || bytes.Contains(b, vnc.PasswdFile([]byte(vncTestPassword))) {
			t.Errorf("the password is in %s", p)
		}
		return nil
	})
	if strings.Contains(out, vncTestPassword) {
		t.Error("the password is in the session output")
	}
}

// The fallback modes hand the password to the client as configured; the
// command line shown in the tab hides it, and the vncpasswd file is gone
// after the client exits.
func TestVNCPasswordModes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TEMP", dir)
	t.Setenv("TMP", dir)
	t.Setenv("VAULTCRAB_VNC_HELPER", dir)
	for _, c := range []struct {
		mode  string
		extra []string
	}{
		{VNCStdin, nil},
		{VNCFile, []string{"{passfile}"}},
		{VNCArgv, []string{"{password}"}},
		{VNCURL, []string{"{url}"}},
	} {
		t.Run(c.mode, func(t *testing.T) {
			h, got := vncServer(t, vncTestPassword)
			l := helperLauncher(t, c.mode, c.extra...)
			if c.mode == VNCURL {
				// the helper gets the password out of the URL
			}
			out, err := runVNC(t, l, h)
			if c.mode == VNCURL {
				// the helper connects with the URL as password: the server refuses
				if !strings.Contains(out, "vnc://bob:%2A%2A%2A%2A%2A%2A%2A%2A@") && !strings.Contains(out, "vnc://bob:********@") {
					t.Errorf("url not shown masked:\n%s", out)
				}
				if !strings.Contains(out, "the password is in the client's command line") {
					t.Error("no warning for the password in the command line")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "closed") {
				t.Errorf("end: %v\n%s", err, out)
			}
			if s := <-got; s != "signed in" {
				t.Fatalf("server: %s\n%s", s, out)
			}
			if strings.Contains(out, vncTestPassword) {
				t.Errorf("the password is in the session output:\n%s", out)
			}
			if c.mode == VNCArgv && !strings.Contains(out, "the password is in the client's command line") {
				t.Error("no warning for the password in the command line")
			}
			if left, _ := filepath.Glob(filepath.Join(dir, "vaultcrab-vnc-*")); len(left) > 0 {
				t.Errorf("password file left: %v", left)
			}
		})
	}
}

// Wrong password, server gone, client missing or failing: an error for the
// tab, nothing left running.
func TestVNCErrors(t *testing.T) {
	t.Setenv("VAULTCRAB_VNC_HELPER", t.TempDir())
	h, _ := vncServer(t, "other")
	sec := secret.Secret{Value: []byte(vncTestPassword)}
	if _, err := helperLauncher(t, VNCProxy).VNC(context.Background(), h, &sec, nil); err == nil ||
		!strings.Contains(err.Error(), "VNC authentication failed: failed") {
		t.Errorf("wrong password: %v", err)
	}
	gone := h
	gone.Port = 1
	if _, err := helperLauncher(t, VNCProxy).VNC(context.Background(), gone, &sec, nil); err == nil ||
		!strings.Contains(err.Error(), "VNC server 127.0.0.1:1") {
		t.Errorf("no server: %v", err)
	}
	l := &Launcher{Specs: map[model.Scheme]LaunchSpec{model.VNC: {Command: []string{"no-such-vncviewer", "{host}::{port}"}}}}
	if _, err := l.VNC(context.Background(), h, &sec, nil); err == nil ||
		!strings.Contains(err.Error(), "no-such-vncviewer") || !strings.Contains(err.Error(), "external.vnc.") {
		t.Errorf("missing client: %v", err)
	}
	l = &Launcher{Specs: map[model.Scheme]LaunchSpec{model.VNC: {Password: VNCArgv}},
		Fallbacks: map[model.Scheme][][]string{model.VNC: {{"vncviewer", "{host}::{port}"}}},
		LookPath:  func(p string) (string, error) { return p, nil }}
	if _, err := l.VNC(context.Background(), h, &sec, nil); err == nil || !strings.Contains(err.Error(), "{password}") {
		t.Errorf("argv mode without a command: %v", err)
	}
	// a client that fails before connecting
	h2, _ := vncServer(t, vncTestPassword)
	exe, _ := os.Executable()
	l = &Launcher{Specs: map[model.Scheme]LaunchSpec{model.VNC: {Command: []string{exe, "-test.run=^TestNothing$", "-test.badflag"}}},
		LookPath: func(p string) (string, error) { return p, nil }}
	out, err := runVNC(t, l, h2)
	if err == nil || !strings.Contains(err.Error(), "exited with status") {
		t.Errorf("failing client: %v\n%s", err, out)
	}
}
