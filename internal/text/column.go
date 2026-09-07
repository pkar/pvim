package text

import "unicode/utf8"

// Display columns are 0-based counts of terminal cells from the left edge of
// the line, before any horizontal scrolling. They are computed on demand from
// the line's bytes and never stored: a stored display column goes stale the
// moment a tab is inserted to the left of it, and vim's own screen code
// recomputes for the same reason.
//
// Vim's virtcol() is the same number 1-based and reported at the LAST cell of a
// multi-cell character, so virtcol == DisplayCol + width. VirtCol below is the
// bridge, and it exists so the oracle comparison is one call and nobody
// re-derives the off-by-one in a hurry.

// DisplayCol returns the display column at which the character containing byte
// col is drawn.
//
// col may be len(line), the position after the last byte, which gives the width
// of the whole line. A col that lands inside a multi-byte character, including
// inside the combining marks that follow a base rune, gives the column of the
// character it lands in, which is what vim's cursor does when it snaps.
func DisplayCol(line []byte, col, tabstop int) int {
	if col <= 0 {
		return 0
	}
	d := 0
	for i := 0; i < len(line); {
		w, n := charAt(line, i, d, tabstop)
		i += n
		if col < i {
			return d
		}
		d += w
	}
	return d
}

// ByteColForDisplay returns the byte column of the character drawn at display
// column disp: vim's coladvance with 'virtualedit' empty.
//
// A disp that lands in the middle of a tab or on the second cell of a wide rune
// gives the byte column that character starts at, because there is nowhere else
// for a cursor to be. A disp past the end of the line gives len(line).
func ByteColForDisplay(line []byte, disp, tabstop int) int {
	if disp <= 0 {
		return 0
	}
	d := 0
	for i := 0; i < len(line); {
		w, n := charAt(line, i, d, tabstop)
		if d+w > disp {
			return i
		}
		d += w
		i += n
	}
	return len(line)
}

// DisplayWidth returns the number of cells the whole line occupies.
func DisplayWidth(line []byte, tabstop int) int {
	return DisplayCol(line, len(line), tabstop)
}

// VirtCol returns what vim's virtcol() returns for a cursor at byte col: the
// 1-based display column of the LAST cell the character occupies. It is here so
// a test can compare against vim without restating that convention.
func VirtCol(line []byte, col, tabstop int) int {
	if col >= len(line) {
		return DisplayWidth(line, tabstop) + 1
	}
	d := 0
	for i := 0; i < len(line); {
		w, n := charAt(line, i, d, tabstop)
		if col < i+n {
			if w < 1 {
				w = 1
			}
			return d + w
		}
		d += w
		i += n
	}
	return d + 1
}

// charAt reports the width in cells and the length in bytes of the character
// starting at line[i], drawn with its first cell at display column d.
//
// "Character" here means a base rune plus every zero-width mark that follows
// it, because that is the unit vim's cursor moves over and the unit that
// occupies cells. d is a parameter because a tab's width is the distance to the
// next tabstop and so depends on where it starts.
func charAt(line []byte, i, d, tabstop int) (width, size int) {
	if line[i] == '\t' {
		if tabstop < 1 {
			tabstop = 1
		}
		width, size = tabstop-d%tabstop, 1
	} else {
		r, n := utf8.DecodeRune(line[i:])
		width, size = runeWidth(r), n
		if width == 0 {
			// A mark with nothing in front of it has nothing to draw over, so
			// vim gives it a cell of its own: utfc_ptr2cells() returns 1 for
			// "only illegal utf-8 or composing char at start". Getting this
			// wrong moves every character after it one cell left, for the whole
			// line, because the accumulator never catches up.
			width = 1
		}
	}
	// Absorb the combining marks that render on top of this character. A mark
	// is part of the character in front of it, so a cursor never lands between
	// the two and neither does a display column. A tab absorbs them too: vim
	// draws "\t" plus a mark in the tab's own cells and charges nothing extra.
	for i+size < len(line) {
		r2, n2 := utf8.DecodeRune(line[i+size:])
		if r2 == utf8.RuneError && n2 == 1 {
			break
		}
		if runeWidth(r2) != 0 {
			break
		}
		size += n2
	}
	return width, size
}

// runeWidth returns the number of cells r occupies, matching vim's
// utf_char2cells() for everything a file is likely to hold.
//
// Three of the four cases are vim's own display conventions rather than
// anything Unicode says, and all three are the same rule: a code point vim will
// not draw is spelled out instead, and the spelling takes cells. A C0 control
// or DEL is shown as ^X, two cells. A C1 control, which utf-8 makes reachable
// as U+0080..U+009F, is shown as <80>, four. The format controls in
// unprintableRanges, which is where a pasted zero-width space or a mid-file
// byte-order mark lands, are shown as <200b>, six. Tabs never reach here; they
// depend on the current column and charAt handles them.
func runeWidth(r rune) int {
	switch {
	case r < 0x20 || r == 0x7f:
		return 2
	case r >= 0x80 && r <= 0x9f:
		return 4
	case r < 0x300:
		// Fast path: everything below the first combining mark is one cell,
		// which is the whole of latin-1 and so almost every line ever edited.
		return 1
	}
	if inRanges(zeroRanges[:], r) {
		return 0
	}
	if inRanges(unprintableRanges[:], r) {
		return 6
	}
	if inRanges(wideRanges[:], r) {
		return 2
	}
	return 1
}

// inRanges binary-searches a sorted, non-overlapping range table.
func inRanges(t []rrange, r rune) bool {
	lo, hi := 0, len(t)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < t[mid].lo:
			hi = mid - 1
		case r > t[mid].hi:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// Composing reports whether r is a mark drawn on top of the character in front
// of it rather than in a cell of its own.
//
// It is the runeWidth == 0 case given a name, because two things outside this
// package have to ask the question and neither of them is about columns: "ga"
// prints the composing characters after the one under the cursor, and vim puts
// a space under one so that the accent has something to sit on. See asciiText
// in internal/mode.
func Composing(r rune) bool {
	return r >= 0x300 && inRanges(zeroRanges[:], r)
}

// Spell returns the characters vim's message line writes for r: the rune
// itself where vim draws it, "^X" for a C0 control, "^?" for DEL, "<80>" for a
// C1 control and "<200b>" for a format control it refuses to draw.
//
// It is transchar() and the msg_outtrans() half of vim's message layer in one
// function, and it belongs beside runeWidth because the two are the same
// classification read two ways: what runeWidth returns for each case above is
// the LENGTH of the string this returns for it, and a change to one that is
// not a change to the other would put a cursor column and the bytes on the
// screen out of step. TestSpellWidthsMatchRuneWidth is that pinned.
func Spell(r rune) string {
	switch {
	case r >= 0 && r < 0x20:
		return "^" + string(r+0x40)
	case r == 0x7f:
		return "^?"
	case r >= 0x80 && r <= 0x9f:
		return "<" + hex(r, 2) + ">"
	case r < 0x300:
		return string(r)
	}
	if inRanges(unprintableRanges[:], r) {
		return "<" + hex(r, 4) + ">"
	}
	return string(r)
}

// hex is r in lower-case hexadecimal, at least width digits wide. strconv would
// do it; this package imports nothing that is not already here, and the whole
// of it is five lines.
func hex(r rune, width int) string {
	const digits = "0123456789abcdef"
	var b []byte
	for v := uint32(r); v != 0; v >>= 4 {
		b = append([]byte{digits[v&0xf]}, b...)
	}
	for len(b) < width {
		b = append([]byte{'0'}, b...)
	}
	return string(b)
}
