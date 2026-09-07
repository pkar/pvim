// Package search runs a pattern over a buffer: the destination /, ?, n, N, *
// and # land on, and the matches 'hlsearch' paints.
//
// It owns the pattern state that outlives one search -- the last pattern, the
// direction it ran in and the offset that came with it -- because n is not "the
// search again", it is "the last search, in its own direction, with its own
// offset", and an editor that keeps only the pattern gets /foo/e right once and
// then wrong on every n after it.
//
// It does not own the command line and it does not own the motion. The mode
// machine reads the pattern from the user, asks here for the destination, and
// turns the answer into a motion of its own, which is what keeps this package
// testable with no keyboard and free of internal/motion.
//
// Vim's dialect goes through internal/regex, which is the only place in this
// module allowed to import the standard library's regexp. The atoms RE2 cannot
// express -- \zs, \ze, \@<=, \@!, \1 and \%V -- come back from Compile as a
// regex.Refused with the atom named, and the caller puts the E-code on the
// message line.
//
// # Positions are raw
//
// Every position this package returns is where vim's do_search() leaves its
// pos_T, which is not always a place a normal-mode cursor may sit: /$ lands on
// the byte after the last one on the line and vim's check_cursor() pulls it
// back afterwards. Clamping here would be wrong twice over, because the same
// position is the exclusive end of the d/foo motion, where the byte past the
// end is exactly the point. The caller clamps when it puts a cursor there and
// not before.
//
// # Where this was checked
//
// Every rule here was run through /opt/homebrew/bin/vim 9.2.321 rather than
// read out of the help, and vim_test.go and random_test.go keep those runs as
// tests: a table of the behaviours this package claims, and sixteen thousand
// generated cases that found five of the rules nobody would have thought to
// write down. The rules that are subtle enough to look like bugs are also
// checked against vim's own searchit() and nv_next(), and where a comment here
// names one of those it means the C was read and not guessed at.
//
// Two things are known to differ and are not fixed. A pattern that can match
// zero characters loses matches Go's regexp will not report, which is written
// up on the gap in cases_test.go. And 'iskeyword' above code point 255 is
// decided by Unicode's categories rather than by vim's own table, which is
// keywordSet.has.
package search

import (
	"github.com/pkar/pvim/internal/regex"
	"github.com/pkar/pvim/internal/text"
)

// Direction is which way a search runs.
type Direction int

const (
	// Forward is /, and n after a /.
	Forward Direction = iota
	// Backward is ?, and n after a ?.
	Backward
)

// Reverse flips the direction, which is the whole of what N is and what the
// ? half of n does.
func (d Direction) Reverse() Direction {
	if d == Forward {
		return Backward
	}
	return Forward
}

// String prints the direction as the character that starts the command line,
// which is what the search prompt shows.
func (d Direction) String() string {
	if d == Backward {
		return "?"
	}
	return "/"
}

// Options are the editor settings a search's answer depends on.
type Options struct {
	// IgnoreCase and SmartCase are the two options of those names, handed
	// through to internal/regex, where \c and \C in the pattern override both.
	IgnoreCase bool
	SmartCase  bool
	// NoMagic is 'magic' turned off. Nothing sets it and it is here so that a
	// pattern compiled under it cannot silently mean something else.
	NoMagic bool
	// WrapScan is 'wrapscan': whether a search that runs off the end starts
	// again at the other one. On by default, and the difference between
	// "search hit BOTTOM, continuing at TOP" and E385.
	WrapScan bool
	// LastSubstitute is what a ~ in the pattern expands to.
	LastSubstitute string
	// IsKeyword is the 'iskeyword' option, as vim spells it. Empty means the
	// default, "@,48-57,_,192-255", which is what --clean vim reports and what
	// decides the word * and # take.
	IsKeyword string
}

// DefaultOptions is vim's own defaults.
func DefaultOptions() Options { return Options{WrapScan: true} }

// regexOptions is the view internal/regex wants.
func (o Options) regexOptions() regex.Options {
	return regex.Options{
		IgnoreCase:     o.IgnoreCase,
		SmartCase:      o.SmartCase,
		NoMagic:        o.NoMagic,
		LastSubstitute: o.LastSubstitute,
	}
}

// Match is one match in the buffer.
//
// Start and End are positions and not offsets into a line because a vim
// pattern can match across a line break through \n and \_., so a match is not
// necessarily inside one line. End is exclusive, as every text.Range's is.
type Match struct {
	Start, End text.Pos
}

// Range is the match as a half-open range, which is what a:s and a highlight
// both want.
func (m Match) Range() text.Range { return text.Range{Start: m.Start, End: m.End} }

// Compile translates and compiles a vim pattern under the search options.
//
// It is a thin wrapper over regex.Compile and it exists so that every search
// in the editor picks up 'ignorecase', 'smartcase' and 'magic' the same way,
// rather than each caller assembling a regex.Options of its own and one of
// them forgetting 'smartcase'.
func Compile(pattern string, opt Options) (*regex.Regexp, error) {
	return regex.Compile(pattern, opt.regexOptions())
}
