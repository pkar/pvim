package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/tui"
)

// The mouse, from both frontends' vocabularies into one.
//
// Every "measured" in this file means vim 9.2.0321 driven through a pty with
// SGR mouse reports written into its input, the harness mouse.go's comment
// describes. Whether a click lands under the pointer on a real screen is item
// 5 of internal/gui/the manual checks and nothing here can answer it; what these
// tests answer is that the rule the harness reported is the rule this editor
// runs.

// sixtyLines is a file long enough for the window to have somewhere to scroll
// to: the harness's own window is 39 rows.
func sixtyLines() string {
	var b strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&b, "line %02d\n", i)
	}
	return b.String()
}

// mouseEditor is an editor that has drawn one frame, which is what fills in
// the screen regions and the window rectangles a click is measured against.
func mouseEditor(t *testing.T) *editor {
	t.Helper()
	return drawnEditor(t, "aaaa bbbb cccc\ndddd eeee ffff\ngggg hhhh iiii\njjjj kkkk llll\n")
}

// drawnEditor is newTestEditor plus the one frame that places everything.
func drawnEditor(t *testing.T, text string) *editor {
	t.Helper()
	e := newTestEditor(t, text)
	e.mouseState() // claim the shared click slot before the test starts
	e.draw(40, 120)
	return e
}

// click is one whole press and release at a screen cell, stamped now.
func mouseClick(row, col int) []mouseAt {
	return []mouseAt{
		{action: mousePress, left: true, row: row, col: col},
		{action: mouseRelease, left: true, row: row, col: col},
	}
}

// send applies a run of reports in order.
func mouseSend(e *editor, ms ...mouseAt) {
	for _, m := range ms {
		e.mouse(m)
	}
}

// clicksAt applies n clicks in one cell, all inside 'mousetime'.
func clicksAt(e *editor, row, col, n int) {
	base := time.Now()
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * 10 * time.Millisecond)
		e.mouse(mouseAt{action: mousePress, left: true, row: row, col: col, at: at})
		e.mouse(mouseAt{action: mouseRelease, left: true, row: row, col: col, at: at})
	}
}

// selection is the two ends of the live visual selection, in buffer order, and
// the mode it is in. It reads them the way ":'<,'>" does, by leaving visual
// mode, so it is the last thing a test calls.
func selection(e *editor) (mode.Mode, text.Pos, text.Pos) {
	m := e.ed.Mode()
	if err := e.ed.Key(key.Key{Special: key.KeyEsc}); err != nil {
		return m, text.Pos{}, text.Pos{}
	}
	start, end, _ := e.ed.LastVisual()
	return m, start, end
}

// TestAClickMovesTheCursor. Measured against vim 9.2.0321 through a pty on
// four lines: gg, a click on the third row and then IZ<Esc> leaves "Zgggg".
// Before the frontends had a case for a mouse event at all it left "Zaaaa",
// because the report was decoded the whole way and then dropped.
func TestAClickMovesTheCursor(t *testing.T) {
	e := mouseEditor(t)
	top := e.regions.Text.Row
	mouseSend(e, mouseClick(top+2, 1)...)
	if got, want := e.ed.Cursor(), (text.Pos{Line: 3, Col: 1}); got != want {
		t.Errorf("a click on the third text row left the cursor at %+v, want %+v", got, want)
	}
}

// TestAClickPastTheEndOfALineLandsOnTheLastCharacter. Measured: a click on
// column 30 of a 14-character line leaves vim's cursor in column 14.
func TestAClickPastTheEndOfALineLandsOnTheLastCharacter(t *testing.T) {
	e := mouseEditor(t)
	mouseSend(e, mouseClick(e.regions.Text.Row, 29)...)
	if got, want := e.ed.Cursor(), (text.Pos{Line: 1, Col: 13}); got != want {
		t.Errorf("a click past the end of the line left the cursor at %+v, want %+v", got, want)
	}
}

// TestAClickOutsideAnyWindowIsDropped. The command line is not a window and a
// click there does nothing in vim; the nearest buffer line would be a wrong
// answer rather than no answer.
func TestAClickOutsideAnyWindowIsDropped(t *testing.T) {
	e := mouseEditor(t)
	before := e.ed.Cursor()
	for _, at := range []mouseAt{
		{action: mousePress, left: true, row: 39, col: 0},
		{action: mousePress, left: true, row: e.regions.Text.Row, col: 200},
	} {
		e.mouse(at)
		if got := e.ed.Cursor(); got != before {
			t.Errorf("a click at row %d col %d moved the cursor to %+v", at.row, at.col, got)
		}
	}
}

// TestARightClickDoesNotMoveTheCursor. Only the left button places the cursor;
// 'mousemodel' is "popup_setpos" here and vim's right button opens a menu this
// editor does not have.
func TestARightClickDoesNotMoveTheCursor(t *testing.T) {
	e := mouseEditor(t)
	before := e.ed.Cursor()
	e.mouse(mouseAt{action: mousePress, row: e.regions.Text.Row + 2, col: 0})
	if got := e.ed.Cursor(); got != before {
		t.Errorf("a press that was not the left button moved the cursor to %+v", got)
	}
}

// TestTheWheelScrollsThreeLines, which is what a notch does in vim, in every
// terminal and in MacVim: one notch moves 'topline' by three.
//
// The cursor follows with 'scrolloff' applied, which is the one number that
// says the notch went through the ordinary scroll path and not a shortcut.
// Measured on a 60-line file with 'scrolloff' 5: one notch down gives topline
// 4 and the cursor on line 9, and a second gives 7 and line 12.
func TestTheWheelScrollsThreeLines(t *testing.T) {
	e := drawnEditor(t, sixtyLines())
	e.mouse(mouseAt{action: mouseWheelDown})
	if got := e.sess.win().View.TopLine; got != 1+wheelLines {
		t.Errorf("one notch down left the top line at %d, want %d", got, 1+wheelLines)
	}
	if got, want := e.ed.Cursor().Line, 1+wheelLines+e.sess.so; got != want {
		t.Errorf("one notch down left the cursor on line %d, want %d", got, want)
	}
	e.mouse(mouseAt{action: mouseWheelUp})
	if got := e.sess.win().View.TopLine; got != 1 {
		t.Errorf("a notch back up left the top line at %d, want 1", got)
	}
}

