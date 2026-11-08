// SPDX-License-Identifier: MIT

package keystore

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Linux: the Secret Service API (org.freedesktop.secrets: GNOME Keyring,
// KWallet, KeePassXC) on the session bus, default collection. The secret is
// passed in a "plain" session: over the private session bus of the user, as
// libsecret does without encryption negotiated; it never touches a file of
// Vaultcrab, argv or the environment.

const (
	ssDest       = "org.freedesktop.secrets"
	ssPath       = dbus.ObjectPath("/org/freedesktop/secrets")
	ssService    = "org.freedesktop.Secret.Service"
	ssCollection = "org.freedesktop.Secret.Collection"
	ssItem       = "org.freedesktop.Secret.Item"
	ssPrompt     = "org.freedesktop.Secret.Prompt"
)

// collection is where items are created: the default collection (the login
// keyring). Tests may use the in-memory "session" collection.
var collection = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")

// ssSecret is the Secret struct of the API: (oayays).
type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type secretService struct{}

func open() (Store, error) {
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	defer c.Close()
	var names []string
	if err := c.BusObject().Call("org.freedesktop.DBus.ListActivatableNames", 0).Store(&names); err == nil {
		for _, n := range names {
			if n == ssDest {
				return secretService{}, nil
			}
		}
	}
	var has bool
	if err := c.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, ssDest).Store(&has); err == nil && has {
		return secretService{}, nil
	}
	return nil, fmt.Errorf("%w: no Secret Service (GNOME Keyring, KWallet, KeePassXC) on the session bus", ErrUnsupported)
}

func (secretService) Name() string { return "Secret Service (keyring)" }

// conn opens the bus and a plain session; done closes both.
func conn() (*dbus.Conn, dbus.ObjectPath, func(), error) {
	c, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, "", nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	var out dbus.Variant
	var session dbus.ObjectPath
	if err := c.Object(ssDest, ssPath).Call(ssService+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session); err != nil {
		c.Close()
		return nil, "", nil, fmt.Errorf("keystore: OpenSession: %w", err)
	}
	done := func() {
		c.Object(ssDest, session).Call("org.freedesktop.Secret.Session.Close", 0)
		c.Close()
	}
	return c, session, done, nil
}

func attrs(name string) map[string]string {
	return map[string]string{"application": service, "name": name}
}

// unlock unlocks paths (the user may be asked by the keyring).
func unlock(c *dbus.Conn, paths []dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := c.Object(ssDest, ssPath).Call(ssService+".Unlock", 0, paths).Store(&unlocked, &prompt); err != nil {
		return fmt.Errorf("keystore: Unlock: %w", err)
	}
	return runPrompt(c, prompt)
}

// runPrompt shows a prompt of the keyring and waits for it (at most 2 min).
func runPrompt(c *dbus.Conn, prompt dbus.ObjectPath) error {
	if prompt == "/" || prompt == "" {
		return nil
	}
	if err := c.AddMatchSignal(dbus.WithMatchObjectPath(prompt), dbus.WithMatchInterface(ssPrompt), dbus.WithMatchMember("Completed")); err != nil {
		return err
	}
	ch := make(chan *dbus.Signal, 2)
	c.Signal(ch)
	defer c.RemoveSignal(ch)
	if err := c.Object(ssDest, prompt).Call(ssPrompt+".Prompt", 0, "").Err; err != nil {
		return fmt.Errorf("keystore: Prompt: %w", err)
	}
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case s := <-ch:
			if s.Path != prompt {
				continue
			}
			if len(s.Body) > 0 {
				if dismissed, ok := s.Body[0].(bool); ok && dismissed {
					return errors.New("keystore: the keyring prompt was dismissed")
				}
			}
			return nil
		case <-t.C:
			return errors.New("keystore: no answer to the keyring prompt")
		}
	}
}

func find(c *dbus.Conn, name string) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	if err := c.Object(ssDest, ssPath).Call(ssService+".SearchItems", 0, attrs(name)).Store(&unlocked, &locked); err != nil {
		return nil, fmt.Errorf("keystore: SearchItems: %w", err)
	}
	if len(locked) > 0 {
		if err := unlock(c, locked); err != nil {
			return nil, err
		}
	}
	return append(unlocked, locked...), nil
}

func (secretService) Get(name string) ([]byte, error) {
	c, session, done, err := conn()
	if err != nil {
		return nil, err
	}
	defer done()
	items, err := find(c, name)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	var s ssSecret
	if err := c.Object(ssDest, items[0]).Call(ssItem+".GetSecret", 0, session).Store(&s); err != nil {
		return nil, fmt.Errorf("keystore: GetSecret: %w", err)
	}
	return s.Value, nil
}

func (secretService) Set(name string, data []byte) error {
	c, session, done, err := conn()
	if err != nil {
		return err
	}
	defer done()
	if err := unlock(c, []dbus.ObjectPath{collection}); err != nil {
		return err
	}
	props := map[string]dbus.Variant{
		ssItem + ".Label":      dbus.MakeVariant("Vaultcrab: " + name),
		ssItem + ".Attributes": dbus.MakeVariant(attrs(name)),
	}
	sec := ssSecret{Session: session, Parameters: []byte{}, Value: data, ContentType: "application/octet-stream"}
	var item, prompt dbus.ObjectPath
	if err := c.Object(ssDest, collection).Call(ssCollection+".CreateItem", 0, props, sec, true).Store(&item, &prompt); err != nil {
		if noCollection(err) {
			return fmt.Errorf("%w: the default keyring is not open (a login without password leaves it locked; open it in Passwords and Keys)", ErrUnsupported)
		}
		return fmt.Errorf("keystore: CreateItem: %w", err)
	}
	return runPrompt(c, prompt)
}

// noCollection: the default collection does not answer (gnome-keyring
// with a login keyring that a login without password left closed).
func noCollection(err error) bool {
	var de dbus.Error
	if errors.As(err, &de) {
		switch de.Name {
		case "org.freedesktop.DBus.Error.UnknownMethod", "org.freedesktop.DBus.Error.UnknownInterface",
			"org.freedesktop.DBus.Error.UnknownObject", "org.freedesktop.Secret.Error.NoSuchObject":
			return true
		}
	}
	return strings.Contains(err.Error(), "No such interface")
}

func (secretService) Delete(name string) error {
	c, _, done, err := conn()
	if err != nil {
		return err
	}
	defer done()
	items, err := find(c, name)
	if err != nil {
		return err
	}
	for _, it := range items {
		var prompt dbus.ObjectPath
		if err := c.Object(ssDest, it).Call(ssItem+".Delete", 0).Store(&prompt); err != nil {
			return fmt.Errorf("keystore: Delete: %w", err)
		}
		if err := runPrompt(c, prompt); err != nil {
			return err
		}
	}
	return nil
}
