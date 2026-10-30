// SPDX-License-Identifier: MIT

// Package launchers knows the external clients vaultcrab can start: where the
// well-known ones are installed on each OS, argument presets for them, and
// the checks and the preview of a command line edited in the settings.
package launchers

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
)

// FS is what detection and the checks need from the system; tests replace it.
type FS interface {
	Stat(path string) (fs.FileInfo, error)
	LookPath(name string) (string, error)
	Glob(pattern string) ([]string, error)
	Getenv(key string) string
}

// OS is the real file system and PATH of this machine.
type OS struct{}

func (OS) Stat(p string) (fs.FileInfo, error)    { return os.Stat(p) }
func (OS) LookPath(n string) (string, error)     { return exec.LookPath(n) }
func (OS) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (OS) Getenv(k string) string                { return os.Getenv(k) }

// Preset is a template of arguments for a client (the program is the
// client's path). Password is the VNC password mode it is meant for.
type Preset struct {
	Name     string
	Args     []string
	Password string
}

// Client is a well-known client program.
type Client struct {
	Name   string
	Scheme model.Scheme
	OS     string // linux | windows | darwin
	// Paths are where it is installed: absolute paths (globs allowed;
	// on Windows relative to the Program Files directories) or names
	// looked up in PATH.
	Paths []string
	// Program, if set, is what is started when the client is found
	// (e.g. /usr/bin/open for an application bundle).
	Program string
	Presets []Preset
}

// Found is an installed client.
type Found struct {
	Client *Client
	Path   string // the program to start
}

// vncPresets are the presets of a client that takes "host::port" and,
// optionally, a vncpasswd file or the password on stdin/in argv.
func vncPresets(extra ...Preset) []Preset {
	return append([]Preset{{Name: "sign-in proxy (recommended)", Args: []string{"{host}::{port}"}, Password: connect.VNCProxy}}, extra...)
}

var passfilePreset = Preset{Name: "password file", Args: []string{"-passwd", "{passfile}", "{host}::{port}"}, Password: connect.VNCFile}

