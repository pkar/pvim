package textobj

import (
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// This file is vim's cursor arithmetic, ported.
//
// Every text object in textobject.c is written in terms of six primitives --
// inc, dec, incl, decl, cls and the word walkers built on them -- and the
// objects are only byte-identical to vim's if the primitives are. The two that
// look wrong and are not:
//
// - a position may sit at Col == len(line). That is where vim's NUL lives,
// it classifies as white space, and half of the end-of-line behaviour in
// aw and i{ falls out of a cursor legally standing there.
// - inc and dec return a code and not a bool. 0 means "moved inside the
// line", 2 means "moved inside the line and landed on the NUL", 1 means
// "moved to another line" and -1 means "could not move". current_word
// reads the difference between 0 and 2, so a bool would lose it.
//
// scan is the cursor: a buffer, a position, and the two settings that decide
// what a word is.
type scan struct {
	b   *text.Buffer
	p   text.Pos
	kw  *keywords
	big bool // 'W' objects: every non-blank is one class
	// quoteEscape is 'quoteescape', which only the quote objects read.
	quoteEscape string
	// paragraphs and sections are the nroff macro lists the paragraph and
	// sentence objects treat as boundaries.
	paragraphs string
	sections   string
	// visual is set when the object is being asked for in visual mode with
	// 'selection' not "old", which suppresses one rule in finish(). See
	// there.
	visual bool
}

func (s *scan) at(p text.Pos) { s.p = p }

// line is the line the cursor is on.
func (s *scan) line() []byte { return s.b.Line(s.p.Line) }

// lineAt is line n, or empty when n is outside the buffer.
func (s *scan) lineAt(n int) []byte {
	if n < 1 || n > s.b.LineCount() {
		return nil
	}
	return s.b.Line(n)
}

// char is the rune under the cursor, or 0 at the end of the line. Vim reads a
// NUL there and every classifier treats it as white space, so 0 is not an
// error value here, it is a position in the text.
func (s *scan) char() rune { return charAt(s.line(), s.p.Col) }

// charAt is the rune at a byte column, 0 past the end of the line.
func charAt(line []byte, col int) rune {
	if col < 0 || col >= len(line) {
		return 0
	}
	r, _ := utf8.DecodeRune(line[col:])
	return r
}

// charLen is the byte width of the character at col, at least one so that no
// loop over invalid utf-8 can stand still.
func charLen(line []byte, col int) int {
	if col >= len(line) {
		return 1
	}
	_, n := utf8.DecodeRune(line[col:])
	if n < 1 {
		n = 1
	}
	return n
}

// The return codes of inc and dec, named because current_word and
// current_block both branch on the difference between them.
const (
	moveFail = -1 // could not move: start or end of the buffer
	moveIn   = 0  // moved within the line, not onto the NUL
	moveLine = 1  // moved to another line
	moveNUL  = 2  // moved within the line and landed on the NUL
)

// inc advances p by one character, vim's inc().
func inc(b *text.Buffer, p *text.Pos) int {
	line := b.Line(p.Line)
	if p.Col < len(line) {
		p.Col += charLen(line, p.Col)
		if p.Col < len(line) {
			return moveIn
		}
		return moveNUL
	}
	if p.Line < b.LineCount() {
		p.Line++
		p.Col = 0
		return moveLine
	}
	return moveFail
}

// incl is inc that does not stop on the NUL at the end of a non-empty line.
func incl(b *text.Buffer, p *text.Pos) int {
	r := inc(b, p)
	if r >= moveLine && p.Col > 0 {
		r = inc(b, p)
	}
	return r
}

// dec moves p back one character, vim's dec(). Moving off the start of a line
// lands on the NUL of the line above, which is a real position and not an
// overshoot.
func dec(b *text.Buffer, p *text.Pos) int {
	if p.Col > 0 {
		line := b.Line(p.Line)
		p.Col--
		for p.Col > 0 && p.Col < len(line) && !utf8.RuneStart(line[p.Col]) {
			p.Col--
		}
		return moveIn
	}
	if p.Line > 1 {
		p.Line--
		p.Col = len(b.Line(p.Line))
		return moveLine
	}
	return moveFail
}

// decl is dec that does not stop on the NUL at the end of a non-empty line.
func decl(b *text.Buffer, p *text.Pos) int {
	r := dec(b, p)
	if r == moveLine && p.Col > 0 {
		r = dec(b, p)
	}
	return r
}

func (s *scan) inc() int  { return inc(s.b, &s.p) }
func (s *scan) incl() int { return incl(s.b, &s.p) }
func (s *scan) dec() int  { return dec(s.b, &s.p) }
func (s *scan) decl() int { return decl(s.b, &s.p) }

// oneleft is vim's oneleft(): one character back inside the line, and a
// failure at column 0 rather than a move to the line above.
func (s *scan) oneleft() bool {
	if s.p.Col == 0 {
		return false
	}
	s.dec()
	return true
}

// The three character classes vim sorts text into. The numbers are vim's, and
// they are ordered: cls() == 0 is the test for white space everywhere.
const (
	clsBlank = 0
	clsPunct = 1
	clsWord  = 2
)

// cls is vim's cls(): the class of the character under the cursor, with the
// bigword flag folding punctuation into words for the W objects.
func (s *scan) cls() int { return class(s.char(), s.kw, s.big) }

// class sorts one rune the way vim's cls() and utf_class() do.
//
// Above Latin-1 the answer comes from the range table in charclass.go, which
// gives Hiragana, Katakana, the CJK ideographs, Hangul and the emoji blocks a
// class each, so that iw on 日 in "日本語のテキスト" takes 日本語 and not the
// line. Vim's cls() folds every non-blank class into one for the W objects and
// leaves the blanks alone, which is the only thing big does here.
func class(r rune, kw *keywords, big bool) int {
	switch r {
	case ' ', '\t', 0:
		return clsBlank
	}
	c := utfClass(r, kw)
	if c != clsBlank && big {
		return clsPunct
	}
	return c
}

// skipChars is vim's skip_chars: run forward or back while the class holds,
// reporting whether it fell off the end of the buffer.
func (s *scan) skipChars(cclass int, forward bool) bool {
	for s.cls() == cclass {
		var r int
		if forward {
			r = s.inc()
		} else {
			r = s.dec()
		}
		if r == moveFail {
			return true
		}
	}
	return false
}

// backInLine is vim's back_in_line: to the start of the run of one class the
// cursor is in, stopping at column 0.
func (s *scan) backInLine() {
	sclass := s.cls()
	for {
		if s.p.Col == 0 {
			return
		}
		s.dec()
		if s.cls() != sclass {
			s.inc()
			return
		}
	}
}

// fwdWord is vim's fwd_word: the w motion. eol true stops at the end of the
// line on the last iteration, which is what an operator wants and what makes
// "daw" on the last word of a line take the line's white space and not the
// next line's indent.
func (s *scan) fwdWord(count int, eol bool) bool {
	for ; count > 0; count-- {
		sclass := s.cls()

		lastLine := s.p.Line == s.b.LineCount()
		i := s.inc()
		if i == moveFail || (i >= moveLine && lastLine) {
			return false
		}
		if i >= moveLine && eol && count == 1 {
			return true
		}

		if sclass != clsBlank {
			for s.cls() == sclass {
				i = s.inc()
				if i == moveFail || (i >= moveLine && eol && count == 1) {
					return true
				}
			}
		}

		for s.cls() == clsBlank {
			if s.p.Col == 0 && len(s.line()) == 0 {
				break // stop on an empty line, which is a word of its own
			}
			i = s.inc()
			if i == moveFail || (i >= moveLine && eol && count == 1) {
				return true
			}
		}
	}
	return true
}

// endWord is vim's end_word: the e motion. stop true means "if the cursor is
// already on the last character of a word, stay in this word" -- the rule that
// makes iw on the last letter of a word select that word and not the next.
func (s *scan) endWord(count int, stop, empty bool) bool {
	for ; count > 0; count-- {
		sclass := s.cls()
		if s.inc() == moveFail {
			return false
		}

		finished := false
		if s.cls() == sclass && sclass != clsBlank {
			if s.skipChars(sclass, true) {
				return false
			}
		} else if !stop || sclass == clsBlank {
			for s.cls() == clsBlank {
				if empty && s.p.Col == 0 && len(s.line()) == 0 {
					finished = true
					break
				}
				if s.inc() == moveFail {
					return false
				}
			}
			if !finished {
				if s.skipChars(s.cls(), true) {
					return false
				}
			}
		}
		if !finished {
			s.dec()
		}
		stop = false
	}
	return true
}

// inindent is vim's inindent(extra): everything before the cursor on this line
// is white space, with extra columns of slack. It is what makes i{ over an
// indented closing brace linewise.
func (s *scan) inindent(extra int) bool {
	line := s.line()
	col := 0
	for col < len(line) && (line[col] == ' ' || line[col] == '\t') {
		col++
	}
	return col >= s.p.Col+extra
}

// lineWhite reports whether line n is empty or nothing but white space, vim's
// linewhite(). The paragraph objects are built on it.
func (s *scan) lineWhite(n int) bool {
	for _, c := range s.lineAt(n) {
		if c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}
