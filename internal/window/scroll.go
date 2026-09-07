package window

import "github.com/pkar/pvim/internal/text"

// ScrollCmd is one of vim's scrolling commands, named by what it does rather
// than by the key, because two keys reach several of them.
type ScrollCmd uint8

// The scrolling commands. The keys in the comments are the ones the fuzzer
// found pvim failing on, which is what this package exists to fix.
const (
	// HalfUp is CTRL-U and HalfDown is CTRL-D: scroll by 'scroll', taking the
	// cursor with them.
	HalfUp ScrollCmd = iota
	HalfDown
	// PageBack is CTRL-B and PageForward is CTRL-F: scroll by a window, less
	// two lines of overlap.
	PageBack
	PageForward
	// LineUp is CTRL-Y and LineDown is CTRL-E: scroll one line, leaving the
	// cursor where it is unless 'scrolloff' pushes it.
	LineUp
	LineDown
	// Redraw at the top, middle and bottom: zt, zz, zb. The First forms are
	// z<CR>, z. and z-, which do the same and then put the cursor on the
	// first non-blank of the line.
	RedrawTop
	RedrawTopFirst
	RedrawMiddle
	RedrawMiddleFirst
	RedrawBottom
	RedrawBottomFirst
	// ScreenDown is z+ and ScreenUp is z^: the next and previous screenful,
	// cursor on the first non-blank. z+ is not CTRL-F: it puts the line after
	// the last one showing at the *top*, with no two-line overlap.
	ScreenDown
	ScreenUp
)

// Scroll runs one scrolling command and reports whether anything moved. False
// is a beep: CTRL-U on the first line of the buffer, CTRL-B with the window
// already at the top.
//
// count is the count the user typed, 0 for none. so is 'scrolloff' as the
// caller resolved it through ScrollOff; it is passed in rather than read so
// that every rule below is a calculation over three integers and a line count.
//
// Every number here was measured against /opt/homebrew/bin/vim,
// VIM 9.2 patches 1-321, through "vim --clean -i NONE --not-a-term -s" with
// ":set lines=" fixing the window height and winrestview() fixing the scroll
// position, reading line('w0'), line('w$') and line('.') back out through
// writefile(). The rules that are not obvious:
//
// - A count on CTRL-U or CTRL-D is not a repeat, it is a new 'scroll', and
// the new value persists for the window: &scroll is 11 in a 23-row window,
// "5<C-U>" makes it 5, and every bare CTRL-U afterwards moves 5. That one
// rule is 85 of the 270 differences in the last fuzz run, and it is why
// this takes a pointer to the window rather than a View.
// - The count is clamped to the window height. "100<C-U>" in a 23-row window
// sets 'scroll' to 23.
// - CTRL-D at the end of the buffer will not scroll past the last screenful,
// but it still puts the cursor on the last line: from a window showing 378
// to 400 the top stays 378 and the cursor goes to 400.
// - CTRL-F, when the last line of the buffer is already showing, puts that
// last line alone at the top of the window. Vim's own test is on w_botline,
// which is one past the last visible line, so a window showing 378 to 400
// of a 400-line file qualifies and one showing 377 to 399 does not: the
// first gives a top of 400 and the second of 398.
// - CTRL-B with the window already at line 1 does nothing at all, not even
// move the cursor, which is not symmetric with CTRL-U: CTRL-U at the top
// of the buffer still walks the cursor up to line 1.
// - CTRL-E may scroll until the last line of the buffer is alone at the top;
// CTRL-D may not. Measured: "G" then 30 CTRL-E leaves w0 400 on a 400-line
// file.
// - zt honours 'scrolloff'. With the cursor on 110 and 'scrolloff' 3 the top
// becomes 107, not 110. So does zb, which stops three lines short.
// - zz puts the cursor line at row (h-1)/2 + 1, so an even-height window puts
// it one row above centre: cursor 110 in a 12-row window gives a top of
// 105 and not 104.
//
// The whole of this is the 'nowrap' arithmetic, one screen row per buffer line,
// which is every window this vimrc opens. Under 'wrap' each of these counts
// display lines instead; see the TODO on BotLine.
func (w *Window) Scroll(cmd ScrollCmd, count, so int) bool {
	h := w.View.Height
	if h <= 0 {
		return false
	}
	so = clampScrollOff(so, h)

	switch cmd {
	case HalfUp, HalfDown:
		if count > 0 {
			w.Opt.Scroll = clamp(count, 1, h)
		}
		if w.Opt.Scroll <= 0 {
			w.Opt.Scroll = DefaultScroll(h)
		}
		return w.halfPage(cmd == HalfDown, w.Opt.Scroll, so)

	case PageForward:
		return w.pageForward(max(1, count), so)
	case PageBack:
		return w.pageBack(max(1, count), so)

	case LineDown:
		return w.lineScroll(max(1, count), so)
	case LineUp:
		return w.lineScroll(-max(1, count), so)

	case ScreenDown, ScreenUp:
		// The raw count, not max(1, count): on z+ and z^ a count is a line
		// number and zero means "no count", which is a different command.
		return w.screen(cmd == ScreenDown, count, so)

	default:
		return w.redraw(cmd, count, so)
	}
}

