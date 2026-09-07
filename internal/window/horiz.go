package window

import "github.com/pkar/pvim/internal/text"

// SideCmd is one of vim's horizontal scrolling commands. They only do anything
// with 'nowrap', which the vimrc sets, so they are live in every window this
// editor opens.
type SideCmd uint8

// The horizontal scrolling commands.
const (
	// SideRight is zl and z<Right>, SideLeft is zh and z<Left>: one column,
	// or count columns.
	SideRight SideCmd = iota
	SideLeft
	// SideHalfRight is zL and SideHalfLeft is zH: half a window's width.
	SideHalfRight
	SideHalfLeft
	// SideStart is zs and SideEnd is ze: put the cursor's column at the left
	// or the right edge of the window, honouring 'sidescrolloff'.
	SideStart
	SideEnd
)

// Side is the option values the horizontal arithmetic reads.
//
// It is a struct rather than three parameters because every function here
// wants all three and the order of three bare ints is a bug waiting to be
// written.
type Side struct {
	// ScrollOff is 'sidescrolloff', which the vimrc sets to 5: how many
	// columns of context stay between the cursor and each edge.
	ScrollOff int
	// Scroll is 'sidescroll': how far a horizontal scroll jumps when the
	// cursor leaves the window. Zero, vim's default, means half a window,
	// which is what puts the cursor in the middle after a long "|" jump.
	Scroll int
	// TabStop is 'tabstop', needed to turn a byte column into a display one.
	TabStop int
}

// CursorDispCol is the cursor's display column, 0-based, which is the
// coordinate every rule below is written in.
func (w *Window) CursorDispCol(tabstop int) int {
	return text.DisplayCol(w.Buf.Line(w.View.Cursor.Line), w.View.Cursor.Col, tabstop)
}

// SideScrollToCursor is vim's curs_columns: after a motion has moved the
// cursor sideways, scroll the window so the cursor is on screen with
// 'sidescrolloff' columns of context.
//
// It does nothing under 'wrap', where a long line takes several rows and there
// is no horizontal scrolling at all.
//
// The rule, measured against vim 9.2.0321 in an 80-column window
// over 300-character lines, by setting 'leftcol' with winrestview(), jumping
// with "{n}|" and reading winsaveview().leftcol back:
//
//	off_left = cursor - leftcol - 'sidescrolloff'
//	off_right = cursor - (leftcol + width - 'sidescrolloff') + 1
//
// If neither is out of range nothing moves. Otherwise, with 'sidescroll' zero,
// or with the overshoot at least half a window, the cursor goes to the middle:
// leftcol = cursor - width/2. Otherwise the window slides by the overshoot, at
// least 'sidescroll' columns.
//
// The middle case is what makes "100|" from column 1 of an 80-column window
// land on leftcol 59 with 'sidescroll' 0 and on leftcol 25 with 'sidescroll' 1:
// the first centres and the second slides the 25 columns it has to.
func (w *Window) SideScrollToCursor(s Side) {
	if w.Opt.Wrap || w.View.Width <= 0 {
		w.View.LeftCol = 0
		return
	}
	width := w.View.Width
	siso := clampSideScrollOff(s.ScrollOff, width)
	col := w.CursorDispCol(s.TabStop)

	offLeft := col - w.View.LeftCol - siso
	offRight := col - (w.View.LeftCol + width - siso) + 1
	if offLeft >= 0 && offRight <= 0 {
		return
	}

	diff := offRight
	if offLeft < 0 {
		diff = -offLeft
	}
	var left int
	switch {
	case s.Scroll == 0, diff >= width/2, offRight >= offLeft:
		left = col - width/2
	case offLeft < 0:
		left = w.View.LeftCol - max(diff, s.Scroll)
	default:
		left = w.View.LeftCol + max(diff, s.Scroll)
	}
	if left < 0 {
		left = 0
	}
	w.View.LeftCol = left
}

