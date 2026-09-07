package motion

import (
	"github.com/pkar/pvim/internal/text"
)

// The up-down motions, :help up-down-motions.
//
// Two rules run through all of them. They are linewise, so the column is a
// consequence and not a destination, and the column they land on comes from
// the wanted column carried in the Context rather than from where the cursor
// is now: that is why $jjj sits at the end of every line it passes and why
// going down through a short line and back up returns to the column you left.
//
// The second rule is what a count that is too big does. j fails only when the
// cursor is already on the last line; from four lines above the end, 5j moves
// four and succeeds, and d5j deletes to the end of the buffer. $ is the
// exception and fails outright, which is vim's cursor_down called before the
// column is worked out rather than after.

// wantedCol is the display column the vertical motions aim for.
//
// CurswantUnset falls through to the cursor's own column, which is vim's
// update_curswant() recomputing from w_virtcol while w_set_curswant is TRUE.
// That matters on the very first motion of a session and nowhere else, because
// every motion after it leaves a real column behind.
func wantedCol(r Request) int {
	if r.Ctx != nil && r.Ctx.Curswant != CurswantUnset {
		return r.Ctx.Curswant
	}
	return dispCol(r.Buf, r.From, r.Opt.TabStop)
}

// cursorDown is vim's cursor_down(): move n lines down, clamped to the last
// line, failing only when there is nowhere to go at all.
func cursorDown(b *text.Buffer, lnum, n int) (int, bool) {
	if n <= 0 {
		return lnum, true
	}
	if lnum >= b.LineCount() {
		return lnum, false
	}
	if lnum += n; lnum > b.LineCount() {
		lnum = b.LineCount()
	}
	return lnum, true
}

// cursorUp is vim's cursor_up().
func cursorUp(b *text.Buffer, lnum, n int) (int, bool) {
	if n <= 0 {
		return lnum, true
	}
	if lnum <= 1 {
		return lnum, false
	}
	if lnum -= n; lnum < 1 {
		lnum = 1
	}
	return lnum, true
}

// motionDown is j, <Down>, CTRL-N and <NL>.
func motionDown(r Request) Result {
	b := r.Buf
	lnum, ok := cursorDown(b, r.From.Line, r.Count1())
	if !ok {
		return fail()
	}
	p := text.Pos{Line: lnum, Col: coladvance(b.Line(lnum), wantedCol(r), r.Opt.TabStop)}
	return Result{
		To: p, Kind: KindLine, Ok: true,
		Curswant: CurswantKeep, Count: lnum - r.From.Line,
	}
}

// motionUp is k, <Up> and CTRL-P.
func motionUp(r Request) Result {
	b := r.Buf
	lnum, ok := cursorUp(b, r.From.Line, r.Count1())
	if !ok {
		return fail()
	}
	p := text.Pos{Line: lnum, Col: coladvance(b.Line(lnum), wantedCol(r), r.Opt.TabStop)}
	return Result{
		To: p, Kind: KindLine, Ok: true,
		Curswant: CurswantKeep, Count: r.From.Line - lnum,
	}
}

// motionDownFirstNonBlank is + and <CR>: down, then to the first non-blank.
func motionDownFirstNonBlank(r Request) Result {
	b := r.Buf
	lnum, ok := cursorDown(b, r.From.Line, r.Count1())
	if !ok {
		return fail()
	}
	p := text.Pos{Line: lnum, Col: firstNonBlank(b.Line(lnum))}
	return Result{
		To: p, Kind: KindLine, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: lnum - r.From.Line,
	}
}

// motionUpFirstNonBlank is -.
func motionUpFirstNonBlank(r Request) Result {
	b := r.Buf
	lnum, ok := cursorUp(b, r.From.Line, r.Count1())
	if !ok {
		return fail()
	}
	p := text.Pos{Line: lnum, Col: firstNonBlank(b.Line(lnum))}
	return Result{
		To: p, Kind: KindLine, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.From.Line - lnum,
	}
}

// motionLineFirstNonBlank is _: this line with a count of one, count-1 lines
// down otherwise, and the first non-blank of it.
func motionLineFirstNonBlank(r Request) Result {
	b := r.Buf
	lnum, ok := cursorDown(b, r.From.Line, r.Count1()-1)
	if !ok {
		return fail()
	}
	p := text.Pos{Line: lnum, Col: firstNonBlank(b.Line(lnum))}
	return Result{
		To: p, Kind: KindLine, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
	}
}

