package regex

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// collection translates a [...] and writes it. width is 1 for a bare bracket
// and 2 for `\[`, so t.i is on the start of the token either way.
//
// eol is the `\_[` form, which also matches a line break.
func (t *translator) collection(width int, eol bool) error {
	if !collectionCloses(t.pat, t.i+width) {
		// Vim does not call an unclosed collection an error: it decides there
		// was no collection and the bracket was a literal. That is what makes
		// /[ a working search for an open bracket, and every second regex in a
		// vimrc relies on it without knowing.
		//
		// The question is asked before a single member is read, exactly as vim
		// asks it, because a reversed range or an equivalence class inside a
		// bracket that never closes is not an error either: there is no
		// collection for it to be wrong in. With 'incsearch' on every prefix of
		// what is typed gets compiled, so deciding it the other way round means
		// /[z-a] raises an error on the fifth keystroke.
		t.i += width
		t.emitAtom(`\[`)
		return nil
	}

	body, neg, end, ok, err := t.scanCollection(t.i + width)
	if err != nil {
		return err
	}
	if !ok {
		// The two scanners can still part company, because collectionCloses
		// carries vim's own reading of a `-` in front of a `[:` and this one
		// does not. See the package doc.
		t.i += width
		t.emitAtom(`\[`)
		return nil
	}

	var b strings.Builder
	b.WriteString("[")
	if neg {
		b.WriteString("^")
	}
	b.WriteString(body)
	if neg != eol {
		// A collection matches a line break only when it was asked to. Adding
		// the newline to a negated set is what keeps [^ab] from swallowing the
		// rest of the buffer; adding it to a positive set is what \_[ab] means.
		b.WriteString(`\n`)
	}
	b.WriteString("]")

	t.emitAtom(b.String())
	t.i = end
	return nil
}

// scanCollection reads the members of a collection starting at j, which is the
// first byte after the opening bracket.
//
// ok is false when there is no closing bracket, which is not an error.
func (t *translator) scanCollection(j int) (body string, neg bool, end int, ok bool, err error) {
	pat := t.pat
	if j < len(pat) && pat[j] == '^' {
		neg = true
		j++
	}

	var b strings.Builder
	// A ']' as the very first member is a literal ']', which is how vim spells
	// a collection containing one without an escape.
	first := true
	for {
		if j >= len(pat) {
			return "", false, 0, false, nil
		}
		if pat[j] == ']' && !first {
			return b.String(), neg, j + 1, true, nil
		}
		first = false

		r, isChar, src, next, err := t.collMember(j)
		if err != nil {
			return "", false, 0, false, err
		}
		j = next
		if !isChar {
			b.WriteString(src)
			continue
		}

		// A range, but only when the '-' has something after it: [a-] is an 'a'
		// and a dash, and [a-c-e] is the range a-c and then a literal dash and
		// an 'e', which is why the dash is consumed here and not by the loop.
		if j+1 < len(pat) && pat[j] == '-' && pat[j+1] != ']' {
			hi, hiIsChar, _, hiNext, err := t.collMember(j + 1)
			if err != nil {
				return "", false, 0, false, err
			}
			if hiIsChar {
				if hi < r {
					return "", false, 0, false, SyntaxError{Pattern: pat, Col: j, Msg: "reversed range in collection"}
				}
				b.WriteString(classQuote(r) + "-" + classQuote(hi))
				j = hiNext
				continue
			}
		}
		b.WriteString(classQuote(r))
	}
}

// collectionCloses reports whether the collection whose first member is at j
// has a closing bracket.
//
// This is vim's skip_anyof, which runs ahead of the collection parser for
// exactly this question and for no other reason. It has to walk the members
// rather than look for the next `]`, because a `]` first in the collection,
// after a backslash or inside a [:class:] is a member and not the end.
func collectionCloses(pat string, j int) bool {
	if j < len(pat) && pat[j] == '^' {
		j++
	}
	if j < len(pat) && (pat[j] == ']' || pat[j] == '-') {
		j++
	}
	for j < len(pat) {
		switch {
		case pat[j] == ']':
			return true
		case pat[j] == '-':
			// Whatever stands after a range dash is a member, even a `[` that
			// would otherwise open a class. This is the vim reading the package
			// doc records as a parser bug and does not reproduce in the scan
			// itself; it is copied here because the only job of this function
			// is to answer the way vim answers.
			j++
			if j < len(pat) && pat[j] != ']' {
				_, size := utf8.DecodeRuneInString(pat[j:])
				j += size
			}
		case pat[j] == '\\' && j+1 < len(pat) && strings.ContainsRune(collBackslash, rune(pat[j+1])):
			j += 2
		case pat[j] == '[':
			if _, _, end, ok := collBracket(pat, j); ok {
				j = end
			} else {
				j++
			}
		default:
			_, size := utf8.DecodeRuneInString(pat[j:])
			j += size
		}
	}
	return false
}

