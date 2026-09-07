package tags

import (
	"strconv"
	"strings"
)

// The identifier under the cursor, which is where CTRL-] and the five CTRL-W
// tag keys all start.
//
// This is vim's find_ident_under_cursor with FIND_IDENT and nothing else: the
// keyword the cursor is on, or the first one after it on the same line, and
// "E349: No identifier under cursor" when the line holds neither. The second
// pass vim's FIND_STRING adds -- any run of non-blanks, which is what makes
// "*" on a line of punctuation search for that punctuation -- is deliberately
// not here: CTRL-] on a line of punctuation is E349 in vim, measured, and a
// tag search for "***" would be a wrong answer rather than a missing one.
//
// The 'iskeyword' reader below is the fourth in this tree, beside the ones in
// internal/mode, internal/motion and internal/search. It is here rather than
// borrowed because all three of those are unexported and belong to packages
// this one does not import; the day one of them is exported this is the
// caller to point at it.

// MsgNoIdent is what a line with no keyword on it answers.
const MsgNoIdent = "E349: No identifier under cursor"

// Ident returns the keyword under or after col in line, with the column it
// starts in.
//
// col is a byte offset, 0-based, which is what a cursor carries. iskeyword is
// the option's value; empty means vim's default.
func Ident(line []byte, col int, iskeyword string) (word string, at int, ok bool) {
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	// Forward along the line for the first keyword byte. Vim does not scan
	// backwards past a non-keyword byte and never leaves the line.
	for col < len(line) && !IsKeywordByte(line[col], iskeyword) {
		col++
	}
	if col >= len(line) {
		return "", 0, false
	}
	start := col
	for start > 0 && IsKeywordByte(line[start-1], iskeyword) {
		start--
	}
	end := col
	for end < len(line) && IsKeywordByte(line[end], iskeyword) {
		end++
	}
	return string(line[start:end]), start, true
}

// IsKeywordByte reports whether a byte is a keyword character under
// 'iskeyword'.
//
// The option is a comma-separated list of single characters, decimal codes,
// ranges of either, "@" for the letters isalpha() accepts, and any of those
// with a "^" in front to take it out again. It is read a byte at a time, which
// is what the default "@,48-57,_,192-255" describes: every byte from 192 up is
// a keyword character, which is how the default makes UTF-8 continuation bytes
// part of a word without knowing what UTF-8 is.
func IsKeywordByte(c byte, iskeyword string) bool {
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