// beginLine is vim's beginline(BL_SOL|BL_FIX): the first non-blank when
// 'startofline' is on, and the wanted column when it is off. G, gg, H, M, L
// and {count}% all end this way.
func beginLine(r Request, lnum int) text.Pos {
	line := r.Buf.Line(lnum)
	if r.Opt.StartOfLine {
		return text.Pos{Line: lnum, Col: firstNonBlank(line)}
	}
	return text.Pos{Line: lnum, Col: coladvance(line, wantedCol(r), r.Opt.TabStop)}
}

// motionGoto is G and gg. last says which end of the buffer the countless form
// goes to. Neither can fail: a count past the end of the buffer clamps, which
// is why 999G is how everybody gets to the bottom of a file.
func motionGoto(last bool) Func {
	return func(r Request) Result {
		b := r.Buf
		lnum := 1
		if last {
			lnum = b.LineCount()
		}
		if r.Count > 0 {
			lnum = r.Count
		}
		if lnum < 1 {
			lnum = 1
		}
		if lnum > b.LineCount() {
			lnum = b.LineCount()
		}
		p := beginLine(r, lnum)
		curswant := dispCol(b, p, r.Opt.TabStop)
		if !r.Opt.StartOfLine {
			curswant = CurswantKeep
		}
		return Result{
			To: p, Kind: KindLine, Ok: true, Jump: true,
			Curswant: curswant, Count: r.Count1(),
		}
	}
}

// motionByte is go: to the count'th byte of the buffer, counting the line
// separators, which is why a DOS buffer counts two per line and a unix one.
// No count is byte one.
func motionByte(r Request) Result {
	b := r.Buf
	sep := 1
	if b.Format() == text.DOS {
		sep = 2
	}
	off := r.Count - 1
	if off < 0 {
		off = 0
	}
	lnum := 1
	for lnum < b.LineCount() {
		n := len(b.Line(lnum)) + sep
		if off < n {
			break
		}
		off -= n
		lnum++
	}
	line := b.Line(lnum)
	if off > len(line) {
		off = len(line)
	}
	p := text.Pos{Line: lnum, Col: charStart(line, off)}
	if p.Col >= len(line) && len(line) > 0 {
		p.Col = prevChar(line, len(line))
	}
	return Result{
		To: p, Kind: KindCharExclusive, Ok: true, Jump: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
	}
}

// motionWindow is H, M and L: the top, middle and bottom line of what the
// window is showing.
//
// They are the only motions that need to know about the screen, and they get
// it as a Window filled in by the frontend rather than by asking one, which is
// what keeps this package testable with no display. A headless run leaves the
// Window zero and all three fail, which is the honest answer to "which line is
// at the top of a window that does not exist".
func motionWindow(where byte) Func {
	return func(r Request) Result {
		b := r.Buf
		if r.Ctx == nil {
			return fail()
		}
		w := r.Ctx.Window
		if w.Top < 1 || w.Bottom < w.Top {
			return fail()
		}
		top, bot := w.Top, w.Bottom
		if bot > b.LineCount() {
			bot = b.LineCount()
		}
		var lnum int
		switch where {
		case 'H':
			lnum = top + r.Count1() - 1
		case 'L':
			lnum = bot - (r.Count1() - 1)
			if r.Count1()-1 >= bot {
				lnum = 1
			}
		default: // M
			lnum = top + (bot-top)/2
		}
		if lnum < 1 {
			lnum = 1
		}
		if lnum > b.LineCount() {
			lnum = b.LineCount()
		}
		// vim's cursor_correct: the cursor may not leave the window, and
		// 'scrolloff' holds it that many lines further in, except at the very
		// top and the very bottom of the file where there is nothing to
		// scroll to. An operator waiting suspends the whole correction, so
		// d20L takes the line the count named even when it is off screen.
		if !r.Pending {
			height := w.Height
			if height <= 0 {
				height = bot - top + 1
			}
			above, below := r.Opt.ScrollOff, r.Opt.ScrollOff
			if w.AtTop {
				above = 0
				if max := height / 2; below > max {
					below = max
				}
			}
			if w.AtBottom {
				below = 0
				if max := (height - 1) / 2; above > max {
					above = max
				}
			}
			if lnum < top+above {
				lnum = top + above
			}
			if lnum > bot-below {
				lnum = bot - below
			}
		}
		p := beginLine(r, lnum)
		curswant := dispCol(b, p, r.Opt.TabStop)
		if !r.Opt.StartOfLine {
			curswant = CurswantKeep
		}
		return Result{
			To: p, Kind: KindLine, Ok: true, Jump: true,
			Curswant: curswant, Count: r.Count1(),
		}
	}
}

