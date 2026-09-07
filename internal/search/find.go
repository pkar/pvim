package search

import (
	"strings"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/regex"
	"github.com/pkar/pvim/internal/text"
)

// Query is one run of the matcher, with everything it needs to decide which
// matches count and none of the offset arithmetic that happens afterwards.
type Query struct {
	// Re is the compiled pattern.
	Re *regex.Regexp
	// From is where the search starts. A match at this position does not
	// count, which is what makes n advance rather than stick.
	//
	// It may be outside the buffer, which is where a character offset that
	// ran off an end leaves it: line 0 is before the first line and line
	// LineCount+1 is after the last. Each means the whole buffer lies one way
	// and none of it the other, so ?pat?e+3 from the third character of the
	// file has nothing above it to find.
	From text.Pos
	// Dir is which way to walk.
	Dir Direction
	// Count is the [count] on the command: 3/foo finds the third match, each
	// one starting from the last.
	Count int
	// End says to judge a match by its last character rather than its first,
	// which is what the e offset does to the "already there" rule: /foo/e with
	// the cursor on the o of foo finds the same foo again, and /foo does not.
	// It applies to the first match of a count and not to the rest; see
	// acceptForward.
	End bool
}

// Hit is where a search landed, before any offset moved it.
type Hit struct {
	Match Match
	// Wrapped says the scan ran off one end of the buffer and started again at
	// the other, which is a message vim prints before it says anything else,
	// including before the E486 of a search that then found nothing.
	Wrapped bool
	// Wraps is how many times it happened, which a count makes more than one.
	// vim warns once per wrap and not once per command: "3*" on a file with
	// one match says "search hit BOTTOM, continuing at TOP" three times, plus
	// the fourth the redraw puts back.
	Wraps int
}

// Find is the search itself: the Count'th match strictly after From when Dir
// is Forward, strictly before it when Backward.
//
// Strictly, in both directions: vim never finds the match the cursor is
// already sitting on. A search that runs off the end starts again at the other
// with 'wrapscan' on, and returns E385 or E384 with it off; with it on and
// nothing anywhere, E486.
//
// The pattern is needed for the message text and Re does not carry the offset
// the user typed, so the error carries Re.String(), which is the pattern as
// typed.
func Find(b *text.Buffer, q Query, opt Options) (Hit, error) {
	if q.Count < 1 {
		q.Count = 1
	}
	m := newMatcher(b, q.Re)

	hit := Hit{}
	from := q.From
	for i := 0; i < q.Count; i++ {
		found, wrapped, ok := m.once(from, q.Dir, q.End, i == 0, opt.WrapScan)
		hit.Wrapped = hit.Wrapped || wrapped
		if wrapped {
			hit.Wraps++
		}
		if !ok {
			return Hit{Wrapped: hit.Wrapped, Wraps: hit.Wraps}, notFound(q.Re.String(), q.Dir, opt)
		}
		hit.Match = found
		// The next round starts where this one landed, which for an end
		// search is the match's last character and not its first.
		from = found.Start
		if q.End {
			from = EndPos(b, found)
		}
	}
	return hit, nil
}

// FindLine returns every non-overlapping match on one line, in order.
//
// This is what 'hlsearch' walks, once per line on screen per frame, so it
// takes the compiled pattern and allocates only the result. It cannot see a
// match that spans a line break, and 'hlsearch' in vim highlights those; that
// is a known gap and belongs in the register the day a pattern needs it.
func FindLine(b *text.Buffer, re *regex.Regexp, line int) []Match {
	if line < 1 || line > b.LineCount() {
		return nil
	}
	return matchesWithin(b.Line(line), line, re)
}

// matcher answers "the matches whose first byte is on line n", which is the
// one question the scan asks, and hides which of the two ways it was answered.
//
// Line by line is the fast way and the way vim's own regexec works when the
// pattern cannot match a line break: one line in, matches out, nothing else
// touched. A pattern that can match a NL -- \n, \_., \_s, \_S -- needs the
// buffer as one string, because a match starting on line 4 may end on line 6,
// and that costs a copy of the buffer and a pass over all of it. Which is why
// it is not the default: a search on a 40,000 line log has to stop at the
// first match and not read the other 39,000 lines.
type matcher struct {
	b  *text.Buffer
	re *regex.Regexp

	multi  bool
	byLine map[int][]Match
}

// newMatcher picks the path and, for the multi-line one, does its single pass
// over the buffer up front.
func newMatcher(b *text.Buffer, re *regex.Regexp) *matcher {
	m := &matcher{b: b, re: re, multi: crossesLines(re.Source())}
	if m.multi {
		m.byLine = allMatches(b, re)
	}
	return m
}

