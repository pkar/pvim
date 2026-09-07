package main

import (
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/textobj"
	"github.com/pkar/pvim/internal/tui"
	"github.com/pkar/pvim/internal/window"
)

// What the mouse does, for both frontends at once.
//
// 'mouse' is "a" in the vimrc and in vim's own defaults, and until this file
// existed both frontends turned the mouse ON -- internal/tui writes the enable
// sequence, internal/gui delivers press, drag and wheel -- and then dropped
// every report on the floor, because neither event switch had a case for one.
//
// The two frontends declare their own MouseEvent, identically, and neither may
// import the other; this is where the two become one so that the editor sees a
// mouse and not a platform.
//
// # How every rule below was measured
//
// Vim decodes SGR mouse reports out of a "-s" script and out of a pty, so the
// mouse has the same byte oracle every other key in this editor has. The
// harness is a pty with vim 9.2.0321 on the far end of it, a keystroke file
// carrying \x1b[<0;COL;ROWM and friends, and a <Cmd> mapping that writes
// mode(1), getpos("."), getpos("v"), winsaveview(), winlayout() and the window
// sizes to a file. Timing matters for 'mousetime' and vim coalesces the drag
// reports that are already queued when it looks, so the cases with a clock in
// them are written a report at a time with a real pause between, which is what
// a hand does anyway.
//
// Every number and every rule in this file came back from that harness, and
// the ones that surprised somebody are written down where they are used. The three that surprised everybody:
//
// - the click count cycles 1, 2, 3, 4, 1 rather than saturating, so a fifth
// click in one place is a plain click again;
// - a drag past the bottom edge scrolls one line per report and a drag past
// the top edge scrolls one line per row of overshoot, and no timer scrolls
// while the pointer is held still;
// - Shift and the wheel is a PAGE in vim and not a sideways scroll. The
// sideways scroll is its own report -- xterm buttons 6 and 7 -- and on
// macOS internal/gui is what turns a shift-wheel into one, because that
// swap is the platform's convention and not the editor's. See wheel.
//
// # What is not here
//
// The right button. 'mousemodel' is "popup_setpos" under --clean and under this
// vimrc, which makes the right button a context menu, and there is no menu.
//
// A double click with an operator pending. Measured: vim applies the operator
// to the WORD under the pointer there, the same run of one character class a
// double click selects, and a triple click gives it the line. This treats
// every press with an operator pending as the position it is, whatever the
// click counter says, and resets the counter so the click after it is a plain
// one. mode.MotionToPos takes a destination and not a range, which is the
// narrow seam this needed; a word-shaped one would be a second entry
// point for the two clicks in a hundred that are a double click typed after an
// operator.

// mouseAction is what the mouse did, in this program's own vocabulary.
type mouseAction uint8

const (
	mouseOther mouseAction = iota
	mousePress
	mouseRelease
	mouseDrag
	mouseWheelUp
	mouseWheelDown
	mouseWheelLeft
	mouseWheelRight
)

// mouseAt is one mouse report, in grid cells.
//
// At is when the report happened, which is what the click counter measures
// against 'mousetime'. A zero At means "now": the frontends do not stamp their
// events, and a test that wants to drive the clock fills it in.
type mouseAt struct {
	action mouseAction
	left   bool
	row    int
	col    int
	mod    key.Mod
	at     time.Time
}

// guiMouse converts a window mouse report.
func guiMouse(ev gui.MouseEvent) mouseAt {
	m := mouseAt{row: ev.Row, col: ev.Col, left: ev.Button == gui.MouseLeft, mod: ev.Mod}
	switch ev.Action {
	case gui.MousePress:
		m.action = mousePress
	case gui.MouseRelease:
		m.action = mouseRelease
	case gui.MouseDrag:
		m.action = mouseDrag
	case gui.MouseWheelUp:
		m.action = mouseWheelUp
	case gui.MouseWheelDown:
		m.action = mouseWheelDown
	case gui.MouseWheelLeft:
		m.action = mouseWheelLeft
	case gui.MouseWheelRight:
		m.action = mouseWheelRight
	}
	return m
}

// tuiMouse converts a terminal mouse report.
func tuiMouse(ev tui.MouseEvent) mouseAt {
	m := mouseAt{row: ev.Row, col: ev.Col, left: ev.Button == tui.MouseLeft, mod: ev.Mod}
	switch ev.Action {
	case tui.MousePress:
		m.action = mousePress
	case tui.MouseRelease:
		m.action = mouseRelease
	case tui.MouseDrag:
		m.action = mouseDrag
	case tui.MouseWheelUp:
		m.action = mouseWheelUp
	case tui.MouseWheelDown:
		m.action = mouseWheelDown
	case tui.MouseWheelLeft:
		m.action = mouseWheelLeft
	case tui.MouseWheelRight:
		m.action = mouseWheelRight
	}
	return m
}

