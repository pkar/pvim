package regex

import (
	"unicode"
	"unicode/utf8"
)

// Magic is one of vim's four magic levels. It decides, for every punctuation
// character, whether the bare character is an operator and the backslashed one
// is a literal, or the other way round.
//
// The zero value is VeryMagic and not the default level on purpose: a Magic
// always arrives from Options, which names the level it wants, and a zero value
// that silently meant "whatever vim does by default" would hide the one bug
// this type exists to prevent.
type Magic int

// The four levels, in the order the table below is indexed.
const (
	VeryMagic   Magic = iota // \v
	MagicOn                  // \m, and what 'magic' on means, which is the default
	NoMagic                  // \M, and what 'magic' off means
	VeryNoMagic              // \V
)

// String gives the atom that switches to this level, which is also how the
// level is spelled everywhere in vim's own documentation.
func (m Magic) String() string {
	switch m {
	case VeryMagic:
		return `\v`
	case MagicOn:
		return `\m`
	case NoMagic:
		return `\M`
	case VeryNoMagic:
		return `\V`
	}
	return "invalid"
}

// bareSpecial is the table the whole dialect turns on: for each character that
// has both a bare and a backslashed form, whether the bare form is the
// operator at each of the four levels. Index by Magic.
//
// The rule that reads it is one line in both directions. A bare character is an
// operator when its entry is true; a backslashed character is an operator when
// its entry is false. That is why `\.` is a literal dot under 'magic' and any
// character under \V, with no second table and no special cases.
//
// Sourced from :help /magic and checked atom by atom against vim 9.2 with
// match(), because the help's table stops at eleven rows and the dialect does
// not.
var bareSpecial = map[byte][4]bool{
	// \v \m \M \V
	'.': {true, true, false, false},
	'*': {true, true, false, false},
	'[': {true, true, false, false},
	'~': {true, true, false, false},
	'^': {true, true, true, false},
	'$': {true, true, true, false},
	'(': {true, false, false, false},
	')': {true, false, false, false},
	'|': {true, false, false, false},
	'+': {true, false, false, false},
	'?': {true, false, false, false},
	'=': {true, false, false, false},
	'{': {true, false, false, false},
	'<': {true, false, false, false},
	'>': {true, false, false, false},
	'%': {true, false, false, false},
	'@': {true, false, false, false},
	'&': {true, false, false, false},
}

// bareIsOperator reports whether c standing on its own is an operator at level
// m. Everything not in the table is a literal at every level, which is most of
// the character set.
func bareIsOperator(c byte, m Magic) bool {
	e, ok := bareSpecial[c]
	if !ok {
		return false
	}
	return e[m]
}

// escapedIsOperator reports whether backslash-c is an operator at level m. It
// is the exact negation of bareIsOperator for the characters in the table:
// exactly one of the two forms is the operator and the other is the literal.
//
// Characters outside the table, \s and \zs and the rest, are handled by the
// translator before this is ever asked, because their bare form is a letter and
// letters are never operators.
func escapedIsOperator(c byte, m Magic) bool {
	e, ok := bareSpecial[c]
	if !ok {
		return false
	}
	return !e[m]
}

// hasUpper reports whether the pattern contains an uppercase character, which
// is the question 'smartcase' asks.
//
// This is vim's pat_has_uppercase from search.c, and it is copied rather than
// approximated because the skipping is the whole behaviour: \%d65 has an upper
// case A in it and does not make a search case-sensitive, and neither does \A
// or \_S, so a naive scan for any uppercase rune gets smartcase wrong on every
// pattern with a character class in it.
func hasUpper(pat string) bool {
	for i := 0; i < len(pat); {
		if pat[i] == '\\' {
			switch {
			case i+2 < len(pat) && (pat[i+1] == '_' || pat[i+1] == '%'):
				i += 3
			case i+1 < len(pat):
				i += 2
			default:
				i++
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(pat[i:])
		if unicode.IsUpper(r) {
			return true
		}
		i += size
	}
	return false
}
