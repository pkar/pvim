//go:build darwin

package gui

import "github.com/pkar/pvim/internal/raster"

// 'guifont' and 'linespace' after the window is already up.
//
// The vimrc is parsed after the window opens -- it has to be, because
// has('gui_running') has to answer true while it is read -- so the font the
// first frame is drawn in is the package default and `set guifont=Monaco:h13`
// arrives afterwards. That makes the run-time path the normal one and the
// startup path the special case, rather than the other way round.
//
// Both of these are called from the editor goroutine. The expensive half, which
// is reading a font file and building a Face, happens there; only the swap and
// the relayout are posted to the main thread, and the swap is safe because the
// only other reader of w.face is Draw, on the same goroutine.

// setFont is the darwin implementation of SetFont.
func setFont(g raster.GUIFont) error {
	w := current
	if w == nil {
		// Before Run: this is the font the first frame will be drawn in.
		Font = g
		return nil
	}
	w.mu.Lock()
	scale := w.scale
	w.mu.Unlock()

	face, err := raster.NewFace(g, scale)
	if err != nil {
		// The old face stays. A window in the wrong font beats a window in no
		// font, and the caller has the error to put on the message line, which
		// is what vim does with E596.
		return err
	}

	// The window's own font and not the package variable: the main thread
	// reads this one back when the display changes, and a package variable
	// written here would be a race across that seam. Font stays what it was,
	// which is what the first frame was drawn in, and api.go says so.
	w.mu.Lock()
	w.face, w.font = face, g
	w.mu.Unlock()
	return onMain(w.relayout)
}

// setLinespace is the darwin implementation of SetLinespace.
func setLinespace(n int) error {
	if n < 0 {
		n = 0
	}
	w := current
	if w == nil {
		return ErrNoMainThread
	}
	w.mu.Lock()
	w.linespace = n
	w.mu.Unlock()
	return onMain(w.relayout)
}

// relayout recounts the grid after the cell size changed underneath it. Main
// thread only, because it reads the view's bounds.
//
// It forces the ResizeEvent that recount would only send on a change. A font
// one point smaller can leave the cell count exactly where it was, and then
// nothing would tell the editor to redraw and the window would sit in the old
// font until the next keystroke. ResizeEvent's own documentation says a font
// change produces one, and this is where that promise is kept.
//
// Exactly one, which is the half that was wrong until a live window caught it.
// recount pushes its own event when the count changed, so pushing another here
// unconditionally meant `:set guifont=` delivered two, and an editor that then
// changed the font again handled the stale one last: measured at 40 rows in the
// event against 32 from Size(), with nothing on screen to say which was true.
//
// Nothing here forces a full repaint, and nothing needs to: the face metrics
// and 'linespace' are both inside raster.StampOf, so the next frame's stamp
// cannot match what any buffer in the pool holds and every one of them is
// repainted whole. That is the same mechanism a:colorscheme reload rides on.
func (w *window) relayout() {
	if w.recount() {
		return
	}
	rows, cols := w.Size()
	w.q.push(ResizeEvent{Rows: rows, Cols: cols})
}
