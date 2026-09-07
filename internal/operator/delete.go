package operator

import (
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// d, c and y are one file because they are one operation seen three ways: work
// out what text the span covers, hand it to a register under vim's rules about
// which registers a delete also writes, and then either remove it, remove it
// and start insert, or leave it alone. Every difference between the three is a
// cursor rule or a message, and both are named below with the keystrokes that
// produced them.

// applyDelete is d, and x, X, D and s once the mode machine has filled in the
// motion.
func applyDelete(r Request) (Result, error) {
	if r.Span.Empty() {
		// Nothing to delete is not an error. vim beeps at an empty region only
		// when 'cpoptions' holds E, which the default does not, and otherwise
		// op_delete runs, finds oap->empty and returns u_save_cursor(): no
		// bytes move, no register is written, and an undo header is numbered
		// all the same. That last part is why "X" in column one leaves
		// undotree().seq_cur at 1 over an untouched buffer.
		return Result{Cursor: r.Span.Range.Start}, nil
	}
	span := promoteDeleteToLinewise(r)

	switch span.Type {
	case register.TypeLine:
		return deleteLines(r, span)
	case register.TypeBlock:
		return deleteBlock(r, span)
	default:
		return deleteChars(r, span)
	}
}

// promoteDeleteToLinewise is op_delete's rule that a charwise delete running
// from a line's indent to the end of another line is really a linewise one.
//
// It is d's alone: 2D on "hello world / second line / third" from column 1
// leaves "third" and a LINEWISE unnamed register, while 2C on the same buffer
// leaves an empty first line and a charwise one. Visual mode is exempt, which
// is why Request carries Visual.
func promoteDeleteToLinewise(r Request) Span {
	s := r.Span
	if s.Type != register.TypeChar || r.Visual {
		return s
	}
	first, last := s.Range.Start.Line, s.Range.End.Line
	if last <= first {
		return s
	}
	if !isInIndent(r.Buf.Line(first), s.Range.Start.Col) {
		return s
	}
	tail := r.Buf.Line(last)
	if s.Range.End.Col > len(tail) {
		return s
	}
	if !whiteOnly(tail[s.Range.End.Col:]) {
		return s
	}
	return Span{
		Type:  register.TypeLine,
		Range: text.Range{Start: text.Pos{Line: first}, End: text.Pos{Line: last}},
	}
}

// deleteChars removes a charwise span.
//
// The cursor lands on the first character that was deleted, backed off onto the
// last character of the line when the delete reached the end of it: ll d$ on
// "hello world" leaves "he" with the cursor on the "e", not one past it.
func deleteChars(r Request, span Span) (Result, error) {
	v := register.Char(splitLines(r.Buf.Text(span.Range))...)
	if err := writeDelete(r, v); err != nil {
		return Result{}, err
	}

	removed := span.Range.End.Line - span.Range.Start.Line
	r.Buf.Delete(span.Range)
	noteCharDelete(r.Buf, span.Range)
	return Result{
		Cursor:  clampNormal(r.Buf, span.Range.Start),
		Message: lineMessage(-removed, r.Opt.Report),
		Keep:    removed != 0,
	}, nil
}

// noteCharDelete reports a characterwise delete the way vim does.
//
// One that stays inside a line is a byte change where it happened, which is
// what Replace already recorded. One that crosses a line break is not: vim
// truncates the first line, deletes the whole ones and then joins what is left
// back on, and that join is a line delete on the line after the start. "d/line"
// with the match on line 2 reports line 2, not the line 1 the cursor was on.
func noteCharDelete(b *text.Buffer, r text.Range) {
	if r.End.Line > r.Start.Line {
		b.ChangedAt(text.Pos{Line: r.Start.Line + 1})
	}
}

// deleteLines removes whole lines.
//
// The cursor goes to the first non-blank of whatever line is now under it, one
// line up when the delete took the end of the buffer with it: dd on the last of
// three lines leaves the cursor on line 2. Emptying the buffer says so and says
// nothing else, and unlike the "N fewer lines" it replaces it does not survive
// the redraw: "ggdG" leaves one "--No lines in buffer--" in the redirect where
// "3dd" leaves two of its own message.
func deleteLines(r Request, span Span) (Result, error) {
	first, last := span.Lines()
	v := register.LineValue(copyLines(r.Buf, first, last)...)
	if err := writeDelete(r, v); err != nil {
		return Result{}, err
	}

	n := last - first + 1
	emptied := n >= r.Buf.LineCount()
	r.Buf.DeleteLines(first, last)

	at := first
	if at > r.Buf.LineCount() {
		at = r.Buf.LineCount()
	}
	msg := lineMessage(-n, r.Opt.Report)
	keep := msg != ""
	if emptied {
		msg, keep = "--No lines in buffer--", false
	}
	return Result{
		Cursor:  text.Pos{Line: at, Col: beginLine(r.Buf.Line(at))},
		Message: msg,
		Keep:    keep,
	}, nil
}

// deleteBlock removes a rectangle. A line the block never reaches is left
// exactly as it was, and a tab the block cuts in half comes back as the spaces
// that were outside it.
func deleteBlock(r Request, span Span) (Result, error) {
	blk := span.Block
	ts := r.Opt.tabStop()

	yanked := make([][]byte, 0, blk.Last-blk.First+1)
	for n := blk.First; n <= blk.Last; n++ {
		yanked = append(yanked, blockYankLine(r.Buf.Line(n), blk, ts))
	}
	v := register.BlockValue(blockWidth(r.Buf, blk, ts), yanked...)
	v.ToEOL = blk.ToEOL
	if err := writeDelete(r, v); err != nil {
		return Result{}, err
	}

	// Back to front, so that an earlier line's edit cannot move a later one.
	for n := blk.Last; n >= blk.First; n-- {
		start, end, fill, ok := blockDeleteLine(r.Buf.Line(n), blk, ts)
		if !ok {
			continue
		}
		r.Buf.Replace(text.Range{Start: text.Pos{Line: n, Col: start}, End: text.Pos{Line: n, Col: end}}, fill)
	}

	col, _ := blockInsertCol(r.Buf.Line(blk.First), blk, ts)
	return Result{Cursor: clampNormal(r.Buf, text.Pos{Line: blk.First, Col: col})}, nil
}

// applyChange is c, and s, S and C once the motion is filled in.
func applyChange(r Request) (Result, error) {
	if r.Span.Empty() {
		// op_change deletes and then starts insert whatever the delete found,
		// so "c0" in column one opens an insert on a hole of no width and says
		// "-- INSERT --". See applyDelete for the empty half.
		//
		// Except that op_delete's empty-line shortcut, the one that skips the
		// yank, is guarded by op_type == OP_DELETE, so a change never takes
		// it: C on a blank line still writes the register, with the empty
		// charwise value the region holds. The two zero-width spans are told
		// apart by which motion built them, which is what EndsOnEmptyLine
		// carries -- $ is inclusive and 0 is exclusive, and only the exclusive
		// one sets vim's oap->empty. Measured on "alpha beta\n\ngamma\n":
		// "ywjCX<Esc>p" leaves line 2 as "X" because the C emptied the
		// register, and "ywjc0X<Esc>p" pastes the yanked word back.
		// ...and not on a buffer that is one empty line, where op_delete's
		// ML_EMPTY return comes first and nothing is written at all: "cE" on
		// an empty file leaves the unnamed register as untouched as it was,
		// where "C" on an empty line inside a file makes it charwise empty.
		emptyBuf := r.Buf.LineCount() == 1 && len(r.Buf.Line(1)) == 0
		if r.Span.EndsOnEmptyLine && !emptyBuf {
			if err := writeDelete(r, register.Char(splitLines(r.Buf.Text(r.Span.Range))...)); err != nil {
				return Result{}, err
			}
		}
		return Result{Cursor: r.Span.Range.Start, Insert: true}, nil
	}
	switch r.Span.Type {
	case register.TypeLine:
		return changeLines(r)
	case register.TypeBlock:
		return changeBlock(r)
	default:
		v := register.Char(splitLines(r.Buf.Text(r.Span.Range))...)
		if err := writeDelete(r, v); err != nil {
			return Result{}, err
		}
		removed := r.Span.Range.End.Line - r.Span.Range.Start.Line
		r.Buf.Delete(r.Span.Range)
		noteCharDelete(r.Buf, r.Span.Range)
		return Result{
			Cursor:  r.Buf.Clamp(r.Span.Range.Start),
			Insert:  true,
			Message: lineMessage(-removed, r.Opt.Report),
			Keep:    removed != 0,
		}, nil
	}
}

// changeLines is the c that surprises people: a linewise change does not
// delete the lines, it replaces all of them with ONE empty line and starts
// insert on it. With 'autoindent' or 'smartindent' that line keeps the first
// line's indent, and without either it is empty and the cursor is in column 0.
//
// Measured on "\tone / (four spaces)two / \tthree / four": j cc with autoindent
// leaves " " and the cursor in column 5, and without it leaves "" and the
// cursor in column 1. c j with autoindent turns the first two lines into one
// "\t" and reports "1 line less".
func changeLines(r Request) (Result, error) {
	first, last := r.Span.Lines()
	v := register.LineValue(copyLines(r.Buf, first, last)...)
	if err := writeDelete(r, v); err != nil {
		return Result{}, err
	}

	var indent []byte
	if r.Opt.AutoIndent || r.Opt.SmartIndent {
		line := r.Buf.Line(first)
		indent = append(indent, line[:indentEnd(line)]...)
	}
	// vim's op_delete does a change over more than one line in two steps and
	// reports both, in this order: del_lines() takes the lines after the first
	// and reports the line it started at, and only then the first line's own
	// text goes and reports that. Only the changelist can tell, and only when
	// an undo block was owed an entry: "I x CTRL-U Esc 2S" leaves two entries
	// at {1,0} in vim where one report leaves one.
	//
	// The order is what a single splice over the whole range gets wrong, not
	// just the count: the splice reports the first line first, and a
	// changelist entry the block before it left on that line is overwritten
	// rather than kept behind a new one. Measured with
	// "S CTRL-U \"0 Esc \"Ac+", which leaves {1,1} and {1,0} in vim and left
	// {1,0} twice here.
	if last > first {
		r.Buf.DeleteLines(first+1, last)
	}
	r.Buf.SetLine(first, indent)

	return Result{
		Cursor:  text.Pos{Line: first, Col: len(indent)},
		Insert:  true,
		Message: lineMessage(-(last - first), r.Opt.Report),
		Keep:    last != first,
	}, nil
}

// changeBlock deletes the rectangle and leaves insert running at its left edge
// on the first line. Repeating the inserted text down the other lines happens
// when insert ends, and that is the mode machine's: only it knows what was
// typed and whether the insert was abandoned.
func changeBlock(r Request) (Result, error) {
	res, err := deleteBlock(r, r.Span)
	if err != nil {
		return Result{}, err
	}
	col, _ := blockInsertCol(r.Buf.Line(r.Span.Block.First), r.Span.Block, r.Opt.tabStop())
	res.Cursor = r.Buf.Clamp(text.Pos{Line: r.Span.Block.First, Col: col})
	res.Insert = true
	return res, nil
}

// applyYank is y, and Y once the mode machine has made the span linewise.
//
// SpanValue is the text a span covers, as a register value of the span's own
// type. It is what a yank stores, without the yank: 'clipboard' containing
// "autoselect" writes the visual selection to the clipboard every time the
// selection changes, and that is not a yank -- no register is named, "0 is not
// touched, and the unnamed alias does not move.
func SpanValue(b *text.Buffer, span Span, opt Options) register.Value {
	first, last := span.Lines()
	switch span.Type {
	case register.TypeLine:
		return register.LineValue(copyLines(b, first, last)...)
	case register.TypeBlock:
		blk := span.Block
		ts := opt.tabStop()
		yanked := make([][]byte, 0, last-first+1)
		for n := blk.First; n <= blk.Last; n++ {
			yanked = append(yanked, blockYankLine(b.Line(n), blk, ts))
		}
		v := register.BlockValue(blockWidth(b, blk, ts), yanked...)
		v.ToEOL = blk.ToEOL
		return v
	default:
		return register.Char(splitLines(b.Text(span.Range))...)
	}
}

// The cursor rule is two rules. A charwise yank moves the cursor to the start
// of what it took, so $ y b lands on the "w" of "world". A linewise yank moves
// only the LINE: w yy on "hello world" leaves the cursor in column 7, which an
// implementation that assigns the span start clobbers.
// An empty region is not an error to y and not a no-op either. op_yank has no
// shortcut for one, so it runs, and the register comes back holding a single
// empty line: getregtype('"') answers "v" where it answered "" before, and the
// yank before it is gone. Measured on "hello world\n" with "y0P", which prints
// no message at all and pastes nothing, where refusing the span printed
// "E353: Nothing in register "" and left the previous yank in place. Nothing
// below needs a special case for it: Lines() gives one line, SpanValue gives
// the empty value and yankMessage says nothing about a one-line charwise yank.
func applyYank(r Request) (Result, error) {
	first, last := r.Span.Lines()
	lines := last - first + 1

	v := SpanValue(r.Buf, r.Span, r.Opt)
	if r.TrimTrailing && v.Type == register.TypeBlock {
		v.Lines = trimTrailingWhite(v.Lines)
	}
	var cursor text.Pos
	switch r.Span.Type {
	case register.TypeLine:
		cursor = text.Pos{Line: first, Col: r.At.Col}
	case register.TypeBlock:
		blk := r.Span.Block
		col, _ := blockInsertCol(r.Buf.Line(blk.First), blk, r.Opt.tabStop())
		cursor = text.Pos{Line: blk.First, Col: col}
	default:
		cursor = r.Span.Range.Start
	}

	if r.Regs != nil {
		if err := r.Regs.Yank(r.Register, v); err != nil {
			return Result{}, err
		}
	}
	return Result{Cursor: clampNormal(r.Buf, cursor), Message: yankMessage(r, lines)}, nil
}

// trimTrailingWhite takes the spaces and tabs off the end of every line, which
// is the whole of what "zy" does that "y" does not.
//
// A copy, because the lines came out of the buffer and shortening them in
// place would shorten the buffer.
func trimTrailingWhite(lines [][]byte) [][]byte {
	out := make([][]byte, len(lines))
	for i, l := range lines {
		n := len(l)
		for n > 0 && (l[n-1] == ' ' || l[n-1] == '\t') {
			n--
		}
		out[i] = l[:n]
	}
	return out
}

// yankMessage is op_yank's report. A charwise yank of one line says nothing at
// all, whatever 'report' is, because vim zeroes the count first; every other
// yank of more than 'report' lines names them, and a named register is spelled
// out with an opening quote and no closing one: "a 3 y y prints
// `3 lines yanked into "a`.
func yankMessage(r Request, lines int) string {
	if r.Span.Type == register.TypeChar && lines == 1 {
		return ""
	}
	if lines <= r.Opt.Report {
		return ""
	}
	msg := plural(lines, "line yanked", "lines yanked")
	if r.Span.Type == register.TypeBlock {
		msg = "block of " + plural(lines, "line yanked", "lines yanked")
	}
	if r.Register != 0 && r.Register != register.Unnamed {
		msg += ` into "` + string(r.Register)
	}
	return msg
}

// writeDelete hands the text to the register file under the rules a delete
// follows, which are not the rules a yank follows: the unnamed register always,
// then "- for a delete within one line and "1 with the numbered shift for
// anything bigger. internal/register works both out from the value itself; the
// one thing it cannot see is the motion, so Request.NumberedRegister goes with
// it. See :help quote_number for the ten motions that force "1.
func writeDelete(r Request, v register.Value) error {
	if r.Regs == nil {
		return nil
	}
	return r.Regs.Delete(r.Register, v, r.NumberedRegister)
}

// copyLines returns copies of lines first through last. Copies, because a
// register outlives the buffer edit that filled it and text.Buffer's own line
// slices do not.
func copyLines(b *text.Buffer, first, last int) [][]byte {
	out := make([][]byte, 0, last-first+1)
	for n := first; n <= last; n++ {
		out = append(out, append([]byte(nil), b.Line(n)...))
	}
	return out
}
