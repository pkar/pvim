package motion

import (
	"github.com/pkar/pvim/internal/text"
)

// The word motions, :help word-motions, and vim's fwd_word, bck_word,
// end_word and bckend_word transcribed rather than reinvented. Each is a walk
// that stops when the character class changes, and every difference between
// them is which end of the run they stop on and what they do at a line
// boundary.
//
// Three cases are the ones an implementation written from the documentation
// gets wrong, and each has a test named after it:
//
// - w at the end of a line with an operator waiting stops after the last
// character of that line and turns inclusive, so dw on the last word of a
// line does not pull the next line up.
// - an empty line is a word. w stops on it, and dw on one deletes the line
// because the exclusive-becomes-linewise rule then fires.
// - cw is ce, unless the cursor is on a blank, in which case it is w. It is
// also ce with a stop, so cw on the last character of a word changes that
// one character and not the word after it.

// walker carries the buffer, the keyword table and the big-word flag through
// the four word walks, so that they read like vim's own and nothing has to be
// threaded through six parameters.
type walker struct {
	b   *text.Buffer
	kw  *keywords
	big bool
}

// cls is vim's cls(): the class of the character under a position.
func (w walker) cls(p text.Pos) int {
	return class(gchar(w.b, p), w.kw, w.big)
}

// skipChars is vim's skip_chars: move while the class stays the same,
// reporting whether the buffer ran out.
func (w walker) skipChars(p *text.Pos, c, dir int) bool {
	for w.cls(*p) == c {
		var i int
		if dir > 0 {
			i = inc(w.b, p)
		} else {
			i = dec(w.b, p)
		}
		if i == -1 {
			return true
		}
	}
	return false
}

// fwdWord is vim's fwd_word: the walk behind w and W. eol is set when an
// operator is waiting, and it is what stops the motion at the end of a line.
func (w walker) fwdWord(p *text.Pos, count int, eol bool) bool {
	for ; count > 0; count-- {
		last := count == 1
		sclass := w.cls(*p)
		lastLine := p.Line == w.b.LineCount()
		i := inc(w.b, p)
		if i == -1 || (i >= 1 && lastLine) {
			return false
		}
		if i >= 1 && eol && last {
			return true
		}
		if sclass != 0 {
			for w.cls(*p) == sclass {
				i = inc(w.b, p)
				if i == -1 || (i >= 1 && eol && last) {
					return true
				}
			}
		}
		for w.cls(*p) == 0 {
			if p.Col == 0 && lineEmpty(w.b, p.Line) {
				break
			}
			i = inc(w.b, p)
			if i == -1 || (i >= 1 && eol && last) {
				return true
			}
		}
	}
	return true
}

// bckWord is vim's bck_word: b and B, and the stop argument nothing in this
// package passes but vim's own visual-mode code does.
func (w walker) bckWord(p *text.Pos, count int, stop bool) bool {
	for ; count > 0; count-- {
		sclass := w.cls(*p)
		if dec(w.b, p) == -1 {
			return false
		}
		if !stop || sclass == w.cls(*p) || sclass == 0 {
			for w.cls(*p) == 0 {
				if p.Col == 0 && lineEmpty(w.b, p.Line) {
					goto next
				}
				if dec(w.b, p) == -1 {
					return true
				}
			}
			if w.skipChars(p, w.cls(*p), -1) {
				return true
			}
		}
		inc(w.b, p)
	next:
		stop = false
	}
	return true
}

// endWord is vim's end_word: e and E, and the walk cw borrows.
//
// stop is the whole of what makes cw different from ce: with it set, a cursor
// already sitting on the last character of a word stays inside that word
// instead of running on to the end of the next one.
func (w walker) endWord(p *text.Pos, count int, stop, empty bool) bool {
	for ; count > 0; count-- {
		sclass := w.cls(*p)
		if inc(w.b, p) == -1 {
			return false
		}
		if w.cls(*p) == sclass && sclass != 0 {
			if w.skipChars(p, sclass, 1) {
				return false
			}
		} else if !stop || sclass == 0 {
			for w.cls(*p) == 0 {
				if empty && p.Col == 0 && lineEmpty(w.b, p.Line) {
					goto finished
				}
				if inc(w.b, p) == -1 {
					return false
				}
			}
			if w.skipChars(p, w.cls(*p), 1) {
				return false
			}
		}
		dec(w.b, p)
	finished:
		stop = false
	}
	return true
}

