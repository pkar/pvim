package regex

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestTranslateSource pins the Go source a pattern turns into.
//
// The table test already checks that the patterns match the right thing, which
// is what actually matters; this checks that they do it by the intended route.
// A `\+` that came out as `{1,}` matches identically and is still a sign that
// something is confused, and this is the test that says so.
func TestTranslateSource(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    string
	}{
		{`abc`, `abc`},
		{`a\+`, `a+`},
		{`a\?`, `a?`},
		{`a\=`, `a?`},
		{`a\{2,5}`, `a{2,5}`},
		{`a\{-}`, `a*?`},
		{`a\{-2,5}`, `a{2,5}?`},
		{`a\{,5}`, `a{0,5}`},
		{`a\{}`, `a*`},
		{`\(a\)\|\%(b\)`, `(a)|(?:b)`},
		{`\<foo\>`, `\bfoo\b`},
		{`\%^a\%$`, `\Aa\z`},
		{`\s\+$`, `[ \t]+$`},
		{`\S`, `[^ \t\n]`},
		{`\_s`, `[ \t\n]`},
		{`\_S`, `[^ \t]`},
		{`\_.`, `(?s:.)`},
		{`\_[ab]`, `[ab\n]`},
		{`[^ab]`, `[^ab\n]`},
		{`\_[^ab]`, `[^ab]`},
		{`\%d65`, `A`},
		{`\%x2a`, `\*`},
		{`\%o570`, `/0`},
		{`\%u20ac`, `€`},
		{`\v(a|b)+`, `(a|b)+`},
		{`\v%(ab)`, `(?:ab)`},
		{`\V.`, `\.`},
		{`\V\.`, `.`},
		{`\M\[a-c]`, `[a-c]`},
		{`a^b`, `a\^b`},
		{`a$b`, `a\$b`},
		{`~`, `(?:xy)`},
		{`\t\e\r`, `\t\x{1b}\r`},
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			got, err := Translate(tc.pattern, Options{LastSubstitute: "xy"})
			if err != nil {
				t.Fatalf("Translate(%q): %v", tc.pattern, err)
			}
			want := "(?m)" + tc.want
			if got != want {
				t.Errorf("Translate(%q) = %q; want %q", tc.pattern, got, want)
			}
		})
	}
}

// TestCaseFlags covers 'ignorecase', 'smartcase' and the \c and \C atoms, which
// between them decide whether the "/" prompt is case sensitive and which nobody
// can hold in their head all at once.
func TestCaseFlags(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		opt     Options
		want    bool
	}{
		{`foo`, Options{}, false},
		{`foo`, Options{IgnoreCase: true}, true},
		{`Foo`, Options{IgnoreCase: true}, true},
		{`foo`, Options{IgnoreCase: true, SmartCase: true}, true},
		{`Foo`, Options{IgnoreCase: true, SmartCase: true}, false},
		{`FOO`, Options{SmartCase: true}, false},
		{`\cfoo`, Options{}, true},
		{`foo\c`, Options{}, true},
		{`\Cfoo`, Options{IgnoreCase: true}, false},
		{`\Cfoo\c`, Options{}, true},
		{`\cfoo\C`, Options{}, true},

		// The smartcase scan skips what follows a backslash, so a class or a
		// numeric escape with a capital in it does not make a search
		// case-sensitive. \%d65 is an A and it does not count; a bare A does.
		{`\A`, Options{IgnoreCase: true, SmartCase: true}, true},
		{`\_S`, Options{IgnoreCase: true, SmartCase: true}, true},
		{`\%d65`, Options{IgnoreCase: true, SmartCase: true}, true},
		{`a\%d65A`, Options{IgnoreCase: true, SmartCase: true}, false},
		{`é`, Options{IgnoreCase: true, SmartCase: true}, true},
		{`É`, Options{IgnoreCase: true, SmartCase: true}, false},
	} {
		re, err := Compile(tc.pattern, tc.opt)
		if err != nil {
			t.Fatalf("Compile(%q, %+v): %v", tc.pattern, tc.opt, err)
		}
		if re.IgnoreCase() != tc.want {
			t.Errorf("Compile(%q, ignorecase=%v smartcase=%v).IgnoreCase() = %v; want %v",
				tc.pattern, tc.opt.IgnoreCase, tc.opt.SmartCase, re.IgnoreCase(), tc.want)
		}
	}
}

