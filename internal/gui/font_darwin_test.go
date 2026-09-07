//go:build darwin

package gui

import (
	"errors"
	"testing"

	"github.com/pkar/pvim/internal/raster"
)

// TestSetFontBeforeRun. cmd/pvim reads the vimrc before it opens a window in
// one order and after it in another -- --oracle never opens one at all -- so
// SetFont has to be callable with no window, and what it does then is pick the
// font the first frame will be drawn in.
func TestSetFontBeforeRun(t *testing.T) {
	if current != nil {
		t.Skip("a window is up in this process, so this is not the no-window path")
	}
	prev := Font
	t.Cleanup(func() { Font = prev })

	want, err := raster.ParseGUIFont("Monaco:h15")
	if err != nil {
		t.Fatalf("parsing a guifont: %v", err)
	}
	if err := SetFont(want); err != nil {
		t.Fatalf("SetFont with no window: %v", err)
	}
	if Font != want {
		t.Errorf("after SetFont the font is %v, want %v", Font, want)
	}
}

// TestSetLinespaceWithNoWindowRefuses. Unlike the font there is nothing to
// record: 'linespace' only means anything to a window's layout, so with no
// window the honest answer is that it did not happen.
func TestSetLinespaceWithNoWindowRefuses(t *testing.T) {
	if current != nil {
		t.Skip("a window is up in this process")
	}
	if err := SetLinespace(2); !errors.Is(err, ErrNoMainThread) {
		t.Errorf("SetLinespace with no window returned %v, want ErrNoMainThread", err)
	}
}

// TestFontMetricsAreInTheStamp is the reason relayout does not force a repaint
// of its own: a face with different metrics cannot collide with the stamp of
// the one before it, so every buffer in the pool is repainted whole on the
// next frame with no code that knows a font changed.
func TestFontMetricsAreInTheStamp(t *testing.T) {
	small, err := raster.DefaultFace(1)
	if err != nil {
		t.Skipf("no Monaco: %v", err)
	}
	big, err := raster.DefaultFace(2)
	if err != nil {
		t.Skipf("no Monaco at scale 2: %v", err)
	}
	s := paintScreen(4, 8)
	frame := raster.Frame{Grid: &s.Grid, HL: s.HL}

	a := raster.StampOf(frame, raster.PaintOptions{Face: small})
	b := raster.StampOf(frame, raster.PaintOptions{Face: big})
	if a == b {
		t.Error("two faces with different metrics stamp the same, so a font change would not repaint")
	}
	c := raster.StampOf(frame, raster.PaintOptions{Face: small, Linespace: 2})
	if a == c {
		t.Error("'linespace' is not in the stamp, so changing it would not repaint")
	}
}