// TestShiftAndCtrlWheelPage. Measured: <S-ScrollWheelDown> and
// <C-ScrollWheelDown> are CTRL-F, not a sideways scroll. The sideways scroll
// is its own report; see TestTheSidewaysWheelScrollsSixColumns and the note in
// mouse.go's wheel about where the macOS shift-wheel becomes one.
func TestShiftAndCtrlWheelPage(t *testing.T) {
	for _, mod := range []key.Mod{key.ModShift, key.ModCtrl} {
		e := drawnEditor(t, sixtyLines())
		e.mouse(mouseAt{action: mouseWheelDown, mod: mod})
		if got := e.sess.win().View.TopLine; got <= 1+wheelLines {
			t.Errorf("a %v wheel notch left the top line at %d, want a whole page down", mod, got)
		}
	}
}

// TestTheSidewaysWheelScrollsSixColumns. Measured on 200-character lines in an
// 80-column window with 'nowrap' and 'sidescrolloff' 0: one <ScrollWheelRight>
// moves 'leftcol' from 0 to 6 and a second to 12.
func TestTheSidewaysWheelScrollsSixColumns(t *testing.T) {
	e := drawnEditor(t, strings.Repeat("abcdefghij", 30)+"\n")
	e.opt.GW.Wrap = false
	e.sess.win().Opt.Wrap = false
	e.opt.G.SideScrollOff = 0
	for i, want := range []int{wheelCols, 2 * wheelCols} {
		e.mouse(mouseAt{action: mouseWheelRight})
		if got := e.sess.win().View.LeftCol; got != want {
			t.Fatalf("%d sideways notches left 'leftcol' at %d, want %d", i+1, got, want)
		}
	}
	e.mouse(mouseAt{action: mouseWheelLeft})
	if got := e.sess.win().View.LeftCol; got != wheelCols {
		t.Errorf("a notch back left 'leftcol' at %d, want %d", got, wheelCols)
	}
}

// TestAnEmptyMouseOptionTurnsItOff. ":set mouse=" is how a person gets their
// terminal's own click-to-select back, and an editor that went on acting on
// the reports would take it away again.
func TestAnEmptyMouseOptionTurnsItOff(t *testing.T) {
	e := mouseEditor(t)
	e.opt.G.Mouse = ""
	before := e.ed.Cursor()
	mouseSend(e, mouseClick(e.regions.Text.Row+2, 1)...)
	if got := e.ed.Cursor(); got != before {
		t.Errorf("a click under 'mouse' empty moved the cursor to %+v", got)
	}
}

// TestBothFrontendsMeanTheSameThing. internal/gui and internal/tui declare
// their own MouseEvent and neither may import the other, so the one place the
// two become one is mouse.go; a case added to one converter and not the other
// is a mouse that works in the window and not in the terminal.
func TestBothFrontendsMeanTheSameThing(t *testing.T) {
	for _, pair := range []struct {
		g gui.MouseAction
		u tui.MouseAction
	}{
		{gui.MousePress, tui.MousePress},
		{gui.MouseRelease, tui.MouseRelease},
		{gui.MouseDrag, tui.MouseDrag},
		{gui.MouseWheelUp, tui.MouseWheelUp},
		{gui.MouseWheelDown, tui.MouseWheelDown},
		{gui.MouseWheelLeft, tui.MouseWheelLeft},
		{gui.MouseWheelRight, tui.MouseWheelRight},
	} {
		g := guiMouse(gui.MouseEvent{Action: pair.g, Button: gui.MouseLeft, Row: 4, Col: 7, Mod: key.ModShift})
		u := tuiMouse(tui.MouseEvent{Action: pair.u, Button: tui.MouseLeft, Row: 4, Col: 7, Mod: key.ModShift})
		if g != u {
			t.Errorf("%v is %+v from the window and %+v from the terminal", pair.g, g, u)
		}
		if g.action == mouseOther {
			t.Errorf("%v converts to no action at all", pair.g)
		}
	}
}

// TestADragSelectsCharwise. Measured: a press on row 1 column 3 and a drag to
// row 3 column 8 leaves vim in charwise visual with getpos("v") at [1,3] and
// the cursor at [3,8], and the release does NOT end it.
func TestADragSelectsCharwise(t *testing.T) {
	e := mouseEditor(t)
	top := e.regions.Text.Row
	mouseSend(e,
		mouseAt{action: mousePress, left: true, row: top, col: 2},
		mouseAt{action: mouseDrag, left: true, row: top + 2, col: 7},
		mouseAt{action: mouseRelease, left: true, row: top + 2, col: 7})
	if got := e.ed.Mode(); got != mode.VisualChar {
		t.Fatalf("a drag left the editor in mode %v, want charwise visual", got)
	}
	m, start, end := selection(e)
	if m != mode.VisualChar || start != (text.Pos{Line: 1, Col: 2}) || end != (text.Pos{Line: 3, Col: 7}) {
		t.Errorf("the drag selected %+v..%+v in %v, want [1,2]..[3,7] charwise", start, end, m)
	}
}

// TestADragBackwardsAnchorsOnThePress. Measured the other way round: press on
// row 3 column 8, drag to row 1 column 3, and getpos("v") is [3,8] with the
// cursor at [1,3].
func TestADragBackwardsAnchorsOnThePress(t *testing.T) {
	e := mouseEditor(t)
	top := e.regions.Text.Row
	mouseSend(e,
		mouseAt{action: mousePress, left: true, row: top + 2, col: 7},
		mouseAt{action: mouseDrag, left: true, row: top, col: 2})
	_, start, end := selection(e)
	if start != (text.Pos{Line: 1, Col: 2}) || end != (text.Pos{Line: 3, Col: 7}) {
		t.Errorf("a backwards drag selected %+v..%+v, want [1,2]..[3,7]", start, end)
	}
}

