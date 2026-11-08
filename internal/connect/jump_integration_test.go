// SPDX-License-Identifier: MIT

//go:build integration

package connect

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// A real chain on the test VMs (scripts/e2e.sh): VAULTCRAB_IT_JUMP and
// VAULTCRAB_IT_JUMP_TARGET are user@host[:port], VAULTCRAB_IT_JUMP_KEY a
// private key file accepted by both (a TEST key).
func jumpEnv(t *testing.T) (jump, target Hop, key ssh.Signer) {
	t.Helper()
	j, tg, kf := os.Getenv("VAULTCRAB_IT_JUMP"), os.Getenv("VAULTCRAB_IT_JUMP_TARGET"), os.Getenv("VAULTCRAB_IT_JUMP_KEY")
	if j == "" || tg == "" || kf == "" {
		t.Skip("VAULTCRAB_IT_JUMP, VAULTCRAB_IT_JUMP_TARGET, VAULTCRAB_IT_JUMP_KEY not set")
	}
	key, err := SignerFromFile(kf, nil)
	if err != nil {
		t.Fatal(err)
	}
	hop := func(s string) Hop {
		user, hp, ok := strings.Cut(s, "@")
		if !ok {
			t.Fatalf("%q: want user@host[:port]", s)
		}
		if _, _, err := net.SplitHostPort(hp); err != nil {
			hp = net.JoinHostPort(hp, "22")
		}
		return Hop{Name: hp, Addr: hp, User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(key)}}
	}
	return hop(j), hop(tg), key
}

func itDial(t *testing.T, j, tg Hop) (*Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return DialJump(ctx, j, tg, 100, 30, SSHOptions{})
}

func TestIntegrationJump(t *testing.T) {
	j, tg, _ := jumpEnv(t)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	asked := 0
	j.Keys = HostKeyPolicy{File: kh, Mode: HostKeyAcceptNew}
	tg.Keys = HostKeyPolicy{File: kh, Mode: HostKeyAsk, Ask: func(_ context.Context, q HostKeyQuestion) (HostKeyAnswer, error) {
		asked++
		t.Logf("new host key of the %s %s: %s %s", q.Role, q.Host, q.KeyType, q.Fingerprint)
		return HostKeyForever, nil
	}}

	t.Run("chain", func(t *testing.T) {
		c, err := itDial(t, j, tg)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		// some output of the shell on the target
		got := make(chan string, 1)
		go func() {
			buf := make([]byte, 4096)
			n, _ := c.Read(buf)
			got <- string(buf[:n])
		}()
		select {
		case s := <-got:
			if s == "" {
				t.Fatal("no output from the target shell")
			}
		case <-time.After(20 * time.Second):
			t.Fatal("no output from the target shell")
		}
		if asked != 1 {
			t.Fatalf("asked %d times about the target key", asked)
		}
		// remembered: the next connection does not ask
		c2, err := itDial(t, j, tg)
		if err != nil {
			t.Fatal(err)
		}
		c2.Close()
		if asked != 1 {
			t.Fatalf("asked again (%d)", asked)
		}
	})

	t.Run("changed host key", func(t *testing.T) {
		kh2 := filepath.Join(t.TempDir(), "known_hosts")
		_, other := newKey(t)
		os.WriteFile(kh2, []byte(knownhosts.Line([]string{knownhosts.Normalize(tg.Addr)}, other.PublicKey())+"\n"), 0o600)
		j2, tg2 := j, tg
		j2.Keys.File, tg2.Keys.File = kh2, kh2
		_, err := itDial(t, j2, tg2)
		var je *JumpError
		if !errors.As(err, &je) || !je.Target || je.Stage != StageHostKey || !strings.Contains(err.Error(), "HOST KEY CHANGED") {
			t.Fatalf("%v", err)
		}
		b, _ := os.ReadFile(kh2)
		if strings.Count(string(b), "\n") != 2 { // the old target line + the jump host
			t.Fatalf("known_hosts changed:\n%s", b)
		}
	})

	t.Run("target unreachable", func(t *testing.T) {
		tg3 := tg
		h, _, _ := net.SplitHostPort(tg.Addr)
		tg3.Addr = net.JoinHostPort(h, strconv.Itoa(1)) // nothing listens on port 1
		_, err := itDial(t, j, tg3)
		var je *JumpError
		if !errors.As(err, &je) || !je.Target || je.Stage != StageConnect || !strings.Contains(err.Error(), "unreachable from the jump host") {
			t.Fatalf("%v", err)
		}
		t.Log(err)
	})

	t.Run("wrong key on the target", func(t *testing.T) {
		_, wrong := newKey(t)
		tg4 := tg
		tg4.Auth = []ssh.AuthMethod{ssh.PublicKeys(wrong)}
		_, err := itDial(t, j, tg4)
		var je *JumpError
		if !errors.As(err, &je) || !je.Target || je.Stage != StageAuth {
			t.Fatalf("%v", err)
		}
		t.Log(err)
	})
}
