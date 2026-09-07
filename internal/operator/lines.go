package operator

import (
	"bytes"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// The helpers in this file are the arithmetic every operator shares: where a
// line's indent ends, how wide it is on screen, how to write a new one, and
// where vim's beginline(BL_WHITE) puts the cursor. They are here rather than in
// internal/text because each one is an editor rule and not a property of the
// buffer: "the first non-blank, or the last character when the line is all
// blank" is what vim does after dd and >>, and text has no business knowing it.

// blank reports whether c is what vim's skipwhite skips: a space or a tab, and
// nothing else. A form feed is not white space to vim and neither is a
// non-breaking space.
func blank(c byte) bool { return c == ' ' || c == '\t' }

// indentEnd returns the byte column just after the line's leading white space,
// which is len(line) for a line that is nothing but blanks.
func indentEnd(line []byte) int {
	i := 0
	for i < len(line) && blank(line[i]) {
		i++
	}
	return i
}

// indentWidth returns the display width of the line's leading white space,
// which is what 'shiftwidth' arithmetic and 'shiftround' both work in. Byte
// counts are the wrong unit the moment a tab is involved and the vimrc's
// ts=2 sw=4 makes that every line.
func indentWidth(line []byte, tabstop int) int {
	return text.DisplayCol(line[:indentEnd(line)], indentEnd(line), tabstop)
}

// whiteOnly reports whether the line is empty or nothing but blanks. vim calls
// this linewhite() and uses it to end a paragraph for gq and to decide that a
// line is not worth shifting.
func whiteOnly(line []byte) bool { return indentEnd(line) == len(line) }

// makeIndent builds white space width cells wide the way vim's set_indent
// does: tabs while a whole tab still fits, then spaces, unless 'expandtab' is
// on and it is spaces all the way.
//
// Measured rather than assumed, because the obvious guess is that
// 'softtabstop' has a say and it does not: with ts=8 sw=4 an indent of 12 comes
// back as one tab and four spaces, and with the vimrc's ts=2 sw=4 an indent of
// 4 comes back as two tabs, both under `>>` in vim 9.2 on this machine.
func makeIndent(width, tabstop int, expandTab bool) []byte {
	if width <= 0 {
		return nil
	}
	if tabstop < 1 {
		tabstop = 8
	}
	out := make([]byte, 0, width)
	if !expandTab {
		for width >= tabstop {
			out = append(out, '\t')
			width -= tabstop
		}
	}
	for range width {
		out = append(out, ' ')
	}
	return out
}

// setIndent returns line with its leading white space replaced by an indent
// width cells wide.
func setIndent(line []byte, width, tabstop int, expandTab bool) []byte {
	rest := line[indentEnd(line):]
	out := makeIndent(width, tabstop, expandTab)
	return append(out, rest...)
}

// beginLine is vim's beginline(BL_WHITE | BL_FIX): the first non-blank, backed
// off to the last character when the whole line is blank, and column zero when
// the line is empty. It is where the cursor lands after dd, >>, cc, = and a
// linewise p, so it is written once.
func beginLine(line []byte) int {
	col := indentEnd(line)
	if col < len(line) {
		return col
	}
	return lastCharCol(line)
}

// lastCharCol returns the byte column the last character of the line starts
// at, and zero for an empty line. It is where normal mode's cursor is allowed
// to sit at the end of a line, so every charwise operator clamps through it.
func lastCharCol(line []byte) int {
	if len(line) == 0 {
		return 0
	}
	_, size := utf8.DecodeLastRune(line)
	return len(line) - size
}

// clampNormal moves p to a position normal mode's cursor may occupy: on the
// line, and on a character rather than past the last one. Insert mode may sit
// one past the end and does not come through here.
func clampNormal(b *text.Buffer, p text.Pos) text.Pos {
	p = b.Clamp(p)
	line := b.Line(p.Line)
	if p.Col > lastCharCol(line) {
		p.Col = lastCharCol(line)
	}
	return p
}

// charLenAt returns the byte length of the character at byte column col and
// how many cells it takes, counting a base rune plus the zero-width marks that
// follow it as one character, which is what text's display arithmetic does and
// what vim's cursor moves over.
//
// It is derived from text.DisplayCol rather than from a width table of its
// own, so a change to what counts as wide lands here for free instead of
// leaving two tables to disagree.
func charLenAt(line []byte, col, tabstop int) (size, width int) {
	if col < 0 || col >= len(line) {
		return 0, 0
	}
	start := text.DisplayCol(line, col, tabstop)
	n := col
	for n < len(line) {
		_, sz := utf8.DecodeRune(line[n:])
		n += sz
		if n >= len(line) || text.DisplayCol(line, n, tabstop) != start {
			break
		}
	}
	return n - col, text.DisplayCol(line, n, tabstop) - start
}

// anyTabStop is the tabstop charSizeAt passes down. Any value gives the same
// answer: a tab is one byte whatever it draws, and whether a rune is
// zero-width does not depend on where it sits on the screen. It is named
// rather than an 8 at three call sites so that nobody has to work that out
// twice.
const anyTabStop = 8

// charSizeAt returns the byte length of the character at byte column col,
// counting the zero-width marks that follow a base rune as part of it.
func charSizeAt(line []byte, col int) int {
	n, _ := charLenAt(line, col, anyTabStop)
	return n
}

// splitLines cuts register text on newlines. A trailing newline leaves a final
// empty line, because that is what a charwise yank ending at column zero holds
// and what p has to put back.
func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return [][]byte{{}}
	}
	return bytes.Split(b, []byte("\n"))
}

// joinLines is splitLines backwards.
func joinLines(lines [][]byte) []byte { return bytes.Join(lines, []byte("\n")) }
