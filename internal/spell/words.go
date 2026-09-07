package spell

import (
	"unicode"
	"unicode/utf8"
)

// Splitting a line into the words a spell checker looks at.
//
// Vim takes the word characters out of the .spl file it loaded; with no .spl
// file the rule here is Unicode's: a word is a run of letters, with an
// apostrophe allowed inside it so that "don't" and "editor's" are one word and
// not three.
//
// Two runs are skipped rather than checked, and both matter more than they
// look:
//
// - a run touching a digit or an underscore. "utf8", "x2", "spell_add" and
// "sha256" are identifiers and version numbers, and a checker that
// underlined them would make a spell-checked README unreadable, which is
// the file this option is turned on for.
// - a run inside a longer run of non-space that holds a "/" or a ".", which
// is a path or a host name: "~/.cache/vim/spell.add" is one thing a person
// wrote on purpose and not five misspellings.

// Word is one word on a line, with the byte range it covers.
//
// Start and End are byte columns into the line, 0-based and End exclusive,
// which is what a highlight over the line wants.
type Word struct {
	Text       string
	Start, End int
}

// Words returns the words on one line, in order.
func Words(line []byte) []Word {
	var out []Word
	for i := 0; i < len(line); {
		r, n := utf8.DecodeRune(line[i:])
		if !isWordRune(r) {
			i += n
			continue
		}
		start := i
		skip := false
		for i < len(line) {
			r, n := utf8.DecodeRune(line[i:])
			switch {
			case isWordRune(r):
				i += n
			case r == '\'' || r == '’':
				// An apostrophe is part of the word only between letters:
				// a quoted 'word' keeps its quotes out of it.
				next, _ := utf8.DecodeRune(line[i+n:])
				if !isWordRune(next) {
					goto done
				}
				i += n
			case unicode.IsDigit(r), r == '_':
				skip = true
				i += n
			case r == '/', r == '.', r == '@', r == '\\':
				// Only inside a run of non-space: "spell.add" is skipped and
				// the "end." of a sentence is not.
				next, _ := utf8.DecodeRune(line[i+n:])
				if !isWordRune(next) && !unicode.IsDigit(next) {
					goto done
				}
				skip = true
				i += n
			default:
				goto done
			}
		}
	done:
		if skip {
			continue
		}
		// A digit or an underscore immediately before the word makes it part
		// of an identifier too: the "bar" of "foo_bar" is reached with the
		// underscore behind it.
		if start > 0 {
			prev, _ := utf8.DecodeLastRune(line[:start])
			if unicode.IsDigit(prev) || prev == '_' {
				continue
			}
		}
		out = append(out, Word{Text: string(line[start:i]), Start: start, End: i})
	}
	return out
}

// isWordRune reports whether a rune can be part of a word.
func isWordRune(r rune) bool { return unicode.IsLetter(r) }

// BadWords returns the words on the line the checker does not know, which is
// what the undercurl is drawn over.
func (c *Checker) BadWords(line []byte) []Word {
	var out []Word
	for _, w := range Words(line) {
		if c.Bad(w.Text) {
			out = append(out, w)
		}
	}
	return out
}
