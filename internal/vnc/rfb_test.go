// SPDX-License-Identifier: MIT

package vnc

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Known values: the vncpasswd file of "password" (as written by vncpasswd of
// TightVNC/TigerVNC/RealVNC) and its use as the authentication key.
func TestPasswdFileVector(t *testing.T) {
	if got := hex.EncodeToString(PasswdFile([]byte("password"))); got != "dbd83cfd727a1458" {
		t.Errorf("PasswdFile(password) = %s, want dbd83cfd727a1458", got)
	}
	// longer passwords are cut to 8 bytes, as by every VNC implementation
	if !bytes.Equal(PasswdFile([]byte("password123")), PasswdFile([]byte("password"))) {
		t.Error("password not cut to 8 bytes")
	}
	// the file is the first 8 bytes encrypted with the fixed key; so is the
	// response to a challenge of the password twice with the fixed key as the
	// password
	fixed := []byte{23, 82, 107, 6, 35, 78, 88, 7}
	r := Response(fixed, []byte("passwordpassword"))
	if got := hex.EncodeToString(r[:8]); got != "dbd83cfd727a1458" {
		t.Errorf("Response = %s, want dbd83cfd727a1458", got)
	}
}

// testServer is a VNC server for one connection: version, security types
// (3.7/3.8) or type (3.3), the password for VNC authentication and an
// optional failure reason.
type testServer struct {
	version  string   // "003.008"
	types    []uint8  // offered (3.7+)
	type33   uint32   // chosen by the server (3.3)
	password string   // for VNC authentication
	reason   string   // refuse with this reason (3.7+: zero types; 3.3: type 0)
	gotVer   string   // what the client answered
	gotType  uint8    // what the client chose (3.7+)
	init     []byte   // ClientInit received
	after    []string // messages exchanged after the handshake, in order
}

func (s *testServer) serve(c net.Conn) error {
	defer c.Close()
	fmt.Fprintf(c, "RFB %s\n", s.version)
	var v [12]byte
	if _, err := io.ReadFull(c, v[:]); err != nil {
		return err
	}
	s.gotVer = strings.TrimSpace(string(v[4:]))
	v38 := s.gotVer == "003.008"
	typ := s.type33
	if s.gotVer == "003.003" {
		if s.reason != "" {
			binary.Write(c, binary.BigEndian, uint32(0))
			binary.Write(c, binary.BigEndian, uint32(len(s.reason)))
			io.WriteString(c, s.reason)
			return nil
		}
		binary.Write(c, binary.BigEndian, typ)
	} else {
		if s.reason != "" {
			c.Write([]byte{0})
			binary.Write(c, binary.BigEndian, uint32(len(s.reason)))
			io.WriteString(c, s.reason)
			return nil
		}
		c.Write(append([]byte{byte(len(s.types))}, s.types...))
		var ch [1]byte
		if _, err := io.ReadFull(c, ch[:]); err != nil {
			return err
		}
		s.gotType, typ = ch[0], uint32(ch[0])
	}
	ok := true
	if typ == secVNCAuth {
		challenge := make([]byte, 16)
		rand.Read(challenge)
		c.Write(challenge)
		resp := make([]byte, 16)
		if _, err := io.ReadFull(c, resp); err != nil {
			return err
		}
		ok = bytes.Equal(resp, Response([]byte(s.password), challenge))
	}
	if v38 || typ == secVNCAuth {
		if !ok {
			binary.Write(c, binary.BigEndian, uint32(1))
			if v38 {
				msg := "Authentication failure"
				binary.Write(c, binary.BigEndian, uint32(len(msg)))
				io.WriteString(c, msg)
			}
			return nil
		}
		binary.Write(c, binary.BigEndian, uint32(0))
	}
	// ClientInit, ServerInit, then one message each way
	s.init = make([]byte, 1)
	if _, err := io.ReadFull(c, s.init); err != nil {
		return err
	}
	io.WriteString(c, "SERVERINIT")
	msg := make([]byte, 6)
	if _, err := io.ReadFull(c, msg); err != nil {
		return err
	}
	s.after = append(s.after, string(msg))
	io.WriteString(c, "FRAME!")
	return nil
}

// start runs s on a local listener and returns its address and a channel
// with the result of serve.
func (s *testServer) start(t *testing.T) (string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		done <- s.serve(c)
	}()
	return ln.Addr().String(), done
}

// testClient is a VNC client of the given version without a password: it
// expects to be let in with security type None.
func testClient(addr, version string) (string, error) {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	var v [12]byte
	if _, err := io.ReadFull(c, v[:]); err != nil {
		return "", err
	}
	if string(v[:]) != "RFB 003.008\n" {
		return "", fmt.Errorf("proxy version %q", v)
	}
	fmt.Fprintf(c, "RFB %s\n", version)
	switch version {
	case "003.003":
		var typ uint32
		binary.Read(c, binary.BigEndian, &typ)
		if typ != secNone {
			return "", fmt.Errorf("3.3: security type %d", typ)
		}
	default:
		var n [1]byte
		io.ReadFull(c, n[:])
		types := make([]byte, n[0])
		io.ReadFull(c, types)
		if !bytes.Equal(types, []byte{secNone}) {
			return "", fmt.Errorf("security types %v, want [None]", types)
		}
		c.Write([]byte{secNone})
		if version == "003.008" {
			var res uint32
			binary.Read(c, binary.BigEndian, &res)
			if res != 0 {
				return "", fmt.Errorf("security result %d", res)
			}
		}
	}
	c.Write([]byte{1}) // ClientInit: shared
	init := make([]byte, len("SERVERINIT"))
	if _, err := io.ReadFull(c, init); err != nil {
		return "", fmt.Errorf("ServerInit: %w", err)
	}
	io.WriteString(c, "UPDATE")
	frame := make([]byte, 6)
	if _, err := io.ReadFull(c, frame); err != nil {
		return "", err
	}
	return string(init) + " " + string(frame), nil
}

