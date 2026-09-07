package syntax

import "strings"

// A magic-aware scanner over a vim pattern.
//
// internal/regex has one of these already and this is not it. That one turns a
// pattern into RE2 source; this one only has to find where a handful of atoms
// stand, because everything:syntax does to a pattern -- splitting it at \zs,
// turning a leading \@<= into one, counting the capture groups in front of a
// split -- is surgery on the vim text before internal/regex ever sees it. The
// two agree about the magic table and about what a collection is, and about
// nothing else.

// magic is the level a pattern is being read at: \v, \m, \M or \V.
type magic int

const (
	veryMagic magic = iota
	magicOn
	noMagic
	veryNoMagic
)

// directive is how a magic level is spelled, so that a rewritten pattern can
// restore the level that was in force at the point it was cut.
func (m magic) directive() string {
	switch m {
	case veryMagic:
		return `\v`
	case noMagic:
		return `\M`
	case veryNoMagic:
		return `\V`
	}
	return `\m`
}

// tokenKind names the pattern atoms this package has to find.
type tokenKind int

const (
	tokOther       tokenKind = iota
	tokGroupOpen             // \( or ( -- a capturing group
	tokNCGroupOpen           // \%( or %( -- a non-capturing group
	tokGroupClose            // \) or )
	tokAlternate             // \| or |
	tokMatchStart            // \zs
	tokMatchEnd              // \ze
	tokLookahead             // \@=
	tokNegLook               // \@!
	tokLookbehind            // \@<=
	tokNegBehind             // \@<!
	tokAtomic                // \@>
	tokBackref               // \1 .. \9
	tokNewline               // \n
	tokAnyOf                 // \_x, the "and end of line" prefix
	tokBOF                   // \%^
	tokEOF                   // \%$
	tokBOL                   // ^ where it is magic
	tokZ                     // \z( \z1 .. \z9, :syntax's own external matches
	tokMagic                 // \v \m \M \V
	tokPosition              // \%23l, \%23c, \%23v, \%#, \%'m
	tokOptSeq                // \%[
	tokConcatAnd             // \&
)

// token is one atom, with the byte range it occupies, the group nesting depth
// it stood at, and the magic level in force when it was read.
type token struct {
	kind  tokenKind
	start int
	end   int
	depth int
	level magic
	// caps is how many capturing groups had been opened before this token.
	caps int
}

// scan walks a vim pattern and returns the tokens above.
//
// It is deliberately not a parser: everything it does not recognise is skipped
// as tokOther, and a pattern it reads wrongly still reaches internal/regex,
// which is the thing that decides whether the pattern is valid at all.
func scan(pat string, start magic) []token {
	var out []token
	level := start
	depth, caps := 0, 0
	i := 0
	add := func(k tokenKind, s, e int) {
		out = append(out, token{kind: k, start: s, end: e, depth: depth, level: level, caps: caps})
	}
	for i < len(pat) {
		c := pat[i]
		switch {
		case c == '\\' && i+1 < len(pat):
			n := pat[i+1]
			switch {
			case n == 'v':
				add(tokMagic, i, i+2)
				level = veryMagic
				i += 2
			case n == 'm':
				add(tokMagic, i, i+2)
				level = magicOn
				i += 2
			case n == 'M':
				add(tokMagic, i, i+2)
				level = noMagic
				i += 2
			case n == 'V':
				add(tokMagic, i, i+2)
				level = veryNoMagic
				i += 2
			case n == 'z' && i+2 < len(pat) && pat[i+2] == 's':
				add(tokMatchStart, i, i+3)
				i += 3
			case n == 'z' && i+2 < len(pat) && pat[i+2] == 'e':
				add(tokMatchEnd, i, i+3)
				i += 3
			case n == 'z':
				// \z( and \z1 .. \z9, the external match family.
				e := i + 2
				if e < len(pat) {
					e++
				}
				add(tokZ, i, e)
				i = e
			case n == '@':
				k, e := scanAt(pat, i)
				add(k, i, e)
				i = e
			case n == 'n':
				add(tokNewline, i, i+2)
				i += 2
			case n == '_':
				e := i + 2
				if e < len(pat) {
					e++
				}
				add(tokAnyOf, i, e)
				i = e
			case n == '%':
				k, e := scanPercent(pat, i, level)
				switch k {
				case tokNCGroupOpen:
					add(k, i, e)
					depth++
				default:
					add(k, i, e)
				}
				i = e
			case n == '&':
				add(tokConcatAnd, i, i+2)
				i += 2
			case n >= '1' && n <= '9':
				add(tokBackref, i, i+2)
				i += 2
			case n == '(' && level != veryMagic:
				add(tokGroupOpen, i, i+2)
				depth++
				caps++
				i += 2
			case n == ')' && level != veryMagic:
				depth--
				add(tokGroupClose, i, i+2)
				i += 2
			case n == '|' && level != veryMagic:
				add(tokAlternate, i, i+2)
				i += 2
			case n == '[' && level >= noMagic:
				// A collection, spelled with a backslash below magic.
				e := scanCollection(pat, i+1)
				add(tokOther, i, e)
				i = e
			default:
				add(tokOther, i, i+2)
				i += 2
			}
		case level == veryMagic && c == '(':
			add(tokGroupOpen, i, i+1)
			depth++
			caps++
			i++
		case level == veryMagic && c == ')':
			depth--
			add(tokGroupClose, i, i+1)
			i++
		case level == veryMagic && c == '|':
			add(tokAlternate, i, i+1)
			i++
		case level == veryMagic && c == '%' && i+1 < len(pat) && pat[i+1] == '(':
			add(tokNCGroupOpen, i, i+2)
			depth++
			i += 2
		case level == veryMagic && c == '@':
			// Very magic spells the lookaround family without a backslash.
			k, e := scanAtBare(pat, i)
			add(k, i, e)
			i = e
		case c == '[' && level <= magicOn:
			e := scanCollection(pat, i)
			add(tokOther, i, e)
			i = e
		case c == '^' && level <= magicOn:
			add(tokBOL, i, i+1)
			i++
		default:
			i++
		}
	}
	return out
}

