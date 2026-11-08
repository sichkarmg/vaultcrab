// SPDX-License-Identifier: MIT

package quickunlock

import "testing"

func BenchmarkDefaultKDF(b *testing.B) {
	salt := make([]byte, 16)
	for range b.N {
		deriveKey([]byte("482913"), salt, DefaultKDF)
	}
}