// halfPage is CTRL-D and CTRL-U: move the top line and the cursor by the same
// number of lines.
//
// The two clamps differ and that is the whole of it. The top may not go past
// the last screenful, so CTRL-D at the end of the buffer does not scroll; the
// cursor may go all the way to the last line, so CTRL-D there still moves it.
func (w *Window) halfPage(down bool, n, so int) bool {
	last := w.Buf.LineCount()
	oldTop, oldCur := w.View.TopLine, w.View.Cursor.Line

	if down {
		// The top may not be dragged BACKWARDS. Vim's halfpage() advances it
		// one line at a time while w_botline <= line_count, so it stops the
		// moment the last line of the buffer reaches the bottom row and does
		// nothing at all when the window is already past that. A plain clamp
		// to lastTop is a backwards scroll for a window zt, zz or CTRL-E has
		// pushed further. Measured on a 400-line file in a 39-row window at
		// 'scrolloff' 5: "Gzt" leaves the top on 395 and the CTRL-D after it
		// leaves it there, where a clamp to lastTop would drag it to 362.
		// "380Gzt<C-D>" keeps the top on 375 and still walks the cursor from
		// 380 to 399, because the cursor is not clamped the same way.
		maxTop := w.lastTop()
		if maxTop < w.View.TopLine {
			maxTop = w.View.TopLine
		}
		w.View.TopLine = clamp(w.View.TopLine+n, 1, maxTop)
		w.setCursorLine(clamp(oldCur+n, 1, last))
	} else {
		w.View.TopLine = clamp(w.View.TopLine-n, 1, last)
		w.setCursorLine(clamp(oldCur-n, 1, last))
	}
	w.CursorInView(so)
	return w.View.TopLine != oldTop || w.View.Cursor.Line != oldCur
}

// pageForward is CTRL-F: the top line jumps forward by a window less two lines
// of overlap, and the cursor goes to the new top line plus 'scrolloff'.
//
// The end-of-buffer case is vim's, tested on w_botline rather than on the last
// visible line: when the window is already showing the last line of the file,
// CTRL-F puts that line alone at the top instead of refusing.
func (w *Window) pageForward(count, so int) bool {
	last := w.Buf.LineCount()
	oldTop, oldCur := w.View.TopLine, w.View.Cursor.Line

	top := w.View.TopLine
	for i := 0; i < count; i++ {
		if top+w.View.Height > last {
			top = last
			break
		}
		top += w.View.Height - 2
		if top > last {
			top = last
		}
	}
	w.View.TopLine = clamp(top, 1, last)
	w.setCursorLine(clamp(w.View.TopLine+so, 1, last))
	w.CursorInView(so)
	return w.View.TopLine != oldTop || w.View.Cursor.Line != oldCur
}

