// SPDX-License-Identifier: MIT

//go:build linux || freebsd || openbsd || netbsd

package clipboard

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// A built-in X11 clipboard for desktops without xclip/xsel (e.g. a fresh
// Linux Mint, whose terminal also ignores OSC 52). vaultcrab owns the CLIPBOARD
// selection and answers paste requests while it runs; a clipboard manager
// usually keeps the text after that.

// maxX11 bounds the text served without the INCR protocol.
const maxX11 = 200 << 10

type x11 struct {
	mu      sync.Mutex
	conn    *xgb.Conn
	win     xproto.Window
	atoms   map[string]xproto.Atom
	text    []byte // what we serve while we own the clipboard
	owned   bool
	notify  chan xproto.SelectionNotifyEvent
	initErr error
}

var x11c struct {
	once sync.Once
	c    *x11
}

func x11Get() (*x11, error) {
	x11c.once.Do(func() { x11c.c = newX11() })
	return x11c.c, x11c.c.initErr
}

func newX11() *x11 {
	c := &x11{atoms: map[string]xproto.Atom{}, notify: make(chan xproto.SelectionNotifyEvent, 1)}
	c.initErr = c.init()
	return c
}

func (c *x11) init() error {
	if os.Getenv("DISPLAY") == "" {
		return errors.New("no X display (DISPLAY is not set)")
	}
	xgb.Logger = log.New(io.Discard, "", 0) // stderr would garble the TUI
	conn, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("X11: %w", err)
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		conn.Close()
		return err
	}
	if err := xproto.CreateWindowChecked(conn, 0, win, screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOnly, screen.RootVisual, 0, nil).Check(); err != nil {
		conn.Close()
		return fmt.Errorf("X11: %w", err)
	}
	for _, name := range []string{"CLIPBOARD", "UTF8_STRING", "TARGETS", "TEXT", "VAULTCRAB_SEL"} {
		r, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
		if err != nil {
			conn.Close()
			return fmt.Errorf("X11: %w", err)
		}
		c.atoms[name] = r.Atom
	}
	c.conn, c.win = conn, win
	go c.loop()
	return nil
}

func (c *x11) loop() {
	for {
		ev, err := c.conn.WaitForEvent()
		if ev == nil && err == nil {
			return // connection closed
		}
		switch e := ev.(type) {
		case xproto.SelectionRequestEvent:
			c.serve(e)
		case xproto.SelectionClearEvent:
			c.mu.Lock()
			c.owned = false
			wipe(c.text)
			c.text = nil
			c.mu.Unlock()
		case xproto.SelectionNotifyEvent:
			select {
			case c.notify <- e:
			default:
			}
		}
	}
}

// serve answers a paste request of another program.
func (c *x11) serve(e xproto.SelectionRequestEvent) {
	prop := e.Property
	if prop == xproto.AtomNone {
		prop = e.Target // obsolete clients
	}
	c.mu.Lock()
	text := append([]byte(nil), c.text...)
	owned := c.owned
	c.mu.Unlock()
	defer wipe(text)
	a := c.atoms
	switch {
	case !owned || e.Selection != a["CLIPBOARD"]:
		prop = xproto.AtomNone
	case e.Target == a["TARGETS"]:
		targets := []xproto.Atom{a["TARGETS"], a["UTF8_STRING"], a["TEXT"], xproto.AtomString}
		buf := make([]byte, 4*len(targets))
		for i, t := range targets {
			xgb.Put32(buf[4*i:], uint32(t))
		}
		xproto.ChangeProperty(c.conn, xproto.PropModeReplace, e.Requestor, prop, xproto.AtomAtom, 32, uint32(len(targets)), buf)
	case e.Target == a["UTF8_STRING"] || e.Target == a["TEXT"] || e.Target == xproto.AtomString:
		typ := e.Target
		if typ == a["TEXT"] {
			typ = a["UTF8_STRING"]
		}
		xproto.ChangeProperty(c.conn, xproto.PropModeReplace, e.Requestor, prop, typ, 8, uint32(len(text)), text)
	default:
		prop = xproto.AtomNone
	}
	ev := xproto.SelectionNotifyEvent{Time: e.Time, Requestor: e.Requestor, Selection: e.Selection, Target: e.Target, Property: prop}
	xproto.SendEvent(c.conn, false, e.Requestor, 0, string(ev.Bytes()))
}

// write takes the CLIPBOARD selection with text; "" gives it up.
func (c *x11) write(text string) error {
	if len(text) > maxX11 {
		return errors.New("text too long for the built-in X11 clipboard (install xclip)")
	}
	c.mu.Lock()
	wipe(c.text)
	c.text, c.owned = []byte(text), text != ""
	c.mu.Unlock()
	owner := c.win
	if text == "" {
		owner = xproto.WindowNone
	}
	if err := xproto.SetSelectionOwnerChecked(c.conn, owner, c.atoms["CLIPBOARD"], xproto.TimeCurrentTime).Check(); err != nil {
		return fmt.Errorf("X11: %w", err)
	}
	if text == "" {
		return nil
	}
	r, err := xproto.GetSelectionOwner(c.conn, c.atoms["CLIPBOARD"]).Reply()
	if err != nil || r.Owner != c.win {
		return errors.New("X11: could not take the clipboard")
	}
	return nil
}

// read returns the CLIPBOARD text (ours, or asked from its owner).
func (c *x11) read() (string, error) {
	c.mu.Lock()
	if c.owned {
		defer c.mu.Unlock()
		return string(c.text), nil
	}
	c.mu.Unlock()
	select { // a stale answer
	case <-c.notify:
	default:
	}
	a := c.atoms
	xproto.ConvertSelection(c.conn, c.win, a["CLIPBOARD"], a["UTF8_STRING"], a["VAULTCRAB_SEL"], xproto.TimeCurrentTime)
	select {
	case e := <-c.notify:
		if e.Property == xproto.AtomNone {
			return "", nil // empty clipboard or no text in it
		}
	case <-time.After(time.Second):
		return "", errors.New("X11: no answer from the clipboard owner")
	}
	r, err := xproto.GetProperty(c.conn, true, c.win, a["VAULTCRAB_SEL"], xproto.GetPropertyTypeAny, 0, maxX11/4).Reply()
	if err != nil {
		return "", fmt.Errorf("X11: %w", err)
	}
	return string(r.Value), nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// x11Available reports whether an X display is set (and not only Wayland
// with its own tools: XWayland also serves X11 clients).
func x11Available() bool { return os.Getenv("DISPLAY") != "" }

func writeX11(text string) error {
	c, err := x11Get()
	if err != nil {
		return err
	}
	return c.write(text)
}

func readX11() (string, error) {
	c, err := x11Get()
	if err != nil {
		return "", err
	}
	return c.read()
}