// TestADragInsideOneCellSelectsNothing. Measured: press and drag without
// leaving the cell leaves '< and '> untouched, so vim never entered visual
// mode at all.
func TestADragInsideOneCellSelectsNothing(t *testing.T) {
	e := mouseEditor(t)
	top := e.regions.Text.Row
	mouseSend(e,
		mouseAt{action: mousePress, left: true, row: top, col: 2},
		mouseAt{action: mouseDrag, left: true, row: top, col: 2},
		mouseAt{action: mouseRelease, left: true, row: top, col: 2})
	if got := e.ed.Mode(); got != mode.Normal {
		t.Errorf("a drag that never left its cell left the editor in %v, want normal", got)
	}
}

// TestADragBelowTheWindowScrollsOneLinePerReport.
//
// Measured with a 23-row text window: one drag report onto the row under it
// moves 'topline' by one and puts the cursor on the last visible line, three
// reports move it by three, and holding the pointer still scrolls nothing --
// vim has no timer here and neither has this. 'scrolloff' is not applied: with
// it at 5, the cursor still ends on the last line on screen.
func TestADragBelowTheWindowScrollsOneLinePerReport(t *testing.T) {
	e := drawnEditor(t, sixtyLines())
	w := e.sess.win()
	below := w.Rect.Row + w.View.Height
	e.mouse(mouseAt{action: mousePress, left: true, row: w.Rect.Row + 2, col: 0})
	for i := 1; i <= 3; i++ {
		// A different column each time, because a report in the cell the last
		// one was in is a report the pointer did not move for.
		e.mouse(mouseAt{action: mouseDrag, left: true, row: below, col: i % 2})
		if got, want := w.View.TopLine, 1+i; got != want {
			t.Fatalf("after %d drags below the window 'topline' is %d, want %d", i, got, want)
		}
		if got, want := e.ed.Cursor().Line, w.View.TopLine+w.View.Height-1; got != want {
			t.Fatalf("after %d drags the cursor is on line %d, want the last visible line %d", i, got, want)
		}
	}
}

// TestADragAboveTheWindowScrollsByTheOvershoot.
//
// The top edge is only reachable when something is above the window, which in
// a terminal means a split: a frontend clamps the pointer to the grid, so a
// window at the top of the screen can never be dragged off it. Measured with
// ":split" on a 24-row screen, the lower window 11 rows tall starting at
// screen row 13: one drag onto row 12 scrolls one line, onto row 11 two, onto
// row 5 eight, and the cursor lands on the new top line each time.
func TestADragAboveTheWindowScrollsByTheOvershoot(t *testing.T) {
	for _, c := range []struct{ up, lines int }{{1, 1}, {2, 2}, {8, 8}} {
		e := drawnEditor(t, sixtyLines())
		if err := e.sess.runLine("split"); err != nil {
			t.Fatal(err)
		}
		e.draw(40, 120)
		wins := e.tabs.Current().Windows()
		lower := wins[1]
		e.focusWindow(lower)
		lower.View.TopLine = 30
		e.ed.SetCursor(text.Pos{Line: 32})
		e.draw(40, 120)

		e.mouse(mouseAt{action: mousePress, left: true, row: lower.Rect.Row + 2, col: 0})
		// The press is an ordinary click and 'scrolloff' applies to it, so the
		// drag is measured from where the press left the window and not from
		// where the test put it.
		top := lower.View.TopLine
		e.mouse(mouseAt{action: mouseDrag, left: true, row: lower.Rect.Row - c.up, col: 1})
		if got, want := lower.View.TopLine, top-c.lines; got != want {
			t.Errorf("a drag %d rows above the window left 'topline' at %d, want %d", c.up, got, want)
		}
		if got, want := e.ed.Cursor().Line, lower.View.TopLine; got != want {
			t.Errorf("a drag %d rows above left the cursor on line %d, want the top line %d", c.up, got, want)
		}
	}
}

// TestDoubleClickSelectsAWord.
//
// Measured on "aaaa bbbb cccc": a double click inside "bbbb" selects columns 6
// to 9, one on the single space between two words selects that space alone,
// and on "foo.bar" one on the "." selects the dot alone. Three character
// classes, which is vim's get_mouse_class and the same run "iw" takes.
func TestDoubleClickSelectsAWord(t *testing.T) {
	for _, c := range []struct {
		name       string
		text       string
		col        int
		start, end int
	}{
		{"inside a word", "aaaa bbbb cccc\n", 6, 5, 8},
		{"first column", "aaaa bbbb cccc\n", 0, 0, 3},
		{"one space", "aaaa bbbb cccc\n", 4, 4, 4},
		{"punctuation on its own", "foo.bar baz\n", 3, 3, 3},
		{"past the end of the line", "aaaa bbbb cccc\n", 29, 10, 13},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := drawnEditor(t, c.text)
			clicksAt(e, e.regions.Text.Row, c.col, 2)
			m, start, end := selection(e)
			want0, want1 := text.Pos{Line: 1, Col: c.start}, text.Pos{Line: 1, Col: c.end}
			if m != mode.VisualChar || start != want0 || end != want1 {
				t.Errorf("a double click selected %+v..%+v in %v, want %+v..%+v charwise",
					start, end, m, want0, want1)
			}
		})
	}
}

// TestTripleClickSelectsALine and TestQuadrupleClickSelectsABlock. Measured:
// mode(1) answers "V" after three clicks and CTRL-V after four.
func TestTripleClickSelectsALine(t *testing.T) {
	e := mouseEditor(t)
	clicksAt(e, e.regions.Text.Row+1, 6, 3)
	if got := e.ed.Mode(); got != mode.VisualLine {
		t.Fatalf("three clicks left the editor in %v, want visual line", got)
	}
	m, start, end := selection(e)
	if start.Line != 2 || end.Line != 2 {
		t.Errorf("three clicks on row 2 selected lines %d..%d in %v, want 2..2", start.Line, end.Line, m)
	}
}

