package operator

import (
	"bytes"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// PutRequest is one p, P, gp, gP, ]p or [p.
type PutRequest struct {
	// Buf is the buffer, mutated in place.
	Buf *text.Buffer
	// Val is the register's contents, already read. Put takes a Value rather
	// than a register name so that a put from the clipboard, a put of the last
	// insert and a put in a test are one code path.
	Val register.Value
	// At is the cursor.
	At text.Pos
	// Before is P rather than p: charwise, before the cursor instead of after
	// it; linewise, on the line above instead of below.
	Before bool
	// Count is how many copies. A count on a linewise put repeats the lines; a
	// count on a charwise put repeats the text with no separator; a count on a
	// blockwise put repeats each of the block's lines sideways.
	Count int
	// After is gp and gP: leave the cursor after the pasted text instead of on
	// its last character, which is what makes gp p p paste three copies in a
	// row.
	After bool
	// Indent is ]p and [p: reindent the pasted lines to the current line's
	// indent. Linewise only; vim ignores it otherwise.
	Indent bool
	// Trim is zp and zP: put a blockwise register without padding each line
	// out to the block's width, so the pasted text is not a rectangle. It is
	// the other half of zy, which yanks a block without its trailing white
	// space, and it does nothing to a charwise or a linewise register.
	//
	// The left padding stays. Measured on "01", "" and "0123456789" with a
	// ragged block put at column 5: both p and zp reach the column by filling
	// the short lines with spaces, and only p pads the block's own lines out
	// to its width afterwards.
	Trim bool
	// Opt is the settings above, for the indent and the report message.
	Opt Options
}

// Put pastes a register into the buffer.
//
// It is here and not in internal/register because it is a buffer edit with a
// cursor rule, and it is a function of its own and not an Op because p takes no
// motion and cannot be dot-repeated the way d can -- . after p repeats the put,
// which the mode machine's dot record replays as keys.
//
// The three types put differently and each has a corner the oracle has a case
// for: charwise at the end of a line, linewise at the end of the buffer, and
// blockwise onto lines shorter than the cursor's column, which have to be
// padded with spaces first.
func Put(r PutRequest) (Result, error) {
	if r.Buf == nil {
		return Result{}, ErrNoRange
	}
	if r.Val.Empty() {
		return Result{}, ErrEmptyRegister
	}
	if r.Count < 1 {
		r.Count = 1
	}
	r.At = r.Buf.Clamp(r.At)

	switch r.Val.Type {
	case register.TypeLine:
		return putLines(r)
	case register.TypeBlock:
		return putBlock(r)
	default:
		return putChars(r)
	}
}

// putChars is a charwise p or P.
//
// Where the cursor ends up depends on how many lines the register holds, which
// is the rule an implementation gets wrong once and then never again: a
// single-line value leaves the cursor on the LAST character pasted, so y w p
// on "hello world" puts the cursor on the space; a value that spans a line
// break leaves it on the FIRST. gp is one past the end in both cases.
func putChars(r PutRequest) (Result, error) {
	line := r.Buf.Line(r.At.Line)
	ins := r.At
	if !r.Before && len(line) > 0 {
		ins.Col = r.At.Col + charSizeAt(line, r.At.Col)
	}
	if ins.Col > len(line) {
		ins.Col = len(line)
	}

	body := joinLines(r.Val.Lines)
	if len(body) == 0 {
		// do_put returns before the insert when the value has no bytes in it,
		// and the "one past the cursor" step a forward put makes is guarded by
		// the same test. So a put of the empty charwise value "y0" leaves
		// behind changes nothing, records nothing in the changelist and does
		// not move the cursor -- and it is not E353 either, because the
		// register is not empty, it holds an empty line. Run on
		// "hello world\n" with "y0P".
		return Result{Cursor: clampNormal(r.Buf, r.At)}, nil
	}
	full := bytes.Repeat(body, r.Count)
	end := r.Buf.Replace(text.Range{Start: ins, End: ins}, full)

	var cursor text.Pos
	switch {
	case r.After:
		cursor = end
	case len(r.Val.Lines) > 1:
		cursor = ins
	default:
		cursor = text.Pos{Line: end.Line, Col: prevCharCol(r.Buf.Line(end.Line), end.Col)}
	}
	return Result{
		Cursor:  clampNormal(r.Buf, cursor),
		Message: lineMessage((len(r.Val.Lines)-1)*r.Count, r.Opt.Report),
		Keep:    len(r.Val.Lines) > 1,
	}, nil
}

// putLines is a linewise p, P, ]p or [p.
//
// p lands the cursor on the first non-blank of the first line it pasted; gp
// lands it in column 0 of the line after the last one, with no first-non-blank
// step, which is what makes gp of an indented register leave the cursor on a
// tab.
func putLines(r PutRequest) (Result, error) {
	lines := make([][]byte, 0, len(r.Val.Lines)*r.Count)
	for range r.Count {
		for _, l := range r.Val.Lines {
			lines = append(lines, append([]byte(nil), l...))
		}
	}
	if r.Indent {
		reindent(lines, r.Buf.Line(r.At.Line), r.Opt)
	}

	at := r.At.Line
	// A linewise put reaches around a closed fold: p lands after its last line
	// and P before its first, wherever inside it the cursor is. Measured with
	// the fold on lines 3 and 4 and the cursor put on line 4 by ":4", where
	// "p" pastes below delta and "P" above gamma.
	if f := r.Opt.Folds; f != nil {
		if first, last, ok := f.ClosedFold(at); ok {
			at = first
			if !r.Before {
				at = last
			}
		}
	}
	if !r.Before {
		at++
	}
	r.Buf.InsertLines(at, lines)

	cursor := text.Pos{Line: at, Col: beginLine(r.Buf.Line(at))}
	if r.After {
		cursor = text.Pos{Line: at + len(lines), Col: 0}
	}
	return Result{
		Cursor:  clampNormal(r.Buf, cursor),
		Message: lineMessage(len(lines), r.Opt.Report),
		Keep:    true,
	}, nil
}

// reindent is ]p and [p: shift every pasted line by the difference between the
// current line's indent and the first pasted line's, so the block keeps its
// shape and lands where the cursor is. A line that would go past column 0 stops
// there.
func reindent(lines [][]byte, target []byte, opt Options) {
	ts := opt.tabStop()
	if len(lines) == 0 {
		return
	}
	delta := indentWidth(target, ts) - indentWidth(lines[0], ts)
	if delta == 0 {
		return
	}
	for i, l := range lines {
		if len(l) == 0 {
			continue
		}
		want := indentWidth(l, ts) + delta
		if want < 0 {
			want = 0
		}
		lines[i] = setIndent(l, want, ts, opt.ExpandTab)
	}
}

// putBlock inserts a rectangle at the cursor's display column, down as many
// lines as the register holds, growing the buffer when it runs out of them.
//
// Two rules here are vim's and neither is obvious. A line that stops before the
// cursor's column is padded out to it with spaces. And the block's own lines
// are padded on the RIGHT to the register's width, but only when there is text
// after the insertion point on that line or another copy still to come: pasting
// a ragged block at the end of a line leaves no trailing spaces, and pasting it
// into the middle of one lines the following text up.
func putBlock(r PutRequest) (Result, error) {
	ts := r.Opt.tabStop()
	line := r.Buf.Line(r.At.Line)

	target := text.DisplayCol(line, r.At.Col, ts)
	if !r.Before && len(line) > 0 {
		_, w := charLenAt(line, r.At.Col, ts)
		target += w
	}

	var cursorCol int
	for i, blockLine := range r.Val.Lines {
		n := r.At.Line + i
		if n > r.Buf.LineCount() {
			r.Buf.InsertLines(r.Buf.LineCount()+1, [][]byte{{}})
		}
		cur := r.Buf.Line(n)
		width := text.DisplayWidth(cur, ts)

		col := len(cur)
		pad := 0
		if width < target {
			pad = target - width
		} else {
			col = text.ByteColForDisplay(cur, target, ts)
			if d := text.DisplayCol(cur, col, ts); d < target && col < len(cur) {
				// The column falls inside a tab. vim splits it; this puts the
				// text after it, which is a cell or two out on a line whose
				// indent is tabs and the cursor is inside one.
				col += charSizeAt(cur, col)
			}
		}

		// vim's "shortline": nothing follows the insertion point on this line,
		// either because the line was too short to reach it or because it ends
		// exactly there.
		short := pad > 0 || col >= len(cur)
		fillRight := 0
		if !r.Val.ToEOL && !r.Trim {
			if f := r.Val.Width - text.DisplayWidth(blockLine, ts); f > 0 {
				fillRight = f
			}
		}

		var ins []byte
		ins = append(ins, bytes.Repeat([]byte{' '}, pad)...)
		for j := range r.Count {
			ins = append(ins, blockLine...)
			if j < r.Count-1 || !short {
				ins = append(ins, bytes.Repeat([]byte{' '}, fillRight)...)
			}
		}
		if i == 0 {
			cursorCol = col + pad
		}
		r.Buf.Replace(text.Range{Start: text.Pos{Line: n, Col: col}, End: text.Pos{Line: n, Col: col}}, ins)
	}

	// The block went in line by line and vim reports it once, at the top left
	// of the block, column zero whatever column the text landed in.
	r.Buf.ChangedAt(text.Pos{Line: r.At.Line})
	return Result{Cursor: clampNormal(r.Buf, text.Pos{Line: r.At.Line, Col: cursorCol})}, nil
}
