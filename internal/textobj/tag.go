package textobj

import "github.com/pkar/pvim/internal/text"

// currentTagBlock is it and at: vim's current_tagblock(), ported.
//
// Vim finds the tags with two regex searchpairs and this finds them with a
// scanner, because internal/regex translates vim patterns to RE2 and RE2 has
// no searchpair. The rules the scanner has to keep, all of them measured
// against vim rather than read off the HTML spec:
//
// - a start tag is '<', a name of anything but space, tab, '>', '/' and '!',
// then attributes up to a '>' that is not inside a quoted value and not
// preceded by '/'. So <br/> and <hr /> are not start tags and never have a
// matching end tag, and <a href="x>y"> ends at the second '>'.
// - an end tag is exactly "</name>", no spaces anywhere in it.
// - names match without regard to case, so <AB> is closed by </ab>.
// - the end of an inner tag block that sits in column one stays in column
// one: unlike every other object here, the tag objects are exempt from the
// rule in finish() that turns such a span linewise, which is why "dit" on
// a block whose tags are each on their own line leaves both tags and an
// empty line between them.
func currentTagBlock(s *scan, count int, include bool) span {
	oldPos := s.p
	oldEnd := s.p
	decl(s.b, &oldEnd) // old_end is inclusive

	// Ignore indent, then take the block the cursor's own tag names.
	for s.inindent(1) {
		if s.inc() != moveIn {
			break
		}
	}
	if s.inHTMLTag(false) {
		for s.char() != '>' {
			if s.inc() == moveFail {
				break
			}
		}
	} else if s.inHTMLTag(true) {
		for s.char() != '<' {
			if s.dec() == moveFail {
				break
			}
		}
		s.dec()
		oldEnd = s.p
	}

	for {
		startPos, ok := s.searchStartTag(count)
		if !ok {
			s.at(oldPos)
			return span{}
		}

		name := s.tagName(startPos)
		if name == "" {
			s.at(oldPos)
			return span{}
		}
		endTag, ok := s.searchEndTag(startPos, name)
		if !ok || endTag.Before(oldEnd) {
			// No matching end tag, or one that closes before the cursor: the
			// tag we found was not the one the cursor is in. Look further
			// out, one level at a time.
			if startPos.Line == 1 && startPos.Col == 0 {
				s.at(oldPos)
				return span{}
			}
			s.at(startPos)
			count = 1
			continue
		}

		s.at(endTag)
		inclusive := true
		if include {
			for s.char() != '>' {
				if s.inc() == moveFail {
					break
				}
			}
		} else if s.p.Col == 0 {
			// An end tag in column one: the object ends with the line break
			// of the line above rather than inside it.
			inclusive = false
		} else {
			s.dec()
		}
		endPos := s.p

		start := startPos
		if !include {
			// Past the '>' of the start tag, which is not necessarily the
			// first '>' after it: <a href="x>y"> ends at the second.
			if gt, ok := s.startTagEnd(startPos); ok && s.charAtPos(gt) == '>' {
				s.at(gt)
				s.inc()
				start = s.p
			}
		}

		if endPos.Before(start) {
			// Nothing between the tags.
			return span{start: start, end: start, noAdjust: true, ok: true}
		}
		return span{start: start, end: endPos, inclusive: inclusive, noAdjust: true, ok: true}
	}
}

// inHTMLTag is vim's in_html_tag(): the cursor is inside "<aaa ...>" when
// endTag is false, or inside "</aaa>" when it is true.
//
// Only the '<' has to be on the cursor's line. The walk that follows it stops
// at the cursor and not at the end of the line, so a '<' that is the last byte
// of its line, with the cursor on it, answers yes with nothing examined at
// all: vim never looks for the '>' here, and the caller walks to it with
// inc(), which crosses line breaks. That is what makes "dit" work on a buffer
// whose first line is a bare "<" and whose second line is "<p>b</p>".
func (s *scan) inHTMLTag(endTag bool) bool {
	line := s.line()
	col := s.p.Col
	if col >= len(line) {
		col = len(line) - 1
	}
	lt := -1
	for i := col; i >= 0; i-- {
		if line[i] == '<' {
			lt = i
			break
		}
		// A '>' behind the cursor closed a tag the cursor is not in. The
		// character under the cursor does not count: standing on the '>' of
		// "</b>" is standing in that end tag.
		if i < col && line[i] == '>' {
			return false
		}
	}
	if lt < 0 {
		return false
	}
	// The character after the '<', which is vim's NUL when the '<' ends the
	// line and never the first byte of the line below.
	next := charAt(line, lt+1)
	if endTag {
		return next == '/'
	}
	if next == '/' {
		return false
	}
	// The '>' that closes this tag must not be behind the cursor. Vim walks
	// from the '<' up to the cursor keeping the character before whatever it
	// stopped at, and refuses only when that character is the '/' of a
	// self-closing tag, so "<br/>" is not a tag the cursor is inside and
	// "<br/" with no '>' yet is.
	last := byte(0)
	for i := lt + 1; i < s.p.Col && i < len(line); i++ {
		if line[i] == '>' {
			break
		}
		last = line[i]
	}
	return last != '/'
}