// pageBack is CTRL-B: the top line jumps back so that the line below the old
// top line ends up on the bottom row, and the cursor goes to the bottom of the
// new window less 'scrolloff'.
//
// Mid-file that is a jump of exactly the window less two lines, which is the
// two lines of overlap CTRL-F leaves. It is not that at the end of the buffer,
// because the line put on the bottom row may not be the last line of the file:
// measured on a 400-line file in a 39-row window, a window sitting at top 398,
// 399 or 400 all come back to top 361 showing 361 to 399, so the bottom stops
// one line short of the end and the jump is 37, 38 and 39 lines respectively.
// A window showing one buffer line above a screen of tildes therefore moves
// further than the formula says, which is where the old (h-2) arithmetic was
// two lines off: "<C-F><C-F><C-F>" on a 50-line file leaves top 50, and the
// CTRL-B after it gives vim top 11 showing to 49, not top 13 showing to 50.
//
// A count is not a repeat of that. The distance is worked out once, from the
// window the key arrived at, and applied count times: 2<C-B> from top 400 of a
// 400-line file in a 39-row window gives 322, where two separate CTRL-Bs give
// 361 and then 324. Measured at heights 23 and 39 with the excess over the end
// at 0, 1 and 2 lines, and mid-file it collapses to count*(h-2) as before.
//
// With the window already at line 1 this does nothing and beeps, cursor
// included. Measured: a window at the top of the file with the cursor on 20
// still has it on 20 afterwards.
func (w *Window) pageBack(count, so int) bool {
	if w.View.TopLine <= 1 {
		return false
	}
	last := w.Buf.LineCount()
	top := w.View.TopLine

	bot := top + 1
	if bot > last-1 {
		bot = last - 1
	}
	step := top - (bot - w.View.Height + 1)
	if step < 1 {
		// A window one or two rows tall has no overlap left to keep. Vim is
		// not measured here and one line back is what the old arithmetic did,
		// so it stays: a window that cannot move is worse than one that
		// crawls.
		step = 1
	}
	if count > last {
		// Each step moves at least one line, so anything past the line count
		// lands on line 1 anyway, and this keeps a typed count of a billion
		// out of the multiplication.
		count = last
	}
	w.View.TopLine = clamp(top-step*count, 1, last)
	w.setCursorLine(clamp(w.BotLine()-so, 1, last))
	w.CursorInView(so)
	return true
}

// lineScroll is CTRL-E and CTRL-Y: move the window by n lines, positive for
// forward, and leave the cursor alone unless 'scrolloff' pushes it.
//
// The forward limit is the last line of the buffer, not the last screenful:
// CTRL-E may leave one line showing above a screen of tildes, which is what
// makes it different from CTRL-D.
func (w *Window) lineScroll(n, so int) bool {
	last := w.Buf.LineCount()
	oldTop, oldCur := w.View.TopLine, w.View.Cursor.Line

	w.View.TopLine = clamp(w.View.TopLine+n, 1, last)
	w.CursorInView(so)
	return w.View.TopLine != oldTop || w.View.Cursor.Line != oldCur
}

// screen is z+ and z^: the screenful after and the screenful before.
//
// Bare, z+ puts the line after the last one showing at the top, with none of
// CTRL-F's two-line overlap, and z^ puts the line before the top one at the
// bottom. Both leave the cursor on the first non-blank.
//
// A count is NOT a repeat, which is the whole reason this is not a loop.
// ":help z+" says a count makes it "just like z<CR>", so "50z+" scrolls line
// 50 to the top and puts the cursor there; measured in a 23-row window from
// top 100, 2z+ gives top 2 and 50z+ gives top 50, where a repeat gave 146 and
// 400. ":help z^" says a count scrolls line [count] to the bottom and then
// does the bare z^ on the result, and vim's nv_zet reads that as: put the
// cursor on line [count], scroll it to the bottom, move the cursor to the line
// that ended up at the top, and scroll that to the bottom in turn. Measured in
// the same window at 'scrolloff' 0, 50z^ gives top 6 with the cursor on 28 and
// 100z^ gives top 56 with the cursor on 78; at 'scrolloff' 3 they are 12/31
// and 62/81, and at 5 they are 16/33 and 66/83.
func (w *Window) screen(down bool, count, so int) bool {
	last := w.Buf.LineCount()
	oldTop, oldCur := w.View.TopLine, w.View.Cursor.Line

	switch {
	case down && count > 0:
		w.redraw(RedrawTopFirst, count, so)
	case down:
		// The line below the window, which is the one vim's w_botline names.
		w.setCursorLine(clamp(w.BotLine()+1, 1, last))
		w.redraw(RedrawTopFirst, 0, so)
	default:
		if count > 0 {
			w.redraw(RedrawBottomFirst, count, so)
			w.setCursorLine(w.View.TopLine)
		} else if w.View.TopLine <= 1 {
			// Vim's z^ with the window already at the top of the file puts the
			// cursor on line 1 rather than on line 0.
			w.setCursorLine(1)
		} else {
			w.setCursorLine(w.View.TopLine - 1)
		}
		w.redraw(RedrawBottomFirst, 0, so)
	}
	return w.View.TopLine != oldTop || w.View.Cursor.Line != oldCur
}

