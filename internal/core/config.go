// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/launchers"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/scripts"
)

// The configuration is re-read without a restart: after the settings save
// it, when r is pressed in the host list, and when the file changes (its
// modification time and size are checked every ConfigPoll while the
// terminal window has the focus, and at once when it gets it back). What
// applies at once: the external programs, the scripts, the appearance and
// behavior settings; the rest is reported as needing a restart. A config
// that does not load leaves the previous one in use.

// defaultConfigPoll is how often the config file is checked: a stat only,
// no wake-up of the UI unless it changed.
const defaultConfigPoll = 5 * time.Second

// configSettle: a changed config file is loaded only when it has stayed the
// same this long. An editor that saves in place truncates the file first; read
// at that moment it is an empty, valid config that would briefly replace the
// real one.
const configSettle = 100 * time.Millisecond

// ConfigStatus is the outcome of the last reload of the configuration.
type ConfigStatus struct {
	Seq     int      // incremented on every reload, successful or not
	Err     error    // the file does not load: the previous config is in use
	Restart []string // changed sections that apply after a restart
	Path    string
}

type configState struct {
	cfg    config.Config
	stamp  fileStamp
	status ConfigStatus
	active bool          // the terminal has the focus: poll the file
	wake   chan struct{} // the focus came back: check now
}

type fileStamp struct {
	mod  time.Time
	size int64
	ok   bool
}

func stampOf(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{mod: st.ModTime(), size: st.Size(), ok: true}
}

func (s *Service) initConfig() {
	s.conf.wake = make(chan struct{}, 1)
	s.conf.active = true
	s.scripts = s.opts.Scripts
	if s.opts.Config != nil {
		s.conf.cfg = *s.opts.Config
		s.conf.stamp = stampOf(s.opts.Config.Path)
		s.conf.status.Path = s.opts.Config.Path
		if s.scripts == nil {
			s.scripts = s.opts.Config.CompiledScripts()
		}
		if err := applyLog(*s.opts.Config); err != nil {
			s.conf.status.Err = fmt.Errorf("log: %w", err)
		}
	}
	if s.opts.ConfigPoll == 0 {
		s.opts.ConfigPoll = defaultConfigPoll
	}
	if s.opts.FS == nil {
		s.opts.FS = launchers.OS{}
	}
}

// Config returns the configuration in use.
func (s *Service) Config() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conf.cfg
}

// ConfigStatus returns the outcome of the last reload.
func (s *Service) ConfigStatus() ConfigStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conf.status
}

func (s *Service) scriptList() []scripts.Script {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scripts
}

// ReloadConfig re-reads the configuration file and applies it. On an error
// the previous configuration stays in use.
func (s *Service) ReloadConfig() error {
	if s.opts.Config == nil {
		return errors.New("no config file")
	}
	path := s.opts.Config.Path
	stamp := stampOf(path)
	load := s.opts.LoadConfig
	if load == nil {
		load = config.Load
	}
	cfg, err := load(path)
	if !stamp.ok {
		stamp = stampOf(path) // not there a moment ago (replaced by a rename)
	}
	s.mu.Lock()
	s.conf.stamp = stamp
	s.conf.status.Seq++
	s.conf.status.Err = err
	s.conf.status.Restart = nil
	if err == nil {
		s.conf.status.Restart = restartNeeded(s.conf.cfg, cfg)
		s.conf.cfg = cfg
		s.scripts = cfg.CompiledScripts()
	}
	s.mu.Unlock()
	if err == nil {
		s.reconcileVaults(cfg.Vaults)
		s.SetLockPolicy(cfg.Unlock)
		s.jumpHist.Configure(cfg.Jump.HistoryLimit, cfg.Jump.History)
		s.connDebug.Store(cfg.SSH.Debug)
		if lerr := applyLog(cfg); lerr != nil {
			logf("config: log: %v", lerr)
		}
		logf("config: reloaded %s", path)
	} else {
		logf("config: %s does not load, the previous config stays in use: %v", path, err)
	}
	if err == nil && s.opts.ApplyConfig != nil {
		s.opts.ApplyConfig(cfg)
	}
	s.emit()
	return err
}

// SetConfigWatch pauses (false: the terminal window lost the focus) or
// resumes the checks of the config file; resuming checks it at once.
func (s *Service) SetConfigWatch(active bool) {
	s.mu.Lock()
	was := s.conf.active
	s.conf.active = active
	s.mu.Unlock()
	if active && !was {
		select {
		case s.conf.wake <- struct{}{}:
		default:
		}
	}
}

// watchConfig checks the config file for changes until ctx ends.
func (s *Service) watchConfig(ctx context.Context) {
	if s.opts.Config == nil || s.opts.ConfigPoll < 0 {
		return
	}
	t := time.NewTimer(s.opts.ConfigPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.conf.wake:
		}
		s.mu.Lock()
		active, old := s.conf.active, s.conf.stamp
		s.mu.Unlock()
		if !active {
			continue // the timer is set again when the focus comes back
		}
		if st := stampOf(s.opts.Config.Path); st != old && s.configSettled(ctx, st) {
			_ = s.ReloadConfig()
		}
		t.Reset(s.opts.ConfigPoll)
	}
}

// configSettled waits configSettle and reports whether the config file still
// has the stamp st (no write under way) and the watch is still active. If
// not, the next check tries again.
func (s *Service) configSettled(ctx context.Context, st fileStamp) bool {
	t := time.NewTimer(configSettle)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
	}
	s.mu.Lock()
	active := s.conf.active
	s.mu.Unlock()
	return active && stampOf(s.opts.Config.Path) == st
}

