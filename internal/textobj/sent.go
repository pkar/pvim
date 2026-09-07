package textobj

import "github.com/pkar/pvim/internal/text"

// A sentence ends at one of these, possibly through a run of closers.
const (
	sentEnd     = ".!?"
	sentClosers = ")]\"'"
)

// currentSent is is and as: vim's current_sent(), ported.
//
// A sentence ends at '.', '!' or '?' followed by the end of a line or by a
// space or tab, with any number of ')', ']', '"' and '\” allowed in between.
// A paragraph or section boundary ends one too. That is the whole definition
// and everything hard about the object is what happens at the edges: the
// cursor sitting in the blanks between two sentences belongs to neither, and
// "as" takes the blanks after the sentence unless the cursor started in the
// blanks in front of it, in which case it takes those instead.
func currentSent(s *scan, count int, include bool) span {
	startPos := s.p
	pos := s.p
	s.findSent(true, 1)

	// Was the cursor sitting in the white space in front of the next
	// sentence rather than inside a sentence of its own?
	for blank(byte(s.charAtPos(pos))) {
		if incl(s.b, &pos) == moveFail {
			break
		}
	}
	startBlank := pos.Compare(s.p) == 0
	if startBlank {
		s.findFirstBlank(&startPos)
	} else {
		// From the start of the next sentence, back to the start of this
		// one. Backwards from the cursor would find the sentence before this
		// one whenever the cursor is already at a sentence start.
		s.findSent(false, 1)
		startPos = s.p
	}

	ncount := count
	if include {
		ncount = count * 2
	} else if startBlank {
		ncount--
	}
	if ncount > 0 {
		s.findSentForward(ncount, true)
	} else {
		s.decl()
	}

	if include {
		// The blanks in front are in, so the blanks at the end are out, and
		// the other way round when there were none in front.
		if startBlank {
			s.findFirstBlank(&s.p)
			if blank(byte(s.char())) {
				s.decl()
			}
		} else if !blank(byte(s.char())) {
			s.findFirstBlank(&startPos)
		}
	}

	// A sentence that runs to the end of its line takes the line break with
	// it, which is what makes "das" on the last sentence of a paragraph
	// linewise once finish() has had its say.
	inclusive := s.incl() == moveFail
	return span{start: startPos, end: s.p, inclusive: inclusive, ok: true}
}

// charAtPos is gchar_pos: the character at an arbitrary position, 0 at the end
// of a line.
func (s *scan) charAtPos(p text.Pos) rune { return charAt(s.lineAt(p.Line), p.Col) }

// findFirstBlank walks back to the first character of a run of white space,
// vim's find_first_blank().
func (s *scan) findFirstBlank(p *text.Pos) {
	for decl(s.b, p) != moveFail {
		if !blank(byte(s.charAtPos(*p))) {
			incl(s.b, p)
			return
		}
	}
}

// findSentForward is vim's findsent_forward(): count sentence steps, where
// every other step lands on the blanks before a sentence rather than on the
// sentence itself, which is how "2as" takes two sentences and their blanks.
func (s *scan) findSentForward(count int, atStartSent bool) {
	for ; count > 0; count-- {
		s.findSent(true, 1)
		if atStartSent {
			s.findFirstBlank(&s.p)
		}
		if count == 1 || atStartSent {
			s.decl()
		}
		atStartSent = !atStartSent
	}
}

// findSent is vim's findsent(): the ( and ) motions, which the sentence
// objects are built out of.
func (s *scan) findSent(forward bool, count int) bool {
	pos := s.p
	move := func(p *text.Pos) int { return decl(s.b, p) }
	if forward {
		move = func(p *text.Pos) int { return incl(s.b, p) }
	}

	for ; count > 0; count-- {
		prevPos := pos
		noskip := false
		found := false

		switch {
		case s.charAtPos(pos) == 0:
			// On an empty line: up to the next line with something on it.
			for {
				if move(&pos) == moveFail {
					break
				}
				if s.charAtPos(pos) != 0 {
					break
				}
			}
			if forward {
				found = true
			}
		case forward && pos.Col == 0 && s.startPS(pos.Line):
			if pos.Line == s.b.LineCount() {
				return false
			}
			pos.Line++
			found = true
		case !forward:
			decl(s.b, &pos)
		}

		if !found {
			// Back up over the white space and the closers at the end of the
			// sentence we may be standing in the middle of.
			foundDot := false
			for {
				c := s.charAtPos(pos)
				if !blank(byte(c)) && !strContains(sentEnd+sentClosers, c) {
					break
				}
				tpos := pos
				if decl(s.b, &tpos) == moveFail || (len(s.lineAt(tpos.Line)) == 0 && forward) {
					break
				}
				if foundDot {
					break
				}
				if strContains(sentEnd, c) {
					foundDot = true
				}
				if strContains(sentClosers, c) && !strContains(sentEnd+sentClosers, s.charAtPos(tpos)) {
					break
				}
				decl(s.b, &pos)
			}

			startLnum := pos.Line
			for {
				c := s.charAtPos(pos)
				if c == 0 || (pos.Col == 0 && s.startPS(pos.Line)) {
					if !forward && pos.Line != startLnum {
						pos.Line++
					}
					break
				}
				if strContains(sentEnd, c) {
					tpos := pos
					i := 0
					for {
						i = inc(s.b, &tpos)
						if i == moveFail {
							break
						}
						if !strContains(sentClosers, s.charAtPos(tpos)) {
							break
						}
					}
					c = s.charAtPos(tpos)
					if i == moveFail || c == ' ' || c == '\t' || c == 0 {
						pos = tpos
						if s.charAtPos(pos) == 0 {
							inc(s.b, &pos) // step off the NUL at the end of the line
						}
						break
					}
				}
				if move(&pos) == moveFail {
					if count > 1 {
						return false
					}
					noskip = true
					break
				}
			}
		}

		if !noskip {
			for {
				c := s.charAtPos(pos)
				if c != ' ' && c != '\t' {
					break
				}
				if incl(s.b, &pos) == moveFail {
					break
				}
			}
		}

		// A sentence that is nothing but terminators at the end of the
		// buffer leaves the position exactly where it started, and a
		// findSent that does not move is the thing currentSent reads as
		// "the cursor was sitting in the blanks in front of a sentence".
		// Vim advances one character and takes the step again, which is
		// what makes "dis" on the "!" of "?\n!\n" delete the "!" instead
		// of nothing. incl moves pos even when it then fails at the end of
		// the buffer, and that landing on the NUL is the move.
		if pos.Compare(prevPos) == 0 {
			if move(&pos) == moveFail {
				if count > 1 {
					return false
				}
				break
			}
			count++
		}
	}

	s.at(pos)
	return true
}

// strContains is strings.ContainsRune without the import, over the two
// one-byte sets above.
func strContains(set string, r rune) bool {
	if r <= 0 || r > 0x7f {
		return false
	}
	return containsByte(set, byte(r))
}
