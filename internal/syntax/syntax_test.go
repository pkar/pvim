package syntax

import (
	"errors"
	"strings"
	"testing"
)

// The tests that need no vim: the parser, the pattern rewrites, and the
// matcher over rules written here. TestAgreement is the gate; these are what
// says which part broke when it goes red.

// build runs syn commands over an empty rule set, the way a syntax file would.
func build(t *testing.T, cmds ...string) *Syntax {
	t.Helper()
	s := newSyntax("test")
	ev := &evaluator{s: s, vars: map[string]value{}, funcs: map[string]*function{}}
	r := &runner{s: s, ev: ev, file: "test.vim"}
	r.block(join(cmds))
	s.finish()
	return s
}

// paint returns the group name of every byte of a line, as a string with one
// name per run, for comparing against a literal.
func paint(t *testing.T, s *Syntax, src ...string) []string {
	t.Helper()
	h := NewHighlighter(s, lines(src))
	var out []string
	for i := range src {
		var parts []string
		for _, sp := range h.SpansOn(i + 1) {
			parts = append(parts, s.GroupName(sp.Group)+":"+src[i][sp.Start:sp.End])
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func TestKeywordAndMatch(t *testing.T) {
	s := build(t,
		`syn keyword tKey if else`,
		`syn match tNum "\<\d\+\>"`,
	)
	got := paint(t, s, `if 12 elsewhere else 3`)
	want := `tKey:if|tNum:12|tKey:else|tNum:3`
	if got[0] != want {
		t.Errorf("got  %s\nwant %s", got[0], want)
	}
}

// TestKeywordOptionsApplyToEveryWord is vim's rule that the options on a
// `syn keyword` line are collected before any keyword is added, so an option
// written behind the words still applies to them. Reading the line left to
// right and adding as it goes leaves `import` uncontained and at the top level,
// which is a whole file's worth of difference on a Go buffer.
func TestKeywordOptionsApplyToEveryWord(t *testing.T) {
	s := build(t, `syn keyword tImport import contained`)
	for _, it := range s.items {
		if !it.containedFlag {
			t.Fatalf("%q came out uncontained", it.word)
		}
	}
}

func TestRegionWithContains(t *testing.T) {
	s := build(t,
		`syn match tEsc "\\." contained`,
		`syn region tStr start=+"+ skip=+\\.+ end=+"+ contains=tEsc`,
	)
	got := paint(t, s, `a "x\"y" b`)
	want := `tStr:"x|tEsc:\"|tStr:y"`
	if got[0] != want {
		t.Errorf("got  %s\nwant %s", got[0], want)
	}
}

func TestRegionAcrossLines(t *testing.T) {
	s := build(t, `syn region tCom start="/\*" end="\*/"`)
	got := paint(t, s, `a /* b`, `c`, `d */ e`)
	want := []string{`tCom:/* b`, `tCom:c`, `tCom:d */`}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %s want %s", i+1, got[i], want[i])
		}
	}
}

// TestOnelineRegionDoesNotStart is vim's `oneline`: a region whose end is not
// on the line its start matched never started at all.
func TestOnelineRegionDoesNotStart(t *testing.T) {
	s := build(t, `syn region tStr oneline start=+"+ end=+"+`)
	got := paint(t, s, `a "unterminated`, `b`)
	if got[0] != "" || got[1] != "" {
		t.Errorf("a oneline region ran anyway: %q %q", got[0], got[1])
	}
}

func TestMatchGroup(t *testing.T) {
	s := build(t, `syn region tStr matchgroup=tQuote start=+"+ end=+"+`)
	got := paint(t, s, `"ab"`)
	want := `tQuote:"|tStr:ab|tQuote:"`
	if got[0] != want {
		t.Errorf("got  %s\nwant %s", got[0], want)
	}
}

func TestNextGroupSkipWhite(t *testing.T) {
	s := build(t,
		`syn keyword tDecl func nextgroup=tName skipwhite`,
		`syn match tName "\w\+" contained`,
	)
	got := paint(t, s, `func main`, `main func`)
	if got[0] != `tDecl:func|tName:main` {
		t.Errorf("nextgroup did not fire: %s", got[0])
	}
	if got[1] != `tDecl:func` {
		t.Errorf("nextgroup fired where nothing armed it: %s", got[1])
	}
}

// TestPriorityLastDefinedWins is rule 1 of:syn-priority: two items matching at
// the same column go to the one defined last.
func TestPriorityLastDefinedWins(t *testing.T) {
	s := build(t,
		`syn match tFirst "\<\d\+\>"`,
		`syn match tLast "\<\d\+\>"`,
	)
	if got := paint(t, s, `42`); got[0] != `tLast:42` {
		t.Errorf("got %s, want the item defined last", got[0])
	}
}

// TestKeywordBeatsMatch is rule 2: a keyword wins over a match that starts
// where it does, however late the match was defined.
func TestKeywordBeatsMatch(t *testing.T) {
	s := build(t,
		`syn keyword tKey word`,
		`syn match tMatch "\<\w\+\>"`,
	)
	if got := paint(t, s, `word`); got[0] != `tKey:word` {
		t.Errorf("got %s, want the keyword", got[0])
	}
}

// TestEarlierStartWins is rule 3, and it is the one that needs a keyword to be
// looked for ahead of the column rather than at it.
func TestEarlierStartWins(t *testing.T) {
	s := build(t,
		`syn keyword tKey end`,
		`syn match tMatch "start.*end"`,
	)
	if got := paint(t, s, `start x end`); got[0] != `tMatch:start x end` {
		t.Errorf("got %s, want the match that starts earlier", got[0])
	}
}

// TestTransparentTakesTheEnclosingGroup: a transparent item is not drawn in
// its own colour, it is drawn in the colour of whatever contains it, and with
// no contains of its own it looks inside with the container's list. go.vim's
// goParen and goBlock are both of those and they are what lets a keyword be
// found inside braces at all.
func TestTransparentTakesTheEnclosingGroup(t *testing.T) {
	rules := func(extra string) *Syntax {
		return build(t,
			`syn region tOuter start="(" end=")" contains=tInner`,
			`syn region tInner start="\[" end="]" `+extra,
		)
	}
	if got := paint(t, rules("transparent"), `([x])`); got[0] != `tOuter:([x])` {
		t.Errorf("transparent: got %s, want the whole run in tOuter", got[0])
	}
	if got := paint(t, rules(""), `([x])`); got[0] != `tOuter:(|tInner:[x]|tOuter:)` {
		t.Errorf("opaque: got %s", got[0])
	}
}

func TestClusterAndContainedin(t *testing.T) {
	s := build(t,
		`syn keyword tTodo contained TODO`,
		`syn cluster tGroup contains=tTodo`,
		`syn region tCom start="//" end="$" contains=@tGroup`,
	)
	if got := paint(t, s, `// a TODO b`); !strings.Contains(got[0], "tTodo:TODO") {
		t.Errorf("cluster did not resolve: %s", got[0])
	}

	s = build(t,
		`syn region tCom start="//" end="$"`,
		`syn match tTodo "TODO" contained containedin=tCom`,
	)
	if got := paint(t, s, `// a TODO b`); !strings.Contains(got[0], "tTodo:TODO") {
		t.Errorf("containedin did not resolve: %s", got[0])
	}
}

func TestOffsets(t *testing.T) {
	s := build(t, `syn match tField "\.\w\+"hs=s+1`)
	if got := paint(t, s, `a.field`); got[0] != `tField:field` {
		t.Errorf("hs=s+1 did not move the highlight: %s", got[0])
	}
	s = build(t, `syn match tX "ab"me=e-1`)
	if got := paint(t, s, `ab`); got[0] != `tX:a` {
		t.Errorf("me=e-1 did not shorten the match: %s", got[0])
	}
}

func TestSynCase(t *testing.T) {
	s := build(t, `syn case ignore`, `syn keyword tKey IF`)
	if got := paint(t, s, `if If IF`); got[0] != `tKey:if|tKey:If|tKey:IF` {
		t.Errorf("syn case ignore: %s", got[0])
	}
	s = build(t, `syn case match`, `syn keyword tKey IF`)
	if got := paint(t, s, `if IF`); got[0] != `tKey:IF` {
		t.Errorf("syn case match: %s", got[0])
	}
}

// TestExternalMatch is `\z(` and `\z1`, which is how python's strings insist
// that the quote that closes one is the quote that opened it.
func TestExternalMatch(t *testing.T) {
	s := build(t, `syn region tStr start=+\z(['"]\)+ skip=+\\\z1+ end=+\z1+`)
	got := paint(t, s, `'a"b' and "c'd"`)
	want := `tStr:'a"b'|tStr:"c'd"`
	if got[0] != want {
		t.Errorf("got  %s\nwant %s", got[0], want)
	}
}

// TestTrailingNegativeLookahead is the `\@!` this package runs as a second
// anchored search rather than refusing.
func TestTrailingNegativeLookahead(t *testing.T) {
	s := build(t, `syn match tWord "\<ab\%(c\)\@!"`)
	if got := paint(t, s, `ab abc`); got[0] != `tWord:ab` {
		t.Errorf("\\@! did not exclude abc: %s", got[0])
	}
}

// TestPatternDelimiterSkipsCollections is vim's skip_regexp: a ] collection
// swallows the delimiter. Without it json.vim's escape rule reads as two
// characters and matches every backslash in the file.
func TestPatternDelimiterSkipsCollections(t *testing.T) {
	s := build(t, `syn match tEsc "\\["\\/bfnrt]"`)
	if got := paint(t, s, `x \n y \q`); got[0] != `tEsc:\n` {
		t.Errorf("got %s", got[0])
	}
}

// TestRefusalsAreNamed is the rule the package is built around: what it cannot
// run it names, with the file, the line, the group and the text.
func TestRefusalsAreNamed(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want any
	}{
		{`syn match tX "\%(a\)\@<!b"`, LookaroundError{}},
		{`syn match tX "\%V"`, PatternError{}},
		{`syn match tX "a\1"`, PatternError{}},
		{`syn frobnicate tX`, CommandError{}},
		{`syn sync match tX grouphere NONE "^a"`, CommandError{}},
	} {
		s := build(t, tc.cmd)
		if len(s.Refusals) == 0 {
			t.Errorf("%s: refused nothing", tc.cmd)
			continue
		}
		r := s.Refusals[0]
		if got, want := reflectKind(r), reflectKind(tc.want); got != want {
			t.Errorf("%s: refused as %s, want %s (%v)", tc.cmd, got, want, r)
		}
		if !strings.Contains(r.Error(), "test.vim") {
			t.Errorf("%s: refusal does not name the file: %v", tc.cmd, r)
		}
	}
}

