package operator

import (
	"strings"
	"testing"
)

// TestFormatTextWidthZero is the answer to "what does gq do with textwidth=0",
// which to check rather than assume.
//
// vim's comp_textwidth() takes the window's width and caps it at 79. Run at a
// 120-column terminal, gq on a 246-character line wraps at 78 and the next
// word would have made 81, so the limit is 79; run at 60 columns the same line
// wraps at 58 and 59. The oracle's pty is 120 columns wide, so 79 is the
// number every oracle case sees, and it is what this package uses when
// Options.TextWidth is zero.
func TestFormatTextWidthZero(t *testing.T) {
	words := strings.Fields(strings.Repeat("aaa bb cccc dddddd ee ffffff ggg hhhhhhhh iii jj kkkkk lll mmmm nnn oo ppppppp qq rrrr sss tttttttt uu vvv www xx yyyy zzz ", 2))
	b := buf(strings.Join(words, " ") + "\n")

	got, err := Apply(Request{Buf: b, Op: OpFormat, Span: SpanForLines(b, 1, 1), At: at(1, 1), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	want := "aaa bb cccc dddddd ee ffffff ggg hhhhhhhh iii jj kkkkk lll mmmm nnn oo ppppppp\n" +
		"qq rrrr sss tttttttt uu vvv www xx yyyy zzz aaa bb cccc dddddd ee ffffff ggg\n" +
		"hhhhhhhh iii jj kkkkk lll mmmm nnn oo ppppppp qq rrrr sss tttttttt uu vvv www\n" +
		"xx yyyy zzz"
	if dump(b) != want {
		t.Errorf("buffer\n%s\nwant\n%s", dump(b), want)
	}
	checkCursor(t, got.Cursor, 4, 1)
	if got.Message != "3 more lines" {
		t.Errorf("message %q", got.Message)
	}
}

// TestFormatParagraphs is gq over two paragraphs with an indented first line,
// which is where three separate rules show at once: a blank line ends a
// paragraph and is left alone; the first line keeps its indent verbatim; and
// the lines after it get NO indent unless 'autoindent' is on. Every line here
// came out of vim at textwidth=30.
func TestFormatParagraphs(t *testing.T) {
	const in = "    one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen\n" +
		"  second line here\n\nnew para starts here and it is short\nmore of it\n"

	t.Run("without autoindent", func(t *testing.T) {
		b := buf(in)
		opt := lineOpts()
		opt.TextWidth = 30
		got, err := Apply(Request{Buf: b, Op: OpFormat, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: opt})
		if err != nil {
			t.Fatal(err)
		}
		want := "    one two three four five\nsix seven eight nine ten\neleven twelve thirteen\n" +
			"fourteen fifteen sixteen\nsecond line here\n\nnew para starts here and it is short\nmore of it"
		if dump(b) != want {
			t.Errorf("buffer\n%s\nwant\n%s", dump(b), want)
		}
		checkCursor(t, got.Cursor, 5, 1)
	})

	t.Run("with autoindent", func(t *testing.T) {
		b := buf(in)
		opt := lineOpts()
		opt.TextWidth, opt.AutoIndent = 30, true
		if _, err := Apply(Request{Buf: b, Op: OpFormat, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: opt}); err != nil {
			t.Fatal(err)
		}
		want := "    one two three four five\n    six seven eight nine ten\n    eleven twelve thirteen\n" +
			"    fourteen fifteen sixteen\n    second line here\n\nnew para starts here and it is short\nmore of it"
		if dump(b) != want {
			t.Errorf("buffer\n%s\nwant\n%s", dump(b), want)
		}
	})
}

// TestFormatJoinSpaces: gq puts two spaces after a full stop under
// 'joinspaces' and one without it, same as J. Measured by reflowing
// "aaa bbb ccc." and "ddd eee fff" into one line at textwidth=40.
func TestFormatJoinSpaces(t *testing.T) {
	for _, tc := range []struct {
		js   bool
		want string
	}{
		{true, "aaa bbb ccc.  ddd eee fff"},
		{false, "aaa bbb ccc. ddd eee fff"},
	} {
		b := buf("aaa bbb ccc.\nddd eee fff\n")
		opt := lineOpts()
		opt.TextWidth, opt.JoinSpaces = 40, tc.js
		if _, err := Apply(Request{Buf: b, Op: OpFormat, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: opt}); err != nil {
			t.Fatal(err)
		}
		if dump(b) != tc.want {
			t.Errorf("joinspaces=%v gave %q, want %q", tc.js, dump(b), tc.want)
		}
	}
}

// TestFormatBlankLineIsASeparator: a line of nothing but spaces ends a
// paragraph and is left exactly as it was, spaces and all. Measured: gq G over
// "aaa bbb / (three spaces) / ccc ddd" at textwidth=40 leaves all three lines
// alone rather than joining them into one.
func TestFormatBlankLineIsASeparator(t *testing.T) {
	b := buf("aaa bbb\n   \nccc ddd\n")
	opt := lineOpts()
	opt.TextWidth = 40
	got, err := Apply(Request{Buf: b, Op: OpFormat, Span: SpanForLines(b, 1, 3), At: at(1, 1), Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "aaa bbb\n   \nccc ddd" {
		t.Errorf("buffer %q", dump(b))
	}
	checkCursor(t, got.Cursor, 3, 1)
}

// TestFormatShortParagraph: gq that changes nothing changes nothing, and the
// cursor still moves to the last line of the region.
func TestFormatShortParagraph(t *testing.T) {
	b := buf("aaaa bbbb cccc\n\ndddd eeee\n")
	opt := lineOpts()
	opt.TextWidth = 30
	got, err := Apply(Request{Buf: b, Op: OpFormat, Span: SpanForLines(b, 1, 3), At: at(1, 1), Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "aaaa bbbb cccc\n\ndddd eeee" {
		t.Errorf("buffer %q", dump(b))
	}
	checkCursor(t, got.Cursor, 3, 1)
	if got.Message != "" {
		t.Errorf("message %q, want none", got.Message)
	}
}

// TestFormatCursorOnLastNonBlank: gq leaves the cursor on the first non-blank
// of the last line it formatted, which for an indented result is not column 0.
func TestFormatCursorOnLastNonBlank(t *testing.T) {
	b := buf("    aaa bbb ccc ddd eee fff ggg hhh\n")
	opt := lineOpts()
	opt.TextWidth, opt.AutoIndent = 20, true
	got, err := Apply(Request{Buf: b, Op: OpFormat, Span: SpanForLines(b, 1, 1), At: at(1, 1), Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "    aaa bbb ccc ddd\n    eee fff ggg hhh" {
		t.Errorf("buffer %q", dump(b))
	}
	checkCursor(t, got.Cursor, 2, 5)
}

// TestFormatKeepCursor is gw, which puts the cursor back on the character it
// was on. Measured: with the cursor at the end of a 246-character line, gw at
// tw=0 leaves it on the last "z" of the reflowed text, which is line 4 column
// 11 and not line 4 column 1 where gq would have left it.
func TestFormatKeepCursor(t *testing.T) {
	words := strings.Fields(strings.Repeat("aaa bb cccc dddddd ee ffffff ggg hhhhhhhh iii jj kkkkk lll mmmm nnn oo ppppppp qq rrrr sss tttttttt uu vvv www xx yyyy zzz ", 2))
	line := strings.Join(words, " ")
	b := buf(line + "\n")

	got, err := Apply(Request{
		Buf: b, Op: OpFormatKeep, Span: SpanForLines(b, 1, 1),
		At: at(1, len(line)), Opt: lineOpts(),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkCursor(t, got.Cursor, 4, 11)
}

// TestFormatKeepCursorAtTheStart: gw with the cursor on the first character
// leaves it there, which is the one case an off-by-one in the character count
// shows immediately.
func TestFormatKeepCursorAtTheStart(t *testing.T) {
	b := buf("aaa bbb ccc ddd eee fff ggg hhh\n")
	opt := lineOpts()
	opt.TextWidth = 12
	got, err := Apply(Request{Buf: b, Op: OpFormatKeep, Span: SpanForLines(b, 1, 1), At: at(1, 1), Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	checkCursor(t, got.Cursor, 1, 1)
}

// TestFormatRegion is the reflow on its own, so a width rule can be stated
// without a buffer. A line of exactly 'textwidth' cells is allowed; the word
// that would take it past is what starts the next line.
func TestFormatRegion(t *testing.T) {
	opt := lineOpts()
	got, _ := formatRegion([][]byte{[]byte("aaaa bbbb cc dd")}, 9, opt)
	want := []string{"aaaa bbbb", "cc dd"}
	if len(got) != len(want) {
		t.Fatalf("%d lines, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Errorf("line %d = %q, want %q", i+1, got[i], want[i])
		}
	}
}
