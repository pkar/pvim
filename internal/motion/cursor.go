package motion

import (
	"unicode"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// The primitives every motion in this package is built from, and they are
// vim's own with vim's return values rather than something tidier.
//
// inc and dec return 0 for a move inside the line, 1 for a move onto another
// line, 2 for a move onto the position after the last byte, and -1 for a move
// that could not happen because the buffer ended. Half the word motions read
// those numbers and change what they do; a bool would lose exactly the
// distinction that makes "dw" stop at the end of a line.
//
// "The position after the last byte" is a real position here, the same as
// vim's cursor sitting on the NUL that terminates a line in its buffer. Normal
// mode never leaves the cursor there, but every motion walks over it.

// gchar returns the character at p, or 0 for the position after the last byte
// of a line, which is vim's NUL and what its own gchar_cursor() returns.
func gchar(b *text.Buffer, p text.Pos) rune {
	line := b.Line(p.Line)
	if p.Col < 0 || p.Col >= len(line) {
		return 0
	}
	r, _ := utf8.DecodeRune(line[p.Col:])
	return r
}

// charLen is the byte length of the character at line[i], a base character
// plus every combining mark that follows it. That is one cursor step: vim's
// mb_ptr2len is utfc_ptr2len under utf-8 and moves over the whole sequence.
func charLen(line []byte, i int) int {
	if i >= len(line) {
		return 0
	}
	_, n := utf8.DecodeRune(line[i:])
	for i+n < len(line) {
		r, size := utf8.DecodeRune(line[i+n:])
		if !isComposing(r) {
			break
		}
		n += size
	}
	return n
}

// charStart backs col up to the first byte of the character it lands in,
// combining marks included, which is vim's mb_head_off.
func charStart(line []byte, col int) int {
	if col <= 0 {
		return 0
	}
	if col > len(line) {
		col = len(line)
	}
	i := 0
	for i < len(line) {
		n := charLen(line, i)
		if col < i+n {
			return i
		}
		i += n
	}
	return i
}

// prevChar returns the byte column of the character before col.
func prevChar(line []byte, col int) int {
	if col <= 0 {
		return 0
	}
	return charStart(line, col-1)
}

// isComposing reports whether a rune combines with the character before it and
// so occupies no cursor position of its own.
func isComposing(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me)
}

// inc moves p one character forward, exactly as vim's inc().
func inc(b *text.Buffer, p *text.Pos) int {
	line := b.Line(p.Line)
	if p.Col < len(line) {
		p.Col += charLen(line, p.Col)
		if p.Col < len(line) {
			return 0
		}
		return 2
	}
	if p.Line < b.LineCount() {
		p.Line++
		p.Col = 0
		return 1
	}
	return -1
}

// dec moves p one character backward, exactly as vim's dec().
func dec(b *text.Buffer, p *text.Pos) int {
	if p.Col > 0 {
		p.Col = prevChar(b.Line(p.Line), p.Col)
		return 0
	}
	if p.Line > 1 {
		p.Line--
		p.Col = len(b.Line(p.Line))
		return 1
	}
	return -1
}

// incl is inc that does not stop on the position after the last byte of a
// non-empty line: vim's incl(), which the sentence motions use so that they
// step from a line's last character straight onto the next line.
func incl(b *text.Buffer, p *text.Pos) int {
	r := inc(b, p)
	if r >= 1 && p.Col != 0 {
		r = inc(b, p)
	}
	return r
}

// decl is the same going backwards: vim's decl().
func decl(b *text.Buffer, p *text.Pos) int {
	r := dec(b, p)
	if r == 1 && p.Col != 0 {
		r = dec(b, p)
	}
	return r
}

// lineEmpty reports whether a line has no bytes at all, which is a paragraph
// boundary, a word of its own, and the one place a word motion stops on a
// blank.
func lineEmpty(b *text.Buffer, lnum int) bool {
	return len(b.Line(lnum)) == 0
}

// firstNonBlank is the byte column of the first character that is not a space
// or a tab, or the last character of a line that is all blanks, which is what
// vim's beginline(BL_WHITE|BL_FIX) leaves the cursor on.
func firstNonBlank(line []byte) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i >= len(line) && len(line) > 0 {
		return prevChar(line, len(line))
	}
	return i
}

// lastNonBlank is the byte column of the last character that is not a space or
// a tab, which is where g_ lands.
func lastNonBlank(line []byte) int {
	i := len(line)
	for i > 0 {
		j := prevChar(line, i)
		if line[j] != ' ' && line[j] != '\t' {
			return j
		}
		i = j
	}
	return 0
}

// lastChar is the byte column of the last character of a line, or zero for an
// empty one: where normal mode's cursor sits after $.
func lastChar(line []byte) int {
	if len(line) == 0 {
		return 0
	}
	return prevChar(line, len(line))
}

// coladvance is vim's coladvance(): the byte column of the character drawn at
// display column want, clamped so that normal mode never lands on the position
// after the last byte. want may be CurswantEOL, which means the end of the
// line however long it is.
func coladvance(line []byte, want, tabstop int) int {
	if want == CurswantEOL {
		return lastChar(line)
	}
	col := text.ByteColForDisplay(line, want, tabstop)
	if col >= len(line) && len(line) > 0 {
		col = prevChar(line, len(line))
	}
	return col
}

// dispCol is the display column vim calls w_virtcol: the cell the cursor is
// drawn in, and the column a later j or k aims for.
//
// It is not simply the first cell of the character. A cursor on a tab in
// normal mode is drawn at the tab's LAST cell -- that is why the cursor sits
// at the right-hand end of an indent -- and vim's getvcol() says so in the one
// branch that separates the two. Everything else, wide runes included, is
// drawn at its first cell. Getting this wrong moves the cursor by seven
// columns on every j out of an indented line and nowhere else, which is a very
// long afternoon.
func dispCol(b *text.Buffer, p text.Pos, tabstop int) int {
	line := b.Line(p.Line)
	d := text.DisplayCol(line, p.Col, tabstop)
	if p.Col < len(line) && line[p.Col] == '\t' {
		if tabstop < 1 {
			tabstop = 1
		}
		d += tabstop - d%tabstop - 1
	}
	return d
}
