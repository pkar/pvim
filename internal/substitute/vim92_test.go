package substitute

import (
	"strings"
	"testing"
)

// step is one command in a case: a ":set" line, a search that only records a
// pattern, or a substitute with its range.
type step struct {
	set    string // a ":set" argument line
	search string // a pattern "/" entered, which only moves the state
	kind   Kind
	rng    string
	args   string
}

func s(rng, args string) step   { return step{kind: KindSubstitute, rng: rng, args: args} }
func amp(rng, args string) step { return step{kind: KindAmpersand, rng: rng, args: args} }
func tld(rng, args string) step { return step{kind: KindTilde, rng: rng, args: args} }

// vimCase is one answer read off /opt/homebrew/bin/vim 9.2.321. Every want in
// this table was produced by running the same steps through
//
//	vim --clean -i NONE --not-a-term -s KEYS FILE
//
// on a pty, and reading the buffer, the redirected message line and
// line(".").",".col(".") back out. Nothing here was written from memory.
type vimCase struct {
	name  string
	in    []string
	steps []step
	// want is the buffer with the lines joined by "|".
	want string
	// msg is every message line the commands printed, in order.
	msg []string
	// cur is the cursor as vim reports it: 1-based line, 1-based column. Zero
	// means the case does not check it.
	cur [2]int
	// err is the error text, empty for a command that succeeded.
	err string
}

