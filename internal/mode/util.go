package mode

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/text"
)

// The byte and rune arithmetic every mode needs, in one file so that "the
// column of the character after this one" has one implementation.
//
// Columns are byte offsets, as text.Pos says, and a character is a rune, so
// stepping the cursor is a UTF-8 decode and never a ++.

// nextRune returns the byte column after the character at col.
func nextRune(line []byte, col int) int {
	if col >= len(line) {
		return len(line)
	}
	_, n := utf8.DecodeRune(line[col:])
	if n <= 0 {
		n = 1
	}
	return col + n
}

// prevRune returns the byte column of the character before col.
func prevRune(line []byte, col int) int {
	if col <= 0 {
		return 0
	}
	if col > len(line) {
		col = len(line)
	}
	_, n := utf8.DecodeLastRune(line[:col])
	if n <= 0 {
		n = 1
	}
	return col - n
}

// runeLen is the length in bytes of the character at the front of b.
func runeLen(b []byte) int {
	_, n := utf8.DecodeRune(b)
	if n <= 0 {
		return 1
	}
	return n
}

// runeBytes is the UTF-8 of a key that carries a character.
func runeBytes(k key.Key) []byte {
	if !k.IsRune() {
		return key.Bytes([]key.Key{k})
	}
	return []byte(string(k.Rune))
}

// firstNonBlank is the byte column of the first character that is not a space
// or a tab, or the length of the line when there is none.
func firstNonBlank(line []byte) int {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return i
		}
	}
	return len(line)
}

// isSpaceByte reports whether a byte is one of the two characters vim counts
// as white space inside a line.
func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' }

// isKeywordByte reports whether a byte is a keyword character under
// 'iskeyword'.
//
// The option's syntax is a comma-separated list of single characters, decimal
// codes, ranges of either, "@" for the letters isalpha() accepts, and any of
// those with a "^" in front to take it out again. This reads it byte at a
// time, which is what the default "@,48-57,_,192-255" describes: every byte
// from 192 up is a keyword character, which is how the default makes UTF-8
// continuation bytes part of a word without knowing what UTF-8 is.
func isKeywordByte(c byte, iskeyword string) bool {
	if iskeyword == "" {
		iskeyword = "@,48-57,_,192-255"
	}
	in := false
	for _, item := range splitIsKeyword(iskeyword) {
		neg := strings.HasPrefix(item, "^") && len(item) > 1
		if neg {
			item = item[1:]
		}
		if matchesIsKeyword(c, item) {
			in = !neg
		}
	}
	return in
}

// splitIsKeyword splits the option on commas, keeping a literal comma written
// as an empty field between two of them.
func splitIsKeyword(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		if parts[i] == "" && i+1 < len(parts) && parts[i+1] == "" {
			out = append(out, ",")
			i++
			continue
		}
		if parts[i] == "" {
			continue
		}
		out = append(out, parts[i])
	}
	return out
}

// matchesIsKeyword reports whether one item of 'iskeyword' covers a byte.
func matchesIsKeyword(c byte, item string) bool {
	if item == "@" {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0xc0
	}
	if i := strings.IndexByte(item, '-'); i > 0 && i < len(item)-1 {
		lo, okLo := isKeywordCode(item[:i])
		hi, okHi := isKeywordCode(item[i+1:])
		return okLo && okHi && int(c) >= lo && int(c) <= hi
	}
	code, ok := isKeywordCode(item)
	return ok && int(c) == code
}

// isKeywordCode reads one end of an 'iskeyword' item: a decimal number or a
// single character.
func isKeywordCode(s string) (int, bool) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, true
	}
	if len(s) == 1 {
		return int(s[0]), true
	}
	return 0, false
}

// toggleCase swaps the case of every letter in b.
//
// ASCII only, and deliberately: vim's ~ folds case for every alphabet it has a
// table for, and a Go implementation over unicode.ToUpper would disagree with
// it in both directions on the letters where the two tables differ. Turkish
// dotless i and the German sharp s are the classic pair, and neither turns up
// in a Go source file or a terraform file. The day one does, this is where the
// difference gets registered.
func toggleCase(b []byte) []byte {
	out := append([]byte(nil), b...)
	for i, c := range out {
		switch {
		case c >= 'a' && c <= 'z':
			out[i] = c - 32
		case c >= 'A' && c <= 'Z':
			out[i] = c + 32
		}
	}
	return out
}

// fillRunes replaces every character between two byte columns with one
// character, which is what r does to a selection.
func fillRunes(line []byte, from, to int, rep []byte) []byte {
	if from < 0 {
		from = 0
	}
	if to > len(line) {
		to = len(line)
	}
	out := append([]byte(nil), line[:from]...)
	for i := from; i < to; i = nextRune(line, i) {
		out = append(out, rep...)
	}
	return append(out, line[to:]...)
}

// normalPos is a position with normal mode's column rule applied: the cursor
// sits on a character and not on the NUL past the end of the line. vim does it
// in adjust_cursor() after every command, and the one place here that needs it
// is a text object that failed with the cursor left past the last character.
func normalPos(b *text.Buffer, p text.Pos) text.Pos {
	p = b.Clamp(p)
	if line := b.Line(p.Line); p.Col >= len(line) && len(line) > 0 {
		p.Col = prevRune(line, len(line))
	}
	return p
}
