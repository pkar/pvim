package raster

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"testing"

	"github.com/pkar/pvim/internal/screen"
)

// frameFor is a small grid with two highlight groups in it, which is the least
// a stamp test needs: one group to recolour and one to leave alone.
func frameFor(t *testing.T) (Frame, *screen.Table) {
	t.Helper()
	tbl := screen.NewTable()
	id := tbl.Set("Search", screen.Highlight{FG: screen.RGB{R: 0xff}, BG: screen.RGB{B: 0x40}})
	g := screen.NewGrid(4, 8)
	g.SetString(0, 0, "alpha", screen.Normal)
	g.SetString(1, 0, "beta", id)
	return Frame{Grid: g, HL: tbl, CursorRow: 1, CursorCol: 2}, tbl
}

// TestStampIsStableAndNeverZero. Stable because a stamp that changed on its own
// would make every frame a full repaint and there would be no point to any of
// it; never zero because zero is what a frontend uses for a buffer nothing has
// been painted into.
func TestStampIsStableAndNeverZero(t *testing.T) {
	f, _ := frameFor(t)
	opt := PaintOptions{Face: testFace(t, 1)}

	first := StampOf(f, opt)
	if first == 0 {
		t.Fatal("StampOf returned zero, which means no frame")
	}
	if second := StampOf(f, opt); second != first {
		t.Errorf("the same frame stamped twice gives %d and %d", first, second)
	}
}

// TestStampSeesWhatTheDiffCannot is the whole reason this exists. Every change
// below leaves the grid byte-identical and changes what is on the screen, so a
// frontend trusting Grid.Diff alone would show the old pixels.
func TestStampSeesWhatTheDiffCannot(t *testing.T) {
	base, tbl := frameFor(t)
	opt := PaintOptions{Face: testFace(t, 1)}
	was := StampOf(base, opt)

	t.Run("a group recoloured in place", func(t *testing.T) {
		// Table.Set writes into the slice the previous frame was painted from,
		// so the frame still points at the same table and the same cells.
		tbl.Set("Search", screen.Highlight{FG: screen.RGB{G: 0xff}})
		if got := StampOf(base, opt); got == was {
			t.Error(":hi Search guifg=... did not change the stamp")
		}
	})

	t.Run("linespace", func(t *testing.T) {
		if got := StampOf(base, PaintOptions{Face: opt.Face, Linespace: 2}); got == StampOf(base, opt) {
			t.Error("'linespace' did not change the stamp")
		}
	})

	t.Run("the scale", func(t *testing.T) {
		two := PaintOptions{Face: testFace(t, 2)}
		if StampOf(base, two) == StampOf(base, opt) {
			t.Error("a face at another scale did not change the stamp")
		}
	})

	t.Run("the cursor shape", func(t *testing.T) {
		bar := base
		bar.Cursor = CursorBar
		if StampOf(bar, opt) == StampOf(base, opt) {
			t.Error("the cursor shape did not change the stamp")
		}
	})
}

