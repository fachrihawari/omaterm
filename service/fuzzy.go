package service

import (
	"sort"
	"strings"
)

// FuzzyMatch is one fuzzy match result.
type FuzzyMatch struct {
	Value   string  // the matched string
	Score   float64 // higher is better
	Indices []int   // byte offsets of matched runes (for highlighting)
}

// FuzzyFilter filters and ranks items against query (subsequence match).
// Returns matches sorted by score descending. An empty query returns items
// unchanged with zero scores.
func FuzzyFilter(items []string, query string, limit int) []FuzzyMatch {
	if query == "" {
		out := make([]FuzzyMatch, 0, len(items))
		for _, it := range items {
			out = append(out, FuzzyMatch{Value: it})
			if limit > 0 && len(out) >= limit {
				break
			}
		}
		return out
	}
	q := strings.ToLower(query)
	var out []FuzzyMatch
	for _, it := range items {
		if score, idx, ok := fuzzyScore(q, strings.ToLower(it)); ok {
			out = append(out, FuzzyMatch{Value: it, Score: score, Indices: idx})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return len(out[i].Value) < len(out[j].Value)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// fuzzyScore scores a subsequence match of q in s.
// Scoring: consecutive-run bonus, word-boundary bonus, basename bonus.
func fuzzyScore(q, s string) (float64, []int, bool) {
	if q == "" {
		return 0, nil, true
	}
	qi := 0
	var indices []int
	score := 0.0
	lastMatch := -2
	for si := 0; si < len(s) && qi < len(q); si++ {
		if s[si] != q[qi] {
			continue
		}
		indices = append(indices, si)
		score += 1
		// Consecutive bonus.
		if si == lastMatch+1 {
			score += 4
		}
		// Word-boundary bonus (start, or after / _ - . space).
		if si == 0 {
			score += 6
		} else {
			switch s[si-1] {
			case '/', '_', '-', '.', ' ':
				score += 5
			}
		}
		// Basename bonus: matches after the last slash score more.
		if li := strings.LastIndex(s, "/"); li >= 0 && si > li {
			score += 2
		}
		lastMatch = si
		qi++
	}
	if qi < len(q) {
		return 0, nil, false
	}
	// Prefer shorter strings.
	score += 10.0 / float64(len(s)+1)
	return score, indices, true
}
