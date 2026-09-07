package mode

import (
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/textobj"
)

// visualSpan is a selection remembered: what gv brings back and what '< and
// '> name. The mode is part of it because reselecting a block selection as a
// charwise one would be a different selection.
type visualSpan struct {
	start, end text.Pos
	mode       Mode
	// curswant is what the selection's right edge was aiming for, which is the
	// whole of what makes a $ block a $ block after gv.
	curswant int
	ok       bool
}

// startVisual enters one of the three visual modes.
//
// A count means vim's {count}v, which selects that many characters, lines or
// columns rather than starting empty. :help v says the size of the previous
// selection is multiplied by the count; measured on this vim, 3v with a
// three-character selection behind it selects three characters and not nine,
// so this takes the count as a size and says so rather than implementing a
// sentence that the editor disagrees with.
func (e *Editor) startVisual(m Mode, count int) error {
	if e.mode == m {
		// v in charwise visual leaves visual mode, and so on for the other two.
		return e.escape()
	}
	if e.mode == VisualChar || e.mode == VisualLine || e.mode == VisualBlock {
		e.mode = m
		e.finish(false)
		return nil
	}
	e.visual = e.cur
	e.mode = m
	if count > 1 {
		e.extendBy(m, count)
	}
	e.finish(false)
	return nil
}

// visualMessage is what the message line says in each of the three visual
// modes, which the oracle diffs.
func visualMessage(m Mode) string {
	switch m {
	case VisualLine:
		return "-- VISUAL LINE --"
	case VisualBlock:
		return "-- VISUAL BLOCK --"
	default:
		return "-- VISUAL --"
	}
}

// extendBy is the {count} on v, V and CTRL-V.
func (e *Editor) extendBy(m Mode, count int) {
	switch m {
	case VisualLine:
		last := e.cur.Line + count - 1
		if n := e.buf.LineCount(); last > n {
			last = n
		}
		e.cur.Line = last
	default:
		line := e.buf.Line(e.cur.Line)
		col := e.cur.Col
		for i := 1; i < count && col < len(line); i++ {
			col = nextRune(line, col)
		}
		e.cur.Col = col
	}
}

// reselect is gv: the last selection, in the mode it was made in.
func (e *Editor) reselect() error {
	if !e.lastVis.ok {
		return e.beep()
	}
	e.visual = e.buf.Clamp(e.lastVis.start)
	e.cur = e.buf.Clamp(e.lastVis.end)
	e.mode = e.lastVis.mode
	e.mctx.Curswant = e.lastVis.curswant
	e.finish(false)
	return nil
}

// endVisual leaves visual mode, remembering the selection for gv and for the
// '< and '> marks. The ends are stored in buffer order, so '< is never after
// '>, which is what vim guarantees and what an ex range depends on.
//
// They are kept here and not in the buffer's mark table because
// internal/text's validMark takes a-z and the five automatic marks and refuses
// these two. Nothing below this package needs them; LastVisual is how the ex
// layer will ask.
func (e *Editor) endVisual() {
	start, end := e.selection()
	e.lastVis = visualSpan{start: start, end: end, mode: e.mode, curswant: e.mctx.Curswant, ok: true}
}

// LastVisual is the selection '< and '> name: its two ends in buffer order and
// whether there has been one. gv reselects it and the ex layer's :'<,'> range
// reads it.
func (e *Editor) LastVisual() (start, end text.Pos, ok bool) {
	return e.lastVis.start, e.lastVis.end, e.lastVis.ok
}

// selection is the two ends of the current selection in buffer order.
func (e *Editor) selection() (start, end text.Pos) {
	if e.cur.Compare(e.visual) < 0 {
		return e.cur, e.visual
	}
	return e.visual, e.cur
}

