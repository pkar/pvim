package motion

import (
	"github.com/pkar/pvim/internal/text"
)

// Sentences, paragraphs and sections: ( ) { } [[ ]] [] ][ , and vim's
// findsent, findpar and startPS transcribed.
//
// The definitions are not the ones anybody would choose and they are
// exactly the ones the keys mean. A paragraph ends at an empty line, a form
// feed, or an nroff macro from 'paragraphs' or 'sections' written as .XX at
// the start of a line. A section ends at a { or a } in column one, at a form
// feed, or at a macro from 'sections'. A sentence ends at a . ! or ? followed
// by any number of ) ] " and ' and then a space, a tab or the end of the line.

// inmacro reports whether the two characters at the start of s name one of the
// two-character nroff macros in opt, which is written as pairs run together:
// "IPLPPPQPP" is IP, LP, PP, PQ, PP. A space in the option matches a space in
// the line or the end of it, which is how a one-letter macro is written.
func inmacro(opt string, s []byte) bool {
	at := func(i int) byte {
		if i < len(s) {
			return s[i]
		}
		return 0
	}
	for i := 0; i+1 <= len(opt); i += 2 {
		a := opt[i]
		var bb byte
		if i+1 < len(opt) {
			bb = opt[i+1]
		}
		if (a == at(0) || (a == ' ' && (at(0) == 0 || at(0) == ' '))) &&
			(bb == at(1) || ((bb == 0 || bb == ' ') && (at(0) == 0 || at(1) == 0 || at(1) == ' '))) {
			return true
		}
	}
	return false
}

// startPS is vim's startPS: does this line start a paragraph or a section?
// para is 0 for a paragraph, which makes an empty line a boundary, and '{' or
// '}' for a section. both adds a '}' in column one, which is what vim calls
// its "strange Vi behaviour" for ]] with an operator waiting.
func startPS(b *text.Buffer, lnum int, para byte, both bool, o Options) bool {
	s := b.Line(lnum)
	var first byte
	if len(s) > 0 {
		first = s[0]
	}
	if first == para || first == '\f' || (both && first == '}') {
		return true
	}
	if first == '.' {
		rest := s[1:]
		return inmacro(o.Sections, rest) || (para == 0 && inmacro(o.Paragraphs, rest))
	}
	return false
}

// findpar is vim's findpar: the walk behind { } [[ ]] [] and ][ . It returns
// the line it stopped on, which may be one past the end of the buffer -- the
// callers clamp it, after they have asked whether it was the last line, in
// that order and for the reason motionSection gives.
func findpar(r Request, dir, count int, what byte, both bool) (lnum int, ok bool) {
	b := r.Buf
	curr := r.From.Line
	for ; count > 0; count-- {
		didSkip := false
		for first := true; ; first = false {
			if len(b.Line(curr)) != 0 {
				didSkip = true
			}
			if !first && didSkip && startPS(b, curr, what, both, r.Opt) {
				break
			}
			curr += dir
			if curr < 1 || curr > b.LineCount() {
				if count > 1 {
					return 0, false
				}
				curr -= dir
				break
			}
		}
	}
	if both && len(b.Line(curr)) > 0 && b.Line(curr)[0] == '}' {
		curr++
	}
	return curr, true
}