func TestQuadrupleClickSelectsABlock(t *testing.T) {
	e := mouseEditor(t)
	clicksAt(e, e.regions.Text.Row+1, 6, 4)
	if got := e.ed.Mode(); got != mode.VisualBlock {
		t.Errorf("four clicks left the editor in %v, want visual block", got)
	}
}

// TestTheFifthClickIsAPlainClickAgain. Measured: the count cycles 1, 2, 3, 4,
// 1, so a fifth click in one place places the cursor and leaves visual mode
// rather than staying stuck in a block selection.
func TestTheFifthClickIsAPlainClickAgain(t *testing.T) {
	e := mouseEditor(t)
	clicksAt(e, e.regions.Text.Row+1, 6, 5)
	if got := e.ed.Mode(); got != mode.Normal {
		t.Errorf("five clicks left the editor in %v, want normal", got)
	}
	if got, want := e.ed.Cursor(), (text.Pos{Line: 2, Col: 6}); got != want {
		t.Errorf("the fifth click left the cursor at %+v, want %+v", got, want)
	}
	e = mouseEditor(t)
	clicksAt(e, e.regions.Text.Row+1, 6, 6)
	if got := e.ed.Mode(); got != mode.VisualChar {
		t.Errorf("a sixth click left the editor in %v, want charwise visual", got)
	}
}

// TestMousetimeSeparatesTwoClicks. Measured against 'mousetime': two clicks
// 400ms apart are a double click and two 700ms apart are two single clicks,
// at the default of 500.
func TestMousetimeSeparatesTwoClicks(t *testing.T) {
	for _, c := range []struct {
		gap  time.Duration
		want mode.Mode
	}{
		{400 * time.Millisecond, mode.VisualChar},
		{700 * time.Millisecond, mode.Normal},
	} {
		e := mouseEditor(t)
		base := time.Now()
		row, col := e.regions.Text.Row, 6
		e.mouse(mouseAt{action: mousePress, left: true, row: row, col: col, at: base})
		e.mouse(mouseAt{action: mouseRelease, left: true, row: row, col: col, at: base})
		e.mouse(mouseAt{action: mousePress, left: true, row: row, col: col, at: base.Add(c.gap)})
		if got := e.ed.Mode(); got != c.want {
			t.Errorf("two clicks %v apart left the editor in %v, want %v", c.gap, got, c.want)
		}
	}
}

// TestAClickInAnotherCellRestartsTheCount. Measured: a second click one column
// over is a single click however fast it was.
func TestAClickInAnotherCellRestartsTheCount(t *testing.T) {
	for _, c := range []struct{ drow, dcol int }{{0, 1}, {1, 0}} {
		e := mouseEditor(t)
		base := time.Now()
		row, col := e.regions.Text.Row, 6
		e.mouse(mouseAt{action: mousePress, left: true, row: row, col: col, at: base})
		e.mouse(mouseAt{action: mousePress, left: true, row: row + c.drow, col: col + c.dcol, at: base})
		if got := e.ed.Mode(); got != mode.Normal {
			t.Errorf("a second click %d,%d away left the editor in %v, want normal", c.drow, c.dcol, got)
		}
	}
}

// TestADragAfterADoubleClickExtendsByWord.
//
// Measured: double click "eeee" on line 2 of "aaaa bbbb cccc / dddd eeee ffff"
// and drag back into "aaaa" on line 1, and the selection runs from the END of
// "eeee" to the START of "aaaa"; dragging forwards instead runs from the start
// of "eeee" to the end of the word under the pointer.
func TestADragAfterADoubleClickExtendsByWord(t *testing.T) {
	top := 0
	back := func(t *testing.T) (*editor, int) {
		e := mouseEditor(t)
		top = e.regions.Text.Row
		clicksAt(e, top+1, 6, 1)
		return e, top
	}

	e, top := back(t)
	e.mouse(mouseAt{action: mousePress, left: true, row: top + 1, col: 6})
	e.mouse(mouseAt{action: mouseDrag, left: true, row: top, col: 1})
	_, start, end := selection(e)
	if start != (text.Pos{Line: 1, Col: 0}) || end != (text.Pos{Line: 2, Col: 8}) {
		t.Errorf("dragging back from a double click selected %+v..%+v, want [1,0]..[2,8]", start, end)
	}

	e, top = back(t)
	e.mouse(mouseAt{action: mousePress, left: true, row: top + 1, col: 6})
	e.mouse(mouseAt{action: mouseDrag, left: true, row: top + 2, col: 1})
	_, start, end = selection(e)
	if start != (text.Pos{Line: 2, Col: 5}) || end != (text.Pos{Line: 3, Col: 3}) {
		t.Errorf("dragging on from a double click selected %+v..%+v, want [2,5]..[3,3]", start, end)
	}
}

// TestADragAfterATripleClickStaysLinewise. Measured: mode(1) still answers "V"
// after the drag, and CTRL-V after a quadruple click and a drag.
func TestADragAfterATripleClickStaysLinewise(t *testing.T) {
	for _, c := range []struct {
		clicks int
		want   mode.Mode
	}{{3, mode.VisualLine}, {4, mode.VisualBlock}} {
		e := mouseEditor(t)
		top := e.regions.Text.Row
		clicksAt(e, top, 6, c.clicks-1)
		e.mouse(mouseAt{action: mousePress, left: true, row: top, col: 6})
		e.mouse(mouseAt{action: mouseDrag, left: true, row: top + 2, col: 2})
		if got := e.ed.Mode(); got != c.want {
			t.Errorf("a drag after %d clicks left the editor in %v, want %v", c.clicks, got, c.want)
		}
	}
}