// Every combination of server and client versions with VNC authentication
// and None on the server: the client gets in without a password and the
// traffic after the handshake goes through unchanged.
func TestProxyHandshake(t *testing.T) {
	servers := []testServer{
		{version: "003.003", type33: secVNCAuth, password: "secret12"},
		{version: "003.003", type33: secNone},
		{version: "003.007", types: []uint8{secVNCAuth}, password: "secret12"},
		{version: "003.008", types: []uint8{secVeNCrypt, secVNCAuth}, password: "secret12"}, // TigerVNC
		{version: "003.008", types: []uint8{secNone}},
		{version: "003.889", types: []uint8{secARD, secVNCAuth}, password: "secret12"}, // macOS
		{version: "005.000", types: []uint8{secRA2, secVNCAuth}, password: "secret12"}, // RealVNC
		{version: "003.005", type33: secVNCAuth, password: "secret12"},                 // old: as 3.3
	}
	for _, srv := range servers {
		for _, cv := range []string{"003.003", "003.007", "003.008"} {
			t.Run(srv.version+"/"+fmt.Sprint(srv.types, srv.type33)+"/client-"+cv, func(t *testing.T) {
				s := srv
				addr, done := s.start(t)
				conn, err := net.Dial("tcp", addr)
				if err != nil {
					t.Fatal(err)
				}
				pw := []byte("secret1234") // 8 bytes count
				info, err := SignIn(conn, pw)
				if err != nil {
					t.Fatalf("sign in: %v", err)
				}
				p, err := Listen(conn)
				if err != nil {
					t.Fatal(err)
				}
				defer p.Close()
				res := make(chan string, 1)
				go func() {
					got, err := testClient(fmt.Sprintf("127.0.0.1:%d", p.Port()), cv)
					if err != nil {
						got = "error: " + err.Error()
					}
					res <- got
				}()
				ci, err := p.Accept(context.Background(), 2*time.Second)
				if err != nil {
					t.Fatalf("accept: %v", err)
				}
				fwd := make(chan error, 1)
				go func() { fwd <- p.Forward() }()
				if got := <-res; got != "SERVERINIT FRAME!" {
					t.Errorf("client got %q", got)
				}
				if err := <-done; err != nil {
					t.Errorf("server: %v", err)
				}
				if err := <-fwd; err != nil {
					t.Errorf("forward: %v", err)
				}
				if ci.Version != cv {
					t.Errorf("client version %q, want %q", ci.Version, cv)
				}
				if !bytes.Equal(s.init, []byte{1}) || len(s.after) != 1 || s.after[0] != "UPDATE" {
					t.Errorf("server got init %v, messages %q", s.init, s.after)
				}
				wantType := uint8(secNone)
				if !bytes.Contains(append(s.types, byte(s.type33)), []byte{secNone}) {
					wantType = secVNCAuth
				}
				if s.version != "003.003" && s.version != "003.005" && s.gotType != wantType {
					t.Errorf("chose security type %d, want %d", s.gotType, wantType)
				}
				if info.Version != s.version {
					t.Errorf("server version %q, want %q", info.Version, s.version)
				}
			})
		}
	}
}

func TestSignInErrors(t *testing.T) {
	cases := []struct {
		name string
		srv  testServer
		pw   string
		want string
		as   any
	}{
		{"wrong password 3.8", testServer{version: "003.008", types: []uint8{secVNCAuth}, password: "right"}, "wrong",
			"VNC authentication failed: Authentication failure", new(*AuthError)},
		{"wrong password 3.3", testServer{version: "003.003", type33: secVNCAuth, password: "right"}, "wrong",
			"VNC authentication failed: wrong password", new(*AuthError)},
		{"refused 3.8", testServer{version: "003.008", reason: "Too many security failures"}, "x",
			"VNC authentication failed: Too many security failures", new(*AuthError)},
		{"refused 3.3", testServer{version: "003.003", reason: "Connection rejected"}, "x",
			"VNC authentication failed: Connection rejected", new(*AuthError)},
		{"ARD only", testServer{version: "003.889", types: []uint8{secARD}}, "x",
			"Apple Remote Desktop (30)", new(*UnsupportedError)},
		{"VeNCrypt only", testServer{version: "003.008", types: []uint8{secVeNCrypt, secTLS}}, "x",
			"VeNCrypt (19), TLS (18); set external.vnc.password", new(*UnsupportedError)},
		{"MS-Logon 3.3", testServer{version: "003.003", type33: secMSLogon}, "x",
			"MS-Logon (UltraVNC) (-6)", new(*UnsupportedError)},
		{"no password", testServer{version: "003.008", types: []uint8{secVNCAuth}, password: "right"}, "",
			ErrNoPassword.Error(), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, _ := c.srv.start(t)
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_, err = SignIn(conn, []byte(c.pw))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if c.as != nil && !errors.As(err, c.as) {
				t.Errorf("err %T is not %T", err, c.as)
			}
		})
	}
}

// The listener takes exactly one connection and is gone after it, and after
// the timeout when nobody connects.
func TestProxyOneConnection(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p, err := Listen(a)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", p.Port())
	go testClient(addr, "003.008")
	if _, err := p.Accept(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Error("a second connection was accepted")
	}

	a2, b2 := net.Pipe()
	defer b2.Close()
	p2, err := Listen(a2)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	start := time.Now()
	_, err = p2.Accept(context.Background(), 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not connect within 300ms") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("timeout not honored")
	}
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p2.Port()), time.Second); err == nil {
		c.Close()
		t.Error("listener still open after the timeout")
	}
}