// visualCommand dispatches a complete key sequence in one of the visual modes.
//
// An operator here does not wait for a motion: the selection is the span, and
// its own kind is the span's kind, which is why V j d takes whole lines and
// v j d does not.
func (e *Editor) visualCommand(s string) error {
	n := e.pend.count()
	n1 := n
	if n1 < 1 {
		n1 = 1
	}

	switch s {
	case "v":
		return e.startVisual(VisualChar, n)
	case "V":
		return e.startVisual(VisualLine, n)
	case "<C-V>":
		return e.startVisual(VisualBlock, n)
	case "gv":
		return e.swapWithLast()
	case "o":
		e.visual, e.cur = e.cur, e.visual
		e.finish(false)
		return nil
	case "O":
		return e.swapCorners()

	case "d", "x", "<Del>":
		return e.visualOperator(operator.OpDelete)
	case "c", "s":
		return e.visualOperator(operator.OpChange)
	case "y":
		return e.visualOperator(operator.OpYank)
	case "zy":
		// vim 9's yank without trailing white space. Over a charwise or
		// linewise selection it is exactly "y"; over a block it drops the
		// spaces and tabs at the end of each line.
		e.pend.trimYank = true
		return e.visualOperator(operator.OpYank)
	case "<":
		return e.visualOperator(operator.OpShiftLeft)
	case ">":
		return e.visualOperator(operator.OpShiftRight)
	case "=":
		return e.visualOperator(operator.OpIndent)
	case "u":
		return e.visualOperator(operator.OpLower)
	case "U":
		return e.visualOperator(operator.OpUpper)
	case "~", "g~":
		return e.visualOperator(operator.OpToggle)
	case "g?":
		return e.visualOperator(operator.OpRot13)
	case "gu":
		return e.visualOperator(operator.OpLower)
	case "gU":
		return e.visualOperator(operator.OpUpper)
	case "gq":
		return e.visualOperator(operator.OpFormat)
	case "gw":
		return e.visualOperator(operator.OpFormatKeep)
	case "zf":
		return e.visualOperator(operator.OpFold)

	// D, C, X, S, R and Y are the linewise shorthands: whatever the selection
	// was, they take whole lines.
	case "D", "X":
		return e.visualLinewise(operator.OpDelete)
	case "C", "S", "R":
		return e.visualLinewise(operator.OpChange)
	case "Y":
		return e.visualLinewise(operator.OpYank)

	case "J", "gJ":
		return e.visualJoin(s == "J")
	case "r":
		e.wait = func(k key.Key) error { return e.visualReplace(k) }
		return nil
	case "p", "P":
		return e.visualPut(n1, s == "P")
	case "I", "A":
		return e.blockInsert(s == "A")
	case "i", "a":
		inner := s == "i"
		e.wait = func(k key.Key) error { return e.visualObject(k, inner) }
		return nil
	case ":":
		e.finish(false)
		return ErrNotImplemented
	}

	if m, ok := motion.ByKeys(s); ok {
		return e.startMotion(m)
	}
	if isPrefix(s) {
		return nil
	}
	switch s {
	case "/":
		return e.startSearch(searchForward)
	case "?":
		return e.startSearch(searchBackward)
	case "n", "N":
		return e.searchNext(s == "n", n1)
	case "*", "#", "g*", "g#":
		return e.searchWord(s, n1)
	}
	return e.unknown(s)
}

// swapWithLast is gv typed while a selection is up: vim swaps the current
// selection with the previous one.
func (e *Editor) swapWithLast() error {
	if !e.lastVis.ok {
		return e.beep()
	}
	prev := e.lastVis
	e.endVisual()
	e.visual = e.buf.Clamp(prev.start)
	e.cur = e.buf.Clamp(prev.end)
	e.mode = prev.mode
	e.finish(false)
	return nil
}

// swapCorners is O: the other two corners of a block. In charwise and linewise
// visual it is the same as o, which is what vim does.
func (e *Editor) swapCorners() error {
	if e.mode != VisualBlock {
		e.visual, e.cur = e.cur, e.visual
		e.finish(false)
		return nil
	}
	a, b := e.visual, e.cur
	a.Col, b.Col = b.Col, a.Col
	e.visual, e.cur = a, b
	e.finish(false)
	return nil
}

// visualSpanNow builds the span the selection covers.
//
// The three kinds are three different shapes and the block is the only one
// that is measured in display columns, because a block down a file of tabs is
// a rectangle on the screen and not a rectangle of bytes.
func (e *Editor) visualSpanNow() operator.Span {
	start, end := e.selection()
	switch e.mode {
	case VisualLine:
		return operator.Span{
			Type:  register.TypeLine,
			Range: text.Range{Start: text.Pos{Line: start.Line}, End: text.Pos{Line: end.Line}},
		}
	case VisualBlock:
		ts := e.opt.TabStop
		l1 := e.buf.Line(e.visual.Line)
		l2 := e.buf.Line(e.cur.Line)
		aLeft := text.DisplayCol(l1, e.visual.Col, ts)
		bLeft := text.DisplayCol(l2, e.cur.Col, ts)
		aRight := text.VirtCol(l1, e.visual.Col, ts) - 1
		bRight := text.VirtCol(l2, e.cur.Col, ts) - 1
		left, right := aLeft, aRight
		if bLeft < left {
			left = bLeft
		}
		if bRight > right {
			right = bRight
		}
		return operator.Span{
			Type: register.TypeBlock,
			Block: operator.Block{
				First: start.Line, Last: end.Line,
				Left: left, Right: right,
				ToEOL: e.mctx.Curswant == motion.CurswantEOL,
			},
		}
	default:
		// Charwise visual is inclusive of the character the cursor is on, and
		// a Range is half-open, so the end moves on by one character.
		//
		// The exception is $: a selection made with it runs to the end of the
		// line and takes the line break with it, so v$d on the first of three
		// lines joins the second one up. Measured, and the register vim leaves
		// behind ends in a newline, which is what says the break was taken.
		line := e.buf.Line(end.Line)
		switch {
		case e.mctx.Curswant == motion.CurswantEOL && end.Line < e.buf.LineCount():
			end = text.Pos{Line: end.Line + 1}
		case end.Col < len(line):
			end.Col = nextRune(line, end.Col)
		default:
			end.Col = len(line)
		}
		return operator.Span{Type: register.TypeChar, Range: text.Range{Start: start, End: end}}
	}
}