// Catalog lists the known clients per OS in order of preference.
var Catalog = []Client{
	// macOS
	{Name: "TigerVNC", Scheme: model.VNC, OS: "darwin",
		Paths:   []string{"/Applications/TigerVNC.app/Contents/MacOS/vncviewer", "/Applications/TigerVNC Viewer*.app/Contents/MacOS/TigerVNC Viewer"},
		Presets: vncPresets(passfilePreset)},
	{Name: "RealVNC Viewer", Scheme: model.VNC, OS: "darwin",
		Paths:   []string{"/Applications/VNC Viewer.app/Contents/MacOS/vncviewer"},
		Presets: vncPresets()},
	{Name: "Screen Sharing", Scheme: model.VNC, OS: "darwin",
		Paths:   []string{"/System/Applications/Utilities/Screen Sharing.app", "/System/Library/CoreServices/Applications/Screen Sharing.app"},
		Program: "/usr/bin/open",
		Presets: []Preset{
			{Name: "sign-in proxy (recommended)", Args: []string{"vnc://{host}:{port}"}, Password: connect.VNCProxy},
			{Name: "password in the URL", Args: []string{"{url}"}, Password: connect.VNCURL},
		}},
	{Name: "Finder", Scheme: model.SMB, OS: "darwin", Paths: []string{"/usr/bin/open"},
		Presets: []Preset{{Name: "open the share", Args: []string{"smb://{host}/{path}"}}}},

	// Linux
	{Name: "TigerVNC", Scheme: model.VNC, OS: "linux", Paths: []string{"xtigervncviewer"},
		Presets: vncPresets(passfilePreset)},
	{Name: "vncviewer", Scheme: model.VNC, OS: "linux", Paths: []string{"vncviewer"},
		Presets: vncPresets(passfilePreset)},
	{Name: "TightVNC", Scheme: model.VNC, OS: "linux", Paths: []string{"xtightvncviewer"},
		Presets: vncPresets(
			Preset{Name: "password on stdin", Args: []string{"-autopass", "{host}::{port}"}, Password: connect.VNCStdin},
			passfilePreset)},
	{Name: "Remmina", Scheme: model.VNC, OS: "linux", Paths: []string{"remmina"},
		Presets: []Preset{{Name: "sign-in proxy (recommended)", Args: []string{"-c", "vnc://{host}:{port}"}, Password: connect.VNCProxy}}},
	{Name: "Vinagre", Scheme: model.VNC, OS: "linux", Paths: []string{"vinagre"}, Presets: vncPresets()},
	{Name: "Nemo", Scheme: model.SMB, OS: "linux", Paths: []string{"nemo"}, Presets: smbURL},
	{Name: "Caja", Scheme: model.SMB, OS: "linux", Paths: []string{"caja"}, Presets: smbURL},
	{Name: "Nautilus", Scheme: model.SMB, OS: "linux", Paths: []string{"nautilus"}, Presets: smbURL},
	{Name: "Thunar", Scheme: model.SMB, OS: "linux", Paths: []string{"thunar"}, Presets: smbURL},
	{Name: "Dolphin", Scheme: model.SMB, OS: "linux", Paths: []string{"dolphin"}, Presets: smbURL},
	{Name: "PCManFM", Scheme: model.SMB, OS: "linux", Paths: []string{"pcmanfm"}, Presets: smbURL},
	{Name: "xdg-open", Scheme: model.SMB, OS: "linux", Paths: []string{"xdg-open"}, Presets: smbURL},

	// Windows (paths relative to Program Files)
	{Name: "TigerVNC", Scheme: model.VNC, OS: "windows", Paths: []string{`TigerVNC\vncviewer.exe`},
		Presets: vncPresets(passfilePreset)},
	{Name: "RealVNC Viewer", Scheme: model.VNC, OS: "windows", Paths: []string{`RealVNC\VNC Viewer\vncviewer.exe`},
		Presets: vncPresets()},
	{Name: "TightVNC", Scheme: model.VNC, OS: "windows", Paths: []string{`TightVNC\tvnviewer.exe`},
		Presets: vncPresets(Preset{Name: "password in the command line", Args: []string{"{host}::{port}", "-password={password}"}, Password: connect.VNCArgv})},
	{Name: "UltraVNC", Scheme: model.VNC, OS: "windows", Paths: []string{`uvnc bvba\UltraVNC\vncviewer.exe`, `UltraVNC\vncviewer.exe`},
		Presets: vncPresets(Preset{Name: "password in the command line", Args: []string{"{host}::{port}", "-password", "{password}"}, Password: connect.VNCArgv})},
	{Name: "Explorer", Scheme: model.SMB, OS: "windows", Paths: []string{"explorer.exe"},
		Presets: []Preset{{Name: "open the share", Args: []string{`\\{host}\{path}`}}}},
}

var smbURL = []Preset{{Name: "open the share", Args: []string{"smb://{host}/{path}"}}}

// programFiles are the Windows directories of installed programs.
func programFiles(f FS) []string {
	var out []string
	for _, d := range []string{f.Getenv("ProgramFiles"), f.Getenv("ProgramFiles(x86)"), `C:\Program Files`, `C:\Program Files (x86)`} {
		if d != "" && !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, d) }) {
			out = append(out, d)
		}
	}
	return out
}

// Detect returns the installed clients for scheme on goos, in catalog order.
func Detect(f FS, goos string, scheme model.Scheme) []Found {
	var out []Found
	for i := range Catalog {
		c := &Catalog[i]
		if c.OS != goos || c.Scheme != scheme {
			continue
		}
		if p, ok := find(f, goos, c); ok {
			if c.Program != "" {
				p = c.Program
			}
			out = append(out, Found{Client: c, Path: p})
		}
	}
	return out
}