// TestAClickInInsertModeStaysInInsert, and a drag from insert mode selects.
// Both measured: 'mouse' has an "i" in it, so a click moves the cursor and
// leaves the mode alone, and a drag makes a charwise selection anchored at the
// press.
func TestAClickInInsertModeStaysInInsert(t *testing.T) {
	e := mouseEditor(t)
	top := e.regions.Text.Row
	if err := e.sess.Run(keysOf(t, "i")); err != nil {
		t.Fatal(err)
	}
	mouseSend(e, mouseClick(top+2, 2)...)
	if got := e.ed.Mode(); got != mode.Insert {
		t.Errorf("a click in insert mode left the editor in %v, want insert", got)
	}
	if got := e.ed.Cursor().Line; got != 3 {
		t.Errorf("a click in insert mode left the cursor on line %d, want 3", got)
	}

	e.mouse(mouseAt{action: mousePress, left: true, row: top, col: 2})
	e.mouse(mouseAt{action: mouseDrag, left: true, row: top + 2, col: 7})
	if got := e.ed.Mode(); got != mode.VisualChar {
		t.Errorf("a drag from insert mode left the editor in %v, want charwise visual", got)
	}
}

// unnamed is the unnamed register's vim type letter and its text, which is
// what says whether a span came out charwise or linewise.
func unnamed(t *testing.T, e *editor) (string, string) {
	t.Helper()
	v, err := e.ed.Registers().Get('"')
	if err != nil {
		t.Fatalf("reading the unnamed register: %v", err)
	}
	return v.RegType(), string(v.Bytes())
}

// bufText is the whole buffer as one string.
func bufText(e *editor) string {
	var b strings.Builder
	for i := 1; i <= e.buf.LineCount(); i++ {
		b.Write(e.buf.Line(i))
		b.WriteByte('\n')
	}
	return b.String()
}

// TestAClickWithAnOperatorPendingIsAMotion is the last of the mouse items: a
// click with an operator pending is a motion.
//
// Every row is vim 9.2.0321 through a pty with 'ttymouse' sgr, on this file.
// The keys column is what was typed before the report, the click is a screen
// row and a 0-based screen column, and want is the buffer vim wrote out
// afterwards with the unnamed register beside it.
//
// The three that decide the code: the backward click, because the span is the
// two positions in buffer order and not cursor-then-pointer; the click on the
// cursor's own cell, where an empty exclusive region makes "d" do nothing at
// all and leaves the register untouched while "c" opens insert mode anyway;
// and the click past the end of a line, which lands one column past the last
// character rather than on it and so hands op_delete a span it promotes to
// linewise.
func TestAClickWithAnOperatorPendingIsAMotion(t *testing.T) {
	cases := []struct {
		name     string
		keys     string
		row, col int
		after    string
		want     string
		regType  string
		regText  string
		wantLine int
		wantCol  int
	}{
		{
			name: "d forwards", keys: "d", row: 2, col: 7,
			want:    "hh iiii\njjjj kkkk llll\n",
			regType: "v", regText: "aaaa bbbb cccc\ndddd eeee ffff\ngggg hh",
			wantLine: 1, wantCol: 0,
		},
		{
			name: "d backwards", keys: "3G3|d", row: 0, col: 1,
			want:    "agg hhhh iiii\njjjj kkkk llll\n",
			regType: "v", regText: "aaa bbbb cccc\ndddd eeee ffff\ngg",
			wantLine: 1, wantCol: 1,
		},
		{
			name: "y forwards", keys: "y", row: 2, col: 7,
			want:    "aaaa bbbb cccc\ndddd eeee ffff\ngggg hhhh iiii\njjjj kkkk llll\n",
			regType: "v", regText: "aaaa bbbb cccc\ndddd eeee ffff\ngggg hh",
			wantLine: 1, wantCol: 0,
		},
		{
			name: "shift right", keys: ">", row: 2, col: 7,
			want:     "\taaaa bbbb cccc\n\tdddd eeee ffff\n\tgggg hhhh iiii\njjjj kkkk llll\n",
			wantLine: 1, wantCol: 1,
		},
		{
			name: "c forwards, then a Z", keys: "c", row: 2, col: 7, after: "Z\x1b",
			want:    "Zhh iiii\njjjj kkkk llll\n",
			regType: "v", regText: "aaaa bbbb cccc\ndddd eeee ffff\ngggg hh",
			wantLine: 1, wantCol: 0,
		},
		{
			name: "d on the cursor's own cell", keys: "d", row: 0, col: 0,
			want:     "aaaa bbbb cccc\ndddd eeee ffff\ngggg hhhh iiii\njjjj kkkk llll\n",
			wantLine: 1, wantCol: 0,
		},
		{
			name: "c on the cursor's own cell, then a Z", keys: "c", row: 0, col: 0, after: "Z\x1b",
			want:     "Zaaaa bbbb cccc\ndddd eeee ffff\ngggg hhhh iiii\njjjj kkkk llll\n",
			wantLine: 1, wantCol: 0,
		},
		{
			name: "d past the end of a line", keys: "d", row: 1, col: 39,
			want:    "gggg hhhh iiii\njjjj kkkk llll\n",
			regType: "V", regText: "aaaa bbbb cccc\ndddd eeee ffff\n",
			wantLine: 1, wantCol: 0,
		},
		{
			name: "d below the last line", keys: "d", row: 5, col: 4,
			want:    " kkkk llll\n",
			regType: "v", regText: "aaaa bbbb cccc\ndddd eeee ffff\ngggg hhhh iiii\njjjj",
			wantLine: 1, wantCol: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := mouseEditor(t)
			if err := e.sess.Run(keysOf(t, c.keys)); err != nil {
				t.Fatal(err)
			}
			mouseSend(e, mouseClick(e.regions.Text.Row+c.row, c.col)...)
			if c.after != "" {
				if err := e.sess.Run(keysOf(t, c.after)); err != nil {
					t.Fatal(err)
				}
			}
			if got := bufText(e); got != c.want {
				t.Errorf("buffer is %q, vim leaves %q", got, c.want)
			}
			if c.regType != "" {
				gt, gx := unnamed(t, e)
				if gt != c.regType || gx != c.regText {
					t.Errorf("unnamed register is %q %q, vim leaves %q %q", gt, gx, c.regType, c.regText)
				}
			}
			if got := e.ed.Cursor(); got.Line != c.wantLine || got.Col != c.wantCol {
				t.Errorf("cursor at %+v, vim leaves line %d column %d", got, c.wantLine, c.wantCol)
			}
		})
	}
}

