package textobj

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// TestVisualFresh checks the one thing visual mode changes about an object
// with a selection that is still a single character: the rule that turns a
// charwise object ending in column one into a linewise one does not run.
//
// The expectations come from vim, one case at a time:
//
//	vim --clean -i NONE --not-a-term -es -c 'source probe.vim' </dev/null
//
// with a probe that does "normal! v<object><Esc>" and writes '< and '>. The
// giveaway is the first row: di{ over that block is linewise and vi{ selects
// the text between the braces, which is the difference this test is here for.
func TestVisualFresh(t *testing.T) {
	for _, tc := range []struct {
		fixture   string
		line, col int
		keys      string
		want      string
	}{
		{"brackets.txt", 5, 3, "i{", "c 6,0-7,0"},
		{"brackets.txt", 5, 3, "a{", "c 5,6-7,3"},
		{"brackets.txt", 1, 4, "i(", "c 1,3-1,10"},
		{"brackets.txt", 2, 4, "i[", "c 2,2-2,8"},
		{"brackets.txt", 3, 4, "i<", "c 3,4-3,5"},
		{"para.txt", 1, 1, "ip", "V 1-2"},
		{"para.txt", 1, 1, "ap", "V 1-3"},
		{"para.txt", 3, 1, "ip", "V 3-3"},
		{"words.txt", 1, 1, "iw", "c 1,0-1,5"},
		{"words.txt", 1, 6, "iw", "c 1,5-1,6"},
		{"words.txt", 1, 7, "aw", "c 1,6-1,13"},
		{"words.txt", 4, 1, "aw", "c 4,0-5,8"},
		{"quotes.txt", 2, 4, `i"`, "c 2,3-2,4"},
		{"quotes.txt", 2, 4, `a"`, "c 2,2-2,6"},
		{"tags.txt", 4, 1, "it", "c 4,4-7,0"},
		{"tags.txt", 4, 1, "at", "c 4,0-7,5"},
		{"sent.txt", 1, 1, "is", "c 1,0-1,4"},
		{"sent.txt", 1, 1, "as", "c 1,0-1,5"},
		{"edge.txt", 6, 1, "iw", "c 5,0-6,0"},
	} {
		k, err := parseKeySpec(tc.keys)
		if err != nil {
			t.Fatalf("%s: %v", tc.keys, err)
		}
		o, ok := ByKey(k.key)
		if !ok {
			t.Fatalf("%q is not in the table", tc.keys)
		}
		buf := readFixture(t, tc.fixture)
		at := text.Pos{Line: tc.line, Col: tc.col - 1}
		got := outcome(o.Find(Request{
			Buf:    buf,
			At:     at,
			Count:  k.count,
			Inner:  k.inner,
			Arg:    k.key,
			Sel:    text.Range{Start: at, End: at},
			HasSel: true,
			Opt:    DefaultOptions(),
		}))
		if got != tc.want {
			t.Errorf("v%s at %s %d,%d: got %s, vim says %s",
				tc.keys, tc.fixture, tc.line, tc.col, got, tc.want)
		}
	}
}

// readFixture loads one of the oracle's fixtures.
func readFixture(t *testing.T, name string) *text.Buffer {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return text.Read(data)
}

// TestAliasesAgree: ib is i(, iB is i{, and either bracket of a pair names the
// same object. Four spellings that have to answer identically or dib and di(
// mean different things.
func TestAliasesAgree(t *testing.T) {
	buf := readFixture(t, "brackets.txt")

	for _, group := range []struct {
		keys []byte
		arg  byte
	}{
		{[]byte{'(', ')', 'b'}, '('},
		{[]byte{'{', '}', 'B'}, '{'},
		{[]byte{'[', ']'}, '['},
		{[]byte{'<', '>'}, '<'},
	} {
		for line := 1; line <= buf.LineCount(); line++ {
			for col := 0; col < len(buf.Line(line)); col++ {
				var want string
				for i, key := range group.keys {
					o, _ := ByKey(key)
					got := outcome(o.Find(Request{
						Buf: buf,
						At:  text.Pos{Line: line, Col: col},
						Arg: group.arg,
						Opt: DefaultOptions(),
					}))
					if i == 0 {
						want = got
						continue
					}
					if got != want {
						t.Fatalf("i%c at %d,%d = %s, but i%c = %s",
							key, line, col, got, group.keys[0], want)
					}
				}
			}
		}
	}
}

