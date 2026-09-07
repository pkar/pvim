package operator

import (
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// Folds is the fold tree of the window an operator is running in, as much of
// it as this package needs: which closed fold covers a line, and here is a new
// one.
//
// It is an interface for the same reason Registers is one, plus a layering
// reason. internal/screen owns the fold tree, because a fold is a window's and
// not a buffer's and because what a closed fold looks like is a drawing
// question; internal/doc.go says the mode machine runs with no display at all,
// so this package cannot import it and cannot name a *screen.Fold. Two methods
// it can name are enough, and Create is spelled the way screen.Folds already
// spells it so that the caller's adapter is only the other one:
//
//	type windowFolds struct{ *screen.Folds }
//
//	func (w windowFolds) ClosedFold(line int) (first, last int, ok bool) {
//		if f:= w.ClosedAt(line); f != nil {
//			return f.Start, f.End, true
//		}
//		return 0, 0, false
//	}
//
// A nil Folds is a window with no folds in it: zf records its range in the
// Result and nothing else, and every rule below is skipped. That is what a
// test and a caller with no window pass, and it is why adding folds to the
// editor changes nothing for anybody who has not wired one in.
type Folds interface {
	// ClosedFold reports the closed fold covering line, first and last
	// inclusive. ok is false for a line in no fold and for a line in a fold
	// that is open, because an open fold changes nothing an operator does.
	ClosedFold(line int) (first, last int, ok bool)
	// Create makes a manual fold over the inclusive line range and closes it,
	// which is zf. It reports whether one was made; vim refuses a fold of one
	// line under 'foldmethod' manual and so does screen.Folds.
	Create(first, last int) bool
}

// The fold rules an operator obeys, all of them measured on
// /opt/homebrew/bin/vim 9.2.0321 over "alpha beta gamma delta epsilon", one
// word per line, with folds made by zf and the result read back through
// foldclosed(), getreg() and getregtype().
//
// :help fold-behavior states the rule in one sentence -- "When using an
// operator, a closed fold is included as a whole. Thus "dl" deletes the whole
// closed fold under the cursor" -- and the sentence is not the whole rule.
// What was measured, with the fold on lines 1 and 2 and the cursor on line 1
// unless the keys say otherwise:
//
//	zfjdd		takes both lines, @" is "alpha\nbeta\n" and linewise
//	zfjx		the same, and so do zfjdw, zfjdl, zfjdfa, zfjd$, zfjD,
//			zfjdiw and zfjsX: a charwise operator that reaches into a
//			closed fold comes out linewise over the whole of it
//	zfjcwX		leaves one line "X": c is not promoted to linewise, so it
//			deletes the fold's text charwise and the lines merge
//	2lzfjdh		deletes "l" and leaves the fold alone. So do 2lzfjX,
//			2lzfjd0 and 2lzfjdFa. A BACKWARD charwise operator does not
//			see the fold at all, whether it is inclusive or exclusive,
//			and whether the fold holds its start or its end
//	jjzfjdk		with the fold on 3 and 4 and the cursor on 3 takes lines
//			2, 3 and 4: a backward LINEWISE operator does see it
//	jjzfjggd/gamma	with the fold on 3 and 4 stops at line 2. An exclusive
//			motion that ends in column 1 of the fold's first line has
//			given that column back before the fold is looked at, so
//			there is nothing of the fold in the span and it stays out
//	2lzfjy/gamma	yanks "alpha\nbeta\n" LINEWISE from column 3. The fold
//			moves the start to column 1, and vim's exclusive rule then
//			finds the start at or before the first non-blank and makes
//			the whole thing linewise, which it would not have done
//			from column 3. The order matters: folds first, then the
//			rules in SpanForMotion
//	jzf2j:3<CR>dd	with the fold on 2 to 4 takes all three lines, from a
//			cursor vim put inside the fold rather than on its first
//			line, which only: and a mark can do
//	jjzfjggvjjd	visual, and the selection is extended too, by the same
//			two ends: "alpha\nbeta\ngamma\ndelta\n" CHARWISE, the
//			trailing newline included, because 'selection' is
//			"inclusive" and the fold's end lands one past the last
//			character
//	3jzfjvd		the same at the end of the buffer, where there is no
//			newline to take: it leaves an empty line behind
//	yyjjzfj:4<CR>p	puts after line 4 and P before line 3, wherever in the
//			fold the cursor is
//
// The one shape here that is not reproduced is a blockwise operator, forced or
// visual: "jjzfjggd<C-v>jj" with the fold on 3 and 4 leaves gamma and delta
// alone and takes a block two lines tall, which is neither the block that was
// typed nor the fold. A forced blockwise span goes through untouched; the
// visual block, which was measured and does take the fold, is in
// foldExtendSpan.

// foldLines widens an inclusive line range to cover every closed fold it
// touches. It is the whole of the rule for a linewise operator and half of it
// for every other one.
func foldLines(f Folds, first, last int) (int, int) {
	if f == nil {
		return first, last
	}
	if a, _, ok := f.ClosedFold(first); ok {
		first = a
	}
	if _, z, ok := f.ClosedFold(last); ok {
		last = z
	}
	return first, last
}

// foldExtendMotion is the fold pass SpanForMotion runs before it resolves
// anything, which is where vim runs it: the exclusive-motion rules read the
// start's column and a fold has already moved it to column one by then.
//
// backward says the motion ran the other way, which SpanForMotion knows from
// the positions before it swaps them. A backward charwise operator is measured
// not to see folds at all; a backward linewise one does.
func foldExtendMotion(b *text.Buffer, start, end text.Pos, kind motion.Kind, backward bool, f Folds) (text.Pos, text.Pos, motion.Kind) {
	if f == nil || kind == motion.KindBlock {
		return start, end, kind
	}
	if backward && kind != motion.KindLine {
		return start, end, kind
	}
	if first, _, ok := f.ClosedFold(start.Line); ok {
		start = text.Pos{Line: first, Col: 0}
	}
	// An exclusive motion that stops in column 1 covers nothing on the line it
	// names, so a fold that starts there is none of its business: with the
	// fold on lines 3 and 4, "d/gamma" from line 1 takes lines 1 and 2.
	if kind == motion.KindCharExclusive && end.Col == 0 {
		return start, end, kind
	}
	_, last, ok := f.ClosedFold(end.Line)
	if !ok {
		return start, end, kind
	}
	if kind == motion.KindLine {
		return start, text.Pos{Line: last}, kind
	}
	// vim sets oap->end to the fold's last character and oap->inclusive with
	// it, which is what turns "d/gamma/e" into a delete of whole lines: the
	// span now runs from an indent to the end of a line, and op_delete's own
	// rule makes that linewise. See promoteDeleteToLinewise.
	line := b.Line(last)
	return start, text.Pos{Line: last, Col: prevCharCol(line, len(line))}, motion.KindCharInclusive
}

// foldExtendSpan is the same pass for a span that no motion resolved: dd and
// yy, x and s, D and C, and every visual-mode selection. Apply runs it, once,
// and FoldAdjusted says whether it already has.
//
// Visual mode is a different rule and not a missing one. vim's
// foldAdjustVisual puts the end one past the fold's last character rather than
// on it, because 'selection' is "inclusive", so a charwise visual delete over a
// closed fold takes the line break as well and the lines close up; the same
// delete from normal mode leaves the lines merged into one. Both were measured
// and they really do differ.
func foldExtendSpan(b *text.Buffer, s Span, f Folds, visual bool, tabstop int) Span {
	if f == nil || s.FoldAdjusted {
		return s
	}
	s.FoldAdjusted = true

	switch s.Type {
	case register.TypeLine:
		first, last := foldLines(f, s.Range.Start.Line, s.Range.End.Line)
		s.Range.Start.Line, s.Range.End.Line = first, last
		return s

	case register.TypeBlock:
		if !visual {
			return s
		}
		if first, _, ok := f.ClosedFold(s.Block.First); ok {
			s.Block.First, s.Block.Left = first, 0
		}
		if _, last, ok := f.ClosedFold(s.Block.Last); ok {
			s.Block.Last = last
			if !s.Block.ToEOL {
				s.Block.Right = text.DisplayCol(b.Line(last), len(b.Line(last)), tabstop)
			}
		}
		return s

	default:
		if first, _, ok := f.ClosedFold(s.Range.Start.Line); ok {
			s.Range.Start = text.Pos{Line: first, Col: 0}
		}
		// Lines() is the last line the span covers and not the line its end
		// names: a charwise end in column 1 stops before the line it points
		// at, and a fold starting there is not part of the span.
		_, covered := s.Lines()
		if s.Range.End.Col == 0 && !s.EndsOnEmptyLine && covered < s.Range.End.Line {
			return s
		}
		_, last, ok := f.ClosedFold(covered)
		if !ok {
			return s
		}
		if visual && last < b.LineCount() {
			// One past the fold's last character is the start of the next
			// line, and a half-open end says that in the only way it can.
			s.Range.End = text.Pos{Line: last + 1, Col: 0}
			s.EndsOnEmptyLine = false
			return s
		}
		s.Range.End = text.Pos{Line: last, Col: len(b.Line(last))}
		s.EndsOnEmptyLine = len(b.Line(last)) == 0
		return s
	}
}

// foldsWiden reports whether the fold pass runs for this operator at all.
//
// It runs for everything vim puts through do_pending_operator, which is every
// operator in this package except one: normal mode's J. vim's nv_join hands
// the command to nv_operator when a Visual selection is up and calls do_join
// itself when there is not, so the fold widening -- which lives in
// do_pending_operator -- is skipped for exactly the second of those.
//
// Measured with the fold on lines 2 to 4 of "alpha beta gamma delta epsilon",
// one word per line, made with "jzf2j":
//
//	J		joins beta and gamma; delta stays a line of its own
//	ggJ		joins alpha and beta, one line out of a closed fold
//	:4<CR>J		joins delta and epsilon, from a cursor inside the fold
//	gg3J		joins alpha, beta and gamma: the count is lines
//	ggVjJ		joins all four, because the Visual selection came first
//
// against dd, yy, x, D, C, >>, gUU and gqq over that same fold, every one of
// which takes three lines.
//
// The Visual case needs Request.Visual set by the caller, which is the same
// flag op_delete's linewise promotion reads. A caller that leaves it false
// gets normal mode's answer.
func foldsWiden(op Op, visual bool) bool {
	if op == OpJoin {
		return visual
	}
	return true
}