// screenRows is the number of display columns the rows of a line cover: the
// width of the window times the number of rows it takes. gj compares the
// wanted column against it to decide whether there is another row of this line
// to move onto or whether the next line starts.
func screenRows(lineLen, width int) int {
	if lineLen <= width {
		return width
	}
	return ((lineLen-width-1)/width+1)*width + width
}

// screenGo is vim's nv_screengo: gj and gk, and the count form of g$.
//
// It moves by display line, which means it works in wanted columns and not in
// buffer positions: going down a display row is adding the window width to the
// wanted column, and running past the end of a line's rows is stepping to the
// next line with the remainder. dir is 1 for down and -1 for up.
func screenGo(r Request, dir, dist, want int) Result {
	b := r.Buf
	w := screenWidth(r.Opt)
	if w <= 0 {
		// No wrapping: a display line is a buffer line and gj is j.
		if dir > 0 {
			return motionDown(r)
		}
		return motionUp(r)
	}
	lnum := r.From.Line
	lineLen := text.DisplayWidth(b.Line(lnum), r.Opt.TabStop)
	atEnd := want == CurswantEOL
	if atEnd {
		v := dispCol(b, r.From, r.Opt.TabStop)
		want = w - 1
		if v > want {
			want += ((v - w) / w) * w
			want += w
		}
	} else if n := screenRows(lineLen, w); want >= n {
		want = n - 1
	}
	moved := 0
	for ; dist > 0; dist-- {
		if dir < 0 {
			if want >= w {
				want -= w
				moved++
				continue
			}
			if lnum <= 1 {
				break
			}
			lnum--
			lineLen = text.DisplayWidth(b.Line(lnum), r.Opt.TabStop)
			if lineLen > w {
				want += ((lineLen-w-1)/w + 1) * w
			}
			moved++
			continue
		}
		n := screenRows(lineLen, w)
		if want+w < n {
			want += w
			moved++
			continue
		}
		if lnum >= b.LineCount() {
			break
		}
		lnum++
		want %= w
		lineLen = text.DisplayWidth(b.Line(lnum), r.Opt.TabStop)
		moved++
	}
	// A gj that runs out of buffer keeps what it managed, and even a gj that
	// managed nothing still puts the cursor at the wanted column: vim's
	// nv_screengo runs its coladvance whether the walk succeeded or not,
	// which is what makes 2g$ on the last line of a file move to the end of
	// it and beep.
	//
	// What is not reproduced is the beep, and with it the abandoning of a waiting
	// operator: vim's d3gj at the end of a file deletes nothing and this deletes
	// what the partial move covered. Written down here rather than papered over,
	// because expressing it needs a Result that can say "moved, but the command
	// is off", and nothing else in the table needs one.
	p := text.Pos{Line: lnum, Col: coladvance(b.Line(lnum), want, r.Opt.TabStop)}
	kind := KindCharExclusive
	// A walk that started at the end of a line sticks there: vim puts MAXCOL
	// back into the wanted column on the way out, which is what makes $gjgjgj
	// follow the ragged right edge of a wrapped paragraph.
	if atEnd {
		kind = KindCharInclusive
		want = CurswantEOL
	}
	return Result{
		To: p, Kind: kind, Ok: true,
		Curswant: want, Count: moved,
	}
}

// motionScreenDown is gj, and motionScreenUp is gk.
func motionScreenDown(r Request) Result { return screenGo(r, 1, r.Count1(), wantedCol(r)) }

// motionScreenUp is gk.
func motionScreenUp(r Request) Result { return screenGo(r, -1, r.Count1(), wantedCol(r)) }
