package screen

import (
	"reflect"
	"testing"
)

// A new grid must be blanks and not zero runes. A zeroed cell renders as NUL,
// which the terminal frontend writes as nothing at all and the rasteriser draws
// as .notdef, so the difference between the two shows up as a screenful of
// boxes rather than as a subtle bug.
func TestNewGridIsBlank(t *testing.T) {
	g := NewGrid(3, 4)

	if g.Rows != 3 || g.Cols != 4 || len(g.Cells) != 12 {
		t.Fatalf("size = %dx%d len %d, want 3x4 len 12", g.Rows, g.Cols, len(g.Cells))
	}
	want(t, Dump(g), ""+
		"+----+\n"+
		"|    |\n"+
		"|    |\n"+
		"|    |\n"+
		"+----+\n")
}

// At is row-major. Writing the corners and reading them back through the flat
// slice is the only way to catch a transposed index, which otherwise looks
// correct on a square grid and wrong on every real one.
func TestGridAtIsRowMajor(t *testing.T) {
	g := NewGrid(3, 4)

	g.Set(0, 0, 'a', Normal)
	g.Set(0, 3, 'b', Normal)
	g.Set(2, 0, 'c', Normal)
	g.Set(2, 3, 'd', Normal)

	for _, w := range []struct {
		index int
		r     rune
	}{{0, 'a'}, {3, 'b'}, {8, 'c'}, {11, 'd'}} {
		if got := g.Cells[w.index].Rune; got != w.r {
			t.Errorf("Cells[%d].Rune = %q, want %q", w.index, got, w.r)
		}
	}
}

// SetString stops at the right edge instead of wrapping. Wrapping a buffer line
// onto the next row is the window renderer's decision, and it needs 'wrap',
// 'breakindent' and 'linebreak' to make it; a grid row is a row.
func TestSetStringStopsAtTheEdge(t *testing.T) {
	g := NewGrid(2, 4)

	if end := g.SetString(0, 0, "abcdefg", Normal); end != 4 {
		t.Errorf("end column = %d, want 4", end)
	}
	want(t, Dump(g), ""+
		"+----+\n"+
		"|abcd|\n"+
		"|    |\n"+
		"+----+\n")
}

// A double-width rune takes two cells and the trailing one is flagged, so a
// renderer never has to guess whether a blank belongs to the rune before it.
func TestSetStringWideRuneTakesTwoCells(t *testing.T) {
	g := NewGrid(1, 5)

	if end := g.SetString(0, 0, "a漢", Normal); end != 3 {
		t.Errorf("end column = %d, want 3", end)
	}
	if got := *g.At(0, 1); got != (Cell{Rune: '漢'}) {
		t.Errorf("lead cell = %+v, want the rune", got)
	}
	if got := *g.At(0, 2); got != (Cell{Tail: true}) {
		t.Errorf("trailing cell = %+v, want a flagged tail", got)
	}
	want(t, Dump(g), ""+
		"+-----+\n"+
		"|a漢  |\n"+
		"+-----+\n")
}

// A wide rune that would straddle the right edge is refused whole and the last
// column is left blank.
//
// That is this primitive's contract and not vim's screen. Vim draws a '>'
// there: screenchar() over a 10-column window at 'nowrap' on "a" followed by
// five U+6F22 gives "a漢漢漢漢>", which contradicts what this comment used to
// claim. The marker is put in by the window renderer, which knows the cell is
// a right-hand edge; see TestWideRuneAtTheRightEdge. Set stays blank because a
// caller filling a status line does not want a '>' at the end of it.
func TestSetStringRefusesToSplitAWideRune(t *testing.T) {
	g := NewGrid(1, 4)

	if end := g.SetString(0, 0, "a漢漢", Normal); end != 3 {
		t.Errorf("end column = %d, want 3", end)
	}
	if got := *g.At(0, 3); got != blank {
		t.Errorf("last cell = %+v, want a blank", got)
	}
	want(t, Dump(g), ""+
		"+----+\n"+
		"|a漢 |\n"+
		"+----+\n")
}

