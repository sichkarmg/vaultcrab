// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// The knock field of a host is knocked before the SSH connection, in
// order; an invalid one fails the connection with the reason; the trace
// shows the knocks.
func TestKnockBeforeSSH(t *testing.T) {
	srv := newTestServer(t, nil)
	k1, _ := net.Listen("tcp", "127.0.0.1:0")
	defer k1.Close()
	k2, _ := net.Listen("tcp", "127.0.0.1:0")
	defer k2.Close()
	order := make(chan int, 2)
	for i, l := range []net.Listener{k1, k2} {
		go func() {
			c, err := l.Accept()
			if err == nil {
				order <- i + 1
				c.Close()
			}
		}()
	}
	h := srv.host("u")
	p := func(l net.Listener) string { return strconv.Itoa(l.Addr().(*net.TCPAddr).Port) }
	h.Fields = map[string]string{FieldKnock: p(k1) + "," + p(k2), FieldKnockDelay: "30ms"}
	sec := secret.FromString("pw")
	tr := &Trace{}
	ctx := WithTrace(context.Background(), tr)
	c, err := DialSSH(ctx, h, &sec, 80, 24, SSHOptions{KnownHosts: filepath.Join(t.TempDir(), "kh")})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	for want := 1; want <= 2; want++ {
		select {
		case got := <-order:
			if got != want {
				t.Fatalf("knock %d came as %d", want, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no knock %d", want)
		}
	}
	if s := tr.String(); !strings.Contains(s, "port knocking at 127.0.0.1") || !strings.Contains(s, p(k2)+"/tcp") {
		t.Fatalf("trace:\n%s", s)
	}
	h.Fields[FieldKnock] = "7000/sctp"
	if _, err := DialSSH(context.Background(), h, &sec, 80, 24, SSHOptions{}); err == nil || !strings.Contains(err.Error(), "knock") {
		t.Fatalf("invalid knock: %v", err)
	}
}