// reflectKind is the type name of a value, without importing reflect for one
// line: every refusal type is in this package and a type switch is honest
// about which ones exist.
func reflectKind(v any) string {
	switch v.(type) {
	case LookaroundError:
		return "LookaroundError"
	case PatternError:
		return "PatternError"
	case MultiLineError:
		return "MultiLineError"
	case SplitError:
		return "SplitError"
	case CommandError:
		return "CommandError"
	case OptionError:
		return "OptionError"
	case ScriptError:
		return "ScriptError"
	case MissingError:
		return "MissingError"
	}
	return "?"
}

// TestPatternErrorUnwrapsToRegex keeps the seam between the two packages
// visible: a caller can ask which atom internal/regex refused without matching
// on a message.
func TestPatternErrorUnwrapsToRegex(t *testing.T) {
	s := build(t, `syn match tX "a\%Vb"`)
	if len(s.Refusals) != 1 {
		t.Fatalf("refusals: %v", s.Refusals)
	}
	var pe PatternError
	if !errors.As(s.Refusals[0].(error), &pe) {
		t.Fatalf("not a PatternError: %v", s.Refusals[0])
	}
	if pe.Err == nil {
		t.Fatal("PatternError carries no cause")
	}
}

// TestMissingFiletypeIsNotAnError: most of vim's 773 syntax files are for
// languages nobody here edits, and a buffer with no rules draws in Normal
// exactly as vim draws it.
func TestMissingFiletype(t *testing.T) {
	_, err := Load(DefaultRuntime(), "no-such-filetype-anywhere")
	var me MissingError
	if !errors.As(err, &me) {
		t.Fatalf("want a MissingError, got %v", err)
	}
}

