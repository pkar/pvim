package motion

import (
	"strings"

	"github.com/pkar/pvim/internal/text"
)

// The left-right motions, :help left-right-motions.
//
// Two of them are not what they look like. l with an operator waiting does not
// fail at the end of a line, it turns the motion inclusive so that dl on the
// last character deletes it, and d5l on a three-character line deletes all
// three. And <BS> and <Space> cross line boundaries because 'whichwrap' is
// "b,s" out of the box, which makes d<Space> at the end of a line a join.

// oneRight reports the column one character to the right, and whether there
// was one: vim's oneright(), which fails both on the position after the last
// byte and on the last character itself, because normal mode may not sit on
// the position after the last byte.
func oneRight(line []byte, col int) (int, bool) {
	if col >= len(line) {
		return col, false
	}
	n := charLen(line, col)
	if col+n >= len(line) {
		return col, false
	}
	return col + n, true
}

// wraps reports whether 'whichwrap' lets this command cross a line boundary.
func wraps(ww string, flag byte) bool {
	return strings.IndexByte(ww, flag) >= 0
}

// motionLeft is h, <BS> and <Left>. flag is the 'whichwrap' letter that lets
// this one wrap.
func motionLeft(flag byte) Func {
	return func(r Request) Result {
		b, p := r.Buf, r.From
		moved := 0
		noAdjust := false
		n := r.Count1()
		for i := 0; i < n; i++ {
			if p.Col > 0 {
				p.Col = prevChar(b.Line(p.Line), p.Col)
				moved++
				continue
			}
			if wraps(r.Opt.WhichWrap, flag) && p.Line > 1 {
				p.Line--
				line := b.Line(p.Line)
				p.Col = lastChar(line)
				// Deleting or changing across the boundary takes the line
				// separator with it, which vim does by leaving the cursor on
				// the position after the last byte rather than on the last
				// character. :help whichwrap says "be careful".
				if (r.Op == 'd' || r.Op == 'c') && len(line) > 0 {
					p.Col = len(line)
					noAdjust = true
				}
				moved++
				continue
			}
			break
		}
		// Only a beep when nothing moved AND nothing is waiting on the
		// motion. vim's nv_left beeps at the start of the line only under
		// "op_type == OP_NOP && n == cap->count1" and otherwise just breaks
		// out of the loop, so with d, c or y waiting the operator runs over
		// an empty region: dh in column one numbers an undo header over an
		// untouched buffer, yh makes the unnamed register an empty charwise
		// yank, and ch starts insert.
		if moved == 0 && !r.Pending {
			return fail()
		}
		curswant := dispCol(b, p, r.Opt.TabStop)
		if moved == 0 {
			// oneleft() never ran, so vim never set the wanted column.
			curswant = CurswantKeep
		}
		return Result{
			To: p, Kind: KindCharExclusive, Ok: true, NoAdjust: noAdjust,
			Curswant: curswant, Count: moved,
		}
	}
}

// motionRight is l, <Space> and <Right>.
func motionRight(flag byte) Func {
	return func(r Request) Result {
		b, p := r.Buf, r.From
		kind := KindCharExclusive
		moved := 0
		n := r.Count1()
		for i := 0; i < n; i++ {
			line := b.Line(p.Line)
			if col, ok := oneRight(line, p.Col); ok {
				p.Col = col
				moved++
				continue
			}
			if wraps(r.Opt.WhichWrap, flag) && p.Line < b.LineCount() {
				// With an operator waiting, the first step off the end of the
				// line takes the last character rather than the boundary, and
				// only the step after that crosses it. That is vim's "when
				// deleting we also count the NL as a character".
				if r.Pending && kind == KindCharExclusive && len(line) > 0 {
					kind = KindCharInclusive
					continue
				}
				p.Line++
				p.Col = 0
				kind = KindCharExclusive
				moved++
				continue
			}
			if r.Pending && len(line) > 0 {
				kind = KindCharInclusive
			}
			break
		}
		// The same rule as motionLeft, and the exclusive kind is what says
		// nothing moved: with an operator waiting on a line that has
		// characters in it the loop above has already turned the motion
		// inclusive, and the only way out of it still exclusive and still on
		// the starting position is an empty line, where vim runs the operator
		// over the empty region rather than beeping. dl and yl on an empty
		// line number an undo header, cl on one starts insert.
		if moved == 0 && kind == KindCharExclusive && !r.Pending {
			return fail()
		}
		curswant := dispCol(b, p, r.Opt.TabStop)
		if moved == 0 && kind == KindCharExclusive {
			// oneright() never ran, so vim never set the wanted column.
			curswant = CurswantKeep
		}
		return Result{
			To: p, Kind: kind, Ok: true,
			Curswant: curswant, Count: moved,
		}
	}
}

