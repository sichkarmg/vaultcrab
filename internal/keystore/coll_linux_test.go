// SPDX-License-Identifier: MIT

package keystore

import (
	"os"
	"testing"

	"github.com/godbus/dbus/v5"
)

// useTestCollection: VAULTCRAB_KEYSTORE_COLLECTION=session uses the
// in-memory collection of the keyring (a test VM with a locked login
// keyring).
func useTestCollection(t *testing.T) {
	if c := os.Getenv("VAULTCRAB_KEYSTORE_COLLECTION"); c != "" {
		old := collection
		collection = dbus.ObjectPath("/org/freedesktop/secrets/collection/" + c)
		t.Cleanup(func() { collection = old })
	}
}
