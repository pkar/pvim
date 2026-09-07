package operator

import (
	"bytes"

	"github.com/pkar/pvim/internal/text"
)

// A blockwise operator works in display columns, so every line it touches has
// to be cut at a column that may fall in the middle of a tab or a wide rune.
// vim's block_prep() answers that with six fields and four operator-dependent
// branches; the four functions here are the same answers named after what asks
// for them, because "startspaces means something different when is_del is set"
// is exactly the kind of flag soup that hides a bug for a year.
//
// The three rules that are not guesses, measured over
// "abcdefgh / ab / ABCDEFGH / xy / zzzzzzzz" with the block CTRL-V from column
// 3 to column 5:
//
// - A yank pads a line that ends before the block starts to the block's full
// width in spaces, and yanks the empty string from a line that ends exactly
// where the block starts. "a" gives " " and "ab" gives "".
// - A delete leaves such a line alone entirely.
// - A blockwise I skips a line that ends before the block starts and inserts
// at the end of one that ends exactly there; a blockwise A pads every short
// line out to the block's right edge with spaces first.

// charAtDisp reports the character drawn at display column d: the byte column
// it starts at, its length in bytes, the display column it starts at and its
// width in cells. A d past the end of the line gives len(line) and zeroes.
func charAtDisp(line []byte, d, tabstop int) (col, size, disp, width int) {
	col = text.ByteColForDisplay(line, d, tabstop)
	if col >= len(line) {
		return len(line), 0, text.DisplayWidth(line, tabstop), 0
	}
	disp = text.DisplayCol(line, col, tabstop)
	size, width = charLenAt(line, col, tabstop)
	return col, size, disp, width
}

// blockRight resolves a block's right edge on one line, which for a $ block is
// the line's own last column and for every other block is Right.
func blockRight(line []byte, blk Block, tabstop int) int {
	if !blk.ToEOL {
		return blk.Right
	}
	w := text.DisplayWidth(line, tabstop)
	if w == 0 {
		return blk.Left
	}
	return w - 1
}

// blockYankLine returns what a blockwise yank takes from one line: the text
// inside the block, with spaces standing in for the cells of a straddled tab
// that fall inside it, and for the whole width of a line the block overshoots.
func blockYankLine(line []byte, blk Block, tabstop int) []byte {
	left, right := blk.Left, blockRight(line, blk, tabstop)
	width := text.DisplayWidth(line, tabstop)

	if width < left {
		// The line stops short of the block. vim pads it out to the block's
		// full width, which is why "block of N lines yanked" of a ragged
		// selection puts back a rectangle.
		if blk.ToEOL {
			return nil
		}
		return bytes.Repeat([]byte{' '}, right-left+1)
	}
	if width == left {
		return nil
	}

	startCol, startSize, startDisp, startWidth := charAtDisp(line, left, tabstop)
	if startDisp < left && startDisp+startWidth > right {
		// vim's is_oneChar: both edges fall inside the same character, a wide
		// tab under a narrow block. The whole block is that character's cells,
		// so the yank is spaces and nothing else.
		//
		// Not measured, and it cannot be: a visual block widens to whole
		// characters, so vim's own UI never produces this geometry and there
		// was nothing to run. It is here because :normal and a scripted
		// operator can still ask for it and a doubled-up count of the padding
		// would be silently wrong.
		return bytes.Repeat([]byte{' '}, right-left+1)
	}
	var head []byte
	if startDisp < left {
		// A tab crosses the left edge: the cells of it inside the block become
		// spaces and the tab itself is left behind.
		head = bytes.Repeat([]byte{' '}, startDisp+startWidth-left)
		startCol += startSize
	}
	if blk.ToEOL {
		return append(head, line[startCol:]...)
	}

	endCol, endSize, endDisp, endWidth := charAtDisp(line, right, tabstop)
	var tail []byte
	switch {
	case endCol >= len(line):
		endCol = len(line)
	case endDisp+endWidth-1 > right:
		// A tab crosses the right edge, same rule the other way round.
		tail = bytes.Repeat([]byte{' '}, right-endDisp+1)
	default:
		endCol += endSize
	}
	if endCol < startCol {
		endCol = startCol
	}
	out := append(head, line[startCol:endCol]...)
	return append(out, tail...)
}

