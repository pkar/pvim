package search

import (
	"unicode/utf8"

	"github.com/pkar/pvim/internal/regex"
	"github.com/pkar/pvim/internal/text"
)

// State is the search state that outlives one search: what n, N and a bare /
// repeat, and what "/ holds.
//
// It is a value and not a package-level variable so that a macro, an :s and a
// test can each have their own, and so that the day a second window wants its
// own last search there is somewhere to put it.
type State struct {
	// Pattern is the last pattern as the user typed it, in vim's dialect,
	// which is what "/ holds and what the search prompt shows again.
	Pattern string
	// Dir is the direction it ran in. n repeats it and N reverses it.
	Dir Direction
	// Off is the offset that came with it, which n repeats too.
	Off Offset
	// Re is Pattern compiled under the options in force when it was entered.
	// It is kept so that 'hlsearch' does not recompile on every line of a
	// 40,000-line file, and it is dropped whenever Pattern changes. It is nil
	// when Pattern is one vim itself would refuse to compile, which is a
	// pattern the state still holds.
	Re *regex.Regexp
	// NoSmartCase is vim's spat_T.no_scs: 'smartcase' was off for the search
	// that entered this pattern, and stays off for every n, N and bare / that
	// reuses it. Only * and # set it, and only a pattern typed at the prompt
	// clears it.
	//
	// It has to live beside the pattern rather than in the options because the
	// options say what 'smartcase' is set to now, and after a * the answer is
	// "set, and not for this pattern".
	NoSmartCase bool
}

// Result is where a search left the cursor and what kind of motion it was.
type Result struct {
	// Pos is the destination, after the offset. It is vim's raw position and
	// may be the byte after the last one on a line, which /$ produces and
	// which the caller clamps when it puts a cursor there.
	//
	// On an error it is zero, meaning the cursor does not move, except after
	// a failed * or #, where it is the start of the word and the cursor goes
	// there. A zero Line is the flag: no real position has one.
	Pos text.Pos
	// Match is the match itself, before the offset moved anything.
	Match Match
	// Wrapped says the scan came round an end of the buffer, and the caller
	// prints WrapMessage(dir) before anything else.
	Wrapped bool
	// Wraps is how many times it happened; see Hit.Wraps.
	Wraps int
	// Linewise and Inclusive are the offset's effect on the motion's kind: a
	// line offset makes the whole search linewise, an end offset makes it
	// inclusive, and that is the only reason offsets live in this package.
	Linewise, Inclusive bool
}

// Command renders the search the way vim echoes it back on the message line,
// which is what n prints when it repeats one: the direction character, the
// pattern, and the offset only when there is one. Not always what was typed:
// /foo/1 comes back as /foo/+1 and /foo/b+2 as /foo/s+2.
//
// dir is passed in rather than read from the state because N prints the
// direction it is going, not the one the search was entered in.
func (s *State) Command(dir Direction) string {
	if off := s.Off.String(); off != "" {
		return dir.String() + s.Pattern + dir.String() + off
	}
	return dir.String() + s.Pattern
}

// Do runs a search command line: everything after the / or the ?, which may be
// empty to repeat the last search with its own offset.
//
// The state is updated whether the search succeeds or not, because vim updates
// it: after a /nosuchword that fails with E486, "/ holds nosuchword and the
// next n looks for that. A pattern that will not compile is stored too, since
// vim's search_regcomp() saves it before it hands it to the engine: after
// /\(bar raises E54, "/ holds \(bar and the n after it raises E54 again and
// goes nowhere, rather than quietly running whatever was searched for before.
func (s *State) Do(b *text.Buffer, from text.Pos, dir Direction, line string, count int, opt Options) (Result, error) {
	cmd, err := Parse(line, dir)
	if err != nil {
		return Result{}, err
	}

	pattern, off, noSCS := cmd.Pattern, cmd.Offset, false
	if pattern == "" {
		if s.Pattern == "" {
			return Result{}, NoPatternError{}
		}
		// Reusing the pattern reuses the case rules it was entered under, so a
		// bare / after a * stays case-insensitive.
		pattern, noSCS = s.Pattern, s.NoSmartCase
	}
	if cmd.KeepOffset {
		off = s.Off
	}
	if noSCS {
		opt.SmartCase = false
	}

	re, err := Compile(pattern, opt)
	s.Pattern, s.Dir, s.Off, s.Re, s.NoSmartCase = pattern, dir, off, re, noSCS
	if err != nil {
		return Result{}, err
	}

	return search(b, re, from, dir, off, count, opt)
}

