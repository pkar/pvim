package finder

import (
	"sort"
	"strings"
)

// The scorer.
//
// # This is a departure from ctrlp and it is deliberate
//
// ctrlp does not score. It filters with a fuzzy regex -- s:buildpat turns
// "abc" into `a[^a]\{-}b[^b]\{-}c` (autoload/ctrlp.vim:2605) -- and then SORTS
// what survived, by full path length first (ctrlp#complen), then, only when
// there are fewer than 21 results, by file name length, by mtime and by
// whether the file shares a parent with the current one, with the tightness of
// the match folded in as a weighted tie-break (s:mixedsort,
// autoload/ctrlp.vim:1610). Shortest path wins; where the letters landed
// barely matters.
//
// This scores fzf-style instead, with path-separator and camel-boundary
// bonuses, and every constant below is fzf's arithmetic rather than a
// measurement of ctrlp. Nothing here can be graded against the plugin, so the
// gate it has to pass is behavioural: "hueveri" puts hue_verify.go first in
// both.
//
// # The algorithm
//
// A candidate has to contain the pattern as a subsequence, matched
// case-insensitively unless the pattern has an uppercase letter in it, which
// is vim's 'smartcase' and also ctrlp's (s:martcs prefixes \C when &scs and
// the pattern has an uppercase, autoload/ctrlp.vim:799).
//
// Every candidate that passes then gets one pass of dynamic programming over
// (pattern position, candidate position), which is fzf's v2 algorithm reduced
// to what a file list needs: no unicode normalisation, no ranged scoring, no
// backtracking to recover the matched positions, because nothing in this
// editor highlights them yet.
//
// - a matched character scores scoreMatch
// - a character right after a path separator, an underscore, a dash, a dot
// or a space scores bonusBoundary on top
// - a lowercase-to-uppercase step scores bonusCamel123
// - the first character of the whole candidate, or of its last path
// component, is worth bonusBoundary doubled
// - consecutive matched characters each score bonusConsecutive, which is
// what makes a run beat a scatter; see the constant for how that differs
// from fzf
// - a gap costs scoreGapStart for the first skipped character and
// scoreGapExtension for each one after it
//
// The numbers are fzf's own (16, 8, 7, -3, -1). They are a ratio and not a
// measurement: what matters is that one boundary hit is worth half a matched
// character and that a two-character gap costs less than one match.
const (
	scoreMatch        = 16
	scoreGapStart     = -3
	scoreGapExtension = -1

	// bonusBoundary is fzf's scoreMatch/2, given to a character that starts a
	// word. bonusCamel123 is one less, so "MyFile" scores its M above its F
	// when both are word starts by different rules.
	bonusBoundary = scoreMatch / 2
	bonusCamel123 = bonusBoundary - 1
	// bonusFirstCharMultiplier doubles the boundary bonus on the first
	// character of the candidate and on the first character of its last path
	// component, which is what makes a query prefer a file whose NAME starts
	// with it over one whose directory does.
	bonusFirstCharMultiplier = 2
)

// Match is one scored candidate.
type Match struct {
	// Index is the candidate's position in the list that was searched, so a
	// caller can get back to whatever it was carrying alongside the string.
	Index int
	// Score is higher for better. It is meaningful only against other scores
	// from the same pattern.
	Score int
}

// Score ranks candidate against pattern.
//
// ok is false when the pattern is not a subsequence of the candidate, which is
// the whole of the filter: an empty pattern matches everything with a score of
// zero, so a finder with nothing typed shows the list it was given.
func Score(candidate, pattern string) (score int, ok bool) {
	if pattern == "" {
		return 0, true
	}
	if len(pattern) > len(candidate) {
		return 0, false
	}
	fold := !hasUpper(pattern)
	if !subsequence(candidate, pattern, fold) {
		return 0, false
	}
	return dp(candidate, pattern, fold), true
}

