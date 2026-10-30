// SPDX-License-Identifier: MIT

// Package vnc implements the part of the RFB (VNC) protocol vaultcrab needs to
// sign in for an external VNC client: the version and security handshake
// with the server (VNC authentication with the password from the vault) and
// the server side of the handshake towards the local client, which is
// offered no authentication. Everything after the handshake is forwarded
// unchanged. No UI code.
package vnc

import (
	"crypto/des"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Protocol versions vaultcrab speaks (RFC 6143): 3.3, 3.7 and 3.8. Other
// versions are mapped onto these.
const (
	v33 = 3
	v37 = 7
	v38 = 8
)

// Security types.
const (
	secInvalid  = 0
	secNone     = 1
	secVNCAuth  = 2
	secRA2      = 5
	secRA2ne    = 6
	secTight    = 16
	secUltra    = 17
	secTLS      = 18
	secVeNCrypt = 19
	secSASL     = 20
	secMD5      = 21
	secXVP      = 22
	secARD      = 30
	secMSLogon  = 0xfffffffa // -6 as uint32 (UltraVNC MS-Logon II)
	secMSLogon2 = 113
)

var secNames = map[uint32]string{
	secNone:     "None",
	secVNCAuth:  "VNC authentication",
	secRA2:      "RealVNC RA2",
	secRA2ne:    "RealVNC RA2ne",
	secTight:    "Tight",
	secUltra:    "UltraVNC",
	secTLS:      "TLS",
	secVeNCrypt: "VeNCrypt",
	secSASL:     "SASL",
	secMD5:      "MD5 hash",
	secXVP:      "xvp",
	secARD:      "Apple Remote Desktop",
	secMSLogon:  "MS-Logon (UltraVNC)",
	secMSLogon2: "MS-Logon II (UltraVNC)",
}

func secName(t uint32) string {
	if n, ok := secNames[t]; ok {
		return fmt.Sprintf("%s (%d)", n, int32(t))
	}
	return fmt.Sprintf("type %d", int32(t))
}

// UnsupportedError is returned when the server offers only security types
// vaultcrab cannot sign in with (ARD, VeNCrypt, MS-Logon, ...); a client that
// supports them can get the password another way (external.vnc.password).
type UnsupportedError struct {
	Types []uint32
}

func (e *UnsupportedError) Error() string {
	names := make([]string, len(e.Types))
	for i, t := range e.Types {
		names[i] = secName(t)
	}
	return "the VNC server offers only security types vaultcrab cannot sign in with: " + strings.Join(names, ", ") +
		"; set external.vnc.password to stdin, file, url or argv to let the client sign in itself"
}

// AuthError is a refused sign-in (wrong password, too many attempts, ...).
type AuthError struct{ Reason string }

func (e *AuthError) Error() string { return "VNC authentication failed: " + e.Reason }

// ErrNoPassword is returned when the server asks for a password and the
// item has none.
var ErrNoPassword = errors.New("the VNC server asks for a password and the item has none")

// readVersion reads a ProtocolVersion message "RFB xxx.yyy\n" and returns
// the minor version vaultcrab uses for it: 3.8 for 3.8 and later (also Apple's
// 3.889 and RealVNC's 4.x/5.x), 3.7, and 3.3 for everything else (3.4 and
// 3.6 are 3.3 by the specification).
func readVersion(r io.Reader) (int, string, error) {
	var b [12]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, "", fmt.Errorf("reading the protocol version: %w", err)
	}
	s := string(b[:])
	if !strings.HasPrefix(s, "RFB ") || s[7] != '.' || s[11] != '\n' {
		return 0, "", fmt.Errorf("not a VNC server or client (got %q)", strings.TrimSpace(s))
	}
	major, err1 := strconv.Atoi(s[4:7])
	minor, err2 := strconv.Atoi(s[8:11])
	if err1 != nil || err2 != nil || major < 3 {
		return 0, "", fmt.Errorf("unsupported protocol version %q", strings.TrimSpace(s))
	}
	name := strings.TrimSpace(s[4:])
	switch {
	case major > 3 || minor >= 8:
		return v38, name, nil
	case minor == 7:
		return v37, name, nil
	}
	return v33, name, nil
}

func versionMsg(minor int) []byte { return fmt.Appendf(nil, "RFB 003.%03d\n", minor) }

