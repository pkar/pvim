package raster

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/screen"
)

// TestPaintIsDeterministic is the claim the goldens rest on, checked directly
// rather than only through a hash in a file: two paints of the same frame into
// two buffers produce the same bytes, including the second time round when
// every glyph comes out of the cache instead of the rasteriser.
func TestPaintIsDeterministic(t *testing.T) {
	f := fixture()
	opt := PaintOptions{Face: testFace(t, 2)}

	first := NewImage(f, opt)
	if err := Paint(first, f, opt); err != nil {
		t.Fatal(err)
	}
	second := NewImage(f, opt)
	if err := Paint(second, f, opt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Pix, second.Pix) {
		t.Fatal("two paints of the same frame with one face differ")
	}

	// A fresh face, so nothing is cached and every glyph is rasterised again.
	third := NewImage(f, PaintOptions{Face: testFace(t, 2)})
	if err := Paint(third, f, PaintOptions{Face: testFace(t, 2)}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Pix, third.Pix) {
		t.Fatal("a paint with a cold glyph cache differs from one with a warm cache")
	}
}

// TestPaintRectsMatchesPaint is what makes the dirty-rect path safe: repainting
// only the spans that changed has to land on the same pixels as repainting
// everything, or a scrolled screen slowly drifts away from a redrawn one.
func TestPaintRectsMatchesPaint(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 1)}

	before := fixture()
	prev := screen.NewGrid(before.Grid.Rows, before.Grid.Cols)
	copy(prev.Cells, before.Grid.Cells)

	after := fixture()
	after.Grid.SetString(3, 4, "EDITED", screen.Normal)
	after.Grid.SetString(8, 5, "漢字", screen.Normal)
	after.CursorCol = 6

	// Start from a fully painted "before" frame, then apply only the diff.
	incremental := NewImage(after, opt)
	if err := Paint(incremental, before, opt); err != nil {
		t.Fatal(err)
	}
	spans := after.Grid.Diff(prev)
	if len(spans) == 0 {
		t.Fatal("the edit produced no spans; the test is not testing anything")
	}
	if err := PaintRects(incremental, after, opt, spans); err != nil {
		t.Fatal(err)
	}

	whole := NewImage(after, opt)
	if err := Paint(whole, after, opt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(incremental.Pix, whole.Pix) {
		t.Errorf("an incremental repaint of %d spans differs from a full one:\n%s",
			len(spans), diffSummary(incremental, whole))
	}
}

// TestPaintRectsWidensWidePairs checks the awkward half of the previous test on
// its own: a span that lands on the trailing cell of a double-width pair has to
// pull in the leading cell, because half a glyph cannot be drawn.
func TestPaintRectsWidensWidePairs(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 1)}
	g := screen.NewGrid(1, 8)
	g.SetString(0, 0, "a世b", screen.Normal)
	f := Frame{Grid: g, Cursor: CursorNone}

	whole := NewImage(f, opt)
	if err := Paint(whole, f, opt); err != nil {
		t.Fatal(err)
	}

	// Repaint starting at column 2, the trailing half of the wide rune, over a
	// buffer scribbled full of a colour nothing in the frame uses.
	partial := NewImage(f, opt)
	for i := range partial.Pix {
		partial.Pix[i] = 0x7f
	}
	if err := PaintRects(partial, f, opt, []screen.Span{{Row: 0, Col: 2, Len: 6}}); err != nil {
		t.Fatal(err)
	}

	m := opt.Face.Metrics()
	// Columns 1 and 2 are the pair. Both must have been drawn.
	for x := m.CellW; x < 3*m.CellW; x++ {
		for y := 0; y < m.CellH; y++ {
			if partial.RGBAAt(x, y) != whole.RGBAAt(x, y) {
				t.Fatalf("pixel (%d,%d) in the wide pair was not repainted: got %v, want %v",
					x, y, partial.RGBAAt(x, y), whole.RGBAAt(x, y))
			}
		}
	}
}