// blockDeleteLine returns the byte range a blockwise delete removes from one
// line and the spaces it puts back in their place, which are the cells of a
// straddled tab that fall outside the block. ok is false for a line the block
// does not reach, which a delete leaves untouched.
func blockDeleteLine(line []byte, blk Block, tabstop int) (start, end int, fill []byte, ok bool) {
	left, right := blk.Left, blockRight(line, blk, tabstop)
	if text.DisplayWidth(line, tabstop) <= left {
		return 0, 0, nil, false
	}

	startCol, _, startDisp, _ := charAtDisp(line, left, tabstop)
	if startDisp < left {
		fill = append(fill, bytes.Repeat([]byte{' '}, left-startDisp)...)
	}
	if blk.ToEOL {
		return startCol, len(line), fill, true
	}

	endCol, endSize, endDisp, endWidth := charAtDisp(line, right, tabstop)
	if endCol >= len(line) {
		endCol = len(line)
	} else {
		endCol += endSize
		if endDisp+endWidth-1 > right {
			fill = append(fill, bytes.Repeat([]byte{' '}, endDisp+endWidth-right-1)...)
		}
	}
	if endCol < startCol {
		endCol = startCol
	}
	return startCol, endCol, fill, true
}

// blockInsertCol returns the byte column a blockwise I or c inserts at on one
// line, and false for a line the block does not reach.
//
// "Does not reach" is strictly shorter than the block's left edge: a line whose
// last cell is the one before the edge gets the text appended to it. CTRL-V
// over four lines from column 3 with "st" as the fourth gives "stZ", not a
// skipped line and not "st Z", which is the difference between reading vim's
// is_short flag and running it.
func blockInsertCol(line []byte, blk Block, tabstop int) (int, bool) {
	if text.DisplayWidth(line, tabstop) < blk.Left {
		return 0, false
	}
	col, size, disp, _ := charAtDisp(line, blk.Left, tabstop)
	if disp < blk.Left && col < len(line) {
		col += size
	}
	return col, true
}

// blockAppendCol returns the byte column a blockwise A inserts at on one line
// and how many spaces have to be put in front of it first, which is how "ab"
// under a block ending at column 5 comes back as "ab Q".
func blockAppendCol(line []byte, blk Block, tabstop int) (col, pad int) {
	if blk.ToEOL {
		return len(line), 0
	}
	width := text.DisplayWidth(line, tabstop)
	if width <= blk.Right {
		return len(line), blk.Right - width + 1
	}
	col, size, disp, _ := charAtDisp(line, blk.Right, tabstop)
	if col >= len(line) {
		return len(line), 0
	}
	_ = disp
	return col + size, 0
}

// blockWidth is the block's width in display columns, which is what a
// blockwise register carries so that a later put can pad short lines.
func blockWidth(b *text.Buffer, blk Block, tabstop int) int {
	if !blk.ToEOL {
		return blk.Right - blk.Left + 1
	}
	// A $ block has no right edge. vim stores MAXCOL and prints the widest
	// line it actually took: getregtype() after CTRL-V j j $ y over lines of
	// 8, 1 and 0 cells from column 1 answers CTRL-V 8.
	wide := 0
	for n := blk.First; n <= blk.Last; n++ {
		if w := text.DisplayWidth(b.Line(n), tabstop) - blk.Left; w > wide {
			wide = w
		}
	}
	if wide < 1 {
		wide = 1
	}
	return wide
}

// BlockInsertAt returns the byte column a blockwise I or c starts inserting at
// on line n, and false for a line the block does not reach, which vim skips.
//
// It is exported because the mode machine, not the operator, is what repeats
// the typed text down the block: the operator ends when insert begins and only
// the mode machine knows what was typed or whether the insert was abandoned.
func BlockInsertAt(b *text.Buffer, blk Block, n int, opt Options) (col int, ok bool) {
	return blockInsertCol(b.Line(n), blk, opt.tabStop())
}

// BlockAppendAt returns the byte column a blockwise A inserts at on line n and
// how many spaces have to go in front of it first, which is how a short line
// under a block ending in column 5 comes back padded out to it.
func BlockAppendAt(b *text.Buffer, blk Block, n int, opt Options) (col, pad int) {
	return blockAppendCol(b.Line(n), blk, opt.tabStop())
}
