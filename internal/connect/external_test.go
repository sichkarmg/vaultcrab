// SPDX-License-Identifier: MIT

//go:build !windows

package connect

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

func TestExpand(t *testing.T) {
	h := model.Host{Scheme: model.SMB, User: "bob", Addr: "files", Path: "share/sub"}
	got := Expand([]string{"x", `\\{host}\{path}`, "{url}", "{host}:{port}"}, h)
	want := []string{"x", `\\files\share/sub`, "smb://bob@files:445/share/sub", "files:445"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	f := Expand([]string{"{name}", "{id}", "--env={field:Env}", "{field:missing}"},
		model.Host{Name: "box", ID: "42", Fields: map[string]string{"env": "prod"}})
	if !reflect.DeepEqual(f, []string{"box", "42", "--env=prod", ""}) {
		t.Errorf("fields %v", f)
	}
	v := Expand([]string{"{host}::{port}"}, model.Host{Scheme: model.VNC, Addr: "::1"})
	if v[0] != "[::1]::5900" {
		t.Errorf("ipv6 %v", v)
	}
}

// fakeClient writes a script that records its args and stdin to out.
func fakeClient(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFile(t *testing.T, p string) string {
	t.Helper()
	for i := 0; i < 200; i++ {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			time.Sleep(20 * time.Millisecond)
			b, _ = os.ReadFile(p)
			return string(b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s not written", p)
	return ""
}

func TestLaunchFallbackAndArgs(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	fakeClient(t, dir, "remmina", `echo "$@" > `+out+`; sleep 5`)
	l := &Launcher{
		Specs:     map[model.Scheme]LaunchSpec{model.VNC: {Command: []string{"no-such-viewer", "{host}"}}},
		Fallbacks: map[model.Scheme][][]string{model.VNC: {{"also-missing"}, {"remmina", "-c", "vnc://{host}:{port}"}}},
		LookPath:  func(f string) (string, error) { return exec.LookPath(filepath.Join(dir, f)) },
		Settle:    200 * time.Millisecond,
	}
	h := model.Host{Scheme: model.VNC, Addr: "10.0.0.6", Port: 5901}
	if err := l.Launch(context.Background(), h, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "-c vnc://10.0.0.6:5901" {
		t.Errorf("args %q", got)
	}
}

func TestLaunchNotFound(t *testing.T) {
	l := &Launcher{LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		Fallbacks: map[model.Scheme][][]string{model.SMB: {{"xdg-open", "smb://{host}"}}}}
	err := l.Launch(context.Background(), model.Host{Scheme: model.SMB, Addr: "h"}, nil)
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "xdg-open") || !strings.Contains(err.Error(), "external.smb") {
		t.Fatalf("err %v", err)
	}
	if err := l.Launch(context.Background(), model.Host{Scheme: model.SSH}, nil); err == nil {
		t.Error("ssh must not be launched externally")
	}
}

func TestFallbackAfterImmediateFailure(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	fakeClient(t, dir, "xdg-open", "exit 4")
	fakeClient(t, dir, "caja", `echo "$@" > `+out)
	l := &Launcher{
		Fallbacks: map[model.Scheme][][]string{model.SMB: {{"xdg-open", "smb://{host}/{path}"}, {"caja", "smb://{host}/{path}"}}},
		LookPath:  func(f string) (string, error) { return exec.LookPath(filepath.Join(dir, f)) },
	}
	if err := l.Launch(context.Background(), model.Host{Scheme: model.SMB, Addr: "h", Path: "s"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "smb://h/s" {
		t.Errorf("got %q", got)
	}
}

func TestLaunchFailureStatus(t *testing.T) {
	dir := t.TempDir()
	fakeClient(t, dir, "bad", "exit 3")
	l := &Launcher{Specs: map[model.Scheme]LaunchSpec{model.SMB: {Command: []string{filepath.Join(dir, "bad")}}}}
	err := l.Launch(context.Background(), model.Host{Scheme: model.SMB, Addr: "h"}, nil)
	if err == nil || !strings.Contains(err.Error(), "status 3") {
		t.Fatalf("err %v", err)
	}
}

func TestPasswordViaStdinNotArgv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	fakeClient(t, dir, "viewer", `read pw; echo "args=$* pw=$pw" > `+out)
	l := &Launcher{Specs: map[model.Scheme]LaunchSpec{model.VNC: {Command: []string{filepath.Join(dir, "viewer"), "-autopass", "{host}"}, PasswordStdin: true}}}
	var kept []byte
	err := l.Launch(context.Background(), model.Host{Scheme: model.VNC, Addr: "h"}, func(context.Context) (secret.Secret, error) {
		s := secret.FromString("s3cret")
		kept = s.Value
		return s, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "args=-autopass h pw=s3cret" {
		t.Errorf("got %q", got)
	}
	if string(kept) != "\x00\x00\x00\x00\x00\x00" {
		t.Error("secret not wiped")
	}
}

// {password} written by the user: put into the command line, percent-
// encoded inside a URL; the value of another placeholder is not searched.
func TestPasswordInArgs(t *testing.T) {
	h := model.Host{Scheme: model.SMB, User: "tmp", Addr: "fs", Path: "share", Fields: map[string]string{"x": "{password}"}}
	got := ExpandPassword([]string{"open", "smb://{user}:{password}@{host}/{path}", "-p", "{password}", "{field:x}"}, h, "p@ss/w:rd")
	want := []string{"open", "smb://tmp:p%40ss%2Fw%3Ard@fs/share", "-p", "p@ss/w:rd", "{password}"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	if e := Expand([]string{"-p", "{password}"}, h); e[1] != "{password}" {
		t.Errorf("Expand filled it: %q", e)
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	fakeClient(t, dir, "ftpc", `echo "$@" > `+out)
	l := &Launcher{Specs: map[model.Scheme]LaunchSpec{model.FTP: {Command: []string{filepath.Join(dir, "ftpc"), "ftp://{user}:{password}@{host}"}}}}
	err := l.Launch(context.Background(), model.Host{Scheme: model.FTP, User: "bob", Addr: "h"}, func(context.Context) (secret.Secret, error) {
		return secret.FromString("s 3"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "ftp://bob:s%203@h" {
		t.Errorf("started with %q", got)
	}
}

// Every external scheme has auto-detected clients on every OS, and the
// default RDP/FTP/SFTP command lines expand as expected.
func TestDefaultFallbacksNewSchemes(t *testing.T) {
	for _, sc := range model.Schemes {
		if sc.External() && len(DefaultFallbacks()[sc]) == 0 {
			t.Errorf("no fallbacks for %s", sc)
		}
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	fakeClient(t, dir, "xfreerdp", `echo "$@" > `+out+`; sleep 5`)
	fakeClient(t, dir, "filezilla", `echo "$@" >> `+out+`; sleep 5`)
	l := &Launcher{
		Fallbacks: DefaultFallbacks(),
		LookPath:  func(f string) (string, error) { return exec.LookPath(filepath.Join(dir, f)) },
		Settle:    200 * time.Millisecond,
	}
	if err := l.Launch(context.Background(), model.Host{Scheme: model.RDP, User: "admin", Addr: "win", Port: 3389}, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "/v:win:3389 /u:admin" {
		t.Errorf("rdp args %q", got)
	}
	os.Remove(out)
	if err := l.Launch(context.Background(), model.Host{Scheme: model.SFTP, User: "bob", Addr: "srv", Port: 22, Path: "home"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "sftp://bob@srv:22/home" {
		t.Errorf("sftp args %q", got)
	}
}

// A start with WithSpec uses that client instead of the configured one
// (the test start of a setting not saved yet); others are not affected.
func TestLaunchWithSpec(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	cfgd := fakeClient(t, dir, "configured", "echo configured \"$@\" >> "+out)
	draft := fakeClient(t, dir, "draft", "echo draft \"$@\" >> "+out)
	l := &Launcher{Specs: map[model.Scheme]LaunchSpec{model.SMB: {Command: []string{cfgd, "{host}"}}}, Settle: 50 * time.Millisecond}
	h := model.Host{Scheme: model.SMB, Addr: "fs"}
	ctx := WithSpec(context.Background(), model.SMB, LaunchSpec{Command: []string{draft, "-x", "{host}"}})
	if err := l.Launch(ctx, h, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "draft -x fs" {
		t.Errorf("with the spec: %q", got)
	}
	os.Remove(out)
	l.SetSpecs(map[model.Scheme]LaunchSpec{model.SMB: {Command: []string{cfgd, "new", "{host}"}}})
	if err := l.Launch(context.Background(), h, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(waitFile(t, out)); got != "configured new fs" {
		t.Errorf("after SetSpecs: %q", got)
	}
}
