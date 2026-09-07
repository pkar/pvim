// Package window is the piece the editor has been missing: a window size and a
// scroll position.
//
// Everything ran headless, which is exactly why 211 of the 270
// differences in the last fuzz run were keys that need a window. CTRL-U alone
// was 85 of them, CTRL-W 36, and H, M and L another 43; the rest were CTRL-D,
// CTRL-F, CTRL-B, CTRL-E, CTRL-Y and the z family. None of them are hard. All
// of them need to know which buffer lines are on the screen, and nothing knew.
//
// A Window owns a buffer, a View and its window-local options. A tab page owns
// a tree of windows split horizontally and vertically, and a Tabs owns the tab
// pages. The scroll commands live here because they are arithmetic over the
// View and 'scroll' and 'scrolloff', and putting them in internal/mode would
// bury them in the largest package in the tree.
//
// This package imports internal/text and internal/options and nothing else. In
// particular it does not import internal/screen: a window knows how many cells
// tall it is, not what a Grid is, so the whole of the layout arithmetic is
// testable with two integers and no display. Rect is this package's own
// rectangle for that reason, and internal/screen converts.
//
// Every number in this package was measured against vim 9.2 patches 1-321
// through "vim --clean -i NONE --not-a-term -s", which gives a 24-row screen,
// an 80-column one, a 23-row text window and 'scroll' at 11. The three tables
// under testdata/window hold the measurements and three tests replay them; a
// row that fails is vim disagreeing with this package and nothing else.
//
// What is not here, and why:
//
// - Folds. za, zo, zc and the rest need a fold model, which belongs with the
// thing that draws them, and until one exists this package counts one
// screen row per buffer line.
// - 'wrap'. Every rule here is the 'nowrap' arithmetic. The vimrc sets
// 'nowrap' so nothing in daily use hits it; see the TODO on BotLine.
// - The tabline. Layout is handed the region below it and does not know it
// exists.
package window

import (
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
)

// View is where a window is looking: the scroll position and the cursor.
//
// TopLine and Cursor.Line are buffer lines, 1-based. LeftCol is a display
// column, 0-based, and is nonzero only with 'nowrap' on a line that has run
// off the right-hand edge. Height and Width are the text area in cells, which
// excludes the status line and will exclude the 'number' column and the fold
// column once those exist.
type View struct {
	// TopLine is the first buffer line drawn in the window.
	TopLine int
	// LeftCol is the display column drawn in the leftmost text cell. Zero
	// under 'wrap', which is every window this vimrc opens except that the
	// vimrc sets 'nowrap', so it is every window.
	LeftCol int
	// Cursor is where the cursor is in the buffer.
	Cursor text.Pos
	// Curswant is the display column j and k are aiming for, carried between
	// motions. It lives here rather than in motion.Context because it is
	// per-window: two windows on one buffer have two cursors and two
	// curswants.
	Curswant int
	// Height and Width are the text area in cells.
	Height, Width int
	// SkipCol is how many display cells of the top line are scrolled off the
	// top under 'wrap' when the first line is taller than the window. Vim
	// calls it w_skipcol. Zero under 'nowrap', which is this vimrc.
	//
	// TODO: nothing sets or reads this yet. It exists so that the wrap-aware
	// scroll arithmetic has somewhere to put its answer rather than being
	// retrofitted through every caller.
	SkipCol int
}

// Rect is a rectangle of cells: where a window sits on the screen.
//
// It is this package's own type and not internal/screen's Region so that the
// layout tree can be computed and tested with no grid in sight. They hold the
// same four numbers and internal/screen converts between them, which is one
// small duplication bought against internal/window importing the display.
type Rect struct {
	Row, Col, Rows, Cols int
}

// Empty reports whether the rectangle has no cells in it.
func (r Rect) Empty() bool { return r.Rows <= 0 || r.Cols <= 0 }

// Window is one view on one buffer, with its own window-local options.
//
// Opt is only the window-local half. The buffer-local and global halves live
// on the editor's options.Options, and the two global-local options a window
// reads -- 'scrolloff' and 'sidescrolloff' -- are resolved by the ScrollOff
// and SideScrollOff methods, which take the editor's options and fall back to
// them. Nothing else here needs a scope it cannot see.
type Window struct {
	// ID is stable for the life of the window and is what vim's winid is.
	ID int

	// Buf is the buffer displayed. Two windows may share one.
	Buf *text.Buffer

	// View is the scroll position and the cursor.
	View View

	// Opt is the window-local options: 'wrap', 'number', 'scroll',
	// 'foldmethod', 'conceallevel' and the local half of 'scrolloff'.
	Opt options.Window

	// Rect is where the window sits on the screen, filled in by
	// TabPage.Layout. It covers the text plus the status line under it and the
	// separator column beside it, so it is one row taller and one column wider
	// than View.Height by View.Width wherever the window has them. The window
	// does not draw itself and never reads this; internal/screen does.
	Rect Rect

	// Alt is the alternate file: what CTRL-^ and ":b#" go back to, and what
	// "#" expands to. Nil until a second buffer has been opened in this
	// window.
	Alt *text.Buffer

	// Preview says this window is the preview window: vim's
	// 'previewwindow', which CTRL-W } and ":ptag" set on the window they
	// make, CTRL-W P goes to and CTRL-W z closes.
	//
	// A field and not an entry in options.Window, which is where every other
	// window-local option lives, because internal/options has no
	// 'previewwindow' in its table and adding one is that package's change to
	// make. A tab page has at most one, which nothing here enforces: vim's
	// rule is that the command reuses the window that already carries the
	// flag, and cmd/pvim/tags.go is what does the looking.
	Preview bool

	// Prev is the position ` and '' jump back to for this window, which vim
	// keeps per window and not per buffer.
	//
	// TODO: the jumplist proper is a ring of these and belongs here too, next to
	// it, once more than one buffer exists.
	Prev text.Pos
}

