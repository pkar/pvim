package gui

import (
	"testing"

	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// TestRasterCursor is the four shapes across the seam.
//
// It looks like a test of a cast, and that is the point: the two enums are
// separate types owned by separate packages, they agree by coincidence
// of ordering, and a caret drawn in the wrong shape is the kind of defect
// nobody notices for a week because it only says which mode the editor is in.
func TestRasterCursor(t *testing.T) {
	cases := []struct {
		in   screen.CursorShape
		want raster.CursorShape
	}{
		{screen.CursorBlock, raster.CursorBlock},
		{screen.CursorBar, raster.CursorBar},
		{screen.CursorUnderline, raster.CursorUnderline},
		{screen.CursorHollow, raster.CursorHollow},
	}
	for _, c := range cases {
		if got := rasterCursor(c.in); got != c.want {
			t.Errorf("rasterCursor(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestRasterCursorFallsBackToABlock. A fifth shape added to internal/screen
// and not to this switch has to draw something, and a block is the shape that
// is right for every mode this editor spends its time in.
func TestRasterCursorFallsBackToABlock(t *testing.T) {
	if got := rasterCursor(screen.CursorShape(200)); got != raster.CursorBlock {
		t.Errorf("an unknown shape mapped to %v, want CursorBlock", got)
	}
}