// Overwriting half of a wide pair blanks the other half. An orphan trailing
// cell is a cell the terminal frontend would emit nothing for and the
// rasteriser would draw nothing in, which reads on screen as a hole.
func TestSetBreaksWidePairsCleanly(t *testing.T) {
	g := NewGrid(1, 6)
	g.SetString(0, 0, "漢字", Normal)

	g.Set(0, 0, 'x', Normal) // over the lead of the first pair
	want(t, Dump(g), ""+
		"+------+\n"+
		"|x 字  |\n"+
		"+------+\n")

	g.Set(0, 3, 'y', Normal) // over the tail of the second pair
	want(t, Dump(g), ""+
		"+------+\n"+
		"|x  y  |\n"+
		"+------+\n")
}

// A combining mark has nowhere to live in a one-rune cell, so SetString drops
// it and keeps going rather than stopping mid-string. The mark is written as an
// escape and not as a decomposed literal, because the two are indistinguishable
// in an editor and this test is about which one it is.
func TestSetStringDropsCombiningMarks(t *testing.T) {
	g := NewGrid(1, 5)

	if end := g.SetString(0, 0, "e\u0301x", Normal); end != 2 {
		t.Errorf("end column = %d, want 2", end)
	}
	want(t, Dump(g), ""+
		"+-----+\n"+
		"|ex   |\n"+
		"+-----+\n")
}

// Fill is how a status line gets a background and how a row gets erased.
func TestFillStopsAtTheEdge(t *testing.T) {
	g := NewGrid(1, 5)

	if end := g.Fill(0, 2, 10, '-', Normal); end != 5 {
		t.Errorf("end column = %d, want 5", end)
	}
	want(t, Dump(g), ""+
		"+-----+\n"+
		"|  ---|\n"+
		"+-----+\n")
}

// Resize clears, both down and back up. Carrying cells across a size change
// would put half of the old frame at the wrong offset, and every one of those
// is a redraw bug that only reproduces while dragging a window edge.
func TestResizeDownAndUpPreservesNothing(t *testing.T) {
	g := NewGrid(3, 5)
	g.SetString(0, 0, "hello", Normal)
	g.SetString(2, 0, "world", Normal)

	g.Resize(1, 3)
	want(t, Dump(g), ""+
		"+---+\n"+
		"|   |\n"+
		"+---+\n")

	g.Resize(3, 5)
	want(t, Dump(g), ""+
		"+-----+\n"+
		"|     |\n"+
		"|     |\n"+
		"|     |\n"+
		"+-----+\n")
}

// Every row is dirty after a resize, because nothing on screen survived it.
func TestResizeMarksEverythingDirty(t *testing.T) {
	g := NewGrid(3, 3)
	g.ClearDirty()

	g.Resize(2, 4)

	if got := g.DirtyRows(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Errorf("dirty rows = %v, want [0 1]", got)
	}
}

// Dirty rows are a hint for a frontend that wants to skip whole rows before
// diffing them, and they have to survive a write that touches two rows and one
// that is refused.
func TestDirtyRowsTrackWrites(t *testing.T) {
	g := NewGrid(4, 4)
	g.ClearDirty()

	g.SetString(1, 0, "ab", Normal)
	g.Set(3, 3, '漢', Normal) // refused, will not fit
	g.Fill(2, 0, 2, ' ', Normal)

	if got := g.DirtyRows(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("dirty rows = %v, want [1 2]; row 3 was never written", got)
	}
	g.ClearDirty()
	if got := g.DirtyRows(); got != nil {
		t.Errorf("dirty rows after ClearDirty = %v, want none", got)
	}
}

