// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// testServer is a minimal SSH server: accepts password "pw" or the given
// client key, grants a PTY, echoes input and reports window changes.
type testServer struct {
	addr    string
	hostKey ssh.Signer
	mu      sync.Mutex
	sizes   []string
	reqs    []string // global/channel requests other than pty/shell/window-change
	l       net.Listener
}

func newTestServer(t *testing.T, clientKey ssh.PublicKey) *testServer {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	hk, _ := ssh.NewSignerFromKey(priv)
	return startTestServer(t, hk, clientKey)
}

func startTestServer(t *testing.T, hk ssh.Signer, clientKey ssh.PublicKey) *testServer {
	return startTestServerCfg(t, hk, clientKey, nil)
}

func startTestServerCfg(t *testing.T, hk ssh.Signer, clientKey ssh.PublicKey, mod func(*ssh.ServerConfig)) *testServer {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == "u" && string(pw) == "pw" {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if clientKey != nil && string(k.Marshal()) == string(clientKey.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(hk)
	if mod != nil {
		mod(cfg)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &testServer{addr: l.Addr().String(), hostKey: hk, l: l}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(c, cfg)
		}
	}()
	return s
}

func (s *testServer) serve(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	go func() {
		for r := range reqs {
			s.addReq("global:" + r.Type)
			if r.WantReply {
				r.Reply(false, nil)
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() == "direct-tcpip" {
			go s.forward(nc)
			continue
		}
		ch, creqs, _ := nc.Accept()
		go func() {
			for r := range creqs {
				switch r.Type {
				case "pty-req":
					// string term, uint32 cols, rows
					tl := binary.BigEndian.Uint32(r.Payload)
					p := r.Payload[4+tl:]
					s.addSize(binary.BigEndian.Uint32(p), binary.BigEndian.Uint32(p[4:]))
					r.Reply(true, nil)
				case "window-change":
					s.addSize(binary.BigEndian.Uint32(r.Payload), binary.BigEndian.Uint32(r.Payload[4:]))
				case "shell":
					r.Reply(true, nil)
					go func() {
						io.WriteString(ch, "welcome\r\n")
						buf := make([]byte, 256)
						for {
							n, err := ch.Read(buf)
							if err != nil {
								return
							}
							if strings.Contains(string(buf[:n]), "exit") {
								ch.SendRequest("exit-status", false, []byte{0, 0, 0, 3})
								ch.Close()
								return
							}
							ch.Write(buf[:n])
						}
					}()
				default:
					s.addReq("channel:" + r.Type)
					r.Reply(false, nil)
				}
			}
		}()
	}
}

// forward serves a direct-tcpip channel (the server as a jump host).
func (s *testServer) forward(nc ssh.NewChannel) {
	var p struct {
		Host     string
		Port     uint32
		OrigHost string
		OrigPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
		nc.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	s.addReq(fmt.Sprintf("direct-tcpip:%s:%d", p.Host, p.Port))
	c, err := net.DialTimeout("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))), 2*time.Second)
	if err != nil {
		nc.Reject(ssh.ConnectionFailed, "connect failed (Connection refused)")
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		c.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() { io.Copy(ch, c); ch.CloseWrite(); ch.Close() }()
	go func() { io.Copy(c, ch); c.Close() }()
}

func (s *testServer) addSize(cols, rows uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes = append(s.sizes, fmt.Sprintf("%dx%d", cols, rows))
}

func (s *testServer) host(user string) model.Host {
	h, p, _ := net.SplitHostPort(s.addr)
	port, _ := strconv.Atoi(p)
	return model.Host{Scheme: model.SSH, User: user, Addr: h, Port: port}
}

func readUntil(t *testing.T, c *Conn, want string) {
	t.Helper()
	var got strings.Builder
	buf := make([]byte, 256)
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(got.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %q, got %q", want, got.String())
		}
		n, err := c.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			t.Fatalf("read: %v (got %q)", err, got.String())
		}
	}
}

func TestPasswordPTYResizeExit(t *testing.T) {
	srv := newTestServer(t, nil)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	sec := secret.FromString("pw")
	c, err := DialSSH(context.Background(), srv.host("u"), &sec, 100, 30, SSHOptions{KnownHosts: kh})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "welcome")
	c.Write([]byte("hello"))
	readUntil(t, c, "hello")
	if err := c.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	srv.mu.Lock()
	sizes := strings.Join(srv.sizes, ",")
	srv.mu.Unlock()
	if sizes != "100x30,120x40" {
		t.Errorf("sizes %s", sizes)
	}
	c.Write([]byte("exit"))
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("not done")
	}
	if c.Err() == nil || !strings.Contains(c.Err().Error(), "status 3") {
		t.Errorf("err %v", c.Err())
	}
	b, _ := os.ReadFile(kh)
	if !strings.Contains(string(b), "ssh-ed25519") {
		t.Errorf("known_hosts not written: %q", b)
	}
}

func TestBadPassword(t *testing.T) {
	srv := newTestServer(t, nil)
	sec := secret.FromString("nope")
	_, err := DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{})
	if err == nil || err.Error() != "authentication failed" {
		t.Fatalf("err %v", err)
	}
}

func TestFlattenedKeyAuth(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	srv := newTestServer(t, sshPub)
	blk, _ := ssh.MarshalPrivateKey(priv, "")
	flat := strings.ReplaceAll(string(pem.EncodeToMemory(blk)), "\n", " ")
	sec := secret.FromString(flat)
	if sec.Kind != secret.PrivateKey {
		t.Fatal("not a key")
	}
	c, err := DialSSH(context.Background(), srv.host("anyone"), &sec, 80, 24, SSHOptions{})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "welcome")
	c.Close()
}

