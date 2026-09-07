package operator

import "testing"

// = is the one operator here that does not match vim, and the tests say so.
//
// vim runs get_c_indent() whatever the file is, because 'equalprg' is empty and
// there is no 'indentexpr': = G on "alpha / (4)beta / (8)gamma / delta" strips
// every indent to zero, and on "foo { / bar / baz; / } / qux" it lays the file
// out in C with sw=4. Both were run. This package does what
// instead, 'autoindent'-style, and the cursor and the two messages ARE vim's
// because the oracle diffs them.

// TestIndentCopiesThePreviousLine is the 'autoindent'-style rule: every line
// takes the indent of the last non-blank line above it.
func TestIndentCopiesThePreviousLine(t *testing.T) {
	b := buf("    alpha\nbeta\n        gamma\ndelta\n")
	opt := lineOpts()
	got, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 2, 2), At: at(2, 1), Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	if dump(b) != "    alpha\n    beta\n    gamma\ndelta" {
		t.Errorf("buffer\n%q", dump(b))
	}
	checkCursor(t, got.Cursor, 2, 5)
}

// TestIndentAtTheTopOfTheBuffer: with no line above it, the first line of the
// range goes to column 0.
func TestIndentAtTheTopOfTheBuffer(t *testing.T) {
	b := buf("      one\n  two\n")
	if _, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	if dump(b) != "one\ntwo" {
		t.Errorf("buffer %q", dump(b))
	}
}

// TestIndentLeavesBlankLinesAlone, and a blank line does not become the line
// the next one copies its indent from.
func TestIndentLeavesBlankLinesAlone(t *testing.T) {
	b := buf("    one\n\n        two\n")
	if _, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 1, 3), At: at(1, 1), Opt: lineOpts()}); err != nil {
		t.Fatal(err)
	}
	if dump(b) != "one\n\ntwo" {
		t.Errorf("buffer %q", dump(b))
	}
}

// TestIndentSmartIndent is the whole indent engine the vimrc asks for: a
// shiftwidth after a line ending in '{' or starting with one of 'cinwords',
// and a shiftwidth back for a line starting with '}'.
func TestIndentSmartIndent(t *testing.T) {
	opt := lineOpts()
	opt.ShiftWidth, opt.ExpandTab = 4, true

	t.Run("braces", func(t *testing.T) {
		o := opt
		o.SmartIndent = true
		b := buf("func f() {\nbody\nmore\n}\nafter\n")
		if _, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 1, 5), At: at(1, 1), Opt: o}); err != nil {
			t.Fatal(err)
		}
		if dump(b) != "func f() {\n    body\n    more\n}\nafter" {
			t.Errorf("buffer\n%q", dump(b))
		}
	})

	t.Run("cinwords", func(t *testing.T) {
		o := opt
		o.SmartIndent = true
		o.CinWords = []string{"if", "else", "for", "while", "def", "class"}
		b := buf("def f():\nbody\nmore\n")
		if _, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 1, 3), At: at(1, 1), Opt: o}); err != nil {
			t.Fatal(err)
		}
		if dump(b) != "def f():\n    body\n    more" {
			t.Errorf("buffer\n%q", dump(b))
		}
	})

	t.Run("a cinword is a whole word", func(t *testing.T) {
		o := opt
		o.SmartIndent = true
		o.CinWords = []string{"if"}
		b := buf("iffy thing\nbody\n")
		if _, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 1, 2), At: at(1, 1), Opt: o}); err != nil {
			t.Fatal(err)
		}
		if dump(b) != "iffy thing\nbody" {
			t.Errorf("buffer %q: \"iffy\" is not \"if\"", dump(b))
		}
	})
}

// TestIndentMessages are vim's, trailing spaces and all: = G over five lines
// prints "4 lines to indent... " and then "5 lines indented ", and the byte
// dump of the redirected message file has a space at the end of each.
func TestIndentMessages(t *testing.T) {
	cases := []struct {
		lines int
		want  string
	}{
		{5, "4 lines to indent... \n5 lines indented "},
		{3, "2 lines to indent... \n3 lines indented "},
		{2, ""},
		{1, ""},
	}
	for _, tc := range cases {
		b := buf("one\ntwo\nthree\nfour\nfive\n")
		got, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 1, tc.lines), At: at(1, 1), Opt: lineOpts()})
		if err != nil {
			t.Fatal(err)
		}
		if got.Message != tc.want {
			t.Errorf("%d lines: message %q, want %q", tc.lines, got.Message, tc.want)
		}
	}
}

// TestIndentSingleLineMessage: with 'report' at zero even one line reports,
// and the progress line stays away because there is nothing to be patient
// about. Measured with :set report=0 and ==.
func TestIndentSingleLineMessage(t *testing.T) {
	b := buf("one\ntwo\n")
	opt := lineOpts()
	opt.Report = -1
	got, err := Apply(Request{Buf: b, Op: OpIndent, Span: SpanForLines(b, 1, 1), At: at(1, 1), Opt: opt})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != "1 line indented " {
		t.Errorf("message %q", got.Message)
	}
}

// TestFold records the line range and changes nothing. The cursor stays where
// it was, moved onto the span's first line: zf j from column 3 leaves it in
// column 3, and G zf k moves it up a line, both run.
func TestFold(t *testing.T) {
	b := buf("\tone\n    two\n\tthree\nfour\n")
	cases := []struct {
		name        string
		first, last int
		at          [2]int
		line, col   int
	}{
		{"zfj from column 5", 2, 3, [2]int{2, 5}, 2, 5},
		{"zfj from column 7", 2, 3, [2]int{2, 7}, 2, 7},
		{"G zfk", 3, 4, [2]int{4, 1}, 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := dump(b)
			got, err := Apply(Request{
				Buf: b, Op: OpFold, Span: SpanForLines(b, tc.first, tc.last-tc.first+1),
				At: at(tc.at[0], tc.at[1]), Opt: lineOpts(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if dump(b) != before {
				t.Error("zf changed the buffer")
			}
			if got.FoldFirst != tc.first || got.FoldLast != tc.last {
				t.Errorf("fold %d..%d, want %d..%d", got.FoldFirst, got.FoldLast, tc.first, tc.last)
			}
			checkCursor(t, got.Cursor, tc.line, tc.col)
		})
	}
}

// TestApplyRejectsANonOperator: p is not an Op and neither is a bare motion,
// and a caller that hands one over gets an error rather than a silent no-op
// that loses an edit.
func TestApplyRejectsANonOperator(t *testing.T) {
	b := buf("one\n")
	if _, err := Apply(Request{Buf: b, Op: OpNone, Span: SpanForLines(b, 1, 1), Opt: lineOpts()}); err == nil {
		t.Error("Apply took OpNone without complaining")
	}
	if _, err := Apply(Request{Op: OpDelete}); err == nil {
		t.Error("Apply took a nil buffer without complaining")
	}
}