// Next is n and N: the last search again, in its own direction with its own
// offset, reversed when reverse is true.
//
// N does not change the direction the state remembers, so n after N goes the
// way the original / went.
//
// The one thing n does that / does not: an n that lands exactly where it
// started and did not wrap runs again with one more on the count. Vim's
// nv_next() calls that "avoid getting stuck on the current cursor position",
// and it is what an offset that clamps produces -- /foo/e+3 on a match near
// the end of the file walks the cursor to the last character and every n after
// it walks to the same place, so without this n stops working and says
// nothing. Only n and N do it; /, ?, * and # do not.
//
// Not after a line offset. Vim's test is on do_search()'s return value, which
// is 1 for a search and 2 for one whose offset made it linewise, and only the
// 1 retries. So /foo/-1 with the only match on the first line repeats forever
// on line 1 and says nothing about it, which looks like a bug and is the
// behaviour.
//
// The comparison is on the clamped position and not the raw one. Vim's
// normal_search() runs check_cursor() before nv_next() looks, so a search that
// lands on the byte after a line's last one and is pulled back onto the same
// character the cursor was already on counts as not having moved.
func (s *State) Next(b *text.Buffer, from text.Pos, reverse bool, count int, opt Options) (Result, error) {
	if s.Pattern == "" {
		return Result{}, NoPatternError{}
	}
	dir := s.Dir
	if reverse {
		dir = dir.Reverse()
	}
	if s.NoSmartCase {
		opt.SmartCase = false
	}
	re, err := Compile(s.Pattern, opt)
	if err != nil {
		return Result{}, err
	}
	s.Re = re
	if count < 1 {
		count = 1
	}

	res, err := search(b, re, from, dir, s.Off, count, opt)
	if err != nil || res.Linewise || res.Wrapped || onACharacter(b, res.Pos) != from {
		return res, err
	}
	return search(b, re, from, dir, s.Off, count+1, opt)
}

// Word is *, #, g* and g#: search for the word under the cursor, forwards for
// * and g*, backwards for # and g#, with \< \> around it for the two without
// the g.
//
// Two things it is not: it takes no offset and it clears the one the state
// held, because vim resets the offset for any search with a pattern in it; and
// it turns 'smartcase' off, because the word came out of the buffer rather
// than off the keyboard and a capital in it is not the user asking for a
// case-sensitive search.
//
// Not for the length of the command: the state remembers it, so the n, the N
// and the bare / after a * are case-insensitive too. That is vim's
// spat_T.no_scs and it is worth more than it looks -- without it, * on Foo in
// a file of foos finds the next foo and the n after it walks past every one of
// them.
//
// When there is no word the error is E348 and the Result is zero. When there
// is one but the search fails, the Result's Pos is the start of the word,
// because that is where vim leaves the cursor.
func (s *State) Word(b *text.Buffer, from text.Pos, dir Direction, whole bool, count int, opt Options) (Result, error) {
	w, err := WordAt(b, from, whole, opt)
	if err != nil {
		return Result{}, err
	}

	opt.SmartCase = false
	re, err := Compile(w.Pattern, opt)
	if err != nil {
		return Result{Pos: w.Start}, err
	}
	s.Pattern, s.Dir, s.Off, s.Re, s.NoSmartCase = w.Pattern, dir, Offset{}, re, true

	res, err := search(b, re, w.Start, dir, Offset{}, count, opt)
	if err != nil {
		res.Pos = w.Start
	}
	return res, err
}

// search is the whole of one search: move the starting point back by the
// character offset, find the match, move the result forward by the offset
// again.
//
// The first of those two is vim's, is easy to miss and is why n after
// /pat/s-2 goes anywhere at all: the cursor is sitting two characters before
// the match, so a search from where it stands would find the same match again
// and n would never move. Vim subtracts the offset from the starting position
// first, and does it once for the whole command rather than once per count.
func search(b *text.Buffer, re *regex.Regexp, from text.Pos, dir Direction, off Offset, count int, opt Options) (Result, error) {
	hit, err := Find(b, Query{
		Re:    re,
		From:  backOffOffset(b, from, off),
		Dir:   dir,
		Count: count,
		End:   off.Kind == OffsetEnd,
	}, opt)
	if err != nil {
		return Result{Wrapped: hit.Wrapped, Wraps: hit.Wraps}, err
	}

	return Result{
		Pos:       applyOffset(b, hit.Match, off),
		Match:     hit.Match,
		Wrapped:   hit.Wrapped,
		Wraps:     hit.Wraps,
		Linewise:  off.Linewise(),
		Inclusive: off.Inclusive(),
	}, nil
}

// backOffOffset is vim's "so we don't get stuck at ?pat?e+2 or /pat/s-2": the
// starting position moves against the offset before the search runs. A line
// offset is left alone, which vim's comment says is for vi compatibility.
//
// A walk that runs off an end of the buffer lands outside it, on line 0 or on
// line LineCount+1, and stays there. That is not a detail: ?pat?e+3 with the
// cursor in the first three characters of the file starts before the first
// line, and a backward search from before the first line has nothing above it
// to find, which under 'nowrapscan' is E384 rather than a match on line 1.
func backOffOffset(b *text.Buffer, from text.Pos, off Offset) text.Pos {
	if off.Kind == OffsetLine || off.N == 0 {
		return from
	}
	p := from
	if off.N > 0 {
		for c := off.N; c > 0; c-- {
			q, ok := decl(b, p)
			if !ok {
				return text.Pos{Line: 0}
			}
			p = q
		}
		return p
	}
	for c := off.N; c < 0; c++ {
		q, ok := incl(b, p)
		if !ok {
			return text.Pos{Line: b.LineCount() + 1}
		}
		p = q
	}
	return p
}

