package search

// The buffers the differential cases run over. Small and deliberate: a match
// at the start of a line, one at the end, two on one line, one on the last
// line, a tab indent, a multi-byte character, a line with no keyword character
// on it at all, and a line of nothing but punctuation.
const (
	bWords  = "alpha beta gamma\ndelta beta epsilon\nzeta eta theta\n"
	bPairs  = "ab ab ab\ncd ab cd\n"
	bIndent = "  alpha beta gamma\n\tdelta beta epsilon\nzeta eta theta beta\n"
	bFoo    = "foo foobar foo\nbar foo baz\nfoobar\n"
	bPunct  = "  ... !!!\nfoo\n"
	bAccent = "café x\nzzz\n"
	bCase   = "Foo\nfoo\nFoo\n"
	// One capital and two lowercase matches, so a search for Foo says which
	// way the case rules went: case-insensitive from line 2 finds line 3,
	// case-sensitive wraps round to line 1.
	bStarCase = "Foo\nfoo\nfoo\n"
	bPara     = "foo\nbar\nfoo bar\nbaz\n"
	bRun      = "aaaa\n"
	bEmpty    = "x\n\ny\n"
	bSlash    = "a/b c\nx[y]z\nfoo\n"
	bDash     = "foo-bar baz\nfoo-bar\n"
)

// zeroWidthGap is the one difference from vim this package knows about and
// does not fix.
//
// Vim's line scan continues at the end of the match it just rejected, and when
// that match was empty it steps one character on, so vim sees an empty match
// at every column a zero-width pattern can match at. Go's FindAllIndex drops
// an empty match that abuts the previous one: `b*` over "bcb\tcb" is
// [0,1) [2,3) [4,4) [5,6) to Go and has three more to vim, at 1, 3 and 6.
// Closing it needs "the leftmost match starting at or after column n", which
// RE2 does not offer and which cannot be faked by slicing the line without
// losing the context ^, $ and \b are decided by.
//
// It shows up for /b*, /^ and /$ used as whole patterns, and only in
// combination with a count or an offset: the plain /$ and /^ cases below pass.
// A pattern with a character in it, which is every pattern anybody types, is
// not affected.
const zeroWidthGap = "a pattern that can match zero characters: vim finds an empty match at every column and Go's FindAllIndex drops the ones that abut a previous match, so a count or an offset lands one match out"

