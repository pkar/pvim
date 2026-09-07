package search

import (
	"strconv"
	"strings"
)

// OffsetKind is which of the four things a /{pattern}/{offset} suffix means.
type OffsetKind int

const (
	// OffsetNone is no offset at all: the cursor lands on the first character
	// of the match and the motion stays exclusive.
	OffsetNone OffsetKind = iota
	// OffsetLine is /pat/3 and /pat/-2: N lines down or up from the match,
	// which makes the whole motion linewise. The cursor lands in column 1 and
	// not on the first non-blank, which is what vim's help says and what vim
	// does.
	OffsetLine
	// OffsetStart is /pat/s+2 and /pat/b-1: N characters from the start of the
	// match. The motion stays exclusive.
	OffsetStart
	// OffsetEnd is /pat/e and /pat/e-1: N characters from the last character
	// of the match, and it makes the motion inclusive, which is the whole
	// reason d/foo/e deletes the match and d/foo does not.
	OffsetEnd
)

// Offset is the suffix after a search pattern's closing separator.
type Offset struct {
	Kind OffsetKind
	// N is the signed count: 3 in /pat/e+3, -2 in /pat/-2, and zero in a bare
	// /pat/e.
	N int
}

// Linewise reports whether the offset makes the search motion linewise, which
// only a line offset does.
func (o Offset) Linewise() bool { return o.Kind == OffsetLine }

// Inclusive reports whether the offset makes the search motion inclusive,
// which only an end offset does. The mode machine turns these two booleans
// into a motion kind; they are booleans here so that this package does not
// have to know what a motion is.
func (o Offset) Inclusive() bool { return o.Kind == OffsetEnd }

// String prints the offset the way vim echoes it back on the message line,
// which is not always the way it was typed: /pat/1 comes back as /pat/+1,
// /pat/b+2 as /pat/s+2 and /pat/e-0 as /pat/e. A bare /pat/s is no offset at
// all and prints as nothing, because vim keeps the three fields and not the
// letter, and an s with no number sets none of them.
func (o Offset) String() string {
	var b strings.Builder
	switch o.Kind {
	case OffsetEnd:
		b.WriteByte('e')
	case OffsetStart:
		b.WriteByte('s')
	case OffsetLine:
		// The number carries the sign, and a line offset of zero still prints.
		if o.N == 0 {
			return "+0"
		}
	default:
		return ""
	}
	switch {
	case o.N > 0:
		b.WriteByte('+')
		b.WriteString(strconv.Itoa(o.N))
	case o.N < 0:
		b.WriteString(strconv.Itoa(o.N))
	}
	return b.String()
}

// Cmd is a search command line taken apart: what the user typed after the
// leading / or ?, split into the pattern and the offset.
type Cmd struct {
	// Pattern is the pattern in vim's dialect, with the separator unescaped
	// the one way vim unescapes it (see Parse). Empty means "the last
	// pattern", which is not an error here because deciding what to do about
	// it needs the State this package's parser does not have.
	Pattern string

	// Offset is what followed the closing separator.
	Offset Offset

	// KeepOffset says the command line was completely empty, which is the one
	// case where vim reuses the offset of the previous search as well as its
	// pattern. It is the difference between / and //: the first repeats
	// /foo/e, the second repeats the pattern and drops the offset, because
	// vim clears the three offset fields whenever there is anything at all to
	// parse and only then.
	KeepOffset bool
}

// ChainedSearchError is /pat/;/pat2, a search whose result is the starting
// point of a second one. It parses, it is real vim, and pvim does not run it;
// refusing by name beats searching for the first half and silently dropping
// the rest.
type ChainedSearchError struct{ Rest string }

func (e ChainedSearchError) Error() string {
	return "pvim: a chained search, /pat/;" + e.Rest + ", is not implemented"
}

