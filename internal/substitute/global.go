package substitute

import (
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/regex"
	"github.com/pkar/pvim/internal/text"
)

// NotFoundMessage is what ":g" says when its pattern matched no line.
//
// It is a message and not an error, and it carries no E-code: vim prints
// "Pattern not found: zzz" for ":g/zzz/d" and "E486: Pattern not found: zzz"
// for ":s/zzz/x/", which is not a typo in either place and is measured in
// both.
func NotFoundMessage(pattern string) string { return "Pattern not found: " + pattern }

// Marks returns the lines a ":g" or a ":v" selected, in the order vim runs its
// command over them, and the pattern it used.
//
// Two passes, and that is the whole reason this exists as its own function:
// vim marks every matching line first and only then runs the command, so a
// command that deletes lines does not change which lines were selected. A
// one-pass implementation of ":g/^/m0" reverses a file the first time and
// hangs the second.
//
// The pattern comes back because the caller needs it for NotFoundMessage, and
// because an empty one on the command line may have resolved to something the
// caller never saw.
//
// It also writes the pattern into BOTH of the state's slots, which is vim's
// RE_BOTH and is why ":g/x/p" followed by ":%s//Q/" replaces x rather than
// whatever was searched for before.
func Marks(b *text.Buffer, first, last int, g Global, st *State, o *options.Options) (*Marked, string, error) {
	if st == nil {
		st = &State{}
	}
	pattern := g.Pattern
	if !g.HavePattern || pattern == "" {
		pattern = st.SubPattern
		if g.Which == FromLast {
			pattern = st.last()
		}
		if pattern == "" {
			return nil, "", ErrNoPrevPattern
		}
	}
	st.NoteGlobal(pattern)

	re, err := regex.Compile(pattern, RegexOptions(o))
	if err != nil {
		return nil, pattern, err
	}

	if first < 1 {
		first = 1
	}
	if last > b.LineCount() {
		last = b.LineCount()
	}
	var lines []int
	for n := first; n <= last; n++ {
		if re.Match(b.Line(n)) != g.Invert {
			lines = append(lines, n)
		}
	}
	return &Marked{lines: lines}, pattern, nil
}

// Marked is the list of lines a ":g" selected, walked in order, with the
// bookkeeping a command that changes the line count needs.
//
// Vim marks the lines themselves, so a ":g/x/d" that deletes line 1 leaves the
// mark on what used to be line 3 pointing at line 2 with no arithmetic
// anywhere. Line numbers cannot do that on their own, so the caller says how
// many lines its command added or removed and Marked carries the difference
// forward. That is exact for the commands people run under a ":g" -- delete,
// substitute, normal, move -- and it is the one place the ex layer has to
// remember to say something.
type Marked struct {
	lines []int
	i     int
	shift int
}

// Len is how many lines were selected, before anything ran.
func (m *Marked) Len() int { return len(m.lines) }

// Next gives the next line to act on, already moved by whatever the earlier
// commands did, and reports false when there are none left.
func (m *Marked) Next() (int, bool) {
	if m == nil || m.i >= len(m.lines) {
		return 0, false
	}
	n := m.lines[m.i] + m.shift
	m.i++
	return n, true
}

// Shift records that the command just run changed the buffer's line count by
// delta, so the lines still to come move with it.
func (m *Marked) Shift(delta int) {
	if m != nil {
		m.shift += delta
	}
}