// readReason reads a length-prefixed reason string (failures).
func readReason(r io.Reader) string {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil || n > 4096 {
		return ""
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ServerInfo describes a finished sign-in.
type ServerInfo struct {
	Version  string // as sent by the server, e.g. "003.008"
	Security string // the security type used
}

// SignIn does the version and security handshake with a VNC server on rw,
// with the password for VNC authentication (only the first 8 bytes count,
// as in every VNC implementation). It returns once the server accepted;
// the next message is the client's ClientInit. The caller wipes password.
func SignIn(rw io.ReadWriter, password []byte) (ServerInfo, error) {
	ver, name, err := readVersion(rw)
	if err != nil {
		return ServerInfo{}, err
	}
	info := ServerInfo{Version: name}
	if _, err := rw.Write(versionMsg(ver)); err != nil {
		return info, err
	}
	var typ uint32
	if ver == v33 {
		// the server decides
		if err := binary.Read(rw, binary.BigEndian, &typ); err != nil {
			return info, fmt.Errorf("reading the security type: %w", err)
		}
		switch typ {
		case secInvalid:
			return info, &AuthError{Reason: orDefault(readReason(rw), "the server refused the connection")}
		case secNone, secVNCAuth:
		default:
			return info, &UnsupportedError{Types: []uint32{typ}}
		}
	} else {
		var n uint8
		if err := binary.Read(rw, binary.BigEndian, &n); err != nil {
			return info, fmt.Errorf("reading the security types: %w", err)
		}
		if n == 0 {
			return info, &AuthError{Reason: orDefault(readReason(rw), "the server refused the connection")}
		}
		types := make([]uint8, n)
		if _, err := io.ReadFull(rw, types); err != nil {
			return info, fmt.Errorf("reading the security types: %w", err)
		}
		switch {
		case slices.Contains(types, secNone):
			typ = secNone
		case slices.Contains(types, secVNCAuth):
			typ = secVNCAuth
		default:
			u := &UnsupportedError{}
			for _, t := range types {
				u.Types = append(u.Types, uint32(t))
			}
			return info, u
		}
		if _, err := rw.Write([]byte{byte(typ)}); err != nil {
			return info, err
		}
	}
	info.Security = secNames[typ]
	if typ == secVNCAuth {
		if len(password) == 0 {
			return info, ErrNoPassword
		}
		var challenge [16]byte
		if _, err := io.ReadFull(rw, challenge[:]); err != nil {
			return info, fmt.Errorf("reading the VNC authentication challenge: %w", err)
		}
		resp := Response(password, challenge[:])
		_, err := rw.Write(resp)
		clear(resp)
		if err != nil {
			return info, err
		}
	}
	// SecurityResult: always in 3.8, after VNC authentication in 3.3/3.7
	if ver == v38 || typ == secVNCAuth {
		var res uint32
		if err := binary.Read(rw, binary.BigEndian, &res); err != nil {
			return info, fmt.Errorf("reading the authentication result: %w", err)
		}
		if res != 0 {
			reason := ""
			if ver == v38 {
				reason = readReason(rw)
			}
			if reason == "" {
				reason = "wrong password"
				if res == 2 {
					reason = "too many attempts"
				}
			}
			return info, &AuthError{Reason: reason}
		}
	}
	return info, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Response computes the VNC authentication response to a 16-byte challenge:
// DES with the password (8 bytes, zero padded) as the key, the bits of
// every key byte reversed.
func Response(password, challenge []byte) []byte {
	var key [8]byte
	defer clear(key[:])
	for i := 0; i < 8 && i < len(password); i++ {
		key[i] = reverseBits(password[i])
	}
	c, err := des.NewCipher(key[:])
	if err != nil {
		panic(err) // the key length is fixed
	}
	out := make([]byte, 16)
	c.Encrypt(out[:8], challenge[:8])
	c.Encrypt(out[8:], challenge[8:16])
	return out
}

func reverseBits(b byte) byte {
	var r byte
	for range 8 {
		r = r<<1 | b&1
		b >>= 1
	}
	return r
}

// ClientInfo describes the local client after its handshake.
type ClientInfo struct {
	Version string // as sent by the client
}

// AcceptClient does the server side of the handshake with a local VNC
// client on rw: it offers version 3.8 and no authentication, in the form the
// client's version expects. The next message is the client's ClientInit.
func AcceptClient(rw io.ReadWriter) (ClientInfo, error) {
	if _, err := rw.Write(versionMsg(v38)); err != nil {
		return ClientInfo{}, err
	}
	ver, name, err := readVersion(rw)
	if err != nil {
		return ClientInfo{}, err
	}
	info := ClientInfo{Version: name}
	if ver == v33 {
		// the server decides: None, no SecurityResult
		return info, binary.Write(rw, binary.BigEndian, uint32(secNone))
	}
	if _, err := rw.Write([]byte{1, secNone}); err != nil {
		return info, err
	}
	var choice [1]byte
	if _, err := io.ReadFull(rw, choice[:]); err != nil {
		return info, fmt.Errorf("reading the client's security type: %w", err)
	}
	if choice[0] != secNone {
		return info, fmt.Errorf("the VNC client chose security type %d instead of None", choice[0])
	}
	if ver == v38 {
		return info, binary.Write(rw, binary.BigEndian, uint32(0)) // SecurityResult OK
	}
	return info, nil
}

// PasswdFile returns the content of a VNC password file (vncpasswd format,
// read by `vncviewer -passwd FILE`): the password DES-encrypted with the
// fixed key every VNC implementation uses. It is obfuscation, not
// protection. The caller wipes the result.
func PasswdFile(password []byte) []byte {
	fixed := []byte{23, 82, 107, 6, 35, 78, 88, 7}
	var pw [8]byte
	defer clear(pw[:])
	copy(pw[:], password)
	var key [8]byte
	for i := range key {
		key[i] = reverseBits(fixed[i])
	}
	c, err := des.NewCipher(key[:])
	if err != nil {
		panic(err)
	}
	out := make([]byte, 8)
	c.Encrypt(out, pw[:])
	return out
}
