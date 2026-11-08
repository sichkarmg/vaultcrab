// SPDX-License-Identifier: MIT

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	osuser "os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/sichkarmg/vaultcrab/internal/config"
	"github.com/sichkarmg/vaultcrab/internal/connect"
	"github.com/sichkarmg/vaultcrab/internal/jump"
	"github.com/sichkarmg/vaultcrab/internal/model"
	"github.com/sichkarmg/vaultcrab/internal/secret"
	"github.com/sichkarmg/vaultcrab/internal/session"
)

// Quick connections through a jump host: a template (jump_templates:) or
// any SSH host of the list as the jump host, and a target typed when
// connecting ([user@]host[:port]) or picked from the list. The session is an
// ordinary SSH session (tabs, splits, reconnect through both hops); what it
// connects to is kept in fields of its pseudo-host, so a clone, a split or
// a restored tab connects the same way.

// JumpTemplate is a template of jump_templates:.
type JumpTemplate = config.JumpTemplate

// Fields of the pseudo-host of a jump session (no secrets).
const (
	FieldVia        = session.FieldVia // the jump host as shown (template or host name)
	fieldJumpVia    = "jump_via"       // JumpSpec.Via
	fieldJumpTarget = "jump_target"    // JumpSpec.Target
	fieldJumpItem   = "jump_item"      // JumpSpec.Item
	fieldJumpCred   = "jump_cred"      // JumpSpec.Cred
)

// JumpSpec says how a jump session connects.
type JumpSpec struct {
	// Via is the jump host: "t:<template name>" or "h:<vault item ID>"
	// (JumpChoice.Key).
	Via string
	// Target is what the user typed: [user@]host[:port].
	Target string
	// Item, instead of Target: a vault host as the target (its own address,
	// user and credential).
	Item string
	// Cred is the credential of a typed target, chosen in the picker: a key
	// reference ("password", "agent", a key file, "vault:<item id>"); ""
	// = the template's key (or the jump host's credential).
	Cred string
}

// JumpSpecOf returns the spec of the pseudo-host of a jump session.
func JumpSpecOf(h model.Host) (JumpSpec, bool) {
	via := h.Fields[fieldJumpVia]
	if via == "" {
		return JumpSpec{}, false
	}
	return JumpSpec{Via: via, Target: h.Fields[fieldJumpTarget], Item: h.Fields[fieldJumpItem], Cred: h.Fields[fieldJumpCred]}, true
}

// IsJump reports whether h is the pseudo-host of a jump session.
func IsJump(h model.Host) bool { return h.Fields[fieldJumpVia] != "" }

// JumpChoice is an entry of the first step of the picker: a template or an
// SSH host of the list.
type JumpChoice struct {
	Key      string // JumpSpec.Via
	Label    string
	Detail   string // where the jump host is
	Template bool
	Recent   bool // used before (the history), best first
}

// JumpChoices returns the templates and the SSH hosts, the ones used most
// and most recently first.
func (s *Service) JumpChoices() []JumpChoice {
	cfg := s.Config()
	var all []JumpChoice
	for _, t := range cfg.JumpTemplates {
		d := "via " + t.Jump
		if h, err := s.findHost(t.Jump, true); err == nil {
			d = "via " + h.Name + " (" + h.Target() + ")"
		}
		all = append(all, JumpChoice{Key: "t:" + t.Name, Label: t.Name, Detail: d, Template: true})
	}
	for _, h := range s.Snapshot().Hosts {
		if h.Scheme == model.SSH {
			all = append(all, JumpChoice{Key: "h:" + h.ID, Label: h.Name, Detail: h.Target()})
		}
	}
	var out []JumpChoice
	for _, k := range s.jumpHist.Jumps(s.now()) {
		if i := slices.IndexFunc(all, func(c JumpChoice) bool { return c.Key == k }); i >= 0 {
			c := all[i]
			c.Recent = true
			out = append(out, c)
			all = slices.Delete(all, i, i+1)
		}
	}
	return append(out, all...)
}

// JumpChoice returns the choice of key (false: the template or host is
// gone).
func (s *Service) JumpChoice(key string) (JumpChoice, bool) {
	for _, c := range s.JumpChoices() {
		if c.Key == key {
			return c, true
		}
	}
	return JumpChoice{}, false
}

