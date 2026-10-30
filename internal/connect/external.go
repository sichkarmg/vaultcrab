// SPDX-License-Identifier: MIT

package connect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// LaunchSpec describes how to start an external client for a scheme.
type LaunchSpec struct {
	// Command and arguments with placeholders {host} {port} {user} {path} {url}.
	// {password} puts the password into the command line (seen by ps).
	Command []string
	// PasswordStdin writes the vault password to the program's stdin (for
	// clients that can read it there, e.g. TightVNC's `vncviewer -autopass`).
	PasswordStdin bool
	// Password is how a VNC client gets the password: VNCProxy (default),
	// VNCStdin, VNCFile, VNCURL or VNCArgv.
	Password string
}

// Launcher starts clients for VNC/SMB/FTP/SFTP/RDP hosts as detached processes.
type Launcher struct {
	mu    sync.RWMutex
	Specs map[model.Scheme]LaunchSpec // replaced by SetSpecs while running
	// Fallbacks are tried when the configured program is not installed.
	Fallbacks map[model.Scheme][][]string
	LookPath  func(string) (string, error) // exec.LookPath by default
	// Settle is how long to wait for an immediate failure (default 1.5s).
	Settle time.Duration
}

// DefaultFallbacks are well-known clients per OS.
func DefaultFallbacks() map[model.Scheme][][]string {
	switch runtime.GOOS {
	case "windows":
		return map[model.Scheme][][]string{
			model.VNC: {
				{`C:\Program Files\TightVNC\tvnviewer.exe`, "{host}::{port}"},
				{`C:\Program Files\RealVNC\VNC Viewer\vncviewer.exe`, "{host}::{port}"},
				{`C:\Program Files\uvnc bvba\UltraVNC\vncviewer.exe`, "{host}::{port}"},
				{`C:\Program Files\TigerVNC\vncviewer.exe`, "{host}::{port}"},
			},
			model.SMB: {{"explorer.exe", `\\{host}\{path}`}},
			model.RDP: {{"mstsc.exe", "/v:{host}:{port}"}},
			model.FTP: {
				{`C:\Program Files (x86)\WinSCP\WinSCP.exe`, "{url}"},
				{`C:\Program Files\WinSCP\WinSCP.exe`, "{url}"},
				{`C:\Program Files\FileZilla FTP Client\filezilla.exe`, "{url}"},
				{"explorer.exe", "{url}"},
			},
			model.SFTP: {
				{`C:\Program Files (x86)\WinSCP\WinSCP.exe`, "{url}"},
				{`C:\Program Files\WinSCP\WinSCP.exe`, "{url}"},
				{`C:\Program Files\FileZilla FTP Client\filezilla.exe`, "{url}"},
			},
		}
	case "darwin":
		return map[model.Scheme][][]string{
			model.VNC: {{"open", "vnc://{host}:{port}"}},
			model.SMB: {{"open", "smb://{host}/{path}"}},
			// Microsoft "Windows App" / Remote Desktop handles rdp:// URLs.
			model.RDP: {{"open", "rdp://full%20address=s:{host}:{port}&username=s:{user}"}},
			model.FTP: {
				{"/Applications/FileZilla.app/Contents/MacOS/filezilla", "{url}"},
				{"open", "{url}"},
			},
			model.SFTP: {
				{"/Applications/FileZilla.app/Contents/MacOS/filezilla", "{url}"},
				{"open", "{url}"}, // Cyberduck, Transmit, ...
			},
		}
	}
	fileManagers := func(url string) [][]string {
		var out [][]string
		for _, fm := range []string{"nemo", "caja", "nautilus", "thunar", "dolphin", "pcmanfm"} {
			out = append(out, []string{fm, url})
		}
		return out
	}
	return map[model.Scheme][][]string{
		model.VNC: {
			{"vncviewer", "{host}::{port}"},
			{"xtigervncviewer", "{host}::{port}"},
			{"remmina", "-c", "vnc://{host}:{port}"},
			{"vinagre", "{host}::{port}"},
			{"xdg-open", "vnc://{host}:{port}"},
		},
		// File managers first: `xdg-open smb://` falls back to `gio open`,
		// which fails for unmounted shares on desktops without an smb
		// handler (e.g. MATE on Linux Mint).
		model.SMB: append(fileManagers("smb://{host}/{path}"), []string{"xdg-open", "smb://{host}/{path}"}),
		model.RDP: {
			{"xfreerdp3", "/v:{host}:{port}", "/u:{user}"},
			{"xfreerdp", "/v:{host}:{port}", "/u:{user}"},
			{"remmina", "-c", "rdp://{user}@{host}:{port}"},
			{"xdg-open", "rdp://{host}:{port}"},
		},
		model.FTP: append(append([][]string{{"filezilla", "{url}"}}, fileManagers("{url}")...),
			[]string{"xdg-open", "{url}"}),
		model.SFTP: append(append([][]string{{"filezilla", "{url}"}}, fileManagers("{url}")...),
			[]string{"xdg-open", "{url}"}),
	}
}

