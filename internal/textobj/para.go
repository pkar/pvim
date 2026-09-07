package textobj

// currentPar is ip and ap, the only linewise text objects: vim's
// current_par(), ported.
//
// The buffer is a run of paragraphs and runs of blank lines, alternating, and
// both objects count in those runs from the one the cursor is in. ip takes
// count runs; ap takes count paragraphs each with the blank lines after it,
// which is why 2ap on the first paragraph of a file reaches into the second
// one's blank lines and 2ip does not.
//
// The two rules that are not obvious from that:
//
// - a paragraph with no blank lines after it, because the file ends, takes
// the blank lines in front of it instead. That is the last block in here
// and it is what makes "dap" on the last paragraph of a file leave no gap
// above it.
// - a blank line is only a paragraph boundary for these objects. The { and }
// motions do not stop there, which is why this is not shared with
// internal/motion.
func currentPar(s *scan, count int, include bool) Result {
	last := s.b.LineCount()
	startLnum := s.p.Line

	// Back up to the start of the paragraph, or of the run of blank lines,
	// the cursor is in.
	whiteInFront := s.lineWhite(startLnum)
	for startLnum > 1 {
		if whiteInFront {
			if !s.lineWhite(startLnum - 1) {
				break
			}
		} else if s.lineWhite(startLnum-1) || s.startPS(startLnum) {
			break
		}
		startLnum--
	}

	// Past the blank lines the cursor started in, if any. For a paragraph
	// this leaves endLnum one line short, which the first pass of the loop
	// below puts right.
	endLnum := startLnum
	for endLnum <= last && s.lineWhite(endLnum) {
		endLnum++
	}
	endLnum--

	i := count
	if !include && whiteInFront {
		// ip starting on blank lines has already selected its first run.
		i--
	}
	for ; i > 0; i-- {
		if endLnum == last {
			return fail()
		}
		doWhite := false
		if !include {
			doWhite = s.lineWhite(endLnum + 1)
		}
		if include || !doWhite {
			endLnum++
			for endLnum < last && !s.lineWhite(endLnum+1) && !s.startPS(endLnum+1) {
				endLnum++
			}
		}
		if i == 1 && whiteInFront && include {
			// ap that started on blank lines ends with the paragraph after
			// them and does not go looking for more blank lines.
			break
		}
		if include || doWhite {
			for endLnum < last && s.lineWhite(endLnum+1) {
				endLnum++
			}
		}
	}

	// No blank lines after the last paragraph: take the ones in front of the
	// first instead.
	if include && !whiteInFront && !s.lineWhite(endLnum) {
		for startLnum > 1 && s.lineWhite(startLnum-1) {
			startLnum--
		}
	}

	return linewise(startLnum, endLnum)
}

// startPS is vim's startPS(lnum, 0, FALSE): the line starts a paragraph or a
// section because it is empty, because it is a form feed, or because it is an
// nroff macro named in 'paragraphs' or 'sections'.
//
// The nroff half has not been useful since about 1985 and is here because the
// options are in the request and a paragraph that stops one line early is the
// kind of difference nobody notices until an operator eats a line.
func (s *scan) startPS(lnum int) bool {
	line := s.lineAt(lnum)
	if len(line) == 0 {
		return true
	}
	if line[0] == '\f' {
		return true
	}
	if line[0] != '.' {
		return false
	}
	return inMacro(s.paragraphs, line[1:]) || inMacro(s.sections, line[1:])
}

// inMacro is vim's inmacro(): the two characters after the dot appear as a
// pair in the option, where a pair may be written with a trailing space for a
// one-character macro name.
func inMacro(opt string, s []byte) bool {
	var c0, c1 byte
	if len(s) > 0 {
		c0 = s[0]
	}
	if len(s) > 1 {
		c1 = s[1]
	}
	for i := 0; i+1 < len(opt)+1 && i < len(opt); i += 2 {
		m0 := opt[i]
		var m1 byte
		if i+1 < len(opt) {
			m1 = opt[i+1]
		}
		if (m0 == c0 || (m0 == ' ' && (c0 == 0 || c0 == ' '))) &&
			(m1 == c1 || ((m1 == 0 || m1 == ' ') && (c1 == 0 || c1 == ' '))) {
			return true
		}
	}
	return false
}