// TestEveryObjectStaysInTheBuffer is the invariant every caller depends on and
// no differential table can state: whatever an object answers, the range is
// inside the buffer, its ends are in order, and asking never panics. Run over
// every fixture, every position, every object, every count, plus the buffer
// shapes the fixtures do not have.
func TestEveryObjectStaysInTheBuffer(t *testing.T) {
	bufs := map[string]*text.Buffer{
		"empty":    text.Read(nil),
		"one line": text.Read([]byte("hello")),
		"brackets": text.Read([]byte("({[<\"'`<a>")),
		"closers":  text.Read([]byte(")}]>\"'`</a>")),
		"blanks":   text.Read([]byte("\n\n   \n\n")),
	}
	for _, name := range fixtures {
		bufs[name] = readFixture(t, name)
	}

	for name, buf := range bufs {
		for line := 1; line <= buf.LineCount(); line++ {
			for col := 0; col <= len(buf.Line(line)); col++ {
				for _, k := range allKeySpecs() {
					o, _ := ByKey(k.key)
					r := o.Find(Request{
						Buf:   buf,
						At:    text.Pos{Line: line, Col: col},
						Count: k.count,
						Inner: k.inner,
						Arg:   k.key,
						Opt:   DefaultOptions(),
					})
					if !r.Ok {
						continue
					}
					where := func() string {
						return fmt.Sprintf("%s %s at %d,%d", name, k.String(), line, col)
					}
					if r.Range.End.Before(r.Range.Start) {
						t.Fatalf("%s: range runs backwards: %v", where(), r.Range)
					}
					if r.Type == register.TypeLine {
						if r.Range.Start.Line < 1 || r.Range.End.Line > buf.LineCount() {
							t.Fatalf("%s: lines %d-%d outside a buffer of %d",
								where(), r.Range.Start.Line, r.Range.End.Line, buf.LineCount())
						}
						continue
					}
					for _, p := range []text.Pos{r.Range.Start, r.Range.End} {
						if p.Line < 1 || p.Line > buf.LineCount() {
							t.Fatalf("%s: line %d outside a buffer of %d", where(), p.Line, buf.LineCount())
						}
						if p.Col < 0 || p.Col > len(buf.Line(p.Line)) {
							t.Fatalf("%s: column %d outside a line of %d bytes",
								where(), p.Col, len(buf.Line(p.Line)))
						}
					}
				}
			}
		}
	}
}

// TestKeywords covers 'iskeyword' parsing, which decides where iw stops.
func TestKeywords(t *testing.T) {
	for _, tc := range []struct {
		spec string
		in   rune
		want bool
	}{
		{"", 'a', true}, // the default: "@,48-57,_,192-255"
		{"", '_', true},
		{"", '5', true},
		{"", '-', false},
		{"", 'é', true}, // 233, inside 192-255
		{"", '{', false},
		{"@,48-57,_,192-255", 'z', true},
		{"@,48-57,_,192-255", '.', false},
		{"a-c", 'b', true},
		{"a-c", 'd', false},
		{"@,^a", 'a', false}, // an exclusion after "@" wins
		{"@,^a", 'b', true},
		{"45", '-', true},  // a decimal character code
		{"@,-", '-', true}, // the dash as a plain character
		{"@,44", ',', true},
		{"@", 'ß', true}, // a letter in latin-1
	} {
		if got := parseKeywords(tc.spec).is(tc.in); got != tc.want {
			t.Errorf("iskeyword=%q: is(%q) = %v, want %v", tc.spec, tc.in, got, tc.want)
		}
	}
}

// TestDefaultOptionsAreVimsDefaults pins the strings, because an option that
// silently differs from vim's default turns every object subtly wrong and
// nothing else in the tree would notice.
func TestDefaultOptionsAreVimsDefaults(t *testing.T) {
	o := DefaultOptions()
	for _, tc := range []struct{ got, want, name string }{
		{o.IsKeyword, "@,48-57,_,192-255", "iskeyword"},
		{o.MatchPairs, "(:),{:},[:]", "matchpairs"},
		{o.Paragraphs, "IPLPPPQPP TPHPLIPpLpItpplpipbp", "paragraphs"},
		{o.Sections, "SHNHH HUnhsh", "sections"},
		{o.Selection, "inclusive", "selection"},
		{o.QuoteEscape, "\\", "quoteescape"},
	} {
		if tc.got != tc.want {
			t.Errorf("'%s' defaults to %q, vim says %q", tc.name, tc.got, tc.want)
		}
	}
	if o.TabStop != 8 {
		t.Errorf("'tabstop' defaults to %d, vim says 8", o.TabStop)
	}
}

// TestKnownDifferences pins the one place this package answers differently
// from vim on purpose, so that it is a test with a number in it rather than a
// sentence in a report nobody reads again. It needs a register entry in
// the register before the oracle can run over input that contains it.
//
// It is not reachable from the vimrc this editor is being written for, which
// has no HTML in it at all.
//
// The CJK difference that used to be here is closed: vim gives CJK
// ideographs, kana, Hangul and a dozen other scripts a character class of
// their own, and charclass.go now has the same range table, so "iw" on the
// first character of "日abc" takes that one character in both. The case stays
// as an assertion that the two agree, because the table is 40 rows of ranges
// and a row that drifts would otherwise only show up in wide.txt.
func TestKnownDifferences(t *testing.T) {
	buf := text.Read([]byte("日abc def\n"))
	o, _ := ByKey('w')
	got := outcome(o.Find(Request{
		Buf: buf, At: text.Pos{Line: 1}, Inner: true, Opt: DefaultOptions(),
	}))
	if want := "c 1,0-1,3"; got != want {
		t.Errorf("iw over 日abc = %s, want %s, which is what vim says", got, want)
	}

	// The one that is left: a start tag whose attribute value opens a quote and never closes it
	// is not a tag here, and vim treats it as one that ends at the first '>'
	// it can find. vim yanks "<a b=\"x>text" for the it below; this fails.
	// Malformed HTML either way, and failing is the answer that cannot delete
	// the wrong thing.
	buf = text.Read([]byte("<a b=\"x>text</a>\n"))
	o, _ = ByKey('t')
	got = outcome(o.Find(Request{
		Buf: buf, At: text.Pos{Line: 1, Col: 9}, Inner: true, Opt: DefaultOptions(),
	}))
	if want := "-"; got != want {
		t.Errorf("it inside an unterminated attribute = %s, want %s", got, want)
	}
}
