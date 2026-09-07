package textobj

import "github.com/pkar/pvim/internal/text"

// currentQuote is i" a" i' a' i` and a`: vim's current_quote(), ported.
//
// The strange one, and the reason it is strange: the object works within one
// line and it decides which quote opens a string by counting from the start of
// that line. So on
//
//	a "b" c "d" e
//
// the cursor on the space at column 7 selects " c " -- the text between two
// strings, with the closing quote of one and the opening quote of the other
// taken as a pair -- while the cursor on either quote of "b" selects b. Both
// are vim, both surprise people, and the second is what the help means by
// "When the cursor starts on a quote, Vim will figure out which quote pairs
// form a string by searching from the start of the line".
//
// The other two rules worth naming: a" takes the white space after the closing
// quote and only takes the white space in front of the opening one when there
// is none after, and a count of two on i" includes the quotes without the
// white space, which is the only thing a count does here.
func currentQuote(s *scan, count int, include bool, quote byte) span {
	line := s.line()
	col := s.p.Col
	esc := s.quoteEscape

	var qs, qe int
	if col < len(line) && line[col] == quote {
		// On a quote. Which one it is depends on how many came before it, so
		// pair them off from the start of the line until a pair reaches the
		// cursor.
		qs = findNextQuote(line, 0, quote, "")
		for qs >= 0 {
			qe = findNextQuote(line, qs+1, quote, esc)
			if qe < 0 {
				return span{}
			}
			if qe >= col {
				break
			}
			qs = findNextQuote(line, qe+1, quote, "")
		}
		if qs < 0 {
			return span{}
		}
	} else {
		qs = findPrevQuote(line, col, quote, esc)
		if qs >= len(line) || line[qs] != quote {
			qs = findNextQuote(line, qs, quote, "")
			if qs < 0 {
				return span{}
			}
		}
		qe = findNextQuote(line, qs+1, quote, esc)
		if qe < 0 {
			return span{}
		}
	}

	start, end := qs, qe
	switch {
	case include:
		// Trailing white space, or leading when there is no trailing.
		j := qe + 1
		for j < len(line) && blank(line[j]) {
			j++
		}
		if j > qe+1 {
			end = j - 1
			break
		}
		i := qs - 1
		for i >= 0 && blank(line[i]) {
			i--
		}
		start = i + 1
	case count > 1:
		// "With a count of 2 the quotes are included, but no extra white
		// space as with a"." Any count above one does it, not only two.
	default:
		start, end = qs+1, qe-1
		if end < start {
			// An empty string: an object that covers nothing, which is not a
			// failure -- ci"" opens insert mode between the quotes.
			return span{
				start: text.Pos{Line: s.p.Line, Col: qs + 1},
				end:   text.Pos{Line: s.p.Line, Col: qs + 1},
				ok:    true,
			}
		}
	}

	return span{
		start:     text.Pos{Line: s.p.Line, Col: start},
		end:       text.Pos{Line: s.p.Line, Col: end},
		inclusive: true,
		ok:        true,
	}
}

// blank is vim's VIM_ISWHITE.
func blank(c byte) bool { return c == ' ' || c == '\t' }

// findNextQuote is the column of the next quote at or after col, or -1.
//
// escape is 'quoteescape' and is empty when the caller is looking for a quote
// that opens a string: an escape only hides a quote inside one, so "a\"b"
// starts at the first quote whatever is in front of it.
func findNextQuote(line []byte, col int, quote byte, escape string) int {
	for col < len(line) {
		c := line[col]
		switch {
		case escape != "" && containsByte(escape, c):
			col++
		case c == quote:
			return col
		}
		col++
	}
	return -1
}

// findPrevQuote is the column of the last quote before col. When there is
// none it returns the column it gave up on, which is zero, and the caller
// checks the character there rather than a sentinel -- vim's shape, kept so
// that the "no quote before the cursor" branch reads the same as vim's.
func findPrevQuote(line []byte, col int, quote byte, escape string) int {
	for col > 0 {
		col--
		n := 0
		for col-n > 0 && containsByte(escape, line[col-n-1]) {
			n++
		}
		if n%2 == 1 {
			col -= n // an odd number of escapes: this quote is escaped
		} else if col < len(line) && line[col] == quote {
			break
		}
	}
	return col
}

// containsByte is strings.IndexByte without the import, for a string that is
// one character long in every configuration anyone has.
func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}
