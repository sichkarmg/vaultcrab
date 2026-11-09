// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/tunnel"
)

// Tunnels forward a local port through a jump host (a template or any SSH
// host of the list) to a target reachable from it, or run a SOCKS proxy
// there. They use the shared connection to the jump host (jumppool.go), so
// the authentication, the keys and the host key checks are those of the
// jump connections; they run until stopped or the end of the program.

// TunnelTemplate is a saved tunnel (tunnels:).
type TunnelTemplate = config.Tunnel

// TunnelState is the state of a tunnel.
type TunnelState = tunnel.State

const (
	TunnelConnecting   = tunnel.Connecting
	TunnelActive       = tunnel.Active
	TunnelReconnecting = tunnel.Reconnecting
	TunnelFailed       = tunnel.Failed
	TunnelStopped      = tunnel.Stopped
)

// TunnelSpec says what a tunnel forwards.
type TunnelSpec struct {
	// Name of the saved tunnel ("" for one started from the picker).
	Name string
	// Via is the jump host (JumpSpec.Via: "t:<template>" or "h:<item ID>").
	Via    string
	Target string // host:port as seen from the jump host (not for SOCKS)
	SOCKS  bool
	Port   int    // local port (0 = any free one)
	Bind   string // local address ("" = 127.0.0.1)
	Open   string // the URL to open ({port}, {host}); "" = http://{host}:{port}
}

// TunnelInfo is a running tunnel.
type TunnelInfo struct {
	ID      int
	Spec    TunnelSpec
	Via     string // the jump host as shown
	Addr    string // listened on (host:port)
	Port    int
	Note    string // e.g. the requested port was busy
	State   TunnelState
	Err     error
	Since   time.Time
	Retry   time.Time // Reconnecting: the next attempt
	Open    int       // connections open now
	Total   int       // connections so far
	Started time.Time
	// Connects counts the connections made (more than one: connected
	// again after a loss or a restart).
	Connects int
}

// Label names the tunnel: its name, else what it forwards.
func (t TunnelInfo) Label() string {
	if t.Spec.Name != "" {
		return t.Spec.Name
	}
	if t.Spec.SOCKS {
		return "SOCKS via " + t.Via
	}
	return t.Spec.Target
}

// Forward describes the tunnel: "127.0.0.1:18006 → jump → 10.0.0.5:8006".
func (t TunnelInfo) Forward(arrow string) string {
	to := t.Spec.Target
	if t.Spec.SOCKS {
		to = "SOCKS5"
	}
	return t.Addr + " " + arrow + " " + t.Via + " " + arrow + " " + to
}

// Exposed reports a tunnel listening for other computers too.
func (t TunnelInfo) Exposed() bool { return !tunnel.IsLoopback(t.Spec.Bind) }

type tunnelSet struct {
	mu   sync.Mutex
	next int
	list []*tunnelEntry
}

type tunnelEntry struct {
	id      int
	spec    TunnelSpec
	via     string
	t       *tunnel.Tunnel
	started time.Time
}

func (e *tunnelEntry) info() TunnelInfo {
	st := e.t.Status()
	return TunnelInfo{ID: e.id, Spec: e.spec, Via: e.via, Addr: st.Addr, Port: st.Port, Note: st.Note,
		State: st.State, Err: st.Err, Since: st.Since, Retry: st.Retry, Open: st.Open, Total: st.Total, Started: e.started,
		Connects: st.Connects}
}

// StartTunnel listens on the local port and connects in the background.
// A saved tunnel that is running already is returned as it is.
func (s *Service) StartTunnel(spec TunnelSpec) (TunnelInfo, error) {
	if spec.Name != "" {
		if t, ok := s.TunnelByName(spec.Name); ok {
			return t, nil
		}
	}
	if !spec.SOCKS {
		if err := config.CheckHostPort(spec.Target); err != nil {
			return TunnelInfo{}, fmt.Errorf("target: %w", err)
		}
	}
	p, _, err := s.planVia(spec.Via)
	if err != nil {
		return TunnelInfo{}, err
	}
	kind := tunnel.Local
	if spec.SOCKS {
		kind, spec.Target = tunnel.SOCKS, ""
	}
	ctx := s.runCtx()
	connector := func(ctx context.Context) (tunnel.Lease, error) {
		p, _, err := s.planVia(spec.Via) // anew: the template or the host may have changed
		if err != nil {
			return tunnel.Lease{}, err
		}
		c, release, err := s.leaseJump(ctx, p)
		if err != nil {
			return tunnel.Lease{}, err
		}
		return tunnel.Lease{Client: c, Done: c.Done(), Release: release}, nil
	}
	t, err := tunnel.Start(ctx, tunnel.Spec{Kind: kind, Target: spec.Target, Bind: spec.Bind, Port: spec.Port}, connector,
		tunnel.Options{Attempts: s.Config().Tunnel.Attempts, Permanent: permanentTunnelErr, Notify: s.emit, Log: logf,
			Backoff: s.opts.TunnelBackoff})
	if err != nil {
		return TunnelInfo{}, err
	}
	s.tunnels.mu.Lock()
	s.tunnels.next++
	e := &tunnelEntry{id: s.tunnels.next, spec: spec, via: p.viaName, t: t, started: time.Now()}
	s.tunnels.list = append(s.tunnels.list, e)
	s.tunnels.mu.Unlock()
	info := e.info()
	logf("tunnel %s: %s", info.Label(), info.Forward("→"))
	s.emit()
	return info, nil
}