// TestMissingGlyphIsTheNotdefBox holds the accepted answer to emoji:
// there is no fallback font, so a rune Monaco has never heard of draws the
// font's own .notdef box. Blank would be a bug; a box is the feature.
func TestMissingGlyphIsTheNotdefBox(t *testing.T) {
	face := testFace(t, 1)
	notdef, _ := face.Glyph('\U0001F600', false, false)
	if notdef.Rect.Empty() {
		t.Fatal("an emoji rendered as an empty mask; it must render .notdef")
	}
	ink := 0
	for _, a := range notdef.Pix {
		if a > 0 {
			ink++
		}
	}
	if ink == 0 {
		t.Fatal("the .notdef mask has no coverage in it")
	}
	// Every rune the font is missing draws the same box, which is what makes it
	// recognisable as "no glyph" rather than as a character.
	other, _ := face.Glyph('世', false, false)
	if !bytes.Equal(notdef.Pix, other.Pix) {
		t.Error("two missing runes drew different masks; both should be .notdef")
	}
}

// TestSyntheticStylesDiffer checks that bold and italic actually do something.
// Monaco has one weight on disk, so a bug here is silent: the text renders, it
// is just never bold.
func TestSyntheticStylesDiffer(t *testing.T) {
	face := testFace(t, 2)
	plain, _ := face.Glyph('H', false, false)
	bold, _ := face.Glyph('H', true, false)
	italic, _ := face.Glyph('H', false, true)

	if bold.Rect.Dx() <= plain.Rect.Dx() {
		t.Errorf("bold 'H' is %d px wide, plain is %d; the smear did nothing",
			bold.Rect.Dx(), plain.Rect.Dx())
	}
	if italic.Rect.Dx() <= plain.Rect.Dx() {
		t.Errorf("italic 'H' is %d px wide, plain is %d; the shear did nothing",
			italic.Rect.Dx(), plain.Rect.Dx())
	}
	if bytes.Equal(bold.Pix, italic.Pix) {
		t.Error("bold and italic produced the same mask")
	}
}

// TestGlyphCacheReturnsTheSameMask is the cheap check that the cache is a cache:
// a second lookup must not re-rasterise, and the mask handed out must be the
// same object so the caller is not allocating a copy per cell per frame.
func TestGlyphCacheReturnsTheSameMask(t *testing.T) {
	face := testFace(t, 1)
	a, offA := face.Glyph('x', false, false)
	b, offB := face.Glyph('x', false, false)
	if a != b || offA != offB {
		t.Error("two lookups of the same glyph returned different masks")
	}
}

// TestLinespaceGrowsTheRow checks 'linespace' does the one thing it is for.
func TestLinespaceGrowsTheRow(t *testing.T) {
	face := testFace(t, 1)
	g := screen.NewGrid(3, 4)
	f := Frame{Grid: g, Cursor: CursorNone}

	tight := PaintOptions{Face: face}
	loose := PaintOptions{Face: face, Linespace: 4}
	_, h0 := Size(f, tight)
	_, h1 := Size(f, loose)
	if h1-h0 != 3*4 {
		t.Errorf("4 px of linespace over 3 rows grew the buffer by %d px, want 12", h1-h0)
	}
	if err := Paint(NewImage(f, loose), f, loose); err != nil {
		t.Errorf("painting with linespace: %v", err)
	}
}

// TestPaintRejectsAShortBuffer: a buffer one pixel too small is a resize race
// in the frontend, and a clipped frame that looks nearly right is worse than an
// error that names both sizes.
func TestPaintRejectsAShortBuffer(t *testing.T) {
	f := fixture()
	opt := PaintOptions{Face: testFace(t, 1)}
	w, h := Size(f, opt)
	small := image.NewRGBA(image.Rect(0, 0, w, h-1))
	if err := Paint(small, f, opt); err == nil {
		t.Error("Paint accepted a buffer one pixel short")
	}
	if err := Paint(NewImage(f, opt), Frame{}, opt); err == nil {
		t.Error("Paint accepted a frame with no grid")
	}
	if err := Paint(NewImage(f, opt), f, PaintOptions{}); err == nil {
		t.Error("Paint accepted options with no face")
	}
}

