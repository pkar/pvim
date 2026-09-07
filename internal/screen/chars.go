package screen

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

// A buffer line is bytes and the screen is cells, and this file is the one
// place that turns the first into the second.
//
// Vim draws four things that are not the rune in the file. A tab covers the
// cells up to the next 'tabstop'. A C0 control or DEL is spelled "^A", two
// cells. A C1 control, which utf-8 makes reachable as U+0080..U+009F, is
// spelled "<80>", four. A format control -- a zero-width space, a bidi mark, a
// byte-order mark -- is spelled "<200b>", six. Everything else is one rune in
// one or two cells, with any combining marks that follow it drawn on top and
// charging nothing.
//
// internal/text carries the same rules, because a cursor column has to agree
// with a screen column or every motion lands a cell out. The duplication is
// deliberate and it is pinned by TestWidthAgreesWithText, which walks a corpus
// through both and fails the day they disagree. What is not duplicated is the
// spelling: internal/text needs the width of "<200b>" and never the string,
// and this package needs both.

// Char is one character of a buffer line as the screen draws it: where it came
// from, where it goes, and what is actually put in the cells.
type Char struct {
	// Byte is the offset in the line of the first byte of the character, and
	// Size its length in bytes including any combining marks absorbed into it.
	Byte, Size int

	// Col is the display column of the character's first cell, counted from
	// the left edge of the line before any horizontal scrolling, and Width is
	// how many cells it covers.
	Col, Width int

	// Rune is what to draw when Text is empty and Tab is false.
	Rune rune

	// Text is the spelling of a character vim will not draw as itself: "^A",
	// "<80>", "<200b>". Empty for everything else.
	Text string

	// Tab reports that the character is a tab, whose cells are blank (or
	// 'listchars' when 'list' is on) and whose width depends on where it
	// started.
	Tab bool
}

// walker steps through a line's characters, accumulating display columns.
type walker struct {
	line []byte
	ts   int
	i    int
	col  int
}

// newWalker returns a walker over line at the given 'tabstop'.
func newWalker(line []byte, tabstop int) *walker {
	if tabstop < 1 {
		tabstop = 1
	}
	return &walker{line: line, ts: tabstop}
}

// next returns the character at the walker's position and advances it. The
// second result is false at the end of the line.
func (w *walker) next() (Char, bool) {
	if w.i >= len(w.line) {
		return Char{}, false
	}
	c := Char{Byte: w.i, Col: w.col}

	switch b := w.line[w.i]; {
	case b == '\t':
		c.Tab = true
		c.Rune = '\t'
		c.Width = w.ts - w.col%w.ts
		c.Size = 1
	default:
		r, n := utf8.DecodeRune(w.line[w.i:])
		if r == utf8.RuneError && n == 1 {
			// An invalid byte is spelled as its hex value, which is what vim
			// does with a latin-1 file opened as utf-8.
			c.Text = fmt.Sprintf("<%02x>", b)
			c.Width, c.Size = 4, 1
			break
		}
		c.Rune, c.Size = r, n
		switch {
		case r < 0x20 || r == 0x7f:
			c.Text = "^" + string(rune(r^0x40))
			c.Width = 2
		case r >= 0x80 && r <= 0x9f:
			c.Text = fmt.Sprintf("<%02x>", r)
			c.Width = 4
		case unprintable(r):
			c.Text = fmt.Sprintf("<%04x>", r)
			c.Width = 6
		default:
			c.Width = RuneWidth(r)
			if c.Width == 0 {
				// A combining mark with nothing in front of it has nothing to
				// draw over, so vim gives it a cell of its own. Getting this
				// wrong moves the rest of the line one cell left.
				c.Width = 1
			}
		}
	}

	// Absorb the combining marks that render on top of this character. They
	// are part of it: a cursor never lands between the two and neither does a
	// display column. A Cell holds one rune, so the marks are dropped from the
	// picture and kept in the byte count.
	for w.i+c.Size < len(w.line) {
		r2, n2 := utf8.DecodeRune(w.line[w.i+c.Size:])
		if r2 == utf8.RuneError && n2 == 1 {
			break
		}
		if RuneWidth(r2) != 0 {
			break
		}
		c.Size += n2
	}

	w.i += c.Size
	w.col += c.Width
	return c, true
}

// Chars returns every character of line with its display column, which is what
// the window renderer walks and what the width cross-check test measures.
func Chars(line []byte, tabstop int) []Char {
	w := newWalker(line, tabstop)
	var out []Char
	for {
		c, ok := w.next()
		if !ok {
			return out
		}
		out = append(out, c)
	}
}

// LineWidth is the number of cells the whole line occupies.
func LineWidth(line []byte, tabstop int) int {
	w := newWalker(line, tabstop)
	for {
		if _, ok := w.next(); !ok {
			return w.col
		}
	}
}

// unprintableRanges holds the code points vim refuses to draw and spells as
// <200b> instead: the bidi and format controls, the zero-width space, the
// byte-order mark and the non-characters at the end of the BMP. It is the same
// table internal/text carries under the same name, from the same sweep of vim
// 9.2.0321's strdisplaywidth.
var unprintableRanges = []interval{
	{0x070F, 0x070F}, {0x180E, 0x180E}, {0x200B, 0x200F}, {0x202A, 0x202E},
	{0x2060, 0x206F}, {0xFEFF, 0xFEFF}, {0xFFF9, 0xFFFB}, {0xFFFE, 0xFFFF},
}

// unprintable reports whether r is spelled out rather than drawn.
func unprintable(r rune) bool {
	i := sort.Search(len(unprintableRanges), func(i int) bool { return unprintableRanges[i].hi >= r })
	return i < len(unprintableRanges) && unprintableRanges[i].lo <= r
}
