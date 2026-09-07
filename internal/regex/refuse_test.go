package regex

import (
	"errors"
	"fmt"
	"testing"
)

// refusals is every pattern the translator will not take, with the error it has
// to come back with.
//
// One entry per atom and per spelling of that atom, because the spelling is
// where a refusal goes wrong: \%[ and %[ are the same refusal and reach it down
// different paths, and a translator that catches one and silently mistranslates
// the other is the exact failure must not happen.
var refusals = []struct {
	pattern string
	want    error
}{
	{`\zsfoo`, MatchStartError{}},
	{`foo\zsbar`, MatchStartError{}},
	{`\v\zsfoo`, MatchStartError{}},
	{`foo\zebar`, MatchEndError{}},
	{`\(foo\)\@=`, LookaheadError{}},
	{`\v(foo)@=`, LookaheadError{}},
	{`\(foo\)\@!`, NegLookaheadError{}},
	{`\(foo\)\@<=bar`, LookbehindError{}},
	{`\v(foo)@<=bar`, LookbehindError{}},
	{`\(foo\)\@<!bar`, NegLookbehindError{}},
	{`\(foo\)\@>`, AtomicGroupError{}},
	{`\(a\)\1`, BackreferenceError{}},
	{`\(a\)\(b\)\2\1`, BackreferenceError{}},
	{`\%V`, VisualAreaError{}},
	{`\v%V`, VisualAreaError{}},
	{`\%[ab]`, OptionalSequenceError{}},
	{`\v%[ab]`, OptionalSequenceError{}},
	{`\%23l`, LineError{}},
	{`\%<3l`, LineError{}},
	{`\%.lfoo`, LineError{}},
	{`\%23c`, ColumnError{}},
	{`\%>3c`, ColumnError{}},
	{`\%23v`, VirtualColumnError{}},
	{`\%#`, CursorError{}},
	{`\%'a`, MarkError{}},
	{`\%<'ma`, MarkError{}},
	{`\%>'ma`, MarkError{}},
	{`\v%>'ma`, MarkError{}},
	{`a\&b`, BranchError{}},
	{`\va&&b`, BranchError{}},
	{`\Zfoo`, UnsupportedError{}},
	{`\%C`, UnsupportedError{}},
	{`[[=a=]]`, UnsupportedError{}},
	{`\z1`, ExternalMatchError{}},
	{`\z(a\)`, ExternalMatchError{}},
}

// refusalFor gives the error a pattern has to be refused with, for the table
// test to check its rows against.
func refusalFor(pattern string) (error, bool) {
	for _, r := range refusals {
		if r.pattern == pattern {
			return r.want, true
		}
	}
	return nil, false
}

// TestRefusals is the list of what pvim does not do, as a test rather than a
// comment. Deleting an entry is how a refused atom stops being refused.
func TestRefusals(t *testing.T) {
	for _, tc := range refusals {
		t.Run(tc.pattern, func(t *testing.T) {
			_, err := Compile(tc.pattern, Options{})
			if err == nil {
				t.Fatalf("Compile(%q) succeeded; want %T", tc.pattern, tc.want)
			}
			if fmt.Sprintf("%T", err) != fmt.Sprintf("%T", tc.want) {
				t.Fatalf("Compile(%q) = %T (%v); want %T", tc.pattern, err, err, tc.want)
			}

			// Every refusal has to be reachable through the interface, or a
			// caller has to type-switch over eighteen types to tell "pvim will
			// not do this" from "your pattern is broken".
			var ref Refused
			if !errors.As(err, &ref) {
				t.Fatalf("Compile(%q) = %T, which does not satisfy Refused", tc.pattern, err)
			}
			r := ref.Refused()
			if r.Atom == "" {
				t.Errorf("Compile(%q) refused with an empty atom", tc.pattern)
			}
			if r.Col < 0 || r.Col >= len(tc.pattern) {
				t.Errorf("Compile(%q) refused at column %d, which is not in the pattern", tc.pattern, r.Col)
			}
		})
	}
}

// TestRefusalColumns pins the column of a refusal in the middle of a pattern,
// because a column that is always zero passes every other test here and is
// useless on the message line.
func TestRefusalColumns(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		atom    string
		col     int
	}{
		{`foo\zsbar`, `\zs`, 3},
		{`ab\(c\)\@=`, `\@=`, 7},
		{`\vabc%[de]`, `%[`, 5},
		{`x\%23lyz`, `\%23l`, 1},
	} {
		_, err := Compile(tc.pattern, Options{})
		var ref Refused
		if !errors.As(err, &ref) {
			t.Fatalf("Compile(%q) = %v; want a refusal", tc.pattern, err)
		}
		got := ref.Refused()
		if got.Atom != tc.atom || got.Col != tc.col {
			t.Errorf("Compile(%q) refused %q at %d; want %q at %d", tc.pattern, got.Atom, got.Col, tc.atom, tc.col)
		}
	}
}
