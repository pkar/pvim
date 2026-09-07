package operator

import (
	"unicode/utf8"

	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// SpanForMotion resolves a motion into the span an operator acts on.
//
// This is where vim's inclusive, exclusive and forced-motion rules meet, and
// they meet nowhere else. In order:
//
// 1. The force, if the user typed v, V or CTRL-V between the operator and the
// motion, replaces the motion's kind outright. See motion.Force.Apply.
// 2. A linewise kind gives a linewise span of the two lines, inclusive.
// 3. An inclusive kind gives a charwise span that covers the character at To,
// which means the half-open end is the position after it, and "the position
// after it" is one character wide and not one byte.
// 4. An exclusive kind gives a charwise span up to To. Then the two
// adjustments from :help exclusive, which apply only with an operator
// pending: if the end is in column 1 it moves to the end of the previous
// line and the motion becomes inclusive; and if it also started at or
// before the first non-blank of its own line, the whole thing becomes
// linewise instead. That second rule is why d} on a paragraph leaves no
// blank line and a naive implementation leaves one.
//
// start is where the cursor was when the operator was typed, which is not
// always before m.To: a backward motion gives a span the other way round and
// swapping them is part of the job.
//
// Rule 4 was checked both ways on this machine. In " aaa bbb / ?? ccc / (a
// blank line) / ?? ddd", d} from column 1 or column 3 -- the first non-blank
// -- deletes two whole lines and the register comes back linewise; d} from
// column 5 deletes "a bbb\n ccc" and the register comes back charwise. Both
// end in column 1 of the blank line; the only difference is whether the start
// is in the indent.
func SpanForMotion(b *text.Buffer, start text.Pos, m motion.Result, f motion.Force, opt Options) (Span, error) {
	if b == nil {
		return Span{}, ErrNoRange
	}
	if !m.Ok {
		return Span{}, ErrNoRange
	}
	kind := f.Apply(m.Kind)
	start = b.Clamp(start)
	end := b.Clamp(m.To)
	backward := end.Before(start)
	if backward {
		start, end = end, start
	}
	// Folds first, and then the four rules below, because vim runs them in
	// that order and the order shows: a closed fold moves the start to column
	// one, and rule 4 then finds the start at or before the first non-blank
	// and makes a span linewise that would have been charwise from where the
	// cursor really was. fold.go has the two keystroke sequences that pin it.
	start, end, kind = foldExtendMotion(b, start, end, kind, backward, opt.Folds)
	// Every branch below carries it, because vim's oap->start is one position
	// whatever the motion turned out to be. See Span.OpStart.
	at := start

	switch kind {
	case motion.KindLine:
		return Span{
			Type:         register.TypeLine,
			Range:        text.Range{Start: text.Pos{Line: start.Line}, End: text.Pos{Line: end.Line}},
			OpStart:      at,
			FoldAdjusted: true,
		}, nil

	case motion.KindBlock:
		ts := opt.tabStop()
		left := text.DisplayCol(b.Line(start.Line), start.Col, ts)
		right := text.DisplayCol(b.Line(end.Line), end.Col, ts)
		if right < left {
			left, right = right, left
		}
		return Span{
			Type:         register.TypeBlock,
			Block:        Block{First: start.Line, Last: end.Line, Left: left, Right: right},
			OpStart:      at,
			FoldAdjusted: true,
		}, nil

	case motion.KindCharInclusive:
		// An inclusive motion covers the character it landed on, and on an
		// empty line there is no character to cover. Widening the span to one
		// column anyway is not a rounding error: it is the difference between
		// op_delete's "Check for trying to delete (e.g. \"D\") in an empty
		// line" early return and a delete that runs and overwrites the unnamed
		// register with an empty charwise value. Measured with
		// "ywj$d$p" on "alpha beta\n\ngamma\n": vim pastes the yw back onto
		// line 2 because the d$ never touched the register.
		tail := b.Line(end.Line)
		size := charSizeAt(tail, end.Col)
		end.Col += size
		return Span{
			Type:            register.TypeChar,
			Range:           text.Range{Start: start, End: end},
			EndsOnEmptyLine: size == 0 && len(tail) == 0,
			OpStart:         at,
			FoldAdjusted:    true,
		}, nil

	default: // motion.KindCharExclusive
		// The two adjustments from :help exclusive. Both need the span to
		// cross a line: an exclusive motion that ends in column 0 of its own
		// starting line has nothing to give back.
		if end.Col == 0 && end.Line > start.Line {
			if isInIndent(b.Line(start.Line), start.Col) {
				return Span{
					Type:         register.TypeLine,
					Range:        text.Range{Start: text.Pos{Line: start.Line}, End: text.Pos{Line: end.Line - 1}},
					OpStart:      at,
					FoldAdjusted: true,
					// vim sets oap->end_adjusted before it decides between
					// linewise and charwise, so this half carries it too:
					// "gq}" from column one leaves the cursor on the blank
					// line after the paragraph.
					EndAdjusted: true,
				}, nil
			}
			// Exclusive becomes inclusive: the end moves onto the last
			// character of the previous line, and a half-open end that
			// includes that character is the line's length. An empty previous
			// line has no character to include and stays at column 0, which
			// its length already is -- and that is the one end in column 0
			// that does cover the line it names, so it is flagged. vim
			// decrements oap->line_count here, once, and counts the blank line
			// from then on: "ly2)" over a paragraph ending in one says
			// "3 lines yanked".
			end = text.Pos{Line: end.Line - 1, Col: len(b.Line(end.Line - 1))}
			return Span{
				Type:            register.TypeChar,
				Range:           text.Range{Start: start, End: end},
				EndsOnEmptyLine: end.Col == 0,
				EndAdjusted:     true,
				OpStart:         at,
				FoldAdjusted:    true,
			}, nil
		}
		return Span{Type: register.TypeChar, Range: text.Range{Start: start, End: end}, OpStart: at, FoldAdjusted: true}, nil
	}
}

// isInIndent is vim's inindent(0): everything on the line before col is white
// space, so the cursor is sitting in the indent or on the first non-blank.
func isInIndent(line []byte, col int) bool {
	if col > len(line) {
		col = len(line)
	}
	for i := range col {
		if !blank(line[i]) {
			return false
		}
	}
	return true
}

// SpanForLines is the dd and yy case: an operator's own key typed twice, or a
// count with no motion, which is linewise over count lines from the cursor.
func SpanForLines(b *text.Buffer, first, count int) Span {
	last := first + count - 1
	if count < 1 {
		last = first
	}
	if n := b.LineCount(); last > n {
		last = n
	}
	return Span{
		Type:  register.TypeLine,
		Range: text.Range{Start: text.Pos{Line: first}, End: text.Pos{Line: last}},
	}
}

// SpanForChars is x and s: charwise over count characters forward from at,
// stopping at the end of the line however large the count is. 99x on a
// three-character line deletes three characters and does not reach the next
// one, which is the whole reason this is not "d" with an l motion.
func SpanForChars(b *text.Buffer, at text.Pos, count int) Span {
	at = clampNormal(b, at)
	line := b.Line(at.Line)
	end := at.Col
	for range max(count, 1) {
		if end >= len(line) {
			break
		}
		end += charSizeAt(line, end)
	}
	return Span{Type: register.TypeChar, Range: text.Range{Start: at, End: text.Pos{Line: at.Line, Col: end}}}
}

// SpanForCharsBefore is X: charwise over count characters backward from at,
// stopping at column 0. X in column 0 covers nothing, and an operator handed
// that span writes no register and beeps, which is what vim does.
//
// The span comes back already fold-adjusted, which for X means untouched: with
// a closed fold over lines 1 and 2, "2lzfjX" deletes the "l" of "alpha" and
// leaves the fold standing, where "2lzfjx" one column along takes both lines.
// Every backward charwise operator measured does the same -- dh, d0, dFa -- so
// this is the rule and not X's own quirk.
func SpanForCharsBefore(b *text.Buffer, at text.Pos, count int) Span {
	at = clampNormal(b, at)
	line := b.Line(at.Line)
	start := at.Col
	for range max(count, 1) {
		if start <= 0 {
			break
		}
		start = prevCharCol(line, start)
	}
	return Span{Type: register.TypeChar, Range: text.Range{Start: text.Pos{Line: at.Line, Col: start}, End: at}, FoldAdjusted: true}
}

// SpanForToEndOfLine is D and C: charwise from at to the end of the line, and
// with a count to the end of the count-th line down. It is d$ and c$ with the
// motion filled in, so it inherits everything $ does, including that a count
// runs off the end of the buffer by clamping rather than failing.
func SpanForToEndOfLine(b *text.Buffer, at text.Pos, count int) Span {
	at = b.Clamp(at)
	last := at.Line + max(count, 1) - 1
	if n := b.LineCount(); last > n {
		last = n
	}
	return Span{
		Type:            register.TypeChar,
		Range:           text.Range{Start: at, End: text.Pos{Line: last, Col: len(b.Line(last))}},
		EndsOnEmptyLine: len(b.Line(last)) == 0,
	}
}

// prevCharCol returns the byte column the character before col starts at.
//
// Backwards, one rune at a time, until the character starting there reaches
// col: the extra steps are the zero-width marks hanging off a base rune, which
// belong to the character in front of them and not to their own position.
func prevCharCol(line []byte, col int) int {
	if col <= 0 {
		return 0
	}
	if col > len(line) {
		col = len(line)
	}
	for i := col; i > 0; {
		_, size := utf8.DecodeLastRune(line[:i])
		i -= size
		if i+charSizeAt(line, i) >= col {
			return i
		}
	}
	return 0
}