// TestChangedRecomputesFromTheLine is the cache's contract: an edit throws away
// everything from the changed line down and nothing above it.
func TestChangedRecomputesFromTheLine(t *testing.T) {
	s := build(t, `syn region tCom start="/\*" end="\*/"`)
	src := []string{`a`, `b`, `c`}
	h := NewHighlighter(s, lines(src))
	for i := range src {
		h.SpansOn(i + 1)
	}
	src[1] = `/* b`
	h.Changed(2)
	got := []string{}
	for i := range src {
		var parts []string
		for _, sp := range h.SpansOn(i + 1) {
			parts = append(parts, s.GroupName(sp.Group))
		}
		got = append(got, strings.Join(parts, ","))
	}
	if got[0] != "" {
		t.Errorf("the line above the change was recoloured: %q", got[0])
	}
	if got[1] != "tCom" || got[2] != "tCom" {
		t.Errorf("the change did not reach the lines below it: %q %q", got[1], got[2])
	}
}

// TestSelfContainingItemTerminates: a `contains=ALLBUT` that does not name its
// own group lets an item contain itself at the same column, which is an endless
// loop unless somebody checks. Vim checks; so does this.
func TestSelfContainingItemTerminates(t *testing.T) {
	done := make(chan []string, 1)
	go func() {
		s := build(t, `syn region tAll start="a" end="z" contains=ALLBUT,tNothing`)
		done <- paint(t, s, `abcz`)
	}()
	select {
	case got := <-done:
		if len(got) != 1 {
			t.Fatalf("got %v", got)
		}
	case <-timeout():
		t.Fatal("the matcher did not terminate")
	}
}