// redraw is the z family: put the cursor line at the top, the middle or the
// bottom of the window without moving it in the buffer.
//
// A count is a line number, not a repeat: "50zt" scrolls so that line 50 is at
// the top and puts the cursor there.
func (w *Window) redraw(cmd ScrollCmd, count, so int) bool {
	last := w.Buf.LineCount()
	if count > 0 {
		w.setCursorLine(clamp(count, 1, last))
	}
	line := w.View.Cursor.Line
	oldTop, oldCur := w.View.TopLine, w.View.Cursor.Line
	h := w.View.Height

	switch cmd {
	case RedrawTop, RedrawTopFirst:
		w.View.TopLine = clamp(line, 1, last)
	case RedrawMiddle, RedrawMiddleFirst:
		// (h-1)/2 and not h/2: in a 12-row window vim puts the cursor on row
		// 6, one above centre, so the top is the cursor less five.
		w.View.TopLine = clamp(line-(h-1)/2, 1, last)
	case RedrawBottom, RedrawBottomFirst:
		w.View.TopLine = clamp(line-h+1, 1, last)
	}
	// ScrollToCursorMinimal and not CursorInView: the z family moves the
	// window, not the cursor, so 'scrolloff' is satisfied by pulling the top
	// line back rather than by pushing the cursor down. Measured: cursor 110
	// with 'scrolloff' 3, zt gives a top of 107 and the cursor still on 110.
	w.ScrollToCursorMinimal(so)

	switch cmd {
	case RedrawTopFirst, RedrawMiddleFirst, RedrawBottomFirst:
		w.FirstNonBlank()
	}
	return w.View.TopLine != oldTop || w.View.Cursor.Line != oldCur
}

// FirstNonBlank puts the cursor on the first non-blank character of its line,
// which is what z<CR>, z., z-, z+ and z^ do after they scroll.
func (w *Window) FirstNonBlank() {
	line := w.Buf.Line(w.View.Cursor.Line)
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) && len(line) > 0 {
		i = len(line) - 1
	}
	w.View.Cursor.Col = i
}

// CursorInView is vim's cursor_correct: pull the cursor back inside the window
// with 'scrolloff' lines of margin at each end.
//
// The margin is suspended at the very top and the very bottom of the buffer,
// because there is nothing left to scroll to: measured, "gg" then H is line 1
// and not line 4, and "G" then L is the last line and not three above it. The
// halves are not symmetric -- at the top the far margin is capped at half the
// window and at the bottom at half the window less one -- and that asymmetry is
// vim's, copied from cursor_correct() rather than reasoned about.
//
// An operator waiting suspends the whole correction in vim, which is why this
// is a method the caller chooses to run rather than something that happens
// inside every motion.
func (w *Window) CursorInView(so int) {
	last := w.Buf.LineCount()
	top, bot := w.View.TopLine, w.BotLine()

	above, below := so, so
	if top <= 1 {
		above = 0
		if maxOff := w.View.Height / 2; below > maxOff {
			below = maxOff
		}
	}
	if bot >= last {
		below = 0
		if maxOff := (w.View.Height - 1) / 2; above > maxOff {
			above = maxOff
		}
	}

	lo, hi := top+above, bot-below
	if lo > hi {
		// The margins ask for more room than the window has, which happens on
		// the last screenful of a buffer shorter than the window. Being on
		// screen wins: measured, CTRL-F from line 380 of a 400-line file in a
		// 23-row window at 'scrolloff' 5 leaves the cursor on 400, which is
		// the bottom margin violated rather than the cursor put in the middle.
		lo = hi
	}
	w.setCursorLine(clamp(w.View.Cursor.Line, lo, hi))
}

