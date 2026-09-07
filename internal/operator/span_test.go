package operator

import (
	"testing"

	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// TestSpanForMotionExclusiveRules is :help exclusive, and both halves of it
// were run on " aaa bbb / ?? ccc / (blank) / ?? ddd":
//
//	d} from column 1 or column 3 deletes two WHOLE lines, register linewise
//	d} from column 5 deletes "a bbb\n ccc", register charwise
//	d/ccc deletes " aaa bbb\n ", register charwise
//
// All three motions end in the same place. The only thing that separates them
// is whether the end is in column 1 and whether the start is in the indent.
func TestSpanForMotionExclusiveRules(t *testing.T) {
	b := buf("  aaa bbb\n  ccc\n\n  ddd\n")

	cases := []struct {
		name  string
		start text.Pos
		to    text.Pos
		kind  motion.Kind
		want  Span
	}{
		{
			name:  "d} from column 1 becomes linewise",
			start: at(1, 1), to: at(3, 1), kind: motion.KindCharExclusive,
			want: Span{Type: register.TypeLine, Range: text.Range{Start: text.Pos{Line: 1}, End: text.Pos{Line: 2}}, EndAdjusted: true, OpStart: at(1, 1), FoldAdjusted: true},
		},
		{
			name:  "d} from the first non-blank becomes linewise",
			start: at(1, 3), to: at(3, 1), kind: motion.KindCharExclusive,
			want: Span{Type: register.TypeLine, Range: text.Range{Start: text.Pos{Line: 1}, End: text.Pos{Line: 2}}, EndAdjusted: true, OpStart: at(1, 3), FoldAdjusted: true},
		},
		{
			name:  "d} from mid-word becomes inclusive and stops at the line end",
			start: at(1, 5), to: at(3, 1), kind: motion.KindCharExclusive,
			want: Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 5), End: text.Pos{Line: 2, Col: 5}}, EndAdjusted: true, OpStart: at(1, 5), FoldAdjusted: true},
		},
		{
			name:  "an exclusive end that is not in column 1 is left alone",
			start: at(1, 1), to: at(2, 3), kind: motion.KindCharExclusive,
			want: Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(2, 3)}, OpStart: at(1, 1), FoldAdjusted: true},
		},
		{
			name:  "an inclusive motion covers the character it lands on",
			start: at(1, 1), to: at(1, 5), kind: motion.KindCharInclusive,
			want: Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 6)}, OpStart: at(1, 1), FoldAdjusted: true},
		},
		{
			name:  "a linewise motion gives whole lines",
			start: at(1, 4), to: at(2, 2), kind: motion.KindLine,
			want: Span{Type: register.TypeLine, Range: text.Range{Start: text.Pos{Line: 1}, End: text.Pos{Line: 2}}, OpStart: at(1, 4), FoldAdjusted: true},
		},
		{
			name:  "a backward motion is swapped",
			start: at(2, 3), to: at(1, 2), kind: motion.KindCharExclusive,
			want: Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 2), End: at(2, 3)}, OpStart: at(1, 2), FoldAdjusted: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SpanForMotion(b, tc.start, motion.Result{To: tc.to, Kind: tc.kind, Ok: true}, motion.ForceNone, lineOpts())
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("span %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSpanForMotionExclusiveNeedsTwoLines: an exclusive motion that ends in
// column 1 of the line it started on has nothing to give back, so neither
// adjustment fires.
func TestSpanForMotionExclusiveNeedsTwoLines(t *testing.T) {
	b := buf("  aaa\n")
	got, err := SpanForMotion(b, at(1, 4), motion.Result{To: at(1, 1), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceNone, lineOpts())
	if err != nil {
		t.Fatal(err)
	}
	want := Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 1), End: at(1, 4)}, OpStart: at(1, 1), FoldAdjusted: true}
	if got != want {
		t.Errorf("span %+v, want %+v", got, want)
	}
}

