package screen

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// wideLine is "ab" followed by eight CJK runes and "ZY": display columns 0 and
// 1 for the two letters, then pairs of cells at 2, 4, 6, 8, 10, 12, 14 and 16,
// then 18 and 19. It is the line every horizontal-scroll measurement below was
// taken over.
const wideLine = "ab一二三四五六七八" + "ZY"

// hrig is a window of the given width over one line, under 'nowrap'.
func hrig(width int, line string) (*window.Window, *options.Options) {
	o := options.Defaults()
	b := text.Read([]byte(line + "\n"))
	w := window.New(1, b, o.GW)
	w.Opt.Wrap = false
	w.View.Cursor = text.Pos{Line: 1}
	w.View.Width = width
	w.View.Height = 4
	return w, &o
}

// With 'sidescroll' 0 vim puts the cursor halfway across the window when it
// scrolls, which is one jump rather than a column at a time.
//
// vim --clean, :set lines=6 columns=12 nowrap sidescrolloff=0, then 13| over
// wideLine: LeftCol becomes 6 and the window shows the runes at columns 6
// through 17.
func TestSideScrollHalfAScreen(t *testing.T) {
	w, o := hrig(12, wideLine)
	o.G.SideScroll = 0
	o.G.SideScrollOff = 0
	w.View.Cursor.Col = byteOfCol(wideLine, 12)

	if !SideScroll(w, o) {
		t.Fatal("SideScroll did not move")
	}
	if w.View.LeftCol != 6 {
		t.Errorf("LeftCol = %d, vim scrolls to 6", w.View.LeftCol)
	}
}

// With 'sidescroll' 1 vim moves the least it can, which is far enough that the
// whole two-cell rune under the cursor fits.
//
// vim --clean, :set nowrap sidescrolloff=0 sidescroll=1 on a 12-column screen:
// at display column 12 LeftCol becomes 2, at 14 it becomes 4.
func TestSideScrollOneColumnAtATime(t *testing.T) {
	for _, c := range []struct{ col, want int }{{12, 2}, {14, 4}} {
		w, o := hrig(12, wideLine)
		o.G.SideScroll = 1
		o.G.SideScrollOff = 0
		w.View.Cursor.Col = byteOfCol(wideLine, c.col)
		SideScroll(w, o)
		if w.View.LeftCol != c.want {
			t.Errorf("cursor at display column %d: LeftCol = %d, vim scrolls to %d",
				c.col, w.View.LeftCol, c.want)
		}
	}
}

// 'sidescrolloff' 5 in a 20-column window, which is what the vimrc asks for:
// the cursor at display column 39 puts LeftCol at 29, halfway back.
//
// vim --clean, :set lines=12 columns=60 nowrap sidescrolloff=5, :vsplit twice,
// 40| in the left window, which is 20 columns wide.
func TestSideScrollOffFive(t *testing.T) {
	line := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi"
	w, o := hrig(20, line)
	o.G.SideScrollOff = 5
	w.View.Cursor.Col = 39

	if !SideScroll(w, o) {
		t.Fatal("SideScroll did not move")
	}
	if w.View.LeftCol != 29 {
		t.Errorf("LeftCol = %d, vim scrolls to 29", w.View.LeftCol)
	}
	// Already in view with room to spare: nothing moves.
	w.View.Cursor.Col = 35
	if SideScroll(w, o) {
		t.Errorf("SideScroll moved to %d for a cursor already in view", w.View.LeftCol)
	}
}

// A tab is one byte and several cells, and the scroll has to be against the
// cells.
func TestSideScrollOverATab(t *testing.T) {
	w, o := hrig(12, "\t\t\tx")
	o.B.TabStop = 8
	o.G.SideScroll = 0
	o.G.SideScrollOff = 0
	w.View.Cursor.Col = 3 // the "x", at display column 24

	SideScroll(w, o)
	if w.View.LeftCol != 18 {
		t.Errorf("LeftCol = %d, want 18: display column 24 less half of 12", w.View.LeftCol)
	}
}

// Under 'wrap' there is no horizontal scrolling at all and LeftCol goes to
// zero, which is what vim does the moment the option is set.
func TestSideScrollDoesNothingUnderWrap(t *testing.T) {
	w, o := hrig(12, wideLine)
	w.Opt.Wrap = true
	w.View.LeftCol = 6
	if !SideScroll(w, o) {
		t.Fatal("setting 'wrap' did not reset LeftCol")
	}
	if w.View.LeftCol != 0 {
		t.Errorf("LeftCol = %d under 'wrap', want 0", w.View.LeftCol)
	}
}

// byteOfCol is the byte column of the character drawn at a display column.
func byteOfCol(line string, disp int) int {
	for _, c := range Chars([]byte(line), 8) {
		if disp >= c.Col && disp < c.Col+c.Width {
			return c.Byte
		}
	}
	return len(line)
}

// With 'sidescroll' non-zero vim does not round LeftCol up to a multiple of
// the step, and it abandons the step altogether for a half-window jump when
// the cursor is far off. Vim's curs_columns():
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
// Every row below was measured on vim 9.2.0321 at:set columns=20 nowrap over
// a 60-character line, setting the state with winrestview({'col':..,
// 'leftcol':..}) and reading winsaveview().leftcol back after a redraw!. The
// whole grid of 'sidescroll' 0..7 by 'sidescrolloff' 0..12 by five LeftCols by
// five cursor columns, 1050 rows, agrees with this arithmetic.
func TestSideScrollExactShortfall(t *testing.T) {
	line := strings.Repeat("L", 60)
	for _, c := range []struct{ ss, siso, left, col, want int }{
		// Rightward: the shortfall is 5 and vim moves by 5, not to the
		// next multiple of the step.
		{1, 0, 0, 24, 5},
		{2, 0, 0, 24, 5},
		{3, 0, 0, 24, 5},
		{5, 0, 0, 24, 5},
		// A shortfall of half the window or more gives up on the step.
		{1, 5, 0, 24, 14},
		{2, 5, 0, 24, 14},
		// Leftward, the same two shapes.
		{2, 0, 40, 24, 14},
		{1, 5, 40, 24, 14},
		{2, 5, 40, 24, 14},
		{3, 3, 40, 24, 14},
		{1, 1, 24, 24, 23},
		{2, 0, 24, 9, 0},
		{2, 3, 14, 44, 34},
		{2, 1, 5, 24, 7},
		// 'sidescroll' 0 is the half-window jump whatever the context is.
		{0, 0, 0, 24, 14},
		{0, 5, 0, 24, 14},
		// Already visible with room to spare: nothing moves.
		{1, 0, 5, 9, 5},
	} {
		w, o := hrig(20, line)
		o.G.SideScroll = c.ss
		o.G.SideScrollOff = c.siso
		w.View.LeftCol = c.left
		w.View.Cursor.Col = c.col

		SideScroll(w, o)
		if w.View.LeftCol != c.want {
			t.Errorf("ss=%d siso=%d leftcol=%d col=%d: LeftCol = %d, vim gives %d",
				c.ss, c.siso, c.left, c.col, w.View.LeftCol, c.want)
		}
	}
}
