// SPDX-License-Identifier: MIT

// Package quickunlock keeps the master passwords of the vaults for a quick
// unlock with a PIN (and, on macOS, Touch ID in front of it) in the secure
// store of the system (internal/keystore).
//
// Layout (items of the store, JSON):
//
//	"pin"          the PIN key: an X25519 key pair whose private key is
//	               encrypted (AES-256-GCM) with a key derived from the PIN by
//	               argon2id (random salt, 256 MiB, 3 passes); the attempt
//	               counter; the vaults that have a sealed password
//	"vault/<key>"  the master password of a vault sealed to the public key
//	               (NaCl anonymous box), the account it belongs to, when the
//	               master password was last entered and in which boot
//
// The public key lets Vaultcrab store the master password again after every
// unlock with it (a changed password, the refresh after a reboot or after N
// days) without asking for the PIN; only the PIN opens it.
//
// A wrong PIN fails the AES-GCM authentication and counts as an attempt in
// the store; after the limit everything is deleted (the master password is
// needed, and the PIN has to be set again). Someone who copies the store
// can try PINs offline without that limit, slowed only by argon2id: a PIN
// is weaker than the master password.
package quickunlock

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/nacl/box"

	"github.com/sichkarmg/vaultcrab/internal/keystore"
	"github.com/sichkarmg/vaultcrab/internal/secret"
)

// KDF are argon2id parameters.
type KDF struct {
	Time    uint32 `json:"t"`
	MemKiB  uint32 `json:"m"`
	Threads uint8  `json:"p"`
}

// DefaultKDF is used for a new PIN (about 0.5-1 s on a laptop).
var DefaultKDF = KDF{Time: 3, MemKiB: 256 << 10, Threads: 4}

type pinItem struct {
	V        int      `json:"v"`
	KDF      KDF      `json:"kdf"`
	Salt     []byte   `json:"salt"`
	Nonce    []byte   `json:"nonce"`
	Priv     []byte   `json:"priv"` // AES-GCM(argon2id(PIN), X25519 private key)
	Pub      []byte   `json:"pub"`
	Attempts int      `json:"attempts"` // wrong PINs in a row
	Vaults   []string `json:"vaults"`   // keys with a sealed password
}

type vaultItem struct {
	V          int       `json:"v"`
	Sealed     []byte    `json:"sealed"` // box.SealAnonymous(master password, pub)
	Account    string    `json:"account"`
	VerifiedAt time.Time `json:"verified"` // the master password was entered
	Boot       string    `json:"boot"`     // in this boot
}

const (
	pinName     = "pin"
	vaultPrefix = "vault/"
)

// Policy decides when the master password is required again.
type Policy struct {
	MaxAttempts int           // wrong PINs before everything is deleted (0 = 5)
	MaxAge      time.Duration // since the master password was entered (0 = 14 days)
	AfterReboot bool          // also after a restart of the system
	Boot        string        // the current boot (BootID)
	Now         time.Time     // zero = time.Now
	KDF         KDF           // for SetPIN (zero = DefaultKDF)
}

func (p Policy) attempts() int {
	if p.MaxAttempts <= 0 {
		return 5
	}
	return p.MaxAttempts
}

func (p Policy) maxAge() time.Duration {
	if p.MaxAge <= 0 {
		return 14 * 24 * time.Hour
	}
	return p.MaxAge
}

func (p Policy) now() time.Time {
	if p.Now.IsZero() {
		return time.Now()
	}
	return p.Now
}

func (p Policy) kdf() KDF {
	if p.KDF.Time == 0 {
		return DefaultKDF
	}
	return p.KDF
}

var (
	// ErrNoPIN: no PIN is set (or it was deleted).
	ErrNoPIN = errors.New("no PIN is set")
	// ErrNotSealed: the PIN is set but this vault has no stored password yet
	// (it is stored at its next unlock with the master password).
	ErrNotSealed = errors.New("the master password of this vault is not stored yet: unlock it with the master password once")
	// ErrWrongPIN wraps a wrong PIN (WrongPIN tells the attempts left).
	ErrWrongPIN = errors.New("wrong PIN")
	// ErrTooManyAttempts: the limit was reached; the stored passwords and the
	// PIN were deleted.
	ErrTooManyAttempts = errors.New("too many wrong PINs: the stored passwords and the PIN were deleted")
	// ErrMasterRequired wraps the reason the master password is needed now.
	ErrMasterRequired = errors.New("the master password is required")
	// ErrWeakPIN: the PIN does not meet the rules.
	ErrWeakPIN = errors.New("weak PIN")
)

// WrongPIN is a wrong PIN with the attempts left.
type WrongPIN struct{ Left int }

func (e WrongPIN) Error() string {
	return fmt.Sprintf("wrong PIN: %d attempt(s) left", e.Left)
}
func (e WrongPIN) Unwrap() error { return ErrWrongPIN }