// on returns the matches starting on line n, leftmost first.
func (m *matcher) on(n int) []Match {
	if n < 1 || n > m.b.LineCount() {
		return nil
	}
	if m.multi {
		return m.byLine[n]
	}
	return matchesWithin(m.b.Line(n), n, m.re)
}

// once is one iteration of the scan: forward or backward from a position,
// wrapping once when 'wrapscan' allows it.
//
// The shape is vim's searchit(): walk the lines outward from the cursor's,
// asking acceptForward or acceptBackward whether a match is far enough along
// to count; then, if nothing was found and 'wrapscan' is on, come round the
// other end and walk back to the cursor's line, this time taking whatever is
// there. That second pass is why a search for the word already under the
// cursor finds it again instead of failing.
//
// Forward takes the first match on a line and backward the last, and forward
// asks about the cursor's line only while backward asks about every line, both
// of which are vim's and both of which acceptBackward explains.
func (m *matcher) once(from text.Pos, dir Direction, end, firstMatch, wrapscan bool) (Match, bool, bool) {
	last := m.b.LineCount()

	// A starting line outside the buffer is a character offset that ran off an
	// end. Coming from the far side it means the whole buffer, with no column
	// to clear; coming from the near side it means the first pass has nothing
	// to look at and only a wrap can find anything.
	start, free := from.Line, false
	switch {
	case dir == Forward && start < 1:
		start, free = 1, true
	case dir == Backward && start > last:
		start, free = last, true
	case dir == Forward && start > last:
		start = last + 1
	case dir == Backward && start < 1:
		start = 0
	}

	if dir == Forward {
		for n := start; n <= last; n++ {
			for _, mt := range m.on(n) {
				if n == start && !free && !acceptForward(m.b, mt, from, end && firstMatch) {
					continue
				}
				return mt, false, true
			}
		}
		if !wrapscan {
			return Match{}, false, false
		}
		for n := 1; n <= min(start, last); n++ {
			if ms := m.on(n); len(ms) > 0 {
				return ms[0], true, true
			}
		}
		return Match{}, true, false
	}

	for n := start; n >= 1; n-- {
		ms := m.on(n)
		for i := len(ms) - 1; i >= 0; i-- {
			if !free && !acceptBackward(ms[i], from, end) {
				continue
			}
			return ms[i], false, true
		}
	}
	if !wrapscan {
		return Match{}, false, false
	}
	for n := last; n >= max(start, 1); n-- {
		if ms := m.on(n); len(ms) > 0 {
			return ms[len(ms)-1], true, true
		}
	}
	return Match{}, true, false
}

// acceptForward decides whether a match on the cursor's own line is far
// enough along to count. Matches on later lines are past any column on this
// one and are never asked about, which is vim's at_first_line.
//
// byEnd is the SEARCH_END rule, and it is not simply "the offset was e". Vim's
// searchit() carries a first_match flag that goes false as soon as it has
// found one, so on a count of three only the first of the three is judged by
// where the match ends and the other two are judged by where it starts. That
// is not documented anywhere and it is the difference between 3/foo/e landing
// on the third foo and landing on the fourth.
//
// The rest is vim's line for line:
//
// - the bar is the cursor's column plus the byte length of the character
// under it, not plus one, so that a search from an á does not find a match
// that starts inside it;
// - a match that runs onto the next line is past any column on this one, so
// the end rule takes it;
// - a match sitting on the byte after the last one on the line counts as one
// to its left, or /$ would match the end of the line the cursor is already
// at the end of, and n would never leave the line.
func acceptForward(b *text.Buffer, m Match, from text.Pos, byEnd bool) bool {
	bar := from.Col + charLen(b.Line(from.Line), from.Col)

	if byEnd {
		if m.End.Line != m.Start.Line {
			return true
		}
		return m.End.Col-1 >= bar
	}

	key := m.Start.Col
	if key >= len(b.Line(m.Start.Line)) {
		key--
	}
	return key >= bar
}

// acceptBackward is the mirror, and it is not the mirror image, in three ways
// that each cost a differential case to find.
//
// It is asked about every line and not only the cursor's, because with an end
// offset the position being judged is where the match ends, which for a
// pattern with a line break in it is below the line the match starts on. It
// compares that end position raw -- the end line and the column one to the
// left of the end, with no walk back onto the previous line when the match
// ends in column zero, which is a step vim takes only after the match has been
// chosen. And it has no character-length term at all: vim's extra_col is the
// character's length for a forward search and zero for a backward one, so the
// bar here is the cursor's own column and the comparison is strict.
//
// It also has no first_match rule. Only the forward branch has one.
func acceptBackward(m Match, from text.Pos, byEnd bool) bool {
	p := m.Start
	if byEnd {
		p = text.Pos{Line: m.End.Line, Col: m.End.Col - 1}
	}
	if p.Line != from.Line {
		return p.Line < from.Line
	}
	return p.Col < from.Col
}

