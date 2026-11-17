package tea

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// chunkReader returns its data a few bytes at a time, as a terminal
// writing a paste does.
type chunkReader struct {
	data  []byte
	first int // the first read (the start marker comes whole, as from a terminal)
	n     int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	k := min(len(p), r.n, len(r.data))
	if r.first > 0 {
		k, r.first = min(r.first, len(r.data)), 0
	}
	copy(p, r.data[:k])
	r.data = r.data[k:]
	return k, nil
}

func readMsgs(t *testing.T, in string, first, chunk int) []Msg {
	t.Helper()
	msgs := make(chan Msg, 100)
	done := make(chan struct{})
	go func() {
		_ = readAnsiInputs(context.Background(), msgs, &chunkReader{data: []byte(in), first: first, n: chunk})
		close(done)
	}()
	var out []Msg
	for {
		select {
		case m := <-msgs:
			out = append(out, m)
		case <-done:
			for len(msgs) > 0 {
				out = append(out, <-msgs)
			}
			return out
		case <-time.After(20 * time.Second):
			t.Fatal("no end")
		}
	}
}

// vaultcrab patch 5: a paste split over many reads comes whole, with the
// keys around it, and a large one is parsed in linear time.
func TestBracketedPasteChunks(t *testing.T) {
	text := strings.Repeat("line\rстрока\t{}\r", 3000)
	for _, chunk := range []int{1, 7, 256, 4096} {
		msgs := readMsgs(t, "a\x1b[200~"+text+"\x1b[201~b", 9, chunk)
		if len(msgs) != 3 {
			t.Fatalf("chunk %d: %d messages", chunk, len(msgs))
		}
		k := msgs[1].(KeyMsg)
		if !k.Paste || string(k.Runes) != text || msgs[0].(KeyMsg).String() != "a" || msgs[2].(KeyMsg).String() != "b" {
			t.Fatalf("chunk %d: %v / %d runes / %v", chunk, msgs[0], len(k.Runes), msgs[2])
		}
	}
	// the end marker split between two reads
	msgs := readMsgs(t, "\x1b[200~xyz\x1b[201~", 11, 11)
	if len(msgs) != 1 || string(msgs[0].(KeyMsg).Runes) != "xyz" {
		t.Fatalf("split marker: %v", msgs)
	}
	big := strings.Repeat("    some python code here, 40 bytes ok\n", 2<<20/40)
	start := time.Now()
	msgs = readMsgs(t, "\x1b[200~"+big+"\x1b[201~", 0, 4096)
	if d := time.Since(start); len(msgs) != 1 || d > 2*time.Second {
		t.Fatalf("2 MB paste: %d messages in %v", len(msgs), d)
	} else {
		t.Logf("2 MB paste in %v", d)
	}
}
