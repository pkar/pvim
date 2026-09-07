package motion

import (
	"github.com/pkar/pvim/internal/text"
)

// ` and ', the two halves of one motion: the same mark, read as a position or
// as a line. That is the whole difference and it is the one people trip over,
// because d'a deletes whole lines and d`a does not.

// markName resolves the character typed after ' or ` to the name the buffer
// stores it under. Both ' and ` mean the position before the latest jump, and
// vim accepts either after either.
func markName(arg byte) (byte, bool) {
	switch {
	// A-Z as well as a-z. An uppercase mark names a file in vim and there is
	// only one buffer here, so what is left is the position, which behaves
	// exactly as a lowercase mark does. The visible half of this is the error:
	// an uppercase name that was never set has to be "E20: Mark not set" and
	// not "E78: Unknown mark", because E78 is what vim says for a character
	// that is not a mark name at all.
	case arg >= 'a' && arg <= 'z', arg >= 'A' && arg <= 'Z':
		return arg, true
	case arg == '\'' || arg == '`':
		return text.MarkLastJump, true
	case arg == '.':
		return text.MarkLastChange, true
	case arg == '^':
		return text.MarkLastInsert, true
	case arg == '[':
		return text.MarkChangeStart, true
	case arg == ']':
		return text.MarkChangeEnd, true
	}
	return 0, false
}

// motionMark is ` when line is false and ' when it is true.
func motionMark(line bool) Func {
	return func(r Request) Result {
		b := r.Buf
		name, ok := markName(r.Arg)
		if !ok {
			return failMsg("E78: Unknown mark")
		}
		p, ok := b.Mark(name)
		if !ok {
			return failMsg("E20: Mark not set")
		}
		p = b.Clamp(p)
		if line {
			p = text.Pos{Line: p.Line, Col: firstNonBlank(b.Line(p.Line))}
			return Result{
				To: p, Kind: KindLine, Ok: true, Jump: true,
				Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
			}
		}
		// Normal mode may not sit after the last byte of a line, which a mark
		// left at the end of one does.
		if line := b.Line(p.Line); p.Col > 0 && p.Col >= len(line) {
			p.Col = prevChar(line, len(line))
		}
		return Result{
			To: p, Kind: KindCharExclusive, Ok: true, Jump: true,
			Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
		}
	}
}