// matchesWithin runs the pattern over one line's bytes.
//
// FindAllIndex and not a loop of FindIndex from a column: vim's 'cpoptions'
// carries c by default, which makes a search continue at the end of the match
// it just rejected rather than one character into it, so the matches vim can
// reach on a line are exactly the non-overlapping ones from the left. /aa on
// "aaaa" finding column 3 and never column 2 is that rule, and it is the
// reason this package needs no "find a match at or after column n" primitive
// that RE2 does not offer.
//
// With one cut at the end: vim gives up on a line the moment the column it
// would resume at is the one after its last byte, so a second match cannot
// start there even though a first one can. That is what stops /b* on "ab"
// reporting an empty match after the b.
func matchesWithin(line []byte, n int, re *regex.Regexp) []Match {
	idx := re.FindAllIndex(line, -1)
	if idx == nil {
		return nil
	}
	out := make([]Match, 0, len(idx))
	for i, p := range idx {
		if i > 0 && resumeCol(line, idx[i-1]) >= len(line) {
			break
		}
		out = append(out, Match{
			Start: text.Pos{Line: n, Col: p[0]},
			End:   text.Pos{Line: n, Col: p[1]},
		})
	}
	return out
}

// resumeCol is the column vim's line scan carries on from after a match: the
// end of it, and one character further when the match was empty and there is a
// character to step over.
func resumeCol(line []byte, m []int) int {
	c := m[1]
	if c == m[0] && c < len(line) {
		c += charLen(line, c)
	}
	return c
}

// allMatches is the multi-line path's scan, grouped by the line each match
// starts on.
//
// Not one FindAllIndex over the joined buffer, which is the obvious thing and
// is wrong. Vim matches a multi-line pattern by asking the engine for a match
// starting on one particular line, once per line, so a match consumed on line
// 4 does not stop a different match starting at column 0 of line 5. A single
// global scan gives the non-overlapping sequence from the top of the file
// instead, and over "ab\tab" with \_a\_a it reports the match that begins at
// the line break above and never the one that begins at column 0, which is the
// one vim finds and lands on.
//
// So: one scan per line, from that line's first byte, keeping the matches that
// begin before the next line does. Slicing at a line start and not at an
// arbitrary column is what makes it safe -- ^ and \< mean the same thing at the
// start of a slice as they do just after a NL. The exception is \%^, which
// internal/regex compiles to \A and which would match at the start of every
// line's slice rather than only at the start of the file; a pattern that has
// both a \%^ and a line break in it gets that wrong, and nothing else does.
//
// The cost is one pass over the buffer and not one per line: when the first
// match from line n starts on a later line, every line in between has nothing
// and the loop jumps straight there.
func allMatches(b *text.Buffer, re *regex.Regexp) map[int][]Match {
	joined, starts := join(b)
	out := map[int][]Match{}
	for n := 1; n <= len(starts); n++ {
		first := re.FindIndex(joined[starts[n-1]:])
		if first == nil {
			break
		}
		if line := posAt(starts, starts[n-1]+first[0]).Line; line > n {
			n = line - 1 // the for's n++ lands on the line that has it
			continue
		}
		out[n] = matchesOnLine(re, joined, starts, n)
	}
	return out
}

// matchesOnLine returns the matches that begin on line n, which may end on a
// later one.
//
// The count grows rather than being -1 because the scan runs to the end of the
// buffer: asking for every match in a 40,000 line file to find the two on line
// three is the difference between a search and a stall.
//
// The same end-of-line cut as matchesWithin, for the same reason, and here it
// is what keeps a match that begins at the line break out of the list when
// something on the line came first: over "aa" followed by "a", \_a\_a matches
// the two a's and then the break and the a below it, and vim only ever finds
// the second of those as the first thing it tries on the line.
func matchesOnLine(re *regex.Regexp, joined []byte, starts []int, n int) []Match {
	lo := starts[n-1]
	hi := len(joined)
	if n < len(starts) {
		hi = starts[n]
	}
	lineEnd := hi - 1 // the NL that ends the line
	seg := joined[lo:]

	var out []Match
	for k := 2; ; k *= 4 {
		idx := re.FindAllIndex(seg, k)
		out = out[:0]
		for i, p := range idx {
			s := lo + p[0]
			if s >= hi {
				return out
			}
			if i > 0 && lo+resumeOff(seg, idx[i-1]) >= lineEnd {
				return out
			}
			out = append(out, Match{
				Start: posAt(starts, s),
				End:   posAt(starts, lo+p[1]),
			})
		}
		if len(idx) < k {
			return out
		}
	}
}