func find(f FS, goos string, c *Client) (string, bool) {
	for _, p := range c.Paths {
		var tries []string
		switch {
		case goos == "windows" && strings.Contains(p, `\`):
			for _, d := range programFiles(f) {
				tries = append(tries, strings.TrimRight(d, `\`)+`\`+p)
			}
		case strings.HasPrefix(p, "/"):
			tries = []string{p}
		default:
			if lp, err := f.LookPath(p); err == nil {
				return lp, true
			}
			continue
		}
		for _, t := range tries {
			if strings.ContainsAny(t, "*?[") {
				if m, _ := f.Glob(t); len(m) > 0 {
					return m[0], true
				}
				continue
			}
			if _, err := f.Stat(t); err == nil {
				return t, true
			}
		}
	}
	return "", false
}

// Presets returns the presets of the client whose program is path (the
// detected path or a known file name), nil if it is not known.
func Presets(f FS, goos string, scheme model.Scheme, path string) (*Client, []Preset) {
	for _, d := range Detect(f, goos, scheme) {
		if d.Path == path {
			return d.Client, d.Client.Presets
		}
	}
	base := strings.ToLower(baseName(path))
	for i := range Catalog {
		c := &Catalog[i]
		if c.OS != goos || c.Scheme != scheme || c.Program != "" {
			continue
		}
		for _, p := range c.Paths {
			if strings.ToLower(baseName(p)) == base {
				return c, c.Presets
			}
		}
	}
	return nil, nil
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Kind is what a command line is for: the placeholders it may use differ.
type Kind int

const (
	// KindVNC is external.vnc: also {passfile} (password file) and
	// {password} (password argv).
	KindVNC Kind = iota
	// KindExternal is another external client (smb, rdp, ftp, sftp).
	KindExternal
	// KindScript is the command of a user script.
	KindScript
)

// Placeholders are the placeholders every command line may use.
var Placeholders = []string{"{host}", "{port}", "{user}", "{path}", "{url}", "{name}", "{id}", "{field:NAME}"}

var placeholderRe = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*(?::[^{}\s]*)?\}`)

// CheckArgs reports an unknown placeholder in args, or a password
// placeholder that the password mode does not fill.
func CheckArgs(args []string, kind Kind, mode string) error {
	for i, a := range args {
		for _, p := range placeholderRe.FindAllString(a, -1) {
			switch {
			case strings.HasPrefix(p, "{field:") && len(p) > len("{field:}"):
			case slices.Contains(Placeholders, p):
			case p == "{password}" && kind != KindVNC:
				// put into the command line: ArgvWarning is shown
			case p == "{passfile}" || p == "{password}":
				if kind != KindVNC {
					return fmt.Errorf("argument %d: %s: the password is never put into this command line", i+1, p)
				}
				if want := connect.VNCPlaceholder(mode); p != want {
					return fmt.Errorf("argument %d: %s needs the password mode %s", i+1, p, modeFor(p))
				}
			default:
				known := strings.Join(Placeholders, " ") + " {password}"
				if kind == KindVNC {
					known += " {passfile}"
				}
				return fmt.Errorf("argument %d: unknown placeholder %s (known: %s)", i+1, p, known)
			}
		}
	}
	if p := connect.VNCPlaceholder(mode); kind == KindVNC && p != "" && !slices.ContainsFunc(args, func(a string) bool { return strings.Contains(a, p) }) {
		return fmt.Errorf("the password mode %s needs %s in the arguments", mode, p)
	}
	return nil
}

// ArgvWarning is shown for a command line with the password in it.
const ArgvWarning = "{password} is in the command line: other users of this computer can see it (ps, Task Manager)"

func modeFor(p string) string {
	if p == "{passfile}" {
		return connect.VNCFile
	}
	return connect.VNCArgv
}

// CheckProgram checks the program of a command line on this machine (goos
// is runtime.GOOS): a path must be an executable file, a name must be found
// in PATH. It returns the program that would be started.
func CheckProgram(f FS, goos, prog string) (string, error) {
	if prog == "" {
		return "", errors.New("no program")
	}
	if !strings.ContainsAny(prog, `/\`) {
		p, err := f.LookPath(prog)
		if err != nil {
			return "", fmt.Errorf("%s: not found in PATH", prog)
		}
		return p, nil
	}
	st, err := f.Stat(prog)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("%s: no such file", prog)
	case err != nil:
		return "", err
	case st.IsDir():
		return "", fmt.Errorf("%s: a directory, not a program", prog)
	case goos == "windows":
		ext := strings.ToLower(filepath.Ext(prog))
		if !slices.Contains([]string{".exe", ".com", ".bat", ".cmd"}, ext) {
			return "", fmt.Errorf("%s: not a program (.exe, .com, .bat, .cmd)", prog)
		}
	case st.Mode().Perm()&0o111 == 0:
		return "", fmt.Errorf("%s: not executable (chmod +x)", prog)
	}
	return prog, nil
}

// Masked is what a hidden password is shown as.
const Masked = "******"

// Preview returns the command line as it would be started for h, with the
// password masked. For a VNC client in proxy mode {host}:{port} is the
// local sign-in proxy.
func Preview(cmd []string, kind Kind, mode string, h model.Host) string {
	if len(cmd) == 0 {
		return ""
	}
	if h.Port == 0 {
		h.Port = h.Scheme.DefaultPort()
	}
	const portMark = "\x00port\x00"
	args := slices.Clone(cmd)
	if kind == KindVNC && (mode == connect.VNCProxy || mode == "") {
		h.Addr = "127.0.0.1"
		for i, a := range args {
			args[i] = strings.ReplaceAll(a, "{port}", portMark)
		}
	}
	urlMark := "\x00url\x00"
	if kind == KindVNC && mode == connect.VNCURL {
		for i, a := range args {
			args[i] = strings.ReplaceAll(a, "{url}", urlMark)
		}
	}
	out := connect.Expand(args, h)
	u := url.URL{Scheme: "vnc", Host: net.JoinHostPort(h.Addr, strconv.Itoa(h.Port)), User: url.UserPassword(h.User, "PASSWORD")}
	r := strings.NewReplacer(portMark, "<port>", urlMark, strings.Replace(u.String(), "PASSWORD", Masked, 1),
		"{password}", Masked, "{passfile}", "<temporary vncpasswd file>")
	for i, a := range out {
		out[i] = r.Replace(a)
		if out[i] == "" || strings.ContainsAny(out[i], " \t\"") {
			out[i] = strconv.Quote(out[i])
		}
	}
	return strings.Join(out, " ")
}

// NoFS has no files and an empty PATH: the presets of another OS are
// looked up by the file name of the program only.
type NoFS struct{}

func (NoFS) Stat(string) (fs.FileInfo, error)  { return nil, fs.ErrNotExist }
func (NoFS) LookPath(n string) (string, error) { return "", exec.ErrNotFound }
func (NoFS) Glob(string) ([]string, error)     { return nil, nil }
func (NoFS) Getenv(string) string              { return "" }

// Clients returns the known clients for scheme on goos.
func Clients(goos string, scheme model.Scheme) []*Client {
	var out []*Client
	for i := range Catalog {
		if c := &Catalog[i]; c.OS == goos && c.Scheme == scheme {
			out = append(out, c)
		}
	}
	return out
}

// DefaultPath is the program started for c where it is usually installed
// (for a client that is not detected, e.g. on another OS).
func (c *Client) DefaultPath() string {
	if c.Program != "" {
		return c.Program
	}
	p := c.Paths[0]
	if c.OS == "windows" && strings.Contains(p, `\`) {
		return `C:\Program Files\` + p
	}
	return p
}