// motionLineStart is 0 and <Home>: the first byte of the line, whatever is in
// it.
func motionLineStart(r Request) Result {
	p := text.Pos{Line: r.From.Line, Col: 0}
	// Not a wanted column of zero: vim's beginline asks for the column to be
	// recomputed from the position, and on a line that starts with a tab that
	// is the tab's last cell.
	return Result{
		To: p, Kind: KindCharExclusive, Ok: true,
		Curswant: dispCol(r.Buf, p, r.Opt.TabStop), Count: 1,
	}
}

// motionFirstNonBlank is ^: vim's beginline(BL_WHITE|BL_FIX), which on a line
// of nothing but blanks lands on the last of them rather than past the end.
func motionFirstNonBlank(r Request) Result {
	b := r.Buf
	p := text.Pos{Line: r.From.Line, Col: firstNonBlank(b.Line(r.From.Line))}
	return Result{
		To: p, Kind: KindCharExclusive, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
	}
}

// motionEndOfLine is $ and <End>. A count moves down first, and a count that
// runs off the end of the buffer fails the whole command rather than stopping
// at the last line, which is the one place $ is stricter than j.
func motionEndOfLine(r Request) Result {
	b := r.Buf
	lnum := r.From.Line + r.Count1() - 1
	if r.Count1() > 1 && r.From.Line >= b.LineCount() {
		// Vim sets the wanted column to the end of the line before it works
		// out that there is no line to move down to, so a 2$ that beeps still
		// leaves a later j at the end of its line.
		return Result{Curswant: CurswantEOL}
	}
	if lnum > b.LineCount() {
		lnum = b.LineCount()
	}
	p := text.Pos{Line: lnum, Col: lastChar(b.Line(lnum))}
	return Result{
		To: p, Kind: KindCharInclusive, Ok: true,
		Curswant: CurswantEOL, Count: r.Count1(),
	}
}

// motionLastNonBlank is g_: the last character of the line that is not a space
// or a tab, count-1 lines down.
func motionLastNonBlank(r Request) Result {
	b := r.Buf
	lnum := r.From.Line + r.Count1() - 1
	if r.Count1() > 1 && r.From.Line >= b.LineCount() {
		return fail()
	}
	if lnum > b.LineCount() {
		lnum = b.LineCount()
	}
	p := text.Pos{Line: lnum, Col: lastNonBlank(b.Line(lnum))}
	return Result{
		To: p, Kind: KindCharInclusive, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
	}
}

// motionLineMiddle is gM: half way along the line's own text, by display
// column, and a percentage of it when a count of 1 to 100 is given. vim's
// nv_g_cmd calls coladvance(linetabsize / 2), so a line five cells wide puts
// the cursor in column three and a thirty-four cell one in column eighteen.
func motionLineMiddle(r Request) Result {
	b := r.Buf
	line := b.Line(r.From.Line)
	width := text.DisplayWidth(line, r.Opt.TabStop)
	want := width / 2
	if n := r.Count; n > 0 && n <= 100 {
		want = width * n / 100
	}
	p := text.Pos{Line: r.From.Line, Col: coladvance(line, want, r.Opt.TabStop)}
	return Result{
		To: p, Kind: KindCharExclusive, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
	}
}

// motionScreenMiddle is gm: half way across the WINDOW, not half way along the
// line, so on a line shorter than half the screen it is the end of the line.
// With no window at all it is the middle of the line, which is the only
// answer left.
func motionScreenMiddle(r Request) Result {
	b := r.Buf
	line := b.Line(r.From.Line)
	want := text.DisplayWidth(line, r.Opt.TabStop) / 2
	if r.Opt.Width > 0 {
		want = leftCol(r) + r.Opt.Width/2
	}
	p := text.Pos{Line: r.From.Line, Col: coladvance(line, want, r.Opt.TabStop)}
	return Result{
		To: p, Kind: KindCharExclusive, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
	}
}

