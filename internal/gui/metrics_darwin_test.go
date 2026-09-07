//go:build darwin

package gui

import (
	"testing"

	"github.com/pkar/pvim/internal/raster"
)

// TestGateMetrics is the gate's measurement, as a test rather than as a
// number in a commit message.
//
// Monaco at 13pt has an 8 pixel advance at scale 1 and 16 at scale 2, and
// MacVim's window at 80 columns is 640 logical points wide plus chrome. That
// is what makes a pvim window and a MacVim window sit side by side
// with their text the same size, and it is the one measurement a person cannot
// eyeball and this test can.
//
// The row height is logged rather than asserted. MacVim's line height comes
// from CoreText's metrics for the face and pvim's from x/image's, and they are
// allowed to differ by a pixel; what is not allowed is the advance differing,
// because a column of text that drifts is visible at a glance across an
// 80-column window.
func TestGateMetrics(t *testing.T) {
	for _, scale := range []int{1, 2} {
		face, err := raster.DefaultFace(scale)
		if err != nil {
			t.Skipf("no Monaco: %v", err)
		}
		m := face.Metrics()
		if want := 8 * scale; m.CellW != want {
			t.Errorf("Monaco 13pt at scale %d has a %dpx advance, want %dpx", scale, m.CellW, want)
		}
		if m.Scale != scale {
			t.Errorf("face at scale %d reports Scale %d", scale, m.Scale)
		}
		t.Logf("scale %d: cell %dx%d, ascent %d, 80 columns is %d device pixels (%d logical points)",
			scale, m.CellW, m.CellH, m.Ascent, 80*m.CellW, 80*m.CellW/scale)

		rows, cols := cellsFor(640*scale, 480*scale, m.CellW, m.CellH)
		t.Logf("scale %d: a 640x480 point window is %d rows by %d columns", scale, rows, cols)
		if cols != 80 {
			t.Errorf("a 640 point wide window is %d columns at scale %d, want 80", cols, scale)
		}
	}
}