// wheelLines is how far one wheel notch scrolls and wheelCols how far one
// sideways notch does.
//
// Vim's 'mousescroll' would name both, and this vim does not have the option:
// ":set mousescroll?" is E518 on 9.2.0321, so three and six are the built-in
// numbers and there is nothing to read them from. Measured: one notch down
// moves 'topline' by three, one notch right moves 'leftcol' by six.
const (
	wheelLines = 3
	wheelCols  = 6
)

// mouseTime is 'mousetime': how long after one click a second one is a double
// click. Vim's default is 500ms, this vimrc does not change it, and it is a
// constant here rather than an option because internal/options has no field
// for 'mousetime' yet -- ":set mousetime=300" is E518 in this editor.
// The day it grows one, this is the only line that reads it.
const mouseTime = 500 * time.Millisecond

// maxClicks is where vim's click counter wraps.
//
// Measured, clicking one cell over and over inside 'mousetime': the second
// click selects a word, the third a line, the fourth a block, and the FIFTH
// places the cursor again. It is a cycle and not a ceiling, which is vim's
// check_multiclick refusing to count past four, and it is the reason a person
// who clicks five times in a hurry ends up in normal mode rather than stuck in
// a block selection.
const maxClicks = 4

// mouseState is everything the mouse remembers between reports.
//
// It would rather be a field on editor. This file does not own editor.go, so
// it is a package-level slot keyed by the editor it belongs to, reset whenever
// a report arrives for a different one -- there is one editor per process and
// the tests build one at a time, so the slot is never contended in practice
// and the mutex is there for the case where two tests are.
type mouseState struct {
	// clicks is the click count vim's check_multiclick keeps: 1 to 4, cycling.
	clicks int
	// at, row and col are the last press, which the next one is timed and
	// placed against. A press in a different cell restarts the count, measured.
	at       time.Time
	row, col int

	// drag is the window a press landed in, and nil when no button is down.
	// The window is remembered rather than looked up per report because a drag
	// that leaves the window still belongs to it: vim scrolls the window the
	// press was in, it does not hand the selection to the window the pointer
	// happens to be over.
	drag *window.Window
	// kind is how the drag extends: 1 by character, 2 by word, 3 by line and
	// 4 by block, which is the click count that started it. Measured: a drag
	// after a double click extends word by word and after a triple click it
	// stays linewise.
	kind int
	// anchor and anchorEnd are the two ends of what the click selected before
	// the drag started. A charwise drag uses anchor alone; a word drag swings
	// between the two, so that dragging left off a double-clicked word anchors
	// on the word's right-hand end.
	anchor, anchorEnd text.Pos

	// live is the anchor the mode machine is holding right now, so that a drag
	// only re-enters visual mode when the anchor actually moved. See
	// selectRange.
	live text.Pos

	// sep is a separator or status line being dragged, and nil the rest of the
	// time. Vertical says the drag moves a column rather than a row.
	sep      *window.Window
	sepVert  bool
	sepOwner *window.TabPage
}

// mice is the one mouseState there is. See mouseState for why it lives here.
var mice struct {
	sync.Mutex
	ed *editor
	st mouseState
}

// mouseState returns this editor's mouse memory, empty the first time.
func (e *editor) mouseState() *mouseState {
	mice.Lock()
	defer mice.Unlock()
	if mice.ed != e {
		mice.ed, mice.st = e, mouseState{}
	}
	return &mice.st
}

// mouse applies one report.
func (e *editor) mouse(m mouseAt) {
	if e.opt == nil || e.opt.G.Mouse == "" {
		return
	}
	if m.at.IsZero() {
		m.at = time.Now()
	}
	switch m.action {
	case mouseWheelUp, mouseWheelDown, mouseWheelLeft, mouseWheelRight:
		e.wheel(m)
	case mousePress:
		if m.left {
			e.press(m)
		}
	case mouseDrag:
		if m.left {
			e.dragTo(m)
		}
	case mouseRelease:
		// The button coming up ends a drag and nothing else: the selection
		// stays up, measured -- a drag that has made a visual selection is
		// still in visual mode after the release, so the next key operates on
		// it, which is the whole point of selecting with the mouse.
		st := e.mouseState()
		st.drag, st.sep, st.sepOwner = nil, nil, nil
	}
}