// resumeOff is resumeCol over the joined buffer.
func resumeOff(seg []byte, m []int) int {
	c := m[1]
	if c == m[0] && c < len(seg) {
		c += charLen(seg, c)
	}
	return c
}

// join glues the buffer into one slice with LF between lines and returns the
// byte offset each line starts at, indexed from zero for line 1.
func join(b *text.Buffer) ([]byte, []int) {
	n := b.LineCount()
	starts := make([]int, n)
	size := 0
	for i := 1; i <= n; i++ {
		starts[i-1] = size
		size += len(b.Line(i)) + 1
	}
	joined := make([]byte, 0, size)
	for i := 1; i <= n; i++ {
		joined = append(joined, b.Line(i)...)
		joined = append(joined, '\n')
	}
	return joined, starts
}

// posAt turns an offset into the joined buffer back into a line and column.
func posAt(starts []int, off int) text.Pos {
	lo, hi := 0, len(starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return text.Pos{Line: lo + 1, Col: off - starts[lo]}
}

// crossesLines reports whether a translated pattern could match a line break,
// which decides whether the search has to look at the buffer as one string.
//
// It reads the Go source internal/regex emitted rather than the vim pattern,
// because that is where the question is decided: the translator turns [^a]
// into [^a\n] precisely so that a vim collection cannot cross a line, and \_a
// into [A-Za-z\n] precisely so that vim's underscore forms can. So the three
// things to look for are an explicit \n, a collection whose membership admits
// one, and the (?s) that \_. compiles to. Everything else in the emitted
// dialect -- literals, a dot without (?s) -- cannot.
//
// Wrong in the safe direction only where it is wrong at all: a false yes costs
// one scan of the buffer, a false no would miss a match, and every construct
// that can match a NL is listed above.
func crossesLines(src string) bool {
	for i := 0; i < len(src); i++ {
		switch {
		case src[i] == '\\' && i+1 < len(src):
			if src[i+1] == 'n' {
				return true
			}
			i++
		case strings.HasPrefix(src[i:], "(?") && strings.ContainsRune(flagsOf(src[i:]), 's'):
			return true
		case src[i] == '[':
			j := skipCollection(src, i)
			body := src[i:j]
			// A collection matches a NL if it lists one and is not negated,
			// or if it is negated and does not list one. The translator emits
			// both shapes: \_a becomes [A-Za-z\n] and vim's own [^a] becomes
			// [^a\n], the second so that a vim collection cannot cross a line
			// where Go's would.
			if strings.Contains(body, `\n`) != strings.HasPrefix(body, "[^") {
				return true
			}
			i = j - 1
		}
	}
	return false
}

// flagsOf reads the letters of a (?flags) or (?flags: group opener.
func flagsOf(s string) string {
	for i := 2; i < len(s); i++ {
		if s[i] == ':' || s[i] == ')' {
			return s[2:i]
		}
		if s[i] == '-' {
			return s[2:i]
		}
	}
	return ""
}

// charLen is the byte length of the character at column col, and 1 when col is
// past the end of the line. It is vim's start_char_len, which is what a
// forward search adds to the cursor's column to get the first column a match
// may start in.
func charLen(line []byte, col int) int {
	if col < 0 || col >= len(line) {
		return 1
	}
	_, n := utf8.DecodeRune(line[col:])
	if n < 1 {
		return 1
	}
	return n
}

// EndPos is where a search with an e offset leaves the cursor: the match's
// last character.
//
// Three cases, all vim's. An empty match has no last character and the answer
// is where it starts. A match that ends in column zero ends with the line
// break above, so its last character is the byte after the last one on that
// line, which is a position no cursor sits on and every caller clamps. Anything
// else steps back one character, whole and not one byte.
func EndPos(b *text.Buffer, m Match) text.Pos {
	if m.Start == m.End {
		return m.Start
	}
	p := m.End
	if p.Col == 0 {
		if p.Line > 1 {
			p.Line--
			p.Col = len(b.Line(p.Line))
		}
		return p
	}
	line := b.Line(p.Line)
	p.Col--
	for p.Col > 0 && p.Col < len(line) && !utf8.RuneStart(line[p.Col]) {
		p.Col--
	}
	return p
}
