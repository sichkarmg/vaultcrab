// SPDX-License-Identifier: MIT

package secret

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func testKeys(t *testing.T) map[string][]byte {
	t.Helper()
	keys := map[string][]byte{}

	_, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	b, err := ssh.MarshalPrivateKey(edPriv, "test")
	if err != nil {
		t.Fatal(err)
	}
	keys["openssh-ed25519"] = pem.EncodeToMemory(b)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys["pkcs1-rsa"] = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})
	b, _ = ssh.MarshalPrivateKey(rsaKey, "")
	keys["openssh-rsa"] = pem.EncodeToMemory(b)

	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalECPrivateKey(ecKey)
	keys["sec1-ecdsa"] = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	der, _ = x509.MarshalPKCS8PrivateKey(ecKey)
	keys["pkcs8-ecdsa"] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return keys
}

func TestNormalizePEMVariants(t *testing.T) {
	variants := map[string]func(string) string{
		"as-is":        func(s string) string { return s },
		"flat-spaces":  func(s string) string { return strings.ReplaceAll(s, "\n", " ") },
		"flat-nospace": func(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ") },
		"crlf":         func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") },
		"tabs-indent":  func(s string) string { return "  \t" + strings.ReplaceAll(s, "\n", "\n\t") },
		"quoted":       func(s string) string { return `"` + strings.ReplaceAll(s, "\n", " ") + `"` },
		"double-space": func(s string) string { return strings.ReplaceAll(s, "\n", "  ") },
	}
	for kname, key := range testKeys(t) {
		for vname, f := range variants {
			t.Run(kname+"/"+vname, func(t *testing.T) {
				in := []byte(f(string(key)))
				s := New(in)
				if s.Kind != PrivateKey {
					t.Fatalf("kind = %v, want key", s.Kind)
				}
				if _, err := ssh.ParsePrivateKey(s.Value); err != nil {
					t.Fatalf("parse: %v\n%s", err, s.Value)
				}
				if kname == "pkcs1-rsa" || kname == "sec1-ecdsa" || kname == "pkcs8-ecdsa" {
					if string(s.Value) != string(key) {
						t.Errorf("normalized PEM differs from canonical")
					}
				}
			})
		}
	}
}

func TestNormalizeEncryptedLegacyPEM(t *testing.T) {
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 1024)
	//nolint:staticcheck // legacy format is exactly what we need to cover
	blk, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey), []byte("pp"), x509.PEMCipherAES128)
	if err != nil {
		t.Fatal(err)
	}
	orig := pem.EncodeToMemory(blk)
	flat := strings.ReplaceAll(string(orig), "\n", " ")
	s := New([]byte(flat))
	if s.Kind != PrivateKey {
		t.Fatal("not detected as key")
	}
	if string(s.Value) != string(orig) {
		t.Fatalf("got\n%s\nwant\n%s", s.Value, orig)
	}
	if _, err := ssh.ParsePrivateKeyWithPassphrase(s.Value, []byte("pp")); err != nil {
		t.Fatal(err)
	}
}

func TestPasswords(t *testing.T) {
	for _, pw := range []string{"", "hunter2", "-----BEGIN nothing", "pa ss\nword", "-----BEGIN PUBLIC KEY----- abc -----END PUBLIC KEY-----"} {
		s := FromString(pw)
		if s.Kind != Password || string(s.Value) != pw {
			t.Errorf("%q: kind=%v", pw, s.Kind)
		}
	}
}

func TestStringDoesNotLeak(t *testing.T) {
	s := FromString("hunter2")
	for _, out := range []string{s.String(), s.GoString()} {
		if strings.Contains(out, "hunter2") {
			t.Fatal("secret leaked via String")
		}
	}
	v := s.Value
	s.Wipe()
	for _, c := range v {
		if c != 0 {
			t.Fatal("not wiped")
		}
	}
}
