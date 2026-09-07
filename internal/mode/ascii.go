package mode

import (
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// "ga", which vim spells :ascii and documents under *ga*.
//
// It is vim's do_ascii(), and it has more shapes than a one-line command has
// any right to: two spellings of the word "octal", two spacings, a tail that
// depends on a 1300-row table, and a second sentence per composing character.
// Every one of them is here because every one of them was measured, against
// /opt/homebrew/bin/vim 9.2.0321, with
//
//	vim --clean -s keys file
//
// where keys was ":set columns=300 nomore", ":redir! > msgs.txt", "ga" and
// ":redir END". The width matters: vim truncates a message that does not fit
// the screen, and at the default 80 columns a character with two composing
// marks comes back with "..." in the middle of it.
//
// # The two shapes
//
// A character below U+0080 is printed with TWO spaces after the ">" and after
// each comma:
//
//	<a> 97, Hex 61, Octal 141
//	<^I> 9, Hex 09, Oct 011, Digr HT
//
// and a character at or above it with one:
//
//	<é> 233, Hex 00e9, Oct 351, Digr e'
//	<日> 26085, Hex 65e5, Octal 62745
//	<😀> 128512, Hex 0001f600, Octal 373000
//
// The hexadecimal is two digits in the first shape, four in the second and
// eight above U+FFFF. The octal is three digits in the first shape and as many
// as it takes in the second. The word is "Oct" with a digraph after it and
// "Octal" without, which is the one rule that cannot be guessed: it is two
// format strings in vim's source and the difference is visible on any two
// adjacent characters, "a" having no digraph and " " having "SP".
//
// # The end of a line, and NUL
//
// An empty line answers "NUL" and nothing else, because there is no character
// under the cursor to describe. A NUL byte inside a line answers
// "<^@> 0, Hex 00, Octal 000" with no digraph tail, measured, even though
// vim's own table has "NU" for the value 10 that vim stores such a byte as.
//
// :help ga is worth reading here because it says "the <Nul> character in a file
// is stored internally as <NL>" and that sentence has been read as "ga prints
// <NL>". It does not: what it describes is vim's own storage, and vim maps it
// back before printing. This editor's buffer holds the NUL byte itself, so the
// same two characters come out of a shorter path and there is no line break to
// name. A line separator is never under the cursor.
//
// # Composing characters
//
// Every composing character after the base one gets a sentence of its own, in
// the second shape, separated by a space, with the mark drawn on top of a SPACE
// so that it has something to sit on:
//
//	<e> 101, Hex 65, Octal 145 < ́> 769, Hex 0301, Octal 1401
//
// Six of them, and no more: vim's MAX_MCO. Measured with eight marks on one
// "a", where the seventh and eighth are not printed and the message is
// byte-for-byte the six-mark one.
//
// # What is not from vim's source but from its screen
//
// A character vim will not draw is spelled out in the message: U+0080 comes
// back as "<<80>> 128, Hex 0080, Oct 200, Digr PA" and U+200B as
// "<<200b>> 8203, Hex 200b, Octal 20013", both measured. That is msg_outtrans
// running over the message vim built, not do_ascii, and text.Spell is where
// this editor keeps the same classification.

// maxCombine is vim's MAX_MCO: how many composing characters utfc_ptr2char
// collects and therefore how many "ga" describes.
const maxCombine = 6

// showAscii is "ga".
func (e *Editor) showAscii() error {
	e.Say(asciiText(e.buf.Line(e.cur.Line), e.cur.Col))
	return e.finishSimple()
}

// asciiText is the whole message, for a cursor at byte col of line.
func asciiText(line []byte, col int) string {
	if col < 0 || col >= len(line) {
		return "NUL"
	}
	c, size := utf8.DecodeRune(line[col:])
	if c == utf8.RuneError && size == 1 {
		// An invalid byte. vim's utfc_ptr2char hands back the byte itself
		// rather than U+FFFD, and so does this: a file that reached the buffer
		// with a stray 0xff in it should report 255 and not 65533.
		c = rune(line[col])
	}
	marks := composingAfter(line[col+size:])

	var b []byte
	next := 0
	if c < 0x80 {
		b = append(b, '<')
		b = append(b, text.Spell(c)...)
		b = append(b, '>', ' ', ' ')
		b = strconv.AppendInt(b, int64(c), 10)
		b = append(b, ',', ' ', ' ', 'H', 'e', 'x', ' ')
		b = append(b, pad(strconv.FormatInt(int64(c), 16), 2)...)
		b = append(b, ',', ' ', ' ')
		if d := digraphFor(c); d != "" {
			b = append(b, "Oct "...)
			b = append(b, pad(strconv.FormatInt(int64(c), 8), 3)...)
			b = append(b, ", Digr "...)
			b = append(b, d...)
		} else {
			b = append(b, "Octal "...)
			b = append(b, pad(strconv.FormatInt(int64(c), 8), 3)...)
		}
		c, next = nextMark(marks, next)
	}
	for c >= 0x80 {
		if len(b) > 0 {
			b = append(b, ' ')
		}
		b = append(b, '<')
		if text.Composing(c) {
			// A mark drawn over the "<" is unreadable, so vim gives it a space
			// of its own. Measured as a space and not as the left-to-right
			// mark some versions of the source use.
			b = append(b, ' ')
		}
		b = append(b, text.Spell(c)...)
		b = append(b, "> "...)
		b = strconv.AppendInt(b, int64(c), 10)
		b = append(b, ", Hex "...)
		width := 4
		if c > 0xffff {
			width = 8
		}
		b = append(b, pad(strconv.FormatInt(int64(c), 16), width)...)
		if d := digraphFor(c); d != "" {
			b = append(b, ", Oct "...)
			b = strconv.AppendInt(b, int64(c), 8)
			b = append(b, ", Digr "...)
			b = append(b, d...)
		} else {
			b = append(b, ", Octal "...)
			b = strconv.AppendInt(b, int64(c), 8)
		}
		c, next = nextMark(marks, next)
	}
	return string(b)
}

// composingAfter is the run of composing characters at the front of b, up to
// vim's MAX_MCO of them.
func composingAfter(b []byte) []rune {
	var out []rune
	for len(out) < maxCombine && len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n == 1 {
			break
		}
		if !text.Composing(r) {
			break
		}
		out = append(out, r)
		b = b[n:]
	}
	return out
}

// nextMark takes the next composing character, or zero when they are used up,
// which is what stops both loops in asciiText.
func nextMark(marks []rune, i int) (rune, int) {
	if i >= len(marks) {
		return 0, i
	}
	return marks[i], i + 1
}

// pad left-fills s with zeros to at least width characters.
func pad(s string, width int) string {
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// digraphFor is vim's get_digraph_for_char: the two keys CTRL-K spells r with,
// or empty when no digraph makes it. See digraph_table.go for the dump and for
// why the first match in vim's own order is the one that counts.
func digraphFor(r rune) string {
	i := sort.Search(len(digraphTable), func(i int) bool { return digraphTable[i].r >= r })
	if i < len(digraphTable) && digraphTable[i].r == r {
		return digraphTable[i].keys
	}
	return ""
}