// runCtx is the context of Start (background before it).
func (s *Service) runCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// permanentTunnelErr: errors another attempt cannot fix without the user
// (refused authentication or host key, a missing template, a locked
// vault, a canceled question); connection problems are retried.
func permanentTunnelErr(err error) bool {
	var je *connect.JumpError
	if errors.As(err, &je) {
		return je.Stage == connect.StageAuth || je.Stage == connect.StageHostKey
	}
	var ne net.Error
	return !errors.As(err, &ne)
}

// Tunnels returns the running tunnels, oldest first.
func (s *Service) Tunnels() []TunnelInfo {
	s.tunnels.mu.Lock()
	es := slices.Clone(s.tunnels.list)
	s.tunnels.mu.Unlock()
	out := make([]TunnelInfo, 0, len(es))
	for _, e := range es {
		out = append(out, e.info())
	}
	return out
}

// TunnelByName returns the running saved tunnel name.
func (s *Service) TunnelByName(name string) (TunnelInfo, bool) {
	for _, t := range s.Tunnels() {
		if t.Spec.Name != "" && strings.EqualFold(t.Spec.Name, name) {
			return t, true
		}
	}
	return TunnelInfo{}, false
}

func (s *Service) tunnelEntry(id int) *tunnelEntry {
	s.tunnels.mu.Lock()
	defer s.tunnels.mu.Unlock()
	for _, e := range s.tunnels.list {
		if e.id == id {
			return e
		}
	}
	return nil
}

// StopTunnel closes the port of the tunnel and its connections.
func (s *Service) StopTunnel(id int) {
	e := s.tunnelEntry(id)
	if e == nil {
		return
	}
	s.tunnels.mu.Lock()
	s.tunnels.list = slices.DeleteFunc(s.tunnels.list, func(x *tunnelEntry) bool { return x == e })
	s.tunnels.mu.Unlock()
	e.t.Stop()
	logf("tunnel %s: stopped", e.info().Label())
	s.emit()
}

// RestartTunnel connects the tunnel again now (the port stays).
func (s *Service) RestartTunnel(id int) {
	if e := s.tunnelEntry(id); e != nil {
		e.t.Restart()
	}
}

// StopTunnels stops every tunnel (at the end).
func (s *Service) StopTunnels() {
	s.tunnels.mu.Lock()
	es := s.tunnels.list
	s.tunnels.list = nil
	s.tunnels.mu.Unlock()
	for _, e := range es {
		e.t.Stop()
	}
}

// SavedTunnels returns the tunnels of the config.
func (s *Service) SavedTunnels() []TunnelTemplate { return s.Config().Tunnels }

// SaveTunnels writes the tunnels to the config file and applies them.
// Running tunnels keep going as they were started.
func (s *Service) SaveTunnels(ts []TunnelTemplate) error {
	cfg := s.Config()
	if cfg.Path == "" {
		return errors.New("no config file")
	}
	if err := config.SaveTunnels(cfg.Path, ts); err != nil {
		return err
	}
	return s.ReloadConfig()
}

// TunnelSpecOf resolves a saved tunnel.
func (s *Service) TunnelSpecOf(t TunnelTemplate) (TunnelSpec, error) {
	via, err := s.ResolveJump(t.Jump)
	if err != nil {
		return TunnelSpec{}, fmt.Errorf("tunnel %s: jump: %w", t.Name, err)
	}
	return TunnelSpec{Name: t.Name, Via: via, Target: strings.TrimSpace(t.Target), SOCKS: t.Kind() == config.TunnelSOCKS,
		Port: int(t.LocalPort), Bind: strings.TrimSpace(t.Bind), Open: strings.TrimSpace(t.Open)}, nil
}