// wheel is one notch, in whichever direction and with whatever was held down.
//
// Vim's table, from scroll.txt and confirmed a line at a time on this machine:
//
//	<ScrollWheelUp> three lines up <S-...>, <C-...> one page up
//	<ScrollWheelDown> three lines down <S-...>, <C-...> one page down
//	<ScrollWheelLeft> six columns left <S-...>, <C-...> one page left
//	<ScrollWheelRight> six columns right <S-...>, <C-...> one page right
//
// So Shift and the wheel is a PAGE in vim, and not the sideways scroll the
// macOS gesture makes it look like. Both are true at once because they are
// about different events: on macOS a shift-wheel IS a horizontal scroll gesture, the
// window server says so, and internal/gui's scrollState swaps the axes before
// the event is built, so a shift-wheel in the window arrives here as
// mouseWheelLeft or mouseWheelRight with no modifier on it and scrolls six
// columns under 'nowrap'. A shift-wheel in the TERMINAL arrives as a vertical
// notch with Shift set, because that is what the terminal reports and what vim
// itself would do with it, and it pages. The difference is a platform's, not
// this editor's, and it is registered here rather than papered over.
//
// The window scrolled is the one under the pointer, which is not necessarily
// the current one. Measured with a ":vsplit": a notch over the other window
// moves that window's 'topline' and leaves the cursor and the current window
// alone, so the wheel reads the pointer and never the focus.
func (e *editor) wheel(m mouseAt) {
	w, _, _, where := e.winAt(m.row, m.col)
	if w == nil || where != inText {
		// Over the tabline, the command line or a separator. Vim scrolls the
		// current window for a notch that is not over any window at all, which
		// is what a wheel over the command line does.
		w = e.sess.win()
	}
	if w == nil {
		return
	}
	page := m.mod&(key.ModShift|key.ModCtrl) != 0
	current := w == e.sess.win()

	switch m.action {
	case mouseWheelUp, mouseWheelDown:
		cmd := window.LineUp
		count := wheelLines
		if page {
			cmd, count = window.PageBack, 1
		}
		if m.action == mouseWheelDown {
			cmd = window.LineDown
			if page {
				cmd = window.PageForward
			}
		}
		if current {
			w.View.Cursor = e.ed.Cursor()
		}
		w.Scroll(cmd, count, e.sess.so)
		if current {
			e.ed.SetCursor(w.View.Cursor)
		}
	case mouseWheelLeft, mouseWheelRight:
		// Sideways scrolling does nothing under 'wrap', which is vim and which
		// window.SideScroll already refuses. The vimrc sets 'nowrap'.
		cmd, count := window.SideLeft, wheelCols
		if page {
			cmd, count = window.SideHalfLeft, 2
		}
		if m.action == mouseWheelRight {
			cmd = window.SideRight
			if page {
				cmd = window.SideHalfRight
			}
		}
		if current {
			w.View.Cursor = e.ed.Cursor()
		}
		w.SideScroll(cmd, count, e.sess.side())
		if current {
			e.ed.SetCursor(w.View.Cursor)
		}
	}
	e.sess.publish()
}

// press is a left button going down.
func (e *editor) press(m mouseAt) {
	st := e.mouseState()
	st.drag, st.sep, st.sepOwner = nil, nil, nil

	if !e.regions.Tabline.Empty() && m.row == e.regions.Tabline.Row {
		st.clicks = 0
		e.tablineClick(m.col)
		return
	}

	w, row, col, where := e.winAt(m.row, m.col)
	if w == nil {
		// The command line, or a screen with no windows on it. Vim does
		// nothing with either.
		st.clicks = 0
		return
	}
	if where != inText {
		// A window's status line or its vertical separator: the start of a
		// resize. Measured, a press there and no drag changes nothing at all,
		// not even the current window, so this only remembers.
		st.clicks = 0
		st.sep, st.sepVert, st.sepOwner = w, where == onSeparator, e.tabs.Current()
		return
	}
	if !e.mouseUsable() {
		return
	}

	// An operator waiting for a motion, and a press in the window it was typed
	// in: the press IS the motion. Everywhere else the half-typed command is
	// thrown away, which is vim's clearop and is what "3" then a click then
	// "x" measures.
	if e.ed.PendingOperator() && w == e.sess.win() {
		st.clicks = 0
		e.operatorClick(w, row, col)
		return
	}
	e.ed.ClearOperator()
	// And the frontend's own half-typed commands, which live here rather than
	// in the mode machine: the "g" of "gt", the z family and CTRL-W. Vim throws
	// the lot away on a click, so "g", click, "x" deletes one character; without
	// this it beeped, because the g was still waiting for its second key.
	e.sess.clearPrefixes()

	st.clicks = st.count(m)
	st.drag, st.kind = w, st.clicks

	if !e.focusWindow(w) {
		st.drag = nil
		return
	}
	pos := e.posAt(w, row, col)
	switch st.clicks {
	case 1:
		e.leaveVisual()
		e.ed.SetCursor(pos)
		st.anchor, st.anchorEnd = e.ed.Cursor(), e.ed.Cursor()
	case 2:
		start, end := e.wordAt(w, pos)
		st.anchor, st.anchorEnd = start, end
		e.selectRange(mode.VisualChar, start, end)
	case 3:
		st.anchor, st.anchorEnd = pos, pos
		e.selectRange(mode.VisualLine, pos, pos)
	case 4:
		st.anchor, st.anchorEnd = pos, pos
		e.selectRange(mode.VisualBlock, pos, pos)
	}
	e.sess.sync()
}