// bckendWord is vim's bckend_word: ge and gE.
func (w walker) bckendWord(p *text.Pos, count int, eol bool) bool {
	for ; count > 0; count-- {
		sclass := w.cls(*p)
		i := dec(w.b, p)
		if i == -1 {
			return false
		}
		if eol && i == 1 {
			return true
		}
		if sclass != 0 {
			for w.cls(*p) == sclass {
				i = dec(w.b, p)
				if i == -1 || (eol && i == 1) {
					return true
				}
			}
		}
		for w.cls(*p) == 0 {
			if p.Col == 0 && lineEmpty(w.b, p.Line) {
				break
			}
			i = dec(w.b, p)
			if i == -1 || (eol && i == 1) {
				return true
			}
		}
	}
	return true
}

// newWalker builds the walk for a request.
func newWalker(r Request, big bool) walker {
	return walker{b: r.Buf, kw: parseKeywords(r.Opt.IsKeyword), big: big}
}

// motionWord is w and W, which is fwd_word plus vim's adjust_cursor: a walk
// that ended after the last byte of a line steps back onto the last character
// and the motion turns inclusive. That single rule is why dw at the end of a
// line does not join it to the next.
func motionWord(big bool) Func {
	return func(r Request) Result {
		w := newWalker(r, big)
		p := r.From

		// cw is ce. Vim calls this "a little strange" in its own source and
		// blames vi; the rule is that c on a non-blank changes to the end of
		// the word rather than up to the start of the next one, and stops
		// there even if the cursor was already on the last character.
		if r.Op == 'c' {
			if c := gchar(r.Buf, p); c != 0 && c != ' ' && c != '\t' {
				w.endWord(&p, r.Count1(), true, false)
				if line := r.Buf.Line(p.Line); p.Col > 0 && p.Col >= len(line) {
					p.Col = prevChar(line, len(line))
				}
				return Result{
					To: p, Kind: KindCharInclusive, Ok: true,
					Curswant: dispCol(r.Buf, p, r.Opt.TabStop), Count: r.Count1(),
				}
			}
		}

		// The walk's own answer is deliberately dropped. Vim's nv_wordcmd
		// beeps when fwd_word fails and keeps the cursor where the walk got
		// to, and it lets a waiting operator run on that partial move: dw on
		// the last character of the file deletes it, because the adjustment
		// below turns the failed walk into an inclusive motion. The one thing
		// lost here is the beep.
		w.fwdWord(&p, r.Count1(), r.Pending)
		kind := KindCharExclusive
		if r.From.Before(p) {
			line := r.Buf.Line(p.Line)
			if p.Col > 0 && p.Col >= len(line) {
				p.Col = prevChar(line, p.Col)
				kind = KindCharInclusive
			}
		}
		return Result{
			To: p, Kind: kind, Ok: true,
			Curswant: dispCol(r.Buf, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}

// motionBackWord is b and B.
func motionBackWord(big bool) Func {
	return func(r Request) Result {
		w := newWalker(r, big)
		p := r.From
		if !w.bckWord(&p, r.Count1(), false) {
			return fail()
		}
		return Result{
			To: p, Kind: KindCharExclusive, Ok: true,
			Curswant: dispCol(r.Buf, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}

// motionEndWord is e and E.
func motionEndWord(big bool) Func {
	return func(r Request) Result {
		w := newWalker(r, big)
		p := r.From
		// Same as w above: a walk that ran out of buffer keeps what it
		// managed, and 2e on the second-to-last word of a file lands on the
		// last character of the last one.
		w.endWord(&p, r.Count1(), false, false)
		if line := r.Buf.Line(p.Line); p.Col > 0 && p.Col >= len(line) {
			p.Col = prevChar(line, len(line))
		}
		return Result{
			To: p, Kind: KindCharInclusive, Ok: true,
			Curswant: dispCol(r.Buf, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}

// motionBackEndWord is ge and gE.
func motionBackEndWord(big bool) Func {
	return func(r Request) Result {
		w := newWalker(r, big)
		p := r.From
		if !w.bckendWord(&p, r.Count1(), false) {
			// The walk moves as it goes and vim does not put the cursor back.
			// See Result.Moved.
			f := fail()
			f.To, f.Moved = p, p != r.From
			return f
		}
		return Result{
			To: p, Kind: KindCharInclusive, Ok: true,
			Curswant: dispCol(r.Buf, p, r.Opt.TabStop), Count: r.Count1(),
		}
	}
}
