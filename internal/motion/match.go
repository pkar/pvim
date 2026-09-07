package motion

import (
	"strings"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// % and the four unmatched-bracket motions, which are one search with
// different starting conditions.
//
// What is not here, and is registered in the report rather than pretended: vim
// skips a bracket that is inside a comment or a string when it can tell, and
// it knows about #if/#else/#endif. This one knows about 'matchpairs' and about
// backslash escaping -- a \( matches a \) and not a bare ) -- and nothing
// else. The difference shows on a brace inside a C string with no matching
// brace anywhere, which is a file that would confuse a person too.

// matchPair is one entry of 'matchpairs'.
type matchPair struct {
	open, close rune
}

// parsePairs reads 'matchpairs', which is "(:),{:},[:]" out of the box: pairs
// of characters separated by a colon, entries separated by commas.
func parsePairs(opt string) []matchPair {
	var out []matchPair
	for _, item := range strings.Split(opt, ",") {
		o, size := utf8.DecodeRuneInString(item)
		if size == 0 || len(item) <= size || item[size] != ':' {
			continue
		}
		c, size2 := utf8.DecodeRuneInString(item[size+1:])
		if size2 == 0 {
			continue
		}
		out = append(out, matchPair{open: o, close: c})
	}
	return out
}

// escaped reports whether the character at col is preceded by an odd number of
// backslashes, which is vim's check_prevcol loop. A bracket that is escaped
// only matches a bracket that is escaped.
func escaped(line []byte, col int) bool {
	n := 0
	for col > 0 && line[col-1] == '\\' {
		col--
		n++
	}
	return n%2 == 1
}

// findMatch is % without a count: find the first bracket at or after the
// cursor on this line, then its match.
func findMatch(r Request) (text.Pos, bool) {
	b := r.Buf
	pairs := parsePairs(r.Opt.MatchPairs)
	line := b.Line(r.From.Line)

	var initc, findc rune
	var dir int
	p := text.Pos{Line: r.From.Line, Col: r.From.Col}
	for p.Col < len(line) {
		c, _ := utf8.DecodeRune(line[p.Col:])
		for _, m := range pairs {
			switch c {
			case m.open:
				initc, findc, dir = m.open, m.close, 1
			case m.close:
				initc, findc, dir = m.close, m.open, -1
			}
		}
		if dir != 0 {
			break
		}
		p.Col += charLen(line, p.Col)
	}
	if dir == 0 {
		return r.From, false
	}

	want := escaped(line, p.Col)
	nest := 0
	for {
		var i int
		if dir > 0 {
			i = inc(b, &p)
		} else {
			i = dec(b, &p)
		}
		if i == -1 {
			return r.From, false
		}
		c := gchar(b, p)
		if c != initc && c != findc {
			continue
		}
		if escaped(b.Line(p.Line), p.Col) != want {
			continue
		}
		if c == initc {
			nest++
			continue
		}
		if nest == 0 {
			return p, true
		}
		nest--
	}
}

// unmatched is [( [{ ]) and ]}: the count'th unmatched bracket in one
// direction, which is what tells you where the block you are inside begins.
func unmatched(r Request, dir int, open, close rune, count int) (text.Pos, bool) {
	b := r.Buf
	p := r.From
	target, other := open, close
	if dir > 0 {
		target, other = close, open
	}
	nest := 0
	for {
		var i int
		if dir > 0 {
			i = inc(b, &p)
		} else {
			i = dec(b, &p)
		}
		if i == -1 {
			return r.From, false
		}
		switch gchar(b, p) {
		case other:
			nest++
		case target:
			if nest > 0 {
				nest--
				continue
			}
			if count--; count == 0 {
				return p, true
			}
		}
	}
}

// motionMatch is %. Bare it is the matchpair jump and inclusive; with a count
// it is a percentage of the file and linewise, which is two motions on one key
// and the reason Result carries its own Kind.
func motionMatch(r Request) Result {
	b := r.Buf
	if r.Count > 0 {
		if r.Count > 100 {
			return fail()
		}
		lnum := (b.LineCount()*r.Count + 99) / 100
		if lnum < 1 {
			lnum = 1
		}
		if lnum > b.LineCount() {
			lnum = b.LineCount()
		}
		p := beginLine(r, lnum)
		curswant := dispCol(b, p, r.Opt.TabStop)
		if !r.Opt.StartOfLine {
			curswant = CurswantKeep
		}
		return Result{
			To: p, Kind: KindLine, Ok: true, Jump: true,
			Curswant: curswant, Count: r.Count,
		}
	}
	p, ok := findMatch(r)
	if !ok {
		return fail()
	}
	return Result{
		To: p, Kind: KindCharInclusive, Ok: true, Jump: true,
		Curswant: dispCol(b, p, r.Opt.TabStop), Count: 1,
	}
}

// motionUnmatched is [( [{ ]) and ]}.
func motionUnmatched(dir int, open, close rune) Func {
	return func(r Request) Result {
		p, ok := unmatched(r, dir, open, close, r.Count1())
		if !ok {
			return fail()
		}
		return Result{
			To: p, Kind: KindCharExclusive, Ok: true, Jump: true,
			Curswant: dispCol(r.Buf, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}
