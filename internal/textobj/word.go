package textobj

// currentWord is iw, aw, iW and aW: vim's current_word(), ported.
//
// The shape is vim's and the two branches are the whole object:
//
// - "the cursor is on white space" == "white space is wanted" picks the end
// of a word (iw on a word, aw on white space): both of those select up to
// the end of the thing under the cursor and no further.
// - the other two (iw on white space, aw on a word) run forward to the start
// of the next word and step back one, which is what makes iw on a run of
// spaces select the spaces and aw on a word take the space after it.
//
// The last block is the rule the help states in one line and nobody
// remembers: "aw" with no white space after the word takes the white space
// before it instead, and never takes a line's indent.
func currentWord(s *scan, count int, include, big bool) span {
	s.big = big
	includeWhite := false
	inclusive := true

	s.backInLine()
	start := s.p

	if (s.cls() == clsBlank) == include {
		if !s.endWord(1, true, true) {
			return span{}
		}
	} else {
		s.fwdWord(1, true)
		// A one-character word at the end of a line leaves the cursor in
		// column 0 of the next line; back up to the end of this one rather
		// than swallowing the first character down there.
		if s.p.Col == 0 {
			s.decl()
		} else {
			s.oneleft()
		}
		if include {
			includeWhite = true
		}
	}

	// Vim's loop decrements first, which is not decoration: on the last
	// object the failure of fwd_word at the end of the buffer is tolerated
	// and the span ends where it got to, so "2aw" on the last two words of a
	// file works and "3aw" with only two words left does not.
	for count--; count > 0; count-- {
		inclusive = true
		if s.incl() == moveFail {
			return span{}
		}
		if include != (s.cls() == clsBlank) {
			if !s.fwdWord(1, true) && count > 1 {
				return span{}
			}
			// The end is just past a line break: the first character of the
			// next line is not part of this object, so the span ends
			// exclusively and finish() decides what that means.
			if !s.oneleft() {
				inclusive = false
			}
		} else {
			if !s.endWord(1, true, true) {
				return span{}
			}
		}
	}

	if includeWhite && (s.cls() != clsBlank || (s.p.Col == 0 && !inclusive)) {
		// No white space was taken after the object, so take some before it,
		// which is what makes "daw" on the last word of a line delete the
		// space in front of it. Never the indent, though: the test for column
		// zero below is what stops "daw" on the first word of an indented
		// line from eating the indent.
		end := s.p
		s.at(start)
		if s.oneleft() {
			s.backInLine()
			if s.cls() == clsBlank && s.p.Col > 0 {
				start = s.p
			}
		}
		s.at(end)
	}

	return span{start: start, end: s.p, inclusive: inclusive, ok: true}
}
