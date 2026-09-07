//go:build darwin

package gui

import (
	"bytes"
	"image"
	"testing"

	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// The dirty-rect blit, tested without a window.
//
// window.paint is Draw minus the two lines that talk to the main thread, so
// everything here runs against a window struct with no run loop, no layer and
// no AppKit at all. What is being tested is the bookkeeping -- which buffer
// holds what, and when that is not enough -- because that is where a partial
// repaint goes wrong, and it goes wrong by leaving a stale rectangle on screen
// that no later frame touches.

// testWindow returns a window with a face and nothing else: no queue, no
// layer, no main queue.
func testWindow(t *testing.T) *window {
	t.Helper()
	face, err := raster.DefaultFace(1)
	if err != nil {
		t.Skipf("no Monaco: %v", err)
	}
	return &window{face: face}
}

// paintScreen is a Screen of the given size with a recognisable pattern in it.
func paintScreen(rows, cols int) *screen.Screen {
	s := screen.NewScreen(rows, cols)
	for r := 0; r < rows; r++ {
		s.Grid.SetString(r, 0, "abcdefghij"[:min(cols, 10)], screen.Normal)
	}
	return s
}

// TestFirstFrameIsFull. Every buffer in a fresh pool holds nothing, so there
// is nothing to diff against and the answer has to be the whole grid.
func TestFirstFrameIsFull(t *testing.T) {
	w := testWindow(t)
	s := paintScreen(8, 20)
	if _, err := w.paint(s); err != nil {
		t.Fatalf("paint: %v", err)
	}
	if w.stats.full != 1 || w.stats.partial != 0 {
		t.Errorf("first frame: %d full and %d partial, want 1 and 0", w.stats.full, w.stats.partial)
	}
}

// TestASecondFrameIntoTheSameBufferIsPartial is the fast path existing at all.
//
// The pool is three deep and paint rotates through it, so it takes four frames
// for one buffer to come round again: the first three are full because each
// buffer is seeing the grid for the first time, and the fourth is the first
// one that can diff.
func TestASecondFrameIntoTheSameBufferIsPartial(t *testing.T) {
	w := testWindow(t)
	s := paintScreen(8, 20)
	for i := 0; i < len(w.bufs)+1; i++ {
		if _, err := w.paint(s); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if w.stats.partial == 0 {
		t.Errorf("%d frames of an unchanging screen were all full repaints; the dirty-rect path is not being taken",
			w.stats.full)
	}
}

// TestRecolourForcesAFullRepaint is the trap by name, and it is
// the reason raster.Stamp exists.
//
// A `:hi Normal guibg=...` or a `:colorscheme` reload changes what every cell
// on screen looks like and changes not one cell of the grid. A frontend that
// trusted the cell diff would paint nothing and leave the window in the old
// colours until something happened to touch each cell, which reproduces about
// once a month and looks like a graphics driver problem.
func TestRecolourForcesAFullRepaint(t *testing.T) {
	w := testWindow(t)
	s := paintScreen(8, 20)

	// Enough frames that every buffer holds this grid and the fast path is
	// running.
	for i := 0; i < 2*len(w.bufs); i++ {
		if _, err := w.paint(s); err != nil {
			t.Fatalf("warm-up frame %d: %v", i, err)
		}
	}
	if w.stats.partial == 0 {
		t.Fatal("the fast path never engaged, so this test cannot show it being overridden")
	}
	fullBefore := w.stats.full

	// The recolour: same grid, same cells, different Normal.
	s.HL.Set(screen.NormalName, screen.Highlight{
		FG: screen.RGB{R: 0xff, G: 0x00, B: 0x00},
		BG: screen.RGB{R: 0x00, G: 0x00, B: 0x40},
	})
	for i := 0; i < len(w.bufs); i++ {
		if _, err := w.paint(s); err != nil {
			t.Fatalf("recoloured frame %d: %v", i, err)
		}
	}
	if got := w.stats.full - fullBefore; got != len(w.bufs) {
		t.Errorf("after a recolour %d of the next %d frames were full repaints, want all of them: "+
			"the buffers still hold the old colours", got, len(w.bufs))
	}
}

// TestPartialPaintingMatchesAFullOne is the property that makes any of this
// safe: after a run of partial frames, what a buffer holds is what a full
// repaint of the same screen would have put there.
//
// It compares pixels. A stale rectangle -- a span the damage missed, a cursor
// cell not covered, a wide-rune pair half repainted -- is a byte difference
// here and is invisible in any test that only counts spans.
func TestPartialPaintingMatchesAFullOne(t *testing.T) {
	w := testWindow(t)
	rows, cols := 8, 20
	s := paintScreen(rows, cols)

	// A run of frames that change a little each time, the way typing does:
	// enough of them that every buffer has been through the fast path.
	for i := 0; i < 12; i++ {
		s.Grid.Set(i%rows, i%cols, rune('A'+i), screen.Normal)
		s.CursorRow, s.CursorCol = i%rows, (i+3)%cols
		s.CursorShape = screen.CursorBlock
		if _, err := w.paint(s); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if w.stats.partial == 0 {
		t.Fatal("no frame took the fast path, so this proves nothing")
	}

	got := w.front.img
	if got == nil {
		t.Fatal("no frame was published")
	}

	opt := raster.PaintOptions{Face: w.face}
	frame := raster.Frame{
		Grid:      &s.Grid,
		HL:        s.HL,
		CursorRow: s.CursorRow,
		CursorCol: s.CursorCol,
		Cursor:    raster.CursorBlock,
	}
	want := image.NewRGBA(got.Bounds())
	if err := raster.Paint(want, frame, opt); err != nil {
		t.Fatalf("reference paint: %v", err)
	}

	pw, ph := raster.Size(frame, opt)
	for y := 0; y < ph; y++ {
		a := got.Pix[y*got.Stride : y*got.Stride+pw*4]
		b := want.Pix[y*want.Stride : y*want.Stride+pw*4]
		if !bytes.Equal(a, b) {
			t.Fatalf("row %d of the partially painted buffer differs from a full repaint of the same screen", y)
		}
	}
}

// TestResizeIsAFullRepaint. A grid of a different shape shares no cell with
// the one a buffer holds, and the buffer is reallocated under it as well when
// the window grew past a step.
func TestResizeIsAFullRepaint(t *testing.T) {
	w := testWindow(t)
	small := paintScreen(8, 20)
	for i := 0; i < 2*len(w.bufs); i++ {
		if _, err := w.paint(small); err != nil {
			t.Fatalf("warm-up: %v", err)
		}
	}
	fullBefore := w.stats.full

	big := paintScreen(20, 60)
	for i := 0; i < len(w.bufs); i++ {
		if _, err := w.paint(big); err != nil {
			t.Fatalf("resized frame %d: %v", i, err)
		}
	}
	if got := w.stats.full - fullBefore; got != len(w.bufs) {
		t.Errorf("after a resize %d of the next %d frames were full, want all of them", got, len(w.bufs))
	}
}

// TestPaintedTravelsWithItsBuffer. The Painted a frame records has to be
// thrown away when the pool is reallocated, or the next frame diffs against a
// grid describing pixels that no longer exist.
func TestPaintedTravelsWithItsBuffer(t *testing.T) {
	w := testWindow(t)
	s := paintScreen(8, 20)
	for i := 0; i < len(w.bufs); i++ {
		if _, err := w.paint(s); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	for i, p := range w.painted {
		if p.Grid == nil || p.Stamp == 0 {
			t.Errorf("buffer %d holds a frame and its Painted is empty", i)
		}
	}

	// Force a grow: every buffer is new and holds nothing.
	w.buffer(w.bufW+1, w.bufH)
	for i, p := range w.painted {
		if p.Grid != nil || p.Stamp != 0 {
			t.Errorf("buffer %d was reallocated and still claims to hold %v", i, p)
		}
	}
}

// TestPaintWithNoFaceFails. Draw before the font is loaded is a startup
// ordering bug, and an error naming it beats a nil dereference three frames
// later.
func TestPaintWithNoFaceFails(t *testing.T) {
	w := &window{}
	if _, err := w.paint(paintScreen(4, 4)); err == nil {
		t.Error("paint with no face returned no error")
	}
}

// BenchmarkKeystrokeFrame is what a keystroke costs now that the frontend
// paints only what changed, against BenchmarkFrame in blit_darwin_test.go,
// which is the full 200x60 repaint measured at 3.87ms.
//
// One cell changes and the caret moves one column, which is what typing a
// character into the middle of a line does to a Grid.
func BenchmarkKeystrokeFrame(b *testing.B) {
	const scale = 2
	face, err := raster.DefaultFace(scale)
	if err != nil {
		b.Skipf("no font: %v", err)
	}
	w := &window{face: face}
	s := screen.NewScreen(60, 200)
	for row := 0; row < s.Grid.Rows; row++ {
		for col := 0; col < s.Grid.Cols; col++ {
			s.Grid.Set(row, col, 'a', screen.Normal)
		}
	}
	// Warm the pool so that every buffer holds a frame and the fast path is
	// what is being measured.
	for i := 0; i < 2*len(w.bufs); i++ {
		if _, err := w.paint(s); err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Grid.Set(30, i%100, rune('a'+i%26), screen.Normal)
		s.CursorRow, s.CursorCol = 30, i%100
		if _, err := w.paint(s); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if w.stats.partial == 0 {
		b.Fatal("every frame was a full repaint; this measured the slow path")
	}
}