// restartNeeded names the changed sections that are not applied while
// running.
func restartNeeded(old, cur config.Config) []string {
	norm := func(c config.Config) config.Config {
		c.External, c.Scripts = nil, nil
		// applied live (the settings dialog)
		c.UI.TabsPosition, c.UI.Accent, c.UI.Selection, c.UI.ShowTree, c.UI.ShowNotes, c.UI.ListWidth, c.UI.TabWidth, c.UI.TabStatus = "", "", "", false, false, 0, 0, ""
		c.UI.RestoreTabs, c.UI.RestoreView = false, false
		c.Terminal.ConfirmPaste = false
		c.Log = config.Log{}
		c.Unlock = config.Unlock{}
		c.Vaults = nil // connected and disconnected while running
		c.Widgets.Show = false
		c.Icons.Mode = ""
		if c.Icons.Set != nil {
			set := map[string]config.Icon{}
			for k, v := range c.Icons.Set {
				if k != config.IconSSH {
					set[k] = v
				}
			}
			c.Icons.Set = set
		}
		return c
	}
	a, b := reflect.ValueOf(norm(old)), reflect.ValueOf(norm(cur))
	var out []string
	for i := 0; i < a.NumField(); i++ {
		f := a.Type().Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
			out = append(out, tag)
		}
	}
	return out
}

// SaveLaunchers saves the changed external programs and script commands
// into the config file and applies them.
func (s *Service) SaveLaunchers(old, l config.Launchers) error {
	if s.opts.Config == nil || s.opts.Config.Path == "" {
		return errors.New("no config file")
	}
	if err := config.SaveLaunchers(s.opts.Config.Path, old, l); err != nil {
		return err
	}
	return s.ReloadConfig()
}

// LaunchSpecs returns the external clients of cfg for this OS.
func LaunchSpecs(cfg config.Config) map[model.Scheme]connect.LaunchSpec {
	out := map[model.Scheme]connect.LaunchSpec{}
	for name, l := range cfg.External {
		out[model.Scheme(name)] = connect.LaunchSpec{Command: l.ForOS(), PasswordStdin: l.PasswordStdin, Password: l.Password}
	}
	return out
}

// --- the External programs page of the settings ---

// DetectClients returns the installed known clients for scheme on this OS.
func (s *Service) DetectClients(scheme model.Scheme) []launchers.Found {
	return launchers.Detect(s.opts.FS, runtime.GOOS, scheme)
}

// ClientPresets returns the known client with the program path on goos and
// its argument presets (nil if it is not a known client).
func (s *Service) ClientPresets(goos string, scheme model.Scheme, path string) (*launchers.Client, []launchers.Preset) {
	if goos != runtime.GOOS {
		return launchers.Presets(launchers.NoFS{}, goos, scheme, path)
	}
	return launchers.Presets(s.opts.FS, goos, scheme, path)
}

// CheckProgram checks that the program exists and is executable here.
func (s *Service) CheckProgram(prog string) (string, error) {
	return launchers.CheckProgram(s.opts.FS, runtime.GOOS, prog)
}

// TestLaunch starts cmd (a setting not saved yet) for h. A VNC client opens
// in a tab: the session is returned. Scripts in terminal mode return their
// session too.
func (s *Service) TestLaunch(ctx context.Context, h model.Host, entry LauncherEntry, cmd []string, mode string, cols, rows int) (*Session, error) {
	if len(cmd) == 0 {
		return nil, errors.New("no command for this OS")
	}
	if entry.Script != nil {
		sc := *entry.Script
		sc.Run = config.PerOS{}
		sc.Run.Set(runtime.GOOS, cmd)
		sc.PasswordStdin = mode == PasswordStdin
		c := config.Config{Scripts: []config.ScriptSpec{sc}}
		return s.RunScript(ctx, h, c.CompiledScripts()[0], cols, rows)
	}
	if h.Scheme != entry.Scheme {
		return nil, fmt.Errorf("%s is not a %s item", h.Name, entry.Scheme)
	}
	spec := connect.LaunchSpec{Command: cmd}
	if entry.Scheme == model.VNC {
		spec.Password = mode
	} else {
		spec.PasswordStdin = mode == PasswordStdin
	}
	if entry.Scheme == model.VNC && s.opts.VNC != nil {
		h.Name += " (test)" // the tab starts the draft also when reopened
		sess, err := s.newSSHSession(h, cols, rows, &spec)
		if err != nil {
			return nil, err
		}
		sess.Connect()
		return sess, nil
	}
	if s.opts.Launch == nil {
		return nil, ErrNoLauncher
	}
	return nil, s.LaunchExternal(connect.WithSpec(ctx, h.Scheme, spec), h)
}

// LauncherEntry is a row of the External programs page: the client of an
// external scheme or the command of a script.
type LauncherEntry struct {
	Scheme model.Scheme       // the external scheme (Script == nil)
	Script *config.ScriptSpec // a user script
}

// Password modes of the External programs page besides the VNC ones.
const (
	PasswordNone  = "none"
	PasswordStdin = "stdin"
)

// Kind returns which placeholders the command of the entry may use.
func (e LauncherEntry) Kind() launchers.Kind {
	switch {
	case e.Script != nil:
		return launchers.KindScript
	case e.Scheme == model.VNC:
		return launchers.KindVNC
	}
	return launchers.KindExternal
}