// motionPara is { and }: dir is -1 and 1.
func motionPara(dir int) Func {
	return func(r Request) Result {
		b := r.Buf
		lnum, ok := findpar(r, dir, r.Count1(), 0, false)
		if !ok {
			return fail()
		}
		p := text.Pos{Line: lnum, Col: 0}
		kind := KindCharExclusive
		// At the end of the buffer the motion covers the last character
		// rather than stopping in front of a line that is not there.
		if lnum == b.LineCount() {
			if line := b.Line(lnum); len(line) != 0 {
				p.Col = prevChar(line, len(line))
				kind = KindCharInclusive
			}
		}
		return Result{
			To: p, Kind: kind, Ok: true, Jump: true,
			Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}

// motionSection is [[ ]] [] and ][ . dir is -1 or 1 and what is '{' for the
// two that look for the start of a section and '}' for the two that look for
// its end.
func motionSection(dir int, what byte) Func {
	return func(r Request) Result {
		b := r.Buf
		// Vim's own comment calls this imitating strange Vi behaviour: with
		// an operator waiting, "]]" also stops at a } in column one.
		both := r.Pending && dir > 0 && what == '{'
		lnum, ok := findpar(r, dir, r.Count1(), what, both)
		if !ok {
			return fail()
		}
		p := text.Pos{Line: lnum, Col: 0}
		kind := KindCharExclusive
		if lnum == b.LineCount() && what != '}' {
			if line := b.Line(lnum); len(line) != 0 {
				p.Col = prevChar(line, len(line))
				kind = KindCharInclusive
			}
		}
		// findpar can hand back one line past the end of the buffer: with
		// both set it steps over a } in column one, and a } on the last line
		// leaves it pointing at nothing. Vim lets the cursor go there and
		// clamps it afterwards, which is why the inclusive test above is
		// asked before this and not after -- d]] on the second-to-last line
		// of a file stops in column one of the last, exclusive, and takes
		// whole lines rather than the closing brace.
		if p.Line > b.LineCount() {
			p.Line, p.Col, kind = b.LineCount(), 0, KindCharExclusive
		}
		if !r.Pending {
			p.Col = firstNonBlank(b.Line(p.Line))
		}
		return Result{
			To: p, Kind: kind, Ok: true, Jump: true,
			Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}

// sentenceEnders are the characters a sentence may end with, and the closers
// that may follow the full stop before the space that ends it.
const sentenceEnders = ".!?)]\"'"

// findsent is vim's findsent: the walk behind ( and ).
func findsent(r Request, dir, count int) (text.Pos, bool) {
	b := r.Buf
	pos := r.From
	step := func(p *text.Pos) int {
		if dir > 0 {
			return incl(b, p)
		}
		return decl(b, p)
	}
	for ; count > 0; count-- {
		noskip := false
		found := false
		if gchar(b, pos) == 0 {
			for {
				if step(&pos) == -1 {
					// vim's "if (count) return FAIL": there is nowhere left to
					// go and there are iterations still to run, so the whole
					// motion fails and the operator with it. Clamping instead
					// is not a cursor one column out, it is 12d) on a two-line
					// file deleting the file, because the exclusive-to-
					// linewise rule then promotes column one of line one to
					// the end of the buffer into a linewise delete.
					if count > 1 {
						return pos, false
					}
					break
				}
				if gchar(b, pos) != 0 {
					break
				}
			}
			if dir > 0 {
				found = true
			}
		} else if dir > 0 && pos.Col == 0 && startPS(b, pos.Line, 0, false, r.Opt) {
			if pos.Line == b.LineCount() {
				return pos, false
			}
			pos.Line++
			pos.Col = 0
			found = true
		} else if dir < 0 {
			decl(b, &pos)
		}

		if !found {
			// Go back to the previous non-white non-punctuation character.
			foundDot := false
			for {
				c := gchar(b, pos)
				if !(c == ' ' || c == '\t' || isSentenceChar(c)) {
					break
				}
				tpos := pos
				if decl(b, &tpos) == -1 || (lineEmpty(b, tpos.Line) && dir > 0) {
					break
				}
				if foundDot {
					break
				}
				if c == '.' || c == '!' || c == '?' {
					foundDot = true
				}
				if (c == ')' || c == ']' || c == '"' || c == '\'') &&
					!isSentenceChar(gchar(b, tpos)) {
					break
				}
				decl(b, &pos)
			}

			startLnum := pos.Line
			for {
				c := gchar(b, pos)
				if c == 0 || (pos.Col == 0 && startPS(b, pos.Line, 0, false, r.Opt)) {
					if dir < 0 && pos.Line != startLnum {
						pos.Line++
						pos.Col = 0
					}
					break
				}
				if c == '.' || c == '!' || c == '?' {
					tpos := pos
					var i int
					for {
						if i = inc(b, &tpos); i == -1 {
							break
						}
						c = gchar(b, tpos)
						if !(c == ')' || c == ']' || c == '"' || c == '\'') {
							break
						}
					}
					if i == -1 || c == ' ' || c == '\t' || c == 0 {
						pos = tpos
						if gchar(b, pos) == 0 { // skip the end of the line
							inc(b, &pos)
						}
						break
					}
				}
				if step(&pos) == -1 {
					if count > 1 {
						return pos, false
					}
					noskip = true
					break
				}
			}
		}

		for !noskip {
			c := gchar(b, pos)
			if c != ' ' && c != '\t' {
				break
			}
			if incl(b, &pos) == -1 {
				break
			}
		}
	}
	return pos, true
}

// isSentenceChar reports whether c is one of the characters findsent walks
// back over: the enders and the closers that may follow them.
func isSentenceChar(c rune) bool {
	for _, e := range sentenceEnders {
		if c == e {
			return true
		}
	}
	return false
}

// motionSentence is ( and ).
func motionSentence(dir int) Func {
	return func(r Request) Result {
		b := r.Buf
		p, ok := findsent(r, dir, r.Count1())
		if !ok {
			return fail()
		}
		kind := KindCharExclusive
		if r.From.Before(p) {
			line := b.Line(p.Line)
			if p.Col > 0 && p.Col >= len(line) {
				p.Col = prevChar(line, p.Col)
				kind = KindCharInclusive
			}
		}
		return Result{
			To: p, Kind: kind, Ok: true, Jump: true,
			Curswant: dispCol(b, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}
