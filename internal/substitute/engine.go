package substitute

import (
	"github.com/pkar/pvim/internal/regex"
	"github.com/pkar/pvim/internal/text"
)

// subber is one running ":s". It exists so that the line walk, the per-match
// decision and the accounting are three short methods instead of one long
// function with six flags in it.
type subber struct {
	req  Request
	st   *State
	re   *regex.Regexp
	repl string

	all  bool // the "g" flag
	ask  bool // the "c" flag
	only bool // the "n" flag

	// answerAll is the "a" answer to the "c" prompt: stop asking and say yes
	// to everything left. stop is "q" or "l", which ends the whole command.
	answerAll bool
	stop      bool

	// prompted is the line of the last match the "c" flag asked about, and
	// promptedCol its column, which together are where vim leaves the cursor
	// when an interactive substitute ends. It is the MATCH and not the first
	// non-blank: ":%s/aaa/X/gc" answered "yynq" leaves the cursor on the aaa
	// it stopped at, column and all.
	prompted    int
	promptedCol int
	// promptedSub says the last prompt was answered with a substitution. vim
	// leaves the cursor on the match only when it stopped there without
	// making one: "yynq" ends on the aaa it refused, and "l", which replaces
	// and then stops, ends on the first non-blank like an ordinary ":s".
	promptedSub bool
	// deferredFrom is the first line of the run after an "a" answer, which is
	// where vim's changed_lines() reports from. It is not the first line of
	// the command: the lines before the "a" were reported one at a time as
	// the screen was redrawn between prompts.
	deferredFrom int

	// needGroups says the replacement mentions \1 through \9, which is the
	// only reason to pay for a second regex run per match. "&" and "\0" are
	// the whole match, and the whole match is already known.
	needGroups bool
}

// run walks the lines and does the work. It returns an error only for
// something that should stop the command; a pattern that matched nothing is
// reported through Result.Found and turned into E486 by the caller, because
// the "e" flag and a running ":g" both suppress it.
func (s *subber) run(first, last int, res *Result) error {
	s.needGroups = mentionsGroup(s.repl)

	for lnum := first; lnum <= last && lnum <= s.req.Buf.LineCount(); lnum++ {
		line := s.req.Buf.Line(lnum)
		ms := lineMatches(s.re, line, s.all)
		if len(ms) == 0 {
			continue
		}
		res.Found = true

		if s.only {
			// The "n" flag counts and changes nothing, so the cursor does not
			// move either.
			res.Subs += len(ms)
			res.Lines++
			continue
		}

		out, n, err := s.rebuild(lnum, line, ms)
		if err != nil {
			return err
		}
		if n == 0 {
			if s.stop {
				break
			}
			continue
		}

		// One write per line, of the whole line, which is what makes a
		// multi-line replacement work: SetLine splits on the newlines "\r"
		// put in and the lines below move down.
		before := s.req.Buf.LineCount()
		s.req.Buf.SetLine(lnum, out)
		added := s.req.Buf.LineCount() - before

		res.Subs += n
		res.Lines++
		if res.FirstLine == 0 {
			res.FirstLine = lnum
		}
		res.Cursor = firstNonBlank(s.req.Buf, lnum+added)

		// A replacement that added lines moves the end of the range down with
		// it, so ":1,2s/a/X\rY/" still reaches the line that was line 2.
		lnum += added
		last += added

		if s.stop {
			break
		}
	}
	res.Quit = s.stop && !s.answerAll
	res.Asked = s.prompted > 0
	res.AnsweredAll = s.answerAll
	res.DeferredFrom = s.deferredFrom
	return nil
}

// rebuild makes the new text of one line out of the old one and the matches on
// it, and returns how many of them were actually replaced.
//
// The new line is built from the ORIGINAL bytes throughout. That is vim's
// sub_firstline and it is what makes ":s/a/aa/g" terminate rather than eat the
// machine: the second match was found in the line as it was, not in the line
// the first replacement left behind.
func (s *subber) rebuild(lnum int, line []byte, ms [][]int) ([]byte, int, error) {
	var out []byte
	done, at := 0, 0
	for _, m := range ms {
		start, end := m[0], m[1]
		if s.ask && !s.answerAll {
			s.promptedSub = false
			switch s.confirm(lnum, start, end) {
			case No:
				continue
			case Quit:
				s.stop = true
				if done == 0 {
					return nil, 0, nil
				}
				out = append(out, line[at:]...)
				return out, done, nil
			case All:
				s.answerAll = true
				// Everything from here on is an ordinary substitute again,
				// reported once from this line.
				s.deferredFrom = lnum
			case Last:
				s.stop = true
			}
		}
		groups := s.groups(line, start, end)
		rep, err := Expand(s.repl, groups, magic(s.req.Opt))
		if err != nil {
			return nil, 0, err
		}
		out = append(out, line[at:start]...)
		out = append(out, rep...)
		at = end
		done++
		s.promptedSub = true
		if s.stop {
			break
		}
	}
	if done == 0 {
		return nil, 0, nil
	}
	out = append(out, line[at:]...)
	return out, done, nil
}

