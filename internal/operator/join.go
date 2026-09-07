package operator

import (
	"github.com/pkar/pvim/internal/text"
)

// applyJoin is J and gJ.
//
// gJ concatenates the lines byte for byte. J does five things instead, and
// every one of them was run through vim rather than read out of the help,
// because the help leaves two of them out:
//
// - The next line's leading white space goes first.
// - One space is inserted between the two, unless the joined-so-far text
// already ends in a space or a tab, unless the next line is empty once its
// white space is gone, unless the next line starts with ')', and unless
// nothing has been accumulated yet.
// - A second space goes in when the text ends in '.', and also after '!' and
// '?' unless 'cpoptions' holds 'j'. This is 'joinspaces', and 'joinspaces'
// is ON under vim --clean on this machine, so "The end." + "next" gives
// "The end. next" by default and not one space.
// - The two-space rule looks THROUGH a trailing space: "end. " + "next"
// gives "end. next", one space kept and one added, because vim replaces
// the end character with the one before it when the end is a space.
// - The cursor lands where the last join happened, which is the length of
// everything before the final piece, clamped onto the line.
func applyJoin(r Request) (Result, error) {
	first, last := r.Span.Lines()
	if last <= first {
		last = first + 1
	}
	if n := r.Buf.LineCount(); last > n {
		last = n
	}
	if first < 1 || first >= last {
		// J on the last line of the buffer has nothing to join to. vim beeps
		// and leaves the cursor alone.
		return Result{}, ErrNoRange
	}

	joined, cursorCol, pieces := joinWithSpaces(copyLines(r.Buf, first, last), r.Spaces, r.Opt)

	// The marks and the changelist entries on the lines being swallowed move
	// along the joined line rather than dying with it. Before the splice,
	// because after it the lines they are on are gone. See Buffer.JoinMarks.
	for t := len(pieces) - 1; t >= 1; t-- {
		r.Buf.JoinMarks(first+t, -t, pieces[t].start, pieces[t].stripped, pieces[t].gap)
	}

	head := len(r.Buf.Line(first))
	r.Buf.Replace(text.Range{
		Start: text.Pos{Line: first, Col: 0},
		End:   text.Pos{Line: last, Col: len(r.Buf.Line(last))},
	}, joined)
	// vim joins by rewriting the first line and then deleting the ones that
	// went into it, and reports both: do_join's own changed_lines names the
	// end of the first line as it was, and del_lines then names the line
	// after it. The second is what "." and "`." see, and the first shows up
	// in the changelist whenever an undo block was owed an entry: "I x
	// CTRL-U Esc 2J" over "aaa bbb" leaves {1,7} and {2,0} in vim.
	r.Buf.ChangedAt(text.Pos{Line: first, Col: head})
	r.Buf.ChangedAt(text.Pos{Line: first + 1})

	return Result{Cursor: clampNormal(r.Buf, text.Pos{Line: first, Col: cursorCol})}, nil
}

// sentenceEnd reports whether c ends a sentence for the two-space rule.
// onlyPeriod is the 'j' flag of 'cpoptions', which takes '!' and '?' out of it.
func sentenceEnd(c byte, onlyPeriod bool) bool {
	if c == '.' {
		return true
	}
	return !onlyPeriod && (c == '!' || c == '?')
}

// JoinCount turns normal mode's count into the number of lines J joins, and
// reports whether the join can happen at all.
//
// vim's rule, from nv_join: a count of 0 or 1 means two lines; a count that
// runs off the end of the buffer is cut back to what is there, but only when
// it was more than two, so 5J two lines from the end joins two lines and 2J on
// the last line beeps. Both were run.
func JoinCount(b *text.Buffer, line, count int) (lines int, ok bool) {
	if count < 2 {
		count = 2
	}
	if line+count-1 > b.LineCount() {
		if count <= 2 {
			return 0, false
		}
		count = b.LineCount() - line + 1
	}
	if count < 2 {
		return 0, false
	}
	return count, true
}

// joinWithSpaces is vim's do_join over a run of lines: the indent of every line but
// the first comes off, and the space that goes in its place is the whole of
// what makes J different from gJ.
//
// The rules, all of them vim's and all measured through cmd/oracle: one space
// between the two halves, two when the first ends a sentence and 'joinspaces'
// is on, none at all when the next line starts with ")", none when the first
// already ends in white space, and none in front of the first piece.
//
// spaces false is gJ: the lines go together untouched.
func joinWithSpaces(lines [][]byte, spaces bool, opt Options) (joined []byte, cursor int, at []joinPiece) {
	pieces := make([][]byte, 0, len(lines))
	gaps := make([]int, len(lines))
	strip := make([]int, len(lines))
	sumsize, currsize := 0, 0
	var end1, end2 byte

	for t, curr := range lines {
		if spaces && t > 0 {
			strip[t] = indentEnd(curr)
			curr = curr[strip[t]:]
			if len(curr) > 0 && curr[0] != ')' && sumsize != 0 && end1 != '\t' {
				if end1 == ' ' {
					end1 = end2
				} else {
					gaps[t]++
				}
				if opt.JoinSpaces && sentenceEnd(end1, opt.JoinSpacesOnlyPeriod) {
					gaps[t]++
				}
			}
		}
		pieces = append(pieces, curr)
		currsize = len(curr)
		sumsize += currsize + gaps[t]

		end1, end2 = 0, 0
		if spaces && currsize > 0 {
			end1 = curr[currsize-1]
			if currsize > 1 {
				end2 = curr[currsize-2]
			}
		}
	}

	at = make([]joinPiece, len(lines))
	joined = make([]byte, 0, sumsize)
	for t, piece := range pieces {
		for range gaps[t] {
			joined = append(joined, ' ')
		}
		at[t] = joinPiece{start: len(joined), stripped: strip[t], gap: gaps[t]}
		joined = append(joined, piece...)
	}
	// Where J leaves the cursor: on the last space it put in, which is the
	// start of the gap in front of the last piece.
	return joined, sumsize - currsize - gaps[len(gaps)-1], at
}

// joinPiece is where one of the joined lines ended up: the byte column its
// text starts at in the joined line, how many bytes of indent came off the
// front of it, and how many spaces went in ahead of it. Only Buffer.JoinMarks
// wants these, and only because a mark on a joined line has to land somewhere
// sensible.
type joinPiece struct{ start, stripped, gap int }
