package textobj

import "github.com/pkar/pvim/internal/text"

// currentBlock is i( a( i{ a{ i[ a[ i< a< and their aliases: vim's
// current_block(), ported.
//
// Three rules in here are not in the help and are what the oracle found:
//
// - the search runs backwards for the count'th unmatched open bracket, and
// only when there is no unmatched bracket behind the cursor at all does it
// run forwards instead, for the count'th unmatched open bracket ahead.
// That is why "di(" with the cursor before a block takes the block, why
// "2di(" from outside takes a block nested one deeper rather than the next
// block along, and why "2di(" from inside a block with nothing around it
// fails instead of jumping forward.
// - the inner block's start is the character after the bracket and skips the
// line break when the bracket is last on its line, and its end walks back
// over a closing bracket that has nothing but indent in front of it. Those
// two together are what makes "di{" over a brace block delete whole lines:
// the span comes out charwise-exclusive ending in column one, and finish()
// applies vim's rule about that.
// - a bracket behind an odd number of backslashes is not a bracket, because
// 'cpo' here has no M in it. See |cpo-M| and escapedBracket.
func currentBlock(s *scan, count int, include bool, open, closer byte) span {
	old := s.p

	// Vim ignores indent for the brace objects only: with the cursor in the
	// white space at the start of a line, i{ looks from the first non-blank.
	if open == '{' {
		for s.inindent(1) {
			if s.inc() != moveIn {
				break
			}
		}
	}
	// On the open bracket itself: step past it so the backward search finds
	// it, which is what makes di( work with the cursor on the '('.
	if s.char() == rune(open) {
		s.p.Col++
	}

	var startPos text.Pos
	found := false
	if _, ok := s.findMatch(open, closer, false); ok {
		for ; count > 0; count-- {
			pos, ok := s.findMatch(open, closer, false)
			if !ok {
				found = false
				break
			}
			s.at(pos)
			startPos = pos
			found = true
		}
	} else {
		for ; count > 0; count-- {
			pos, ok := s.findMatch(open, closer, true)
			if !ok {
				found = false
				break
			}
			s.at(pos)
			startPos = pos
			found = true
		}
	}
	if !found {
		s.at(old)
		return span{}
	}

	endPos, ok := s.findMatchOther(open, closer)
	if !ok {
		s.at(old)
		return span{}
	}
	s.at(endPos)

	if include {
		return span{start: startPos, end: endPos, inclusive: true, ok: true}
	}

	// Exclude the brackets. The start is the character after the opening one,
	// and the line break with it when the bracket is last on its line: that
	// is what puts the start of "di{" in column one of the next line and so
	// what makes it linewise.
	start := startPos
	incl(s.b, &start)

	// sol records that the closing bracket had nothing but white space in
	// front of it on its line, in which case the object ends with the line
	// break of the line above rather than inside it.
	sol := s.p.Col == 0
	s.decl()
	for s.inindent(1) {
		sol = true
		if s.decl() != moveIn {
			break
		}
	}

	inclusive := false
	if sol {
		s.incl()
	} else if !s.p.Before(start) {
		inclusive = true
	} else {
		// Nothing between the brackets: an empty object, which an operator
		// does nothing with and c opens insert mode inside.
		s.at(start)
	}
	return span{start: start, end: s.p, inclusive: inclusive, ok: true}
}

// findMatch is vim's findmatch() as current_block uses it: the nearest
// unmatched open bracket behind the cursor, or with forward set, the nearest
// unmatched one ahead of it. The cursor is not examined, only the text either
// side of it.
func (s *scan) findMatch(open, closer byte, forward bool) (text.Pos, bool) {
	p := s.p
	count := 0
	for {
		var r int
		if forward {
			r = inc(s.b, &p)
		} else {
			r = dec(s.b, &p)
		}
		if r == moveFail {
			return text.Pos{}, false
		}
		line := s.lineAt(p.Line)
		if p.Col >= len(line) {
			continue // the NUL at the end of a line is not a bracket
		}
		c := line[p.Col]
		if c != open && c != closer {
			continue
		}
		if escapedBracket(line, p.Col) {
			continue
		}
		if c == closer {
			count++
			continue
		}
		if count == 0 {
			return p, true
		}
		count--
	}
}

// findMatchOther is the other half: from the open bracket the search above
// found, the matching close bracket ahead of it.
//
// This one skips brackets inside double quotes and the search for the opening
// bracket does not, which looks like an oversight and is vim's: current_block
// forces 'cpo' to "%" for the backward search and puts it back before this
// one, so "if (strcmp("foo(", s))" matches the way a person reads it while the
// search that got here counted every paren. See |cpo-%|.
func (s *scan) findMatchOther(open, closer byte) (text.Pos, bool) {
	p := s.p
	count := 0
	line := s.lineAt(p.Line)
	doQuotes := quotedLine(line)
	inquote := false
	for {
		lnum := p.Line
		if inc(s.b, &p) == moveFail {
			return text.Pos{}, false
		}
		if p.Line != lnum {
			line = s.lineAt(p.Line)
			doQuotes = quotedLine(line)
			inquote = false
		}
		if p.Col >= len(line) {
			continue
		}
		c := line[p.Col]
		if c == '"' && doQuotes && !escapedBracket(line, p.Col) {
			inquote = !inquote
			continue
		}
		if c != open && c != closer {
			continue
		}
		if escapedBracket(line, p.Col) {
			continue
		}
		if inquote {
			continue
		}
		if c == open {
			count++
			continue
		}
		if count == 0 {
			return p, true
		}
		count--
	}
}

// quotedLine reports whether double quotes on this line are worth paying
// attention to.
//
// Vim only trusts quotes on a line with an even number of them, on the theory
// that an odd one is an apostrophe or a broken string rather than a delimiter.
// A quote written as '"' is a character constant and does not count, and a
// backslash hides the character after it.
//
// The one piece of vim's rule not here: a line ending in a backslash continues
// the string onto the next line, and findmatch uses that to decide the quote
// state at the start of the following line. Nothing in this editor's own
// sources does it, and getting it wrong costs a bracket match inside a
// multi-line C string.
func quotedLine(line []byte) bool {
	quotes := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++ // the character after a backslash is not a delimiter
		case '"':
			if i > 0 && i+2 < len(line) && line[i-1] == '\'' && line[i+1] == '\'' {
				continue // '"' is a character constant
			}
			quotes++
		}
	}
	return quotes%2 == 0
}

// escapedBracket reports whether the bracket at col is hidden by a backslash,
// which under the default 'cpo' means it is not a bracket at all: see |cpo-M|,
// whose M is excluded, so "\(" does not match ")".
//
// The rule is parity and not presence, which is the part that reads wrong and
// is vim's: findmatch_limit counts the whole run of backslashes in front of
// the character and takes it as escaped only when the run is odd, because an
// even run is that many escaped backslashes and the bracket after them is a
// real one. So in a Go or vimscript source line holding the string "\\(", the
// paren is a bracket to vim and "di(" over it works, where testing the one
// character in front finds a backslash and abandons the operator.
//
// The run is counted within the line: a backslash at the end of the line
// above escapes the line break and not the first character of this one.
func escapedBracket(line []byte, col int) bool {
	n := 0
	for col-n > 0 && line[col-n-1] == '\\' {
		n++
	}
	return n%2 == 1
}
