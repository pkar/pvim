package operator

import (
	"bytes"
	"unicode"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// applyCase is gu, gU, g~ and g?, which are one operator with four maps over
// the same span. None of them writes a register and none of them moves the
// cursor anywhere but the start of what they changed -- for a linewise span
// that is column 0 and not the first non-blank, which gU j on an indented line
// puts on the tab.
func applyCase(r Request) (Result, error) {
	m := caseMapper(r.Op)
	if r.Span.Empty() {
		return caseEmpty(r, m)
	}
	first, last := r.Span.Lines()

	switch r.Span.Type {
	case register.TypeLine:
		changed := false
		for n := first; n <= last; n++ {
			changed = setChanged(r.Buf, n, mapCase(r.Buf.Line(n), m)) || changed
		}
		// One changed_lines() for the run, at its first line: "3g~j" reports
		// line 1 and not the last line it flipped -- and none at all when
		// nothing flipped, because vim's op_tilde reports on did_change.
		// "g~~" on a line with no letters in it numbers an undo header and
		// leaves getchangelist() empty.
		if changed {
			r.Buf.ChangedAt(text.Pos{Line: first})
		}
		// The cursor keeps the column vim's oap->start had, which for a
		// linewise motion is not zero. See Span.OpStart.
		at := text.Pos{Line: first}
		if r.Span.OpStart.Line == first {
			at = r.Span.OpStart
		}
		return Result{
			Cursor:  clampNormal(r.Buf, at),
			Message: changedMessage(last-first+1, r.Opt.Report),
		}, nil

	case register.TypeBlock:
		blk := r.Span.Block
		ts := r.Opt.tabStop()
		for n := blk.First; n <= blk.Last; n++ {
			line := r.Buf.Line(n)
			start, end, _, ok := blockDeleteLine(line, blk, ts)
			if !ok || end <= start {
				continue
			}
			r.Buf.Replace(text.Range{
				Start: text.Pos{Line: n, Col: start},
				End:   text.Pos{Line: n, Col: end},
			}, mapCase(line[start:end], m))
		}
		col, _ := blockInsertCol(r.Buf.Line(blk.First), blk, ts)
		return Result{
			Cursor:  clampNormal(r.Buf, text.Pos{Line: blk.First, Col: col}),
			Message: changedMessage(blk.Last-blk.First+1, r.Opt.Report),
		}, nil

	default:
		old := r.Buf.Text(r.Span.Range)
		if now := mapCase(old, m); !bytes.Equal(old, now) {
			r.Buf.Replace(r.Span.Range, now)
		}
		return Result{
			Cursor:  clampNormal(r.Buf, r.Span.Range.Start),
			Message: changedMessage(last-first+1, r.Opt.Report),
		}, nil
	}
}

// setChanged rewrites a line only when the text is actually different.
//
// vim's op_tilde tracks did_change and calls changed_lines() only when
// something moved, so "guu" on a line that is already lower case leaves the
// changelist alone while still numbering an undo header. Rewriting a line with
// the same bytes would put an entry in the changelist that vim does not have.
func setChanged(b *text.Buffer, n int, now []byte) bool {
	if bytes.Equal(b.Line(n), now) {
		return false
	}
	b.SetLine(n, now)
	return true
}

// caseEmpty is gu, gU or g~ over a region with nothing in it, which vim does
// not refuse and does not leave alone either.
//
// op_tilde takes an exclusive region and steps its end back one with dec(),
// then, because the region is on one line as far as the line count is
// concerned, changes end.col - start.col + 1 characters starting at start. On
// an empty region that arithmetic is a bug with three answers, all measured:
// from column one of line one, dec() cannot move and one character changes, so
// "gU0" on "abc" gives "Abc"; from column one of any other line dec() lands on
// the END of the line above and the count comes out longer than the line, so
// "2GgU0" upper-cases the whole of line two; and from any other column the
// count is zero and nothing happens at all. Reproduced rather than corrected,
// because the oracle is the specification.
func caseEmpty(r Request, m func(rune) rune) (Result, error) {
	at := r.Span.Range.Start
	if r.Span.Type != register.TypeChar {
		return Result{Cursor: clampNormal(r.Buf, at)}, nil
	}
	end := at.Col - 1
	switch {
	case at.Col > 0:
	case at.Line > 1:
		end = len(r.Buf.Line(at.Line - 1))
	default:
		end = 0
	}
	line := r.Buf.Line(at.Line)
	if end < at.Col || at.Col >= len(line) {
		return Result{Cursor: clampNormal(r.Buf, at)}, nil
	}
	stop := min(end+1, len(line))
	span := text.Range{
		Start: text.Pos{Line: at.Line, Col: at.Col},
		End:   text.Pos{Line: at.Line, Col: stop},
	}
	if now := mapCase(line[at.Col:stop], m); !bytes.Equal(line[at.Col:stop], now) {
		r.Buf.Replace(span, now)
	}
	return Result{Cursor: clampNormal(r.Buf, at)}, nil
}

// changedMessage is op_tilde's report, which counts the lines the span covers
// and not the characters it altered: gU G over six lines of which four already
// hold capitals still says "6 lines changed".
func changedMessage(lines, report int) string {
	if lines <= report {
		return ""
	}
	return plural(lines, "line changed", "lines changed")
}

// caseMapper picks the rune map for an operator.
func caseMapper(op Op) func(rune) rune {
	switch op {
	case OpLower:
		return unicode.ToLower
	case OpUpper:
		return unicode.ToUpper
	case OpRot13:
		return rot13
	default:
		return toggleCase
	}
}

// mapCase applies m to every rune of b and returns a new slice. Newlines pass
// through untouched, which matters because a charwise span across a line break
// arrives here as one byte slice with an LF in it.
func mapCase(b []byte, m func(rune) rune) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size == 1 {
			// An invalid byte is not a character and vim leaves it alone
			// rather than turning it into U+FFFD.
			out = append(out, b[i])
			i++
			continue
		}
		out = utf8.AppendRune(out, m(r))
		i += size
	}
	return out
}

// toggleCase is g~: upper becomes lower, lower becomes upper, and anything
// with no case at all is left as it is.
func toggleCase(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.ToLower(r)
	}
	if unicode.IsLower(r) {
		return unicode.ToUpper(r)
	}
	return r
}

// rot13 is g?, which vim applies to the 52 ASCII letters and nothing else. Not
// to the accented letters, not to Cyrillic: there is no thirteenth letter along
// from those and vim does not invent one.
func rot13(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z':
		return 'a' + (r-'a'+13)%26
	case r >= 'A' && r <= 'Z':
		return 'A' + (r-'A'+13)%26
	default:
		return r
	}
}
