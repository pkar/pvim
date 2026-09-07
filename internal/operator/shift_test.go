package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// TestShiftIndent is vim's shift_line arithmetic on its own, because
// 'shiftround' is where the rounding direction and the remainder interact and
// the table is the only way to see all four corners at once.
//
// Every row was run :set sw=3 [shiftround] then >> or << over
// "zero / (2)two / (empty) / (3 blanks) / (5)five / six".
func TestShiftIndent(t *testing.T) {
	cases := []struct {
		indent, sw, amount int
		left, round        bool
		want               int
	}{
		// Plain, sw=3: add or take away one shiftwidth, floor at zero.
		{0, 3, 1, false, false, 3},
		{2, 3, 1, false, false, 5},
		{5, 3, 1, false, false, 8},
		{2, 3, 1, true, false, 0},
		{5, 3, 1, true, false, 2},
		{0, 3, 1, true, false, 0},
		// shiftround right: floor to a multiple and add one, so 2 goes to 3
		// and 3 goes to 6.
		{0, 3, 1, false, true, 3},
		{2, 3, 1, false, true, 3},
		{3, 3, 1, false, true, 6},
		{5, 3, 1, false, true, 6},
		// shiftround left: a remainder is spent first, so 5 goes to 3 and 2
		// goes to 0, but an exact 3 goes to 0 as well.
		{5, 3, 1, true, true, 3},
		{3, 3, 1, true, true, 0},
		{2, 3, 1, true, true, 0},
		{0, 3, 1, true, true, 0},
		// A count multiplies the amount, and rounding still happens once. The
		// left-shift rows are V 2 < and V 3 < on a seven-cell indent with
		// sw=3 shiftround, which give three and zero: the remainder eats one
		// of the repeats and the rest are whole shiftwidths.
		{0, 4, 3, false, true, 12},
		{2, 3, 2, false, true, 6},
		{7, 3, 2, true, true, 3},
		{7, 3, 3, true, true, 0},
	}
	for _, tc := range cases {
		got := shiftIndent(tc.indent, tc.sw, tc.amount, tc.left, tc.round)
		if got != tc.want {
			t.Errorf("shiftIndent(indent=%d sw=%d amount=%d left=%v round=%v) = %d, want %d",
				tc.indent, tc.sw, tc.amount, tc.left, tc.round, got, tc.want)
		}
	}
}

// TestShiftLines is >> and << over a buffer, which is where the rules about
// which lines get touched at all live: an EMPTY line is skipped and a line of
// nothing but blanks is not.
func TestShiftLines(t *testing.T) {
	const in = "zero\n  two\n\n   \n     five\nsix\n"

	cases := []struct {
		name        string
		op          Op
		opt         func() Options
		count       int
		first, last int
		want        string
		line, col   int
		msg         string
	}{
		{
			name: "5>> with sw=3", op: OpShiftRight,
			opt:   func() Options { o := lineOpts(); o.ShiftWidth = 3; return o },
			first: 1, last: 5,
			want: "   zero\n     two\n\n      \n\tfive\nsix",
			line: 1, col: 4, msg: "5 lines >ed 1 time",
		},
		{
			name: "5>> with sw=3 shiftround", op: OpShiftRight,
			opt:   func() Options { o := lineOpts(); o.ShiftWidth, o.ShiftRound = 3, true; return o },
			first: 1, last: 5,
			want: "   zero\n   two\n\n      \n      five\nsix",
			line: 1, col: 4, msg: "5 lines >ed 1 time",
		},
		{
			name: "5<< with sw=3", op: OpShiftLeft,
			opt:   func() Options { o := lineOpts(); o.ShiftWidth = 3; return o },
			first: 1, last: 5,
			want: "zero\ntwo\n\n\n  five\nsix",
			line: 1, col: 1, msg: "5 lines <ed 1 time",
		},
		{
			name: "5<< with sw=3 shiftround", op: OpShiftLeft,
			opt:   func() Options { o := lineOpts(); o.ShiftWidth, o.ShiftRound = 3, true; return o },
			first: 1, last: 5,
			want: "zero\ntwo\n\n\n   five\nsix",
			line: 1, col: 1, msg: "5 lines <ed 1 time",
		},
		{
			// A blank-only line is shifted and lands the cursor on its last
			// character, because "first non-blank" on an all-blank line has
			// nowhere else to go.
			name: ">> on a line of blanks", op: OpShiftRight,
			opt:   func() Options { o := lineOpts(); o.ShiftWidth = 4; return o },
			first: 4, last: 4,
			want: "zero\n  two\n\n       \n     five\nsix",
			line: 4, col: 7, msg: "",
		},
		{
			name: "<< on a line of blanks empties it", op: OpShiftLeft,
			opt:   func() Options { o := lineOpts(); o.ShiftWidth = 4; return o },
			first: 4, last: 4,
			want: "zero\n  two\n\n\n     five\nsix",
			line: 4, col: 1, msg: "",
		},
		{
			// shiftwidth=0 means "use tabstop", which is 8 here.
			name: ">> with shiftwidth 0", op: OpShiftRight,
			opt:   func() Options { o := lineOpts(); o.ShiftWidth = 0; return o },
			first: 1, last: 1,
			want: "\tzero\n  two\n\n   \n     five\nsix",
			line: 1, col: 2, msg: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(in)
			span := SpanForLines(b, tc.first, tc.last-tc.first+1)
			got, err := Apply(Request{Buf: b, Op: tc.op, Span: span, At: at(tc.first, 1), Count: tc.count, Opt: tc.opt()})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
			if got.Message != tc.msg {
				t.Errorf("message %q, want %q", got.Message, tc.msg)
			}
		})
	}
}