// SetSpecs replaces the configured clients (the config was re-read): the
// next start uses them, running clients are not affected.
func (l *Launcher) SetSpecs(specs map[model.Scheme]LaunchSpec) {
	l.mu.Lock()
	l.Specs = specs
	l.mu.Unlock()
}

type specKey struct{ scheme model.Scheme }

// WithSpec makes the starts with ctx use spec for scheme instead of the
// configured client (a test start of a setting that is not saved yet).
func WithSpec(ctx context.Context, scheme model.Scheme, spec LaunchSpec) context.Context {
	return context.WithValue(ctx, specKey{scheme}, spec)
}

// spec returns the client for scheme: the one in ctx, else the configured one.
func (l *Launcher) spec(ctx context.Context, scheme model.Scheme) LaunchSpec {
	if s, ok := ctx.Value(specKey{scheme}).(LaunchSpec); ok {
		return s
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.Specs[scheme]
}

// ErrNotFound is returned when no client program is installed.
var ErrNotFound = errors.New("no client program found")

// Expand substitutes placeholders in args for host h: {host} {port} {user}
// {path} {url} {name} {id} {field:NAME}. {password} is left as it is.
func Expand(args []string, h model.Host) []string { return expand(args, h, nil) }

// ExpandPassword is Expand that also puts the password in place of
// {password}: percent-encoded in an argument that is a URL (scheme://...).
// The command line is then visible to the other users of the machine (ps):
// the user asked for it by writing {password}.
func ExpandPassword(args []string, h model.Host, password string) []string {
	return expand(args, h, &password)
}

// HasPassword reports whether a command line wants the password in it.
func HasPassword(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, "{password}") })
}

func expand(args []string, h model.Host, password *string) []string {
	port := h.Port
	if port == 0 {
		port = h.Scheme.DefaultPort()
	}
	hostPort := net.JoinHostPort(h.Addr, strconv.Itoa(port))
	u := url.URL{Scheme: string(h.Scheme), Host: hostPort, Path: "/" + h.Path}
	if h.User != "" {
		u.User = url.User(h.User)
	}
	host := h.Addr
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	r := strings.NewReplacer("{host}", host, "{port}", strconv.Itoa(port), "{user}", h.User,
		"{path}", h.Path, "{url}", u.String(), "{name}", h.Name, "{id}", h.ID)
	fill := func(a string) string {
		// {field:NAME} is a custom (non-hidden) field of the item.
		a = fieldRe.ReplaceAllStringFunc(a, func(m string) string {
			return h.Fields[strings.ToLower(m[len("{field:"):len(m)-1])]
		})
		return r.Replace(a)
	}
	out := make([]string, len(args))
	for i, a := range args {
		if password == nil || !strings.Contains(a, "{password}") {
			out[i] = fill(a)
			continue
		}
		pw := *password
		if strings.Contains(a, "://") {
			pw = strings.TrimPrefix(url.UserPassword("u", pw).String(), "u:")
		}
		// split first: the value of another placeholder is not searched
		parts := strings.Split(a, "{password}")
		for j := range parts {
			parts[j] = fill(parts[j])
		}
		out[i] = strings.Join(parts, pw)
	}
	return out
}

var fieldRe = regexp.MustCompile(`\{field:[^{}]+\}`)

// candidate is a resolved command line.
type candidate struct {
	argv       []string
	argv0      []string // not expanded yet (the program resolved)
	configured bool     // from the config (not auto-detected)
}

