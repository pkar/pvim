package search

import (
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// TestWordAt is the pattern * and # build, without a search behind it. The
// oracle checks the whole command; this checks the half that is easy to get
// subtly wrong and hard to see in a cursor position: the escaping and where
// the word starts.
func TestWordAt(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		col     int // 0-based byte column on line 1
		whole   bool
		iskw    string
		pattern string
		start   int
		err     bool
	}{
		{name: "star", in: "foo bar\n", whole: true, pattern: `\<foo\>`},
		{name: "g star", in: "foo bar\n", pattern: "foo"},
		{name: "from inside the word", in: "foo bar\n", col: 2, whole: true, pattern: `\<foo\>`},
		{name: "backs up to the start", in: "foo bar\n", col: 2, pattern: "foo", start: 0},
		{name: "scans forward for a keyword", in: "  ... foo\n", whole: true, pattern: `\<foo\>`, start: 6},
		{name: "falls back to a non-blank run", in: "  ... \n", whole: true, pattern: `\.\.\.`, start: 2},
		{name: "escapes the magic characters", in: "~.^$[*/\\\n", whole: true, pattern: `\~\.\^\$\[\*\/\\`, start: 0},
		{name: "no question mark escape", in: "?? \n", whole: true, pattern: "??", start: 0},
		{name: "digits and underscore are keyword", in: "a1_b x\n", whole: true, pattern: `\<a1_b\>`},
		{name: "latin1 letters are keyword", in: "café x\n", whole: true, pattern: `\<café\>`},
		{name: "cjk is keyword", in: "日本 x\n", whole: true, pattern: `\<日本\>`},
		// The run stops where the SCRIPT changes and not where the keyword
		// characters run out: vim's find_ident_at_pos walks by mb_get_class,
		// and ideographs and Hiragana are two of them. Measured, "#" over
		// this line puts \<日本語\> in @/ and not the whole run. A fuzz
		// script over the wide-rune corpus file found it.
		{name: "cjk stops at hiragana", in: "日本語のテキスト\n", whole: true, pattern: `\<日本語\>`},
		{name: "hiragana is one run of its own", in: "日本語のテキスト\n", col: 9, whole: true, pattern: `\<の\>`, start: 9},
		{name: "katakana starts a third run", in: "日本語のテキスト\n", col: 12, whole: true, pattern: `\<テキスト\>`, start: 12},
		{name: "katakana is its own run", in: "テキストです\n", whole: true, pattern: `\<テキスト\>`},
		{name: "copyright sign is not", in: "©© x\n", whole: true, pattern: `\<x\>`, start: 5},
		{name: "iskeyword adds the dash", in: "foo-bar x\n", whole: true, iskw: "@,48-57,_,192-255,-", pattern: `\<foo-bar\>`},
		{name: "empty line", in: "\nfoo\n", whole: true, err: true},
		{name: "blank line", in: "   \nfoo\n", whole: true, err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := text.Read([]byte(tc.in))
			opt := DefaultOptions()
			opt.IsKeyword = tc.iskw
			w, err := WordAt(b, text.Pos{Line: 1, Col: tc.col}, tc.whole, opt)
			if tc.err {
				if err == nil {
					t.Fatalf("WordAt gave %q, want E348", w.Pattern)
				}
				if got, want := err.Error(), "E348: No string under cursor"; got != want {
					t.Errorf("error = %q, want %q", got, want)
				}
				return
			}
			if err != nil {
				t.Fatalf("WordAt: %v", err)
			}
			if w.Pattern != tc.pattern {
				t.Errorf("pattern = %q, want %q", w.Pattern, tc.pattern)
			}
			if w.Start.Col != tc.start {
				t.Errorf("start col = %d, want %d", w.Start.Col, tc.start)
			}
		})
	}
}

// TestParseIsKeyword covers the option's own grammar, which is not obvious
// from the default value: the bare @ means "the letters", a ^ item takes
// characters back out, and a range is inclusive.
func TestParseIsKeyword(t *testing.T) {
	cases := []struct {
		spec string
		in   []rune
		out  []rune
	}{
		{"", []rune{'a', 'Z', '0', '_', 'é', 'µ', '×'}, []rune{' ', '-', '.', '©', 'ª'}},
		{"a-c", []rune{'a', 'b', 'c'}, []rune{'d', '0', '_'}},
		{"@,48-57,_,192-255,-", []rune{'a', '0', '_', '-'}, []rune{' ', '.'}},
		{"@,^a", []rune{'b', 'Z'}, []rune{'a', '0'}},
		{"48-57", []rune{'0', '9'}, []rune{'a', '_'}},
		{"@-@", []rune{'@'}, []rune{'a', '0'}},
	}
	for _, tc := range cases {
		t.Run(tc.spec, func(t *testing.T) {
			k := parseIsKeyword(tc.spec)
			for _, r := range tc.in {
				if !k.has(r) {
					t.Errorf("%q is not a keyword character and should be", r)
				}
			}
			for _, r := range tc.out {
				if k.has(r) {
					t.Errorf("%q is a keyword character and should not be", r)
				}
			}
		})
	}
}
