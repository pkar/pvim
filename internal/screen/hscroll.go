package screen

import (
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// Horizontal scrolling under 'nowrap', which is every window this vimrc opens.
//
// It lives here rather than in internal/window with the vertical scrolling
// because it is arithmetic over display columns, and a display column is a
// question about tabs and wide runes that only the renderer can answer. The
// vertical half needs nothing but line numbers, which is why the two are in
// different packages.
//
// Measured on a 12-column window over "ab" and eight CJK runes, with
// 'sidescrolloff' 0:
//
//	sidescroll=0, cursor at display column 12: LeftCol becomes 6, which puts
//	 the cursor halfway across the window.
//	sidescroll=1, cursor at display column 12: LeftCol becomes 2, the least
//	 that shows the whole two-cell rune the cursor is on. At column 14 it
//	 becomes 4, for the same reason.

// SideScroll moves a window's LeftCol so the cursor is visible with
// 'sidescrolloff' columns of context, and reports whether it moved.
//
// Under 'wrap' there is no horizontal scrolling and LeftCol goes to zero,
// which is what vim does the moment the option is set.
func SideScroll(w *window.Window, o *options.Options) bool {
	if w == nil {
		return false
	}
	if w.Opt.Wrap {
		if w.View.LeftCol == 0 {
			return false
		}
		w.View.LeftCol = 0
		return true
	}

	width := w.View.Width
	if width < 1 {
		return false
	}
	ts := 8
	if o != nil && o.B.TabStop > 0 {
		ts = o.B.TabStop
	}

	line := lineAt(w.Buf, w.View.Cursor.Line)
	start, cw := charAtByte(line, w.View.Cursor.Col, ts)

	// Vim itself does not hold 'sidescrolloff' to half the window here, and
	// with the arithmetic below it does not need to: a siso wider than the
	// window makes off_right >= off_left and takes the centring branch. This
	// clamp is older than that arithmetic and it changes the answer for a siso
	// above (width - 1) / 2, which is eleven of the 1050 measured rows and no
	// setting this vimrc has: it never sets 'sidescrolloff' at all. Left as it
	// was rather than changed on the side of a fix to the step.
	siso := w.SideScrollOff(o)
	if maxSiso := (width - 1) / 2; siso > maxSiso {
		siso = maxSiso
	}
	if siso < 0 {
		siso = 0
	}
	step := 0
	if o != nil {
		step = o.G.SideScroll
	}

	// Vim's curs_columns(), the whole of it, because the three branches are
	// not interchangeable:
	//
	//	off_left = startcol - w_leftcol - siso;
	//	off_right = endcol - (w_leftcol + textwidth - siso) + 1;
	//	if (off_left < 0 || off_right > 0)
	//	{
	//	 diff = off_left < 0 ? -off_left: off_right;
	//	 if (p_ss == 0 || diff >= textwidth / 2 || off_right >= off_left)
	//	 new_leftcol = w_wcol - extra - textwidth / 2;
	//	 else
	//	 {
	//	 if (diff < p_ss) diff = p_ss;
	//	 new_leftcol = w_leftcol + (off_left < 0 ? -diff: diff);
	//	 }
	//	}
	//
	// 'sidescroll' is a floor on how far to move and not a grid to land on, so
	// a shortfall of five columns moves five and not up to the next multiple
	// of the step, and a cursor half a window or more off gives up on the step
	// and centres. Measured on vim 9.2.0321 at:set columns=20 nowrap over a
	// 60-character line, driving winrestview() and reading back
	// winsaveview().leftcol: the whole grid of 'sidescroll' 0 to 7 by
	// 'sidescrolloff' 0 to 12 by five LeftCols by five cursor columns, 1050
	// rows, agrees with this everywhere the clamp above does not bite.
	left := w.View.LeftCol
	offLeft := start - left - siso
	offRight := start + cw - 1 - (left + width - siso) + 1
	if offLeft >= 0 && offRight <= 0 {
		return false
	}
	diff := offRight
	if offLeft < 0 {
		diff = -offLeft
	}
	if step <= 0 || diff >= width/2 || offRight >= offLeft {
		left = start - width/2
	} else {
		if diff < step {
			diff = step
		}
		if offLeft < 0 {
			left -= diff
		} else {
			left += diff
		}
	}

	if left < 0 {
		left = 0
	}
	if left == w.View.LeftCol {
		return false
	}
	w.View.LeftCol = left
	return true
}

// lineAt returns a buffer line, or nothing at all for a line number off the
// end, so the arithmetic above never has to check.
func lineAt(b *text.Buffer, n int) []byte {
	if b == nil || n < 1 || n > b.LineCount() {
		return nil
	}
	return b.Line(n)
}

// charAtByte returns the display column the character containing byte col
// starts at, and how many cells it takes.
func charAtByte(line []byte, col, tabstop int) (start, width int) {
	w := newWalker(line, tabstop)
	for {
		c, ok := w.next()
		if !ok {
			// Past the end of the line: the cursor sits in the cell after it.
			return w.col, 1
		}
		if col < c.Byte+c.Size {
			return c.Col, c.Width
		}
	}
}