// hasUpper is 'smartcase': a pattern with an uppercase letter in it is matched
// case sensitively.
func hasUpper(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

// subsequence is the cheap filter in front of the expensive score. It runs
// over every candidate on every keystroke, so it is a byte loop and not a
// regexp: a repository of ten thousand paths is ten thousand of these.
func subsequence(candidate, pattern string, fold bool) bool {
	j := 0
	for i := 0; i < len(candidate) && j < len(pattern); i++ {
		if eq(candidate[i], pattern[j], fold) {
			j++
		}
	}
	return j == len(pattern)
}

func eq(a, b byte, fold bool) bool {
	if a == b {
		return true
	}
	if !fold {
		return false
	}
	return lower(a) == lower(b)
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func upper(c byte) bool { return c >= 'A' && c <= 'Z' }
func alnum(c byte) bool {
	return upper(c) || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// bonusAt is what the character at i is worth for starting where it starts.
//
// The path separator is the interesting one and it is why this function has a
// second clause for the last component: "internal/finder/score.go" should rank
// "score" above a file called "s.go" in a directory called "core", and the
// only thing that says so is that the s of score is the first character of the
// last component.
func bonusAt(s string, i, lastSlash int) int {
	c := s[i]
	if i == 0 {
		return bonusBoundary * bonusFirstCharMultiplier
	}
	if i == lastSlash+1 {
		return bonusBoundary * bonusFirstCharMultiplier
	}
	prev := s[i-1]
	switch {
	case prev == '/' || prev == '\\':
		return bonusBoundary
	case prev == '_' || prev == '-' || prev == '.' || prev == ' ':
		return bonusBoundary
	case !upper(prev) && upper(c):
		// The camel boundary: a lowercase or a digit followed by an
		// uppercase, which is what makes "hv" find HueVerify.
		if alnum(prev) {
			return bonusCamel123
		}
		return bonusBoundary
	case !alnum(prev):
		return bonusBoundary
	}
	return 0
}

// bonusConsecutive is what a matched character gets for following the one
// before it with no gap.
//
// fzf's rule is subtler: a run keeps the bonus of the character that STARTED
// it, so "verify" matched inside "hue_verify.go" earns the underscore's
// boundary bonus once and not six times. This gives every character of a run
// the boundary bonus instead, which rewards runs harder than fzf does and is
// two lines of recurrence rather than a second array tracking where each run
// began. It is a simplification and not a reproduction, and the ordering it
// changes is between two candidates that both match in one run, where the
// longer run wins either way.
const bonusConsecutive = bonusBoundary

// dp is the score itself: one row per pattern character over the candidate.
//
// best[j] is the best score of a match of pattern[:i+1] whose LAST character
// landed on candidate[j]. Two rows are kept and not the whole table, because
// nothing reads the matched positions back -- there is no highlighting in the
// match window yet.
//
// gap[j] is the other half of the recurrence and the reason it is linear: the
// best way to arrive at j having already matched pattern[:i], with the gap
// paid for. It is built left to right as
//
//	gap[j] = max(prev[j-1] + scoreGapStart, gap[j-1] + scoreGapExtension)
//
// which charges the first skipped character more than the ones after it,
// exactly as fzf does.
func dp(candidate, pattern string, fold bool) int {
	n, m := len(candidate), len(pattern)
	lastSlash := strings.LastIndexAny(candidate, `/\`)
	const none = -1 << 30

	prev := make([]int, n)
	cur := make([]int, n)
	gap := make([]int, n)

	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			cur[j] = none
			if i == 0 {
				gap[j] = 0 // the first character may start anywhere, free
			} else {
				gap[j] = none
				if j > 0 {
					if prev[j-1] > none {
						gap[j] = prev[j-1] + scoreGapStart
					}
					if gap[j-1] > none && gap[j-1]+scoreGapExtension > gap[j] {
						gap[j] = gap[j-1] + scoreGapExtension
					}
				}
			}
			if !eq(candidate[j], pattern[i], fold) {
				continue
			}
			b := bonusAt(candidate, j, lastSlash)
			best := none
			if i == 0 {
				best = scoreMatch + b
			} else {
				if j > 0 && prev[j-1] > none {
					// Straight on from the character before, with no gap.
					run := b
					if run < bonusConsecutive {
						run = bonusConsecutive
					}
					best = prev[j-1] + scoreMatch + run
				}
				if gap[j] > none {
					if s := gap[j] + scoreMatch + b; s > best {
						best = s
					}
				}
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}

	best := none
	for _, v := range prev {
		if v > best {
			best = v
		}
	}
	if best == none {
		return 0
	}
	return best
}

// Rank scores every candidate and returns the matches, best first.
//
// A tie breaks on the length of the candidate, shortest first, and then on the
// candidate's own order. The length rule is not decoration: "hueveri" scores
// hue_verify.go and hue_verifier_registry_internal.go identically, because the
// seven matched characters land in the same places in both and everything
// after them is free. Something has to choose, and ctrlp's own first sort key
// is exactly this one -- ctrlp#complen compares strlen and nothing else
// (autoload/ctrlp.vim:1533) -- so the shorter path wins here as it does there,
// and the gate has one answer rather than whichever of the two the
// filesystem listed first.
//
// The last tie-break is the input order, which for a walk is alphabetical and
// for the buffer and MRU lists is recency: a stable sort over a list the caller
// already put in the order it wants is one fewer arbitrary rule.
func Rank(candidates []string, pattern string) []Match {
	out := make([]Match, 0, len(candidates))
	for i, c := range candidates {
		if s, ok := Score(c, pattern); ok {
			out = append(out, Match{Index: i, Score: s})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return len(candidates[out[a].Index]) < len(candidates[out[b].Index])
	})
	return out
}