// TestStampSeesAFontChangeTheMetricsHide is the case the scale subtest above
// cannot reach. Changing the scale always moves CellW, so a stamp over the
// metrics alone passes that test and still leaves the window in the old font
// after a `:set guifont=` between two faces whose metrics agree.
//
// Monaco at 13pt and at 12.8pt is such a pair: 8x18 with a 13px ascent at
// scale 1, 16x35 with a 26px ascent at scale 2, the same four integers both
// times, because the ceilings that produce them swallow the 0.2pt. The glyphs
// do not agree -- one is rasterised at 25.6px against the other's 26 -- so
// every pixel on screen changes and nothing in Metrics does. Eight of the
// faces on a stock macOS box collide the same way at a single size, so this is
// reachable with `:set guifont=NewYork:h13` followed by `:set guifont=SFNS:h13`
// and no fractional point size at all.
//
// The two premises are asserted rather than assumed: if the metrics ever stop
// matching, the old stamp catches the change and this test has quietly stopped
// testing anything, and if the glyphs ever start matching there is nothing here
// to catch.
func TestStampSeesAFontChangeTheMetricsHide(t *testing.T) {
	mono, err := LoadFont(DefaultFontPath)
	if err != nil {
		t.Fatalf("loading %s: %v", DefaultFontPath, err)
	}
	base, _ := frameFor(t)

	for _, scale := range []int{1, 2} {
		big, err := mono.Face(DefaultPointSize, scale)
		if err != nil {
			t.Fatalf("Monaco at %vpt scale %d: %v", DefaultPointSize, scale, err)
		}
		small, err := mono.Face(DefaultPointSize-0.2, scale)
		if err != nil {
			t.Fatalf("Monaco at %vpt scale %d: %v", DefaultPointSize-0.2, scale, err)
		}

		if a, b := big.Metrics(), small.Metrics(); a != b {
			t.Fatalf("scale %d: Monaco h13 is %+v and h12.8 is %+v; they no longer collide, "+
				"so this test needs another pair of faces with equal metrics and different glyphs", scale, a, b)
		}
		if a, b := glyphHash(t, big), glyphHash(t, small); a == b {
			t.Fatalf("scale %d: Monaco h13 and h12.8 rasterise identically (%x); "+
				"there is no font change here to see", scale, a)
		}

		optBig := PaintOptions{Face: big}
		optSmall := PaintOptions{Face: small}
		if StampOf(base, optBig) == StampOf(base, optSmall) {
			t.Errorf("scale %d: :set guifont=Monaco:h12.8 over Monaco:h13 did not change the stamp, "+
				"so Damage takes the fast path and the window keeps the 13pt glyphs", scale)
		}

		// And the fast path is the thing that goes wrong, so drive it.
		prev := Painted{Grid: screen.NewGrid(base.Grid.Rows, base.Grid.Cols), Stamp: StampOf(base, optBig),
			CursorRow: base.CursorRow, CursorCol: base.CursorCol}
		copy(prev.Grid.Cells, base.Grid.Cells)
		if spans, _, full := Damage(prev, base, optSmall); !full {
			t.Errorf("scale %d: Damage across a font change returned %d spans and full=false; "+
				"every cell outside them stays in the old font", scale, len(spans))
		}
	}
}

// TestStampSurvivesTheSameFaceRebuilt is the other side of the test above. A
// face identified by its address, or by a serial handed out per face, would
// make `:set guifont=Monaco:h13` when that is already the font a full repaint,
// and so would anything else in the frontend that happens to rebuild a face.
func TestStampSurvivesTheSameFaceRebuilt(t *testing.T) {
	base, _ := frameFor(t)
	a := StampOf(base, PaintOptions{Face: testFace(t, 1)})
	b := StampOf(base, PaintOptions{Face: testFace(t, 1)})
	if a != b {
		t.Errorf("two faces built from the same file at the same size and scale stamp %d and %d, "+
			"which makes rebuilding a face a full repaint", a, b)
	}
}

// glyphHash is a face's rasterisation of a handful of runes, which is what
// distinguishes two faces that share their metrics.
func glyphHash(t *testing.T, f Face) uint64 {
	t.Helper()
	h := fnv.New64a()
	for _, r := range "MgW#abcXYZ" {
		mask, off := f.Glyph(r, false, false)
		fmt.Fprintf(h, "%v%v", off, mask.Rect)
		h.Write(mask.Pix)
	}
	return h.Sum64()
}

// TestStampIgnoresTheCursorPosition is the other half of the shape case above,
// and it is what the fast path is built on: a caret moving must not cost a full
// repaint, so Damage covers the two cells itself.
func TestStampIgnoresTheCursorPosition(t *testing.T) {
	base, _ := frameFor(t)
	opt := PaintOptions{Face: testFace(t, 1)}
	moved := base
	moved.CursorCol = base.CursorCol + 1
	if StampOf(moved, opt) != StampOf(base, opt) {
		t.Error("moving the caret changed the stamp, which makes every keystroke a full repaint")
	}
}