// collBracket reads a [:name:], [=x=] or [.x.] form inside a collection, with j
// on the opening bracket. kind is the character that spells the form, inner is
// the text between the markers, and end is the byte after the closing bracket.
//
// ok is false when what stands at j is none of the three or names a class vim
// does not have, and that is not an error: vim reads the bracket as an ordinary
// member and carries on, so /[[:bogus:] is the collection {[,:,b,o,g,u,s} and
// it matches. Both the scanner and collectionCloses ask this one function, so
// that the two cannot disagree about where a collection ends.
func collBracket(pat string, j int) (kind byte, inner string, end int, ok bool) {
	if j >= len(pat) || pat[j] != '[' || j+1 >= len(pat) {
		return 0, "", 0, false
	}
	switch k := pat[j+1]; k {
	case ':':
		n := strings.Index(pat[j+2:], ":]")
		if n < 0 {
			return 0, "", 0, false
		}
		name := pat[j+2 : j+2+n]
		if _, known := posixClasses[name]; !known {
			return 0, "", 0, false
		}
		return ':', name, j + 2 + n + 2, true
	case '=', '.':
		// Vim takes exactly one character between the markers and nothing else
		// is the form at all.
		r, size := utf8.DecodeRuneInString(pat[j+2:])
		e := j + 2 + size
		if size == 0 || r == utf8.RuneError && size == 1 || e+1 >= len(pat) {
			return 0, "", 0, false
		}
		if pat[e] != k || pat[e+1] != ']' {
			return 0, "", 0, false
		}
		return k, pat[j+2 : e], e + 2, true
	}
	return 0, "", 0, false
}

// collMember reads one member of a collection at j.
//
// isChar says the member is a single character, which is the only kind that can
// be the start of a range; src carries the Go source for the kinds that are
// not, which is the [:name:] classes.
func (t *translator) collMember(j int) (r rune, isChar bool, src string, next int, err error) {
	pat := t.pat

	if kind, inner, end, ok := collBracket(pat, j); ok {
		switch kind {
		case ':':
			return 0, false, posixClasses[inner], end, nil
		case '=':
			// An equivalence class needs the table of what counts as the same
			// letter with the accents off, and pvim has no such table.
			return 0, false, "", end, UnsupportedError{Refusal{Atom: pat[j:end], Col: j}}
		default:
			// A collating element, which vim only ever fills with one
			// character, so it is that character.
			c, _ := utf8.DecodeRuneInString(inner)
			return c, true, "", end, nil
		}
	}

	if pat[j] == '\\' && j+1 < len(pat) {
		return t.collEscape(j)
	}

	c, size := utf8.DecodeRuneInString(pat[j:])
	return c, true, "", j + size, nil
}

// collBackslash is the set of characters vim gives a meaning to after a
// backslash inside a collection. A backslash in front of anything else is a
// literal backslash and the character after it is an ordinary member, which is
// why "[\xyz]" matches a backslash, an x, a y and a z.
const collBackslash = `^]-\bdertnoUux`

// collEscape reads a backslash escape inside a collection.
func (t *translator) collEscape(j int) (r rune, isChar bool, src string, next int, err error) {
	pat := t.pat
	c := pat[j+1]
	if !strings.ContainsRune(collBackslash, rune(c)) {
		return '\\', true, "", j + 1, nil
	}
	switch c {
	case '^', ']', '-', '\\':
		return rune(c), true, "", j + 2, nil
	case 'e':
		return 0x1b, true, "", j + 2, nil
	case 't':
		return '\t', true, "", j + 2, nil
	case 'r':
		return '\r', true, "", j + 2, nil
	case 'b':
		return '\b', true, "", j + 2, nil
	case 'n':
		return '\n', true, "", j + 2, nil
	case 'd':
		return collNumber(pat, j+2, 10, 3)
	case 'o':
		return collOctal(pat, j+2)
	case 'x':
		return collNumber(pat, j+2, 16, 2)
	case 'u':
		return collNumber(pat, j+2, 16, 4)
	case 'U':
		return collNumber(pat, j+2, 16, 8)
	}
	return rune(c), true, "", j + 2, nil
}

// collNumber reads the digits of one of the numeric escapes inside a
// collection and gives the character they name.
//
// With no digits after it the escape is not an error: vim takes the backslash
// as a literal member and carries on from the letter, so "[\xyz]" is a
// backslash, an x, a y and a z. j-1 is the letter and j-2 the backslash.
func collNumber(pat string, j, base, maxDigits int) (r rune, isChar bool, src string, next int, err error) {
	k := j
	for k < len(pat) && k-j < maxDigits && digitValue(pat[k]) < base {
		k++
	}
	if k == j {
		return '\\', true, "", j - 1, nil
	}
	n, convErr := strconv.ParseInt(pat[j:k], base, 64)
	if convErr != nil || n < 0 || n > 0x10ffff {
		return 0, false, "", 0, SyntaxError{Pattern: pat, Col: j, Msg: "numeric escape names no character"}
	}
	return rune(n), true, "", k, nil
}

// collOctal reads \o40 inside a collection, with the same three-digit,
// stop-at-0o40 rule vim uses outside one.
func collOctal(pat string, j int) (r rune, isChar bool, src string, next int, err error) {
	nr := 0
	k := j
	for k < len(pat) && k-j < 3 && nr < 0o40 && pat[k] >= '0' && pat[k] <= '7' {
		nr = nr<<3 | int(pat[k]-'0')
		k++
	}
	if k == j {
		return '\\', true, "", j - 1, nil
	}
	return rune(nr), true, "", k, nil
}

// classMeta is every character Go's parser reads as syntax inside a character
// class.
const classMeta = `\]^-[`

// classQuote writes r as Go source that matches exactly r inside a character
// class.
func classQuote(r rune) string {
	if r < utf8.RuneSelf && strings.ContainsRune(classMeta, r) {
		return `\` + string(r)
	}
	switch r {
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	}
	if r < 0x20 || r == 0x7f {
		return `\x{` + strconv.FormatInt(int64(r), 16) + `}`
	}
	return string(r)
}
