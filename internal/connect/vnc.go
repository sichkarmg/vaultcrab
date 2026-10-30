// SPDX-License-Identifier: MIT

package connect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/vnc"
)

// How the VNC client gets the password (external.vnc.password).
const (
	// VNCProxy (default): vaultcrab signs in to the server itself and lets the
	// client in through a local proxy without a password. The password is
	// never given to the client.
	VNCProxy = "proxy"
	// VNCStdin writes the password to the client's stdin
	// (e.g. `xtightvncviewer -autopass`).
	VNCStdin = "stdin"
	// VNCFile writes a vncpasswd file for the client ({passfile},
	// e.g. `vncviewer -passwd {passfile}`), removed after the client
	// started.
	VNCFile = "file"
	// VNCURL puts the password into {url}: vnc://user:pass@host:port
	// (macOS: `open vnc://...`). The URL is in the command line.
	VNCURL = "url"
	// VNCArgv substitutes {password} in the command line.
	VNCArgv = "argv"
)

// VNCModes are the valid values of external.vnc.password.
var VNCModes = []string{VNCProxy, VNCStdin, VNCFile, VNCURL, VNCArgv}

// VNCPlaceholder is the placeholder a mode needs in the command line.
func VNCPlaceholder(mode string) string {
	switch mode {
	case VNCFile:
		return "{passfile}"
	case VNCURL:
		return "{url}"
	case VNCArgv:
		return "{password}"
	}
	return ""
}

// passfileRemoveAfter: the vncpasswd file is removed when the client exits,
// or this long after it started (clients read it when they connect).
const passfileRemoveAfter = 10 * time.Second

// launcherExit: a client program exiting successfully this soon is a
// launcher (open, xdg-open) that handed the address to the real client.
const launcherExit = 3 * time.Second

// VNCConn is a running VNC client with its connection, shown as a session:
// Read returns what happens (vaultcrab's status lines and the client's own
// output), Done closes when the client is gone.
type VNCConn struct {
	cmd   *exec.Cmd
	proxy *vnc.Proxy
	fwd   net.Listener // forwarder for a jump host (modes other than proxy)
	tun   *Tunnel
	dir   string // temporary directory of the vncpasswd file

	// output for the session: written before anyone reads, so buffered
	outMu   sync.Mutex
	outCond *sync.Cond
	outBuf  []byte
	outDone bool

	once sync.Once
	done chan struct{}
	err  error
}

