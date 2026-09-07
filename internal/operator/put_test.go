package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/register"
)

// TestPutCharwise is p, P and gp with a charwise register, and the rule the
// table exists for is the cursor: a single-line value leaves it on the LAST
// character pasted and a value that spans a line break leaves it on the FIRST.
// Both were run: y w p on "hello world" ends in column 7, and a visual y of
// "hello world\nse" followed by P ends in column 1.
func TestPutCharwise(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		val       register.Value
		at        [2]int
		before    bool
		after     bool
		count     int
		want      string
		line, col int
	}{
		{
			name: "yw p", in: "hello world\n", val: register.Char([]byte("hello ")),
			at: [2]int{1, 1}, want: "hhello ello world", line: 1, col: 7,
		},
		{
			name: "yw w P", in: "hello world\n", val: register.Char([]byte("hello ")),
			at: [2]int{1, 7}, before: true, want: "hello hello world", line: 1, col: 12,
		},
		{
			name: "yw $ p", in: "hello world\n", val: register.Char([]byte("hello ")),
			at: [2]int{1, 11}, want: "hello worldhello ", line: 1, col: 17,
		},
		{
			name: "yw 2p", in: "hello world\n", val: register.Char([]byte("hello ")),
			at: [2]int{1, 1}, count: 2, want: "hhello hello ello world", line: 1, col: 13,
		},
		{
			name: "yw gp", in: "hello world\n", val: register.Char([]byte("hello ")),
			at: [2]int{1, 1}, after: true, want: "hhello ello world", line: 1, col: 8,
		},
		{
			name: "yw 2gp", in: "hello world\n", val: register.Char([]byte("hello ")),
			at: [2]int{1, 1}, after: true, count: 2, want: "hhello hello ello world", line: 1, col: 14,
		},
		{
			// A charwise value that spans a line break: the cursor stays at
			// the start of what was pasted.
			name: "P of a two-line charwise register", in: "hello world\nsecond line\n",
			val: register.Char([]byte("hello world"), []byte("se")),
			at:  [2]int{1, 1}, before: true,
			want: "hello world\nsehello world\nsecond line", line: 1, col: 1,
		},
		{
			name: "p onto the middle of another line", in: "hello world\nsecond line\n",
			val: register.Char([]byte("hello world")), at: [2]int{2, 1},
			want: "hello world\nshello worldecond line", line: 2, col: 12,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			got, err := Put(PutRequest{
				Buf: b, Val: tc.val, At: at(tc.at[0], tc.at[1]),
				Before: tc.before, After: tc.after, Count: tc.count, Opt: lineOpts(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
		})
	}
}

// TestPutLinewise is p, P, gp and gP with whole lines. p lands on the first
// non-blank of the first line pasted; gp lands in COLUMN 0 of the line after
// the last one, with no first-non-blank step, which is what leaves it on a tab.
func TestPutLinewise(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		val       register.Value
		at        [2]int
		before    bool
		after     bool
		count     int
		want      string
		line, col int
		msg       string
	}{
		{
			name: "yy j p", in: "hello world\nsecond line\nthird\n", val: register.LineValue([]byte("hello world")),
			at: [2]int{2, 1}, want: "hello world\nsecond line\nhello world\nthird", line: 3, col: 1,
		},
		{
			name: "yy j P", in: "hello world\nsecond line\nthird\n", val: register.LineValue([]byte("hello world")),
			at: [2]int{2, 1}, before: true, want: "hello world\nhello world\nsecond line\nthird", line: 2, col: 1,
		},
		{
			name: "yy G p at the end of the buffer", in: "hello world\nsecond line\nthird\n",
			val: register.LineValue([]byte("hello world")), at: [2]int{3, 1},
			want: "hello world\nsecond line\nthird\nhello world", line: 4, col: 1,
		},
		{
			name: "yy 2p", in: "hello world\nsecond\n", val: register.LineValue([]byte("hello world")),
			at: [2]int{1, 1}, count: 2,
			want: "hello world\nhello world\nhello world\nsecond", line: 2, col: 1,
		},
		{
			name: "p lands on the first non-blank", in: "\tone\n    two\n\tthree\nfour\n",
			val: register.LineValue([]byte("\tone")), at: [2]int{3, 1},
			want: "\tone\n    two\n\tthree\n\tone\nfour", line: 4, col: 2,
		},
		{
			name: "gp lands in column 0 of the next line", in: "\tone\n    two\n\tthree\n",
			val: register.LineValue([]byte("\tone")), at: [2]int{2, 1}, after: true,
			want: "\tone\n    two\n\tone\n\tthree", line: 4, col: 1,
		},
		{
			name: "gP lands after the pasted lines too", in: "\tone\n    two\n\tthree\n",
			val: register.LineValue([]byte("    two")), at: [2]int{2, 1}, before: true, after: true,
			want: "\tone\n    two\n    two\n\tthree", line: 3, col: 1,
		},
		{
			name: "3p of three lines reports nine", in: "a\nb\n", val: register.LineValue([]byte("x"), []byte("y"), []byte("z")),
			at: [2]int{2, 1}, count: 3,
			want: "a\nb\nx\ny\nz\nx\ny\nz\nx\ny\nz", line: 3, col: 1, msg: "9 more lines",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			got, err := Put(PutRequest{
				Buf: b, Val: tc.val, At: at(tc.at[0], tc.at[1]),
				Before: tc.before, After: tc.after, Count: tc.count, Opt: lineOpts(),
			})
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

// TestPutIndent is ]p and [p: the pasted block keeps its shape and moves to
// the current line's indent. Measured with a register holding a four-space
// indent put onto an unindented line, and one holding a tab put onto a
// four-space line.
func TestPutIndent(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		val       register.Value
		at        [2]int
		before    bool
		want      string
		line, col int
	}{
		{
			name: "]p onto an unindented line", in: "\tone\n    two\n\tthree\nfour\n",
			val: register.LineValue([]byte("    two")), at: [2]int{4, 1},
			want: "\tone\n    two\n\tthree\nfour\ntwo", line: 5, col: 1,
		},
		{
			name: "[p onto an unindented line", in: "\tone\n    two\n\tthree\nfour\n",
			val: register.LineValue([]byte("    two")), at: [2]int{4, 1}, before: true,
			want: "\tone\n    two\n\tthree\ntwo\nfour", line: 4, col: 1,
		},
		{
			name: "]p from a tab onto four spaces", in: "\tone\n    two\n\tthree\nfour\n",
			val: register.LineValue([]byte("\tone")), at: [2]int{2, 1},
			want: "\tone\n    two\n    one\n\tthree\nfour", line: 3, col: 5,
		},
		{
			name: "]p keeps the block's own shape", in: "  base\n",
			val: register.LineValue([]byte("x"), []byte("  y"), []byte("    z")), at: [2]int{1, 1},
			want: "  base\n  x\n    y\n      z", line: 2, col: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			got, err := Put(PutRequest{
				Buf: b, Val: tc.val, At: at(tc.at[0], tc.at[1]),
				Before: tc.before, Indent: true, Opt: lineOpts(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
		})
	}
}

// TestPutBlock is a blockwise p and P, and the two rules that are not
// guessable are both in here.
//
// A line that stops before the cursor's column is padded out to it. And the
// block's own lines are padded on the RIGHT to the register's width, but only
// when text follows the insertion point on that line or another copy is still
// to come: pasting a ragged block at the end of a line leaves no trailing
// spaces at all, and pasting the same block into the middle of one lines up
// what comes after.
func TestPutBlock(t *testing.T) {
	const ragged = "abcdefgh\nab\nABCDEFGH\n----\n----\n----\nxx\n"
	block := register.BlockValue(3, []byte("cde"), []byte(""), []byte("CDE"))

	cases := []struct {
		name      string
		in        string
		val       register.Value
		at        [2]int
		before    bool
		count     int
		want      string
		line, col int
	}{
		{
			name: "P in column 1 pads the empty block line", in: ragged, val: block,
			at: [2]int{6, 1}, before: true,
			want: "abcdefgh\nab\nABCDEFGH\n----\n----\ncde----\n   xx\nCDE", line: 6, col: 1,
		},
		{
			name: "p at the end of a line pads nothing", in: ragged, val: block,
			at:   [2]int{4, 4},
			want: "abcdefgh\nab\nABCDEFGH\n----cde\n----\n----CDE\nxx", line: 4, col: 5,
		},
		{
			name: "P onto the last line grows the buffer", in: ragged, val: block,
			at: [2]int{7, 1}, before: true,
			want: "abcdefgh\nab\nABCDEFGH\n----\n----\n----\ncdexx\n\nCDE", line: 7, col: 1,
		},
		{
			name: "2p repeats the block sideways", in: ragged, val: block,
			at: [2]int{1, 3}, count: 2,
			want: "abccdecdedefgh\nab    \nABCCDECDEDEFGH\n----\n----\n----\nxx", line: 1, col: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			got, err := Put(PutRequest{
				Buf: b, Val: tc.val, At: at(tc.at[0], tc.at[1]),
				Before: tc.before, Count: tc.count, Opt: lineOpts(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
		})
	}
}

// TestPutEmptyRegister is vim's E353. The caller names the register in the
// message because this package never sees the name.
func TestPutEmptyRegister(t *testing.T) {
	b := buf("hello\n")
	if _, err := Put(PutRequest{Buf: b, At: at(1, 1), Opt: lineOpts()}); err != ErrEmptyRegister {
		t.Fatalf("error %v, want ErrEmptyRegister", err)
	}
	if dump(b) != "hello" {
		t.Errorf("buffer %q", dump(b))
	}
}

// TestPutEmptyCharValue is p and P of a register holding one empty line, which
// is what "y0" in column one leaves behind. vim's do_put skips the whole
// insert when the yanked length is zero -- no ml_replace, no changed_bytes,
// and the ++w_cursor.col a forward put makes is itself guarded by the same
// test -- so the buffer, the changelist and the cursor are all untouched.
//
// Run on "hello world\n" with "y0P": vim's changelist stays empty, where a
// replacement of nothing by nothing left an entry in it.
func TestPutEmptyCharValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		before bool
		col    int
	}{
		{"P in column one", true, 1},
		{"p mid-line", false, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := buf("hello world\n")
			got, err := Put(PutRequest{Buf: b, Val: register.Char([]byte{}), At: at(1, tc.col), Before: tc.before, Opt: lineOpts()})
			if err != nil {
				t.Fatalf("error %v, want a put of nothing", err)
			}
			if dump(b) != "hello world" {
				t.Errorf("buffer %q", dump(b))
			}
			if len(b.ChangeList()) != 0 {
				t.Errorf("changelist %v, want nothing recorded", b.ChangeList())
			}
			checkCursor(t, got.Cursor, 1, tc.col)
			if got.Message != "" {
				t.Errorf("message %q, want none", got.Message)
			}
		})
	}
}

// TestPutReportZero: 'report' is a real 0 and not a missing value. With
// :set report=0, "yyp" prints "1 line yanked" and then "1 more line", and a
// put that rewrites 0 as 2 prints neither.
func TestPutReportZero(t *testing.T) {
	b := buf("abcdef\nghi\n")
	opt := lineOpts()
	opt.Report = 0
	got, err := Put(PutRequest{Buf: b, Val: register.LineValue([]byte("abcdef")), At: at(1, 1), Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != "1 more line" {
		t.Errorf("message %q, want %q", got.Message, "1 more line")
	}
}
