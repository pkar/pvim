package mode

import (
	"errors"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// Every expectation here was produced by running the same keystrokes through
// /opt/homebrew/bin/vim 9.2.0321 under a pty and reading the redirected
// message line, the cursor and the buffer back. See cmd/oracle; the promoted
// cases are testdata/keys/bracket_*, ctrl_w_ctrl_*, and K_*.

// TestIdentUnderCursor is vim's find_ident_under_cursor: the word the cursor
// is on, or the first one after it on the line, and nothing from the line
// below.
func TestIdentUnderCursor(t *testing.T) {
	cases := []struct {
		name string
		in   string
		col  int
		want string
	}{
		{"on the first letter", "foo bar", 0, "foo"},
		{"inside the word", "foo bar", 1, "foo"},
		{"on the space before a word", "foo bar", 3, "bar"},
		{"on punctuation, scans forward", "  ...foo...", 0, "foo"},
		{"nothing on the line", "+++ ,,, ;;;", 0, ""},
		{"empty line", "", 0, ""},
		{"past the end of the line", "ab", 9, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := New(text.Read([]byte(tc.in + "\n")))
			e.SetCursor(text.Pos{Line: 1, Col: tc.col})
			got, ok := e.identUnderCursor()
			if !ok {
				got = ""
			}
			if got != tc.want {
				t.Errorf("identUnderCursor() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIdentMessages is the three ways the family refuses, each measured.
func TestIdentMessages(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
	}{
		{"no identifier", "\n", "[i", "E349: No identifier under cursor"},
		{"K with no identifier", "+++\n", "K", "E349: No identifier under cursor"},
		{"match on the current line", "foo bar\nbaz foo\n", "[i", "E387: Match is on current line"},
		{"nothing found forwards", "zzz\nfoo\nbbb\n", "2G]\t", "E389: Couldn't find pattern"},
		{"no definition", "foo bar\n", "[d", "E388: Couldn't find definition"},
		// The clamp: a "]" command on the last line starts on it rather than
		// past it, so the match it finds is always the current line's.
		{"last line clamps to itself", "aaa\nbbb\nccc\n", "3G]\t", "E387: Match is on current line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			if got := e.Message(); got != tc.want {
				t.Errorf("message %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIdentGotoAndShow is what the two that do something do: "[<Tab>" moves
// the cursor onto the match and "[i" prints the line without moving.
func TestIdentGotoAndShow(t *testing.T) {
	const in = "foo bar\nzz\nfoo end\nlast foo\n"

	e := edit(t, in, "3G[\t")
	if got := e.Cursor(); got != (text.Pos{Line: 1, Col: 0}) {
		t.Errorf("[<Tab> left the cursor at %v, want line 1 column 0", got)
	}

	// The count picks the Nth match: lines 1, 3 and 4 hold "foo".
	e = edit(t, in, "2[\t")
	if got := e.Cursor().Line; got != 3 {
		t.Errorf("2[<Tab> landed on line %d, want 3", got)
	}

	// "]" starts on the line after the cursor.
	e = edit(t, in, "3G]\t")
	if got := e.Cursor(); got != (text.Pos{Line: 4, Col: 5}) {
		t.Errorf("]<Tab> left the cursor at %v, want line 4 column 5", got)
	}

	e = edit(t, in, "3G[i")
	if got, want := e.Message(), "foo bar"; got != want {
		t.Errorf("[i said %q, want %q", got, want)
	}
	if got := e.Cursor().Line; got != 3 {
		t.Errorf("[i moved the cursor to line %d; it moves nothing", got)
	}
}

// TestIdentShowAllFormat is "[I", whose layout is vim's: the file name, then
// "%3d: %4d " and the line with its tabs expanded.
func TestIdentShowAllFormat(t *testing.T) {
	e := New(text.Read([]byte("zz\n\tfoo bar\nfoo end\n")))
	e.SetFileName("buf.txt")
	e.SetCursor(text.Pos{Line: 3})
	for _, k := range decodeKeys([]byte("[I")) {
		if err := e.Key(k); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"buf.txt",
		"  1:    2         foo bar",
		"  2:    3 foo end",
	}
	got := e.Messages()
	if len(got) < len(want) {
		t.Fatalf("[I said %q, want %q", got, want)
	}
	got = got[len(got)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d is %q, want %q", i, got[i], want[i])
		}
	}
}

// TestMsgOutTrans is how a line looks on the message line: a Tab advances to
// the next 'tabstop' and a control character shows as its caret form.
//
// 'tabstop' and not a fixed eight, measured under both oracle profiles: the
// same line shows six spaces after "ab" under `vim --clean` and two under the
// vimrc profile, which sets ts=2.
func TestMsgOutTrans(t *testing.T) {
	cases := []struct {
		in      string
		tabstop int
		want    string
	}{
		{"ab\tcd", 8, "ab      cd"},
		{"a\tcd", 8, "a       cd"},
		{"ab\tcd", 2, "ab  cd"},
		{"\tfoo", 8, "        foo"},
		{"ab\x01cd", 8, "ab^Acd"},
		{"plain", 8, "plain"},
	}
	for _, tc := range cases {
		if got := msgOutTrans([]byte(tc.in), tc.tabstop); got != tc.want {
			t.Errorf("msgOutTrans(%q, %d) = %q, want %q", tc.in, tc.tabstop, got, tc.want)
		}
	}
}

// TestKeywordPrgError is K over a word: the mode machine builds the command
// line and hands it up, because running a shell is not its job.
func TestKeywordPrgError(t *testing.T) {
	e := New(text.Read([]byte("foo bar\n")))
	var kw *KeywordError
	err := e.Key(decodeKeys([]byte("K"))[0])
	if !errors.As(err, &kw) {
		t.Fatalf("K returned %v, want a KeywordError", err)
	}
	const want = "!man 'foo'"
	if kw.Cmd != want {
		t.Errorf("K asked for %q, want %q", kw.Cmd, want)
	}
	if !strings.Contains(kw.Error(), want) {
		t.Errorf("the error text %q does not name the command", kw.Error())
	}
}

// TestShellQuote is vim's vim_strsave_shellescape for a POSIX shell.
func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"foo":     "'foo'",
		"a b":     "'a b'",
		"it's":    `'it'\''s'`,
		"$(rm -)": "'$(rm -)'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestIdentSplitReportsTheMatch is CTRL-W i as the frontend sees it: false
// when it has already said why there is nothing to split for, true when there
// is a window to open.
func TestIdentSplitReportsTheMatch(t *testing.T) {
	e := New(text.Read([]byte("\n")))
	if found, err := e.IdentSplit(false, 1); found || err != nil {
		t.Errorf("IdentSplit over an empty buffer = %v, %v; want false, nil", found, err)
	}
	if got, want := e.Message(), "E349: No identifier under cursor"; got != want {
		t.Errorf("message %q, want %q", got, want)
	}

	e = New(text.Read([]byte("foo bar\nzz\nfoo\n")))
	e.SetCursor(text.Pos{Line: 3})
	found, err := e.IdentSplit(false, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Error("IdentSplit found nothing over a buffer with the word on line 1")
	}
	// It moves nothing: the window the match wants is the caller's to open.
	if got := e.Cursor().Line; got != 3 {
		t.Errorf("IdentSplit moved the cursor to line %d", got)
	}
}

// TestZYTrimsOnlyABlock is vim 9's "zy". Measured against 9.2.0321: over a
// charwise or linewise span it puts exactly what "y" puts, trailing spaces
// included, and over a block each line loses the white space at its end.
func TestZYTrimsOnlyABlock(t *testing.T) {
	cases := []struct {
		name, in, keys, want string
	}{
		{"charwise keeps the spaces", "abc   def   \nghi\n", "zy$", "abc   def   "},
		{"plain y keeps them too", "abc   def   \nghi\n", "y$", "abc   def   "},
		{"doubled is linewise", "abc   def   \nghi\n", "zyy", "abc   def   "},
		{"blockwise drops them", "ab  \ncd  \nef  \n", "\x16jj$zy", "ab\ncd\nef"},
		{"blockwise y keeps them", "ab  \ncd  \nef  \n", "\x16jj$y", "ab  \ncd  \nef  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := edit(t, tc.in, tc.keys)
			v, err := e.Registers().Get('"')
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, l := range v.Lines {
				got = append(got, string(l))
			}
			if strings.Join(got, "\n") != tc.want {
				t.Errorf("register holds %q, want %q", strings.Join(got, "\n"), tc.want)
			}
		})
	}
}

// TestJoinCountOverrunsMovesToColumnOne is vim's nv_join clamping a count that
// runs off the end of the buffer: do_join still runs, over one line, and ends
// by putting the cursor where the last join happened, which is column one
// because there was none. Measured, "llll3J" on a one-line file.
func TestJoinCountOverrunsMovesToColumnOne(t *testing.T) {
	e := edit(t, "the only line\n", "llll3J")
	if got := e.Cursor(); got != (text.Pos{Line: 1, Col: 0}) {
		t.Errorf("cursor at %v, want line 1 column 0", got)
	}
	// A count of one or two on the last line beeps instead, and beeping moves
	// nothing.
	e = edit(t, "the only line\n", "llll2J")
	if got := e.Cursor(); got != (text.Pos{Line: 1, Col: 4}) {
		t.Errorf("cursor at %v after a beeping join, want line 1 column 4", got)
	}
}