// New returns a window on a buffer, at the top of it, with the window-local
// options a new window is initialised from.
//
// gw is the global half of the window-local options, which is what
// ":setglobal" wrote and what vim copies into a new window. Passing
// options.Defaults().GW gives vim's own.
func New(id int, b *text.Buffer, gw options.Window) *Window {
	if b == nil {
		b = text.New()
	}
	return &Window{
		ID:   id,
		Buf:  b,
		Opt:  gw,
		View: View{TopLine: 1, Cursor: text.Pos{Line: 1}},
	}
}

// BotLine is the last buffer line the window is showing, 1-based and
// inclusive.
//
// This is the 'nowrap' answer: one screen row per buffer line, so the window
// shows Height lines starting at TopLine, clamped to the end of the buffer.
// The vimrc sets 'nowrap', so it is the answer for every window this editor
// opens.
//
// TODO: under 'wrap' a long line takes several rows and this is wrong. The fix
// is to walk forward from TopLine adding the row count of each line until
// Height is used up, which needs 'linebreak', 'breakindent' and the
// display-column arithmetic in internal/text, and it belongs in the same place
// that decides how to draw them. Until then, a wrapped window reports more
// visible lines than it has, which shows up as H, M and L landing too far
// down.
func (w *Window) BotLine() int {
	last := w.View.TopLine + w.View.Height - 1
	if n := w.Buf.LineCount(); last > n {
		last = n
	}
	if last < w.View.TopLine {
		last = w.View.TopLine
	}
	return last
}

// AtTop reports whether the window is scrolled hard against the start of the
// buffer, which is what suspends 'scrolloff' for H.
//
// Measured: with 'scrolloff' 3 and the window at the top of a 100-line file,
// H goes to line 1, not to line 4. Scrolled to line 39, H goes to 42.
func (w *Window) AtTop() bool { return w.View.TopLine <= 1 }

// AtBottom reports whether the last line of the buffer is on screen, which
// suspends 'scrolloff' for L in the same way.
//
// Measured: G on a 100-line file leaves lines 78 to 100 showing, and L goes to
// 100. Scrolled so that line 61 is the last one, L goes to 58.
func (w *Window) AtBottom() bool { return w.BotLine() >= w.Buf.LineCount() }

// Visible is what the window is showing, in the shape internal/motion's H, M
// and L want.
//
// It is a separate struct rather than motion.Window because this package does
// not import internal/motion: a motion answers where the cursor goes given a
// buffer, and the day it can ask a window a question it stops being testable
// with a buffer and a position. The ex layer, which imports both, converts.
type Visible struct {
	// Top and Bottom are the first and last buffer line displayed, 1-based
	// and inclusive.
	Top, Bottom int
	// AtTop and AtBottom say whether 'scrolloff' is suspended at that end.
	AtTop, AtBottom bool
	// LeftCol is the display column drawn in the leftmost cell.
	LeftCol int
	// Height is the window in rows, which is more than Bottom-Top+1 whenever
	// the buffer runs out before the screen does.
	Height int
}

// Visible returns what the window is showing.
func (w *Window) Visible() Visible {
	return Visible{
		Top:      w.View.TopLine,
		Bottom:   w.BotLine(),
		AtTop:    w.AtTop(),
		AtBottom: w.AtBottom(),
		LeftCol:  w.View.LeftCol,
		Height:   w.View.Height,
	}
}

// ScrollOff resolves 'scrolloff' for this window: the window-local value when
// it is set, the global one otherwise.
//
// o is the editor's options, whose W half is not necessarily this window's, so
// the local value is read off the window and only the fallback comes from o.
// The vimrc sets it globally to 3.
func (w *Window) ScrollOff(o *options.Options) int {
	if w.Opt.ScrollOff >= 0 {
		return w.Opt.ScrollOff
	}
	if o == nil {
		return 0
	}
	return o.G.ScrollOff
}

// SideScrollOff resolves 'sidescrolloff' the same way. The vimrc sets it to 5,
// and with 'nowrap' it is what holds a long line five columns off the edge.
func (w *Window) SideScrollOff(o *options.Options) int {
	if w.Opt.SideScrollOff >= 0 {
		return w.Opt.SideScrollOff
	}
	if o == nil {
		return 0
	}
	return o.G.SideScrollOff
}

// DefaultScroll is what 'scroll' resets to: half the window height, rounded
// down, and at least one.
//
// Measured: a 23-row window gives 11. Vim recomputes this whenever the window
// is resized, and a count on CTRL-U or CTRL-D overrides it until the next
// resize.
func DefaultScroll(height int) int {
	n := height / 2
	if n < 1 {
		n = 1
	}
	return n
}

// SetHeight resizes the text area and recomputes 'scroll'.
//
// The recompute is the part that is easy to forget: vim's 'scroll' follows the
// window height on every resize, so a window dragged taller moves further on
// the next CTRL-D even though nobody typed a count. A count typed on CTRL-U or
// CTRL-D sets 'scroll' too, and that value lasts until the next resize, which
// is what this undoes.
func (w *Window) SetHeight(rows int) {
	if w.View.Height == rows && w.Opt.Scroll > 0 {
		// Not a resize, so 'scroll' is left alone. Layout runs on every frame
		// and calls this for every window; recomputing unconditionally would
		// throw away the value a count on CTRL-U set, one redraw later, and
		// the fuzzer would find it as a CTRL-U that moved half a window when
		// the last one moved five lines.
		return
	}
	w.View.Height = rows
	w.Opt.Scroll = DefaultScroll(rows)
}