// ScrollToCursor is vim's update_topline: after a motion has moved the cursor,
// scroll the window so that the cursor is inside it with 'scrolloff' lines of
// margin.
//
// Every motion that leaves the window calls this and every scroll command calls
// CursorInView, which is the same constraint solved from the two ends.
//
// Vim does not always scroll minimally. A short jump moves the top line as
// little as the margin allows; a long one redraws with the cursor near the
// middle of the window, so that "50G" on a file being read top to bottom does
// not leave the cursor pinned to the bottom row. The boundary between the two,
// and the row the cursor lands on, were measured over five window heights and
// four 'scrolloff' values; testdata/window/topline-vim9.2.0321.txt is the
// table and TestJumpsMatchVim replays it.
//
// Going forward there are three cases, keyed on how far past the last row of
// the window the cursor landed:
//
//	past <= (h+1)/2 scroll minimally: top = line + 'scrolloff' - h + 1
//	past + so <= h + 1 top = line - h/2
//	otherwise top = line - (h-1)/2
//
// The first boundary does not move with 'scrolloff' and the second does, which
// is the detail no amount of reading vim's source settles and one afternoon of
// asking it settles completely. The two halfway cases differ by one row in an
// even-height window and not at all in an odd one, which is why a 23-row window
// -- the one a headless vim gives -- hides the distinction entirely.
//
// Going backward there are two, keyed on how far the top line would have to
// move:
//
//	moved <= max(1, h/2-2) scroll minimally: top = line - 'scrolloff'
//	otherwise top = line - (h-1)/2
//
// That limit is independent of 'scrolloff' and of where the cursor was in the
// window, both checked; the forward one is independent of neither.
func (w *Window) ScrollToCursor(so int) {
	h := w.View.Height
	if h <= 0 {
		return
	}
	last := w.Buf.LineCount()
	line := clamp(w.View.Cursor.Line, 1, last)
	so = clampScrollOff(so, h)
	top := w.View.TopLine

	switch {
	case line+so > top+h-1:
		past := line - (top + h - 1)
		switch {
		case past <= (h+1)/2:
			top = line + so - h + 1
		case past+so <= h+1:
			top = line - h/2
		default:
			top = line - (h-1)/2
		}
	case line-so < top:
		want := line - so
		if top-want <= upJumpLimit(h) {
			top = want
		} else {
			top = line - (h-1)/2
		}
		// Scrolling BACKWARDS is vim's scroll_cursor_top, and that one has no
		// "the window must stay full" clamp on it: only scroll_cursor_bot,
		// the forwards half below, refuses to leave tildes under the last
		// line. The difference shows on a buffer shorter than the window,
		// where lastTop is 1: CTRL-F on a ten-line file in a thirty-nine row
		// window puts line 10 at the top, and the x typed there pulls the top
		// back to 5 and not to 1. Measured against vim, where the M after it
		// lands on 10 and used to land on 5.
		w.View.TopLine = clamp(top, 1, w.Buf.LineCount())
		return
	}
	w.setTop(top)
}