// operatorClick is a press in the current window with an operator waiting for
// a motion: the position under the pointer IS the motion, charwise and
// exclusive.
//
// Measured against vim 9.2.0321 through a pty with 'ttymouse' sgr, the file
// "alpha bravo charlie / delta echo foxtrot / golf hotel india / juliet kilo
// lima / mike november oscar / papa quebec romeo" and the cursor at line 3
// column 3,. "d" and a click at row 5 column 5 deletes forwards
// to the pointer and leaves the cursor where the "d" was typed; the same click
// at row 2 column 2 deletes backwards and leaves the cursor at the pointer;
// the same click on the cursor's own cell is an empty region, which deletes
// nothing and does not even write a register. "c" and "y" take the same text,
// "y" leaves the cursor at the start of it, ">" shifts the lines the span
// touches, and the "c" that lands on its own cell still opens insert mode. The
// rules and the two clamps are written up in internal/mode/position.go, which
// is the seam this calls.
//
// Nothing is armed for a drag. The press has been spent on the operator, and
// vim's own answer here -- a selection anchored at a position the operator has
// just deleted out from under -- is not worth reproducing from a guess.
func (e *editor) operatorClick(w *window.Window, row, col int) {
	if err := e.ed.MotionToPos(e.opPosAt(w, row, col), motion.KindCharExclusive); err != nil {
		if msg := errorMessage(err, ""); msg != "" {
			e.ed.Say(msg)
		}
	}
	e.sess.sync()
}

// dragTo is the pointer moving with the left button down.
//
// Three things can be dragging and they are told apart by what the press
// landed on, not by where the pointer is now: a separator resizes, a press in
// the text extends a selection, and a press anywhere else does nothing.
func (e *editor) dragTo(m mouseAt) {
	st := e.mouseState()
	if st.sep != nil {
		e.dragSeparator(st, m)
		return
	}
	w := st.drag
	if w == nil || !e.mouseUsable() {
		return
	}
	if w != e.sess.win() {
		// The window went away under the drag -- a ":q" from a mapping, a
		// buffer swap -- and a selection into a window that is not current
		// would be drawn in the wrong place.
		st.drag = nil
		return
	}

	// The vertical overshoot, in rows, of the pointer past the window's text.
	// Measured with a ":split" so that both edges are reachable: with the
	// window 11 rows tall, a drag one row above it scrolls one line, two rows
	// above scrolls two, and one row below scrolls one, so the rule is the
	// distance past the edge and vim's own arithmetic is symmetric. Each
	// REPORT scrolls once: holding the pointer still outside the window
	// scrolls nothing, because vim has no timer here and neither has this.
	row := m.row - w.Rect.Row
	switch {
	case row < 0:
		w.Scroll(window.LineUp, -row, 0)
		row = 0
	case row >= w.View.Height:
		w.Scroll(window.LineDown, row-w.View.Height+1, 0)
		row = w.View.Height - 1
	}
	// Sideways there is no scroll of its own: the pointer's column is a
	// display column measured from 'leftcol', it is allowed to be off the
	// right-hand edge, and the ordinary curs_columns rule in session.sync is
	// what brings the window across. Measured in a 40-column ":vsplit" whose
	// 'leftcol' was 39: a drag one column past the edge left the cursor on
	// display column 79 and 'leftcol' on 59, which is exactly what a "|"
	// motion to the same column does.
	col := m.col - w.Rect.Col
	pos := e.posAt(w, row, col)

	switch st.kind {
	case 3:
		e.selectRange(mode.VisualLine, st.anchor, pos)
	case 4:
		e.selectRange(mode.VisualBlock, st.anchor, pos)
	case 2:
		// Word by word, swinging on whichever end of the double-clicked word
		// is away from the pointer. Measured: double click "eeee" on line 2
		// and drag back into "aaaa" on line 1 and the selection runs from the
		// END of "eeee" to the START of "aaaa".
		start, end := e.wordAt(w, pos)
		if pos.Compare(st.anchor) < 0 {
			e.selectRange(mode.VisualChar, st.anchorEnd, start)
		} else {
			e.selectRange(mode.VisualChar, st.anchor, end)
		}
	default:
		if pos == st.anchor && e.ed.Mode() == mode.Normal {
			// A drag that has not left the cell the press was in has not
			// started a selection yet. Measured: press and drag inside one
			// cell leaves '< and '> untouched, so vim never entered visual
			// mode at all.
			return
		}
		e.selectRange(mode.VisualChar, st.anchor, pos)
	}
	e.mouseSync()
}

