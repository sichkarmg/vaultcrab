// SPDX-License-Identifier: MIT

package jump

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultHistoryLimit is the number of entries kept (jump.history_limit).
const DefaultHistoryLimit = 200

// halfLife: the weight of a use halves every week.
const halfLife = 7 * 24 * time.Hour

// Entry is one remembered connection: through which jump host (a template
// "t:<name>" or a vault item "h:<id>"), to which target, as which user.
// Only names and addresses: never a secret.
type Entry struct {
	Jump   string `json:"jump"`
	Target string `json:"target"` // host[:port]
	User   string `json:"user,omitempty"`
	// Item is the vault item ID of a target picked from the host list (it
	// connects with its own credential); empty for a typed address.
	Item string `json:"item,omitempty"`
	// Cred is the credential chosen for the target (a key reference:
	// "password", "agent", a key file, "vault:<item id>"; "" = the
	// template's). Never the secret itself.
	Cred  string    `json:"cred,omitempty"`
	Last  time.Time `json:"last"`
	Count int       `json:"count"`
}

// Score is the frecency of e at now: every use counts, older uses less
// (half-life one week), so frequent and recent entries come first.
func (e Entry) Score(now time.Time) float64 {
	age := max(now.Sub(e.Last), 0)
	return float64(e.Count) * math.Exp2(-float64(age)/float64(halfLife))
}

// History is the local history of the connections through jump hosts
// (<cache dir>/jump-history.json). A disabled history neither reads nor
// writes the file. Safe for concurrent use.
type History struct {
	path  string
	limit int
	off   bool

	mu      sync.Mutex
	loaded  bool
	entries []Entry
}

// NewHistory returns the history kept in path (limit <= 0: the default).
func NewHistory(path string, limit int, enabled bool) *History {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	return &History{path: path, limit: limit, off: !enabled || path == ""}
}

// Configure changes the limit and whether the history is kept (a config
// reload); turning it off does not delete the file (vaultcrab history clear
// does).
func (h *History) Configure(limit int, enabled bool) {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	h.mu.Lock()
	h.limit, h.off = limit, !enabled || h.path == ""
	if h.off {
		h.entries, h.loaded = nil, false
	}
	h.mu.Unlock()
}

// Enabled reports whether connections are remembered.
func (h *History) Enabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.off
}

func (h *History) load() {
	if h.loaded || h.off {
		return
	}
	h.loaded = true
	b, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	var f struct {
		Entries []Entry `json:"entries"`
	}
	if json.Unmarshal(b, &f) == nil {
		h.entries = f.Entries
	}
}

// Add records a connection at now and saves the file.
func (h *History) Add(jump, target, user, item, cred string, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.off || jump == "" || target == "" {
		return nil
	}
	h.load()
	i := slices.IndexFunc(h.entries, func(e Entry) bool {
		return e.Jump == jump && strings.EqualFold(e.Target, target) && e.User == user && e.Item == item
	})
	if i < 0 {
		h.entries = append(h.entries, Entry{Jump: jump, Target: target, User: user, Item: item})
		i = len(h.entries) - 1
	}
	h.entries[i].Cred = cred // the last choice for this target
	h.entries[i].Count++
	h.entries[i].Last = now
	if len(h.entries) > h.limit {
		sortEntries(h.entries, now)
		h.entries = h.entries[:h.limit]
	}
	return h.save()
}

func (h *History) save() error {
	b, err := json.MarshalIndent(struct {
		Entries []Entry `json:"entries"`
	}{h.entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return err
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.path)
}

// Targets returns the entries of jump, best first.
func (h *History) Targets(jump string, now time.Time) []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.load()
	var out []Entry
	for _, e := range h.entries {
		if e.Jump == jump {
			out = append(out, e)
		}
	}
	sortEntries(out, now)
	return out
}

// LastCred returns the credential chosen most recently for a typed target
// through jump ("" if none): it is offered again for the next address.
func (h *History) LastCred(jump string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.load()
	var last Entry
	for _, e := range h.entries {
		if e.Jump == jump && e.Item == "" && e.Last.After(last.Last) {
			last = e
		}
	}
	return last.Cred
}

// Jumps returns the jump hosts used, best first (the sum of the scores of
// their entries).
func (h *History) Jumps(now time.Time) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.load()
	score := map[string]float64{}
	last := map[string]time.Time{}
	for _, e := range h.entries {
		score[e.Jump] += e.Score(now)
		if e.Last.After(last[e.Jump]) {
			last[e.Jump] = e.Last
		}
	}
	out := make([]string, 0, len(score))
	for j := range score {
		out = append(out, j)
	}
	slices.SortFunc(out, func(a, b string) int {
		switch {
		case score[a] != score[b]:
			if score[a] > score[b] {
				return -1
			}
			return 1
		case !last[a].Equal(last[b]):
			return last[b].Compare(last[a])
		}
		return strings.Compare(a, b)
	})
	return out
}

// sortEntries orders by frecency, then by the last use, then by target.
func sortEntries(es []Entry, now time.Time) {
	slices.SortStableFunc(es, func(a, b Entry) int {
		sa, sb := a.Score(now), b.Score(now)
		switch {
		case sa != sb:
			if sa > sb {
				return -1
			}
			return 1
		case !a.Last.Equal(b.Last):
			return b.Last.Compare(a.Last)
		}
		return strings.Compare(a.Target, b.Target)
	})
}

// ClearHistory deletes the history file at path (vaultcrab history clear).
// It reports whether there was one.
func ClearHistory(path string) (bool, error) {
	err := os.Remove(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	}
	return false, err
}

// Clear forgets every entry and deletes the file.
func (h *History) Clear() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries, h.loaded = nil, true
	if h.path == "" {
		return nil
	}
	_, err := ClearHistory(h.path)
	return err
}