// TestDamageWithNoPreviousFrameIsFull: a fresh buffer, a resized one and one
// whose paint failed all arrive here as a zero Painted, and every cell has to
// be in a span.
func TestDamageWithNoPreviousFrameIsFull(t *testing.T) {
	f, _ := frameFor(t)
	opt := PaintOptions{Face: testFace(t, 1)}

	spans, now, full := Damage(Painted{}, f, opt)
	if !full {
		t.Error("Damage over no previous frame did not report a full repaint")
	}
	if now.Stamp == 0 {
		t.Error("Damage returned a Painted with no stamp in it")
	}
	assertCovers(t, spans, f.Grid, allCells(f.Grid))
}

// TestDamageIsFullWheneverTheBufferCannotBeTrusted walks the four ways a
// previous frame is worthless. Each one leaves the window showing stale
// pixels if it is missed, and only the first of them is visible in the grid.
func TestDamageIsFullWheneverTheBufferCannotBeTrusted(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 1)}

	settle := func(t *testing.T, f Frame) Painted {
		t.Helper()
		_, now, _ := Damage(Painted{}, f, opt)
		if _, _, full := Damage(now, f, opt); full {
			t.Fatal("an unchanged frame after a full repaint asked for another one")
		}
		return now
	}

	t.Run("a resize", func(t *testing.T) {
		f, _ := frameFor(t)
		prev := settle(t, f)
		f.Grid = screen.NewGrid(f.Grid.Rows+1, f.Grid.Cols)
		if _, _, full := Damage(prev, f, opt); !full {
			t.Error("a grid of another size was diffed against the old one")
		}
	})

	t.Run("a recolour", func(t *testing.T) {
		f, tbl := frameFor(t)
		prev := settle(t, f)
		tbl.Set("Search", screen.Highlight{FG: screen.RGB{G: 0xff}})
		if _, _, full := Damage(prev, f, opt); !full {
			t.Error(":hi on a group already on screen did not force a full repaint")
		}
	})

	t.Run("a face at another scale", func(t *testing.T) {
		f, _ := frameFor(t)
		prev := settle(t, f)
		if _, _, full := Damage(prev, f, PaintOptions{Face: testFace(t, 2)}); !full {
			t.Error("moving to a display at another scale did not force a full repaint")
		}
	})

	t.Run("the live grid handed back as the snapshot", func(t *testing.T) {
		f, _ := frameFor(t)
		prev := settle(t, f)
		prev.Grid = f.Grid
		_, now, full := Damage(prev, f, opt)
		if !full {
			t.Error("a caller sharing the editor's grid got an empty diff instead of a repaint")
		}
		if now.Grid == f.Grid {
			t.Fatal("the snapshot handed back is still the editor's own grid")
		}
		if _, _, full := Damage(now, f, opt); full {
			t.Error("the frame after that one did not recover")
		}
	})
}

// TestDamageOfOneEditIsOneSpan is the point of the whole file: an edit that
// touched four cells must not ask for 200x60 of them.
func TestDamageOfOneEditIsOneSpan(t *testing.T) {
	f, _ := frameFor(t)
	f.Cursor = CursorNone
	opt := PaintOptions{Face: testFace(t, 1)}

	_, prev, _ := Damage(Painted{}, f, opt)
	f.Grid.SetString(2, 3, "xy", screen.Normal)

	spans, _, full := Damage(prev, f, opt)
	if full {
		t.Fatal("an edit to two cells reported a full repaint")
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1: %v", len(spans), spans)
	}
	if want := (screen.Span{Row: 2, Col: 3, Len: 2}); spans[0] != want {
		t.Errorf("span %v, want %v", spans[0], want)
	}
}

// TestDamageCoversBothCursorCells: the caret is drawn by PaintRects only where
// a span covers it and erased only where a span covers where it was, so a
// cursor that moved over cells nothing else touched still needs both.
func TestDamageCoversBothCursorCells(t *testing.T) {
	f, _ := frameFor(t)
	f.Cursor = CursorBlock
	opt := PaintOptions{Face: testFace(t, 1)}

	_, prev, _ := Damage(Painted{}, f, opt)
	f.CursorRow, f.CursorCol = 3, 7

	spans, _, full := Damage(prev, f, opt)
	if full {
		t.Fatal("a cursor move reported a full repaint")
	}
	assertCovers(t, spans, f.Grid, [][2]int{{prev.CursorRow, prev.CursorCol}, {3, 7}})
	if n := cellsIn(spans); n != 2 {
		t.Errorf("a cursor move repainted %d cells, want the two it was on", n)
	}
}

