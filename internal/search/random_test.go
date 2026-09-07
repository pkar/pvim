package search

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// TestAgainstVimRandom is the table's insurance. A table holds the cases
// somebody thought of, and the rules in this package -- which column a match
// has to clear, when the offset is subtracted from the start, which end of a
// wrapped scan a backward search takes -- interact in ways nobody thinks of.
// So: random buffers, random cursors, random searches, both sides, diff.
//
// Fixed seed, because a test that fails once a week on a different case is a
// test nobody fixes. A new seed is a one-line change and belongs in the commit
// that widens the grammar.
func TestAgainstVimRandom(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to vim once per case")
	}
	for _, tc := range randomCases(rand.New(rand.NewSource(20260903)), 200) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAgainstVim(t, tc)
		})
	}
}

// The grammar. Small on purpose: every pattern here is one internal/regex
// translates exactly, and none of them can match zero characters, because a
// zero-width match is the one known difference from vim (see the gap on
// "empty matches" in cases_test.go) and a fuzzer that keeps rediscovering it
// is noise. Every other shape is in: an alternation, a multi-line pattern, a
// negated collection, a word boundary, an anchor with a character on it.
var (
	randPatterns = []string{
		"a", "b", "ab", "ba", "a.", ".b", "^a", "a$", "[ab]", `\<a\>`,
		"c", `\s`, `ab\|b`, "aa", `a\+`, "abc", ".",
		`[^a]`, `\<b\>`, `c\|a`, `a\nb`, `b\nc`, `a\_.b`, `\.`,
		`a\_sb`, `\_a\_a`, `a\_[bc]`, `b\_.\_.`,
	}
	randOffsets = []string{
		"", "/e", "/e+1", "/e-1", "/s+1", "/s-1", "/b+2", "/+1", "/-1", "/+0",
		"/e+3", "/2", "/e-4", "/s+5", "/-3", "/e+0", "/s", "/", "/e+9",
	}
	randOpts = []string{
		"", "nowrapscan", "ignorecase", "ignorecase smartcase", "ignorecase nowrapscan",
	}
)

// randomCases builds n cases from the grammar.
func randomCases(r *rand.Rand, n int) []vimCase {
	out := make([]vimCase, 0, n)
	for i := 0; i < n; i++ {
		in := randBuffer(r)
		lines := strings.Split(strings.TrimSuffix(in, "\n"), "\n")
		ln := r.Intn(len(lines)) + 1
		col := 1
		if w := len(lines[ln-1]); w > 0 {
			col = r.Intn(w) + 1
		}

		tc := vimCase{
			name: "r" + strconv.Itoa(i),
			in:   in,
			line: ln,
			col:  col,
			opts: randOpts[r.Intn(len(randOpts))],
		}
		for steps := r.Intn(5) + 1; steps > 0; steps-- {
			tc.steps = append(tc.steps, randStep(r))
		}
		out = append(out, tc)
	}
	return out
}

// randBuffer makes one to five short lines out of an alphabet with just enough
// in it to make a pattern ambiguous: two letters that appear in every pattern,
// a third that mostly does not, a space and a tab.
func randBuffer(r *rand.Rand) string {
	var b strings.Builder
	for n := r.Intn(6) + 1; n > 0; n-- {
		for w := r.Intn(11); w > 0; w-- {
			b.WriteByte("aabbc \t"[r.Intn(7)])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// randStep picks one command. A case that opens with n gets E35 out of both
// sides, which is a case worth having, so nothing here forces a search first.
func randStep(r *rand.Rand) step {
	dir := Forward
	if r.Intn(2) == 0 {
		dir = Backward
	}
	count := 0
	if r.Intn(4) == 0 {
		count = r.Intn(3) + 1
	}
	switch r.Intn(8) {
	case 0, 1:
		return step{kind: doNext, count: count}
	case 2:
		return step{kind: doPrev, count: count}
	case 3:
		return step{kind: doStar, dir: dir, count: count}
	case 4:
		return step{kind: doGStar, dir: dir, count: count}
	default:
		// One search in eight has an empty command line, which repeats the
		// last pattern with the last offset, and one has a bare separator,
		// which repeats the pattern and throws the offset away.
		pat := randPatterns[r.Intn(len(randPatterns))]
		off := randOffsets[r.Intn(len(randOffsets))]
		switch r.Intn(8) {
		case 0:
			pat, off = "", ""
		case 1:
			pat, off = "", "/"
		}
		if dir == Backward {
			off = strings.Replace(off, "/", "?", 1)
		}
		return step{kind: doSearch, dir: dir, line: pat + off, count: count}
	}
}
