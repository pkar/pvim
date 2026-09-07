//go:build darwin

package gui

import (
	"testing"

	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/raster"
)

// Resizing, through the registered setFrameSize: and out the other side as an
// event, with no window server anywhere.
//
// Monaco 13pt at scale 1 is an 8 pixel advance and an 18 pixel row, so 640x480
// points is 80 columns and 26 rows -- 26 and not 26.67, because half a row at
// the bottom of the window is what vim leaves there too.

// resizeWindow returns a window with a real view at 640x480 and the first
// ResizeEvent already delivered, which is the state a window is in by the time
// anybody drags its edge.
func resizeWindow(t *testing.T) *window {
	t.Helper()
	face, err := raster.DefaultFace(1)
	if err != nil {
		t.Skipf("no Monaco: %v", err)
	}
	w := &window{q: newEventq(), view: testView(t), face: face, scale: 1}
	withWindow(t, w)
	w.recount()
	if got := drain(w.q); len(got) != 1 {
		t.Fatalf("the first recount pushed %d events, want the size the editor allocates from", len(got))
	}
	if rows, cols := w.Size(); rows != 26 || cols != 80 {
		t.Fatalf("640x480 points at scale 1 counted %dx%d cells, want 26x80", rows, cols)
	}
	return w
}

// TestSetFrameSizeCountsTheGrid drives the real IMP: AppKit changes the frame,
// the callback runs, -bounds reports the new size and the editor is told in
// cells.
func TestSetFrameSizeCountsTheGrid(t *testing.T) {
	w := resizeWindow(t)

	w.view.Send(selSetFrameSize, NSSize{Width: 320, Height: 240})
	got := drain(w.q)
	if len(got) != 1 {
		t.Fatalf("halving the window pushed %d events, want 1: %v", len(got), got)
	}
	ev, ok := got[0].(ResizeEvent)
	if !ok {
		t.Fatalf("a resize pushed a %T", got[0])
	}
	if ev.Rows != 13 || ev.Cols != 40 {
		t.Errorf("320x240 points counted %dx%d cells, want 13x40", ev.Rows, ev.Cols)
	}
	if rows, cols := w.Size(); rows != ev.Rows || cols != ev.Cols {
		t.Errorf("Size() = %dx%d after an event saying %dx%d", rows, cols, ev.Rows, ev.Cols)
	}
}

// TestResizeToNothingIsOneCell. A window dragged to a sliver still has to hand
// the editor a grid it can address; every crash report about resizing to
// nothing starts with a zero here.
func TestResizeToNothingIsOneCell(t *testing.T) {
	w := resizeWindow(t)

	w.view.Send(selSetFrameSize, NSSize{Width: 1, Height: 1})
	if rows, cols := w.Size(); rows != 1 || cols != 1 {
		t.Errorf("a 1x1 point window counted %dx%d cells, want 1x1", rows, cols)
	}

	// And back, without a panic and without a stale count.
	w.view.Send(selSetFrameSize, NSSize{Width: 640, Height: 480})
	if rows, cols := w.Size(); rows != 26 || cols != 80 {
		t.Errorf("back at 640x480 the count is %dx%d, want 26x80", rows, cols)
	}
}

// TestAResizeToTheSameSizeSaysNothing. A window dragged one point at a time
// produces a setFrameSize: per mouse-move event and a cell count that changes
// every eight of them; an event per point would be seven redraws in eight that
// paint the identical grid.
func TestAResizeToTheSameSizeSaysNothing(t *testing.T) {
	w := resizeWindow(t)

	w.view.Send(selSetFrameSize, NSSize{Width: 643, Height: 483})
	if got := drain(w.q); len(got) != 0 {
		t.Errorf("three points wider is the same 80 columns, and it pushed %v", got)
	}
}

// TestRelayoutPushesExactlyOneEvent is the defect a live window found on
// and the reason recount reports whether it pushed.
//
// A font or 'linespace' change has to deliver an event whether or not the cell
// count moved, because the window has to be repainted either way. It must not
// deliver two: an editor handling the stale one last is left believing a size
// the window does not have, which is what `:set guifont=Monaco:h9` followed by
// `:set linespace=6` produced -- an event saying 40 rows against a window of
// 32.
func TestRelayoutPushesExactlyOneEvent(t *testing.T) {
	w := resizeWindow(t)

	// A change that moves the count: an 18 pixel row plus 6 is 24, and 480
	// over 24 is 20 rows.
	w.mu.Lock()
	w.linespace = 6
	w.mu.Unlock()
	w.relayout()

	got := drain(w.q)
	if len(got) != 1 {
		t.Fatalf("a relayout that changed the count pushed %d events, want 1: %v", len(got), got)
	}
	if ev := got[0].(ResizeEvent); ev.Rows != 20 || ev.Cols != 80 {
		t.Errorf("linespace 6 counted %dx%d, want 20x80", ev.Rows, ev.Cols)
	}

	// And one that does not: same metrics, same count, and the event still has
	// to arrive or the window sits in the old font until the next keystroke.
	w.relayout()
	got = drain(w.q)
	if len(got) != 1 {
		t.Fatalf("a relayout that changed nothing pushed %d events, want 1: %v", len(got), got)
	}
	if ev := got[0].(ResizeEvent); ev.Rows != 20 || ev.Cols != 80 {
		t.Errorf("the forced event says %dx%d, want the size the window has, 20x80", ev.Rows, ev.Cols)
	}
	if rows, cols := w.Size(); rows != 20 || cols != 80 {
		t.Errorf("Size() = %dx%d, want 20x80", rows, cols)
	}
}

// TestBackingChangeRebuildsTheFace is the second-display path as far as it can
// be driven here: readScale answers 1 for a window that is not on a screen, so
// what this checks is the half that does not need one -- that a scale which
// has not changed pushes nothing, which is what stops every window move from
// rebuilding the font.
func TestBackingChangeRebuildsTheFace(t *testing.T) {
	w := resizeWindow(t)
	before := w.face

	w.view.Send(objc.RegisterName("viewDidChangeBackingProperties"))
	if got := drain(w.q); len(got) != 0 {
		t.Errorf("a backing change at the same scale pushed %v", got)
	}
	w.mu.Lock()
	after := w.face
	w.mu.Unlock()
	if after != before {
		t.Error("the face was rebuilt for a scale that did not change")
	}
}