// MinPIN is the minimal PIN length.
const MinPIN = 6

// CheckPIN applies the rules: at least MinPIN characters, digits only unless
// alphanumeric; not all the same digit, not a plain run (123456).
func CheckPIN(pin []byte, alphanumeric bool) error {
	if len(pin) < MinPIN {
		return fmt.Errorf("%w: at least %d characters", ErrWeakPIN, MinPIN)
	}
	for _, c := range pin {
		digit := c >= '0' && c <= '9'
		alnum := digit || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !alphanumeric && !digit {
			return fmt.Errorf("%w: digits only (Settings → Security → PIN characters)", ErrWeakPIN)
		}
		if alphanumeric && !alnum {
			return fmt.Errorf("%w: letters and digits only", ErrWeakPIN)
		}
	}
	same, up, down := true, true, true
	for i := 1; i < len(pin); i++ {
		same = same && pin[i] == pin[0]
		up = up && pin[i] == pin[i-1]+1
		down = down && pin[i] == pin[i-1]-1
	}
	if same || up || down {
		return fmt.Errorf("%w: not a repeated digit or a plain sequence", ErrWeakPIN)
	}
	return nil
}

func deriveKey(pin, salt []byte, k KDF) []byte {
	return argon2.IDKey(pin, salt, k.Time, k.MemKiB, k.Threads, 32)
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

// Manager works on one store.
type Manager struct {
	Store keystore.Store
}

func (m Manager) load(name string, v any) error {
	b, err := m.Store.Get(name)
	if err != nil {
		return err
	}
	defer secret.Zero(b)
	return json.Unmarshal(b, v)
}

func (m Manager) save(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	defer secret.Zero(b)
	return m.Store.Set(name, b)
}

// HasPIN reports whether a PIN is set.
func (m Manager) HasPIN() bool {
	var p pinItem
	return m.load(pinName, &p) == nil
}

// SetPIN sets a new PIN (replacing an old one: the stored passwords are
// deleted, they are sealed to the old key). pin is wiped.
func (m Manager) SetPIN(pin []byte, alphanumeric bool, pol Policy) error {
	defer secret.Zero(pin)
	if err := CheckPIN(pin, alphanumeric); err != nil {
		return err
	}
	if err := m.Forget(); err != nil {
		return err
	}
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	defer secret.Zero(priv[:])
	k := pol.kdf()
	it := pinItem{V: 1, KDF: k, Salt: make([]byte, 16), Nonce: make([]byte, 12), Pub: pub[:]}
	if _, err := rand.Read(it.Salt); err != nil {
		return err
	}
	if _, err := rand.Read(it.Nonce); err != nil {
		return err
	}
	key := deriveKey(pin, it.Salt, k)
	defer secret.Zero(key)
	a, err := gcm(key)
	if err != nil {
		return err
	}
	it.Priv = a.Seal(nil, it.Nonce, priv[:], []byte("vaultcrab-pin-v1"))
	return m.save(pinName, it)
}

// Seal stores the master password of vault key (account: the bw account,
// to notice a change) after it was entered and accepted by bw. It needs no
// PIN. password is not wiped (the caller unlocks with it).
func (m Manager) Seal(key, account string, password []byte, pol Policy) error {
	var p pinItem
	if err := m.load(pinName, &p); err != nil {
		if errors.Is(err, keystore.ErrNotFound) {
			return ErrNoPIN
		}
		return err
	}
	if len(p.Pub) != 32 {
		return errors.New("quickunlock: bad PIN key")
	}
	pub := (*[32]byte)(p.Pub)
	sealed, err := box.SealAnonymous(nil, password, pub, rand.Reader)
	if err != nil {
		return err
	}
	if err := m.save(vaultPrefix+key, vaultItem{V: 1, Sealed: sealed, Account: account, VerifiedAt: pol.now(), Boot: pol.Boot}); err != nil {
		return err
	}
	if p.Attempts != 0 || !slices.Contains(p.Vaults, key) {
		p.Attempts = 0 // the master password was entered
		if !slices.Contains(p.Vaults, key) {
			p.Vaults = append(p.Vaults, key)
		}
		return m.save(pinName, p)
	}
	return nil
}

// Ready says whether vault key can be unlocked with the PIN now: nil, or
// ErrNoPIN, ErrNotSealed, or ErrMasterRequired with the reason. It does not
// ask for the PIN.
func (m Manager) Ready(key, account string, pol Policy) error {
	var p pinItem
	if err := m.load(pinName, &p); err != nil {
		if errors.Is(err, keystore.ErrNotFound) {
			return ErrNoPIN
		}
		return err
	}
	var v vaultItem
	if err := m.load(vaultPrefix+key, &v); err != nil {
		if errors.Is(err, keystore.ErrNotFound) {
			return ErrNotSealed
		}
		return err
	}
	switch {
	case account != "" && v.Account != "" && v.Account != account:
		_ = m.Drop(key)
		return fmt.Errorf("%w: the vault is logged in to another account", ErrMasterRequired)
	case pol.AfterReboot && pol.Boot != "" && v.Boot != pol.Boot:
		return fmt.Errorf("%w after a restart of the computer", ErrMasterRequired)
	case pol.now().Sub(v.VerifiedAt) > pol.maxAge():
		return fmt.Errorf("%w every %d days", ErrMasterRequired, int(pol.maxAge().Hours()/24))
	}
	return nil
}

// Open returns the master password of vault key for the PIN (the caller
// wipes it). A wrong PIN counts; at the limit everything is deleted. pin is
// wiped.
func (m Manager) Open(key, account string, pin []byte, pol Policy) ([]byte, error) {
	defer secret.Zero(pin)
	if err := m.Ready(key, account, pol); err != nil {
		return nil, err
	}
	var p pinItem
	if err := m.load(pinName, &p); err != nil {
		return nil, err
	}
	var v vaultItem
	if err := m.load(vaultPrefix+key, &v); err != nil {
		return nil, err
	}
	dk := deriveKey(pin, p.Salt, p.KDF)
	defer secret.Zero(dk)
	a, err := gcm(dk)
	if err != nil {
		return nil, err
	}
	priv, err := a.Open(nil, p.Nonce, p.Priv, []byte("vaultcrab-pin-v1"))
	if err != nil {
		p.Attempts++
		if p.Attempts >= pol.attempts() {
			_ = m.Forget()
			return nil, ErrTooManyAttempts
		}
		if err := m.save(pinName, p); err != nil {
			return nil, err
		}
		return nil, WrongPIN{Left: pol.attempts() - p.Attempts}
	}
	defer secret.Zero(priv)
	if len(priv) != 32 || len(p.Pub) != 32 {
		return nil, errors.New("quickunlock: bad PIN key")
	}
	pw, ok := box.OpenAnonymous(nil, v.Sealed, (*[32]byte)(p.Pub), (*[32]byte)(priv))
	if !ok {
		_ = m.Drop(key)
		return nil, errors.New("quickunlock: the stored password does not open; it was deleted")
	}
	if p.Attempts != 0 {
		p.Attempts = 0
		_ = m.save(pinName, p)
	}
	return pw, nil
}

// CheckPINOnly verifies pin without a vault (change PIN): a wrong PIN counts
// like in Open. pin is wiped.
func (m Manager) CheckPINOnly(pin []byte, pol Policy) error {
	defer secret.Zero(pin)
	var p pinItem
	if err := m.load(pinName, &p); err != nil {
		if errors.Is(err, keystore.ErrNotFound) {
			return ErrNoPIN
		}
		return err
	}
	dk := deriveKey(pin, p.Salt, p.KDF)
	defer secret.Zero(dk)
	a, err := gcm(dk)
	if err != nil {
		return err
	}
	priv, err := a.Open(nil, p.Nonce, p.Priv, []byte("vaultcrab-pin-v1"))
	if err != nil {
		p.Attempts++
		if p.Attempts >= pol.attempts() {
			_ = m.Forget()
			return ErrTooManyAttempts
		}
		_ = m.save(pinName, p)
		return WrongPIN{Left: pol.attempts() - p.Attempts}
	}
	secret.Zero(priv)
	return nil
}

// Drop deletes the stored password of vault key (a logout, a changed master
// password).
func (m Manager) Drop(key string) error {
	if err := m.Store.Delete(vaultPrefix + key); err != nil {
		return err
	}
	var p pinItem
	if m.load(pinName, &p) == nil && slices.Contains(p.Vaults, key) {
		p.Vaults = slices.DeleteFunc(p.Vaults, func(s string) bool { return s == key })
		return m.save(pinName, p)
	}
	return nil
}

// Forget deletes the PIN and every stored password; extra names other
// stored data (Touch ID items) to delete too. It reports nothing when there
// was nothing.
func (m Manager) Forget(extra ...string) error {
	var p pinItem
	var errs []error
	if err := m.load(pinName, &p); err == nil {
		for _, k := range p.Vaults {
			errs = append(errs, m.Store.Delete(vaultPrefix+k))
		}
	}
	for _, n := range extra {
		errs = append(errs, m.Store.Delete(n))
	}
	errs = append(errs, m.Store.Delete(pinName))
	return errors.Join(errs...)
}

// Vaults lists the vault keys with a stored password.
func (m Manager) Vaults() []string {
	var p pinItem
	if m.load(pinName, &p) != nil {
		return nil
	}
	return p.Vaults
}