// TestLineBreaks is the half of the behaviour the oracle table cannot carry.
//
// vim answers the table through match() over a String, where a line break is an
// ordinary character; in a buffer it is a line break, and that is the reading
// pvim implements. So these are asserted here, against text with real newlines
// in it, and they are the rules that matter the first time a pattern is run
// over more than one line.
func TestLineBreaks(t *testing.T) {
	const text = "ab\ncd"
	for _, tc := range []struct {
		pattern string
		want    string // the matched text, or "" for no match at all
		matched bool
	}{
		{`a.\+`, "ab", true},         // . stops at the line break
		{`\_.\+`, "ab\ncd", true},    // \_. does not
		{`[^x]\+`, "ab", true},       // a negated collection stops
		{`\_[^x]\+`, "ab\ncd", true}, // its \_ form does not
		{`\S\+`, "ab", true},         // so does a negated class
		{`\_S\+`, "ab\ncd", true},    //
		{`^cd`, "cd", true},          // ^ is any line start
		{`ab$`, "ab", true},          // $ is any line end
		{`\%^cd`, "", false},         // \%^ is the start of the text only
		{`\%^ab`, "ab", true},        //
		{`ab\%$`, "", false},         // \%$ is the end of the text only
		{`cd\%$`, "cd", true},        //
		{`ab\ncd`, "ab\ncd", true},   // \n is the line break itself
		{`b\_sc`, "b\nc", true},      // and so is \_s
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			re, err := Compile(tc.pattern, Options{})
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.pattern, err)
			}
			loc := re.FindStringIndex(text)
			if (loc != nil) != tc.matched {
				t.Fatalf("%q over %q: matched %v, want %v (source %s)", tc.pattern, text, loc != nil, tc.matched, re.Source())
			}
			if loc == nil {
				return
			}
			if got := text[loc[0]:loc[1]]; got != tc.want {
				t.Errorf("%q over %q matched %q; want %q (source %s)", tc.pattern, text, got, tc.want, re.Source())
			}
		})
	}
}

