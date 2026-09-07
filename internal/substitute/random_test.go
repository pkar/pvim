package substitute

import (
	"math/rand"
	"testing"
)

// corpus is the eight-file shape cmd/oracle fuzzes over, cut down to the
// shapes a substitution can trip on: an empty line, a line of one character,
// tabs, wide runes, and a line with no match anywhere in it.
var corpus = [][]string{
	{""},
	{"a"},
	{"aaa", "bbb", "aaa"},
	{"\tindented\t", "  two  ", ""},
	{"héllo wörld", "ünicode ünicode"},
	{"one", "", "three", "", "five"},
	{"aaaaaaaaaaaaaaaaaaaa"},
	{"no match here at all"},
}

// patterns and replacements are the grammar this walks. Every pattern is one
// internal/regex accepts, because a refused one is its own test.
var (
	patterns     = []string{"a", "a*", "a\\+", "^", "$", ".", ".*", "\\s", "\\w\\+", "[abc]", "\\<a", "o\\|e", "\\d", "é"}
	replacements = []string{"X", "", "&&", "[&]", "\\u&", "\\U&", "a\\rb", "\\0\\0", "~", "\\t"}
)

// TestCountFlagAgreesWithTheRealRun is the property that ties the "n" flag to
// the substitution it does not make: counting has to give the same number as
// doing it. They are two paths through the same loop and only one of them is
// exercised by anything a person types, so this is what stops the quiet one
// rotting.
//
// It also runs several thousand substitutions over the corpus, which is what
// catches a panic on an empty line, a wide rune or a pattern that only matches
// past the end of the line.
func TestCountFlagAgreesWithTheRealRun(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	for i := 0; i < 4000; i++ {
		lines := corpus[rng.Intn(len(corpus))]
		pat := patterns[rng.Intn(len(patterns))]
		rep := replacements[rng.Intn(len(replacements))]
		flags := ""
		if rng.Intn(2) == 0 {
			flags = "g"
		}

		counted := run(t, lines, "/"+pat+"/"+rep+"/"+flags+"n")
		done := run(t, lines, "/"+pat+"/"+rep+"/"+flags)
		if counted.Subs != done.Subs {
			t.Fatalf("s/%s/%s/%s counted %d matches and made %d substitutions over %q",
				pat, rep, flags, counted.Subs, done.Subs, lines)
		}
		if counted.Lines != done.Lines {
			t.Fatalf("s/%s/%s/%s counted %d lines and changed %d over %q",
				pat, rep, flags, counted.Lines, done.Lines, lines)
		}
	}
}

// run does one substitution over a fresh buffer and returns what it did. An
// E486 is an answer and not a failure; anything else is a bug in the package
// or in the grammar above.
func run(t *testing.T, lines []string, args string) Result {
	t.Helper()
	s := newSession(lines...)
	s.st.Replacement, s.st.HaveReplacement = "-", true // so a repeat form has something to repeat
	s.st.Tilde, s.st.HaveTilde = "-", true             // and so "~" has something to be
	cmd, err := Parse(KindSubstitute, args, &s.st, &s.opt)
	if err != nil {
		t.Fatalf("Parse(%q): %v", args, err)
	}
	res, err := Do(Request{Buf: s.buf, First: 1, Last: s.buf.LineCount(), Cmd: cmd, Opt: &s.opt, State: &s.st})
	if err != nil {
		if _, ok := err.(NotFoundError); !ok {
			t.Fatalf("%q over %q: %v", args, lines, err)
		}
	}
	return res
}

// TestMarksSurvivesTheCorpus runs ":g" and ":v" over the same shapes, for the
// same reason: the line walk is short and the ways a buffer can be odd are not.
func TestMarksSurvivesTheCorpus(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 2000; i++ {
		lines := corpus[rng.Intn(len(corpus))]
		pat := patterns[rng.Intn(len(patterns))]
		invert := rng.Intn(2) == 0

		s := newSession(lines...)
		g, err := ParseGlobal("/"+pat+"/d", invert)
		if err != nil {
			t.Fatalf("ParseGlobal(%q): %v", pat, err)
		}
		m, _, err := Marks(s.buf, 1, s.buf.LineCount(), g, &s.st, &s.opt)
		if err != nil {
			t.Fatalf("Marks(%q): %v", pat, err)
		}
		if m.Len() > len(lines) {
			t.Fatalf(":g/%s/ marked %d of %d lines", pat, m.Len(), len(lines))
		}
		for {
			n, ok := m.Next()
			if !ok {
				break
			}
			if n < 1 || n > s.buf.LineCount() {
				t.Fatalf(":g/%s/ marked line %d of %d", pat, n, s.buf.LineCount())
			}
		}
	}
}
