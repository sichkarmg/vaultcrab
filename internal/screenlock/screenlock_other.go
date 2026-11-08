// SPDX-License-Identifier: MIT

//go:build !linux && !windows && !(darwin && cgo)

package screenlock

import "context"

func watch(context.Context, func()) error { return ErrUnsupported }
