package textobj

import (
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// span is an object mid-computation: vim's oap->start, oap->end and
// oap->inclusive, before the operator has looked at them.
//
// It exists because the last rule every object goes through cannot be applied
// to a half-open range. "The end is in column 1 and the motion is exclusive"
// and "the end is the last character of the line above" are the same set of
// bytes and vim treats them differently, so the inclusive flag has to survive
// until finish() has had its say.
type span struct {
	start     text.Pos
	end       text.Pos
	inclusive bool
	// noAdjust exempts the span from the exclusive-end rule in finish(). The
	// tag objects are the only ones that set it, and vim exempts them the
	// same way, with CA_NO_ADJ_OP_END out of nv_object.
	noAdjust bool
	ok       bool
}

// fail is the answer for "there is no such object here": i( with no bracket
// around the cursor, it outside any tag. Vim beeps and abandons the operator.
func fail() Result { return Result{} }

// finish turns a span into the Result an operator acts on, applying the one
// rule that turns a charwise object into a linewise one.
//
// The rule is vim's, in do_pending_operator(), and it is why di{ over a block
// whose braces are each alone on their line deletes lines rather than joining
// them onto the brace line:
//
//	If the end of an operator is in column one while the motion is
//	charwise and exclusive, put the end after the last character in the
//	previous line. If the start is on or before the first non-blank in its
//	line, the operator becomes linewise.
//
// It is not written per object because vim does not write it per object: the
// same code runs for i{, i(, is, ap and aw, and every one of them produces a
// linewise register in the cases where it fires.
func finish(s *scan, sp span) Result {
	if !sp.ok {
		return fail()
	}

	// vim's adjust_cursor_col(), which nv_object calls on the way out of
	// every text object: an object that left the cursor on the NUL past the
	// end of a line backs up one, and only then does do_pending_operator
	// decide which end is the start.
	//
	// It reads like a cosmetic tidy-up and it is load-bearing for one case:
	// a sentence at the end of the buffer made of nothing but its own
	// terminators. "is" on a buffer holding one "!" ends with the start on
	// the NUL and the cursor one before it, which is a backward span that
	// covers the "!" -- and without this the cursor is on the NUL too, the
	// span is empty and d deletes nothing where vim deletes the character.
	if end := sp.end; end.Col > 0 && end.Col >= len(s.lineAt(end.Line)) {
		save := s.p
		s.at(end)
		s.dec()
		sp.end = s.p
		s.at(save)
	}

	if sp.end.Before(sp.start) {
		// A backward object: vim normalises the two ends in
		// do_pending_operator and the inclusive flag stays with whichever one
		// is now the end. "iw" on the last line of a file when that line is
		// empty is the one that gets here.
		sp.start, sp.end = sp.end, sp.start
	}

	// In visual mode the rule below does not run: "when used in Visual mode it
	// is made characterwise" in the help for i{ and friends is this condition,
	// and it is vim's own -- do_pending_operator asks for !is_VIsual or a
	// 'selection' of "old" before adjusting. So vi{ over a brace block selects
	// the text between the braces and di{ over the same block takes the lines.
	adjusted := false
	if !sp.inclusive && !sp.noAdjust && !s.visual && sp.end.Col == 0 && sp.end.Line > sp.start.Line {
		sp.end.Line--
		save := s.p
		s.at(sp.start)
		indent := s.inindent(0)
		s.at(save)
		if indent {
			return Result{
				Range: text.Range{
					Start: text.Pos{Line: sp.start.Line},
					End:   text.Pos{Line: sp.end.Line},
				},
				Type:        register.TypeLine,
				Ok:          true,
				EndAdjusted: true,
			}
		}
		adjusted = true
		line := s.lineAt(sp.end.Line)
		if len(line) == 0 {
			sp.end.Col = 0
			sp.inclusive = false
		} else {
			sp.end.Col = lastCol(line)
			sp.inclusive = true
		}
	}

	end := sp.end
	line := s.lineAt(end.Line)
	if sp.inclusive {
		end.Col += charLen(line, end.Col)
	}
	// The end may be the NUL past the last character, which is a position vim
	// can stand on and not a byte anyone can operate on. An object that ends
	// there covers nothing, which is not a failure: "iw" on an empty line
	// succeeds with an empty span, d does nothing with it and c opens insert
	// mode on the line.
	if end.Col > len(line) {
		end.Col = len(line)
	}
	if end.Compare(sp.start) < 0 {
		return fail()
	}
	return Result{
		Range:       text.Range{Start: sp.start, End: end},
		Type:        register.TypeChar,
		Ok:          true,
		EndAdjusted: adjusted,
	}
}

// lastCol is the byte column of the last character of a line.
func lastCol(line []byte) int {
	col := len(line) - 1
	for col > 0 && !utf8Start(line[col]) {
		col--
	}
	return col
}

// utf8Start reports whether b begins a character rather than continuing one.
func utf8Start(b byte) bool { return b&0xc0 != 0x80 }

// linewise builds a linewise result over an inclusive range of lines, for the
// two objects that are linewise by definition rather than by the rule above.
func linewise(first, last int) Result {
	if last < first {
		return fail()
	}
	return Result{
		Range: text.Range{
			Start: text.Pos{Line: first},
			End:   text.Pos{Line: last},
		},
		Type: register.TypeLine,
		Ok:   true,
	}
}