// mouseSync is session.sync without the vertical cursor correction.
//
// A drag places the window itself and 'scrolloff' must not then move it again.
// Measured with 'scrolloff' 5 and the window at line 25: a CLICK on the top
// visible row scrolls up to line 20, which is the option doing its job, and a
// DRAG onto the row below the window scrolls exactly one line and leaves the
// cursor on the last line on screen, where the option would have moved the
// window five more. So a press syncs and a drag does not, and the sideways
// half runs either way because a drag past the right-hand edge is corrected by
// exactly the rule a "|" motion to the same column is.
//
// One measured case is not reproduced and is registered here rather than
// guessed at: a drag onto the TOP visible row of a scrolled window scrolls vim
// up by one line with the cursor left on the line that was on that row, which
// is neither the overshoot rule nor 'scrolloff'. This scrolls nothing there.
func (e *editor) mouseSync() {
	if e.sess.relayout != nil {
		e.sess.relayout()
	}
	w := e.sess.win()
	if w == nil {
		return
	}
	w.View.Cursor = e.ed.Cursor()
	w.SideScrollToCursor(e.sess.side())
	e.sess.publish()
}

// count is the click count this press makes, per vim's check_multiclick.
//
// Measured on 9.2.0321 through a pty: two clicks 400ms apart are a double
// click and two 700ms apart are two single clicks, with 'mousetime' at its
// default of 500; at "mousetime=1000" the 700ms pair is a double click and at
// "mousetime=100" the 200ms pair is not. A click in a different row or a
// different column restarts the count whatever the gap was.
func (s *mouseState) count(m mouseAt) int {
	n := 1
	if s.clicks > 0 && s.clicks < maxClicks && s.row == m.row && s.col == m.col &&
		m.at.Sub(s.at) < mouseTime {
		n = s.clicks + 1
	}
	s.at, s.row, s.col = m.at, m.row, m.col
	return n
}

// where a screen cell falls inside a window's rectangle.
type mouseWhere uint8

const (
	// outside is a cell no window's rectangle covers.
	outside mouseWhere = iota
	// inText is the window's text area.
	inText
	// onStatus is the status line under a window, which drags its height.
	onStatus
	// onSeparator is the divider column beside a window, which drags its
	// width.
	onSeparator
)

// winAt finds the window a screen cell belongs to.
//
// A window's Rect covers its text plus the status line under it and the
// separator column beside it, and View.Height by View.Width is the text alone,
// which is what makes the three cases below a subtraction rather than a search.
// The row and column returned are relative to the text area's top left and are
// only meaningful for inText.
func (e *editor) winAt(row, col int) (*window.Window, int, int, mouseWhere) {
	tab := e.tabs.Current()
	if tab == nil {
		return nil, 0, 0, outside
	}
	for _, w := range tab.Windows() {
		r := w.Rect
		if row < r.Row || row >= r.Row+r.Rows || col < r.Col || col >= r.Col+r.Cols {
			continue
		}
		dr, dc := row-r.Row, col-r.Col
		switch {
		case dc >= w.View.Width:
			return w, dr, dc, onSeparator
		case dr >= w.View.Height:
			return w, dr, dc, onStatus
		default:
			return w, dr, dc, inText
		}
	}
	return nil, 0, 0, outside
}

// posAt turns a cell inside a window's text area into a buffer position.
//
// Under 'nowrap', which the vimrc sets, there is one screen row per buffer
// line and the row is a subtraction. Under 'wrap' a long line takes several
// rows and this lands on the wrong line; internal/screen owns the row-to-line
// map and does not hand it out, so the case is named here rather than being
// silently wrong.
//
// The column is a display column, because a tab before the pointer is one cell
// here and several on screen. Landing in the middle of a tab gives the tab's
// own byte, because there is nowhere else for a cursor to be, and a column
// past the end of the line gives the last byte, which is where vim's own click
// lands: measured, a click on column 30 of a 14-character line leaves the
// cursor in column 14.
func (e *editor) posAt(w *window.Window, row, col int) text.Pos {
	line := w.View.TopLine + row
	if line < 1 {
		line = 1
	}
	if n := w.Buf.LineCount(); line > n {
		line = n
	}
	src := w.Buf.Line(line)
	disp := col + w.View.LeftCol
	return text.Pos{Line: line, Col: lastCol(src, text.ByteColForDisplay(src, disp, e.opt.B.TabStop))}
}