// motionColumn is |: to display column count, counting from one, which is why
// the arithmetic below subtracts one and the option's tabstop decides where a
// tab puts it.
func motionColumn(r Request) Result {
	b := r.Buf
	want := r.Count1() - 1
	line := b.Line(r.From.Line)
	p := text.Pos{Line: r.From.Line, Col: coladvance(line, want, r.Opt.TabStop)}
	return Result{
		To: p, Kind: KindCharExclusive, Ok: true,
		Curswant: want, Count: r.Count1(),
	}
}

// screenWidth is the width of one display line, or zero when the caller has
// not told this package how wide the window is, which is every headless run.
// With no width and with 'nowrap' the g0, g^, g$, gj and gk family are 0, ^,
// $, j and k, which is what the vimrc's own 'nowrap' makes them anyway.
func screenWidth(o Options) int {
	if !o.Wrap || o.Width <= 0 {
		return 0
	}
	return o.Width
}

// rowStart is the display column at which the display row holding virtual
// column v begins.
func rowStart(v, width int) int {
	if width <= 0 || v < width {
		return 0
	}
	return (v-width)/width*width + width
}

// leftCol is the display column in the window's leftmost cell, which is zero
// in a headless run and whenever the window has not scrolled sideways.
func leftCol(r Request) int {
	if r.Ctx == nil {
		return 0
	}
	return r.Ctx.Window.LeftCol
}

// motionDisplayLineStart is g0: the first character of the display line.
func motionDisplayLineStart(r Request) Result {
	b := r.Buf
	w := screenWidth(r.Opt)
	line := b.Line(r.From.Line)
	want := rowStart(dispCol(b, r.From, r.Opt.TabStop), w)
	if w == 0 {
		// With 'nowrap' the display line starts at whatever the window has
		// scrolled to.
		want = leftCol(r)
	}
	p := text.Pos{Line: r.From.Line, Col: coladvance(line, want, r.Opt.TabStop)}
	return Result{
		To: p, Kind: KindCharExclusive, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
	}
}

// motionDisplayFirstNonBlank is g^: the first non-blank of the display line.
func motionDisplayFirstNonBlank(r Request) Result {
	res := motionDisplayLineStart(r)
	line := r.Buf.Line(r.From.Line)
	for res.To.Col < len(line) && (line[res.To.Col] == ' ' || line[res.To.Col] == '\t') {
		col, ok := oneRight(line, res.To.Col)
		if !ok {
			break
		}
		res.To.Col = col
	}
	res.Curswant = dispCol(r.Buf, res.To, r.Opt.TabStop)
	return res
}

// motionDisplayEndOfLine is g$: the last character of the display line, which
// with no wrapping is the last character of the line.
func motionDisplayEndOfLine(r Request) Result {
	b := r.Buf
	w := screenWidth(r.Opt)
	if w == 0 {
		// With 'nowrap' a display line is as wide as the window and stops
		// there, so g$ is the rightmost visible column and not the end of the
		// line. With no window at all -- every headless run -- there is no
		// such column and it is $.
		if !r.Opt.Wrap && r.Opt.Width > 0 {
			lnum, _ := cursorDown(b, r.From.Line, r.Count1()-1)
			line := b.Line(lnum)
			want := leftCol(r) + r.Opt.Width - 1
			p := text.Pos{Line: lnum, Col: coladvance(line, want, r.Opt.TabStop)}
			return Result{
				To: p, Kind: KindCharInclusive, Ok: true,
				Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
			}
		}
		return motionEndOfLine(r)
	}
	line := b.Line(r.From.Line)
	if r.Count1() > 1 {
		res := screenGo(r, 1, r.Count1()-1, CurswantEOL)
		return res
	}
	v := dispCol(b, r.From, r.Opt.TabStop)
	want := w - 1
	if v >= w {
		want += ((v - w) / w) * w
		want += w
	}
	col := coladvance(line, want, r.Opt.TabStop)
	// A character that got split at the end of the display line would put the
	// cursor on the next row, so step back off it.
	if col > 0 && dispCol(b, text.Pos{Line: r.From.Line, Col: col}, r.Opt.TabStop) > want {
		col = prevChar(line, col)
	}
	p := text.Pos{Line: r.From.Line, Col: col}
	return Result{
		To: p, Kind: KindCharInclusive, Ok: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
	}
}
