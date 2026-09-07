package substitute

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// tilde is vim's regtilde: it replaces "~" in a replacement with the previous
// replacement, before anything else looks at the string.
//
// Three things about it are not obvious and all three are measured:
//
// - the text it inserts is NOT rescanned. ":s/a/X/" then ":s/b/~Y/" then
// ":s/c/~Z/" gives X, XY, XYZ, because the second "~" sees the string the
// first one produced and stops there.
// - it does not remove the backslash from "\~". That is left for Expand,
// where a backslash before anything means the character itself, so the two
// stages agree without either knowing about the other.
// - the result is what gets remembered, which is why the chain above grows.
// Under 'nomagic' the two spellings swap, so "~" is literal and "\~" is
// the previous replacement.
func tilde(s, prev string, have, magic bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case magic && s[i] == '~', !magic && s[i] == '\\' && i+1 < len(s) && s[i+1] == '~':
			if !magic {
				i++ // step over the backslash; the "~" is at i now
			}
			if have {
				b.WriteString(prev)
			}
		case s[i] == '\\' && i+1 < len(s):
			b.WriteByte(s[i])
			b.WriteByte(s[i+1])
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// caseFold is the state \u, \U, \l, \L, \e and \E leave behind.
type caseFold int

const (
	foldNone caseFold = iota
	foldUpperOne
	foldLowerOne
	foldUpperRun
	foldLowerRun
)

// Expand renders one replacement against one match.
//
// It is exported and separate from Do because it is the piece with the grammar
// in it and the piece a table test can cover exhaustively. groups is the match
// and its subexpressions, groups[0] being the whole match, exactly the shape
// FindSubmatchIndex hands back; a group that did not take part is nil and
// renders as nothing, which is what vim does.
//
// magic decides only which of "&" and "\&" means the whole match. "~" is not
// special here at all: tilde has already dealt with it, and vim's vim_regsub
// does not look at it either.
//
// The grammar, every entry of it measured against vim 9.2.321:
//
//	\0 .. \9 the whole match and the subexpressions
//	& the whole match, when 'magic'
//	\& the whole match, when 'nomagic'; a literal & otherwise
//	\u \l the next character upper-cased or lower-cased
//	\U \L every character until \e or \E
//	\e \E end a \U or \L
//	\r a line break
//	\n a NUL byte, which is the one everybody gets backwards
//	\t \b a tab and a backspace
//	\\ a backslash
//	\x x, for any other x
//
// A "\u" inside a "\U" applies to one character and the run carries on after
// it, so "\Uab\lCD" is "ABcD". A run survives a "\r", so "\Uab\rcd" is "AB"
// and "CD" on two lines.
func Expand(replacement string, groups [][]byte, magic bool) ([]byte, error) {
	if strings.HasPrefix(replacement, `\=`) {
		return nil, ErrExpression
	}

	var out []byte
	run, one := foldNone, foldNone
	// write appends s with whatever case folding is in force, one CHARACTER at
	// a time and not one byte: "\uünicode" is "Ünicode" and "\Uaüb" is "AÜB",
	// both measured, so the fold has to see a rune and the one-shot has to be
	// spent on the whole of it.
	write := func(s []byte) {
		for i := 0; i < len(s); {
			r, n := utf8.DecodeRune(s[i:])
			if r == utf8.RuneError && n <= 1 {
				// A byte that is not valid UTF-8 passes through as itself.
				// Vim keeps such bytes in the buffer and so does this.
				out = append(out, s[i])
				i++
				one = foldNone
				continue
			}
			switch {
			case one == foldUpperOne:
				r, one = unicode.ToUpper(r), foldNone
			case one == foldLowerOne:
				r, one = unicode.ToLower(r), foldNone
			case run == foldUpperRun:
				r = unicode.ToUpper(r)
			case run == foldLowerRun:
				r = unicode.ToLower(r)
			}
			out = utf8.AppendRune(out, r)
			i += n
		}
	}

	for i := 0; i < len(replacement); i++ {
		c := replacement[i]
		if c == '&' && magic {
			write(group(groups, 0))
			continue
		}
		if c != '\\' || i+1 >= len(replacement) {
			// A whole character at a time, so that a one-shot "\u" before a
			// multi-byte one is spent on the character and not on its first
			// byte.
			_, n := utf8.DecodeRuneInString(replacement[i:])
			if n < 1 {
				n = 1
			}
			write([]byte(replacement[i : i+n]))
			i += n - 1
			continue
		}
		i++
		switch e := replacement[i]; {
		case e == '&':
			if magic {
				write([]byte{'&'})
			} else {
				write(group(groups, 0))
			}
		case e >= '0' && e <= '9':
			write(group(groups, int(e-'0')))
		case e == 'u':
			one = foldUpperOne
		case e == 'l':
			one = foldLowerOne
		case e == 'U':
			run, one = foldUpperRun, foldNone
		case e == 'L':
			run, one = foldLowerRun, foldNone
		case e == 'e', e == 'E':
			run, one = foldNone, foldNone
		case e == 'r':
			// A line break. The buffer splits on it; nothing else does.
			// It goes through write() like every other character because
			// vim's vim_regsub_both pushes it through func_one, which
			// spends a pending "\u" or "\l" on it and leaves a "\U" or
			// "\L" run alone. Upper-casing a newline gives a newline, so
			// the byte comes out untouched and only the one-shot is gone.
			write([]byte{'\n'})
		case e == 'n':
			// A NUL, and not a line break. Vim stores a NUL in a line as its
			// own NL and writes it back out as a NUL, so the file ends up
			// with a zero byte, which is what this is. Through write() for
			// the same reason "\r" is.
			write([]byte{0})
		case e == 't':
			write([]byte{'\t'})
		case e == 'b':
			write([]byte{'\b'})
		default:
			write([]byte{e})
		}
	}
	return out, nil
}

// group returns one subexpression's bytes, or nothing when it did not take
// part in the match or does not exist.
func group(groups [][]byte, n int) []byte {
	if n < 0 || n >= len(groups) {
		return nil
	}
	return groups[n]
}