// applyOffset moves the match to where the cursor actually goes.
//
// A line offset lands in column 1 of the line N below or above the match's
// first line, clamped to the buffer, and not on the first non-blank: /foo/+1
// on a tab-indented line puts the cursor on the tab. A character offset counts
// from the match's first character, or from its last one when the offset is e,
// and stops at whichever end of the buffer it reaches.
func applyOffset(b *text.Buffer, m Match, off Offset) text.Pos {
	switch off.Kind {
	case OffsetLine:
		n := m.Start.Line + off.N
		if n < 1 {
			n = 1
		}
		if n > b.LineCount() {
			n = b.LineCount()
		}
		return text.Pos{Line: n, Col: 0}

	case OffsetEnd:
		return walk(b, EndPos(b, m), off.N)

	case OffsetStart:
		return walk(b, m.Start, off.N)

	default:
		return m.Start
	}
}

// walk moves n characters, forwards for a positive n and backwards for a
// negative one, stopping at the end of the buffer rather than failing.
func walk(b *text.Buffer, p text.Pos, n int) text.Pos {
	for ; n > 0; n-- {
		q, ok := incl(b, p)
		if !ok {
			break
		}
		p = q
	}
	for ; n < 0; n++ {
		q, ok := decl(b, p)
		if !ok {
			break
		}
		p = q
	}
	return p
}

// onACharacter is vim's check_cursor(): the nearest position a normal-mode
// cursor may sit at, which is on a character and never on the byte after a
// line's last one. Only Next's did-it-move test uses it. Nothing else in this
// package clamps, because the raw position is also the end of a d/foo motion
// and clamping it there would delete one byte too few.
func onACharacter(b *text.Buffer, p text.Pos) text.Pos {
	if p.Line < 1 {
		p.Line = 1
	}
	if p.Line > b.LineCount() {
		p.Line = b.LineCount()
	}
	line := b.Line(p.Line)
	if len(line) == 0 {
		p.Col = 0
		return p
	}
	if p.Col >= len(line) {
		_, n := utf8.DecodeLastRune(line)
		p.Col = len(line) - n
	}
	if p.Col < 0 {
		p.Col = 0
	}
	return p
}

// inc is vim's inc(): one character forward, where the position just after a
// line's last byte is a place the cursor can be. The int is vim's return code,
// 0 for a move inside the line, 2 for a move onto that end-of-line position, 1
// for a move to the next line and -1 for the end of the buffer.
func inc(b *text.Buffer, p text.Pos) (text.Pos, int) {
	line := b.Line(p.Line)
	if p.Col < len(line) {
		_, n := utf8.DecodeRune(line[p.Col:])
		if n < 1 {
			n = 1
		}
		p.Col += n
		if p.Col >= len(line) {
			return p, 2
		}
		return p, 0
	}
	if p.Line < b.LineCount() {
		return text.Pos{Line: p.Line + 1, Col: 0}, 1
	}
	return p, -1
}

// incl is vim's incl(): inc, and then inc again when that landed on the
// position after a line's last byte, so that it always ends on a real
// character. That is the step a search offset counts in.
func incl(b *text.Buffer, p text.Pos) (text.Pos, bool) {
	p, r := inc(b, p)
	if r >= 1 && p.Col != 0 {
		p, r = inc(b, p)
	}
	return p, r != -1
}

// dec is vim's dec(): one character back, landing on the position after the
// previous line's last byte when it crosses a line.
func dec(b *text.Buffer, p text.Pos) (text.Pos, int) {
	if p.Col > 0 {
		line := b.Line(p.Line)
		c := p.Col - 1
		if c >= len(line) {
			c = len(line) - 1
		}
		for c > 0 && !utf8.RuneStart(line[c]) {
			c--
		}
		if c < 0 {
			c = 0
		}
		p.Col = c
		return p, 0
	}
	if p.Line > 1 {
		return text.Pos{Line: p.Line - 1, Col: len(b.Line(p.Line - 1))}, 1
	}
	return p, -1
}

// decl is vim's decl(): dec past the end-of-line position, so that it always
// ends on a real character.
func decl(b *text.Buffer, p text.Pos) (text.Pos, bool) {
	p, r := dec(b, p)
	if r == 1 && p.Col != 0 {
		p, r = dec(b, p)
	}
	return p, r != -1
}
