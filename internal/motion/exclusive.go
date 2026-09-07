package motion

import (
	"github.com/pkar/pvim/internal/text"
)

// The two rules under :help exclusive, which silently change what d} and dw
// take at a line boundary and are the reason a from-the-documentation
// implementation of } deletes one line too many for ever.
//
// Both apply only with an operator waiting, only to a charwise exclusive
// motion, and only when the span ends in column one of a later line:
//
// - exclusive-inclusive: the end moves back to the last character of the
// previous line and the motion becomes inclusive. That is why d} on a
// paragraph does not swallow the blank line after it.
// - exclusive-linewise: if, on top of that, the span started at or before
// the first non-blank of its own line, the whole thing becomes linewise.
// That is why d} on the first non-blank of an indented paragraph takes the
// indent with it, and why dw on an empty line deletes the line.
//
// The rules live here rather than with the operator because they are a
// property of the motion and its kind and nothing else, and because a test
// that names them belongs next to the motions they change.

// inIndent is vim's inindent(): is col at or before the first non-blank of the
// line? A line of nothing but blanks answers yes from anywhere in it.
func inIndent(line []byte, col int) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return i >= col
}

// AdjustExclusive applies both rules to an ordered span and returns the span
// and kind an operator should act on.
//
// start and end are the two ends of the motion in buffer order, and end is the
// motion's destination, not one past it: an exclusive span covers up to but
// not including end, and an inclusive one covers end as well.
func AdjustExclusive(b *text.Buffer, start, end text.Pos, k Kind) (text.Pos, text.Pos, Kind) {
	if k != KindCharExclusive || end.Col != 0 || end.Line <= start.Line {
		return start, end, k
	}
	end.Line--
	if inIndent(b.Line(start.Line), start.Col) {
		return start, end, KindLine
	}
	line := b.Line(end.Line)
	end.Col = len(line)
	if end.Col == 0 {
		// The previous line is empty, so there is no last character to move
		// onto and the motion stays exclusive with nothing of that line in it.
		return start, end, KindCharExclusive
	}
	end.Col = prevChar(line, end.Col)
	return start, end, KindCharInclusive
}