// visualOperator applies an operator to the selection and leaves visual mode.
//
// The selection's own kind is the span's kind, which is the rule that makes
// Vjd take whole lines and vjd not, and it is why nothing here consults the
// motion that made the selection.
func (e *Editor) visualOperator(op operator.Op) error {
	span := e.visualSpanNow()
	at := e.operatorAt(op, span)
	if op.Changes() {
		e.recordVisualDot(op, 0)
	}
	e.endVisual()
	e.mode = Normal
	e.pend.op = op
	// A count in visual mode belongs to the operator and not to the
	// selection: 3> shifts the selected lines by three shiftwidths.
	n := e.pend.count()
	if n < 1 {
		n = 1
	}
	return e.runOperatorCount(span, n, at)
}

// operatorAt is where the cursor was when a visual-mode operator was typed,
// which two operators read: a linewise yank and zf both keep that column.
//
// The rule is not "the start of the selection" and it is not "the cursor". It
// is column zero unless the selection covers more than one line AND the cursor
// is on the first of them, in which case the cursor's own column is kept.
// Measured over thirteen shapes on a three-line file: llVy and llVhy and
// llVjy all leave the cursor in column 1, while jllVky and jllVkhy and
// llVjoy -- the three where the cursor ended up on the top line of a
// multi-line selection -- keep the column they were in.
//
// zf answers the same, because it is the same oap->start: on " abcdef"
// three times over, Vzf and Vjzf leave the cursor in column 1 and Vkzf leaves
// it in column 6. gq and gw are not on the list; op_format saves the cursor
// for itself before do_pending_operator moves it.
// It is the span's kind and not the selection's that picks the rule: v h Y and
// v j Y both leave the cursor in column 1 because Y made the span linewise,
// while v j y leaves it on the selection's own start. A blockwise span is
// handed back the cursor untouched, because the corner it wants is worked out
// from the block by internal/operator, whose Block.Left is a display column
// and not the byte column this package deals in.
func (e *Editor) operatorAt(op operator.Op, span operator.Span) text.Pos {
	at := e.cur
	if op != operator.OpYank && op != operator.OpFold {
		return at
	}
	switch span.Type {
	case register.TypeChar:
		return span.Range.Start
	case register.TypeBlock:
		return at
	}
	start, end := e.selection()
	if end.Line == start.Line || e.cur.Line != start.Line {
		at.Col = 0
	}
	return at
}

// visualLinewise is D, C, X, S, R and Y in visual mode: the selection's lines,
// whatever kind of selection it was.
func (e *Editor) visualLinewise(op operator.Op) error {
	start, end := e.selection()
	lines := operator.Span{
		Type:  register.TypeLine,
		Range: text.Range{Start: text.Pos{Line: start.Line}, End: text.Pos{Line: end.Line}},
	}
	at := e.operatorAt(op, lines)
	if op.Changes() {
		// The shape . repeats is the selection and not the D, and it is a
		// linewise one whatever the selection was: vim records vjX as V over
		// two whole lines. Without this the repeat replays a bare "D" in
		// normal mode, which is a different command on one line.
		was := e.mode
		e.mode = VisualLine
		e.recordVisualDot(op, 0)
		e.mode = was
	}
	e.endVisual()
	e.mode = Normal
	e.pend.op = op
	return e.runOperatorCount(lines, 1, at)
}

// visualJoin is J on a selection: every line of it joined into one.
func (e *Editor) visualJoin(spaces bool) error {
	start, end := e.selection()
	e.endVisual()
	e.mode = Normal
	e.cur = text.Pos{Line: start.Line}
	last := end.Line
	if last == start.Line {
		last = start.Line + 1
	}
	if last > e.buf.LineCount() {
		return e.beep()
	}
	return e.join(spaces, last-start.Line+1)
}

