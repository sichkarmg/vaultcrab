// SPDX-License-Identifier: MIT

// Package knock sends a port knocking sequence (knockd, fwknop-less
// setups, MikroTik firewall rules): a TCP connection attempt or a UDP
// packet to each port in turn, so that the firewall opens the SSH port for
// this address. Nothing secret is involved: the sequence is a plain field
// of the vault item (or of a jump template).
package knock

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultDelay is the pause after each knock.
const DefaultDelay = 200 * time.Millisecond

// Port is one knock.
type Port struct {
	Port int
	UDP  bool
}

func (p Port) String() string {
	if p.UDP {
		return strconv.Itoa(p.Port) + "/udp"
	}
	return strconv.Itoa(p.Port) + "/tcp"
}

// Sequence is a port knocking sequence.
type Sequence struct {
	Ports []Port
	Delay time.Duration // after each knock (also before connecting)
}

// Empty reports a sequence without knocks.
func (s Sequence) Empty() bool { return len(s.Ports) == 0 }

func (s Sequence) String() string {
	parts := make([]string, len(s.Ports))
	for i, p := range s.Ports {
		parts[i] = p.String()
	}
	return strings.Join(parts, ", ")
}

// Parse reads a sequence: ports separated by commas or spaces, each
// "port", "port/tcp" or "port/udp" (TCP by default); delay is a duration
// ("300ms", "1s") or milliseconds ("300"), empty for DefaultDelay.
func Parse(seq, delay string) (Sequence, error) {
	s := Sequence{Delay: DefaultDelay}
	for _, f := range strings.FieldsFunc(seq, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\t' }) {
		p := Port{}
		num, proto, ok := strings.Cut(strings.ToLower(f), "/")
		if ok {
			switch proto {
			case "tcp":
			case "udp":
				p.UDP = true
			default:
				return Sequence{}, fmt.Errorf("knock %q: want port, port/tcp or port/udp", f)
			}
		}
		n, err := strconv.Atoi(num)
		if err != nil || n < 1 || n > 65535 {
			return Sequence{}, fmt.Errorf("knock %q: port 1..65535", f)
		}
		p.Port = n
		s.Ports = append(s.Ports, p)
	}
	if d := strings.TrimSpace(delay); d != "" {
		v, err := time.ParseDuration(d)
		if err != nil {
			ms, err2 := strconv.Atoi(d)
			if err2 != nil {
				return Sequence{}, fmt.Errorf("knock_delay %q: want a duration such as 300ms", d)
			}
			v = time.Duration(ms) * time.Millisecond
		}
		if v < 0 || v > 10*time.Second {
			return Sequence{}, fmt.Errorf("knock_delay %q: want 0..10s", d)
		}
		s.Delay = v
	}
	return s, nil
}

// Do knocks at host (a name or an address, without a port): a TCP
// connection attempt (closed at once if it succeeds; a refused or
// unanswered one is a knock too) or a UDP datagram per port, a pause after
// each. log, if not nil, gets a line per knock.
func (s Sequence) Do(ctx context.Context, host string, log func(format string, args ...any)) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	wait := s.Delay
	if wait <= 0 {
		wait = 50 * time.Millisecond
	}
	for _, p := range s.Ports {
		addr := net.JoinHostPort(host, strconv.Itoa(p.Port))
		if p.UDP {
			c, err := (&net.Dialer{}).DialContext(ctx, "udp", addr)
			if err != nil {
				return fmt.Errorf("port knocking %s: %w", p, err)
			}
			_, _ = c.Write([]byte{0})
			c.Close()
		} else {
			dctx, cancel := context.WithTimeout(ctx, wait)
			c, err := (&net.Dialer{}).DialContext(dctx, "tcp", addr)
			cancel()
			if err == nil {
				c.Close()
			}
		}
		log("port knocking: %s", p)
		t := time.NewTimer(s.Delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}