// TestPaintHonoursAnOffsetImage checks the painter uses PixOffset and not raw
// indices: a sub-image of a bigger buffer is how a frontend paints into one
// window of a shared bitmap, and getting it wrong shifts the whole frame.
func TestPaintHonoursAnOffsetImage(t *testing.T) {
	f := fixture()
	opt := PaintOptions{Face: testFace(t, 1)}
	w, h := Size(f, opt)

	flat := NewImage(f, opt)
	if err := Paint(flat, f, opt); err != nil {
		t.Fatal(err)
	}

	big := image.NewRGBA(image.Rect(0, 0, w+11, h+7))
	sub := big.SubImage(image.Rect(5, 3, 5+w, 3+h)).(*image.RGBA)
	if err := Paint(sub, f, opt); err != nil {
		t.Fatal(err)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if got, want := big.RGBAAt(x+5, y+3), flat.RGBAAt(x, y); got != want {
				t.Fatalf("offset paint differs at (%d,%d): got %v, want %v", x, y, got, want)
			}
		}
	}
}

// TestCursorShapesPaintDifferently is a sanity net under the goldens: five
// hashes that all happened to be equal would still pass a golden test.
func TestCursorShapesPaintDifferently(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 1)}
	seen := map[string]CursorShape{}
	for _, shape := range []CursorShape{CursorNone, CursorBlock, CursorBar, CursorUnderline, CursorHollow} {
		f := fixture()
		f.Cursor = shape
		dst := NewImage(f, opt)
		if err := Paint(dst, f, opt); err != nil {
			t.Fatal(err)
		}
		k := string(dst.Pix)
		if prev, ok := seen[k]; ok {
			t.Errorf("cursor shapes %d and %d render identically", prev, shape)
		}
		seen[k] = shape
	}
}

// TestCursorOnAWidePairCoversBoth: parking the caret on either half of a
// double-width rune has to light up the whole rune, not half of it.
func TestCursorOnAWidePairCoversBoth(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 1)}
	g := screen.NewGrid(1, 6)
	g.SetString(0, 0, "a世b", screen.Normal)

	lead := Frame{Grid: g, CursorRow: 0, CursorCol: 1, Cursor: CursorBlock}
	tail := lead
	tail.CursorCol = 2

	a, b := NewImage(lead, opt), NewImage(tail, opt)
	if err := Paint(a, lead, opt); err != nil {
		t.Fatal(err)
	}
	if err := Paint(b, tail, opt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Error("a block cursor on the two halves of one wide rune drew two different frames")
	}
}