// visualObject is iw, ap and the rest typed with a selection up: the object is
// found around the cursor and the selection is extended to cover it.
func (e *Editor) visualObject(k key.Key, inner bool) error {
	if k == keyEsc {
		return e.abandon()
	}
	if !k.IsRune() || k.Rune > 0x7f {
		return e.beep()
	}
	e.cmdKeys = append(e.cmdKeys, k)
	obj, ok := textobj.ByKey(byte(k.Rune))
	if !ok {
		return e.beep()
	}
	start, end := e.selection()
	res := obj.Find(textobj.Request{
		Buf:    e.buf,
		At:     e.cur,
		Count:  e.pend.count(),
		Inner:  inner,
		Arg:    byte(k.Rune),
		Sel:    text.Range{Start: start, End: end},
		HasSel: start != end,
		Opt:    e.opt.TextObj(),
	})
	if !res.Ok {
		return e.beep()
	}
	e.visual = res.Range.Start
	e.cur = res.Range.End
	if e.cur.Col > 0 {
		line := e.buf.Line(e.cur.Line)
		e.cur.Col = prevRune(line, min(e.cur.Col, len(line)))
	}
	if res.Type == register.TypeLine {
		e.mode = VisualLine
	}
	e.cur = e.buf.Clamp(e.cur)
	e.finish(false)
	return nil
}

// visualReplace is r on a selection: every character in it becomes the one
// typed, and a block replaces only the columns it covers.
func (e *Editor) visualReplace(k key.Key) error {
	if k == keyEsc {
		return e.abandon()
	}
	if !k.IsRune() {
		return e.beep()
	}
	rep := runeBytes(k)
	span := e.visualSpanNow()
	if k.IsRune() && k.Rune < 0x80 {
		e.recordVisualDot(operator.OpNone, byte(k.Rune))
	}
	start, _ := e.selection()
	if span.Type == register.TypeLine {
		// op_replace zeroes oap->start.col for a linewise region before it
		// puts the cursor back, so lVjrz lands in column 1 and not in the
		// column the selection was started in. Measured, indent and all: on
		// " aaaa", wVjrz still reports column 1 rather than the first
		// non-blank.
		start.Col = 0
	}
	e.endVisual()
	e.mode = Normal

	e.openUndo(start)
	e.rememberLine()
	switch span.Type {
	case register.TypeLine:
		for ln := span.Range.Start.Line; ln <= span.Range.End.Line; ln++ {
			e.buf.SetLine(ln, fillRunes(e.buf.Line(ln), 0, len(e.buf.Line(ln)), rep))
		}
		// One report for the whole region, on its first line, because
		// op_replace hands changed_lines() the start it has just zeroed the
		// column of. Without it the last SetLine names the last line and
		// lVjrz reports line 2 where vim reports line 1.
		e.buf.ChangedAt(text.Pos{Line: span.Range.Start.Line})
	case register.TypeBlock:
		ts := e.opt.TabStop
		for ln := span.Block.First; ln <= span.Block.Last; ln++ {
			line := e.buf.Line(ln)
			from := text.ByteColForDisplay(line, span.Block.Left, ts)
			to := len(line)
			if !span.Block.ToEOL {
				to = text.ByteColForDisplay(line, span.Block.Right+1, ts)
			}
			if from >= len(line) {
				continue
			}
			e.buf.SetLine(ln, fillRunes(line, from, to, rep))
		}
		// One report for the whole block, at its top left corner -- and the
		// corner has a column. op_replace hands changed_lines() oap->start.col
		// where a line-oriented command hands it zero, so the changelist entry
		// and the '. mark land on the block's left edge. Measured:
		// l CTRL-V jjlrz reports column 1 and 3G$ CTRL-V kkhrz reports 3.
		e.buf.ChangedAt(text.Pos{
			Line: span.Block.First,
			Col:  text.ByteColForDisplay(e.buf.Line(span.Block.First), span.Block.Left, e.opt.TabStop),
		})
	default:
		r := span.Range
		if r.Start.Line == r.End.Line {
			e.buf.SetLine(r.Start.Line, fillRunes(e.buf.Line(r.Start.Line), r.Start.Col, r.End.Col, rep))
		} else {
			for ln := r.Start.Line; ln <= r.End.Line; ln++ {
				line := e.buf.Line(ln)
				from, to := 0, len(line)
				if ln == r.Start.Line {
					from = r.Start.Col
				}
				if ln == r.End.Line {
					to = r.End.Col
				}
				e.buf.SetLine(ln, fillRunes(line, from, to, rep))
			}
		}
	}
	e.closeUndo()
	e.moveTo(start)
	e.finish(true)
	return nil
}