// opPosAt is posAt for a click that is completing an operator, which lands one
// column further right at the end of a line.
//
// The two clamps really are different, measured on "juliet kilo lima" with the
// cursor at line 3 column 3: a click at column 60 with nothing pending leaves
// the cursor on the final "a", and the same click with "d" pending deletes the
// whole of "lima", so its exclusive end is the column AFTER the last
// character. That is the operator-pending cursor allowance, the one that also
// lets "$" take the last byte of a line, and it is why this stops at len(line)
// where posAt walks back onto the last character.
func (e *editor) opPosAt(w *window.Window, row, col int) text.Pos {
	p := e.posAt(w, row, col)
	src := w.Buf.Line(p.Line)
	p.Col = text.ByteColForDisplay(src, col+w.View.LeftCol, e.opt.B.TabStop)
	return p
}

// lastCol holds a byte column on the last character of a line rather than one
// past it, which is where a normal-mode cursor lives and where vim's own click
// lands. An empty line is column zero and stays there.
func lastCol(line []byte, col int) int {
	if col < len(line) {
		return col
	}
	return prevRuneCol(line, len(line))
}

// wordAt is what a double click selects: the run of one character class around
// a position, which is vim's get_mouse_class and is the same run "iw" takes.
//
// Measured on "aaaa bbbb cccc": a double click inside "bbbb" selects columns 6
// to 9, one on the single space between the words selects that space alone,
// and one on the "." of "foo.bar" selects the dot alone. That is three classes
// -- blank, keyword per 'iskeyword', and everything else -- and it is exactly
// what internal/textobj answers for "iw", so this asks it rather than writing
// the scan a second time.
//
// The two ends come back inclusive, which is what a visual selection wants;
// textobj's charwise range is half open.
func (e *editor) wordAt(w *window.Window, pos text.Pos) (start, end text.Pos) {
	obj, ok := textobj.ByKey('w')
	if !ok {
		return pos, pos
	}
	opt := textobj.DefaultOptions()
	if e.opt != nil {
		opt.IsKeyword = e.opt.B.IsKeyword
		opt.TabStop = e.opt.B.TabStop
	}
	res := obj.Find(textobj.Request{
		Buf: w.Buf, At: pos, Count: 1, Inner: true,
		Sel: text.Range{Start: pos, End: pos}, HasSel: true,
		Opt: opt,
	})
	if res.Type != register.TypeChar || res.Range.End.Line != res.Range.Start.Line ||
		res.Range.End.Col <= res.Range.Start.Col {
		return pos, pos
	}
	start = res.Range.Start
	end = text.Pos{Line: start.Line, Col: prevRuneCol(w.Buf.Line(start.Line), res.Range.End.Col)}
	return start, end
}

// prevRuneCol is the byte column of the rune before i, which turns textobj's
// half-open end into the inclusive one a selection wants.
func prevRuneCol(line []byte, i int) int {
	if i <= 0 {
		return 0
	}
	j := i - 1
	for j > 0 && line[j]&0xc0 == 0x80 {
		j--
	}
	return j
}

// selectRange puts the editor in a visual mode with the two ends given.
//
// The anchor goes in through the key that starts the mode, because
// internal/mode takes its anchor from wherever the cursor is when "v", "V" or
// CTRL-V arrives and offers no other way in. Anything already selected is
// escaped out of first: measured, a click or a drag while a selection is up
// replaces it, and a drag out of visual LINE or visual BLOCK leaves a CHARWISE
// selection behind, so the mode the mouse asks for is the mode that wins.
func (e *editor) selectRange(m mode.Mode, anchor, to text.Pos) {
	st := e.mouseState()
	if e.ed.Mode() != m || st.live != anchor {
		st.live = anchor
		e.leaveMode()
		e.ed.SetCursor(anchor)
		var k key.Key
		switch m {
		case mode.VisualLine:
			k = key.Rune('V')
		case mode.VisualBlock:
			k = key.Ctrl('v')
		default:
			k = key.Rune('v')
		}
		if err := e.ed.Key(k); err != nil {
			return
		}
	}
	e.ed.SetCursor(to)
}

// leaveVisual drops a selection, the way Escape does. A click that is not a
// drag leaves insert mode alone, which is why this and leaveMode are two.
func (e *editor) leaveVisual() {
	switch e.ed.Mode() {
	case mode.VisualChar, mode.VisualLine, mode.VisualBlock:
		_ = e.ed.Key(key.Key{Special: key.KeyEsc})
	}
}

// leaveMode gets back to normal mode from wherever a selection is about to
// start. Insert and replace are in it because a drag from insert mode makes a
// selection: measured, "i" then a press and a drag leaves vim in charwise
// visual with the anchor at the press, and the "v" that starts it would
// otherwise be typed into the buffer.
func (e *editor) leaveMode() {
	switch e.ed.Mode() {
	case mode.Normal:
	default:
		_ = e.ed.Key(key.Key{Special: key.KeyEsc})
	}
}

