package substitute

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/regex"
)

// TestRangesThatCannotHappen. internal/ex resolves the range, and every one of
// these is a range it can hand over after a fuzzed script: past the end,
// backwards, or at line zero. None of them may panic and none may edit
// anything it was not asked to.
func TestRangesThatCannotHappen(t *testing.T) {
	for _, c := range []struct{ first, last int }{
		{1, 99}, {99, 1}, {0, 2}, {-5, -1}, {3, 3},
	} {
		s := newSession("a1", "a2")
		cmd, err := Parse(KindSubstitute, "/a/X/", &s.st, &s.opt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Do(Request{Buf: s.buf, First: c.first, Last: c.last, Cmd: cmd, Opt: &s.opt, State: &s.st}); err != nil {
			if _, ok := err.(NotFoundError); !ok {
				t.Errorf("range %d,%d: %v", c.first, c.last, err)
			}
		}
	}
}

// TestSubstituteOnAnEmptyBuffer. A vim buffer is never empty; it is one empty
// line, and an anchored pattern still matches it.
func TestSubstituteOnAnEmptyBuffer(t *testing.T) {
	s := newSession("")
	if err := s.sub(t, KindSubstitute, "%", "/^/x/"); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "x" {
		t.Errorf("buffer %q, want %q", got, "x")
	}
}

// TestReplacementCanEmptyALine, which is ":%s/.*//" and which has to leave a
// line behind rather than remove one.
func TestReplacementCanEmptyALine(t *testing.T) {
	s := newSession("a a a", "b b b")
	if err := s.sub(t, KindSubstitute, "1", "/.*//"); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "|b b b" {
		t.Errorf("buffer %q, want %q", got, "|b b b")
	}
	if s.cur.Line != 1 || s.cur.Col != 0 {
		t.Errorf("cursor %+v, want line 1 column 0", s.cur)
	}
}

// TestRefusedAtomReachesTheCaller.
//
// internal/regex refuses "\zs", "\@<=" and backreferences with an E-code and a
// named atom, and ":s" has to hand that up rather than turn it into E486. The
// plan holds those atoms behind exactly this refusal, so the day one is missed
// is a message with a name on it and not a substitution that quietly did
// nothing.
func TestRefusedAtomReachesTheCaller(t *testing.T) {
	s := newSession("foo bar")
	err := s.sub(t, KindSubstitute, "1", `/foo\zsbar/X/`)
	if err == nil {
		t.Fatal("a refused atom substituted something")
	}
	if _, ok := err.(regex.Refused); !ok {
		t.Errorf("the error is %T (%v), want a regex refusal", err, err)
	}
	if got := s.dump(); got != "foo bar" {
		t.Errorf("the buffer changed: %q", got)
	}
}

// TestNoMagicSwapsAmpersandAndTilde. With 'nomagic' the escaped spellings are
// the special ones, both measured.
func TestNoMagicSwapsAmpersandAndTilde(t *testing.T) {
	s := newSession("one two three")
	s.set(t, "nomagic")
	if err := s.sub(t, KindSubstitute, "1", "/two/[&]/"); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "one [&] three" {
		t.Errorf("buffer %q, want %q", got, "one [&] three")
	}

	s = newSession("one two three")
	s.set(t, "nomagic")
	if err := s.sub(t, KindSubstitute, "1", `/two/[\&]/`); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "one [two] three" {
		t.Errorf("buffer %q, want %q", got, "one [two] three")
	}
}

// TestPatternSpanningALineBreakIsNotDone is a gap, written down as a test so
// it is a failure with a name the day somebody implements it and forgets to
// delete this.
//
// Vim substitutes across a line break: over "abc", "def", "ghi",
// ":%s/c\ndef/Z/" leaves "abZ" and "ghi", measured. This package matches one
// line at a time, so the same command finds nothing. Nothing silently wrong
// happens -- the answer is E486 with the pattern in it -- and no pattern in
// ~/.vimrc or in the four nofrils files spans a line.
func TestPatternSpanningALineBreakIsNotDone(t *testing.T) {
	s := newSession("abc", "def", "ghi")
	err := s.sub(t, KindSubstitute, "%", `/c\ndef/Z/`)
	if err == nil {
		t.Fatal("a multi-line pattern matched; delete this test and its note in STATUS")
	}
	if !strings.HasPrefix(err.Error(), "E486:") {
		t.Errorf("a multi-line pattern gave %v, want E486", err)
	}
	if got := s.dump(); got != "abc|def|ghi" {
		t.Errorf("the buffer changed: %q", got)
	}
}

// TestSubstituteDoesNotLoopOnAGrowingReplacement. ":s/a/aa/g" terminates
// because every match is found in the line as it was and not in the line the
// replacement left behind.
func TestSubstituteDoesNotLoopOnAGrowingReplacement(t *testing.T) {
	s := newSession("aaa")
	if err := s.sub(t, KindSubstitute, "1", "/a/aa/g"); err != nil {
		t.Fatal(err)
	}
	if got := s.dump(); got != "aaaaaa" {
		t.Errorf("buffer %q, want %q", got, "aaaaaa")
	}
}

// TestUndoTakesTheWholeSubstituteBack. Do writes one line at a time and the
// undo block around the lot is the ex layer's, so a ":u" after a ":%s" is one
// step and not one per line. This is the half this package can prove: every
// write joins the block that was open when Do started.
func TestUndoTakesTheWholeSubstituteBack(t *testing.T) {
	s := newSession("a1", "a2", "a3")
	s.buf.OpenUndoBlock(s.cur)
	if err := s.sub(t, KindSubstitute, "%", "/a/X/"); err != nil {
		t.Fatal(err)
	}
	s.buf.CloseUndoBlock()
	if got := s.dump(); got != "X1|X2|X3" {
		t.Fatalf("buffer %q", got)
	}
	if _, ok := s.buf.Undo(); !ok {
		t.Fatal("nothing to undo after a substitute")
	}
	if got := s.dump(); got != "a1|a2|a3" {
		t.Errorf("after one u the buffer is %q, want the original", got)
	}
}