// TestShiftCount is the visual-mode count: V j 3 > shifts two lines by three
// shiftwidths and says so with "times" in the plural.
func TestShiftCount(t *testing.T) {
	b := buf("a\nb\nc\nd\n")
	opt := lineOpts()
	opt.ShiftWidth = 4
	got, err := Apply(Request{Buf: b, Op: OpShiftRight, Span: SpanForLines(b, 1, 4), At: at(1, 1), Count: 3, Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "\t    a\n\t    b\n\t    c\n\t    d" {
		t.Errorf("buffer %q", dump(b))
	}
	if got.Message != "4 lines >ed 3 times" {
		t.Errorf("message %q", got.Message)
	}
	checkCursor(t, got.Cursor, 1, 6)
}

// TestShiftUnderTheVimrc is the second oracle profile: sw=4 ts=2 shiftround and
// no 'expandtab', where a four-cell indent comes back as TWO TABS. An
// implementation that builds the indent out of 'shiftwidth' or 'softtabstop'
// gives one tab and two spaces and differs on every shifted line in the file.
func TestShiftUnderTheVimrc(t *testing.T) {
	b := buf("a\nb\nc\nd\n")
	if _, err := Apply(Request{Buf: b, Op: OpShiftRight, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: vimrcOpts()}); err != nil {
		t.Fatal(err)
	}
	if dump(b) != "\t\ta\n\t\tb\nc\nd" {
		t.Errorf("buffer %q, want two tabs of indent on the first two lines", dump(b))
	}
}

// TestShiftExpandTab: with 'expandtab' the same indent is all spaces.
func TestShiftExpandTab(t *testing.T) {
	b := buf("\tone\n")
	opt := lineOpts()
	opt.ShiftWidth, opt.ExpandTab = 4, true
	if _, err := Apply(Request{Buf: b, Op: OpShiftRight, Span: SpanForLines(b, 1, 1), At: at(1, 1), Opt: opt}); err != nil {
		t.Fatal(err)
	}
	if dump(b) != "            one" {
		t.Errorf("buffer %q, want twelve spaces", dump(b))
	}
}

// TestShiftRecordsAChangeThatMovedNothing is "<<" on a line with no indent.
//
// vim's op_shift calls changed_lines() for the whole run whether or not
// set_indent moved a byte, so the changelist gets an entry, '. moves and the
// buffer is modified. Run on "abc\ndef\n" with "<<", vim's state dump prints
// changelist [[{'lnum': 1, 'col': 0, 'coladd': 0}], 1] and pvim printed
// [[], 0], which puts g; on the wrong line for the rest of the session.
func TestShiftRecordsAChangeThatMovedNothing(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		op             Op
	}{
		{"<< on an unindented line", "abc\ndef\n", "abc\ndef", OpShiftLeft},
		{">> on an empty line", "\ndef\n", "\ndef", OpShiftRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			if _, err := Apply(Request{Buf: b, Op: tc.op, Span: SpanForLines(b, 1, 1), At: at(1, 1), Opt: lineOpts()}); err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer %q, want %q", dump(b), tc.want)
			}
			got := b.ChangeList()
			if len(got) != 1 || got[0] != (text.Pos{Line: 1}) {
				t.Errorf("changelist %v, want one entry on line 1 column 1", got)
			}
		})
	}
}

// TestShiftOnAnEmptyBufferWritesNothing: an empty file is vim's ML_EMPTY and
// it writes zero bytes. A shift that moves nothing has to record its change
// without taking the buffer out of that state, or "<<" on an empty file
// invents a newline that neither editor had.
func TestShiftOnAnEmptyBufferWritesNothing(t *testing.T) {
	b := buf("")
	if _, err := Apply(Request{Buf: b, Op: OpShiftLeft, Span: SpanForLines(b, 1, 1), At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	if got := b.Bytes(); len(got) != 0 {
		t.Errorf("wrote %q, want an empty file", got)
	}
}