// TestSpanForMotionForce is o_v, o_V and o_CTRL-V: the force replaces the
// motion's kind, and on a charwise motion it swaps inclusive for exclusive.
func TestSpanForMotionForce(t *testing.T) {
	b := buf("abcdef\nghijkl\n")

	t.Run("v makes an exclusive motion inclusive", func(t *testing.T) {
		got, err := SpanForMotion(b, at(1, 1), motion.Result{To: at(1, 4), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceChar, lineOpts())
		if err != nil {
			t.Fatal(err)
		}
		if got.Range.End != at(1, 5) {
			t.Errorf("end %s, want %s", vimPos(got.Range.End), vimPos(at(1, 5)))
		}
	})

	t.Run("V forces linewise", func(t *testing.T) {
		got, err := SpanForMotion(b, at(1, 2), motion.Result{To: at(2, 3), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceLine, lineOpts())
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != register.TypeLine || got.Range.Start.Line != 1 || got.Range.End.Line != 2 {
			t.Errorf("span %+v", got)
		}
	})

	t.Run("CTRL-V forces a block in display columns", func(t *testing.T) {
		b := buf("\tone\n    two\n")
		got, err := SpanForMotion(b, at(1, 2), motion.Result{To: at(2, 5), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceBlock, vimrcOpts())
		if err != nil {
			t.Fatal(err)
		}
		// With ts=2 the tab is two cells wide, so byte 1 of "\tone" is display
		// column 2, and byte 4 of " two" is display column 4.
		want := Block{First: 1, Last: 2, Left: 2, Right: 4}
		if got.Type != register.TypeBlock || got.Block != want {
			t.Errorf("block %+v, want %+v", got.Block, want)
		}
	})
}

// TestSpanForMotionFailedMotion: a motion that did not happen abandons the
// whole command, operator and all.
func TestSpanForMotionFailedMotion(t *testing.T) {
	b := buf("abc\n")
	if _, err := SpanForMotion(b, at(1, 1), motion.Result{To: at(1, 3)}, motion.ForceNone, lineOpts()); err != ErrNoRange {
		t.Fatalf("error %v, want ErrNoRange", err)
	}
}

// TestSpanForChars is x and s: forward over count characters, stopping at the
// end of the line however big the count. 99x on a three-character line takes
// three characters and does not reach the next one.
func TestSpanForChars(t *testing.T) {
	b := buf("hello world\nsecond\n")
	for _, tc := range []struct {
		count int
		want  text.Pos
	}{{1, at(1, 2)}, {3, at(1, 4)}, {99, at(1, 12)}} {
		got := SpanForChars(b, at(1, 1), tc.count)
		if got.Range.End != tc.want {
			t.Errorf("%dx end %s, want %s", tc.count, vimPos(got.Range.End), vimPos(tc.want))
		}
	}
}

// TestSpanForCharsBefore is X, which in column 1 covers nothing at all: 3X at
// the start of a line changes no text and writes no register, measured.
func TestSpanForCharsBefore(t *testing.T) {
	b := buf("hello\n")
	if got := SpanForCharsBefore(b, at(1, 1), 3); !got.Empty() {
		t.Errorf("X in column 1 gave %+v, want an empty span", got)
	}
	got := SpanForCharsBefore(b, at(1, 3), 2)
	if got.Range.Start != at(1, 1) || got.Range.End != at(1, 3) {
		t.Errorf("2X span %s..%s", vimPos(got.Range.Start), vimPos(got.Range.End))
	}
}

// TestSpanForCharsMultibyte: a count is characters and not bytes, so 2x on
// two-byte runes takes four bytes.
func TestSpanForCharsMultibyte(t *testing.T) {
	b := buf("éèê\n")
	if got := SpanForChars(b, at(1, 1), 2); got.Range.End.Col != 4 {
		t.Errorf("2x end column %d, want 4 bytes for two runes", got.Range.End.Col)
	}
	// The cursor sits on the third rune, byte 4. 2X takes the two runes
	// before it and stops at the start of the line.
	if got := SpanForCharsBefore(b, text.Pos{Line: 1, Col: 4}, 2); got.Range.Start.Col != 0 {
		t.Errorf("2X start column %d, want 0", got.Range.Start.Col)
	}
}

// TestSpanForLines is the dd and yy shape: linewise, from the cursor's line,
// count lines, clamped to the end of the buffer. The clamp is the case that
// matters, because 5dd two lines from the end deletes two lines in vim and
// panics in an editor that trusts the count.
func TestSpanForLines(t *testing.T) {
	b := buf("one\ntwo\nthree\n")
	cases := []struct {
		name              string
		first, count      int
		wantFirst, wantAt int
	}{
		{"one line", 2, 1, 2, 2},
		{"two lines", 1, 2, 1, 2},
		{"count past the end", 2, 9, 2, 3},
		{"no count", 3, 0, 3, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := SpanForLines(b, tc.first, tc.count)
			if s.Type != register.TypeLine {
				t.Errorf("Type = %v, want linewise", s.Type)
			}
			if s.Range.Start.Line != tc.wantFirst || s.Range.End.Line != tc.wantAt {
				t.Errorf("lines %d..%d, want %d..%d",
					s.Range.Start.Line, s.Range.End.Line, tc.wantFirst, tc.wantAt)
			}
		})
	}
}

// TestSpanLines is the "which lines does this touch" question every message
// and every linewise operator asks. A charwise end in column 0 does not touch
// the line it names.
func TestSpanLines(t *testing.T) {
	cases := []struct {
		name        string
		span        Span
		first, last int
	}{
		{
			"charwise ending in column 0",
			Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 3), End: text.Pos{Line: 3, Col: 0}}},
			1, 2,
		},
		{
			// The same end, on a line the span does cover because the line
			// has no bytes for it to stop before. vim counts it: "ly2)" over
			// a paragraph whose last line is blank says "3 lines yanked".
			"charwise ending in column 0 of an empty line",
			Span{Type: register.TypeChar, EndsOnEmptyLine: true, Range: text.Range{Start: at(1, 3), End: text.Pos{Line: 3, Col: 0}}},
			1, 3,
		},
		{
			"charwise ending mid-line",
			Span{Type: register.TypeChar, Range: text.Range{Start: at(1, 3), End: at(3, 2)}},
			1, 3,
		},
		{
			"charwise within one line",
			Span{Type: register.TypeChar, Range: text.Range{Start: at(2, 1), End: at(2, 4)}},
			2, 2,
		},
		{
			"linewise",
			Span{Type: register.TypeLine, Range: text.Range{Start: text.Pos{Line: 2}, End: text.Pos{Line: 5}}},
			2, 5,
		},
		{
			"blockwise",
			Span{Type: register.TypeBlock, Block: Block{First: 3, Last: 7, Left: 1, Right: 4}},
			3, 7,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, last := tc.span.Lines()
			if first != tc.first || last != tc.last {
				t.Errorf("lines %d..%d, want %d..%d", first, last, tc.first, tc.last)
			}
		})
	}
}

