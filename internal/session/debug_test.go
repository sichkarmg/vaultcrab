// SPDX-License-Identifier: MIT

package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// With ssh.debug a failed attempt writes its steps into the terminal (and
// keeps them for copying); without it nothing is traced.
func TestDebugOnFailure(t *testing.T) {
	for _, on := range []bool{true, false} {
		s := New(model.Host{Name: "h", User: "u", Addr: "a", Scheme: model.SSH}, Options{
			Cols: 100, Rows: 20,
			Secret: func(context.Context) (secret.Secret, error) { return secret.FromString("pw-secret"), nil },
			Dial: func(ctx context.Context, _ model.Host, _ *secret.Secret, _, _ int) (Remote, error) {
				connect.TraceOf(ctx).Add("connecting to a:22")
				return nil, errors.New("connection refused")
			},
			Debug: func() bool { return on },
		})
		s.Connect()
		wait(t, s, Disconnected)
		text, d := s.Term.PlainText(), s.Debug()
		if on {
			for _, want := range []string{"connection debug", "reading the credential from the vault", "connecting to a:22", "failed: connection refused"} {
				if !strings.Contains(text, want) || !strings.Contains(d+"connection debug", want) {
					t.Errorf("lacks %q:\n%s\n--\n%s", want, text, d)
				}
			}
			if strings.Contains(text, "pw-secret") || strings.Contains(d, "pw-secret") {
				t.Fatal("secret in the debug")
			}
		} else if d != "" || strings.Contains(text, "connection debug") {
			t.Fatalf("traced while off:\n%s", text)
		}
		s.Close()
	}
}
