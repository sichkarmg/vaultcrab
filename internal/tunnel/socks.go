// SPDX-License-Identifier: MIT

package tunnel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// SOCKS5 (RFC 1928), the server side of a dynamic tunnel: no
// authentication (the port listens on this computer only by default), the
// CONNECT command, IPv4, IPv6 and domain names (resolved by the far side,
// like ssh -D).

const (
	socksVersion   = 5
	socksNoAuth    = 0
	socksNoMethods = 0xff
	socksConnect   = 1

	socksOK             = 0
	socksFailure        = 1
	socksNetUnreachable = 3
	socksHostUnreach    = 4
	socksRefused        = 5
	socksCmdUnsupported = 7
	socksAddrUnsupport  = 8
)

// socksHandshake reads the greeting and the request of a SOCKS5 client and
// returns the address asked for and a function sending the reply (nil =
// connected). The handshake has 30 s.
func socksHandshake(c net.Conn) (string, func(error), error) {
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	defer c.SetDeadline(time.Time{})
	var hdr [2]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return "", nil, err
	}
	if hdr[0] != socksVersion {
		return "", nil, fmt.Errorf("version %d (only SOCKS5)", hdr[0])
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(c, methods); err != nil {
		return "", nil, err
	}
	if !strings.ContainsRune(string(methods), socksNoAuth) {
		_, _ = c.Write([]byte{socksVersion, socksNoMethods})
		return "", nil, errors.New("the client wants authentication")
	}
	if _, err := c.Write([]byte{socksVersion, socksNoAuth}); err != nil {
		return "", nil, err
	}
	var req [4]byte
	if _, err := io.ReadFull(c, req[:]); err != nil {
		return "", nil, err
	}
	fail := func(code byte) {
		_, _ = c.Write([]byte{socksVersion, code, 0, 1, 0, 0, 0, 0, 0, 0})
	}
	if req[0] != socksVersion {
		return "", nil, fmt.Errorf("request version %d", req[0])
	}
	var host string
	switch req[3] {
	case 1:
		var b [4]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return "", nil, err
		}
		host = net.IP(b[:]).String()
	case 4:
		var b [16]byte
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return "", nil, err
		}
		host = net.IP(b[:]).String()
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return "", nil, err
		}
		b := make([]byte, n[0])
		if _, err := io.ReadFull(c, b); err != nil {
			return "", nil, err
		}
		host = string(b)
	default:
		fail(socksAddrUnsupport)
		return "", nil, fmt.Errorf("address type %d", req[3])
	}
	var pb [2]byte
	if _, err := io.ReadFull(c, pb[:]); err != nil {
		return "", nil, err
	}
	if req[1] != socksConnect {
		fail(socksCmdUnsupported)
		return "", nil, fmt.Errorf("command %d (only CONNECT)", req[1])
	}
	addr := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb[:]))))
	reply := func(err error) {
		if err == nil {
			_, _ = c.Write([]byte{socksVersion, socksOK, 0, 1, 0, 0, 0, 0, 0, 0})
			return
		}
		fail(socksCode(err))
	}
	return addr, reply, nil
}

// socksCode maps a dial error of the far side to a SOCKS5 reply.
func socksCode(err error) byte {
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "refused"):
		return socksRefused
	case strings.Contains(s, "no route"), strings.Contains(s, "unreachable"):
		return socksHostUnreach
	case strings.Contains(s, "network"):
		return socksNetUnreachable
	}
	return socksFailure
}