var vimCases = []vimCase{
	// Messages, and what 'report' does to them.
	{
		name:  "four substitutions on four lines",
		in:    []string{"aaa", "bbb", "aaa", "ccc", "aaa", "aaa"},
		steps: []step{s("%", "/aaa/xxx/")},
		want:  "xxx|bbb|xxx|ccc|xxx|xxx",
		msg:   []string{"4 substitutions on 4 lines"},
		cur:   [2]int{6, 1},
	},
	{
		name:  "report 4 swallows four substitutions",
		in:    []string{"aaa", "bbb", "aaa", "ccc", "aaa", "aaa"},
		steps: []step{{set: "report=4"}, s("%", "/aaa/xxx/")},
		want:  "xxx|bbb|xxx|ccc|xxx|xxx",
	},
	{
		name:  "report 3 does not",
		in:    []string{"aaa", "bbb", "aaa", "ccc", "aaa", "aaa"},
		steps: []step{{set: "report=3"}, s("%", "/aaa/xxx/")},
		want:  "xxx|bbb|xxx|ccc|xxx|xxx",
		msg:   []string{"4 substitutions on 4 lines"},
	},
	{
		name:  "report counts substitutions and not lines",
		in:    []string{"aaa", "bbb", "aaa", "ccc", "aaa", "aaa"},
		steps: []step{s("1,3", "/a/X/g")},
		want:  "XXX|bbb|XXX|ccc|aaa|aaa",
		msg:   []string{"6 substitutions on 2 lines"},
		cur:   [2]int{3, 1},
	},
	{
		name:  "one substitution on one line",
		in:    []string{"aaa", "bbb"},
		steps: []step{{set: "report=0"}, s("1", "/aaa/xxx/")},
		want:  "xxx|bbb",
		msg:   []string{"1 substitution on 1 line"},
	},

	// The "n" flag counts, changes nothing, and ignores 'report'.
	{
		name:  "n flag counts matches",
		in:    []string{"aaa", "bbb", "aaa", "ccc", "aaa", "aaa"},
		steps: []step{s("%", "/aaa/xxx/n")},
		want:  "aaa|bbb|aaa|ccc|aaa|aaa",
		msg:   []string{"4 matches on 4 lines"},
		cur:   [2]int{1, 1},
	},
	{
		name:  "n flag with g counts every match",
		in:    []string{"aaa", "bbb", "aaa", "ccc", "aaa", "aaa"},
		steps: []step{s("%", "/a/X/gn")},
		want:  "aaa|bbb|aaa|ccc|aaa|aaa",
		msg:   []string{"12 matches on 4 lines"},
	},
	{
		name:  "n flag beats report at one match",
		in:    []string{"aaa", "bbb"},
		steps: []step{s("1", "/aaa/x/n")},
		want:  "aaa|bbb",
		msg:   []string{"1 match on 1 line"},
	},
	{
		name:  "n flag with report 99",
		in:    []string{"aaa", "bbb", "aaa", "ccc", "aaa", "aaa"},
		steps: []step{{set: "report=99"}, s("%", "/aaa/x/n")},
		want:  "aaa|bbb|aaa|ccc|aaa|aaa",
		msg:   []string{"4 matches on 4 lines"},
	},

	{
		name:  "the n flag still moves the cursor to the first non-blank",
		in:    []string{"  lead", "trail  ", "\tmix\t"},
		steps: []step{s("3", `/\w\+/X/gn`)},
		want:  "  lead|trail  |\tmix\t",
		msg:   []string{"1 match on 1 line"},
		cur:   [2]int{1, 3},
	},
	{
		name:  "the n flag prints the line it counted on",
		in:    []string{"  lead", "trail  ", "\tmix\t"},
		steps: []step{s("2", `/\w\+/X/n#`)},
		want:  "  lead|trail  |\tmix\t",
		msg:   []string{"1 match on 1 line", "  1   lead"},
		cur:   [2]int{1, 3},
	},
	{
		name:  "a substitute that found nothing leaves the cursor alone",
		in:    []string{"  lead", "trail  ", "\tmix\t"},
		steps: []step{s("3", "/zzz/X/ge")},
		want:  "  lead|trail  |\tmix\t",
		cur:   [2]int{1, 1},
	},

	// Not found, and the "e" flag that silences it.
	{
		name:  "E486 carries the pattern",
		in:    []string{"aaa", "bbb"},
		steps: []step{s("%", "/zzz/xxx/")},
		want:  "aaa|bbb",
		err:   "E486: Pattern not found: zzz",
	},
	{
		name:  "e flag silences E486",
		in:    []string{"aaa", "bbb"},
		steps: []step{s("%", "/zzz/xxx/e")},
		want:  "aaa|bbb",
	},
	{
		name:  "n flag still raises E486",
		in:    []string{"aaa", "bbb"},
		steps: []step{s("%", "/zzz/x/n")},
		want:  "aaa|bbb",
		err:   "E486: Pattern not found: zzz",
	},

	// The cursor lands on the first non-blank of the last line changed.
	{
		name:  "cursor on the first non-blank",
		in:    []string{"    indented aaa", "plain aaa", "\tttt aaa"},
		steps: []step{s("%", "/aaa/x/")},
		want:  "    indented x|plain x|\tttt x",
		msg:   []string{"3 substitutions on 3 lines"},
		cur:   [2]int{3, 2},
	},
	{
		name:  "cursor stops at the last line changed",
		in:    []string{"    indented aaa", "plain aaa", "\tttt aaa"},
		steps: []step{{set: "report=0"}, s("1,2", "/aaa/x/")},
		want:  "    indented x|plain x|\tttt aaa",
		msg:   []string{"2 substitutions on 2 lines"},
		cur:   [2]int{2, 1},
	},

	// The trailing count moves the range to the end of itself.
	{
		name:  "count on % takes the last three lines",
		in:    []string{"a1", "a2", "a3", "a4", "a5", "a6"},
		steps: []step{{set: "report=0"}, s("%", "/a/X/ 3")},
		want:  "a1|a2|a3|a4|a5|X6",
		msg:   []string{"1 substitution on 1 line"},
		cur:   [2]int{6, 1},
	},
	{
		name:  "count starts at the last line of the range",
		in:    []string{"a1", "a2", "a3", "a4", "a5", "a6"},
		steps: []step{{set: "report=0"}, s("2,3", "/a/X/ 2")},
		want:  "a1|a2|X3|X4|a5|a6",
		msg:   []string{"2 substitutions on 2 lines"},
		cur:   [2]int{4, 1},
	},
	{
		name:  "count needs no space",
		in:    []string{"a1", "a2", "a3", "a4", "a5", "a6"},
		steps: []step{{set: "report=0"}, s("1", "/a/X/3")},
		want:  "X1|X2|X3|a4|a5|a6",
		msg:   []string{"3 substitutions on 3 lines"},
	},
	{
		name:  "count clamps at the end of the buffer",
		in:    []string{"a1", "a2", "a3", "a4", "a5", "a6"},
		steps: []step{{set: "report=0"}, s("5", "/a/X/ 9")},
		want:  "a1|a2|a3|a4|X5|X6",
		msg:   []string{"2 substitutions on 2 lines"},
	},

	// Flag letters toggle, and "&" has to be the first of them.
	{
		name:  "gg is not global",
		in:    []string{"a a a"},
		steps: []step{s("1", "/a/X/gg")},
		want:  "X a a",
	},
	{
		name:  "ggg is",
		in:    []string{"a a a"},
		steps: []step{s("1", "/a/X/ggg")},
		want:  "X X X",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "a flag after the count is E488",
		in:    []string{"a a a"},
		steps: []step{s("1", "/a/X/3g")},
		want:  "a a a",
		err:   "E488: Trailing characters: g",
	},
	{
		name:  "an unknown flag is E488",
		in:    []string{"a a a"},
		steps: []step{s("1", "/a/X/zz")},
		want:  "a a a",
		err:   "E488: Trailing characters: zz",
	},
	{
		name:  "an ampersand that is not first is E488",
		in:    []string{"a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), s("2", "/a/Y/g&")},
		want:  "X X X|a a a",
		msg:   []string{"3 substitutions on 1 line"},
		err:   "E488: Trailing characters: &",
	},
	{
		name:  "the ampersand flag inherits and the letters after it toggle",
		in:    []string{"a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), s("2", "/a/Y/&g")},
		want:  "X X X|Y a a",
		msg:   []string{"3 substitutions on 1 line"},
	},

	// The repeat forms.
	{
		name:  "colon ampersand drops the flags",
		in:    []string{"a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), amp("2", "")},
		want:  "X X X|X a a",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "colon ampersand ampersand keeps them",
		in:    []string{"a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), amp("2", "&")},
		want:  "X X X|X X X",
		msg:   []string{"3 substitutions on 1 line", "3 substitutions on 1 line"},
	},
	{
		name:  "a bare :s is a repeat without flags",
		in:    []string{"a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), s("2", "")},
		want:  "X X X|X a a",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "a bare :s takes flags of its own",
		in:    []string{"a a a", "a a a"},
		steps: []step{s("1", "/a/X/"), s("2", "g")},
		want:  "X a a|X X X",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "a bare :s takes flags after a space",
		in:    []string{"a a a", "a a a"},
		steps: []step{s("1", "/a/X/"), s("2", " g")},
		want:  "X a a|X X X",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "colon tilde takes the last used pattern",
		in:    []string{"a a a", "a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), {search: " a"}, tld("3", "")},
		want:  "X X X|a a a|aX a",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "colon ampersand takes the last :s pattern and not the last search",
		in:    []string{"a a a", "a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), {search: " a"}, amp("3", "")},
		want:  "X X X|a a a|X a a",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "the r flag makes colon ampersand take the last used pattern",
		in:    []string{"a a a", "a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), {search: " a"}, amp("3", "r")},
		want:  "X X X|a a a|aX a",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "the r flag on a delimiter form with an empty pattern",
		in:    []string{"a a a", "a a a", "a a a"},
		steps: []step{s("1", "/a/X/g"), {search: " a"}, s("3", "//Y/r")},
		want:  "X X X|a a a|aY a",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "an empty pattern takes the last search",
		in:    []string{"foo bar baz", "foo bar baz", "foo bar baz"},
		steps: []step{s("1", "/foo/X/"), {search: "bar"}, s("3", "//Y/")},
		want:  "X bar baz|foo bar baz|foo Y baz",
	},
	{
		name:  "an empty pattern takes the last :s when nothing was searched",
		in:    []string{"foo bar baz", "foo bar baz", "foo bar baz"},
		steps: []step{s("1", "/foo/X/"), s("3", "//Y/")},
		want:  "X bar baz|foo bar baz|Y bar baz",
	},
	{
		name:  "no previous substitute is E33",
		in:    []string{"a a a"},
		steps: []step{s("1", "")},
		want:  "a a a",
		err:   "E33: No previous substitute regular expression",
	},
	{
		name:  "no previous ampersand is E33",
		in:    []string{"a a a"},
		steps: []step{amp("1", "")},
		want:  "a a a",
		err:   "E33: No previous substitute regular expression",
	},
	{
		name:  "no previous pattern at all is E35 and E476 behind it",
		in:    []string{"a a a"},
		steps: []step{s("1", "//Q/")},
		want:  "a a a",
		err:   "E35: No previous regular expression\nE476: Invalid command",
	},
	{
		name:  "a failed :s still arms the repeat forms",
		in:    []string{"aaa", "bbb", "ccc"},
		steps: []step{s("1", "//X/"), amp("2", "")},
		want:  "aaa|bbb|ccc",
		err:   "E33: No previous substitute regular expression\nE476: Invalid command",
	},
	{
		name:  "a failed :s and then a colon tilde asks the other slot",
		in:    []string{"aaa", "bbb", "ccc"},
		steps: []step{s("1", "//X/"), tld("2", "")},
		want:  "aaa|bbb|ccc",
		err:   "E35: No previous regular expression\nE476: Invalid command",
	},
	{
		name:  "the e flag drops the E476 and keeps the E35",
		in:    []string{"a a a"},
		steps: []step{s("1", "//Q/e")},
		want:  "a a a",
		err:   "E35: No previous regular expression",
	},

	// The tilde in the replacement, which grows.
	{
		name:  "tilde chains through three substitutes",
		in:    []string{"aaa bbb ccc ddd"},
		steps: []step{s("1", "/aaa/X/"), s("1", "/bbb/~Y/"), s("1", "/ccc/~Z/")},
		want:  "X XY XYZ ddd",
	},
	{
		name:  "a repeat reuses the replacement as typed and expands it again",
		in:    []string{"aaa", "bbb", "ccc", "ddd"},
		steps: []step{s("1", `/\w\+/~Y/`), s("2", ""), s("3", ""), s("4", "")},
		want:  "Y|YY|YYY|YYYY",
	},
	{
		name:  "an explicit tilde after a repeat",
		in:    []string{"aaa", "bbb", "ccc", "ddd"},
		steps: []step{s("1", `/\w\+/X/`), s("2", `/\w\+/~Y/`), s("3", "")},
		want:  "X|XY|XYY|ddd",
	},
	{
		name:  "colon ampersand reuses the raw replacement too",
		in:    []string{"aaa", "bbb", "ccc", "ddd"},
		steps: []step{s("1", `/\w\+/~Y/`), amp("2", "")},
		want:  "Y|YY|ccc|ddd",
	},
	{
		name:  "an escaped tilde is a tilde",
		in:    []string{"one two three"},
		steps: []step{s("1", "/two/XY/"), s("1", `/three/\~Z/`)},
		want:  "one XY ~Z",
	},
	{
		name:  "a tilde in the pattern is the last replacement",
		in:    []string{"one two three"},
		steps: []step{s("1", "/one/two/"), s("1", "/~/Q/")},
		want:  "Q two three",
	},

	// Where vim stops looking on a line, which is the empty-match rule and the
	// one place Go's own scan and vim's disagree.
	{name: "x star", in: []string{"abc"}, steps: []step{s("1", "/x*/-/g")}, want: "-a-b-c",
		msg: []string{"3 substitutions on 1 line"}},
	{name: "b star", in: []string{"abc"}, steps: []step{s("1", "/b*/-/g")}, want: "-a-c"},
	{name: "c star", in: []string{"abc"}, steps: []step{s("1", "/c*/-/g")}, want: "-a-b-",
		msg: []string{"3 substitutions on 1 line"}},
	{name: "a star over aab", in: []string{"aab"}, steps: []step{s("1", "/a*/-/g")}, want: "-b"},
	{name: "digit star", in: []string{"a1b"}, steps: []step{s("1", `/\d*/-/g`)}, want: "-a-b"},
	{name: "dollar", in: []string{"abc"}, steps: []step{s("1", "/$/;/g")}, want: "abc;"},
	{name: "caret", in: []string{"abc"}, steps: []step{s("1", "/^/-/g")}, want: "-abc"},
	{name: "b or dollar keeps the end", in: []string{"abc"}, steps: []step{s("1", `/\vb|$/-/g`)}, want: "a-c-"},
	{name: "c or dollar does not", in: []string{"abc"}, steps: []step{s("1", `/\vc|$/-/g`)}, want: "ab-"},
	{name: "dot star", in: []string{"abc"}, steps: []step{s("1", "/.*/-/g")}, want: "-"},
	{name: "optional b", in: []string{"abc"}, steps: []step{s("1", `/\vb=/-/g`)}, want: "-a-c"},
	{name: "c star anchored", in: []string{"abc"}, steps: []step{s("1", "/c*$/-/g")}, want: "ab-"},
	{name: "z star anchored", in: []string{"abc"}, steps: []step{s("1", "/z*$/-/g")}, want: "abc-"},
	{name: "x star on an empty line", in: []string{"", "abc"}, steps: []step{s("1", "/x*/-/g")}, want: "-|abc"},
	{name: "anchored to both ends", in: []string{"abc", "def", "ghi"}, steps: []step{s("%", "/^/> /g")},
		want: "> abc|> def|> ghi", msg: []string{"3 substitutions on 3 lines"}},

	// Case: 'ignorecase', 'smartcase' and the i and I flags.
	{
		name:  "ignorecase",
		in:    []string{"Foo foo FOO"},
		steps: []step{{set: "ic"}, s("%", "/foo/X/g")},
		want:  "X X X",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "smartcase with a lower case pattern",
		in:    []string{"Foo foo FOO"},
		steps: []step{{set: "ic scs"}, s("%", "/foo/X/g")},
		want:  "X X X",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "smartcase with an upper case pattern",
		in:    []string{"Foo foo FOO"},
		steps: []step{{set: "ic scs"}, s("%", "/Foo/X/g")},
		want:  "X foo FOO",
	},
	{
		name:  "the i flag beats noignorecase",
		in:    []string{"Foo foo FOO"},
		steps: []step{{set: "noic"}, s("%", "/foo/X/gi")},
		want:  "X X X",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "the i flag beats smartcase",
		in:    []string{"Foo foo FOO"},
		steps: []step{{set: "ic scs"}, s("%", "/Foo/X/gi")},
		want:  "X X X",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "the I flag beats ignorecase",
		in:    []string{"Foo foo FOO"},
		steps: []step{{set: "ic"}, s("%", "/foo/X/gI")},
		want:  "Foo X FOO",
	},

	// 'gdefault' inverts what the g flag means.
	{
		name:  "gdefault makes a bare :s global",
		in:    []string{"a a a", "b b b"},
		steps: []step{{set: "gdefault"}, s("1", "/a/X/")},
		want:  "X X X|b b b",
		msg:   []string{"3 substitutions on 1 line"},
	},
	{
		name:  "gdefault makes the g flag local",
		in:    []string{"a a a", "b b b"},
		steps: []step{{set: "gdefault"}, s("1", "/a/X/g")},
		want:  "X a a|b b b",
	},

	// A replacement with a line break in it moves the end of the range down.
	{
		name:  "a line break splits the line and the range follows",
		in:    []string{"a1", "a2", "a3"},
		steps: []step{s("1,2", `/a/X\rY/`)},
		want:  "X|Y1|X|Y2|a3",
	},
	{
		name:  "a line break inside a global substitution",
		in:    []string{"aaa", "zzz"},
		steps: []step{s("%", `/a/X\rY/g`)},
		want:  "X|YX|YX|Y|zzz",
		msg:   []string{"3 substitutions on 1 line"},
		cur:   [2]int{4, 1},
	},

	// Delimiters other than "/".
	{name: "hash delimiter", in: []string{"abc"}, steps: []step{s("1", "#b#X#")}, want: "aXc"},
	{name: "comma delimiter", in: []string{"abc"}, steps: []step{s("1", ",b,X,")}, want: "aXc"},
	{name: "a missing closing delimiter", in: []string{"abc"}, steps: []step{s("1", "/b/X")}, want: "aXc"},
	{name: "no replacement at all", in: []string{"abc"}, steps: []step{s("1", "/b")}, want: "ac"},
	{name: "a space before the delimiter", in: []string{"a a a"}, steps: []step{s("1", " /a/X/")}, want: "X a a"},
	{
		name:  "a backslash is not a delimiter",
		in:    []string{"a a a"},
		steps: []step{s("1", `\a\X\`)},
		want:  "a a a",
		err:   `E10: \ should be followed by /, ? or &`,
	},
	{
		name:  "an expression replacement is refused",
		in:    []string{"aaa bbb"},
		steps: []step{s("1", `/aaa/\=1+1/`)},
		want:  "aaa bbb",
		err:   ErrExpression.Error(),
	},
}

func TestVim92(t *testing.T) {
	for _, c := range vimCases {
		t.Run(c.name, func(t *testing.T) {
			sess := newSession(c.in...)
			var last error
			for _, st := range c.steps {
				switch {
				case st.set != "":
					sess.set(t, st.set)
				case st.search != "":
					sess.st.NoteSearch(st.search)
				default:
					last = sess.sub(t, st.kind, st.rng, st.args)
				}
			}
			if got := sess.dump(); got != c.want {
				t.Errorf("buffer\n got %q\nwant %q", got, c.want)
			}
			if got := strings.Join(sess.msg, "\n"); got != strings.Join(c.msg, "\n") {
				t.Errorf("messages\n got %q\nwant %q", got, strings.Join(c.msg, "\n"))
			}
			gotErr := ""
			if last != nil {
				gotErr = last.Error()
			}
			if gotErr != c.err {
				t.Errorf("error\n got %q\nwant %q", gotErr, c.err)
			}
			if c.cur != [2]int{0, 0} {
				if got := [2]int{sess.cur.Line, sess.cur.Col + 1}; got != c.cur {
					t.Errorf("cursor got %v want %v", got, c.cur)
				}
			}
		})
	}
}
