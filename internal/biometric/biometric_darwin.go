// SPDX-License-Identifier: MIT

//go:build darwin && cgo

package biometric

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework LocalAuthentication -framework Foundation
#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>
#include <string.h>

#pragma clang diagnostic ignored "-Wdeprecated-declarations"

// vcBioAvailable: 0 = available, else the LAError code.
static int vcBioAvailable(void) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		NSError *err = nil;
		if ([ctx canEvaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics error:&err]) {
			return 0;
		}
		return err ? (int)err.code : -1;
	}
}

// vcBioDomainState copies the domain state (the enrolled fingerprints) to
// out (max n bytes) and returns its length, or -1.
static int vcBioDomainState(void *out, int n) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		NSError *err = nil;
		if (![ctx canEvaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics error:&err]) {
			return -1;
		}
		NSData *d = ctx.evaluatedPolicyDomainState;
		if (d == nil) {
			return -1;
		}
		int len = (int)d.length;
		if (len > n) {
			len = n;
		}
		memcpy(out, d.bytes, len);
		return len;
	}
}

// vcBioAuthenticate shows the prompt and waits: 0 = success, else the
// LAError code. timeoutSec bounds the wait.
static int vcBioAuthenticate(const char *reason, int timeoutSec) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		ctx.localizedFallbackTitle = @""; // no "Enter password": the fallback is the PIN / master password of Vaultcrab
		__block int result = -1;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		NSString *r = [NSString stringWithUTF8String:reason];
		[ctx evaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics
			localizedReason:r
			reply:^(BOOL ok, NSError *err) {
				result = ok ? 0 : (err ? (int)err.code : -1);
				dispatch_semaphore_signal(sem);
			}];
		long w = dispatch_semaphore_wait(sem, dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeoutSec * NSEC_PER_SEC));
		if (w != 0) {
			[ctx invalidate];
			return -2; // timeout
		}
		return result;
	}
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"unsafe"
)

type system struct{}

// LAError codes (LocalAuthentication/LAError.h).
const (
	laAuthenticationFailed = -1
	laUserCancel           = -2
	laUserFallback         = -3
	laSystemCancel         = -4
	laPasscodeNotSet       = -5
	laBiometryNotAvailable = -6
	laBiometryNotEnrolled  = -7
	laBiometryLockout      = -8
	laAppCancel            = -9
)

func mapErr(code int) error {
	switch code {
	case 0:
		return nil
	case laAuthenticationFailed:
		return ErrFailed
	case laUserCancel, laUserFallback, laSystemCancel, laAppCancel, -2000:
		return ErrCanceled
	case laPasscodeNotSet, laBiometryNotAvailable, laBiometryNotEnrolled:
		return ErrUnavailable
	case laBiometryLockout:
		return ErrLockout
	}
	return fmt.Errorf("Touch ID: LocalAuthentication error %d", code)
}

func (system) Available() error { return mapErr(int(C.vcBioAvailable())) }

func (system) DomainState() ([]byte, error) {
	buf := make([]byte, 256)
	n := int(C.vcBioDomainState(unsafe.Pointer(&buf[0]), C.int(len(buf))))
	if n < 0 {
		return nil, ErrUnavailable
	}
	return buf[:n], nil
}

func (system) Authenticate(ctx context.Context, reason string) error {
	if reason == "" {
		return errors.New("Touch ID: empty reason")
	}
	cr := C.CString(reason)
	done := make(chan int, 1)
	go func() {
		// the C call owns cr until it returns (also after ctx ended)
		code := int(C.vcBioAuthenticate(cr, 120))
		C.free(unsafe.Pointer(cr))
		done <- code
	}()
	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrTimeout
		}
		return ctx.Err()
	case code := <-done:
		if code == -2 {
			return ErrTimeout
		}
		return mapErr(code)
	}
}