// Diff returns exactly the runs that changed, coalesced, in row then column
// order. This is what the terminal frontend turns into one cursor positioning
// plus a string of ANSI per span, so a span too wide is wasted bandwidth and a
// span too narrow is a hole on screen.
func TestDiffReturnsChangedRuns(t *testing.T) {
	prev := NewGrid(3, 8)
	prev.SetString(0, 0, "hello", Normal)
	prev.SetString(1, 0, "world", Normal)

	next := NewGrid(3, 8)
	next.SetString(0, 0, "hellX", Normal) // one cell, column 4
	next.SetString(1, 0, "wArlZ", Normal) // two runs, columns 1 and 4
	next.SetString(2, 6, "ab", Normal)    // a run at the right edge

	got := next.Diff(prev)
	expect := []Span{
		{Row: 0, Col: 4, Len: 1},
		{Row: 1, Col: 1, Len: 1},
		{Row: 1, Col: 4, Len: 1},
		{Row: 2, Col: 6, Len: 2},
	}
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("Diff = %+v, want %+v", got, expect)
	}
}

// Adjacent changed cells coalesce into one span rather than one span each.
func TestDiffCoalescesAdjacentCells(t *testing.T) {
	prev := NewGrid(1, 8)
	next := NewGrid(1, 8)
	next.SetString(0, 2, "abcd", Normal)

	got := next.Diff(prev)
	expect := []Span{{Row: 0, Col: 2, Len: 4}}
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("Diff = %+v, want %+v", got, expect)
	}
}

// Two identical frames diff to nothing at all, which is the case that runs on
// every keystroke that only moves the cursor.
func TestDiffOfIdenticalFramesIsEmpty(t *testing.T) {
	prev := NewGrid(2, 4)
	prev.SetString(0, 0, "ab", Normal)
	next := NewGrid(2, 4)
	next.SetString(0, 0, "ab", Normal)

	if got := next.Diff(prev); len(got) != 0 {
		t.Errorf("Diff = %+v, want nothing", got)
	}
}

// A highlight change with no character change is still a change. A frame where
// only hlsearch turned on looks identical as text and must not be skipped.
func TestDiffSeesHighlightOnlyChanges(t *testing.T) {
	prev := NewGrid(1, 6)
	prev.SetString(0, 0, "abcdef", Normal)
	next := NewGrid(1, 6)
	next.SetString(0, 0, "abc", Normal)
	next.SetString(0, 3, "def", 4)

	got := next.Diff(prev)
	expect := []Span{{Row: 0, Col: 3, Len: 3}}
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("Diff = %+v, want %+v", got, expect)
	}
}

// A span is widened so it never starts on the trailing half of a wide pair or
// ends on the leading half. Half a glyph is not a thing either frontend can
// draw, and neither of them should have to know that.
func TestDiffWidensToWholeWidePairs(t *testing.T) {
	prev := NewGrid(1, 6)
	prev.SetString(0, 0, "漢字漢", Normal)

	next := NewGrid(1, 6)
	next.SetString(0, 0, "漢字漢", Normal)
	// Recolour only the trailing cell of the middle pair. The raw run of
	// changed cells is column 3 alone; the span must be columns 2 and 3.
	next.At(0, 3).HL = 9

	got := next.Diff(prev)
	expect := []Span{{Row: 0, Col: 2, Len: 2}}
	if !reflect.DeepEqual(got, expect) {
		t.Errorf("Diff = %+v, want %+v", got, expect)
	}
}

// A diff against a differently sized frame, or against no frame at all, is a
// full repaint. There is nothing useful to compare a 40-column buffer against
// after the window became 80 wide.
func TestDiffAgainstWrongSizeIsFullRepaint(t *testing.T) {
	next := NewGrid(2, 3)
	full := []Span{{Row: 0, Col: 0, Len: 3}, {Row: 1, Col: 0, Len: 3}}

	if got := next.Diff(nil); !reflect.DeepEqual(got, full) {
		t.Errorf("Diff(nil) = %+v, want %+v", got, full)
	}
	if got := next.Diff(NewGrid(2, 9)); !reflect.DeepEqual(got, full) {
		t.Errorf("Diff(wrong width) = %+v, want %+v", got, full)
	}
}