// VNC starts the VNC client for h and returns once it is running. With a
// jump function the server is reached through that SSH tunnel. sec is used
// before VNC returns and not retained: the caller wipes it.
func (l *Launcher) VNC(ctx context.Context, h model.Host, sec *secret.Secret, jump func(context.Context) (*Tunnel, error)) (_ *VNCConn, err error) {
	spec := l.spec(ctx, model.VNC)
	mode := spec.Password
	if mode == "" {
		mode = VNCProxy
		if spec.PasswordStdin {
			mode = VNCStdin
		}
	}
	cands, err := l.candidates(h, spec)
	if err != nil {
		return nil, err
	}
	if p := VNCPlaceholder(mode); p != "" && !cands[0].configured {
		return nil, fmt.Errorf("external.vnc.password: %s needs the client's command line with %s in external.vnc.%s", mode, p, runtime.GOOS)
	}
	c := &VNCConn{done: make(chan struct{})}
	c.outCond = sync.NewCond(&c.outMu)
	defer func() {
		if err != nil {
			c.cleanup()
		}
	}()

	port := h.Port
	if port == 0 {
		port = model.VNC.DefaultPort()
	}
	target := net.JoinHostPort(h.Addr, strconv.Itoa(port))
	dial := func(ctx context.Context) (net.Conn, error) {
		d := net.Dialer{Timeout: 15 * time.Second}
		return d.DialContext(ctx, "tcp", target)
	}
	via := ""
	if jump != nil {
		if c.tun, err = jump(ctx); err != nil {
			return nil, err
		}
		dial = func(ctx context.Context) (net.Conn, error) { return c.tun.Dial(ctx, target) }
		via = " via the jump host"
	}

	// where the client connects to
	local := h
	var passfile, password string
	switch mode {
	case VNCProxy:
		server, err := dial(ctx)
		if err != nil {
			return nil, fmt.Errorf("VNC server %s%s: %w", target, via, err)
		}
		stop := context.AfterFunc(ctx, func() { server.Close() })
		_ = server.SetDeadline(time.Now().Add(30 * time.Second))
		info, err := vnc.SignIn(server, sec.Value)
		_ = server.SetDeadline(time.Time{})
		stop()
		if err != nil {
			server.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("VNC server %s: %w", target, err)
		}
		if c.proxy, err = vnc.Listen(server); err != nil {
			server.Close()
			return nil, err
		}
		c.status("signed in to %s%s (RFB %s, %s)", target, via, info.Version, info.Security)
		local.Addr, local.Port = "127.0.0.1", c.proxy.Port()
	default:
		if c.tun != nil {
			if c.fwd, err = c.forward(dial); err != nil {
				return nil, err
			}
			local.Addr, local.Port = "127.0.0.1", c.fwd.Addr().(*net.TCPAddr).Port
		}
		switch mode {
		case VNCFile:
			if c.dir, err = os.MkdirTemp("", "vaultcrab-vnc-"); err != nil {
				return nil, err
			}
			passfile = filepath.Join(c.dir, "passwd")
			data := vnc.PasswdFile(sec.Value)
			err = os.WriteFile(passfile, data, 0o600)
			clear(data)
			if err != nil {
				return nil, err
			}
		case VNCURL, VNCArgv:
			password = string(sec.Value) // a command line holds strings
		}
	}

	argv := expandVNC(cands[0].argv0, local, passfile, password, mode)
	shown := expandVNC(cands[0].argv0, local, passfile, strings.Repeat("*", min(len(password), 8)), mode)
	cmd := exec.Command(argv[0], argv[1:]...) // the program itself, no shell (also on Windows)
	detach(cmd)
	// a launcher's children (xdg-open) may keep the output open
	cmd.WaitDelay = time.Second
	var stdin io.WriteCloser
	if mode == VNCStdin {
		if stdin, err = cmd.StdinPipe(); err != nil {
			return nil, err
		}
	}
	cmd.Stdout, cmd.Stderr = (*clientOutput)(c), (*clientOutput)(c)
	c.status("starting %s", strings.Join(shown, " "))
	if mode == VNCURL || mode == VNCArgv {
		c.status("\x1b[33mwarning:\x1b[0;2m the password is in the client's command line: other local users can see it in the process list while the client runs (external.vnc.password: %s)", mode)
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("VNC client %q not found; set external.vnc.%s in the config", argv[0], runtime.GOOS)
		}
		return nil, fmt.Errorf("starting the VNC client: %w", err)
	}
	c.cmd = cmd
	if stdin != nil {
		buf := append(append([]byte(nil), sec.Value...), '\n')
		_, _ = stdin.Write(buf)
		clear(buf)
		_ = stdin.Close()
	}
	go c.watch(baseName(argv[0]))
	return c, nil
}

// expandVNC fills the placeholders of argv (argv[0] already resolved) for
// the address the client connects to; {passfile}, {password} and, in url
// mode, the password in {url}.
func expandVNC(argv []string, h model.Host, passfile, password, mode string) []string {
	out := Expand(argv, h)
	out[0] = argv[0]
	var u string
	if mode == VNCURL {
		uu := url.URL{Scheme: "vnc", Host: net.JoinHostPort(h.Addr, strconv.Itoa(h.Port)), User: url.UserPassword(h.User, password)}
		u = uu.String()
	}
	for i, a := range argv[1:] {
		if mode == VNCURL && strings.Contains(a, "{url}") {
			out[i+1] = Expand([]string{strings.ReplaceAll(a, "{url}", "\x00")}, h)[0]
			out[i+1] = strings.ReplaceAll(out[i+1], "\x00", u)
		}
		out[i+1] = strings.NewReplacer("{passfile}", passfile, "{password}", password).Replace(out[i+1])
	}
	return out
}

// forward listens on 127.0.0.1 and connects every client connection through
// dial (the jump host) to the server.
func (c *VNCConn) forward(dial func(context.Context) (net.Conn, error)) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				server, err := dial(context.Background())
				if err != nil {
					c.status("\x1b[31merror:\x1b[0;2m %v", err)
					return
				}
				defer server.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(server, conn); done <- struct{}{} }()
				go func() { _, _ = io.Copy(conn, server); done <- struct{}{} }()
				<-done
			}()
		}
	}()
	return ln, nil
}

