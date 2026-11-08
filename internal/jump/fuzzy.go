// SPDX-License-Identifier: MIT

package jump

import (
	"strings"
	"unicode"
)

// Match scores how well query matches s as a fuzzy subsequence (case
// insensitive): ok is false if not every rune of query occurs in order.
// Higher is better: consecutive runes, matches at the start of a word and
// a prefix match score more; a shorter s wins ties.
func Match(query, s string) (score int, ok bool) {
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(q) == 0 {
		return 0, true
	}
	r := []rune(s)
	low := []rune(strings.ToLower(s))
	qi, prev := 0, -2
	for i := 0; i < len(low) && qi < len(q); i++ {
		if low[i] != q[qi] {
			continue
		}
		score++
		if i == prev+1 {
			score += 5 // consecutive
		}
		if i == 0 {
			score += 8
		} else if p := r[i-1]; !unicode.IsLetter(p) && !unicode.IsDigit(p) || unicode.IsLower(p) && unicode.IsUpper(r[i]) {
			score += 4 // start of a word
		}
		prev = i
		qi++
	}
	if qi < len(q) {
		return 0, false
	}
	if strings.Contains(strings.ToLower(s), string(q)) {
		score += 10
	}
	return score*100 - min(len(r), 99), true
}