// TestDamageWithNoCaretAsksForNothing. CursorNone means no caret was drawn and
// none is wanted, so a frame in which nothing at all moved is free.
func TestDamageWithNoCaretAsksForNothing(t *testing.T) {
	f, _ := frameFor(t)
	f.Cursor = CursorNone
	opt := PaintOptions{Face: testFace(t, 1)}

	_, prev, _ := Damage(Painted{}, f, opt)
	spans, _, full := Damage(prev, f, opt)
	if full || len(spans) != 0 {
		t.Errorf("an unchanged frame asked for %d spans, full=%v", len(spans), full)
	}
}

// TestDamageReusesTheSnapshot: a frontend keeps one snapshot grid per buffer
// and Damage copies into it, because a grid allocated per frame is 200x60
// cells of garbage sixty times a second.
func TestDamageReusesTheSnapshot(t *testing.T) {
	f, _ := frameFor(t)
	opt := PaintOptions{Face: testFace(t, 1)}

	_, first, _ := Damage(Painted{}, f, opt)
	if first.Grid == f.Grid {
		t.Fatal("the snapshot is the editor's own grid; the next frame would diff against itself")
	}
	f.Grid.SetString(0, 0, "OMEGA", screen.Normal)
	_, second, _ := Damage(first, f, opt)
	if second.Grid != first.Grid {
		t.Error("the second frame allocated a new snapshot grid instead of copying into the old one")
	}
	if !bytes.Equal(cellBytes(second.Grid), cellBytes(f.Grid)) {
		t.Error("the snapshot does not hold what was just painted")
	}
}

// TestDamageIncrementalEqualsFull is the safety of the whole optimisation, and
// it is the reason this file is allowed to exist: a buffer driven through a
// session of Damage plus PaintRects has to hold, at every step, the bytes a
// full Paint of that frame would have written. Anything Damage forgets to
// return shows up here as a stale pixel.
func TestDamageIncrementalEqualsFull(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 1)}
	f := fixture()
	f.Cursor = CursorBlock

	incremental := NewImage(f, opt)
	whole := NewImage(f, opt)
	var prev Painted

	steps := []struct {
		name string
		do   func()
	}{
		{"the first frame", func() {}},
		{"nothing at all", func() {}},
		{"a cursor move", func() { f.CursorRow, f.CursorCol = 5, 9 }},
		{"typing a character", func() { f.Grid.Set(5, 9, 'Z', screen.Normal); f.CursorCol = 10 }},
		{"a whole line rewritten", func() { f.Grid.SetString(2, 0, "A DIFFERENT HEADING", screen.Normal) }},
		{"a wide pair overwritten by one narrow rune", func() { f.Grid.Set(8, 5, 'x', screen.Normal) }},
		{"a narrow rune overwritten by a wide pair", func() { f.Grid.Set(0, 3, '界', screen.Normal) }},
		{"the caret onto the tail of a pair", func() { f.CursorRow, f.CursorCol = 0, 4 }},
		{"the caret off the tail again", func() { f.CursorRow, f.CursorCol = 0, 0 }},
		{"a scroll: every row shifted up one", func() {
			g := f.Grid
			copy(g.Cells, g.Cells[g.Cols:])
			for col := 0; col < g.Cols; col++ {
				g.Set(g.Rows-1, col, ' ', screen.Normal)
			}
		}},
		{"the caret onto the last row", func() { f.CursorRow, f.CursorCol = f.Grid.Rows-1, 0 }},
		{"a mode change to a bar caret", func() { f.Cursor = CursorBar }},
		{"a mode change back", func() { f.Cursor = CursorBlock }},
	}

	for _, step := range steps {
		step.do()
		spans, now, _ := Damage(prev, f, opt)
		if err := PaintRects(incremental, f, opt, spans); err != nil {
			t.Fatalf("%s: PaintRects: %v", step.name, err)
		}
		prev = now
		if err := Paint(whole, f, opt); err != nil {
			t.Fatalf("%s: Paint: %v", step.name, err)
		}
		if !bytes.Equal(incremental.Pix, whole.Pix) {
			t.Fatalf("after %q the incrementally painted buffer is not what a full paint gives:\n%s",
				step.name, diffSummary(incremental, whole))
		}
	}
}