// TestAClickWithAnOperatorPendingLeavesTheDotRecordAlone.
//
// This is the one place on the mouse path that does not match vim, and it is
// deliberate: vim puts the mouse report itself into the redo record and "."
// re-decodes it against the screen as it is now, so a "gg" and a "." after a
// "d" and a click at screen row 5 deletes down to screen row 5 of a buffer
// that is three lines shorter. This editor's redo record is keys, a screen
// position is not one, and the change before the click stays the one "."
// repeats. See internal/mode/position.go.
func TestAClickWithAnOperatorPendingLeavesTheDotRecordAlone(t *testing.T) {
	e := mouseEditor(t)
	if err := e.sess.Run(keysOf(t, "xd")); err != nil {
		t.Fatal(err)
	}
	mouseSend(e, mouseClick(e.regions.Text.Row+2, 7)...)
	if err := e.sess.Run(keysOf(t, ".")); err != nil {
		t.Fatal(err)
	}
	// The x took the first "a", the click deleted through to line 3, and the
	// "." is the x again on what is left.
	if got, want := bufText(e), "h iiii\njjjj kkkk llll\n"; got != want {
		t.Errorf("buffer is %q, want %q: \".\" should have repeated the x", got, want)
	}
}

// TestAClickInAnotherWindowClearsThePendingOperator.
//
// vim's do_mouse says why in a comment: "when jumping to another window, clear
// a pending operator. That's a bit friendlier than beeping and not jumping to
// that window." Measured with a ":split", a "d" typed in the top window and a
// click in the bottom one: the buffer is untouched and the "w" typed after it
// moves a word rather than deleting one.
func TestAClickInAnotherWindowClearsThePendingOperator(t *testing.T) {
	e := mouseEditor(t)
	if err := e.sess.runLine("split"); err != nil {
		t.Fatal(err)
	}
	e.draw(40, 120)
	wins := e.tabs.Current().Windows()
	if len(wins) != 2 {
		t.Fatalf("%d windows after a split", len(wins))
	}
	other := wins[1]
	before := bufText(e)
	if err := e.sess.Run(keysOf(t, "d")); err != nil {
		t.Fatal(err)
	}
	mouseSend(e, mouseClick(other.Rect.Row+1, 2)...)
	if got := bufText(e); got != before {
		t.Errorf("a click in another window with an operator pending changed the buffer to %q", got)
	}
	if e.sess.win() != other {
		t.Error("the click did not switch to the window it landed in")
	}
	if err := e.sess.Run(keysOf(t, "w")); err != nil {
		t.Fatal(err)
	}
	if got := bufText(e); got != before {
		t.Errorf("the w after the click deleted a word: buffer is %q", got)
	}
}

// TestAClickThrowsAwayAHalfTypedPrefix. Measured: "3" then a click then "x"
// deletes one character and not three, and so does "g" then a click then "x".
//
// The "g" was the interesting one: session.go holds the g of "gt" and "gT" in
// pendingG, and the same for the z family and CTRL-W, so a click used to clear
// the mode machine's half-typed command and leave the frontend's, and "g",
// click, "x" beeped where vim deletes a character. press() now calls
// clearPrefixes, so the g is in the list.
func TestAClickThrowsAwayAHalfTypedPrefix(t *testing.T) {
	for _, prefix := range []string{"3", "\"a", "g", "z", "\x17"} {
		e := mouseEditor(t)
		if err := e.sess.Run(keysOf(t, prefix)); err != nil {
			t.Fatal(err)
		}
		mouseSend(e, mouseClick(e.regions.Text.Row+2, 0)...)
		if err := e.sess.Run(keysOf(t, "x")); err != nil {
			t.Fatal(err)
		}
		if got, want := string(e.buf.Line(3)), "ggg hhhh iiii"; got != want {
			t.Errorf("%q then a click then x left line 3 as %q, want %q", prefix, got, want)
		}
	}
}

// TestAClickWhileACommandWaitsForACharacterIsDropped. Measured: "f" and then a
// click on another line moves nothing and changes nothing, because vim is
// holding the next keystroke for the f and a mouse report is not one.
func TestAClickWhileACommandWaitsForACharacterIsDropped(t *testing.T) {
	e := mouseEditor(t)
	if err := e.sess.Run(keysOf(t, "f")); err != nil {
		t.Fatal(err)
	}
	before := e.ed.Cursor()
	mouseSend(e, mouseClick(e.regions.Text.Row+2, 7)...)
	if got := e.ed.Cursor(); got != before {
		t.Errorf("a click while f was waiting moved the cursor to %+v", got)
	}
}

// TestAClickInVisualModeWithAHalfTypedOperator ends the selection and moves,
// measured: "v", "l", "l", "g" and then a click leaves vim in normal mode with
// the cursor under the pointer and nothing changed. The half-typed "g" goes
// with the selection.
func TestAClickInVisualModeWithAHalfTypedOperator(t *testing.T) {
	e := mouseEditor(t)
	before := bufText(e)
	if err := e.sess.Run(keysOf(t, "vllg")); err != nil {
		t.Fatal(err)
	}
	mouseSend(e, mouseClick(e.regions.Text.Row+2, 4)...)
	if got := e.ed.Mode(); got != mode.Normal {
		t.Errorf("the click left the editor in %v, want normal", got)
	}
	if got := e.ed.Cursor(); got.Line != 3 || got.Col != 4 {
		t.Errorf("cursor at %+v, want line 3 column 4", got)
	}
	if got := bufText(e); got != before {
		t.Errorf("the click changed the buffer to %q", got)
	}
}