// vimCases is the table TestAgainstVim runs. Every behaviour this package
// claims has a row here, because the claim is only worth what the oracle says
// about it.
func vimCases() []vimCase {
	return []vimCase{
		// ---- / and ?, the plain forms ----
		{name: "forward", in: bWords, line: 1, col: 1, steps: []step{fwd("beta")}},
		{name: "forward wraps", in: bWords, line: 3, col: 1, steps: []step{fwd("alpha")}},
		{name: "backward wraps", in: bWords, line: 1, col: 1, steps: []step{back("theta")}},
		{name: "backward", in: bWords, line: 3, col: 1, steps: []step{back("beta")}},
		{name: "forward finds nothing", in: bWords, line: 1, col: 1, steps: []step{fwd("nosuchword")}},
		{name: "backward finds nothing", in: bWords, line: 1, col: 1, steps: []step{back("nosuchword")}},
		{name: "the match under the cursor is skipped", in: bWords, line: 1, col: 1, steps: []step{fwd("alpha")}},

		// ---- 'wrapscan' off ----
		{name: "nowrapscan hits bottom", in: bWords, line: 3, col: 14, opts: "nowrapscan", steps: []step{fwd("alpha")}},
		{name: "nowrapscan hits top", in: bWords, line: 1, col: 1, opts: "nowrapscan", steps: []step{back("alpha")}},
		{name: "nowrapscan absent forward", in: bWords, line: 1, col: 1, opts: "nowrapscan", steps: []step{fwd("zzz")}},
		{name: "nowrapscan absent backward", in: bWords, line: 1, col: 1, opts: "nowrapscan", steps: []step{back("zzz")}},
		{name: "nowrapscan n off the end", in: bWords, line: 1, col: 1, opts: "nowrapscan", steps: []step{fwd("beta"), next(), next()}},

		// ---- offsets ----
		{name: "offset e", in: bWords, line: 1, col: 1, steps: []step{fwd("beta/e")}},
		{name: "offset e+2", in: bWords, line: 1, col: 1, steps: []step{fwd("beta/e+2")}},
		{name: "offset e-1", in: bWords, line: 1, col: 1, steps: []step{fwd("beta/e-1")}},
		{name: "offset s+1", in: bWords, line: 1, col: 1, steps: []step{fwd("beta/s+1")}},
		{name: "offset s-1", in: bWords, line: 1, col: 1, steps: []step{fwd("beta/s-1")}},
		{name: "offset b+2", in: bWords, line: 1, col: 1, steps: []step{fwd("beta/b+2")}},
		{name: "offset bare s", in: bWords, line: 1, col: 1, steps: []step{fwd("beta/s")}},
		{name: "offset line +1", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/+1")}},
		{name: "offset line -1", in: bIndent, line: 3, col: 1, steps: []step{fwd("beta/-1")}},
		{name: "offset line bare number", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/1")}},
		{name: "offset line 0", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/0")}},
		{name: "offset line clamps at the bottom", in: bWords, line: 1, col: 1, steps: []step{fwd("theta/+9")}},
		{name: "offset line clamps at the top", in: bWords, line: 1, col: 1, steps: []step{fwd("alpha/-9")}},
		{name: "offset bare plus", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/+")}},
		{name: "offset bare minus", in: bIndent, line: 3, col: 1, steps: []step{fwd("beta/-")}},
		{name: "offset e runs off the end", in: bPairs, line: 1, col: 1, steps: []step{fwd("ab/e+99")}},
		{name: "offset e crosses a line", in: bPairs, line: 1, col: 1, steps: []step{fwd("ab/e+9")}},
		{name: "offset s-1 crosses back", in: bPairs, line: 2, col: 4, steps: []step{fwd("cd/s-4")}},
		{name: "offset trailing garbage", in: bPara, line: 1, col: 1, steps: []step{fwd("foo/exyz")}},
		{name: "offset with a backward search", in: bPairs, line: 2, col: 8, steps: []step{back("ab?e")}},
		{name: "offset s+1 with a backward search", in: bPairs, line: 2, col: 8, steps: []step{back("ab?s+1")}},

		// ---- the offset is subtracted from the start, so n moves ----
		{name: "n after s-1", in: bPairs, line: 1, col: 1, steps: []step{fwd("ab/s-1"), next(), next()}},
		{name: "n after e+1", in: bPairs, line: 1, col: 1, steps: []step{fwd("ab/e+1"), next()}},
		{name: "n after e", in: bPairs, line: 1, col: 1, steps: []step{fwd("ab/e"), next(), next(), next()}},
		{name: "n after a line offset", in: bPairs, line: 1, col: 1, steps: []step{fwd("ab/+1"), next(), next()}},

		// ---- n and N ----
		{name: "n repeats", in: bWords, line: 1, col: 1, steps: []step{fwd("beta"), next()}},
		{name: "N reverses", in: bWords, line: 1, col: 1, steps: []step{fwd("beta"), prev()}},
		{name: "n after N goes forward again", in: bWords, line: 1, col: 1, steps: []step{fwd("beta"), prev(), next()}},
		{name: "n after a backward search", in: bWords, line: 3, col: 1, steps: []step{back("beta"), next()}},
		{name: "N after a backward search", in: bWords, line: 3, col: 1, steps: []step{back("beta"), prev()}},
		{name: "n keeps the offset", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/e"), next()}},
		{name: "N keeps the offset", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/e"), prev()}},
		{name: "n with nothing searched for", in: bWords, line: 1, col: 1, steps: []step{next()}},
		{name: "N with nothing searched for", in: bWords, line: 1, col: 1, steps: []step{prev()}},
		{name: "n after a failed search", in: bWords, line: 1, col: 1, steps: []step{fwd("zzz"), next()}},

		// ---- the empty command line ----
		{name: "bare slash keeps the offset", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/e"), fwd("")}},
		{name: "slash slash drops the offset", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/e"), fwd("/")}},
		{name: "slash slash e sets a new one", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/e"), fwd("/e")}},
		{name: "a new pattern drops the offset", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/e"), fwd("eta")}},
		{name: "bare question reverses", in: bIndent, line: 1, col: 1, steps: []step{fwd("beta/e"), back("")}},
		{name: "bare slash with nothing before it", in: bWords, line: 1, col: 1, steps: []step{fwd("")}},

		// ---- counts ----
		{name: "count 2 forward", in: bPairs, line: 1, col: 1, steps: []step{cnt(2, fwd("ab"))}},
		{name: "count 3 forward", in: bPairs, line: 1, col: 1, steps: []step{cnt(3, fwd("ab"))}},
		{name: "count 4 wraps", in: bPairs, line: 1, col: 1, steps: []step{cnt(4, fwd("ab"))}},
		{name: "count with an end offset", in: bPairs, line: 1, col: 1, steps: []step{cnt(2, fwd("ab/e"))}},
		{name: "count with a start offset", in: bPairs, line: 1, col: 1, steps: []step{cnt(2, fwd("ab/s-1"))}},
		{name: "count backward", in: bPairs, line: 1, col: 1, steps: []step{cnt(2, back("ab"))}},
		{name: "count on n", in: bPairs, line: 1, col: 1, steps: []step{fwd("ab"), cnt(2, next())}},

		// ---- the columns a match has to clear ----
		{name: "forward from every column", in: bPairs, line: 1, col: 4, steps: []step{fwd("ab")}},
		{name: "forward end from every column", in: bPairs, line: 1, col: 5, steps: []step{fwd("ab/e")}},
		{name: "backward from a column inside a match", in: bPairs, line: 1, col: 5, steps: []step{back("ab")}},
		{name: "backward end", in: bPairs, line: 1, col: 6, steps: []step{back("ab?e")}},
		{name: "overlapping matches are not found", in: bRun, line: 1, col: 1, steps: []step{fwd("aa")}},
		{name: "overlapping matches wrap", in: bRun, line: 1, col: 3, steps: []step{fwd("aa")}},
		{name: "multibyte start column", in: bAccent, line: 1, col: 1, steps: []step{fwd("f")}},
		{name: "end of line does not stick", in: bPara, line: 1, col: 1, steps: []step{fwd("$"), next(), next()}},
		{name: "start of line does not stick", in: bPara, line: 1, col: 1, steps: []step{fwd("^"), next()}},
		{name: "empty matches", in: bPairs, line: 1, col: 1, steps: []step{fwd("b*"), next(), next()},
			gap: zeroWidthGap},
		{name: "empty lines", in: bEmpty, line: 1, col: 1, steps: []step{fwd("^"), next(), next()}},
		{name: "zero width with an offset", in: bPairs, line: 1, col: 1, steps: []step{fwd("b*/e+1")}, gap: zeroWidthGap},
		{name: "zero width with a count", in: bPara, line: 2, col: 1, steps: []step{cnt(3, fwd("^"))}, gap: zeroWidthGap},

		// ---- 'ignorecase' and 'smartcase' ----
		{name: "ignorecase", in: bCase, line: 1, col: 1, opts: "ignorecase", steps: []step{fwd("Foo")}},
		{name: "smartcase with a capital", in: bCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{fwd("Foo")}},
		{name: "smartcase all lower", in: bCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{fwd("foo")}},
		{name: "backslash C beats ignorecase", in: bCase, line: 1, col: 1, opts: "ignorecase", steps: []step{fwd(`\CFoo`)}},
		{name: "backslash c beats the pattern", in: bCase, line: 2, col: 1, steps: []step{fwd(`\cfoo`)}},

		// ---- * and # ----
		{name: "star", in: bFoo, line: 1, col: 1, steps: []step{star(Forward)}},
		{name: "star on a longer word", in: bFoo, line: 1, col: 5, steps: []step{star(Forward)}},
		{name: "g star", in: bFoo, line: 1, col: 1, steps: []step{gstar(Forward)}},
		{name: "g star on a longer word", in: bFoo, line: 1, col: 5, steps: []step{gstar(Forward)}},
		{name: "hash", in: bFoo, line: 1, col: 1, steps: []step{star(Backward)}},
		{name: "g hash", in: bFoo, line: 1, col: 5, steps: []step{gstar(Backward)}},
		{name: "star from the middle of a word", in: bRun, line: 1, col: 3, steps: []step{gstar(Forward)}},
		{name: "star scans forward for a keyword", in: bPunct, line: 1, col: 1, steps: []step{star(Forward)}},
		{name: "star falls back to a non-blank run", in: "  ... !!!\nzzz ...\n", line: 1, col: 1, steps: []step{star(Forward)}},
		{name: "star with no string at all", in: "   \nfoo\n", line: 1, col: 1, steps: []step{star(Forward)}},
		{name: "star on an empty line", in: "\nfoo\n", line: 1, col: 1, steps: []step{star(Forward)}},
		{name: "star ignores smartcase", in: bCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{star(Forward)}},
		{name: "star then n", in: bFoo, line: 2, col: 5, steps: []step{star(Forward), next()}},
		{name: "star clears the offset", in: bFoo, line: 1, col: 1, steps: []step{fwd("foo/e"), star(Forward)}},
		{name: "star leaves the cursor on the word when it fails", in: "  abc\nzzz\n", line: 1, col: 5, opts: "nowrapscan", steps: []step{star(Forward)}},
		{name: "star on a multibyte word", in: bAccent, line: 1, col: 1, steps: []step{star(Forward)},
			gap: `internal/regex translates \< and \> to Go's \b, which is ASCII-only, so \<café\> matches nothing. The word is built right here and the boundary is refused one package down`},
		{name: "star with iskeyword", in: bDash, line: 1, col: 1, opts: "iskeyword+=-", steps: []step{star(Forward)}},
		{name: "star without iskeyword", in: bDash, line: 1, col: 1, steps: []step{star(Forward)}},
		{name: "star with a count", in: bFoo, line: 2, col: 5, steps: []step{cnt(2, star(Forward))}},

		// ---- * stores "no smartcase" beside the pattern, so n keeps it ----
		{name: "star then n keeps no smartcase", in: bStarCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{star(Forward), next()}},
		{name: "g star then n keeps no smartcase", in: bStarCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{gstar(Forward), next()}},
		{name: "star then a bare search keeps no smartcase", in: bStarCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{star(Forward), fwd("")}},
		{name: "star then n then N keeps no smartcase", in: bStarCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{star(Forward), next(), prev()}},
		{name: "a typed pattern after a star brings smartcase back", in: bStarCase, line: 1, col: 1, opts: "ignorecase smartcase", steps: []step{star(Forward), fwd("Foo"), next()}},

		// ---- patterns with the separator in them ----
		{name: "escaped separator", in: bSlash, line: 3, col: 1, steps: []step{fwd(`a\/b`)}},
		{name: "escaped separator backward", in: bSlash, line: 3, col: 1, steps: []step{back(`a\/b`)}},
		{name: "escaped question backward", in: "zz\na?b\n", line: 1, col: 1, steps: []step{back(`a\?b`)}},
		{name: "separator inside a collection", in: bSlash, line: 3, col: 1, steps: []step{fwd(`[/]`)}},
		{name: "collection and an offset", in: bSlash, line: 3, col: 1, steps: []step{fwd(`[/]/e`)}},
		{name: "close bracket first in a collection", in: bSlash, line: 1, col: 1, steps: []step{fwd(`[]y]`)}},

		// ---- patterns that cross a line break ----
		{name: "newline in the pattern", in: bPara, line: 3, col: 1, steps: []step{fwd(`foo\nbar`)}},
		{name: "newline with an end offset", in: bPara, line: 3, col: 1, steps: []step{fwd(`foo\nbar/e`)}},
		{name: "underscore any", in: bPara, line: 1, col: 1, steps: []step{fwd(`o\_.b`)}},
		{name: "newline and n", in: bPara, line: 1, col: 1, steps: []step{fwd(`o\nb`), next()}},
	}
}
