package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// TestJoin is J and gJ, and every row is a vim run.
//
// The two rows that decide whether an implementation is right are "The end."
// and "end. ": the first gets TWO spaces with vim's own defaults, because
// 'joinspaces' is on under --clean whatever :help implies, and the second gets
// two as well, because the rule looks through a trailing space at the
// character before it.
func TestJoin(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		spaces    bool
		count     int
		opt       func() Options
		want      string
		line, col int
	}{
		{name: "J after a full stop", in: "The end.\nnext line\n", spaces: true, want: "The end.  next line", line: 1, col: 9},
		{name: "J with nojoinspaces", in: "The end.\nnext line\n", spaces: true,
			opt: func() Options { o := lineOpts(); o.JoinSpaces = false; return o }, want: "The end. next line", line: 1, col: 9},
		{name: "J after a bang", in: "end!\nnext\n", spaces: true, want: "end!  next", line: 1, col: 5},
		{name: "J after a bang with cpo j", in: "end!\nnext\n", spaces: true,
			opt: func() Options { o := lineOpts(); o.JoinSpacesOnlyPeriod = true; return o }, want: "end! next", line: 1, col: 5},
		{name: "J after a question mark", in: "end?\nnext\n", spaces: true, want: "end?  next", line: 1, col: 5},
		{name: "J after a quote following a stop", in: "end.\"\nnext\n", spaces: true, want: "end.\" next", line: 1, col: 6},
		{name: "J after a paren following a stop", in: "end.)\nnext\n", spaces: true, want: "end.) next", line: 1, col: 6},
		{name: "J through a trailing space", in: "end. \nnext\n", spaces: true, want: "end.  next", line: 1, col: 6},
		{name: "J strips the next line's indent", in: "foo\n   bar\n", spaces: true, want: "foo bar", line: 1, col: 4},
		{name: "J puts no space before a close paren", in: "foo\n)close\n", spaces: true, want: "foo)close", line: 1, col: 4},
		{name: "J adds no space after one", in: "foo \nbar\n", spaces: true, want: "foo bar", line: 1, col: 5},
		{name: "J onto an empty line", in: "foo\n\nbar\n", spaces: true, want: "foo\nbar", line: 1, col: 3},
		{name: "J onto a blank line", in: "foo\n   \nbar\n", spaces: true, want: "foo\nbar", line: 1, col: 3},
		{name: "J from an empty line", in: "\nfoo\n", spaces: true, want: "foo", line: 1, col: 1},
		{name: "J after a tab", in: "foo\n\t bar\n", spaces: true, want: "foo bar", line: 1, col: 4},
		{name: "3J", in: "a\nb\nc\nd\n", spaces: true, count: 3, want: "a b c\nd", line: 1, col: 4},
		{name: "1J joins two lines", in: "a\nb\nc\nd\n", spaces: true, count: 1, want: "a b\nc\nd", line: 1, col: 2},
		{name: "2J joins two lines", in: "a\nb\nc\nd\n", spaces: true, count: 2, want: "a b\nc\nd", line: 1, col: 2},
		{name: "gJ keeps the indent", in: "foo\n   bar\n", want: "foo   bar", line: 1, col: 4},
		{name: "gJ adds nothing after a stop", in: "The end.\nnext line\n", want: "The end.next line", line: 1, col: 9},
		{name: "gJ onto an empty line", in: "foo\n\nbar\n", want: "foo\nbar", line: 1, col: 3},
		{name: "3gJ", in: "a\nb\nc\nd\n", count: 3, want: "abc\nd", line: 1, col: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			opt := lineOpts()
			if tc.opt != nil {
				opt = tc.opt()
			}
			lines, ok := JoinCount(b, 1, tc.count)
			if !ok {
				t.Fatal("JoinCount says there is nothing to join")
			}
			got, err := Apply(Request{
				Buf: b, Op: OpJoin, Spaces: tc.spaces, At: at(1, 1),
				Span: SpanForLines(b, 1, lines), Opt: opt,
			})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
			if got.Message != "" {
				t.Errorf("message %q, want none: J never reports", got.Message)
			}
		})
	}
}

// TestJoinCount is nv_join's arithmetic. A count over the end of the buffer is
// cut back to what is there, but only when it was more than two: 5J two lines
// from the end joins two lines and 2J on the last line beeps.
func TestJoinCount(t *testing.T) {
	b := buf("a\nb\nc\nd\n")
	cases := []struct {
		line, count int
		want        int
		ok          bool
	}{
		{1, 0, 2, true},
		{1, 1, 2, true},
		{1, 2, 2, true},
		{1, 3, 3, true},
		{1, 9, 4, true},
		{3, 5, 2, true},
		{4, 2, 0, false},
		{4, 1, 0, false},
		{4, 9, 0, false},
	}
	for _, tc := range cases {
		got, ok := JoinCount(b, tc.line, tc.count)
		if got != tc.want || ok != tc.ok {
			t.Errorf("JoinCount(line %d, count %d) = %d %v, want %d %v", tc.line, tc.count, got, ok, tc.want, tc.ok)
		}
	}
}

// TestJoinOnTheLastLine: J with nothing under it is an error and not a no-op,
// because the mode machine has to beep and abandon a dot record.
func TestJoinOnTheLastLine(t *testing.T) {
	b := buf("The end.\nnext line\n")
	span := Span{Type: register.TypeLine, Range: text.Range{Start: text.Pos{Line: 2}, End: text.Pos{Line: 2}}}
	if _, err := Apply(Request{Buf: b, Op: OpJoin, Spaces: true, Span: span, At: at(2, 1), Opt: lineOpts()}); err != ErrNoRange {
		t.Fatalf("error %v, want ErrNoRange", err)
	}
	if dump(b) != "The end.\nnext line" {
		t.Errorf("buffer %q", dump(b))
	}
}

// TestJoinWritesNoRegister: J is not a delete, however much it looks like one.
func TestJoinWritesNoRegister(t *testing.T) {
	b := buf("a\nb\n")
	regs := &recorder{}
	if _, err := Apply(Request{Buf: b, Regs: regs, Op: OpJoin, Spaces: true, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	if regs.sawAnyCall {
		t.Error("J touched a register")
	}
}
