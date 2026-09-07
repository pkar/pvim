package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// TestCaseOperators is gu, gU, g~ and g?. The cursor rule is the one worth a
// test: a linewise case operator lands on COLUMN 0 and not on the first
// non-blank, so gU j on a tab-indented line leaves the cursor on the tab.
func TestCaseOperators(t *testing.T) {
	cases := []struct {
		name      string
		op        Op
		in        string
		span      func(*text.Buffer) Span
		want      string
		line, col int
	}{
		{
			name: "gUU", op: OpUpper, in: "hello world\nsecond line\n",
			span: func(b *text.Buffer) Span { return SpanForLines(b, 1, 1) },
			want: "HELLO WORLD\nsecond line", line: 1, col: 1,
		},
		{
			name: "gUw", op: OpUpper, in: "hello world\n",
			span: func(*text.Buffer) Span {
				return Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 7)}}
			},
			want: "HELLO world", line: 1, col: 1,
		},
		{
			name: "g~~", op: OpToggle, in: "hello WORLD\n",
			span: func(b *text.Buffer) Span { return SpanForLines(b, 1, 1) },
			want: "HELLO world", line: 1, col: 1,
		},
		{
			name: "g?w", op: OpRot13, in: "hello world\n",
			span: func(*text.Buffer) Span {
				return Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 6)}}
			},
			want: "uryyb world", line: 1, col: 1,
		},
		{
			name: "guw on capitals", op: OpLower, in: "HELLO WORLD\n",
			span: func(*text.Buffer) Span {
				return Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 6)}}
			},
			want: "hello WORLD", line: 1, col: 1,
		},
		{
			// gU j over an indented line: column 0, on the tab.
			name: "gUj lands in column 0", op: OpUpper, in: "\tone\n    two\n\tthree\n",
			span: func(b *text.Buffer) Span { return SpanForLines(b, 1, 2) },
			want: "\tONE\n    TWO\n\tthree", line: 1, col: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := buf(tc.in)
			regs := &recorder{}
			got, err := Apply(Request{Buf: b, Regs: regs, Op: tc.op, Span: tc.span(b), At: at(1, 1), Opt: lineOpts()})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != tc.want {
				t.Errorf("buffer\n%q\nwant\n%q", dump(b), tc.want)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
			if regs.sawAnyCall {
				t.Error("a case operator touched a register")
			}
		})
	}
}

// TestCaseMessage counts the lines the SPAN covers, not the characters that
// changed: gU G over six lines says "6 lines changed" even when four of them
// were already capitals.
func TestCaseMessage(t *testing.T) {
	b := buf("A\nB\nC\nd\ne\nf\n")
	got, err := Apply(Request{Buf: b, Op: OpUpper, Span: SpanForLines(b, 1, 6), At: at(1, 1), Opt: lineOpts()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != "6 lines changed" {
		t.Errorf("message %q", got.Message)
	}
}

// TestRot13IsAsciiOnly: g? maps the 52 ASCII letters and leaves everything
// else, digits and accented letters included, exactly as it found them.
func TestRot13IsAsciiOnly(t *testing.T) {
	for _, tc := range []struct{ in, want rune }{
		{'a', 'n'}, {'n', 'a'}, {'z', 'm'}, {'A', 'N'}, {'M', 'Z'},
		{'0', '0'}, {'-', '-'}, {'é', 'é'}, {'Ж', 'Ж'},
	} {
		if got := rot13(tc.in); got != tc.want {
			t.Errorf("rot13(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCaseAcrossALineBreak: a charwise span that crosses a line arrives as one
// byte slice with an LF in it, and the LF has to come out the other side.
func TestCaseAcrossALineBreak(t *testing.T) {
	b := buf("abc\ndef\n")
	span := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 2), End: at(2, 3)}}
	if _, err := Apply(Request{Buf: b, Op: OpUpper, Span: span, At: at(1, 2), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	if dump(b) != "aBC\nDEf" {
		t.Errorf("buffer %q", dump(b))
	}
}