// TestTheWheelScrollsTheWindowUnderThePointer.
//
// Measured with ":vsplit": a notch over the window that is not current moves
// that window's 'topline' and leaves the cursor and the current window alone.
func TestTheWheelScrollsTheWindowUnderThePointer(t *testing.T) {
	e := drawnEditor(t, sixtyLines())
	if err := e.sess.runLine("vsplit"); err != nil {
		t.Fatal(err)
	}
	e.draw(40, 120)
	wins := e.tabs.Current().Windows()
	left, right := wins[0], wins[1]
	before := e.ed.Cursor()

	e.mouse(mouseAt{action: mouseWheelDown, row: right.Rect.Row + 2, col: right.Rect.Col + 2})
	if got := right.View.TopLine; got != 1+wheelLines {
		t.Errorf("a notch over the other window left its top line at %d, want %d", got, 1+wheelLines)
	}
	if got := left.View.TopLine; got != 1 {
		t.Errorf("a notch over the other window scrolled the current one to %d", got)
	}
	if got := e.ed.Cursor(); got != before {
		t.Errorf("a notch over the other window moved the cursor to %+v", got)
	}
	if got := e.tabs.Current().Cur; got != left {
		t.Errorf("a notch over the other window changed the current window")
	}
}

// TestAClickInAnotherWindowSwitchesToIt. Measured: the click switches windows
// and puts the cursor where the pointer is, and the window that was left keeps
// its own cursor.
func TestAClickInAnotherWindowSwitchesToIt(t *testing.T) {
	e := drawnEditor(t, sixtyLines())
	if err := e.sess.runLine("vsplit"); err != nil {
		t.Fatal(err)
	}
	e.draw(40, 120)
	wins := e.tabs.Current().Windows()
	left, right := wins[0], wins[1]
	e.ed.SetCursor(text.Pos{Line: 4})
	e.sess.sync()

	mouseSend(e, mouseClick(right.Rect.Row+2, right.Rect.Col+3)...)
	if got := e.tabs.Current().Cur; got != right {
		t.Fatalf("a click in the other window did not make it current")
	}
	if got, want := e.ed.Cursor(), (text.Pos{Line: 3, Col: 3}); got != want {
		t.Errorf("the click left the cursor at %+v, want %+v", got, want)
	}
	if got := left.View.Cursor.Line; got != 4 {
		t.Errorf("the window that was left kept its cursor on line %d, want 4", got)
	}
}

// TestAClickInAWindowOnAnotherBufferSwapsTheEditor. ":new" makes a window on a
// buffer of its own; a click in it has to hand the mode machine that buffer,
// or the next keystroke edits the wrong file. The swap is done in mouse.go and
// not through ":wincmd w" because internal/ex's own window switch resets the
// window it switches to back to line 1; see focusWindow.
func TestAClickInAWindowOnAnotherBufferSwapsTheEditor(t *testing.T) {
	e := drawnEditor(t, sixtyLines())
	if err := e.sess.runLine("new"); err != nil {
		t.Fatal(err)
	}
	e.draw(40, 120)
	wins := e.tabs.Current().Windows()
	fresh, old := wins[0], wins[1]
	if fresh.Buf == old.Buf {
		t.Fatalf(":new gave both windows the same buffer")
	}
	mouseSend(e, mouseClick(old.Rect.Row+2, 3)...)
	if e.buf != old.Buf {
		t.Errorf("a click in the other window left the editor on the wrong buffer")
	}
	if got, want := e.ed.Cursor(), (text.Pos{Line: 3, Col: 3}); got != want {
		t.Errorf("the click left the cursor at %+v, want %+v", got, want)
	}
	if got := e.ed.Buffer(); got != old.Buf {
		t.Errorf("the mode machine is on a different buffer than the window it is showing")
	}
}

// TestDraggingASeparatorResizes.
//
// Measured on an 80-column, 24-row screen. ":vsplit" gives windows of 40 and
// 39 with the divider in column 41; dragging it to column 51 leaves 50 and 29.
// ":split" gives 11 and 10 rows with a status line on row 12; dragging that to
// row 16 leaves 15 and 6. Neither drag changes the current window, and a press
// on the divider with no drag changes nothing at all.
func TestDraggingASeparatorResizes(t *testing.T) {
	t.Run("vertical", func(t *testing.T) {
		e := drawnEditor(t, sixtyLines())
		if err := e.sess.runLine("vsplit"); err != nil {
			t.Fatal(err)
		}
		e.draw(40, 120)
		wins := e.tabs.Current().Windows()
		left, right := wins[0], wins[1]
		sep := left.Rect.Col + left.View.Width
		wide := left.View.Width + 10

		mouseSend(e, mouseClick(left.Rect.Row+2, sep)...)
		e.windowLayout()
		if got := left.View.Width; got != sep {
			t.Fatalf("a press on the divider with no drag resized the window to %d", got)
		}

		e.mouse(mouseAt{action: mousePress, left: true, row: left.Rect.Row + 2, col: sep})
		e.mouse(mouseAt{action: mouseDrag, left: true, row: left.Rect.Row + 2, col: left.Rect.Col + wide})
		e.windowLayout()
		if got := left.View.Width; got != wide {
			t.Errorf("dragging the divider ten columns right left the window %d wide, want %d", got, wide)
		}
		if got := right.View.Width; got != 120-wide-1 {
			t.Errorf("the window beside it is %d wide, want %d", got, 120-wide-1)
		}
		if e.tabs.Current().Cur != left {
			t.Errorf("dragging the divider changed the current window")
		}
	})

	t.Run("horizontal", func(t *testing.T) {
		e := drawnEditor(t, sixtyLines())
		if err := e.sess.runLine("split"); err != nil {
			t.Fatal(err)
		}
		e.draw(40, 120)
		wins := e.tabs.Current().Windows()
		upper, lower := wins[0], wins[1]
		status := upper.Rect.Row + upper.View.Height
		tall := upper.View.Height + 4

		e.mouse(mouseAt{action: mousePress, left: true, row: status, col: 4})
		e.mouse(mouseAt{action: mouseDrag, left: true, row: upper.Rect.Row + tall, col: 4})
		e.windowLayout()
		if got := upper.View.Height; got != tall {
			t.Errorf("dragging the status line down four left the window %d rows, want %d", got, tall)
		}
		if got, want := lower.View.Height, 39-tall-2; got != want {
			t.Errorf("the window under it is %d rows, want %d", got, want)
		}
	})
}