// TestSyntaxErrors covers the patterns nobody would run, which have to come
// back as a SyntaxError and not as a Refused. The distinction is what the
// message line says: one is "pvim will not do that", the other is "you typed
// that wrong".
func TestSyntaxErrors(t *testing.T) {
	for _, pattern := range []string{
		`a\`,
		`\(a`,
		`a\)`,
		`\v+`,
		`\v?`,
		`\M\*`,
		`a\{2`,
		`\_`,
		`\_q`,
		`\%`,
		`\%q`,
		`[z-a]`,
	} {
		t.Run(pattern, func(t *testing.T) {
			_, err := Compile(pattern, Options{})
			if err == nil {
				t.Fatalf("Compile(%q) succeeded", pattern)
			}
			var ref Refused
			if errors.As(err, &ref) {
				t.Fatalf("Compile(%q) = %T, a refusal; a broken pattern is a SyntaxError", pattern, err)
			}
			var se SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Compile(%q) = %T (%v); want SyntaxError", pattern, err, err)
			}
		})
	}
}

// TestCompileErrorCarriesTheSource covers the one thing RE2 refuses that vim
// takes: a repeat count over a thousand. The emitted source has to be in the
// message, because it is the only way to tell a limit from a translator bug.
func TestCompileErrorCarriesTheSource(t *testing.T) {
	_, err := Compile(`a\{1001}`, Options{})
	var ce CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("Compile(a\\{1001}) = %v; want a CompileError", err)
	}
	if !strings.Contains(ce.Source, "a{1001}") {
		t.Errorf("the error does not carry the emitted source: %v", err)
	}
}

// TestMagicTable walks the magic table from both directions, because the whole
// dialect is that table and a row of it flipped is a pattern that means
// something else and still compiles.
func TestMagicTable(t *testing.T) {
	for c, levels := range bareSpecial {
		for m := VeryMagic; m <= VeryNoMagic; m++ {
			bare := bareIsOperator(c, m)
			escaped := escapedIsOperator(c, m)
			if bare == escaped {
				t.Errorf("at %s, %q is an operator both bare and escaped, or neither", m, string(c))
			}
			if bare != levels[m] {
				t.Errorf("at %s, bareIsOperator(%q) = %v; the table says %v", m, string(c), bare, levels[m])
			}
		}
	}
	// A character not in the table is a literal wherever it stands.
	for _, c := range []byte{'a', 'Z', '0', '_', ':', ',', ';', '!', '"'} {
		for m := VeryMagic; m <= VeryNoMagic; m++ {
			if bareIsOperator(c, m) || escapedIsOperator(c, m) {
				t.Errorf("%q is an operator at %s and should be a literal", string(c), m)
			}
		}
	}
}

// TestRegexpAccessors covers the surface the rest of the editor sees.
func TestRegexpAccessors(t *testing.T) {
	re, err := Compile(`\(a\)\(b\)`, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if re.String() != `\(a\)\(b\)` {
		t.Errorf("String() = %q; a Regexp prints as the pattern that was typed", re.String())
	}
	if re.NumSubexp() != 2 {
		t.Errorf("NumSubexp() = %d; want 2", re.NumSubexp())
	}
	if got := re.FindStringSubmatchIndex("xaby"); len(got) != 6 || got[0] != 1 || got[2] != 1 || got[4] != 2 {
		t.Errorf("FindStringSubmatchIndex = %v; want the match at 1 and the two groups at 1 and 2", got)
	}
	if got := re.FindAllStringIndex("ab ab", -1); len(got) != 2 {
		t.Errorf("FindAllStringIndex found %d matches; want 2", len(got))
	}
	if !re.Match([]byte("xaby")) || !re.MatchString("xaby") {
		t.Error("Match and MatchString disagree with FindStringIndex")
	}
}

// TestKnownGaps is the package doc's list of differences from vim, as a test.
//
// Every case here was measured against /opt/homebrew/bin/vim and the comment
// says what vim answered. They are here so that a gap closing is a test failing
// and not something nobody notices for a year.
func TestKnownGaps(t *testing.T) {
	// \< and \> are Go's \b, which is one symmetric ASCII boundary where vim
	// has two directional 'iskeyword'-aware ones.
	if got := find(t, `\>`, "AĀĀ0"); got != 0 {
		// vim: 6, the end of the word.
		t.Errorf(`\> over "AĀĀ0" matched at %d; the gap says 0 where vim says 6`, got)
	}
	if got := find(t, `\<é`, " été"); got != 4 {
		// vim: 1, the start of the first word. Go's \b has never heard of é, so
		// the only boundary it can see in front of one is the one at byte 4,
		// where an ASCII t sits behind it.
		t.Errorf(`\<é over " été" matched at %d; the gap says 4 where vim says 1`, got)
	}

	// Vim's NFA engine takes a longer match than a backtracking search on an
	// alternation with an empty branch. vim as it ships says "0"; vim with
	// \%#=1, Perl and Go all say the empty string.
	if got := find(t, `^[[:digit:]aa-c]\{-0,2}\|.\{-}`, "09)中"); got != 0 {
		t.Errorf("got %d; want an empty match at 0", got)
	}
	if re := mustCompile(t, `^[[:digit:]aa-c]\{-0,2}\|.\{-}`); re.FindStringIndex("09)中")[1] != 0 {
		t.Error("the match is not empty; the gap says pvim stops where a backtracking search would")
	}

	// A loop whose body can match nothing. Both of vim's engines run the body
	// once and let it go as far as it can; RE2 takes zero iterations.
	if re := mustCompile(t, `\%(^#\{-}\)*`); re.FindStringIndex("#9")[1] != 0 {
		// vim: "#", with either engine.
		t.Error(`\%(^#\{-}\)* over "#9" matched something; the gap says it matches nothing and vim matches "#"`)
	}

	// \k and \K used to be three Unicode categories and a hope. They are
	// vim's own table now, so the emoji and the non-ASCII digit that were the
	// registered gaps are matches, and the rows stay to say so.
	if !mustCompile(t, `\k`).MatchString("😀") {
		t.Error(`\k does not match an emoji; vim's does`)
	}
	if !mustCompile(t, `\k`).MatchString("中") {
		t.Error(`\k does not match 中, which is not a gap, it is a bug`)
	}
	if !mustCompile(t, `\K`).MatchString("٣") {
		t.Error(`\K does not match an Arabic-Indic digit; vim only excludes the ASCII ones`)
	}

	// The collection parser bug in vim 9.2: a dash in range position before a
	// [: turns the class's own ] into the collection's terminator and leaves a
	// literal ] behind.
	if !mustCompile(t, `[a-c-[:alpha:]]`).MatchString("a") {
		// vim: no match, because it wants an "a]" there.
		t.Error(`[a-c-[:alpha:]] does not match "a"; pvim reads it as {a-c, -, alpha} and vim does not`)
	}

	// Case folding, which is not a gap: both fold the Kelvin sign and the long
	// s the same way, and the row is here so that a change in either is visible.
	if !mustCompile(t, `\ck`).MatchString("K") {
		t.Error(`\ck does not match the Kelvin sign; vim's does`)
	}
	if !mustCompile(t, `\cs`).MatchString("ſ") {
		t.Error(`\cs does not match the long s; vim's does`)
	}
}

func mustCompile(t *testing.T, pattern string) *Regexp {
	t.Helper()
	re, err := Compile(pattern, Options{})
	if err != nil {
		t.Fatalf("Compile(%q): %v", pattern, err)
	}
	return re
}

func find(t *testing.T, pattern, s string) int {
	t.Helper()
	loc := mustCompile(t, pattern).FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// TestDollarAcrossSwitchAtoms covers the `$` that is not the last thing in the
// pattern only because a case or magic switch follows it.
//
// Vim's peekchr skips the whole run of \c \C \m \M \v \V \Z after a `$` before
// it decides whether the `$` is the end-of-line anchor, and appending \c to a
// search to make it case-insensitive is the standard vim habit, so this shape
// is common and wrong in both directions when the skip is missing. Every row is
// what /opt/homebrew/bin/vim answered through matchstrpos().
func TestDollarAcrossSwitchAtoms(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		in      string
		want    int // where the match starts, -1 for no match
	}{
		{`error$\c`, "an error", 3},
		{`error$\c`, "an error$", -1},
		{`\.go$\c`, "main.go", 4},
		{`foo$\m`, "a foo", 2},
		{`foo$\M`, "a foo", 2},
		{`foo$\V`, "a foo", 2},
		{`foo$\V`, "a foo$", -1},
		{`foo$\c\C\m`, "a foo", 2},
		{`a$\c\|b`, "xa", 1},

		// A \v after the `$` decides what the `|` behind it spells, which is
		// why the skip has to track the level and not only step over it.
		{`a$\v|b`, "xa", 1},
		{`a$\v|b`, "xab", 2},

		// Anything that is not a switch atom still leaves a literal dollar.
		{`a$\cb`, "a$b", 0},
		{`a$\cb`, "ab", -1},
	} {
		t.Run(tc.pattern+"/"+tc.in, func(t *testing.T) {
			if got := find(t, tc.pattern, tc.in); got != tc.want {
				t.Errorf("%q over %q matched at %d; vim says %d (source %s)",
					tc.pattern, tc.in, got, tc.want, mustCompile(t, tc.pattern).Source())
			}
		})
	}
}

// TestVeryNoMagicAnchors covers `\^` and `\$` under \V, which are the anchors
// wherever they stand.
//
// At MAGIC_NONE peekchr turns `\^` and `\$` into the magic form with no
// position test at all, so the at-start and end-of-branch rules that govern the
// bare forms never get asked. \M is the level that behaves the other way, and
// it is here so that the two cannot be confused for each other again.
func TestVeryNoMagicAnchors(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		in      string
		want    int
	}{
		{`\Vfoo\$bar`, "foo$bar", -1},
		{`\V\$foo`, "$foo", -1},
		{`\Vfoo\^bar`, "foo^bar", -1},
		{`\V\^foo`, "foo", 0},
		{`\Vfoo\$`, "foo", 0},
		{`\Vfoo\$\|zzz`, "foo", 0},

		// The bare forms at \V are literals, which pvim already had right.
		{`\Vfoo$bar`, "foo$bar", 0},
		{`\Vfoo^bar`, "foo^bar", 0},

		// \M keeps the position rules: there `\$` is the literal.
		{`\Mfoo\$bar`, "foo$bar", 0},
		{`\Mfoo\^bar`, "foo^bar", 0},
	} {
		t.Run(tc.pattern+"/"+tc.in, func(t *testing.T) {
			if got := find(t, tc.pattern, tc.in); got != tc.want {
				t.Errorf("%q over %q matched at %d; vim says %d (source %s)",
					tc.pattern, tc.in, got, tc.want, mustCompile(t, tc.pattern).Source())
			}
		})
	}
}

// TestUnclosedCollection is the fallback that makes /[ a working search.
//
// Vim decides whether a collection closes before it reads a single member, so a
// reversed range or an unknown class name inside a bracket that never closes is
// not an error: the bracket was a literal all along. With 'incsearch' on, every
// prefix of what is typed gets compiled, so /[z-a] would otherwise raise an
// error on the fifth keystroke.
func TestUnclosedCollection(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		in      string
		want    string // the matched text, "" for no match
	}{
		{`[c-a`, "x[c-ay", "[c-a"},
		{`\_[c-a`, "x[c-ay", "[c-a"},
		{`[[=a=]`, "x[ay", "[a"},
		{`[`, "a[b", "["},

		// An unknown class name is not an error in vim either, closed or not:
		// the bracket and the name are ordinary members.
		{`[[:bogus:]`, "sob", "s"},
		{`[[:bogus:]`, "x[q-ay", "["},
		{`[[:nosuch:]`, "xn]z", "n"},
		{`[[:nosuch:]]`, "xn]z", "n]"},

		// With the second bracket the collection closes on the first `]` and
		// the second one is a literal, which is vim's answer and not a typo.
		{`[[:bogus:]]`, "sob", ""},
		{`[[:bogus:]]`, "so]b", "o]"},
	} {
		t.Run(tc.pattern+"/"+tc.in, func(t *testing.T) {
			re, err := Compile(tc.pattern, Options{})
			if err != nil {
				t.Fatalf("Compile(%q): %v", tc.pattern, err)
			}
			got := ""
			if loc := re.FindStringIndex(tc.in); loc != nil {
				got = tc.in[loc[0]:loc[1]]
			}
			if got != tc.want {
				t.Errorf("%q over %q matched %q; vim matches %q (source %s)",
					tc.pattern, tc.in, got, tc.want, re.Source())
			}
		})
	}

	// A collection that does close keeps its errors, because vim raises them
	// too: E944 for the reversed range, and an equivalence class is a refusal.
	if _, err := Compile(`[z-a]`, Options{}); err == nil {
		t.Error("Compile(`[z-a]`) succeeded; a closed reversed range is E944 in vim")
	}
	var unsupported UnsupportedError
	if _, err := Compile(`[[=a=]]`, Options{}); !errors.As(err, &unsupported) {
		t.Errorf("Compile(`[[=a=]]`) = %v; want an UnsupportedError", err)
	}
}

