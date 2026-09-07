//go:build darwin

package gui

import (
	"fmt"
	"image"

	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// The frame path: a Screen from the editor goroutine turns into pixels here,
// into one of three buffers, and the main thread puts the newest of them on a
// CALayer.
//
// # The dirty-rect blit, and the trap it comes with
//
// Painting every cell of every frame was measured first: a full 200x60 repaint
// at scale 2 is 3.87ms against the 4ms budget, a three per cent margin. So this
// paints only what changed, which is raster.Damage and raster.PaintRects, and a
// keystroke costs the two cells the caret moved between.
//
// That is only correct because each buffer carries a raster.Painted saying
// what it currently holds. Three things make it wrong if any one is missed:
//
// - The pool is three deep and a frame is painted into whichever buffer
// CoreGraphics is not reading, so the buffer this frame lands in may hold
// a frame two or three old. The diff is therefore against THAT buffer's
// grid and not against "the last frame", which is why Painted is per
// buffer and not per window.
// - A `:hi` or a `:colorscheme` recolours a highlight group and changes not
// one cell of the grid. A frontend that trusted the cell diff would leave
// the window in the old colours until something happened to touch each
// cell. raster.Stamp is the answer -- it hashes the highlight table, the
// face metrics, 'linespace' and the cursor shape -- and a stamp that does
// not match is a full repaint. TestRecolourForcesAFullRepaint is the test
// that says so, and it is the one test in this file worth keeping if the
// rest were deleted.
// - A resize, a font change and a move to a display at another scale all
// change the metrics, so they all change the stamp, so they are all full
// repaints without a line of code that knows they exist.
type frameStats struct {
	// full and partial count what Damage decided, for the tests and for
	// anyone who wants to know whether the fast path is being taken at all.
	full    int
	partial int
}

// bufferStep is the granularity frame buffers are allocated at, in device
// pixels.
//
// Every cell of extra window width during a resize drag is a new grid size, a
// new ResizeEvent and a new Draw at a larger size, so an exactly-sized pool
// would be reallocated a few hundred times over one drag from 640 to 3000
// points. Rounding up to a coarse step makes it a dozen, and a dozen
// allocations of a few megabytes over a drag is not worth further cleverness.
const bufferStep = 256

// Draw paints s into a back buffer and asks the main thread to put it on
// screen. It is called on the editor goroutine and touches no AppKit.
//
// The rasterising happens here, on the editor's own goroutine, rather than in
// the main-thread callback. That is deliberate: turning a grid into pixels is
// the expensive half of a frame and doing it on main would put the window's
// event handling behind the editor's slowest redraw.
func (w *window) Draw(s *screen.Screen) error {
	if s == nil {
		return nil
	}
	bg, err := w.paint(s)
	if err != nil {
		return err
	}

	if bg.changed {
		w.main.do(func() { w.blit.setBackground(bg.rgb.R, bg.rgb.G, bg.rgb.B) })
	}
	// wakeRedraw rather than do: a hundred frames queued between two run loop
	// passes are one blit of the newest, not a hundred blits nobody sees.
	w.main.wakeRedraw(w.blitFront)
	return nil
}

// background is what paint decided about the layer's own colour.
type background struct {
	rgb     screen.RGB
	changed bool
}

// paint is Draw with no AppKit in it: everything from a Screen to a published
// frame, and nothing that has to happen on the main thread.
//
// It is split out so that the damage bookkeeping -- which is the part with a
// bug in it, if there is one -- is testable against a window struct with no
// window server, no run loop and no layer behind it.
func (w *window) paint(s *screen.Screen) (background, error) {
	w.mu.Lock()
	face, linespace := w.face, w.linespace
	w.mu.Unlock()
	if face == nil {
		return background{}, fmt.Errorf("gui: Draw before the font was loaded")
	}

	opt := raster.PaintOptions{Face: face, Linespace: linespace}
	frame := raster.Frame{
		Grid:      &s.Grid,
		HL:        s.HL,
		CursorRow: s.CursorRow,
		CursorCol: s.CursorCol,
		Cursor:    rasterCursor(s.CursorShape),
	}
	pw, ph := raster.Size(frame, opt)

	img, prev, slot := w.buffer(pw, ph)
	spans, now, full := raster.Damage(prev, frame, opt)
	err := raster.PaintRects(img, frame, opt, spans)
	if err != nil {
		// The buffer now holds a half-painted frame, and the only safe
		// description of it is "nothing that can be kept".
		w.record(slot, raster.Painted{}, full)
		return background{}, err
	}
	w.record(slot, now, full)

	bg := s.Look(screen.Normal).BG
	w.mu.Lock()
	w.front = frameBuf{img: img, w: pw, h: ph}
	w.curRow, w.curCol = s.CursorRow, s.CursorCol
	w.lastMode = inputModeOf(s)
	changed := !w.bgSet || w.bg != bg
	w.bg, w.bgSet = bg, true
	w.mu.Unlock()

	return background{rgb: bg, changed: changed}, nil
}

// record stores what a buffer now holds and counts the decision Damage made.
func (w *window) record(slot int, p raster.Painted, full bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if slot >= 0 && slot < len(w.painted) {
		w.painted[slot] = p
	}
	if full {
		w.stats.full++
	} else {
		w.stats.partial++
	}
}

// buffer returns a back buffer of at least pw by ph pixels that CoreGraphics is
// not reading, what that buffer currently holds, and which slot of the pool it
// came from.
//
// Buffers only ever grow, and growing replaces the whole pool: the old buffers
// are retired, and the ones the layer or an in-flight blit still point into are
// unpinned by sweepRetired once a later frame has replaced them, rather than
// kept for the life of the process. A replaced buffer's Painted goes with it,
// which is what makes the first frame after a resize a full repaint.
func (w *window) buffer(pw, ph int) (*image.RGBA, raster.Painted, int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.bufs[0] == nil || pw > w.bufW || ph > w.bufH {
		if pw < w.bufW {
			pw = w.bufW
		}
		if ph < w.bufH {
			ph = w.bufH
		}
		pw, ph = roundUp(pw, bufferStep), roundUp(ph, bufferStep)
		w.bufW, w.bufH = pw, ph
		for i := range w.bufs {
			w.retire(w.bufs[i])
			w.bufs[i] = image.NewRGBA(image.Rect(0, 0, pw, ph))
			w.painted[i] = raster.Painted{}
			pin(w.bufs[i])
		}
	}
	w.sweepRetired()

	for range w.bufs {
		w.next = (w.next + 1) % len(w.bufs)
		b := w.bufs[w.next]
		if b == w.shown || b == w.blitting {
			continue
		}
		// The pool is three deep, so the buffer that comes round can still be
		// the one holding the frame the main thread has not blitted yet. That
		// frame is about to be replaced by a newer one painted into the same
		// pixels, so it is unpublished here, in the same critical section that
		// hands the buffer out: leaving it published would let the main thread
		// start reading it halfway through the repaint. Nothing is lost, the
		// repaint ends in its own wakeRedraw.
		if b == w.front.img {
			w.front = frameBuf{}
		}
		return b, w.painted[w.next], w.next
	}
	// Unreachable. shown and blitting are at most two distinct buffers and the
	// pool holds three, so the loop always finds one.
	return w.bufs[w.next], w.painted[w.next], w.next
}

// roundUp returns n rounded up to a multiple of step.
func roundUp(n, step int) int {
	if n <= 0 {
		return step
	}
	return ((n + step - 1) / step) * step
}

// retire gives up a buffer the window has stopped drawing into. It is unpinned
// straight away unless CoreGraphics is still reading it, in which case it waits
// on the retired list. Caller holds w.mu.
func (w *window) retire(img *image.RGBA) {
	if img == nil {
		return
	}
	if img == w.shown || img == w.blitting {
		w.retired = append(w.retired, img)
		return
	}
	unpin(img)
}

// sweepRetired unpins every retired buffer CoreGraphics has finished with,
// which is every one that is neither on the layer nor under an in-flight blit.
// Caller holds w.mu.
func (w *window) sweepRetired() {
	kept := w.retired[:0]
	for _, img := range w.retired {
		if img == w.shown || img == w.blitting {
			kept = append(kept, img)
			continue
		}
		unpin(img)
	}
	w.retired = kept
}

// blitFront puts the newest painted frame on screen. Main thread only: it is
// called from the run loop source and from the view's updateLayer.
//
// The two assignments around show() are what keeps the editor off the pixels
// CoreGraphics is reading. blitting says a CGImage is being built over this
// buffer right now; shown says the layer's contents point into it and will
// until some later frame replaces them. buffer() refuses both.
func (w *window) blitFront() {
	w.mu.Lock()
	fb := w.front
	w.blitting = fb.img
	w.mu.Unlock()

	w.blit.show(fb)

	w.mu.Lock()
	if fb.img != nil {
		w.shown = fb.img
	}
	w.blitting = nil
	w.sweepRetired()
	w.mu.Unlock()
}
