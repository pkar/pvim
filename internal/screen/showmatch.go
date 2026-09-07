package screen

import (
	"strings"
	"time"

	"github.com/pkar/pvim/internal/text"
)

// 'showmatch' and 'matchpairs'.
//
// When 'showmatch' is on and a closing bracket is typed, vim jumps the cursor
// to the matching opening one for 'matchtime' tenths of a second and then puts
// it back. The vimrc sets 'showmatch' and leaves 'matchtime' at 5.
//
// Nothing here sleeps. A Grid is a picture and a picture cannot wait, so the
// hop is returned as a Hop the frontend arms a timer for: it draws a frame
// with Frame.CursorOver set, and when the timer fires it draws another with it
// cleared. That keeps the editor goroutine free the whole time, which matters
// because vim's own implementation blocks and swallows keys typed during the
// half second, and this one will not.

// MatchTime is 'matchtime' in tenths of a second, vim's default and the
// vimrc's.
const MatchTime = 5

// Hop is a cursor position to show for a while and then forget: what
// 'showmatch' asks the frontend to do.
type Hop struct {
	// Pos is where to draw the cursor. It is a buffer position, not a cell,
	// because the window may scroll between arming the timer and drawing.
	Pos text.Pos

	// For is how long the cursor stays there.
	For time.Duration
}

// ShowMatch returns the hop typing a closing bracket at pos asks for.
//
// pos is the position of the bracket that was just typed. The second result is
// false when the character is not a closing bracket in 'matchpairs', when its
// opening partner is not found, or when the partner is off the screen -- vim
// does not hop to a line it is not showing, and neither does this.
//
// top and bot are the first and last buffer lines the window is displaying.
func ShowMatch(b *text.Buffer, pos text.Pos, matchpairs string, top, bot int) (Hop, bool) {
	open, ok := closerOf(matchpairs, byteAt(b, pos))
	if !ok {
		return Hop{}, false
	}
	at, ok := searchBack(b, pos, open, byteAt(b, pos))
	if !ok || at.Line < top || at.Line > bot {
		return Hop{}, false
	}
	return Hop{Pos: at, For: time.Duration(MatchTime) * 100 * time.Millisecond}, true
}

// FindMatch is the whole of 'matchpairs': from a bracket at pos, the position
// of its partner, forwards for an opener and backwards for a closer.
//
// It is the arithmetic behind "%" as well as behind 'showmatch', and it lives
// here because 'matchpairs' is a display option in every list vim keeps and
// because the two callers would otherwise each grow their own copy.
func FindMatch(b *text.Buffer, pos text.Pos, matchpairs string) (text.Pos, bool) {
	c := byteAt(b, pos)
	if open, ok := closerOf(matchpairs, c); ok {
		return searchBack(b, pos, open, c)
	}
	if closer, ok := openerOf(matchpairs, c); ok {
		return searchForward(b, pos, c, closer)
	}
	return text.Pos{}, false
}

// closerOf returns the opening bracket that matches c, if c is a closer.
func closerOf(matchpairs string, c byte) (byte, bool) {
	for _, pair := range strings.Split(matchpairs, ",") {
		o, cl, ok := splitPair(pair)
		if ok && cl == c {
			return o, true
		}
	}
	return 0, false
}

// openerOf returns the closing bracket that matches c, if c is an opener.
func openerOf(matchpairs string, c byte) (byte, bool) {
	for _, pair := range strings.Split(matchpairs, ",") {
		o, cl, ok := splitPair(pair)
		if ok && o == c {
			return cl, true
		}
	}
	return 0, false
}

// splitPair reads one "(:)" item of 'matchpairs'. Multi-byte pairs, which vim
// allows, are refused: nothing in this vimrc uses one and half a rune in a
// byte would be worse than saying no.
func splitPair(pair string) (open, shut byte, ok bool) {
	if len(pair) != 3 || pair[1] != ':' {
		return 0, 0, false
	}
	return pair[0], pair[2], true
}

// byteAt returns the byte at a buffer position, or zero when it is off the
// end of the line.
func byteAt(b *text.Buffer, p text.Pos) byte {
	line := lineAt(b, p.Line)
	if p.Col < 0 || p.Col >= len(line) {
		return 0
	}
	return line[p.Col]
}

// searchBack walks backwards from pos for the unmatched open that pairs with
// the close at pos, counting nested pairs on the way.
func searchBack(b *text.Buffer, pos text.Pos, open, shut byte) (text.Pos, bool) {
	depth := 0
	line, col := pos.Line, pos.Col-1
	for line >= 1 {
		l := lineAt(b, line)
		if col > len(l)-1 {
			col = len(l) - 1
		}
		for ; col >= 0; col-- {
			switch l[col] {
			case shut:
				depth++
			case open:
				if depth == 0 {
					return text.Pos{Line: line, Col: col}, true
				}
				depth--
			}
		}
		line--
		col = 1 << 30
	}
	return text.Pos{}, false
}

// searchForward is searchBack the other way, for the close that pairs with an
// open at pos.
func searchForward(b *text.Buffer, pos text.Pos, open, shut byte) (text.Pos, bool) {
	if b == nil {
		return text.Pos{}, false
	}
	depth := 0
	line, col := pos.Line, pos.Col+1
	for line <= b.LineCount() {
		l := lineAt(b, line)
		for ; col < len(l); col++ {
			switch l[col] {
			case open:
				depth++
			case shut:
				if depth == 0 {
					return text.Pos{Line: line, Col: col}, true
				}
				depth--
			}
		}
		line++
		col = 0
	}
	return text.Pos{}, false
}
