// SPDX-License-Identifier: MIT

//go:build darwin && cgo

package keystore

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <stdlib.h>
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>

static CFMutableDictionaryRef vcQuery(const char *service, const char *account) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef s = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef a = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, s);
	CFDictionarySetValue(q, kSecAttrAccount, a);
	CFRelease(s);
	CFRelease(a);
	return q;
}

// vcSet adds or updates the item; data never leaves the process except to
// the Security framework.
static OSStatus vcSet(const char *service, const char *account, const void *data, int len) {
	CFMutableDictionaryRef q = vcQuery(service, account);
	CFDataRef d = CFDataCreate(NULL, data, len);
	CFMutableDictionaryRef upd = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFDictionarySetValue(upd, kSecValueData, d);
	OSStatus st = SecItemUpdate(q, upd);
	if (st == errSecItemNotFound) {
		CFDictionarySetValue(q, kSecValueData, d);
		// readable only while the Mac is unlocked, never synced to other devices
		CFDictionarySetValue(q, kSecAttrAccessible, kSecAttrAccessibleWhenUnlockedThisDeviceOnly);
		st = SecItemAdd(q, NULL);
	}
	CFRelease(upd);
	CFRelease(d);
	CFRelease(q);
	return st;
}

static OSStatus vcGet(const char *service, const char *account, CFDataRef *out) {
	CFMutableDictionaryRef q = vcQuery(service, account);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	OSStatus st = SecItemCopyMatching(q, (CFTypeRef *)out);
	CFRelease(q);
	return st;
}

static OSStatus vcDelete(const char *service, const char *account) {
	CFMutableDictionaryRef q = vcQuery(service, account);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return st;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// macOS Keychain (the login keychain): generic passwords of service
// "vaultcrab". The binary is not signed, so the keychain identifies it by
// its path and hash: a new version asks once for access to existing items.

type keychain struct{}

func open() (Store, error) { return keychain{}, nil }

func (keychain) Name() string { return "macOS Keychain" }

func cstrs(name string) (*C.char, *C.char) { return C.CString(service), C.CString(name) }

func (keychain) Set(name string, data []byte) error {
	s, a := cstrs(name)
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(a))
	var p unsafe.Pointer
	if len(data) > 0 {
		p = unsafe.Pointer(&data[0])
	}
	if st := C.vcSet(s, a, p, C.int(len(data))); st != C.errSecSuccess {
		return fmt.Errorf("keystore: keychain: OSStatus %d", int(st))
	}
	return nil
}

func (keychain) Get(name string) ([]byte, error) {
	s, a := cstrs(name)
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(a))
	var d C.CFDataRef
	st := C.vcGet(s, a, &d)
	if st == C.errSecItemNotFound {
		return nil, ErrNotFound
	}
	if st != C.errSecSuccess {
		return nil, fmt.Errorf("keystore: keychain: OSStatus %d", int(st))
	}
	defer C.CFRelease(C.CFTypeRef(d))
	n := int(C.CFDataGetLength(d))
	src := unsafe.Slice((*byte)(unsafe.Pointer(C.CFDataGetBytePtr(d))), n)
	return append([]byte(nil), src...), nil
}

func (keychain) Delete(name string) error {
	s, a := cstrs(name)
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(a))
	if st := C.vcDelete(s, a); st != C.errSecSuccess && st != C.errSecItemNotFound {
		return fmt.Errorf("keystore: keychain: OSStatus %d", int(st))
	}
	return nil
}