// SideScroll runs one horizontal scrolling command and reports whether the
// window moved.
//
// zh and zl move the window and let the cursor follow; zs and ze move the
// window to where the cursor already is and leave it alone. Measured with the
// cursor on display column 60 of an 80-column window at 'sidescrolloff' 5 and
// leftcol 50: zh gives 49, zH gives 10, zL gives 90, zs gives 55 and ze gives
// 0, which is 60 - 80 + 1 + 5 clamped up from -14.
func (w *Window) SideScroll(cmd SideCmd, count int, s Side) bool {
	if w.Opt.Wrap || w.View.Width <= 0 {
		return false
	}
	width := w.View.Width
	siso := clampSideScrollOff(s.ScrollOff, width)
	col := w.CursorDispCol(s.TabStop)
	n := max(1, count)
	old := w.View.LeftCol

	moveCursor := true
	left := w.View.LeftCol
	switch cmd {
	case SideRight:
		left += n
	case SideLeft:
		left -= n
	case SideHalfRight:
		left += width / 2 * n
	case SideHalfLeft:
		left -= width / 2 * n
	case SideStart:
		left, moveCursor = col-siso, false
	case SideEnd:
		left, moveCursor = col-width+1+siso, false
	}
	if left < 0 {
		left = 0
	}
	w.View.LeftCol = left
	if moveCursor {
		w.CursorInSideView(s)
	}
	return w.View.LeftCol != old
}

// CursorInSideView is the sideways half of cursor_correct: pull the cursor
// back between the window's edges with 'sidescrolloff' columns of margin.
//
// It is what makes zl move the cursor: scrolling one column right off a window
// whose cursor was in column 1 leaves the cursor outside, and vim puts it back
// at the margin rather than scrolling again.
func (w *Window) CursorInSideView(s Side) {
	if w.Opt.Wrap || w.View.Width <= 0 {
		return
	}
	width := w.View.Width
	siso := clampSideScrollOff(s.ScrollOff, width)
	col := w.CursorDispCol(s.TabStop)

	lo, hi := w.View.LeftCol+siso, w.View.LeftCol+width-1-siso
	if w.View.LeftCol == 0 {
		// Nothing is hidden to the left, so there is no context to keep and
		// the margin is suspended, exactly as 'scrolloff' is at the top of the
		// buffer. Measured: zh with the window already at column 0 leaves the
		// cursor in column 3, where zl to leftcol 1 moves it to column 6.
		lo = 0
	}
	if lo > hi {
		lo = w.View.LeftCol + width/2
		hi = lo
	}
	want := clamp(col, lo, hi)
	if want == col {
		return
	}
	line := w.Buf.Line(w.View.Cursor.Line)
	if end := lastDispCol(line, s.TabStop); want > end {
		// The margin column does not exist on this line, which is zl and zL
		// scrolled past the last character. Vim's leftcol_changed leaves the
		// cursor on the last character, pulls 'leftcol' back to it when the
		// character has scrolled off the left edge entirely, and then lets
		// curs_columns put the window where the cursor needs it. Letting
		// Buf.Clamp cap the column instead lands on len(line), which is the
		// insert-mode position and not a normal-mode one.
		//
		// Measured in an 80-column window over one 300-character line, all
		// display columns 0-based: at 'sidescrolloff' 0 and 'sidescroll' 0,
		// "2zL" from leftcol 220 with the cursor on 299 gives leftcol 299 and
		// the cursor still on 299, and so does "100zl" from leftcol 200 with
		// the cursor on 249. At 'sidescrolloff' 5 and 'sidescroll' 1, "zL"
		// from leftcol 259 gives 294 and "100zl" from the same place gives
		// 294 too. At 'sidescrolloff' 5 and 'sidescroll' 0 the last step
		// centres instead of sliding, so "zL" from leftcol 290 gives 259, and
		// in a 40-column window "zL" from leftcol 290 gives 279.
		want = end
		if end < w.View.LeftCol {
			w.View.LeftCol = end
		}
		w.View.Cursor = w.Buf.Clamp(text.Pos{
			Line: w.View.Cursor.Line,
			Col:  text.ByteColForDisplay(line, want, s.TabStop),
		})
		w.SideScrollToCursor(s)
		return
	}
	w.View.Cursor = w.Buf.Clamp(text.Pos{
		Line: w.View.Cursor.Line,
		Col:  text.ByteColForDisplay(line, want, s.TabStop),
	})
}

// lastDispCol is the display column the last character of a line is drawn at,
// which is the rightmost column a normal-mode cursor can occupy. An empty line
// has none and answers 0, the column the cursor sits in there.
//
// It is the start of the character and not its last cell: a line ending in a
// tab puts the cursor on the tab's first column, which is where vim's
// coladvance leaves it.
func lastDispCol(line []byte, tabstop int) int {
	if len(line) == 0 {
		return 0
	}
	return text.DisplayCol(line, len(line)-1, tabstop)
}

// clampSideScrollOff is 'sidescrolloff' held to something the window can
// honour: half its width less one column, so that the low and the high margin
// cannot cross.
func clampSideScrollOff(siso, width int) int {
	if siso < 0 {
		return 0
	}
	if maxOff := (width - 1) / 2; siso > maxOff {
		return maxOff
	}
	return siso
}