func TestZeroWidthMatchTerminates(t *testing.T) {
	done := make(chan []string, 1)
	go func() {
		s := build(t,
			`syn match tStart "^" nextgroup=tRest`,
			`syn match tRest "\w\+" contained`,
		)
		done <- paint(t, s, `word here`)
	}()
	select {
	case got := <-done:
		if !strings.Contains(got[0], "tRest:word") {
			t.Errorf("a zero-width match did not arm its nextgroup: %s", got[0])
		}
	case <-timeout():
		t.Fatal("the matcher did not terminate")
	}
}

// TestBufferAnchors is \%^ and \%$, the start and the end of the buffer.
// internal/regex turns them into \A and \z, which are the start and end of what
// the matcher hands over -- one line -- so the line number is the rest of the
// answer. json.vim's jsonPadding is anchored to the top of the file and would
// otherwise match at the top of every line in it.
func TestBufferAnchors(t *testing.T) {
	s := build(t, `syn match tTop "\%^abc"`, `syn match tBot "xyz\%$"`)
	got := paint(t, s, `abc`, `abc xyz`, `xyz`)
	want := []string{`tTop:abc`, ``, `tBot:xyz`}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q want %q", i+1, got[i], want[i])
		}
	}
}

// The other four offsets. ms and me move the match, which is what contained
// items search inside and where scanning resumes; rs and re move where a
// region's body begins and ends; lc is leading context, a prefix the pattern
// must match and the match does not include.
func TestMoreOffsets(t *testing.T) {
	for _, tc := range []struct {
		name, cmd, line, want string
	}{
		{"ms", `syn match tX "abc"ms=s+1`, `abc`, `tX:bc`},
		{"he", `syn match tX "abc"he=e-1`, `abc`, `tX:ab`},
		{"lc", `syn match tX "ab"lc=1`, `ab`, `tX:b`},
		{
			"rs on a region body",
			`syn region tR matchgroup=tM start="<<"rs=s+1 end=">>"`,
			`<<x>>`,
			`tM:<|tR:<x|tM:>>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := paint(t, build(t, tc.cmd), tc.line); got[0] != tc.want {
				t.Errorf("got %s want %s", got[0], tc.want)
			}
		})
	}
}

// syn iskeyword adds characters to the set a keyword is made of, which is what
// lets a language whose identifiers carry a - or a $ have keywords at all.
func TestSynIsKeyword(t *testing.T) {
	plain := build(t, `syn keyword tK a-b`)
	if got := paint(t, plain, `a-b`); got[0] != "" {
		t.Errorf("a-b matched without iskeyword: %s", got[0])
	}
	widened := build(t, `syn iskeyword @,48-57,_,192-255,-`, `syn keyword tK a-b`)
	if got := paint(t, widened, `a-b`); got[0] != `tK:a-b` {
		t.Errorf("syn iskeyword did not widen the set: %s", got[0])
	}
}

// hi def link only fires when nothing has linked the group already; hi link
// always does. Both are what resolves a syntax group to a colour.
func TestHighlightLinks(t *testing.T) {
	s := build(t,
		`hi def link tA Comment`,
		`hi def link tA String`,
		`hi link tB Comment`,
		`hi link tB String`,
	)
	links := s.Links()
	if links["tA"] != "Comment" {
		t.Errorf("hi def link was overwritten: %q", links["tA"])
	}
	if links["tB"] != "String" {
		t.Errorf("hi link did not overwrite: %q", links["tB"])
	}
}