// visualPut is p or P over a selection: the selection is deleted and the
// register put in its place.
//
// It is vim's nv_put_opt() and not do_put(), which is the distinction that
// matters here: three of the four rules below are decided by the mode machine
// before the put runs, from what the visual mode was and where the delete left
// the cursor, and none of them is visible to a put that only sees a register
// and a position.
//
//	p the delete fills the registers, so a second p pastes what the
//	 first one took out.
//	P the delete goes to the black hole. :help v_P: "Unlike v_p, the
//	 previously selected text is not put into any register", which is
//	 what makes a second P paste the same text again. vim spells it
//	 "cap->oap->regname = keep_registers ? '_' : NUL".
//	V the register goes in as whole lines whatever type it is, so that a
//	 charwise register put over a linewise selection becomes its own
//	 line instead of being joined onto the one below. That is vim's
//	 PUT_LINE flag.
//	dir backward unless the delete left the cursor BEFORE where the
//	 selection started, which is what happens when the selection ran to
//	 the end of the line or to the end of the buffer and there is
//	 nothing left to put in front of.
//
// Measured on "alpha beta / second line / third one": jVjyVjp leaves the
// buffer unchanged, because the delete of the last two lines leaves the cursor
// on line 1 and the lines go back after it; yljVp gives "alpha beta / a /
// third one" and not "alpha beta / athird one"; and yljvlP leaves @" holding
// the "a" that yl yanked rather than the "se" that the put replaced.
//
// Not done here, and not this function's to do: vim's PUT_LINE_SPLIT, which
// breaks the line in two when a LINEWISE register is put over a CHARWISE
// selection. That one lives inside do_put(), which is internal/operator.
func (e *Editor) visualPut(count int, keepRegisters bool) error {
	// The register is read before the delete, which is what normally keeps a
	// visual put pasting what was yanked and not what it just replaced. Under
	// 'clipboard' containing "unnamed" the name resolves to "*, and with
	// "autoselect" alongside it that register already holds the selection --
	// so the paste puts back what was there. See Editor.autoSelect.
	name := e.pend.reg
	if name == 0 {
		name = e.regs.UnnamedName()
	}
	val, err := e.regs.Get(name)
	if err != nil || val.Empty() {
		return e.beep()
	}
	span := e.visualSpanNow()
	start, _ := e.selection()
	vmode := e.mode
	e.endVisual()
	e.mode = Normal

	// P must not fill a register, so its delete is aimed at the black hole,
	// which register.File.Delete drops without shifting "1 or writing "- or
	// the unnamed alias or the clipboard.
	delReg := byte(0)
	if keepRegisters {
		delReg = register.BlackHole
	}

	e.openUndo(start)
	e.rememberLine()
	del, err := operator.Apply(operator.Request{
		Buf: e.buf, Regs: e.regs, Op: operator.OpDelete,
		Span: span, Register: delReg, Count: 1, Opt: e.opt.Operator(),
	})
	if err != nil {
		e.closeUndo()
		e.beep()
		return err
	}

	at := e.buf.Clamp(del.Cursor)
	if vmode == VisualLine && val.Type != register.TypeLine {
		val = register.LineValue(val.Lines...)
	}
	res, err := operator.Put(operator.PutRequest{
		Buf: e.buf, Val: val, At: at, Before: putBefore(vmode, start, at),
		Count: count, Opt: e.opt.Operator(),
	})
	e.closeUndo()
	if err != nil {
		e.beep()
		return err
	}
	e.moveTo(res.Cursor)
	e.finish(true)
	return nil
}

// putBefore is the direction a visual put goes in, which is vim's
//
//	dir = BACKWARD;
//	if ((VIsual_mode != 'V' && curwin->w_cursor.col < curbuf->b_op_start.col)
//	 || (VIsual_mode == 'V' && curwin->w_cursor.lnum < curbuf->b_op_start.lnum))
//	 dir = FORWARD;
//
// where b_op_start is where the selection began and the cursor is where the
// delete left it. Those two are the same position for a selection in the
// middle of a buffer, and the register goes in in front of it; a delete that
// took the end of a line or the last lines of the buffer has nowhere to leave
// the cursor but before that, and then the register goes in after it instead.
//
// Only the two coordinates vim looks at are looked at here, one per mode: a
// linewise put compares lines and every other kind compares columns, even
// across a line, which is what vim does.
func putBefore(vmode Mode, start, at text.Pos) bool {
	if vmode == VisualLine {
		return at.Line >= start.Line
	}
	return at.Col >= start.Col
}