// confirm asks the "c" flag's question. A nil callback answers yes, which is
// what cmd/oracle and a table test want: the prompt needs a frontend and the
// substitution does not.
func (s *subber) confirm(lnum, start, end int) Answer {
	s.prompted, s.promptedCol = lnum, start
	if s.req.Confirm == nil {
		return Yes
	}
	return s.req.Confirm(Prompt{
		Line:  lnum,
		Start: start,
		End:   end,
		// The replacement as TYPED, not as it will come out:
		// ":s/a\(.\)/<\u\1>/c" prompts with `<\u\1>`. Measured, and the
		// opposite of what a first guess says.
		Text: "replace with " + s.repl + " (y/n/a/q/l/^E/^Y)?",
	})
}

// groups returns the match and its subexpressions for one match.
//
// The whole match is always exact, because the caller found it. The
// subexpressions cost a second run of the pattern over the line from the
// match's start, and are only paid for when the replacement mentions "\1"
// through "\9".
//
// internal/regex offers FindSubmatchIndex over a whole string and not over a
// string from a column, so the second run is over line[start:] and the result
// is checked against the match that is already known: same start, same end, or
// it is not the same match and the groups are dropped rather than guessed. The
// one thing that can make them differ is a "^" inside the pattern, which the
// translator compiles to a multi-line anchor and which is therefore true at
// the start of the slice; a pattern that has both a "^" in an alternation and
// a "\1" in the replacement gets group 0 and nothing else, which is a corner
// nobody has typed and which is a wrong answer rather than a wrong buffer.
func (s *subber) groups(line []byte, start, end int) [][]byte {
	whole := line[start:end]
	if !s.needGroups {
		return [][]byte{whole}
	}
	idx := s.re.FindSubmatchIndex(line[start:])
	if idx == nil || idx[0] != 0 || idx[1] != end-start {
		return [][]byte{whole}
	}
	out := make([][]byte, len(idx)/2)
	for i := range out {
		lo, hi := idx[2*i], idx[2*i+1]
		if lo < 0 || hi < 0 {
			continue
		}
		out[i] = line[start+lo : start+hi]
	}
	return out
}

// mentionsGroup reports whether a replacement uses "\1" through "\9". "\0" and
// "&" are the whole match and do not count.
func mentionsGroup(repl string) bool {
	for i := 0; i < len(repl)-1; i++ {
		if repl[i] != '\\' {
			continue
		}
		if repl[i+1] >= '1' && repl[i+1] <= '9' {
			return true
		}
		i++ // an escaped anything is not the start of another escape
	}
	return false
}

// lineMatches returns the matches on one line that vim would act on, leftmost
// first, as [start, end] byte columns. With all false it is at most one.
//
// The list comes from one FindAllIndex, which is the only way to get vim's
// anchoring right: internal/regex compiles "^" and "$" to multi-line anchors,
// so a scan over a slice of the line would let "^" be true in the middle of
// it. Go's own rule for empty matches is already vim's for everything except
// the very end of the line, and dropEOL is that exception.
func lineMatches(re *regex.Regexp, line []byte, all bool) [][]int {
	n := 1
	if all {
		n = -1
	}
	idx := re.FindAllIndex(line, n)
	if len(idx) == 0 {
		return nil
	}
	if !all {
		return idx[:1]
	}
	if dropEOL(re, line, idx) {
		idx = idx[:len(idx)-1]
	}
	return idx
}

// dropEOL decides whether the last match on a line is one vim would not have
// made: an empty match sitting past the last byte, which vim reaches only by
// walking there and which it refuses when it does.
//
// Vim's line scan resumes at the end of each match, skips an empty match that
// lands exactly where the previous one ended, steps one character on, and
// gives up on the line the moment that step arrives at the end of it. Go's
// scan skips the same empty matches but has no end-of-line rule, so the two
// agree everywhere except on one trailing empty match, and only when the
// pattern could also have matched empty at the previous match's end -- which
// is what vim walked over to get there.
//
// Measured, both ways round, on vim 9.2.321 with "abc":
//
//	:s/b*/-/g -a-c the empty match at the end is dropped
//	:s/\vb|$/-/g a-c- the same empty match is kept
//
// The pattern is what tells them apart: "b*" matches empty at column 2 and
// "b|$" does not, so vim stepped to the end for the first and jumped to it for
// the second.
func dropEOL(re *regex.Regexp, line []byte, idx [][]int) bool {
	if len(idx) < 2 {
		return false // the first match on a line is never the one to drop
	}
	last := idx[len(idx)-1]
	if last[0] != last[1] || last[0] != len(line) {
		return false
	}
	resume := idx[len(idx)-2][1]
	if resume >= len(line) {
		return true
	}
	m := re.FindIndex(line[resume:])
	return m != nil && m[0] == 0 && m[1] == 0
}

// firstNonBlank is where ":s" leaves the cursor: the first byte of the line
// that is not a space or a tab, or column zero when the line is all blanks.
func firstNonBlank(b *text.Buffer, lnum int) text.Pos {
	line := b.Line(lnum)
	col := 0
	for col < len(line) && (line[col] == ' ' || line[col] == '\t') {
		col++
	}
	if col >= len(line) && len(line) > 0 {
		col = len(line) - 1
	}
	return text.Pos{Line: lnum, Col: col}
}
