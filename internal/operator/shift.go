package operator

import (
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// applyShift is < and >.
//
// Both are linewise whatever motion completed them, both repeat Count times
// -- V j 3 > shifts two lines by three shiftwidths -- and both leave an empty
// line alone. A line of nothing but blanks is not empty and does get shifted,
// so ">>" on three spaces with sw=4 gives seven, and "<<" on it gives an
// empty line rather than a shorter run of blanks.
func applyShift(r Request) (Result, error) {
	first, last := r.Span.Lines()
	if r.Span.Type == register.TypeBlock {
		// A blockwise < or > shifts the lines by whole shiftwidths from the
		// block's left edge in vim. That is a rule about the block and not
		// about the shift, and until internal/mode has blockwise visual
		// running there is nothing to test it against, so this takes the
		// lines and says so rather than guessing at shift_block.
		first, last = r.Span.Block.First, r.Span.Block.Last
	}
	if first < 1 || last > r.Buf.LineCount() || first > last {
		return Result{}, ErrNoRange
	}

	left := r.Op == OpShiftLeft
	sw := r.Opt.shiftWidth()
	ts := r.Opt.tabStop()

	moved := false
	for n := first; n <= last; n++ {
		line := r.Buf.Line(n)
		if len(line) == 0 {
			continue // vim skips an empty line and only an empty one
		}
		want := shiftIndent(indentWidth(line, ts), sw, r.Count, left, r.Opt.ShiftRound)
		if want == indentWidth(line, ts) {
			continue
		}
		r.Buf.SetLine(n, setIndent(line, want, ts, r.Opt.ExpandTab))
		moved = true
	}
	if !moved {
		noteUnmovedShift(r.Buf, first)
	}

	// One changed_lines() for the whole run, at its first line: 3>> reports
	// line 1 and not line 3.
	r.Buf.ChangedAt(text.Pos{Line: first})

	n := last - first + 1
	msg := ""
	if n > r.Opt.Report {
		verb := ">"
		if left {
			verb = "<"
		}
		msg = plural(n, "line "+verb+"ed", "lines "+verb+"ed") + " " + plural(r.Count, "time", "times")
	}
	return Result{
		Cursor:  text.Pos{Line: first, Col: beginLine(r.Buf.Line(first))},
		Message: msg,
		Keep:    msg != "",
	}, nil
}

// noteUnmovedShift records the change a shift that moved no bytes still makes.
//
// op_shift calls changed_lines() whether or not set_indent found anything to
// do, so "<<" on a line with no indent leaves the buffer modified, moves the
// '. mark and puts an entry in the changelist. Measured on "abc\ndef\n": vim
// comes back with [[{'lnum': 1, 'col': 0, 'coladd': 0}], 1] and 'modified' on,
// and a pvim that recorded nothing put g; and '. on whatever line the session
// had touched before.
//
// text.Buffer records a change when bytes move, and ChangedAt only relocates
// the entry a move already made, so the way to record one from out here is to
// write the line back with the bytes it already has.
func noteUnmovedShift(b *text.Buffer, first int) {
	line := b.Line(first)
	// A buffer of one empty line is either an empty file, vim's ML_EMPTY,
	// which writes zero bytes, or a file holding one newline, and only the
	// rendered bytes tell them apart from here. Writing anything at all takes
	// the first out of that state and DeleteLines is what puts it back, so the
	// empty file gets a delete of its own empty line, which records the same
	// change and still writes nothing.
	if b.LineCount() == 1 && len(line) == 0 && len(b.Bytes()) == 0 {
		b.DeleteLines(1, 1)
		return
	}
	b.Replace(text.Range{
		Start: text.Pos{Line: first},
		End:   text.Pos{Line: first, Col: len(line)},
	}, line)
}

// shiftIndent is vim's shift_line arithmetic, and 'shiftround' is the half of
// it that is easy to get almost right.
//
// Rounding is done by integer division of the OLD indent, not by adding and
// then rounding the result. With sw=3 and shiftround on, an indent of 5 goes to
// 6 under >> and to 3 under <<, and an indent of 3 goes to 6 and to 0: the left
// shift spends one whole amount on removing the remainder before it starts
// counting shiftwidths, which is why 2 goes to 0 and not to a negative number
// that then clamps. All four were run.
func shiftIndent(indent, sw, amount int, left, round bool) int {
	if amount < 1 {
		amount = 1
	}
	if !round {
		if left {
			indent -= sw * amount
			if indent < 0 {
				indent = 0
			}
			return indent
		}
		return indent + sw*amount
	}

	whole := indent / sw
	extra := indent % sw
	if left {
		if extra != 0 {
			amount--
		}
		whole -= amount
		if whole < 0 {
			whole = 0
		}
	} else {
		whole += amount
	}
	return whole * sw
}