// JumpSuggestion is a target used before through a jump host.
type JumpSuggestion struct {
	Spec  JumpSpec
	Label string // as shown
	Count int
	Last  time.Time
}

// JumpTargets returns the targets used through via, best first.
func (s *Service) JumpTargets(via string) []JumpSuggestion {
	var out []JumpSuggestion
	for _, e := range s.jumpHist.Targets(via, s.now()) {
		sp := JumpSpec{Via: via, Target: e.Target, Item: e.Item, Cred: e.Cred}
		label := e.Target
		if e.User != "" {
			label = e.User + "@" + e.Target
		}
		if e.Item != "" {
			h, ok := s.Host(e.Item)
			if !ok {
				continue // deleted from the vault
			}
			sp.Target, label = "", h.Name+" ("+h.Target()+")"
		} else {
			sp.Target = label
		}
		out = append(out, JumpSuggestion{Spec: sp, Label: label, Count: e.Count, Last: e.Last})
	}
	return out
}

// JumpHistoryEnabled reports whether the connections are remembered.
func (s *Service) JumpHistoryEnabled() bool { return s.jumpHist.Enabled() }

// ClearJumpHistory forgets the jump hosts and targets used.
func (s *Service) ClearJumpHistory() error { return s.jumpHist.Clear() }

// SaveJumpTemplates writes the templates to the config file and applies
// them.
func (s *Service) SaveJumpTemplates(ts []JumpTemplate) error {
	cfg := s.Config()
	if cfg.Path == "" {
		return errors.New("no config file")
	}
	if err := config.SaveJumpTemplates(cfg.Path, ts); err != nil {
		return err
	}
	return s.ReloadConfig()
}

// OpenJump opens a session through a jump host and starts connecting.
func (s *Service) OpenJump(spec JumpSpec, cols, rows int) (*Session, error) {
	h, err := s.JumpHost(spec)
	if err != nil {
		return nil, err
	}
	sess, err := s.newSSHSession(h, cols, rows, nil)
	if err != nil {
		return nil, err
	}
	sess.Connect()
	return sess, nil
}

// JumpHost returns the pseudo-host of a jump session (checks the spec).
func (s *Service) JumpHost(spec JumpSpec) (model.Host, error) {
	p, err := s.planJump(spec)
	if err != nil {
		return model.Host{}, err
	}
	t := p.target
	name := t.Addr
	tg, _ := jump.ParseTarget(t.Addr)
	if spec.Item != "" {
		name = p.targetName
	} else {
		name = tg.HostPort()
		if tg.Port == 22 {
			name = tg.Host
		}
	}
	id := "jump:" + spec.Via + "|"
	if spec.Item != "" {
		id += "item:" + spec.Item
	} else {
		id += t.User + "@" + t.Addr
	}
	h := model.Host{ID: id, Name: name, Scheme: model.SSH, User: t.User, Addr: tg.Host, Port: tg.Port,
		Fields: map[string]string{FieldVia: p.viaName, fieldJumpVia: spec.Via}}
	if spec.Item != "" {
		h.Fields[fieldJumpItem] = spec.Item
	} else {
		h.Fields[fieldJumpTarget] = spec.Target
		if spec.Cred != "" {
			h.Fields[fieldJumpCred] = spec.Cred
		}
	}
	return h, nil
}

// keySource is where the key of a hop comes from.
type keySource struct {
	kind  jump.KeyKind
	path  string // KeyFile
	item  string // KeyVault: the item ID
	scope string // KeyPassword: whose typed password (the jump host or template)
	user  string // KeyVault: the user name of the item ("" if none)
}

// jumpPlan is a resolved spec (without credentials).
type jumpPlan struct {
	viaName    string
	targetName string
	jump       connect.Hop
	target     connect.Hop
	jumpKey    keySource
	targetKey  keySource
	mode       connect.HostKeyMode
	histTarget string // host[:port] as remembered
	histUser   string
}

// itemRef is a vault item usable as a credential: a host or a Credential.
type itemRef struct{ id, name, user string }

