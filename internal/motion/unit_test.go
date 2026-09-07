package motion

import (
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// The pieces underneath the motions, tested on their own because a failure in
// one of them shows up as a cursor in the wrong place a hundred probes later
// and says nothing about which piece it was.

// TestParseKeywords covers the syntax of 'iskeyword', which is not the syntax
// of anything else: "@" is every letter and not the character @, a range is
// written with a dash, a number is a decimal codepoint, and "^" removes.
func TestParseKeywords(t *testing.T) {
	for _, tc := range []struct {
		opt  string
		word string
		not  string
	}{
		{"@,48-57,_,192-255", "aZ09_", "-. ()"},
		{"", "aZ09_", "-. "},
		{"@,48-57,_,192-255,-", "a0_-", ". "},
		{"a-c", "abc", "dZ_"},
		{"@,^a", "bZ", "a"},
		{"64", "@", "a"},
		{"@,44", "a,", "."},
	} {
		k := parseKeywords(tc.opt)
		for _, c := range tc.word {
			if !k.isWord(c) {
				t.Errorf("iskeyword=%q: %q is not a keyword character, want it to be", tc.opt, c)
			}
		}
		for _, c := range tc.not {
			if k.isWord(c) {
				t.Errorf("iskeyword=%q: %q is a keyword character, want it not to be", tc.opt, c)
			}
		}
	}
}

// TestClass is vim's cls(): three classes for Latin-1, one per script above
// it, and everything flattened to one for the big-word motions.
func TestClass(t *testing.T) {
	k := parseKeywords("")
	for _, tc := range []struct {
		r    rune
		want int
	}{
		{' ', classBlank}, {'\t', classBlank}, {0, classBlank}, {0xa0, classBlank},
		{'a', classWord}, {'Z', classWord}, {'0', classWord}, {'_', classWord},
		{'é', classWord}, {'ß', classWord},
		{'.', classPunct}, {'-', classPunct}, {'(', classPunct},
		{'。', classPunct}, {'、', classPunct},
		{'漢', 0x4e00}, {'字', 0x4e00},
		{'あ', 0x3040}, {'ア', 0x30a0},
	} {
		if got := class(tc.r, k, false); got != tc.want {
			t.Errorf("class(%q) = %d, want %d", tc.r, got, tc.want)
		}
		want := classPunct
		if tc.want == classBlank {
			want = classBlank
		}
		if got := class(tc.r, k, true); got != want {
			t.Errorf("class(%q) with W = %d, want %d", tc.r, got, want)
		}
	}
}

// TestIncDec is vim's inc() and dec(), and the four return values the word
// motions read: 0 inside a line, 2 onto the position after its last byte, 1
// onto another line, -1 nowhere left to go.
func TestIncDec(t *testing.T) {
	b := text.Read([]byte("ab\n\ncd\n"))
	p := text.Pos{Line: 1, Col: 0}
	for _, want := range []struct {
		code int
		pos  text.Pos
	}{
		{0, text.Pos{Line: 1, Col: 1}},
		{2, text.Pos{Line: 1, Col: 2}},
		{1, text.Pos{Line: 2, Col: 0}},
		{1, text.Pos{Line: 3, Col: 0}},
		{0, text.Pos{Line: 3, Col: 1}},
		{2, text.Pos{Line: 3, Col: 2}},
		{-1, text.Pos{Line: 3, Col: 2}},
	} {
		if code := inc(b, &p); code != want.code || p != want.pos {
			t.Fatalf("inc gave %d at %v, want %d at %v", code, p, want.code, want.pos)
		}
	}
	for _, want := range []struct {
		code int
		pos  text.Pos
	}{
		{0, text.Pos{Line: 3, Col: 1}},
		{0, text.Pos{Line: 3, Col: 0}},
		{1, text.Pos{Line: 2, Col: 0}},
		{1, text.Pos{Line: 1, Col: 2}},
		{0, text.Pos{Line: 1, Col: 1}},
		{0, text.Pos{Line: 1, Col: 0}},
		{-1, text.Pos{Line: 1, Col: 0}},
	} {
		if code := dec(b, &p); code != want.code || p != want.pos {
			t.Fatalf("dec gave %d at %v, want %d at %v", code, p, want.code, want.pos)
		}
	}
}

// TestCharLen: one cursor step is a base character plus the combining marks
// that follow it, which is what vim's mb_ptr2len returns under utf-8 and why
// the cursor never lands between the two.
func TestCharLen(t *testing.T) {
	for _, tc := range []struct {
		line string
		want int
	}{
		{"a", 1}, {"é", 2}, {"漢", 3}, {"éx", 3}, {"\t", 1},
	} {
		if got := charLen([]byte(tc.line), 0); got != tc.want {
			t.Errorf("charLen(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

// TestInMacro is the nroff macro list 'paragraphs' and 'sections' are written
// in: two-character names run together, with a space standing for a name one
// character long or the end of the line.
func TestInMacro(t *testing.T) {
	const paragraphs = "IPLPPPQPP TPHPLIPpLpItpplpipbp"
	for _, tc := range []struct {
		s    string
		want bool
	}{
		{"IP", true}, {"LP", true}, {"bp", true}, {"P", true},
		// A line of nothing but "." names no macro: every pair in the list
		// starts with a letter, so there is nothing for the end of the line
		// to match.
		{"", false},
		{"XX", false}, {"Ip", false}, {"IPx", true},
	} {
		if got := inmacro(paragraphs, []byte(tc.s)); got != tc.want {
			t.Errorf("inmacro(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// TestParsePairs reads 'matchpairs', and TestEscaped is the backslash rule
// that keeps a \( from matching a bare ).
func TestParsePairs(t *testing.T) {
	got := parsePairs("(:),{:},[:]")
	want := []matchPair{{'(', ')'}, {'{', '}'}, {'[', ']'}}
	if len(got) != len(want) {
		t.Fatalf("parsePairs gave %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pair %d is %v, want %v", i, got[i], want[i])
		}
	}
	if got := parsePairs("<:>,(:)"); len(got) != 2 || got[0] != (matchPair{'<', '>'}) {
		t.Errorf("parsePairs of an added pair gave %v", got)
	}
}

func TestEscaped(t *testing.T) {
	for _, tc := range []struct {
		line string
		col  int
		want bool
	}{
		{"(", 0, false},
		{"\\(", 1, true},
		{"\\\\(", 2, false},
		{"\\\\\\(", 3, true},
	} {
		if got := escaped([]byte(tc.line), tc.col); got != tc.want {
			t.Errorf("escaped(%q, %d) = %v, want %v", tc.line, tc.col, got, tc.want)
		}
	}
}

// TestInIndent is vim's inindent(), the test the exclusive-linewise rule asks:
// is the cursor at or before the first non-blank of its line?
func TestInIndent(t *testing.T) {
	for _, tc := range []struct {
		line string
		col  int
		want bool
	}{
		{"   abc", 0, true},
		{"   abc", 3, true},
		{"   abc", 4, false},
		{"abc", 0, true},
		{"abc", 1, false},
		{"", 0, true},
		{"    ", 2, true},
	} {
		if got := inIndent([]byte(tc.line), tc.col); got != tc.want {
			t.Errorf("inIndent(%q, %d) = %v, want %v", tc.line, tc.col, got, tc.want)
		}
	}
}

// TestAdjustExclusiveLeavesOtherKindsAlone: both rules apply to charwise
// exclusive motions that end in column one of a later line and to nothing
// else. A rule that fires on an inclusive motion deletes one character too
// many everywhere.
func TestAdjustExclusiveLeavesOtherKindsAlone(t *testing.T) {
	b := text.Read([]byte("one\ntwo\nthree\n"))
	start, end := text.Pos{Line: 1, Col: 1}, text.Pos{Line: 3, Col: 0}
	for _, kind := range []Kind{KindCharInclusive, KindLine, KindBlock} {
		s, e, k := AdjustExclusive(b, start, end, kind)
		if s != start || e != end || k != kind {
			t.Errorf("AdjustExclusive changed a %v motion: %v %v %v", kind, s, e, k)
		}
	}
	// Same line: no adjustment either, whatever the column.
	s, e, k := AdjustExclusive(b, text.Pos{Line: 2, Col: 0}, text.Pos{Line: 2, Col: 0}, KindCharExclusive)
	if k != KindCharExclusive || s.Line != 2 || e.Line != 2 {
		t.Errorf("AdjustExclusive changed a one-line span: %v %v %v", s, e, k)
	}
}
