// SPDX-License-Identifier: MIT

//go:build !(darwin && cgo)

package biometric

import "context"

type system struct{}

func (system) Available() error                           { return ErrUnsupported }
func (system) DomainState() ([]byte, error)               { return nil, ErrUnsupported }
func (system) Authenticate(context.Context, string) error { return ErrUnsupported }