// watch follows the client: in proxy mode the connection, in every mode the
// process, and ends the session when the client is gone.
func (c *VNCConn) watch(name string) {
	exited := make(chan error, 1)
	go func() { exited <- c.cmd.Wait() }()
	exitErr := func(err error) error {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() > 0 {
			return fmt.Errorf("%s exited with status %d", name, ee.ExitCode())
		}
		return err
	}
	if c.dir != "" {
		go func() {
			select {
			case <-time.After(passfileRemoveAfter):
			case <-c.done:
			}
			os.RemoveAll(c.dir)
		}()
	}
	if c.proxy == nil {
		started := time.Now()
		err := exitErr(<-exited)
		if err == nil && c.fwd != nil && time.Since(started) < launcherExit {
			// a launcher (open) handed the address on: the client it started
			// still needs the tunnel
			c.status("%s exited; the tunnel stays open until this tab is closed", name)
			<-c.done
			return
		}
		if err == nil {
			err = fmt.Errorf("%s closed", name)
		}
		c.finish(err)
		return
	}

	type accepted struct {
		info vnc.ClientInfo
		err  error
	}
	acc := make(chan accepted, 1)
	go func() {
		info, err := c.proxy.Accept(context.Background(), vnc.AcceptTimeout)
		acc <- accepted{info, err}
	}()
	var procErr error
	running := true
	for {
		select {
		case err := <-exited:
			running = false
			if err = exitErr(err); err != nil {
				// a failure before connecting: the client's message is above
				c.finish(err)
				return
			}
			// a launcher (open, xdg-open) hands the address on and exits
			exited = nil
			continue
		case a := <-acc:
			if a.err != nil {
				c.finish(a.err)
				return
			}
			c.status("%s connected (RFB %s); the password stays with vaultcrab", name, a.info.Version)
			ferr := c.proxy.Forward()
			if running {
				if ferr != nil {
					c.status("connection closed: %v; waiting for %s to exit", ferr, name)
				}
				procErr = exitErr(<-exited)
			}
			switch {
			case procErr != nil:
				c.finish(procErr)
			case ferr != nil:
				c.finish(fmt.Errorf("connection lost: %w", ferr))
			default:
				c.finish(fmt.Errorf("%s closed", name))
			}
			return
		}
	}
}

// status writes a status line for the session.
func (c *VNCConn) status(format string, args ...any) {
	c.write([]byte("\x1b[0;2m[vnc] " + fmt.Sprintf(format, args...) + "\x1b[0m\r\n"))
}

func (c *VNCConn) write(p []byte) {
	c.outMu.Lock()
	defer c.outMu.Unlock()
	if !c.outDone {
		c.outBuf = append(c.outBuf, p...)
		c.outCond.Signal()
	}
}

// clientOutput writes the client's own output into the session as lines.
type clientOutput VNCConn

func (o *clientOutput) Write(p []byte) (int, error) {
	c := (*VNCConn)(o)
	sc := bufio.NewScanner(strings.NewReader(string(p)))
	for sc.Scan() {
		if line := strings.TrimRight(sc.Text(), "\r"); line != "" {
			c.write([]byte(line + "\r\n"))
		}
	}
	return len(p), nil
}

func (c *VNCConn) finish(err error) {
	c.once.Do(func() {
		c.err = err
		c.cleanup()
		c.outMu.Lock()
		c.outDone = true
		c.outMu.Unlock()
		c.outCond.Broadcast()
		close(c.done)
	})
}

// cleanup closes the connections and the tunnel, stops the client and
// removes the password file.
func (c *VNCConn) cleanup() {
	if c.proxy != nil {
		c.proxy.Close()
	}
	if c.fwd != nil {
		c.fwd.Close()
	}
	if c.tun != nil {
		c.tun.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill() // an error if it already exited
	}
	if c.dir != "" {
		os.RemoveAll(c.dir)
	}
}

// Read returns the status lines and the client's output.
func (c *VNCConn) Read(p []byte) (int, error) {
	c.outMu.Lock()
	defer c.outMu.Unlock()
	for len(c.outBuf) == 0 && !c.outDone {
		c.outCond.Wait()
	}
	if len(c.outBuf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.outBuf)
	c.outBuf = c.outBuf[n:]
	return n, nil
}

// Write ignores keys typed into the tab.
func (c *VNCConn) Write(p []byte) (int, error) { return len(p), nil }

// Resize does nothing: the client has its own window.
func (c *VNCConn) Resize(int, int) error { return nil }

// Done is closed when the client is gone.
func (c *VNCConn) Done() <-chan struct{} { return c.done }

// Err says why the client is gone.
func (c *VNCConn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close stops the client (its connection goes through vaultcrab anyway).
func (c *VNCConn) Close() error {
	c.finish(errors.New("closed"))
	return nil
}