// candidates returns the installed command lines for h: the configured one
// first, then the installed fallbacks.
func (l *Launcher) candidates(h model.Host, spec LaunchSpec) ([]candidate, error) {
	look := l.LookPath
	if look == nil {
		look = exec.LookPath
	}
	var tried []string
	var out []candidate
	add := func(c []string, configured bool) {
		if len(c) == 0 {
			return
		}
		p, err := look(c[0])
		if err != nil {
			tried = append(tried, c[0])
			return
		}
		argv := Expand(c, h)
		argv[0] = p
		argv0 := append([]string{p}, c[1:]...)
		out = append(out, candidate{argv: argv, argv0: argv0, configured: configured})
	}
	add(spec.Command, true)
	if len(out) == 0 {
		for _, c := range l.Fallbacks[h.Scheme] {
			add(c, false)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w for %s (tried: %s); set external.%s.%s in the config",
			ErrNotFound, h.Scheme, strings.Join(tried, ", "), h.Scheme, runtime.GOOS)
	}
	return out, nil
}

// Resolve returns the command line that would be tried first for h.
func (l *Launcher) Resolve(h model.Host) ([]string, error) {
	c, err := l.candidates(h, l.spec(context.Background(), h.Scheme))
	if err != nil {
		return nil, err
	}
	return c[0].argv, nil
}

// Launch starts the client for h and returns once it is running (or failed
// right away). An auto-detected client that fails immediately is followed by
// the next one. secretFn is called only when the spec reads the password
// from stdin.
func (l *Launcher) Launch(ctx context.Context, h model.Host, secretFn func(context.Context) (secret.Secret, error)) error {
	if !h.Scheme.External() {
		return fmt.Errorf("%s hosts are not opened by external programs", h.Scheme)
	}
	spec := l.spec(ctx, h.Scheme)
	cands, err := l.candidates(h, spec)
	if err != nil {
		return err
	}
	var errs []string
	for _, c := range cands {
		argv := c.argv
		if c.configured && HasPassword(c.argv0) {
			sec, err := secretFn(ctx)
			if err != nil {
				return fmt.Errorf("credential: %w", err)
			}
			argv = ExpandPassword(c.argv0, h, string(sec.Value))
			sec.Wipe()
		}
		err := l.start(ctx, argv, spec.PasswordStdin && c.configured, secretFn)
		if err == nil {
			return nil
		}
		errs = append(errs, err.Error())
		if c.configured {
			break
		}
	}
	return errors.New(strings.Join(errs, "; "))
}

func (l *Launcher) start(ctx context.Context, argv []string, pwStdin bool, secretFn func(context.Context) (secret.Secret, error)) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = nil, nil // never write into the TUI
	detach(cmd)
	var stdin io.WriteCloser
	var err error
	if pwStdin {
		if stdin, err = cmd.StdinPipe(); err != nil {
			return err
		}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if stdin != nil {
		sec, err := secretFn(ctx)
		if err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("credential: %w", err)
		}
		_, _ = stdin.Write(append(sec.Value, '\n'))
		sec.Wipe()
		_ = stdin.Close()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	settle := l.Settle
	if settle == 0 {
		settle = 1500 * time.Millisecond
	}
	select {
	case err := <-done:
		// Launchers like xdg-open/open/explorer exit quickly; only a failure
		// status is an error.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() != 0 && !(runtime.GOOS == "windows" && ee.ExitCode() == 1 && isExplorer(argv[0])) {
			return fmt.Errorf("%s exited with status %d", baseName(argv[0]), ee.ExitCode())
		}
		return err
	case <-time.After(settle):
		return nil // still running: success
	case <-ctx.Done():
		return nil
	}
}

// Start runs argv as a detached program (like the external clients). With
// pwStdin the credential from secretFn is written to its stdin.
func (l *Launcher) Start(ctx context.Context, argv []string, pwStdin bool, secretFn func(context.Context) (secret.Secret, error)) error {
	if len(argv) == 0 {
		return errors.New("empty command")
	}
	p, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	argv = append([]string{p}, argv[1:]...)
	return l.start(ctx, argv, pwStdin, secretFn)
}

// explorer.exe returns 1 even on success.
func isExplorer(p string) bool { return strings.EqualFold(baseName(p), "explorer.exe") }

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