// diffSummary says where two frames first disagree and how badly, because
// "pixels differ" on a 320x180 image is not something a person can act on.
func diffSummary(got, want *image.RGBA) string {
	b := got.Bounds()
	n := 0
	first := ""
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if got.RGBAAt(x, y) != want.RGBAAt(x, y) {
				n++
				if first == "" {
					first = "first at (" + itoa(x) + "," + itoa(y) + ")"
				}
			}
		}
	}
	return itoa(n) + " pixels differ, " + first
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestPaintRectsCellByCellMatchesPaint is the equality the whole dirty-rect
// path rests on, taken to its extreme: every cell of the frame painted as its
// own one-cell span, in an order chosen to be awkward, over a buffer full of a
// colour nothing in the frame uses, has to land on the bytes a single full
// Paint writes. Anything in the painter that depends on what was drawn beside
// it -- a glyph bleeding out of its advance, a decoration drawn across a whole
// span, a caret that a later span erases -- shows up here and nowhere else.
func TestPaintRectsCellByCellMatchesPaint(t *testing.T) {
	f := fixture()
	f.Cursor = CursorBlock
	f.CursorRow, f.CursorCol = 8, 5 // on the leading half of a wide pair
	opt := PaintOptions{Face: testFace(t, 1)}

	whole := NewImage(f, opt)
	if err := Paint(whole, f, opt); err != nil {
		t.Fatal(err)
	}

	// Both directions, because the order decides which span paints a cell last
	// and a caret is drawn by whichever span covered it most recently.
	for _, dir := range []struct {
		name string
		back bool
	}{{"left to right", false}, {"right to left", true}} {
		t.Run(dir.name, func(t *testing.T) {
			piecemeal := NewImage(f, opt)
			for i := range piecemeal.Pix {
				piecemeal.Pix[i] = 0x7f
			}
			for row := 0; row < f.Grid.Rows; row++ {
				for i := 0; i < f.Grid.Cols; i++ {
					col := i
					if dir.back {
						col = f.Grid.Cols - 1 - i
					}
					if err := PaintRects(piecemeal, f, opt, []screen.Span{{Row: row, Col: col, Len: 1}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if !bytes.Equal(piecemeal.Pix, whole.Pix) {
				t.Errorf("painting one cell at a time differs from one full paint:\n%s",
					diffSummary(piecemeal, whole))
			}
		})
	}
}

// TestPaintRectsKeepsACaretItRepaintedOver is the case cell-by-cell painting
// found: a span reaches the caret's cell by being widened onto the leading half
// of a wide pair, repaints it, and has to draw the caret back. Without that the
// caret blinks out whenever the cell beside it changes.
func TestPaintRectsKeepsACaretItRepaintedOver(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 1)}
	g := screen.NewGrid(1, 6)
	g.SetString(0, 0, "a世b", screen.Normal)
	f := Frame{Grid: g, CursorRow: 0, CursorCol: 1, Cursor: CursorBlock}

	whole := NewImage(f, opt)
	if err := Paint(whole, f, opt); err != nil {
		t.Fatal(err)
	}

	// Column 2 is the trailing half of the pair the caret sits on, so the span
	// widens back onto column 1 and paints over the caret.
	partial := NewImage(f, opt)
	if err := Paint(partial, f, opt); err != nil {
		t.Fatal(err)
	}
	if err := PaintRects(partial, f, opt, []screen.Span{{Row: 0, Col: 2, Len: 1}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(partial.Pix, whole.Pix) {
		t.Errorf("repainting the tail of the pair under the caret lost the caret:\n%s",
			diffSummary(partial, whole))
	}
}

// TestEachAttributeChangesPixels names the attributes one at a time. The
// goldens cover them all in one hash, which says "something moved" and not
// which; this says which, so an attribute that quietly stops being drawn fails
// with its own name on it.
func TestEachAttributeChangesPixels(t *testing.T) {
	opt := PaintOptions{Face: testFace(t, 2)}
	plain := screen.Highlight{
		FG: screen.RGB{R: 0xbb, G: 0xbb, B: 0xbb},
		BG: screen.RGB{R: 0x26, G: 0x26, B: 0x26},
		SP: screen.RGB{R: 0xd7},
	}

	render := func(hl screen.Highlight) []byte {
		tab := screen.NewTable()
		id := tab.Set("Test", hl)
		g := screen.NewGrid(1, 8)
		g.SetString(0, 0, "Hxy", id)
		f := Frame{Grid: g, HL: tab, Cursor: CursorNone}
		dst := NewImage(f, opt)
		if err := Paint(dst, f, opt); err != nil {
			t.Fatal(err)
		}
		return dst.Pix
	}

	base := render(plain)
	seen := map[string]string{string(base): "plain"}
	for _, tc := range []struct {
		name string
		attr screen.Attr
	}{
		{"bold", screen.AttrBold},
		{"italic", screen.AttrItalic},
		{"underline", screen.AttrUnderline},
		{"undercurl", screen.AttrUndercurl},
		{"strikethrough", screen.AttrStrikethrough},
		{"reverse", screen.AttrReverse},
	} {
		hl := plain
		hl.Attr = tc.attr
		got := render(hl)
		if prev, ok := seen[string(got)]; ok {
			t.Errorf("%s renders exactly like %s; it is not being drawn", tc.name, prev)
		}
		seen[string(got)] = tc.name
	}
}

// measurementRow is one line of the table: what was measured and the benchmark
// that measures it.
type measurementRow struct {
	what  string
	bench string
}

// measurementRows pulls the table out of the Measurements section. It is a
// deliberately dumb parser -- pipes, two header lines, backticks stripped --
// because the alternative is a markdown dependency for one table.
func measurementRows(t *testing.T, doc string) []measurementRow {
	t.Helper()
	_, after, ok := strings.Cut(doc, "\n## Measurements\n")
	if !ok {
		return nil
	}
	if before, _, ok := strings.Cut(after, "\n## "); ok {
		after = before
	}

	var rows []measurementRow
	for _, line := range strings.Split(after, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) < 2 || cells[0] == "what" || strings.HasPrefix(cells[0], "---") {
			continue // the header and the rule under it
		}
		rows = append(rows, measurementRow{what: cells[0], bench: strings.Trim(cells[1], "`")})
	}
	return rows
}

// benchmarkNames is every benchmark entry point in the package's test files.
func benchmarkNames(t *testing.T) map[string]bool {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^func (Benchmark\w+)\(`)
	names := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
			names[m[1]] = true
		}
	}
	return names
}

// BenchmarkFullRepaint is the repaint budget: a full 200x60 repaint at retina
// scale, which has to come in under 4 ms.
func BenchmarkFullRepaint(b *testing.B) {
	f, opt, dst := benchFrame(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Paint(dst, f, opt); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEmptyRepaint is the same screen with nothing written on it: the same
// fills, the same cursor, and no glyphs, because a cell nobody wrote holds the
// zero rune and drawRune leaves it alone. Subtracting it from
// BenchmarkFullRepaint is where the "82% of a full repaint is glyph
// compositing" figure comes from, and it exists so that
// number can be reproduced rather than taken on trust.
func BenchmarkEmptyRepaint(b *testing.B) {
	f, opt, dst := benchScreen(b, false)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := Paint(dst, f, opt); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRowRepaint is one row of that screen, which is what a status line
// changing or a single line being edited costs.
func BenchmarkRowRepaint(b *testing.B) {
	f, opt, dst := benchFrame(b)
	spans := []screen.Span{{Row: 30, Col: 0, Len: f.Grid.Cols}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := PaintRects(dst, f, opt, spans); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSingleCellRepaint is what a keystroke costs once the frontend paints
// only what changed: one cell of a 200x60 screen at retina scale. It is the
// number the whole of damage.go exists to produce, and it is measured against
// BenchmarkFullRepaint, which is the same screen repainted whole.
func BenchmarkSingleCellRepaint(b *testing.B) {
	f, opt, dst := benchFrame(b)
	spans := []screen.Span{{Row: 30, Col: 100, Len: 1}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := PaintRects(dst, f, opt, spans); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDamage is the bookkeeping around that paint: the stamp over the
// highlight table, the diff of two 200x60 grids and the snapshot copy. If this
// were the expensive half there would be no point painting less.
func BenchmarkDamage(b *testing.B) {
	f, opt, _ := benchFrame(b)
	prev := screen.NewGrid(f.Grid.Rows, f.Grid.Cols)
	copy(prev.Cells, f.Grid.Cells)
	painted := Painted{Grid: prev, Stamp: StampOf(f, opt), CursorRow: f.CursorRow, CursorCol: f.CursorCol}
	f.Grid.Set(30, 100, 'x', screen.Normal)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spans, _, _ := Damage(painted, f, opt)
		if len(spans) == 0 {
			b.Fatal("no spans; the benchmark is measuring nothing")
		}
	}
}

// benchFrame is the 200x60 screen of Go source the paint benchmarks share, with
// the glyph cache already warm, which is the state it is in for every frame
// after the first.
func benchFrame(b *testing.B) (Frame, PaintOptions, *image.RGBA) {
	b.Helper()
	return benchScreen(b, true)
}

// benchScreen is that screen with the text optional, so that the cost of the
// glyphs can be measured as the difference between two otherwise identical
// paints.
func benchScreen(b *testing.B, withText bool) (Frame, PaintOptions, *image.RGBA) {
	b.Helper()
	face, err := DefaultFace(2)
	if err != nil {
		b.Fatal(err)
	}
	g := screen.NewGrid(60, 200)
	line := "func (p *painter) cell(row, col int) { p.fill(box, bg) } // 200 cols of go"
	for row := 0; withText && row < g.Rows; row++ {
		for col := 0; col < g.Cols; col += len(line) {
			g.SetString(row, col, line, screen.Normal)
		}
	}
	f := Frame{Grid: g, HL: screen.NewTable(), CursorRow: 30, CursorCol: 100, Cursor: CursorBlock}
	opt := PaintOptions{Face: face}
	dst := NewImage(f, opt)
	if err := Paint(dst, f, opt); err != nil {
		b.Fatal(err)
	}
	return f, opt, dst
}

// TestSyntheticStylesStayInsideTheirCell measures what the cell edge cuts off a
// synthesised style, because the painter clips every glyph to its own cell and
// bold and italic are both wider than the glyph they are made from.
//
// The first assertion is the one that matters: a plain glyph loses nothing, at
// either scale, for every printable ASCII rune. Monaco's outlines sit inside
// their advance, so ordinary text is never cut, and the day a change to the
// rasteriser starts trimming it this fails before any golden does.
//
// The rest is a measurement kept as a ceiling. Clipping is what makes a span a
// pure function of the frame -- a glyph that leaned into the cell beside it
// would paint differently depending on whether that cell was repainted
// afterwards, which is exactly what TestPaintRectsCellByCellMatchesPaint
// forbids -- so the ink a smear or a shear hangs over the edge is lost by
// design and the useful question is how much. Measured at 13pt over printable
// ASCII, the answer is 1.1% of bold ink at scale 1 and 1.0% at scale 2, 2.0%
// and 2.4% of italic, and 7.4% and 8.6% of both together, whose worst rune is
// 'M' at 19% and 21%.
//
// Centring the smear and pivoting the shear halfway up the ascent, which cuts
// every one of those numbers by two thirds, was tried and rejected: with the
// smear centred a bold 'M' and the 'W' beside it merge into one blob, because
// what the cell edge was trimming was the gap between them. The measurement is
// in this package's manual checks, with the pictures it was read off.
func TestSyntheticStylesStayInsideTheirCell(t *testing.T) {
	var runes []rune
	for r := '!'; r <= '~'; r++ {
		runes = append(runes, r)
	}

	for _, scale := range []int{1, 2} {
		face := testFace(t, scale)
		m := face.Metrics()
		for _, tc := range []struct {
			name         string
			bold, italic bool
			all, worst   float64 // ceilings, in percent of the style's ink
		}{
			{"plain", false, false, 0, 0},
			{"bold", true, false, 2, 12},
			{"italic", false, true, 3, 14},
			{"bold italic", true, true, 10, 24},
		} {
			var ink, lost float64
			worst, worstRune := 0.0, ' '
			for _, r := range runes {
				mask, off := face.Glyph(r, tc.bold, tc.italic)
				i, l := clippedInk(mask, off, m.CellW, m.CellH)
				ink += i
				lost += l
				if i > 0 && l/i > worst {
					worst, worstRune = l/i, r
				}
			}
			if ink == 0 {
				t.Fatalf("scale %d %s: no ink at all; the face rasterised nothing", scale, tc.name)
			}
			got := 100 * lost / ink
			if got > tc.all {
				t.Errorf("scale %d: the cell edge cuts %.1f%% of %s ink, over the %.0f%% this was measured at",
					scale, got, tc.name, tc.all)
			}
			if 100*worst > tc.worst {
				t.Errorf("scale %d: %s %q loses %.0f%% of its ink to the cell edge, over the %.0f%% this was measured at",
					scale, tc.name, worstRune, 100*worst, tc.worst)
			}
		}
	}
}

// clippedInk totals the coverage in a glyph mask and the part of it that falls
// outside the cell the painter will clip it to. Coverage and not pixel count,
// so an antialiased edge counts for what it is worth.
func clippedInk(mask *image.Alpha, off image.Point, cellW, cellH int) (ink, lost float64) {
	if mask == nil {
		return 0, 0
	}
	for y := 0; y < mask.Rect.Dy(); y++ {
		for x := 0; x < mask.Rect.Dx(); x++ {
			a := float64(mask.Pix[y*mask.Stride+x])
			if a == 0 {
				continue
			}
			ink += a
			px, py := off.X+x, off.Y+y
			if px < 0 || px >= cellW || py < 0 || py >= cellH {
				lost += a
			}
		}
	}
	return ink, lost
}