// Parse splits a search command line into its pattern and its offset.
//
// The input is what the user typed after the / or the ?, without the leading
// separator: "foo", "foo/e+1", "foo\/bar/s-2". The separator is the same
// character the search started with, and it is escaped inside the pattern as
// \/ or \?.
//
// Vim unescapes exactly one of those two and not the other: a backward search
// rewrites \? to ? before it stores the pattern, and a forward search leaves
// \/ alone, so "?a\?b" searches for a?b and puts a?b in @/ while "/a\/b" puts
// a\/b there. Both work, because the translator reads \/ as /, and both are
// reproduced here because @/ is in the state cmd/oracle diffs.
//
// The separator does not end the pattern inside a [] collection, so /[a/]/e is
// the collection [a/] with an end offset, and neither does a backslash escape.
func Parse(line string, dir Direction) (Cmd, error) {
	if line == "" {
		return Cmd{KeepOffset: true}, nil
	}

	delim := byte('/')
	if dir == Backward {
		delim = '?'
	}
	pattern, rest, closed := splitPattern(line, delim)

	cmd := Cmd{Pattern: pattern}
	if !closed {
		return cmd, nil
	}
	off, n := parseOffset(rest)
	cmd.Offset = off
	if after := rest[n:]; strings.HasPrefix(after, ";") {
		return cmd, ChainedSearchError{Rest: after}
	}
	return cmd, nil
}

// splitPattern finds the separator that ends the pattern and returns the two
// halves, the first with \? unescaped when the separator is ?.
//
// It is vim's skip_regexp_ex(): a backslash takes the next character with it,
// a [] collection swallows everything to its ], and a [:] name nested inside
// one does not end it early.
func splitPattern(line string, delim byte) (pattern, rest string, closed bool) {
	var b strings.Builder
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == delim:
			return b.String(), line[i+1:], true

		case c == '[':
			j := skipCollection(line, i)
			b.WriteString(line[i:j])
			i = j

		case c == '\\' && i+1 < len(line):
			// Vim rewrites \? to ? in a backward search and nothing else in a
			// forward one, which is why this is not symmetric.
			if delim == '?' && line[i+1] == '?' {
				b.WriteByte('?')
			} else {
				b.WriteString(line[i : i+2])
			}
			i += 2

		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), "", false
}

// skipCollection returns the index just past the [] collection starting at i,
// or just past the [ when there is no closing ].
//
// The three leading special cases are vim's: ^ negates, and a ] or a - in
// first position is a literal rather than the end of the collection.
func skipCollection(line string, i int) int {
	j := i + 1
	if j < len(line) && line[j] == '^' {
		j++
	}
	if j < len(line) && (line[j] == ']' || line[j] == '-') {
		j++
	}
	for j < len(line) && line[j] != ']' {
		switch {
		case line[j] == '\\' && j+1 < len(line):
			j += 2
		case line[j] == '[' && j+1 < len(line) && (line[j+1] == ':' || line[j+1] == '=' || line[j+1] == '.'):
			// [:alpha:], [=a=] and [.a.] hold a ] that does not close the
			// collection around them.
			if k := strings.Index(line[j+2:], string([]byte{line[j+1], ']'})); k >= 0 {
				j += 2 + k + 2
			} else {
				j++
			}
		default:
			j++
		}
	}
	if j < len(line) {
		j++ // the closing ]
	}
	return j
}

// parseOffset reads vim's offset grammar off the front of s and returns it
// with the number of bytes it consumed.
//
// The grammar, from do_search(): a leading +, - or digit means a line offset;
// an e means from the end of the match and an s or a b from its start; then an
// optional signed number, where a lone + is +1 and a lone - is -1. Anything
// left over is ignored, exactly as vim ignores it, so /foo/exyz is /foo/e.
func parseOffset(s string) (Offset, int) {
	i := 0
	kind := OffsetNone
	switch {
	case i < len(s) && (s[i] == '+' || s[i] == '-' || isDigit(s[i])):
		kind = OffsetLine
	case i < len(s) && s[i] == 'e':
		kind = OffsetEnd
		i++
	case i < len(s) && (s[i] == 's' || s[i] == 'b'):
		kind = OffsetStart
		i++
	default:
		return Offset{}, 0
	}

	n := 0
	if i < len(s) && (isDigit(s[i]) || s[i] == '+' || s[i] == '-') {
		switch {
		case isDigit(s[i]) || (i+1 < len(s) && isDigit(s[i+1])):
			j := i
			if s[j] == '+' || s[j] == '-' {
				j++
			}
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			// A + prefix is not a sign strconv will take.
			n, _ = strconv.Atoi(strings.TrimPrefix(s[i:j], "+"))
			i = j
		case s[i] == '-':
			n = -1
			i++
		default:
			n = 1
			i++
		}
	}

	// An s or a b with no number sets none of vim's three offset fields, so it
	// is the same as no offset at all, and vim echoes it back as none.
	if kind == OffsetStart && n == 0 {
		return Offset{}, i
	}
	return Offset{Kind: kind, N: n}, i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
