// SPDX-License-Identifier: MIT

package launchers

import (
	"errors"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/model"
)

// fakeFS is a file system of the given files (path -> mode) with a PATH of
// the given names (name -> path).
type fakeFS struct {
	files map[string]fs.FileMode
	path  map[string]string
	env   map[string]string
}

type fakeInfo struct {
	name string
	mode fs.FileMode
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return 1 }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i fakeInfo) Sys() any           { return nil }

func (f fakeFS) Stat(p string) (fs.FileInfo, error) {
	if m, ok := f.files[p]; ok {
		return fakeInfo{baseName(p), m}, nil
	}
	return nil, fs.ErrNotExist
}

func (f fakeFS) LookPath(n string) (string, error) {
	if p, ok := f.path[n]; ok {
		return p, nil
	}
	return "", errors.New("not found")
}

func (f fakeFS) Glob(pattern string) ([]string, error) {
	var out []string
	for p := range f.files {
		if ok, _ := path.Match(pattern, p); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f fakeFS) Getenv(k string) string { return f.env[k] }

func names(fs []Found) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Client.Name+"="+f.Path)
	}
	return strings.Join(out, ", ")
}

func TestDetect(t *testing.T) {
	mac := fakeFS{files: map[string]fs.FileMode{
		"/Applications/TigerVNC Viewer 1.15.0.app/Contents/MacOS/TigerVNC Viewer": 0o755,
		"/System/Applications/Utilities/Screen Sharing.app":                       fs.ModeDir | 0o755,
		"/usr/bin/open": 0o755,
	}}
	if got := names(Detect(mac, "darwin", model.VNC)); got != "TigerVNC=/Applications/TigerVNC Viewer 1.15.0.app/Contents/MacOS/TigerVNC Viewer, Screen Sharing=/usr/bin/open" {
		t.Errorf("macOS: %s", got)
	}
	mac.files["/Applications/VNC Viewer.app/Contents/MacOS/vncviewer"] = 0o755
	mac.files["/Applications/TigerVNC.app/Contents/MacOS/vncviewer"] = 0o755
	if got := names(Detect(mac, "darwin", model.VNC)); !strings.HasPrefix(got, "TigerVNC=/Applications/TigerVNC.app/Contents/MacOS/vncviewer, RealVNC Viewer=") {
		t.Errorf("macOS, the documented path first: %s", got)
	}

	linux := fakeFS{path: map[string]string{"vncviewer": "/usr/bin/vncviewer", "remmina": "/usr/bin/remmina", "caja": "/usr/bin/caja", "xdg-open": "/usr/bin/xdg-open"}}
	if got := names(Detect(linux, "linux", model.VNC)); got != "vncviewer=/usr/bin/vncviewer, Remmina=/usr/bin/remmina" {
		t.Errorf("linux: %s", got)
	}
	if got := names(Detect(linux, "linux", model.SMB)); got != "Caja=/usr/bin/caja, xdg-open=/usr/bin/xdg-open" {
		t.Errorf("linux smb: %s", got)
	}

	win := fakeFS{
		env: map[string]string{"ProgramFiles": `D:\Apps`, "ProgramFiles(x86)": `C:\Program Files (x86)`},
		files: map[string]fs.FileMode{
			`D:\Apps\TightVNC\tvnviewer.exe`:                          0o755,
			`C:\Program Files (x86)\uvnc bvba\UltraVNC\vncviewer.exe`: 0o755,
			`C:\Program Files\RealVNC\VNC Viewer\vncviewer.exe`:       0o755, // the default directory is tried too
		},
		path: map[string]string{"explorer.exe": `C:\Windows\explorer.exe`},
	}
	if got := names(Detect(win, "windows", model.VNC)); got != `RealVNC Viewer=C:\Program Files\RealVNC\VNC Viewer\vncviewer.exe, TightVNC=D:\Apps\TightVNC\tvnviewer.exe, UltraVNC=C:\Program Files (x86)\uvnc bvba\UltraVNC\vncviewer.exe` {
		t.Errorf("windows: %s", got)
	}
	if got := names(Detect(fakeFS{}, "windows", model.VNC)); got != "" {
		t.Errorf("nothing installed: %s", got)
	}

	// presets of a detected client and of a known file name
	c, p := Presets(win, "windows", model.VNC, `D:\Apps\TightVNC\tvnviewer.exe`)
	if c == nil || c.Name != "TightVNC" || len(p) != 2 || p[1].Password != "argv" {
		t.Errorf("TightVNC presets: %v %v", c, p)
	}
	c, p = Presets(fakeFS{}, "linux", model.VNC, "/opt/tiger/bin/xtigervncviewer")
	if c == nil || c.Name != "TigerVNC" || strings.Join(p[1].Args, " ") != "-passwd {passfile} {host}::{port}" {
		t.Errorf("TigerVNC by name: %v %v", c, p)
	}
	if c, _ := Presets(fakeFS{}, "linux", model.VNC, "/usr/bin/unknown"); c != nil {
		t.Errorf("unknown client: %v", c)
	}
}

func TestCheckArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		kind Kind
		mode string
		err  string
	}{
		{[]string{"{host}::{port}"}, KindVNC, "proxy", ""},
		{[]string{"-passwd", "{passfile}", "{host}::{port}"}, KindVNC, "file", ""},
		{[]string{"{url}"}, KindVNC, "url", ""},
		{[]string{"{host}", "-password={password}"}, KindVNC, "argv", ""},
		{[]string{"--env={field:Env}", "{user}@{host}", "{name}", "{id}", "{path}"}, KindScript, "", ""},
		{[]string{"bash", "-c", "awk '{print $1}' x"}, KindScript, "", ""}, // not a placeholder
		{[]string{"{hots}::{port}"}, KindVNC, "proxy", "argument 1: unknown placeholder {hots}"},
		{[]string{"open", "smb://{user}:{password}@{host}/{path}"}, KindExternal, "", ""},
		{[]string{"sshpass", "-p", "{password}", "ssh", "{user}@{host}"}, KindScript, "", ""},
		{[]string{"{passfile}"}, KindScript, "", "never put"},
		{[]string{"{host}", "{password}"}, KindVNC, "proxy", "argument 2: {password} needs the password mode argv"},
		{[]string{"{passfile}"}, KindVNC, "argv", "{passfile} needs the password mode file"},
		{[]string{"{host}::{port}"}, KindVNC, "file", "the password mode file needs {passfile}"},
		{[]string{"{field:}"}, KindScript, "", "unknown placeholder {field:}"},
	} {
		err := CheckArgs(tc.args, tc.kind, tc.mode)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%q: %v", tc.args, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%q: %v, want %q", tc.args, err, tc.err)
		}
	}
}

func TestCheckProgram(t *testing.T) {
	f := fakeFS{
		files: map[string]fs.FileMode{"/opt/vnc/viewer": 0o755, "/opt/vnc/readme": 0o644, "/opt/vnc": fs.ModeDir | 0o755,
			`C:\VNC\viewer.exe`: 0o666, `C:\VNC\viewer.txt`: 0o666},
		path: map[string]string{"vncviewer": "/usr/bin/vncviewer"},
	}
	for _, tc := range []struct{ goos, prog, want, err string }{
		{"linux", "/opt/vnc/viewer", "/opt/vnc/viewer", ""},
		{"linux", "vncviewer", "/usr/bin/vncviewer", ""},
		{"linux", "remmina", "", "remmina: not found in PATH"},
		{"linux", "/opt/vnc/readme", "", "not executable"},
		{"linux", "/opt/vnc", "", "a directory"},
		{"linux", "/opt/none", "", "no such file"},
		{"linux", "", "", "no program"},
		{"windows", `C:\VNC\viewer.exe`, `C:\VNC\viewer.exe`, ""},
		{"windows", `C:\VNC\viewer.txt`, "", "not a program"},
	} {
		got, err := CheckProgram(f, tc.goos, tc.prog)
		if got != tc.want || (tc.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s %q: %q %v, want %q %q", tc.goos, tc.prog, got, err, tc.want, tc.err)
		}
	}
}

func TestPreview(t *testing.T) {
	h := model.Host{Scheme: model.VNC, Addr: "10.0.0.5", User: "bob", Name: "desk"}
	for _, tc := range []struct {
		cmd  []string
		mode string
		want string
	}{
		{[]string{"/usr/bin/vncviewer", "{host}::{port}"}, "proxy", "/usr/bin/vncviewer 127.0.0.1::<port>"},
		{[]string{"/usr/bin/vncviewer", "{host}::{port}"}, "", "/usr/bin/vncviewer 127.0.0.1::<port>"},
		{[]string{"vncviewer", "-passwd", "{passfile}", "{host}::{port}"}, "file", `vncviewer -passwd "<temporary vncpasswd file>" 10.0.0.5::5900`},
		{[]string{"tvnviewer.exe", "{host}::{port}", "-password={password}"}, "argv", "tvnviewer.exe 10.0.0.5::5900 -password=******"},
		{[]string{"open", "{url}"}, "url", "open vnc://bob:******@10.0.0.5:5900"},
		{[]string{`C:\Program Files\TigerVNC\vncviewer.exe`, "{host}::{port}"}, "stdin", `"C:\\Program Files\\TigerVNC\\vncviewer.exe" 10.0.0.5::5900`},
	} {
		if got := Preview(tc.cmd, KindVNC, tc.mode, h); got != tc.want {
			t.Errorf("%q %s:\n got %s\nwant %s", tc.cmd, tc.mode, got, tc.want)
		}
	}
	smb := model.Host{Scheme: model.SMB, Addr: "files", Path: "share"}
	if got := Preview([]string{"caja", "smb://{host}/{path}"}, KindExternal, "", smb); got != "caja smb://files/share" {
		t.Errorf("smb: %s", got)
	}
	smb.User = "tmp"
	if got := Preview([]string{"open", "smb://{user}:{password}@{host}/{path}"}, KindExternal, "", smb); got != "open smb://tmp:******@files/share" {
		t.Errorf("smb with the password: %s", got)
	}
}