// TestSpanForMotionInclusiveOnEmptyLine is d$ and dg_ on a blank line.
//
// An inclusive motion covers the character it lands on, and on an empty line
// there is no character to cover. Taking the position itself instead turns a
// zero-width span into a one-column one, which is not a rounding error: it is
// the difference between vim's op_delete returning early and pvim running the
// delete and writing the unnamed register with an empty charwise value. Run on
// "alpha beta\n\ngamma\n" with "ywj$d$p", vim leaves line 2 as "alpha " and an
// inflated span leaves it blank.
func TestSpanForMotionInclusiveOnEmptyLine(t *testing.T) {
	b := buf("alpha\n\nbravo\n")
	got, err := SpanForMotion(b, at(2, 1), motion.Result{To: at(2, 1), Kind: motion.KindCharInclusive, Ok: true}, motion.ForceNone, lineOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Empty() {
		t.Errorf("span %+v covers a column, want nothing at all", got)
	}
	if !got.EndsOnEmptyLine {
		t.Error("EndsOnEmptyLine is false, so nothing downstream can tell this from d0")
	}
}

// TestSpanForMotionInclusiveEndsOnEmptyLine is 2dg_ over a line and a blank
// one. The end is in column 0 of a line the span does cover, which is the
// case Lines() cannot read off the columns, and it is also what lets
// op_delete's "the result is a blank line" rule promote the delete to
// linewise: run on "abc\n\nxyz\n", 2dg_ leaves "xyz" and a LINEWISE register
// holding "abc\n\n".
func TestSpanForMotionInclusiveEndsOnEmptyLine(t *testing.T) {
	b := buf("abc\n\nxyz\n")
	got, err := SpanForMotion(b, at(1, 1), motion.Result{To: at(2, 1), Kind: motion.KindCharInclusive, Ok: true}, motion.ForceNone, lineOpts())
	if err != nil {
		t.Fatal(err)
	}
	if got.Range.End != (text.Pos{Line: 2}) {
		t.Errorf("end %s, want line 2 column 1", vimPos(got.Range.End))
	}
	if !got.EndsOnEmptyLine {
		t.Error("EndsOnEmptyLine is false, so Lines() counts one line too few")
	}
	if first, last := got.Lines(); first != 1 || last != 2 {
		t.Errorf("lines %d..%d, want 1..2", first, last)
	}
}

// TestSpanForMotionExclusiveOntoEmptyLine is the other way in: an exclusive
// motion ending in column 1 gives the column back and lands on the END of the
// previous line, and when that line is empty its end is column 0 again. vim
// decrements oap->line_count once, in the adjustment, and never derives it
// from the columns afterwards, so the empty line counts. Measured with
// "ly2)" over "hello world\nfoo bar baz\n\nlast_word here\n": vim says
// "3 lines yanked".
func TestSpanForMotionExclusiveOntoEmptyLine(t *testing.T) {
	b := buf("hello world\nfoo bar baz\n\nlast_word here\n")
	got, err := SpanForMotion(b, at(1, 2), motion.Result{To: at(4, 1), Kind: motion.KindCharExclusive, Ok: true}, motion.ForceNone, lineOpts())
	if err != nil {
		t.Fatal(err)
	}
	if got.Range.End != (text.Pos{Line: 3}) {
		t.Errorf("end %s, want line 3 column 1", vimPos(got.Range.End))
	}
	if !got.EndsOnEmptyLine {
		t.Error("EndsOnEmptyLine is false after the adjustment landed on a blank line")
	}
	if first, last := got.Lines(); first != 1 || last != 3 {
		t.Errorf("lines %d..%d, want 1..3", first, last)
	}
}

// TestSpanForToEndOfLineOnEmptyLine is D and C on a blank line: nothing to
// take, and the flag that says the end is on a line with no bytes in it,
// which is what tells C to yank an empty register where c0 leaves the
// register alone.
func TestSpanForToEndOfLineOnEmptyLine(t *testing.T) {
	b := buf("alpha\n\nbravo\n")
	got := SpanForToEndOfLine(b, at(2, 1), 1)
	if !got.Empty() {
		t.Errorf("span %+v, want nothing at all", got)
	}
	if !got.EndsOnEmptyLine {
		t.Error("EndsOnEmptyLine is false on a blank line")
	}
	if got := SpanForToEndOfLine(b, at(1, 1), 1); got.EndsOnEmptyLine {
		t.Error("EndsOnEmptyLine is true on a line with text on it")
	}
}