// mouseUsable reports whether the mode machine is in a state a click means
// something in.
//
// Insert and replace mode are fine: measured, a click in insert mode moves the
// cursor and STAYS in insert, which is 'mouse' having an "i" in it, and a drag
// from insert mode makes a charwise selection.
//
// The one state a click means nothing in is a command holding the next
// keystroke for itself: the target of an f, the name of a mark, the register
// after a quote. Measured, "f" and then a click on another line: the cursor
// does not move and the buffer is untouched.
//
// Everything else goes through, including the half-typed commands this used to
// refuse. An operator is the click's own motion, see press; a count or a "x
// prefix or a lone "g" is thrown away by it, measured -- "3" then a click then
// "x" deletes one character, and so does "g" then a click then "x".
func (e *editor) mouseUsable() bool { return !e.ed.Waiting() }

// focusWindow makes w the current window, and reports whether it could.
//
// Measured: a click in another window switches to it and puts the cursor where
// the pointer is, and the window that was left keeps its own cursor.
//
// The buffer follow is done here rather than through ":wincmd w" for one
// reason, and it is a bug in internal/ex and not a preference: Context's
// swapBuffer ends with w.View.TopLine = 1, so every window switch through the
// ex layer scrolls the window being switched to back to the top. A mouse click
// that jumped the window to line 1 and then put the cursor where the pointer
// USED to be would be unusable. What this loses by going around it is
// setRegisterNames, so "% and "# still name the buffer the last ex command
// left them on until the next one runs.
func (e *editor) focusWindow(w *window.Window) bool {
	tab := e.tabs.Current()
	if tab == nil || w == nil {
		return false
	}
	if w == tab.Cur {
		return true
	}
	old, prev := tab.Cur, tab.Prev
	old.View.Cursor = e.ed.Cursor()
	tab.Goto(w)
	if w.Buf != old.Buf {
		b := e.bufFor(w.Buf)
		if b == nil {
			// A window on a buffer the list has never heard of, which nothing
			// makes. Put the current window back rather than editing a
			// buffer this editor cannot name.
			tab.Cur, tab.Prev = old, prev
			return false
		}
		if l := e.ctx.Bufs; l != nil && l.Cur != nil && l.Cur != b {
			l.Cur.Cursor = old.View.Cursor
			l.Alt, l.Cur = l.Cur, b
		}
		e.open(b)
	}
	e.ed.SetCursor(w.View.Cursor)
	return true
}

// bufFor is the buffer-list entry for a text buffer, or nil.
func (e *editor) bufFor(b *text.Buffer) *ex.Buf {
	if e.ctx == nil || e.ctx.Bufs == nil {
		return nil
	}
	for _, x := range e.ctx.Bufs.Bufs {
		if x.Text == b {
			return x
		}
	}
	return nil
}

// tablineClick is a click on the tabline.
//
// Three things are up there and vim answers all three. Measured with three
// tabs open on an 80-column screen: a click on a label goes to that tab; a
// click in the empty space after the last label is "gt", so it goes to the
// NEXT tab and wraps from the last to the first; and a click on the "X" in the
// last column closes the current tab, which is a ":tabclose" with everything
// that command refuses about a modified buffer.
func (e *editor) tablineClick(col int) {
	if e.tabs == nil || len(e.tabs.Pages) < 2 {
		return
	}
	cols := e.regions.Tabline.Cols
	if col == cols-1 {
		e.mouseExec("tabclose")
		return
	}
	for i, span := range e.tabSpans() {
		if col >= span[0] && col < span[1] {
			if i != e.tabs.Cur {
				// ":3tabnext" and not ":tabnext 3": internal/ex takes a tab
				// number as the command's range and reads the argument form
				// as a bare "gt".
				e.mouseExec(strconv.Itoa(i+1) + "tabnext")
			}
			return
		}
	}
	e.mouseExec("tabnext")
}

// mouseExec runs an ex command on the mouse's behalf.
//
// It is session.runLine without the newline that opens a typed ":" line: vim
// prints nothing when a tab is clicked, and an editor that cleared the message
// line every time somebody switched tab with the pointer would lose the answer
// to whatever they had just asked. The E-codes still come through, which is
// what a click on the X over a modified buffer needs -- that is E37 and it has
// to be readable.
func (e *editor) mouseExec(line string) {
	if e.ctx == nil {
		return
	}
	if err := e.ctx.RunLine(line); err != nil {
		if msg := errorMessage(err, line); msg != "" {
			e.ed.Say(msg)
		}
	}
	e.sess.sync()
}