// TestCharacterClassPoints pins the code points where the option-driven classes
// were guessed rather than measured.
//
// Every answer is what /opt/homebrew/bin/vim said about the single character:
// nr2char(cp) =~# '^\k$' and the same for \K, \p, \P, [[:lower:]] and
// [[:upper:]].
func TestCharacterClassPoints(t *testing.T) {
	for _, tc := range []struct {
		atom string
		r    rune
		want bool
		why  string
	}{
		// \p and [[:print:]] are vim's utf_printable, which has holes in it
		// where the zero-width and bidi format characters are.
		{`\p`, 0x200b, false, "zero width space"},
		{`\p`, 0xfeff, false, "byte order mark"},
		{`\p`, 0x202a, false, "left-to-right embedding"},
		{`\p`, 0x2060, false, "word joiner"},
		{`\p`, 0x070f, false, "syriac abbreviation mark"},
		{`\p`, 0x00e9, true, "e acute"},
		{`\p`, 0x4e2d, true, "CJK"},
		{`\P`, 0x200b, false, "zero width space"},

		// \k is utf_class, which defaults to "word" for everything it has no
		// punctuation or space entry for. That is much wider than three Unicode
		// categories, in both directions.
		{`\k`, 0x00aa, false, "feminine ordinal indicator"},
		{`\k`, 0x00ba, false, "masculine ordinal indicator"},
		{`\k`, 0x20d0, false, "combining left harpoon above"},
		{`\k`, 0x3005, false, "ideographic iteration mark"},
		{`\k`, 0x1d7ce, false, "mathematical bold digit zero"},
		{`\k`, 0x02c2, true, "modifier letter left arrowhead"},
		{`\k`, 0x0375, true, "greek lower numeral sign"},
		{`\k`, 0x2070, true, "superscript zero"},
		{`\k`, 0xfeff, true, "byte order mark"},
		{`\k`, 0x1f600, true, "grinning face"},
		{`\K`, 0x0663, true, "arabic-indic digit three"},
		{`\K`, '7', false, "ascii digit"},

		// [[:lower:]] and [[:upper:]] ask whether the character has a case
		// counterpart, which is not the same question as its Unicode category.
		{`[[:lower:]]`, 0x0138, false, "kra, an Ll with no upper case"},
		{`[[:lower:]]`, 0x01c5, true, "a titlecase letter"},
		{`[[:upper:]]`, 0x01c5, true, "a titlecase letter"},
		{`[[:upper:]]`, 0x03d2, false, "upsilon with hook, an Lu with no lower case"},
		{`[[:lower:]]`, 0x00df, true, "sharp s, lower with no upper case"},
		{`[[:lower:]]`, 0x2170, true, "small roman numeral one, an Nl with a case pair"},
		{`[[:upper:]]`, 0x2160, true, "roman numeral one"},
		{`[[:lower:]]`, 0x0250, true, "latin small letter turned a"},
	} {
		t.Run(fmt.Sprintf("%s/U+%04X", tc.atom, tc.r), func(t *testing.T) {
			got := mustCompile(t, tc.atom).MatchString(string(tc.r))
			if got != tc.want {
				t.Errorf("%s over U+%04X (%s) = %v; vim says %v (source %s)",
					tc.atom, tc.r, tc.why, got, tc.want, mustCompile(t, tc.atom).Source())
			}
		})
	}

	// The whole-atom shapes the finding was reported with.
	for _, tc := range []struct{ pattern, in, want string }{
		{`\p\+`, "ab​cd", "ab"},
		{`[[:print:]]\+`, "ab​cd", "ab"},
		{`\P\+`, "ab​cd", "ab"},
		{`\k\+`, "aª", "a"},
		{`\k\+`, "a⁰", "a⁰"},
	} {
		re := mustCompile(t, tc.pattern)
		got := ""
		if loc := re.FindStringIndex(tc.in); loc != nil {
			got = tc.in[loc[0]:loc[1]]
		}
		if got != tc.want {
			t.Errorf("%q over %q matched %q; vim matches %q", tc.pattern, tc.in, got, tc.want)
		}
	}
}
