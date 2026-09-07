package search

import (
	"reflect"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// TestCrossesLines is the decision that picks the fast path. Getting it wrong
// in one direction costs a scan of the buffer per search and in the other
// loses every multi-line match, so both directions are here, over the source
// internal/regex actually emits rather than over the vim pattern.
func TestCrossesLines(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"foo", false},
		{".", false},
		{"^foo$", false},
		{"[a-z]", false},
		{`\s`, false},
		{`\w\+`, false},
		{`[^a]`, false},
		{`foo\nbar`, true},
		{`\n`, true},
		{`\_.`, true},
		{`\_s`, true},
		{`\_S`, true},
		{`\_a`, true},
		{`\_[a-z]`, true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			re, err := Compile(tc.pattern, DefaultOptions())
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			if got := crossesLines(re.Source()); got != tc.want {
				t.Errorf("crossesLines(%q) = %v, want %v", re.Source(), got, tc.want)
			}
		})
	}
}

// TestFindLine is what 'hlsearch' walks: every non-overlapping match on one
// line, and nothing at all off the ends of the buffer.
func TestFindLine(t *testing.T) {
	b := text.Read([]byte("ab ab ab\ncd\n"))
	re, err := Compile("ab", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{Start: text.Pos{Line: 1, Col: 0}, End: text.Pos{Line: 1, Col: 2}},
		{Start: text.Pos{Line: 1, Col: 3}, End: text.Pos{Line: 1, Col: 5}},
		{Start: text.Pos{Line: 1, Col: 6}, End: text.Pos{Line: 1, Col: 8}},
	}
	if got := FindLine(b, re, 1); !reflect.DeepEqual(got, want) {
		t.Errorf("FindLine(1) = %+v, want %+v", got, want)
	}
	if got := FindLine(b, re, 2); got != nil {
		t.Errorf("FindLine(2) = %+v, want none", got)
	}
	for _, n := range []int{0, 3, -1} {
		if got := FindLine(b, re, n); got != nil {
			t.Errorf("FindLine(%d) = %+v, want none", n, got)
		}
	}
}

// TestFindAcrossLines: a match that starts on one line and ends on another
// comes back with both ends in the right place, which is the whole point of
// the second path through the matcher.
func TestFindAcrossLines(t *testing.T) {
	b := text.Read([]byte("foo\nbar\nbaz\n"))
	re, err := Compile(`foo\nbar`, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	hit, err := Find(b, Query{Re: re, From: text.Pos{Line: 3, Col: 0}, Dir: Forward}, DefaultOptions())
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := Match{Start: text.Pos{Line: 1, Col: 0}, End: text.Pos{Line: 2, Col: 3}}
	if hit.Match != want {
		t.Errorf("match = %+v, want %+v", hit.Match, want)
	}
	if !hit.Wrapped {
		t.Error("the scan came round the bottom and did not say so")
	}
}

// TestFindErrors: the three failures a search can report, each with vim's own
// text on it, because cmd/oracle diffs the message line.
func TestFindErrors(t *testing.T) {
	b := text.Read([]byte("foo\nbar\n"))
	re, err := Compile("zzz", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	q := Query{Re: re, From: text.Pos{Line: 1, Col: 0}, Dir: Forward}

	if _, err := Find(b, q, DefaultOptions()); err == nil ||
		err.Error() != "E486: Pattern not found: zzz" {
		t.Errorf("wrapscan on: %v", err)
	}

	off := Options{}
	if _, err := Find(b, q, off); err == nil ||
		err.Error() != "E385: Search hit BOTTOM without match for: zzz" {
		t.Errorf("wrapscan off, forward: %v", err)
	}

	q.Dir = Backward
	if _, err := Find(b, q, off); err == nil ||
		err.Error() != "E384: Search hit TOP without match for: zzz" {
		t.Errorf("wrapscan off, backward: %v", err)
	}
}

// TestNoPattern: n with nothing behind it is E35, and so is a bare / with
// nothing behind it.
func TestNoPattern(t *testing.T) {
	b := text.Read([]byte("foo\n"))
	s := &State{}
	const want = "E35: No previous regular expression"
	if _, err := s.Next(b, text.Pos{Line: 1}, false, 1, DefaultOptions()); err == nil || err.Error() != want {
		t.Errorf("Next with no pattern: %v", err)
	}
	if _, err := s.Do(b, text.Pos{Line: 1}, Forward, "", 1, DefaultOptions()); err == nil || err.Error() != want {
		t.Errorf("Do with an empty line and no pattern: %v", err)
	}
}

// TestFailedSearchStillRemembersThePattern: vim sets "/ before it decides
// whether the search worked, so the n after a failed / looks for the same
// thing rather than for whatever was there before.
func TestFailedSearchStillRemembersThePattern(t *testing.T) {
	b := text.Read([]byte("foo\n"))
	s := &State{}
	if _, err := s.Do(b, text.Pos{Line: 1}, Forward, "zzz", 1, DefaultOptions()); err == nil {
		t.Fatal("a search for zzz in a buffer with no zzz in it succeeded")
	}
	if s.Pattern != "zzz" {
		t.Errorf("pattern = %q, want zzz", s.Pattern)
	}
}