func TestHostKeyChanged(t *testing.T) {
	kh := filepath.Join(t.TempDir(), "known_hosts")
	srv := newTestServer(t, nil)
	sec := secret.FromString("pw")
	c, err := DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{KnownHosts: kh})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// Same address, new host key: rewrite known_hosts entry to another key.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	op, _ := ssh.NewPublicKey(other.Public())
	b, _ := os.ReadFile(kh)
	fields := strings.Fields(string(b))
	os.WriteFile(kh, []byte(fields[0]+" "+string(ssh.MarshalAuthorizedKey(op))), 0o600)
	_, err = DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{KnownHosts: kh})
	var hke *HostKeyError
	if !errors.As(err, &hke) {
		t.Fatalf("want HostKeyError, got %v", err)
	}
	// Strict mode rejects unknown hosts.
	_, err = DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{KnownHosts: filepath.Join(t.TempDir(), "kh"), StrictHosts: true})
	var uh *UnknownHostError
	if !errors.As(err, &uh) {
		t.Fatalf("want UnknownHostError, got %v", err)
	}
}

func TestConnectionDrop(t *testing.T) {
	srv := newTestServer(t, nil)
	sec := secret.FromString("pw")
	c, err := DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{Keepalive: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "welcome")
	c.client.Conn.Close() // simulate a dead transport
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("drop not detected")
	}
	if c.Err() == nil {
		t.Error("no error on drop")
	}
}

func (s *testServer) addReq(r string) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	s.mu.Unlock()
}

func (s *testServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.reqs...)
}

// Keepalives go as channel requests: RouterOS answers unknown global requests
// with SSH_MSG_UNIMPLEMENTED, on which x/crypto/ssh drops the connection.
func TestKeepaliveIsChannelRequest(t *testing.T) {
	srv := newTestServer(t, nil)
	sec := secret.New([]byte("pw"))
	c, err := DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{Keepalive: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	time.Sleep(200 * time.Millisecond)
	select {
	case <-c.Done():
		t.Fatalf("connection ended: %v", c.Err())
	default:
	}
	n := 0
	for _, r := range srv.requests() {
		switch r {
		case "channel:keepalive@openssh.com":
			n++
		default:
			t.Errorf("unexpected request %s", r)
		}
	}
	if n < 3 {
		t.Errorf("keepalives: %d", n)
	}
}

// Old devices (RouterOS 6, old embedded sshd) offer only weak algorithms.
func TestLegacyAlgorithms(t *testing.T) {
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rs, _ := ssh.NewSignerFromKey(rk)
	hk, _ := ssh.NewSignerWithAlgorithms(rs.(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA})
	srv := startTestServerCfg(t, hk, nil, func(c *ssh.ServerConfig) {
		c.KeyExchanges = []string{"diffie-hellman-group1-sha1"}
		c.Ciphers = []string{"aes128-cbc"}
		c.MACs = []string{"hmac-sha1"}
	})
	sec := secret.New([]byte("pw"))
	if _, err := DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{}); err == nil {
		t.Fatal("connected without legacy algorithms")
	}
	sec = secret.New([]byte("pw"))
	c, err := DialSSH(context.Background(), srv.host("u"), &sec, 80, 24, SSHOptions{Legacy: true})
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	c.Close()
}