// findItem returns the host or credential item with ID ref, or the only one
// named ref.
func (s *Service) findItem(ref string) (itemRef, error) {
	ref = strings.TrimSpace(ref)
	snap := s.Snapshot()
	var all []itemRef
	for _, h := range snap.Hosts {
		all = append(all, itemRef{h.ID, h.Name, h.User})
	}
	for _, c := range snap.Credentials {
		all = append(all, itemRef{c.ID, c.Name, c.User})
	}
	var named []itemRef
	for _, it := range all {
		if it.id == ref {
			return it, nil
		}
		if strings.EqualFold(it.name, ref) {
			named = append(named, it)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return itemRef{}, fmt.Errorf("%w %q", errNoItem, ref)
	}
	return itemRef{}, fmt.Errorf("%d vault items are named %q: use the item ID", len(named), ref)
}

func localUser() string {
	u, err := osuser.Current()
	if err != nil {
		return os.Getenv("USER")
	}
	name := u.Username
	if i := strings.LastIndex(name, `\`); i >= 0 {
		name = name[i+1:] // DOMAIN\user on Windows
	}
	return name
}

// errNoItem: no vault item has this name or ID.
var errNoItem = errors.New("no vault item")

// findHost returns the host with ID ref, or the only one named ref (only
// SSH hosts if sshOnly).
func (s *Service) findHost(ref string, sshOnly bool) (model.Host, error) {
	ref = strings.TrimSpace(ref)
	var named []model.Host
	for _, h := range s.Snapshot().Hosts {
		if sshOnly && h.Scheme != model.SSH {
			continue
		}
		if h.ID == ref {
			return h, nil
		}
		if strings.EqualFold(h.Name, ref) {
			named = append(named, h)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return model.Host{}, fmt.Errorf("%w %q", errNoItem, ref)
	}
	return model.Host{}, fmt.Errorf("%d vault items are named %q: use the item ID", len(named), ref)
}

// keyOf resolves a key reference; scope names whose typed password a
// "password" reference is (the template or jump host).
func (s *Service) keyOf(k jump.Key, scope string) (keySource, error) {
	switch k.Kind {
	case jump.KeyFile:
		return keySource{kind: jump.KeyFile, path: k.Value}, nil
	case jump.KeyAgent:
		return keySource{kind: jump.KeyAgent}, nil
	case jump.KeyPassword:
		return keySource{kind: jump.KeyPassword, scope: scope}, nil
	case jump.KeyVault:
		it, err := s.findItem(k.Value)
		if err != nil {
			// any item by its ID, also one that is neither a host nor marked
			if errors.Is(err, errNoItem) && len(k.Value) >= 32 && !strings.ContainsAny(k.Value, " ") {
				return keySource{kind: jump.KeyVault, item: k.Value}, nil
			}
			return keySource{}, err
		}
		return keySource{kind: jump.KeyVault, item: it.id, user: it.user}, nil
	}
	return keySource{}, nil
}

func hostKeyMode(check string) connect.HostKeyMode {
	switch check {
	case config.HostKeyAcceptNew:
		return connect.HostKeyAcceptNew
	case config.HostKeyStrict:
		return connect.HostKeyStrict
	}
	return connect.HostKeyAsk
}

// planJump resolves spec: the jump host, the target, users, ports, where
// the keys come from and the host key check.
func (s *Service) planJump(spec JumpSpec) (*jumpPlan, error) {
	p := &jumpPlan{mode: connect.HostKeyAsk}
	var typed jump.Target
	if spec.Item == "" {
		var err error
		if typed, err = jump.ParseTarget(spec.Target); err != nil {
			return nil, err
		}
	}
	var tpl *JumpTemplate
	kind, ref, _ := strings.Cut(spec.Via, ":")
	switch kind {
	case "t":
		for _, t := range s.Config().JumpTemplates {
			if t.Name == ref {
				tpl = &t
				break
			}
		}
		if tpl == nil {
			return nil, fmt.Errorf("jump template %q no longer exists", ref)
		}
		p.viaName = tpl.Name
		p.mode = hostKeyMode(tpl.HostKeys())
		if h, err := s.findHost(tpl.Jump, true); err == nil {
			p.jump = connect.Hop{Name: h.Name, Addr: sshAddr(h), User: h.User}
			p.jumpKey = keySource{kind: jump.KeyVault, item: h.ID}
		} else if !errors.Is(err, errNoItem) {
			return nil, fmt.Errorf("template %s: jump: %w", tpl.Name, err) // several items with the name
		} else {
			jt, perr := jump.ParseTarget(tpl.Jump)
			if perr != nil {
				return nil, fmt.Errorf("template %s: jump %q: neither an SSH vault item (%v) nor [user@]host[:port] (%v)", tpl.Name, tpl.Jump, err, perr)
			}
			p.jump = connect.Hop{Name: jt.HostPort(), Addr: jt.Addr(22), User: jt.User}
			p.jumpKey = keySource{kind: jump.KeyAgent}
			if p.jump.User == "" {
				p.jump.User = tpl.User
			}
		}
		key, _ := jump.ParseKey(tpl.Key) // checked when the config was loaded
		jk, _ := jump.ParseKey(tpl.JumpKey)
		if jk.Kind != jump.KeyNone {
			ks, err := s.keyOf(jk, spec.Via+"#jump")
			if err != nil {
				return nil, fmt.Errorf("template %s: jump_key: %w", tpl.Name, err)
			}
			p.jumpKey = ks
		} else if p.jumpKey.kind != jump.KeyVault && key.Kind != jump.KeyNone {
			ks, err := s.keyOf(key, spec.Via)
			if err != nil {
				return nil, fmt.Errorf("template %s: key: %w", tpl.Name, err)
			}
			p.jumpKey = ks
		}
		p.targetKey = p.jumpKey
		if key.Kind != jump.KeyNone {
			ks, err := s.keyOf(key, spec.Via)
			if err != nil {
				return nil, fmt.Errorf("template %s: key: %w", tpl.Name, err)
			}
			p.targetKey = ks
		}
	case "h":
		h, ok := s.Host(ref)
		if !ok || h.Scheme != model.SSH {
			return nil, errors.New("the jump host is no longer in the vault")
		}
		p.viaName = h.Name
		p.jump = connect.Hop{Name: h.Name, Addr: sshAddr(h), User: h.User}
		p.jumpKey = keySource{kind: jump.KeyVault, item: h.ID}
		p.targetKey = p.jumpKey
	default:
		return nil, fmt.Errorf("unknown jump host %q", spec.Via)
	}
	if p.jump.User == "" {
		p.jump.User = localUser()
	}
	if spec.Item != "" {
		h, ok := s.Host(spec.Item)
		if !ok {
			return nil, errors.New("the target is no longer in the vault")
		}
		p.targetName = h.Name
		p.target = connect.Hop{Name: h.Name, Addr: sshAddr(h), User: h.User}
		if p.target.User == "" {
			p.target.User = p.jump.User
		}
		p.targetKey = keySource{kind: jump.KeyVault, item: h.ID}
		p.histTarget = sshAddr(h)
		return p, nil
	}
	if spec.Cred != "" {
		// chosen in the picker for this address
		k, err := jump.ParseKey(spec.Cred)
		if err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
		if p.targetKey, err = s.keyOf(k, spec.Via); err != nil {
			return nil, fmt.Errorf("credential: %w", err)
		}
	}
	port := typed.Port
	if port == 0 && tpl != nil {
		port = tpl.Port
	}
	if port == 0 {
		port = 22
	}
	typed.Port = port
	user := typed.User
	if user == "" && tpl != nil {
		user = tpl.User
	}
	if user == "" && p.targetKey.kind == jump.KeyVault && p.targetKey.item != p.jumpKey.item {
		user = p.targetKey.user // the user name of a credential item
	}
	if user == "" {
		user = p.jump.User
	}
	p.target = connect.Hop{Name: typed.HostPort(), Addr: typed.Addr(22), User: user}
	p.targetName = typed.HostPort()
	hp := typed
	hp.User = ""
	if hp.Port == 22 {
		hp.Port = 0
	}
	p.histTarget, p.histUser = hp.HostPort(), typed.User
	return p, nil
}

// jumpDialer connects a jump session: both hops anew on every (re)connect.
func (s *Service) jumpDialer(spec JumpSpec) session.Dialer {
	return func(ctx context.Context, _ model.Host, _ *secret.Secret, cols, rows int) (session.Remote, error) {
		p, err := s.planJump(spec)
		if err != nil {
			return nil, err
		}
		var cleanup []func()
		defer func() {
			for _, f := range cleanup {
				f()
			}
		}()
		auth := func(k keySource, role string, h *connect.Hop) error {
			a, done, desc, err := s.hopAuth(ctx, k, role)
			if done != nil {
				cleanup = append(cleanup, done)
			}
			h.Auth, h.AuthInfo = a, desc
			if err != nil {
				connect.TraceOf(ctx).Add("%s: credential: %v", role, err)
			}
			return err
		}
		if err = auth(p.jumpKey, "jump host "+p.jump.Name, &p.jump); err != nil {
			return nil, fmt.Errorf("jump host %s: %w", p.jump.Name, err)
		}
		if err = auth(p.targetKey, "target "+p.target.Name, &p.target); err != nil {
			return nil, fmt.Errorf("target %s: %w", p.target.Name, err)
		}
		pol := connect.HostKeyPolicy{File: s.opts.SSH.KnownHosts, Mode: p.mode, Ask: s.askHostKey(p.mode)}
		p.jump.Keys, p.target.Keys = pol, pol
		logf("jump: %s → %s@%s", p.jump.Name, p.target.User, p.target.Addr)
		dial := s.opts.JumpDial
		if dial == nil {
			dial = func(ctx context.Context, j, t connect.Hop, cols, rows int, o connect.SSHOptions) (session.Remote, error) {
				c, err := connect.DialJump(ctx, j, t, cols, rows, o)
				if err != nil {
					return nil, err // not a nil *Conn in an interface
				}
				return c, nil
			}
		}
		c, err := dial(ctx, p.jump, p.target, cols, rows, s.opts.SSH)
		if err != nil {
			// a typed password that was refused is asked again next time
			var je *connect.JumpError
			if errors.As(err, &je) && je.Stage == connect.StageAuth {
				k := p.jumpKey
				if je.Target {
					k = p.targetKey
				}
				if k.kind == jump.KeyPassword {
					s.forgetPassword(k.scope)
				}
			}
			return nil, err
		}
		if err := s.jumpHist.Add(spec.Via, p.histTarget, p.histUser, spec.Item, spec.Cred, s.now()); err != nil {
			logf("jump: history: %v", err)
		}
		return c, nil
	}
}

// hopAuth returns the authentication of a hop and a function releasing
// what it holds (a vault credential is wiped, the agent disconnected).
func (s *Service) hopAuth(ctx context.Context, k keySource, role string) ([]ssh.AuthMethod, func(), string, error) {
	switch k.kind {
	case jump.KeyVault:
		name := k.item
		if it, err := s.findItem(k.item); err == nil {
			name = it.name
		}
		sec, err := s.Secret(ctx, k.item)
		if err != nil {
			return nil, nil, "", fmt.Errorf("credential: %w", err)
		}
		desc := connect.DescribeSecret(&sec) + " of the vault item " + name
		a, err := connect.AuthFromSecret(&sec)
		if err != nil {
			sec.Wipe()
			return nil, nil, desc, err
		}
		return a, sec.Wipe, desc, nil
	case jump.KeyFile:
		signer, err := s.keyFile(ctx, k.path, role)
		if err != nil {
			return nil, nil, "key file " + k.path, err
		}
		return []ssh.AuthMethod{connect.SignerAuth(signer)}, nil, "public key " + connect.DescribeKey(signer.PublicKey()) + " from " + k.path, nil
	case jump.KeyAgent:
		a, done, err := connect.Agent()
		if err != nil {
			return nil, nil, "ssh-agent", err
		}
		return []ssh.AuthMethod{a}, done, "keys of the ssh-agent", nil
	case jump.KeyPassword:
		pw, err := s.typedPassword(ctx, k.scope, role)
		if err != nil {
			return nil, nil, "typed password", err
		}
		sec := secret.Secret{Kind: secret.Password, Value: pw} // a copy: wiped after the handshake
		a, err := connect.AuthFromSecret(&sec)
		if err != nil {
			sec.Wipe()
			return nil, nil, "typed password", err
		}
		return a, sec.Wipe, "password typed in Vaultcrab", nil
	}
	return nil, nil, "", errors.New("no key: set key in the template")
}

// keyFiles keeps the keys read from files for the run of the program, so
// a passphrase is asked once (the keys never go to disk).
type keyFiles struct {
	mu sync.Mutex
	m  map[string]keyFile
	pw map[string][]byte // typed passwords by scope (KeyPassword)
}

// typedPassword returns a copy of the password typed for scope, asking for
// it the first time; it is kept in memory until Vaultcrab exits (or bw
// refuses it, see forgetPassword).
func (s *Service) typedPassword(ctx context.Context, scope, role string) ([]byte, error) {
	s.keys.mu.Lock()
	pw, ok := s.keys.pw[scope]
	s.keys.mu.Unlock()
	if ok {
		return slices.Clone(pw), nil
	}
	via := scope
	if c, ok := s.JumpChoice(strings.TrimSuffix(scope, "#jump")); ok {
		via = c.Label
	}
	r, err := s.ask(ctx, &Prompt{Kind: PromptPassphrase, Title: "SSH password",
		Text: fmt.Sprintf("The password for the %s.\nKept in memory until Vaultcrab exits for every address typed through %s; never written to disk.", role, via)})
	if err != nil {
		return nil, err
	}
	if len(r.secret) == 0 {
		return nil, errors.New("no password typed")
	}
	s.keys.mu.Lock()
	if s.keys.pw == nil {
		s.keys.pw = map[string][]byte{}
	}
	s.keys.pw[scope] = r.secret
	s.keys.mu.Unlock()
	return slices.Clone(r.secret), nil
}

// forgetPassword wipes the typed password of scope (it was refused).
func (s *Service) forgetPassword(scope string) {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	if pw, ok := s.keys.pw[scope]; ok {
		secret.Zero(pw)
		delete(s.keys.pw, scope)
	}
}

type keyFile struct {
	signer ssh.Signer
	mod    time.Time
	size   int64
}

// keyFile returns the key in path, asking for its passphrase (up to three
// times) if it has one and was not read before.
func (s *Service) keyFile(ctx context.Context, path, role string) (ssh.Signer, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("key file: %w", err)
	}
	s.keys.mu.Lock()
	k, ok := s.keys.m[path]
	s.keys.mu.Unlock()
	if ok && k.mod.Equal(st.ModTime()) && k.size == st.Size() {
		return k.signer, nil
	}
	warn := ""
	for try := 0; ; try++ {
		signer, err := connect.SignerFromFile(path, func() ([]byte, error) {
			r, err := s.ask(ctx, &Prompt{Kind: PromptPassphrase, Title: "Key passphrase",
				Text:    fmt.Sprintf("The key %s (%s) is protected with a passphrase.\nIt is kept in memory until Vaultcrab exits, never written to disk.", path, role),
				Warning: warn})
			return r.secret, err
		})
		if errors.Is(err, connect.ErrPassphrase) && try < 2 {
			warn = "Wrong passphrase, try again."
			continue
		}
		if err != nil {
			return nil, err
		}
		s.keys.mu.Lock()
		if s.keys.m == nil {
			s.keys.m = map[string]keyFile{}
		}
		s.keys.m[path] = keyFile{signer: signer, mod: st.ModTime(), size: st.Size()}
		s.keys.mu.Unlock()
		return signer, nil
	}
}

// askHostKey asks the user about a new host key (ask mode).
func (s *Service) askHostKey(mode connect.HostKeyMode) func(context.Context, connect.HostKeyQuestion) (connect.HostKeyAnswer, error) {
	if mode != connect.HostKeyAsk {
		return nil
	}
	return func(ctx context.Context, q connect.HostKeyQuestion) (connect.HostKeyAnswer, error) {
		name := q.Name
		if name != q.Host {
			name += " (" + q.Host + ")"
		}
		r, err := s.ask(ctx, &Prompt{Kind: PromptHostKey, Title: "New host key", HostKey: q,
			Text: fmt.Sprintf("The %s %s is not in known_hosts.\n%s key %s", q.Role, name, q.KeyType, q.Fingerprint)})
		if err != nil {
			return connect.HostKeyReject, err
		}
		return r.answer, nil
	}
}

// sshAddr returns host:port of an SSH host (port 22 by default).
func sshAddr(h model.Host) string {
	return (jump.Target{Host: h.Addr, Port: h.Port}).Addr(22)
}

// SetJumpHistory turns the history of the jump connections on or off (the
// config file) and applies it.
func (s *Service) SetJumpHistory(on bool) error {
	cfg := s.Config()
	if cfg.Path == "" {
		return errors.New("no config file")
	}
	if err := config.SetJumpHistory(cfg.Path, on); err != nil {
		return err
	}
	return s.ReloadConfig()
}

// For the UI (which uses core only).

// HostKeyAnswer is the user's decision about a new host key.
type HostKeyAnswer = connect.HostKeyAnswer

const (
	HostKeyReject  = connect.HostKeyReject
	HostKeyOnce    = connect.HostKeyOnce
	HostKeyForever = connect.HostKeyForever
)

// FuzzyMatch scores query as a fuzzy subsequence of s (see jump.Match).
func FuzzyMatch(query, s string) (int, bool) { return jump.Match(query, s) }

// NormalizeTarget checks a typed target ([user@]host[:port]) and returns it
// normalized.
func NormalizeTarget(s string) (string, error) {
	t, err := jump.ParseTarget(s)
	if err != nil {
		return "", err
	}
	return t.String(), nil
}

// CredChoice is a credential the picker offers for a typed target.
type CredChoice struct {
	Ref    string // JumpSpec.Cred ("" = the default)
	Label  string
	Detail string
	// AskPath: the user types the path of a key file (Ref is then the path).
	AskPath bool
}

// JumpLastCred returns the credential chosen last for an address typed
// through via (offered again), "" if none.
func (s *Service) JumpLastCred(via string) string { return s.jumpHist.LastCred(via) }

// JumpCreds returns the credentials the picker offers for a target typed
// through via: the default (the template's key or the jump host's
// credential), a password typed once, the ssh-agent, the usual key files,
// another key file, and the vault items marked as credentials and the SSH
// hosts (their credential).
func (s *Service) JumpCreds(via string) []CredChoice {
	out := []CredChoice{{Ref: "", Label: "Default", Detail: s.defaultCred(via)}}
	pw := "typed once, kept in memory until Vaultcrab exits"
	s.keys.mu.Lock()
	if _, ok := s.keys.pw[via]; ok {
		pw = "typed already (in memory)"
	}
	s.keys.mu.Unlock()
	out = append(out, CredChoice{Ref: "password", Label: "Password", Detail: pw},
		CredChoice{Ref: "agent", Label: "ssh-agent", Detail: "keys of the agent (SSH_AUTH_SOCK)"})
	if home, err := os.UserHomeDir(); err == nil {
		for _, n := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
			p := filepath.Join(home, ".ssh", n)
			if _, err := os.Stat(p); err == nil {
				out = append(out, CredChoice{Ref: p, Label: "Key file " + n, Detail: p})
			}
		}
	}
	out = append(out, CredChoice{Label: "Other key file…", Detail: "type the path", AskPath: true})
	snap := s.Snapshot()
	for _, c := range snap.Credentials {
		d := "vault credential"
		if c.User != "" {
			d += ", user " + c.User
		}
		out = append(out, CredChoice{Ref: "vault:" + c.ID, Label: c.Name, Detail: d})
	}
	for _, h := range snap.Hosts {
		if h.Scheme == model.SSH {
			out = append(out, CredChoice{Ref: "vault:" + h.ID, Label: h.Name, Detail: "credential of the SSH item " + h.Target()})
		}
	}
	return out
}

// defaultCred describes the credential of a typed target when none is
// chosen.
func (s *Service) defaultCred(via string) string {
	if name, ok := strings.CutPrefix(via, "t:"); ok {
		for _, t := range s.Config().JumpTemplates {
			if t.Name == name {
				if t.Key != "" {
					return "the template's key: " + s.CredLabel(t.Key)
				}
				break
			}
		}
	}
	return "the credential of the jump host"
}

// CredLabel names a credential reference for the UI.
func (s *Service) CredLabel(ref string) string {
	k, err := jump.ParseKey(ref)
	if err != nil {
		return ref
	}
	switch k.Kind {
	case jump.KeyNone:
		return "default"
	case jump.KeyPassword:
		return "password (typed)"
	case jump.KeyAgent:
		return "ssh-agent"
	case jump.KeyFile:
		return "key " + k.Value
	case jump.KeyVault:
		if it, err := s.findItem(k.Value); err == nil {
			return "vault: " + it.name
		}
		return "vault: " + k.Value + " (not found)"
	}
	return ref
}

// CheckKeyFile checks that path (~ allowed) is a readable file and returns
// the reference to store.
func CheckKeyFile(path string) (string, error) {
	k, err := jump.ParseKey(path)
	if err != nil || k.Kind != jump.KeyFile {
		return "", errors.New("type the path of a private key file")
	}
	st, err := os.Stat(k.Value)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", fmt.Errorf("%s is a directory", k.Value)
	}
	return k.Value, nil
}

// SetConnDebug turns the trace of connection attempts on or off (ssh.debug;
// the settings save it).
func (s *Service) SetConnDebug(on bool) { s.connDebug.Store(on) }