// ScrollToCursorMinimal is update_topline without the halfway redraw: move the
// top line as little as 'scrolloff' allows.
//
// It is the z family's correction and the one every scroll command wants, and
// it is separate because the halfway rule above belongs to a cursor that jumped
// and not to a window that was told where to be. zt with the cursor on 110 and
// 'scrolloff' 3 gives a top of 107, never of 99.
func (w *Window) ScrollToCursorMinimal(so int) {
	h := w.View.Height
	if h <= 0 {
		return
	}
	so = clampScrollOff(so, h)
	line := clamp(w.View.Cursor.Line, 1, w.Buf.LineCount())

	last := w.Buf.LineCount()
	top := w.View.TopLine
	if line-so < top {
		top = line - so
	}
	// The push DOWN is bounded by the lines that exist, in vim's code twice
	// over: update_topline runs its bottom check only while w_botline is still
	// inside the buffer, and scroll_cursor_bot stops counting 'scrolloff'
	// context the moment it walks off the end. Together they say the bottom
	// margin never asks for a line past the last one, which is one cap at
	// lastTop. Measured on a 400-line file in a 23-row window at 'scrolloff'
	// 3: zb with the cursor on 397, 398, 399 or 400 all give a top line of
	// 378, where the uncapped push gives 378, 379, 380 and 381. The gate is
	// separate from the cap and both are needed: a window already past the
	// last screenful must be left where it is rather than pulled back to it,
	// so on a 38-line file in a 39-row window zb keeps a top line of 1.
	if bot := top + h - 1; bot < last && line+so > bot {
		top = line + so - h + 1
		if maxTop := w.lastTop(); top > maxTop {
			top = maxTop
		}
	}
	// clamp and not setTop: zt near the end of the buffer is allowed to leave
	// tildes below the last line. Measured, a 23-row window with the cursor on
	// 395 of a 400-line file: zt gives a top line of 395, where a motion to the
	// same line would stop at 378.
	w.View.TopLine = clamp(top, 1, last)
}

// setTop assigns a top line, held inside the buffer.
//
// The upper clamp is why "G" on a 100-line file in a 23-row window reports w0
// 78 and not 89: once the last line is on the bottom row there is nothing to
// gain by scrolling further, and 'scrolloff' below the cursor has nothing left
// to ask for. A buffer shorter than the window has nothing to clamp and keeps a
// top line of 1.
func (w *Window) setTop(top int) {
	if maxTop := w.lastTop(); top > maxTop {
		top = maxTop
	}
	if top < 1 {
		top = 1
	}
	w.View.TopLine = top
}

// lastTop is the largest top line that still fills the window, which is where
// CTRL-D and every cursor motion stop. CTRL-E is the one command allowed past
// it.
func (w *Window) lastTop() int {
	last := w.Buf.LineCount()
	if last < w.View.Height {
		return 1
	}
	return last - w.View.Height + 1
}

// upJumpLimit is how far the top line may move backwards before vim stops
// scrolling minimally and redraws with the cursor near the middle.
//
// Measured over h = 3, 5, 7, 9, 11, 12, 15, 16, 23, 24, 30 and 40 at
// 'scrolloff' 0, 1 and 3, with the old cursor on every row of the window: the
// answer is max(1, h/2-2) every time and depends on neither.
func upJumpLimit(h int) int {
	if n := h/2 - 2; n > 1 {
		return n
	}
	return 1
}

// clampScrollOff is vim's rule for a 'scrolloff' larger than the window can
// honour: it becomes half the window, rounded down. In a 5-row window
// 'scrolloff' 3 behaves as 2, so zt on line 4 gives a top line of 2.
func clampScrollOff(so, h int) int {
	if maxOff := (h - 1) / 2; so > maxOff {
		return maxOff
	}
	if so < 0 {
		return 0
	}
	return so
}

// setCursorLine moves the cursor to a line and clamps the column to it, which
// is what every line-wise move in vim does.
//
// TODO: the column vim lands on is 'startofline' plus Curswant and not simply
// a clamp: a scroll command with 'startofline' off keeps the column, and with
// it on goes to the first non-blank. The clamp here is right for the
// column-preserving half and wrong for the other, and the caller that knows
// about 'startofline' is the one that should apply it.
func (w *Window) setCursorLine(line int) {
	w.View.Cursor = w.Buf.Clamp(text.Pos{Line: line, Col: w.View.Cursor.Col})
}

// clamp holds n between lo and hi.
func clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