// tabSpans is the half-open column range each tab's label occupies.
//
// It is internal/screen's drawTabline arithmetic, and it is here because that
// package draws the label and does not say where it put it. TestTabSpans in
// mouse_test.go renders a real tabline for every layout it can think of and
// fails if these spans and the highlighted cells of the drawn grid disagree,
// so the duplication is checked by a machine rather than by whoever remembers.
// The right fix is one exported function on internal/screen; this is what can
// be done from here.
func (e *editor) tabSpans() [][2]int {
	cols := e.regions.Tabline.Cols
	pages := e.tabs.Pages
	count := len(pages)
	tabWidth := 6
	if n := (cols - 1 + count/2) / count; n > tabWidth {
		tabWidth = n
	}
	names, modified := e.names(), e.modifiedSet()

	out := make([][2]int, 0, count)
	col := 0
	for _, p := range pages {
		if col >= cols-4 {
			break
		}
		start := col
		col++
		wins := p.Windows()
		mod := false
		for _, w := range wins {
			if modified[w.Buf] {
				mod = true
			}
		}
		if mod || len(wins) > 1 {
			if len(wins) > 1 {
				col += len(strconv.Itoa(len(wins)))
			}
			if mod {
				col++
			}
			col++
		}
		name := p.Label
		if name == "" && p.Cur != nil {
			if n, ok := names[p.Cur.Buf]; ok {
				name = n
			} else {
				name = "[No Name]"
			}
		}
		if room := start - col + tabWidth - 1; room > 0 {
			name = tabTail(name, room)
		}
		col += screen.LineWidth([]byte(name), 8)
		col++
		out = append(out, [2]int{start, col})
	}
	return out
}

// dragSeparator resizes a window by moving the divider the press landed on.
//
// Measured with ":vsplit" on an 80-column screen, which gives windows of 40
// and 39 with the divider in column 41: dragging the divider to column 51
// leaves 50 and 29, so the window to the left of the divider ends where the
// pointer is and the one after it gives up the difference. The same with
// ":split" and a status line: 11 and 10 rows, drag the status line from row 12
// to row 16 and they are 15 and 6. A press on the divider with no drag changes
// nothing, and neither drag moves the current window.
func (e *editor) dragSeparator(st *mouseState, m mouseAt) {
	tab := st.sepOwner
	w := st.sep
	if tab == nil || w == nil || tab != e.tabs.Current() {
		return
	}
	dir, want, have := window.Horizontal, m.row-w.Rect.Row, w.View.Height
	if st.sepVert {
		dir, want, have = window.Vertical, m.col-w.Rect.Col, w.View.Width
	}
	if want < 1 || want == have {
		return
	}
	// TabPage.Resize acts on the tab's current window, and this drag must not
	// change which that is: vim leaves the focus where it was while the
	// divider moves. Cur is put back before the relayout so that 'winwidth'
	// and 'winheight' hold the real current window at its minimum and not the
	// one being dragged.
	// The sizes have to exist before they can be moved. internal/window shares
	// a frame out by window count until something has an opinion, and nothing
	// in this editor has ever laid a tab out at the window layer: editor.relayout
	// calls internal/screen's LayoutTree, which shares by count and never reads
	// window.Node.Size. So the window layer is given the same band first, and
	// then the divider is moved. See windowLayout for what this still costs.
	e.windowLayout()
	cur, prev := tab.Cur, tab.Prev
	tab.Cur = w
	ok := tab.Resize(dir, want-have)
	tab.Cur, tab.Prev = cur, prev
	if ok {
		tab.Relayout()
		e.sess.publish()
	}
}

// windowLayout lays the current tab out through internal/window's own Layout,
// over the band the last frame gave the windows.
//
// It exists because the two layout functions in this editor are not the same
// function. internal/screen.LayoutTree, which screen.Render and
// editor.relayout call, shares the band out by window count and never reads
// window.Node.Size; window.TabPage.Layout does read it, and Node.Size is where
// every resize -- CTRL-W +, CTRL-W <, and the divider drag above -- puts its
// answer. So a drag sets the size correctly and the next frame throws it away,
// and dragging a divider in the running editor moves nothing on the screen.
//
// The fix is one function in internal/screen honouring Size, and internal/screen
// is not this file's to change. Until it does, this is the layer the resize is
// real at and the test measures it there.
func (e *editor) windowLayout() {
	tab := e.tabs.Current()
	if tab == nil || e.cols <= 0 {
		return
	}
	r := e.regions
	if r.Text.Rows <= 0 {
		return
	}
	tab.Layout(window.Rect{
		Row:  r.Text.Row,
		Rows: r.Text.Rows + r.Status.Rows,
		Cols: e.cols,
	}, e.opt)
}

// tabTail is internal/screen's tailOf: drop leading characters until a tab
// label fits its cell. It is duplicated for the same reason tabSpans is, and
// checked by the same test.
func tabTail(s string, n int) string {
	for screen.LineWidth([]byte(s), 8) > n && len(s) > 0 {
		_, size := utf8.DecodeRuneInString(s)
		s = s[size:]
	}
	return s
}