// scanAt reads the \@ family starting at the backslash.
//
// Vim allows a digit between the @ and the <, which caps how far back a
// lookbehind will look: \@1<= is "one character back". It changes how long the
// search takes and not what matches, so the digits are skipped.
func scanAt(pat string, i int) (tokenKind, int) {
	// pat[i] == '\\', pat[i+1] == '@'
	j := i + 2
	for j < len(pat) && pat[j] >= '0' && pat[j] <= '9' {
		j++
	}
	i = j - 2
	rest := pat[j:]
	switch {
	case strings.HasPrefix(rest, "<="):
		return tokLookbehind, i + 4
	case strings.HasPrefix(rest, "<!"):
		return tokNegBehind, i + 4
	case strings.HasPrefix(rest, "="):
		return tokLookahead, i + 3
	case strings.HasPrefix(rest, "!"):
		return tokNegLook, i + 3
	case strings.HasPrefix(rest, ">"):
		return tokAtomic, i + 3
	}
	return tokOther, i + 2
}

// scanAtBare reads the same family in very magic, where the backslash is gone.
func scanAtBare(pat string, i int) (tokenKind, int) {
	j := i + 1
	for j < len(pat) && pat[j] >= '0' && pat[j] <= '9' {
		j++
	}
	i = j - 1
	rest := pat[j:]
	switch {
	case strings.HasPrefix(rest, "<="):
		return tokLookbehind, i + 3
	case strings.HasPrefix(rest, "<!"):
		return tokNegBehind, i + 3
	case strings.HasPrefix(rest, "="):
		return tokLookahead, i + 2
	case strings.HasPrefix(rest, "!"):
		return tokNegLook, i + 2
	case strings.HasPrefix(rest, ">"):
		return tokAtomic, i + 2
	}
	return tokOther, i + 1
}

// scanPercent reads the \% family starting at the backslash.
func scanPercent(pat string, i int, level magic) (tokenKind, int) {
	rest := pat[i+2:]
	switch {
	case strings.HasPrefix(rest, "("):
		return tokNCGroupOpen, i + 3
	case strings.HasPrefix(rest, "^"):
		return tokBOF, i + 3
	case strings.HasPrefix(rest, "$"):
		return tokEOF, i + 3
	case strings.HasPrefix(rest, "["):
		return tokOptSeq, i + 3
	case strings.HasPrefix(rest, "#"):
		return tokPosition, i + 3
	case strings.HasPrefix(rest, "'"):
		return tokPosition, i + 4
	case strings.HasPrefix(rest, "d"), strings.HasPrefix(rest, "x"),
		strings.HasPrefix(rest, "o"), strings.HasPrefix(rest, "u"),
		strings.HasPrefix(rest, "U"):
		// \%d123 and friends: a literal character by number.
		j := i + 3
		for j < len(pat) && isHexDigit(pat[j]) {
			j++
		}
		return tokOther, j
	case strings.HasPrefix(rest, "C"), strings.HasPrefix(rest, "V"):
		return tokPosition, i + 3
	}
	// \%23l, \%<23c, \%>23v.
	j := i + 2
	if j < len(pat) && (pat[j] == '<' || pat[j] == '>') {
		j++
	}
	k := j
	for k < len(pat) && pat[k] >= '0' && pat[k] <= '9' {
		k++
	}
	if k > j && k < len(pat) && (pat[k] == 'l' || pat[k] == 'c' || pat[k] == 'v') {
		return tokPosition, k + 1
	}
	return tokOther, i + 2
}

// scanCollection returns the byte just past a [] collection whose '[' is at i.
//
// Vim's rules, and Go's: a ']' first in the collection is a literal, a '^'
// before it does not count, [:class:] and [=a=] and [.a.] nest one level, and a
// backslash escapes the next byte.
func scanCollection(pat string, i int) int {
	j := i + 1
	if j < len(pat) && pat[j] == '^' {
		j++
	}
	if j < len(pat) && pat[j] == ']' {
		j++
	}
	for j < len(pat) {
		switch {
		case pat[j] == '\\' && j+1 < len(pat):
			j += 2
		case pat[j] == '[' && j+1 < len(pat) && (pat[j+1] == ':' || pat[j+1] == '=' || pat[j+1] == '.'):
			close := string(pat[j+1]) + "]"
			k := strings.Index(pat[j+2:], close)
			if k < 0 {
				j += 2
				continue
			}
			j += 2 + k + 2
		case pat[j] == ']':
			return j + 1
		default:
			j++
		}
	}
	// An unclosed collection is not a collection at all: vim reads the '[' as
	// a literal. Report one byte so the caller carries on.
	return i + 1
}

// isHexDigit is here rather than in a table because scanPercent is the only
// caller and a package for one predicate is worse than four comparisons.
func isHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}