// TestATablineClick.
//
// Measured with three tabs on an 80-column screen: a click on a label goes to
// that tab, a click in the empty space after the last label is "gt" and wraps
// from the last tab to the first, and a click on the "X" in the last column
// closes the current tab.
func TestATablineClick(t *testing.T) {
	newTabs := func(t *testing.T) *editor {
		t.Helper()
		e := drawnEditor(t, sixtyLines())
		for _, line := range []string{"tabnew", "tabnew"} {
			if err := e.sess.runLine(line); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.sess.runLine("tabnext 1"); err != nil {
			t.Fatal(err)
		}
		e.draw(40, 120)
		return e
	}

	t.Run("a label", func(t *testing.T) {
		e := newTabs(t)
		spans := e.tabSpans()
		if len(spans) != 3 {
			t.Fatalf("three tabs drew %d labels", len(spans))
		}
		e.mouse(mouseAt{action: mousePress, left: true, row: e.regions.Tabline.Row, col: spans[2][0] + 1})
		if got := e.tabs.Cur; got != 2 {
			t.Errorf("a click on the third label left tab %d current, want 2", got)
		}
	})

	t.Run("past the last label is gt", func(t *testing.T) {
		e := newTabs(t)
		past := e.tabSpans()[2][1] + 1
		for _, want := range []int{1, 2, 0} {
			e.mouse(mouseAt{action: mousePress, left: true, row: e.regions.Tabline.Row, col: past})
			if got := e.tabs.Cur; got != want {
				t.Fatalf("a click past the last label left tab %d current, want %d", got, want)
			}
			e.draw(40, 120)
		}
	})

	t.Run("the X closes the tab", func(t *testing.T) {
		e := newTabs(t)
		e.mouse(mouseAt{action: mousePress, left: true, row: e.regions.Tabline.Row, col: e.regions.Tabline.Cols - 1})
		if got := len(e.tabs.Pages); got != 2 {
			t.Errorf("a click on the X left %d tabs, want 2", got)
		}
	})

	t.Run("one tab draws no tabline, so row 0 is text", func(t *testing.T) {
		// 'showtabline' is 1, so a single tab has no line of its own and the
		// first screen row is the first line of the buffer. A mouse that
		// reserved row 0 for a tabline that was not there would swallow every
		// click on line 1.
		e := drawnEditor(t, sixtyLines())
		if !e.regions.Tabline.Empty() {
			t.Fatalf("one tab drew a tabline")
		}
		e.mouse(mouseAt{action: mousePress, left: true, row: 0, col: 4})
		if got, want := e.ed.Cursor(), (text.Pos{Line: 1, Col: 4}); got != want {
			t.Errorf("a click on row 0 with no tabline left the cursor at %+v, want %+v", got, want)
		}
	})
}

// TestTabSpansMatchTheDrawnTabline.
//
// tabSpans is internal/screen's drawTabline arithmetic written a second time,
// because that package draws the label and does not say where it put it. This
// is what makes the duplication safe: the current tab's label is the only run
// of TabLineSel cells on the row, so making each tab current in turn and
// rendering gives an exact answer from the renderer itself, and any change to
// how a tab is laid out fails here rather than sending clicks to the wrong tab.
func TestTabSpansMatchTheDrawnTabline(t *testing.T) {
	for _, n := range []int{2, 3, 5, 9} {
		e := drawnEditor(t, sixtyLines())
		for i := 1; i < n; i++ {
			if err := e.sess.runLine("tabnew " + fmt.Sprintf("f%02d.txt", i)); err != nil {
				t.Fatal(err)
			}
		}
		// A modified buffer and a split, so that the "+" and the window count
		// in front of a label are exercised too.
		if err := e.sess.runLine("tabnext 1"); err != nil {
			t.Fatal(err)
		}
		if err := e.sess.Run(keysOf(t, "x")); err != nil {
			t.Fatal(err)
		}
		if err := e.sess.runLine("split"); err != nil {
			t.Fatal(err)
		}

		for i := range e.tabs.Pages {
			e.tabs.Goto(i)
			s := e.draw(40, 120)
			e.regions = s.Regions()
			spans := e.tabSpans()
			if i >= len(spans) {
				continue
			}
			start, end := selRun(s)
			if start < 0 {
				t.Fatalf("%d tabs, tab %d current: no TabLineSel run on the drawn tabline", n, i)
			}
			if spans[i][0] != start || spans[i][1] != end {
				t.Errorf("%d tabs, tab %d: tabSpans says [%d,%d) and the grid drew [%d,%d)",
					n, i, spans[i][0], spans[i][1], start, end)
			}
		}
	}
}

// selRun is the half-open column range of the TabLineSel cells on row 0.
func selRun(s *screen.Screen) (start, end int) {
	want := screen.LookupGroups(s.HL).TabLineSel
	start = -1
	for col := 0; col < s.Grid.Cols; col++ {
		if s.Grid.At(0, col).HL == want {
			if start < 0 {
				start = col
			}
			end = col + 1
		}
	}
	return start, end
}
