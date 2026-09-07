package motion

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// Character classes are the whole of what a word motion knows. Vim's cls()
// answers 0 for a blank, 2 for a keyword character, 1 for anything else
// printable, and for characters above Latin-1 a per-script number so that a run
// of Hiragana and the run of CJK ideographs next to it are two words. w, e, b
// and ge all mean "move until the class changes", and every difference between
// them is which end of the run they stop at.
//
// The classes above 0xff are vim's utf_class() table transcribed. The values
// are arbitrary and only ever compared for equality, which is why vim uses the
// first codepoint of a script as its own class number and this does too.

const (
	// classBlank is a space, a tab, a NUL and the end of a line. Vim counts
	// the position after the last byte of a line as a blank, which is what
	// makes w stop at the end of a line with an operator pending.
	classBlank = 0
	// classPunct is a printable character that is not a keyword character.
	classPunct = 1
	// classWord is a keyword character, per 'iskeyword'.
	classWord = 2
	// classEmoji is what vim gives every emoji, so that a run of them is one
	// word whatever scripts they sit between.
	classEmoji = 3
)

// keywords is a parsed 'iskeyword'. Only characters below 0x100 consult it:
// vim decides everything above from utf_class() and its own script table, and
// an 'iskeyword' of "@,48-57,_,192-255" says nothing about a CJK ideograph.
type keywords struct {
	// tab is one bit per byte value, which is vim's own chartab in miniature.
	tab [256]bool
}

// parseKeywords turns an 'iskeyword' value into a lookup table.
//
// The syntax is vim's: a comma-separated list where an item is a single
// character, a range "a-z", a decimal number, a decimal range "192-255", "@"
// for every alphabetic character in the locale, or any of those with "^" in
// front to remove it again. A literal comma is written as "," on its own and a
// literal ^ or - has to be first, which is why the parse works on the item and
// not on a character stream.
//
// Anything that does not parse is skipped rather than reported. The option
// itself is validated where :set lives; a motion asked to move the cursor is
// not the place to raise E474.
func parseKeywords(opt string) *keywords {
	k := &keywords{}
	if opt == "" {
		opt = "@,48-57,_,192-255"
	}
	for _, item := range splitKeywordList(opt) {
		remove := false
		if len(item) > 1 && item[0] == '^' {
			remove, item = true, item[1:]
		}
		// "@" is every alphabetic character, and a literal @ is written
		// "@-@", which is why this is checked before the range parse and not
		// inside it.
		if item == "@" {
			for c := 0; c < 256; c++ {
				if unicode.IsLetter(rune(c)) {
					k.tab[c] = !remove
				}
			}
			continue
		}
		lo, hi, ok := keywordRange(item)
		if !ok {
			continue
		}
		for c := lo; c <= hi && c < 256; c++ {
			k.tab[c] = !remove
		}
	}

	// Vim never lets these be keyword characters whatever the option says: a
	// NUL ends a line and a newline separates two.
	k.tab[0] = false
	k.tab['\n'] = false
	return k
}

// splitKeywordList splits 'iskeyword' on commas, keeping a literal comma
// item. Vim writes a comma in the list as an item that is a bare comma, so
// "a,b" is a, comma and b, and the split cannot be a plain strings.Split.
func splitKeywordList(opt string) []string {
	var items []string
	for i := 0; i < len(opt); {
		j := i
		for j < len(opt) && opt[j] != ',' {
			j++
		}
		if j == i {
			// A bare comma. It is the item itself when what follows is a
			// separator or the end, and a separator otherwise.
			if j+1 < len(opt) && opt[j+1] == ',' {
				items = append(items, ",")
				i = j + 2
				continue
			}
			if j+1 == len(opt) {
				items = append(items, ",")
			}
			i = j + 1
			continue
		}
		items = append(items, opt[i:j])
		i = j + 1
	}
	return items
}

// keywordRange resolves one item of 'iskeyword' to the byte range it covers.
func keywordRange(item string) (lo, hi int, ok bool) {
	if item == "" {
		return 0, 0, false
	}
	if i := strings.IndexByte(item, '-'); i > 0 && i < len(item)-1 {
		lo, ok1 := keywordPoint(item[:i])
		hi, ok2 := keywordPoint(item[i+1:])
		if ok1 && ok2 && lo <= hi {
			return lo, hi, true
		}
		return 0, 0, false
	}
	c, ok := keywordPoint(item)
	return c, c, ok
}

// keywordPoint resolves one end of an 'iskeyword' item: a decimal number, or a
// single character standing for itself.
func keywordPoint(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, n >= 0 && n < 256
	}
	r, size := utf8.DecodeRuneInString(s)
	if size != len(s) || r == utf8.RuneError {
		return 0, false
	}
	return int(r), true
}

// isWord reports whether c is a keyword character. Only asked about bytes
// below 0x100.
func (k *keywords) isWord(c rune) bool {
	if c < 0 || c > 0xff {
		return false
	}
	return k.tab[c]
}

// class is vim's cls(): the class of the character at a position, with big
// meaning W, B, E and gE, which flatten every non-blank class to one.
func class(r rune, k *keywords, big bool) int {
	if r == ' ' || r == '\t' || r == 0 {
		return classBlank
	}
	c := utfClass(r, k)
	if c != 0 && big {
		return classPunct
	}
	return c
}

// utfClass is vim's utf_class(): the class of one character.
//
// Below 0x100 the answer is 'iskeyword' and this decides it; above, the table
// is in internal/text, because internal/search needs the same answer and the
// two may not import each other. See text.UTFClass.
func utfClass(r rune, k *keywords) int {
	if r < 0x100 {
		if r == ' ' || r == '\t' || r == 0 || r == 0xa0 {
			return classBlank
		}
		if k.isWord(r) {
			return classWord
		}
		return classPunct
	}
	return text.UTFClass(r)
}