// ResolveJump returns the JumpSpec.Via of ref: a jump template (by name)
// or an SSH host of the list (name or ID).
func (s *Service) ResolveJump(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("a jump template or an SSH host is required")
	}
	for _, t := range s.Config().JumpTemplates {
		if strings.EqualFold(t.Name, ref) {
			return "t:" + t.Name, nil
		}
	}
	h, err := s.findHost(ref, true)
	if err != nil {
		if errors.Is(err, errNoItem) {
			return "", fmt.Errorf("%q is neither a jump template nor an SSH host of the list", ref)
		}
		return "", err
	}
	return "h:" + h.ID, nil
}

// JumpRef returns how a saved tunnel names via: the template name, the
// host name (its ID when the name is not unique).
func (s *Service) JumpRef(via string) string {
	kind, ref, _ := strings.Cut(via, ":")
	if kind == "t" {
		return ref
	}
	h, ok := s.Host(ref)
	if !ok {
		return ref
	}
	n := 0
	for _, o := range s.Snapshot().Hosts {
		if o.Scheme == model.SSH && strings.EqualFold(o.Name, h.Name) {
			n++
		}
	}
	if n == 1 {
		for _, t := range s.Config().JumpTemplates {
			if strings.EqualFold(t.Name, h.Name) {
				return h.ID // a template has the same name
			}
		}
		return h.Name
	}
	return h.ID
}

// FreeTunnelPort returns port if it can be listened on at bind, else a
// free one (0: none).
func FreeTunnelPort(bind string, port int) int { return tunnel.FreePort(bind, port) }

// IsLoopback reports whether a tunnel bound to bind is reachable from this
// computer only.
func IsLoopback(bind string) bool { return tunnel.IsLoopback(bind) }

// TunnelURL returns the URL of the tunnel: its open template (default
// http://{host}:{port}) with the port and the host (localhost, or the bind
// address when it is another one) filled in.
func (t TunnelInfo) TunnelURL() string {
	u := t.Spec.Open
	if u == "" {
		u = "http://{host}:{port}"
	}
	host := "localhost"
	if !tunnel.IsLoopback(t.Spec.Bind) {
		host = t.Spec.Bind
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			host = "localhost"
		}
	}
	return strings.NewReplacer("{port}", strconv.Itoa(t.Port), "{host}", host).Replace(u)
}

// LocalAddr is what to connect to: localhost:port (the bind address when
// it is another one).
func (t TunnelInfo) LocalAddr() string {
	host := "localhost"
	if !tunnel.IsLoopback(t.Spec.Bind) {
		if ip := net.ParseIP(t.Spec.Bind); ip == nil || !ip.IsUnspecified() {
			host = t.Spec.Bind
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(t.Port))
}

// OpenTunnel opens the URL of the tunnel in the browser (tunnel.browser,
// else the system default).
func (s *Service) OpenTunnel(id int) (string, error) {
	e := s.tunnelEntry(id)
	if e == nil {
		return "", errors.New("the tunnel is no longer running")
	}
	url := e.info().TunnelURL()
	if s.opts.OpenURL != nil {
		return url, s.opts.OpenURL(url)
	}
	argv := browserArgv(s.Config().Tunnel.Browser, url)
	logf("tunnel: opening %s with %s", url, argv[0])
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		return url, fmt.Errorf("open %s: %w", url, err)
	}
	go func() { _ = cmd.Wait() }()
	return url, nil
}

// browserArgv returns the command opening url: the configured one ({url}
// replaced, added at the end if missing) or the system default.
func browserArgv(conf []string, url string) []string {
	if len(conf) == 0 {
		switch runtime.GOOS {
		case "darwin":
			return []string{"open", url}
		case "windows":
			return []string{"rundll32", "url.dll,FileProtocolHandler", url}
		}
		return []string{"xdg-open", url}
	}
	out := make([]string, 0, len(conf)+1)
	used := false
	for _, a := range conf {
		if strings.Contains(a, "{url}") {
			used = true
		}
		out = append(out, strings.ReplaceAll(a, "{url}", url))
	}
	if !used {
		out = append(out, url)
	}
	return out
}

// SelfSigned reports a URL whose page probably has a self-signed
// certificate (an https URL of a tunnel: Proxmox, routers, iLO, ...).
func SelfSigned(url string) bool { return strings.HasPrefix(strings.ToLower(url), "https://") }

// For the UI (which uses core only).

// LocalPort is local_port of a saved tunnel (0 = auto).
type LocalPort = config.LocalPort

// ParseLocalPort reads a typed local port ("", "auto" or 1..65535).
func ParseLocalPort(s string) (LocalPort, error) { return config.ParseLocalPort(s) }

// DefaultTunnelAttempts is the default of tunnel.attempts.
const DefaultTunnelAttempts = config.DefaultTunnelAttempts

// Tunnel types of a saved tunnel.
const (
	TunnelLocal = config.TunnelLocal
	TunnelSOCKS = config.TunnelSOCKS
)