// TestAddCellMerges pins the span bookkeeping on its own, because the case it
// gets wrong is invisible in a pixel comparison: painting a cell twice is
// harmless and only wastes the work this file exists to save.
func TestAddCellMerges(t *testing.T) {
	g := screen.NewGrid(2, 10)
	for _, tc := range []struct {
		name string
		in   []screen.Span
		row  int
		col  int
		want []screen.Span
	}{
		{"into an empty list", nil, 0, 3, []screen.Span{{Row: 0, Col: 3, Len: 1}}},
		{"already covered", []screen.Span{{Row: 0, Col: 2, Len: 4}}, 0, 3, []screen.Span{{Row: 0, Col: 2, Len: 4}}},
		{"onto the end", []screen.Span{{Row: 0, Col: 2, Len: 2}}, 0, 4, []screen.Span{{Row: 0, Col: 2, Len: 3}}},
		{"onto the start", []screen.Span{{Row: 0, Col: 2, Len: 2}}, 0, 1, []screen.Span{{Row: 0, Col: 1, Len: 3}}},
		{"between two, joining both", []screen.Span{{Row: 0, Col: 0, Len: 2}, {Row: 0, Col: 3, Len: 2}}, 0, 2,
			[]screen.Span{{Row: 0, Col: 0, Len: 5}}},
		{"in the gap, touching neither", []screen.Span{{Row: 0, Col: 0, Len: 1}, {Row: 0, Col: 8, Len: 2}}, 0, 4,
			[]screen.Span{{Row: 0, Col: 0, Len: 1}, {Row: 0, Col: 4, Len: 1}, {Row: 0, Col: 8, Len: 2}}},
		{"on the row after", []screen.Span{{Row: 0, Col: 0, Len: 2}}, 1, 0,
			[]screen.Span{{Row: 0, Col: 0, Len: 2}, {Row: 1, Col: 0, Len: 1}}},
		{"on the row before", []screen.Span{{Row: 1, Col: 0, Len: 2}}, 0, 5,
			[]screen.Span{{Row: 0, Col: 5, Len: 1}, {Row: 1, Col: 0, Len: 2}}},
		{"off the grid", []screen.Span{{Row: 0, Col: 0, Len: 2}}, 9, 9, []screen.Span{{Row: 0, Col: 0, Len: 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := addCell(append([]screen.Span(nil), tc.in...), g, tc.row, tc.col)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// assertCovers fails unless every named cell is inside some span.
func assertCovers(t *testing.T, spans []screen.Span, g *screen.Grid, cells [][2]int) {
	t.Helper()
	painted := map[[2]int]bool{}
	for _, sp := range spans {
		for c := sp.Col; c < sp.Col+sp.Len; c++ {
			painted[[2]int{sp.Row, c}] = true
		}
	}
	for _, cell := range cells {
		if !painted[cell] {
			t.Fatalf("cell %d,%d is in no span: %v", cell[0], cell[1], spans)
		}
	}
}

func allCells(g *screen.Grid) [][2]int {
	var out [][2]int
	for row := 0; row < g.Rows; row++ {
		for col := 0; col < g.Cols; col++ {
			out = append(out, [2]int{row, col})
		}
	}
	return out
}

func cellsIn(spans []screen.Span) int {
	n := 0
	for _, sp := range spans {
		n += sp.Len
	}
	return n
}

// cellBytes is a comparable image of a grid's contents, for a test that wants
// to say "these two hold the same cells" without a loop.
func cellBytes(g *screen.Grid) []byte {
	b := make([]byte, 0, len(g.Cells)*8)
	for _, c := range g.Cells {
		b = append(b, byte(c.Rune), byte(c.Rune>>8), byte(c.Rune>>16), byte(c.Rune>>24),
			byte(c.HL), byte(c.HL>>8))
		if c.Tail {
			b = append(b, 1)
		} else {
			b = append(b, 0)
		}
	}
	return b
}
