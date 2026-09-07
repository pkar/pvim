package substitute

import (
	"strings"
	"testing"
)

// TestExpandAgainstVim is the replacement grammar, one row per answer read off
// vim 9.2.321. The match in every row is the word "two" in "one two three",
// with group 1 "o" and group 2 "two" where the row uses them, so that the row
// reads as the ":s" it came from.
func TestExpandAgainstVim(t *testing.T) {
	groups := [][]byte{[]byte("two"), []byte("o"), []byte("two")}
	for _, c := range []struct {
		repl  string
		magic bool
		want  string
	}{
		// Groups and the whole match.
		{`<\0>`, true, "<two>"},
		{`[&]`, true, "[two]"},
		{`[\&]`, true, "[&]"},
		{`\2-\1`, true, "two-o"},
		{`[&]`, false, "[&]"},
		{`[\&]`, false, "[two]"},

		// Case folding. \u and \l reach one character; \U and \L run until
		// \e or \E; a one-character fold inside a run applies once and the
		// run carries on after it.
		{`\u&`, true, "Two"},
		{`\uabc`, true, "Abc"},
		{`\U&\Edone`, true, "TWOdone"},
		{`\Uab\Ecd`, true, "ABcd"},
		{`\Uab\ecd`, true, "ABcd"},
		{`\U&\lx`, true, "TWOx"},
		{`\Uab\lCD`, true, "ABcD"},
		{`\Lab\uCD`, true, "abCd"},
		{`\LABC`, true, "abc"},
		{`ab\u`, true, "ab"},

		// The fold reaches a whole character and not a byte, so "\uünicode"
		// is "Ünicode" and "\Uaüb" is "AÜB". Both measured.
		{`\uünicode`, true, "Ünicode"},
		{`\lÜNICODE`, true, "üNICODE"},
		{`\Uaüb`, true, "AÜB"},
		{`\LAÜB`, true, "aüb"},

		// The escapes, including the two everybody gets backwards.
		{`A\rB`, true, "A\nB"},
		{`A\nB`, true, "A\x00B"},
		{`A\tB`, true, "A\tB"},
		{`A\bB`, true, "A\bB"},
		{`a\\b`, true, `a\b`},
		{`\q\d`, true, "qd"},

		// A case run survives a line break, which is what makes
		// ":s/two/\Uab\rcd/" two upper case lines and not one.
		{`\Uab\rcd`, true, "AB\nCD"},

		// A one-shot fold does NOT survive one. Vim's vim_regsub_both
		// pushes the CR and the NUL through func_one like any other
		// character: do_upper hands the character back unchanged and clears
		// the one-shot, where do_Upper hands itself back and the run stays.
		// So the "\u" is spent on the break and the character after it is
		// left alone. On "abc", ":s/a/\u\rx/" leaves an empty line and a
		// LOWER case x, and ":s/a/\u\nx/" a NUL and the same x. Measured
		// both ways, and the reason "\t" and "\b" go through write() too.
		{`\u\rx`, true, "\nx"},
		{`\u\nx`, true, "\x00x"},
		{`\l\rY`, true, "\nY"},
		{`\U\rx`, true, "\nX"},
	} {
		t.Run(c.repl, func(t *testing.T) {
			got, err := Expand(c.repl, groups, c.magic)
			if err != nil {
				t.Fatalf("Expand(%q): %v", c.repl, err)
			}
			if string(got) != c.want {
				t.Errorf("Expand(%q, magic=%v) = %q, want %q", c.repl, c.magic, got, c.want)
			}
		})
	}
}

// TestExpandGroupThatDidNotMatch. A group that took no part in the match
// renders as nothing rather than as an error, which is what vim does and what
// makes ":s/\(a\)\|b/[\1]/" survive the b.
func TestExpandGroupThatDidNotMatch(t *testing.T) {
	got, err := Expand(`[\1][\9]`, [][]byte{[]byte("b"), nil}, true)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "[][]" {
		t.Errorf("got %q, want %q", got, "[][]")
	}
}

// TestExpressionReplacementIsRefusedByName.
//
// pvim refuses "\=" rather than mistranslating it, because there is no
// expression evaluator and there is not going to be one. The refusal carries
// an E-code so that a habit keyed to one still works, and says what happened
// in pvim's own words, which is difference D-002.
func TestExpressionReplacementIsRefusedByName(t *testing.T) {
	msg := ErrExpression.Error()
	if !strings.HasPrefix(msg, "E479:") {
		t.Errorf("the refusal is %q and does not start with an E-code", msg)
	}
	if !strings.Contains(msg, `\=`) {
		t.Errorf("the refusal is %q and does not say which atom was refused", msg)
	}
	if _, err := Expand(`\=1+1`, [][]byte{[]byte("x")}, true); err != ErrExpression {
		t.Errorf("Expand of an expression gave %v, want the refusal", err)
	}
}

// TestTildeExpansion is regtilde on its own: the stage that runs before Expand
// and that nothing else in the grammar knows about.
func TestTildeExpansion(t *testing.T) {
	for _, c := range []struct {
		in, prev string
		have     bool
		magic    bool
		want     string
	}{
		{"~Y", "X", true, true, "XY"},
		{`\~Z`, "XY", true, true, `\~Z`}, // left for Expand to unescape
		{"~", "", false, true, ""},       // no previous substitute at all
		{"a~b~c", "-", true, true, "a-b-c"},
		{`\Uz`, "", true, true, `\Uz`},    // no tilde, nothing happens
		{"~q", `\Uz`, true, true, `\Uzq`}, // the inserted text is not rescanned
		{"~", "X", true, false, "~"},      // nomagic: a bare tilde is literal
		{`\~`, "X", true, false, "X"},     // nomagic: the escaped one expands
	} {
		if got := tilde(c.in, c.prev, c.have, c.magic); got != c.want {
			t.Errorf("tilde(%q, prev=%q, magic=%v) = %q, want %q", c.in, c.prev, c.magic, got, c.want)
		}
	}
}