// searchStartTag walks back over the count'th unmatched start tag, counting
// end tags as nesting, and leaves the cursor on its '<'.
func (s *scan) searchStartTag(count int) (text.Pos, bool) {
	pos := s.p
	for ; count > 0; count-- {
		depth := 0
		for done := false; !done; {
			if dec(s.b, &pos) == moveFail {
				return text.Pos{}, false
			}
			line := s.lineAt(pos.Line)
			if pos.Col >= len(line) || line[pos.Col] != '<' {
				continue
			}
			switch {
			case s.isEndTag(pos) != "":
				depth++
			case s.isStartTag(pos):
				if depth == 0 {
					done = true
					break
				}
				depth--
			}
		}
	}
	s.at(pos)
	return pos, true
}

// searchEndTag finds "</name>" for the start tag at from, counting further
// "<name...>" as nesting, and returns the position of its '<'.
func (s *scan) searchEndTag(from text.Pos, name string) (text.Pos, bool) {
	pos := from
	inc(s.b, &pos) // the start tag itself is not one of its own children
	depth := 0
	for {
		line := s.lineAt(pos.Line)
		if pos.Col < len(line) && line[pos.Col] == '<' {
			switch {
			case sameName(s.isEndTag(pos), name):
				if depth == 0 {
					return pos, true
				}
				depth--
			case s.isStartTag(pos) && sameName(s.tagName(pos), name):
				depth++
			}
		}
		if inc(s.b, &pos) == moveFail {
			return text.Pos{}, false
		}
	}
}

// tagName is the name of the tag whose '<' is at pos: everything up to a
// space, tab or '>'.
func (s *scan) tagName(pos text.Pos) string {
	line := s.lineAt(pos.Line)
	i := pos.Col + 1
	start := i
	for i < len(line) && line[i] != '>' && !blank(line[i]) {
		i++
	}
	return string(line[start:i])
}

// isStartTag reports whether a start tag begins at pos.
func (s *scan) isStartTag(pos text.Pos) bool {
	_, ok := s.startTagEnd(pos)
	return ok
}

// startTagEnd finds the '>' that closes the start tag at pos, which is vim's
// "<[^ \t>/!]\+" followed by attributes. A '>' inside a quoted attribute
// value is not it, and a '>' with a '/' in front of it closes nothing: <br/>
// is not a start tag and has no end tag to look for.
//
// The second return is false when there is no start tag here at all. When
// there is one that nothing closes before the end of the file, vim's "$"
// alternative still calls it a tag, so that answers true with a position that
// is not a '>' -- the caller checks ok before using it.
func (s *scan) startTagEnd(pos text.Pos) (text.Pos, bool) {
	line := s.lineAt(pos.Line)
	i := pos.Col + 1
	name := i
	for i < len(line) {
		c := line[i]
		if c == ' ' || c == '\t' || c == '>' || c == '/' || c == '!' {
			break
		}
		i++
	}
	if i == name {
		return text.Pos{}, false // "<", "</", "<!" and "< " name nothing
	}
	if i < len(line) && line[i] == '!' {
		return text.Pos{}, false
	}

	p := text.Pos{Line: pos.Line, Col: i}
	prev := byte(0)
	for {
		line = s.lineAt(p.Line)
		if p.Col >= len(line) {
			if p.Line >= s.b.LineCount() {
				return text.Pos{}, true
			}
			p.Line++
			p.Col = 0
			prev = 0
			continue
		}
		c := line[p.Col]
		switch {
		case c == '"' || c == '\'':
			end, ok := s.closingQuote(text.Pos{Line: p.Line, Col: p.Col + 1}, c)
			if !ok {
				return text.Pos{}, false // an unterminated value ends nothing
			}
			p = end
			p.Col++
			prev = c
			continue
		case c == '>':
			if prev == '/' {
				return text.Pos{}, false
			}
			return p, true
		}
		prev = c
		p.Col++
	}
}

// closingQuote finds the quote that closes an attribute value, starting at
// from, and it looks past the end of the line to do it.
//
// The rest of startTagEnd already walks the buffer and only the quote scan was
// line-scoped, which meant a value written across a line break -- <a href="x
// then ">z</a> on the next line -- was read as an unterminated value and the
// start tag stopped being a start tag, so "dit" on the z found no object at
// all where vim deletes it.
func (s *scan) closingQuote(from text.Pos, quote byte) (text.Pos, bool) {
	for p := from; p.Line <= s.b.LineCount(); p.Line, p.Col = p.Line+1, 0 {
		if col := findNextQuote(s.lineAt(p.Line), p.Col, quote, ""); col >= 0 {
			return text.Pos{Line: p.Line, Col: col}, true
		}
	}
	return text.Pos{}, false
}

// isEndTag returns the name of the end tag at pos, or "" when there is none.
// The spelling is exact: "</a >" and "</ a>" close nothing.
func (s *scan) isEndTag(pos text.Pos) string {
	line := s.lineAt(pos.Line)
	if pos.Col+1 >= len(line) || line[pos.Col+1] != '/' {
		return ""
	}
	i := pos.Col + 2
	start := i
	for i < len(line) && line[i] != '>' {
		i++
	}
	if i >= len(line) || i == start {
		return ""
	}
	name := line[start:i]
	for _, c := range name {
		if c == ' ' || c == '\t' || c == '<' || c == '/' {
			return ""
		}
	}
	return string(name)
}

// sameName compares two tag names the way vim's search does, which is without
// regard to case: <AB> is closed by </ab>.
func sameName(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
