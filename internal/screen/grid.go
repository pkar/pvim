// Package screen is the editor's picture of itself: a rectangular grid of cells
// with a highlight table beside it, and nothing at all about pixels or escape
// sequences. Both frontends render this and neither is visible from here.
//
// The point of the seam is that the editor can be driven and asserted with no
// window and no terminal. Every test in this package compares a Grid against a
// literal multi-line string, the way vim's own screendump tests do, and
// everything built on top of it leans on the same Dump helper.
//
// Render is the whole of it: a Frame in, a Screen out. Around that sit the
// pieces a frame is made of -- the character walk that turns a buffer line's
// bytes into cells, the fold tree, the horizontal scroll under 'nowrap', the
// layout of a tab page's windows, the status lines, the tabline, the ruler,
// the command line, the message area and the completion menu.
//
// Everything here was measured against vim 9.2 patches 1-321 rather than read
// out of the documentation. The measurement is
//
//	vim --clean -n -i NONE --not-a-term -s KEYS FILE
//
// with a keys script that sets ":set lines= columns=", does the thing, and
// writes the screen out cell by cell through screenchar() and screenattr().
// Two numbers could not be taken that way -- the ruler's fill count and
// 'showcmd”s column, both of which vanish under the redraw the dump needs --
// and those came off the escape sequences vim wrote to a pty. Where a rule
// could not be measured at all the comment above it says so.
package screen

// Cell is one character position on the screen.
//
// A double-width rune occupies two cells: the first holds the rune, the second
// holds a zero Rune with Tail set and the same HL, and the pair is written and
// erased together. The flag is on the second half rather than the first because
// that is the half a renderer has to be told about: the first half is already
// self-describing, RuneWidth of its rune says two, and a cell holding a zero
// rune with no flag would be indistinguishable from a NUL somebody wrote by
// mistake.
//
// The struct is comparable and small on purpose. Frame diffing is a struct
// compare per cell, twice a frame at 200x60, and that is the one place in this
// editor where an indirection would actually show up.
type Cell struct {
	Rune rune
	HL   HLID
	Tail bool
}

// blank is the cell Clear and Resize fill with: a space in Normal, never a zero
// rune. A zeroed cell renders as NUL, which the terminal frontend writes as
// nothing at all and the rasteriser draws as .notdef, so the difference between
// the two shows up as a screenful of boxes rather than as a subtle bug.
var blank = Cell{Rune: ' '}

// Grid is the drawable surface, row-major, len(Cells) == Rows*Cols.
type Grid struct {
	Rows  int
	Cols  int
	Cells []Cell

	// dirty is one flag per row, set by every write through this type's
	// methods. It is a hint for a frontend that wants to skip whole rows before
	// diffing them; Diff itself does not read it, because the authoritative
	// answer to "what changed" is a comparison against the frame that was
	// actually painted, not a record of what somebody wrote.
	dirty []bool
}

// NewGrid returns a grid of blanks in the Normal highlight.
func NewGrid(rows, cols int) *Grid {
	g := &Grid{}
	g.Resize(rows, cols)
	return g
}

// InBounds reports whether row and col name a cell of this grid.
func (g *Grid) InBounds(row, col int) bool {
	return row >= 0 && row < g.Rows && col >= 0 && col < g.Cols
}

// At returns a pointer to the cell at row, col, both 0-based. It panics on an
// out-of-range index, the way a slice does, because every caller here has
// already clamped to the window it was given.
//
// Writing through the returned pointer skips the dirty-row bookkeeping and the
// wide-pair repair that Set does. Read through At, write through Set.
func (g *Grid) At(row, col int) *Cell {
	return &g.Cells[row*g.Cols+col]
}

// Resize reallocates the grid and clears it. Redrawing after a resize is the
// caller's job: keeping stale cells across a size change only ever produces a
// frame nobody meant to draw, with half the old picture at the wrong offset,
// and every one of those is a bug that reproduces only while dragging a window
// edge.
func (g *Grid) Resize(rows, cols int) {
	if rows < 0 {
		rows = 0
	}
	if cols < 0 {
		cols = 0
	}
	g.Rows, g.Cols = rows, cols
	g.Cells = make([]Cell, rows*cols)
	g.dirty = make([]bool, rows)
	g.Clear()
}

// Clear fills the grid with blanks in the Normal highlight and marks every row
// dirty.
func (g *Grid) Clear() {
	for i := range g.Cells {
		g.Cells[i] = blank
	}
	for i := range g.dirty {
		g.dirty[i] = true
	}
}

// MarkDirty records that row needs repainting. Callers that wrote through At
// have to say so themselves.
func (g *Grid) MarkDirty(row int) {
	if row >= 0 && row < len(g.dirty) {
		g.dirty[row] = true
	}
}

// DirtyRows returns the rows written since the last ClearDirty, in order.
func (g *Grid) DirtyRows() []int {
	var rows []int
	for r, d := range g.dirty {
		if d {
			rows = append(rows, r)
		}
	}
	return rows
}

// ClearDirty forgets which rows were written. A frontend calls this after it
// has painted, not before.
func (g *Grid) ClearDirty() {
	for i := range g.dirty {
		g.dirty[i] = false
	}
}

// Set writes r at row, col in highlight hl and returns the number of cells it
// took: 1, or 2 for a double-width rune.
//
// It returns 0 and writes nothing for a rune that will not fit. A double-width
// rune in the last column is the case that matters and the cell is left blank,
// because half a glyph is not a thing that can be drawn and this primitive has
// no opinion about what should go there instead.
//
// Vim's own answer is a '>'. Measured with screenchar() over a
// 10-column window at 'nowrap' on "a" followed by five U+6F22:
//
//	a漢漢漢漢>
//
// An earlier comment here said vim left the column blank and called it
// measured; it is not what vim does, and the sentence is corrected rather than
// deleted so the next reader knows the claim was checked and found wrong.
// Drawing the '>' belongs to the window renderer, which is the only thing that
// knows whether the cell is a right-hand edge or the middle of a status line,
// and winline.go does it there.
//
// A rune of width 0, a combining mark, is also refused. A Cell holds one rune
// and there is nowhere to put it; see SetString.
func (g *Grid) Set(row, col int, r rune, hl HLID) int {
	w := RuneWidth(r)
	if w == 0 || !g.InBounds(row, col) || col+w > g.Cols {
		return 0
	}
	g.breakPair(row, col)
	if w == 2 {
		g.breakPair(row, col+1)
	}
	*g.At(row, col) = Cell{Rune: r, HL: hl}
	if w == 2 {
		*g.At(row, col+1) = Cell{HL: hl, Tail: true}
	}
	g.MarkDirty(row)
	return w
}

// breakPair blanks the other half of a double-width pair that overlaps row,
// col, so that overwriting half of a wide rune never leaves an orphan behind.
func (g *Grid) breakPair(row, col int) {
	c := g.At(row, col)
	switch {
	case c.Tail && col > 0:
		*g.At(row, col-1) = blank
	case RuneWidth(c.Rune) == 2 && col+1 < g.Cols:
		*g.At(row, col+1) = blank
	}
}

// SetString writes s starting at row, col in highlight hl and returns the
// column after the last cell it wrote.
//
// It stops at the right edge rather than wrapping: a grid row is a row, and the
// decision to wrap a buffer line onto the next one belongs to the window
// renderer, which is the only thing that knows about 'wrap' and 'breakindent'.
// A double-width rune that would straddle the edge is refused whole, leaving
// the last column blank, which is what vim does.
//
// Combining marks are dropped. A Cell holds one rune, so an accent has nowhere
// to live until the day a Cell grows a small tail of them; that day is not
// here yet, and this is the place it will change.
func (g *Grid) SetString(row, col int, s string, hl HLID) int {
	if row < 0 || row >= g.Rows {
		return col
	}
	for _, r := range s {
		if col >= g.Cols {
			break
		}
		w := g.Set(row, col, r, hl)
		if w == 0 {
			if RuneWidth(r) == 0 {
				continue // a combining mark, dropped, not a stop
			}
			break // will not fit; the remaining column stays blank
		}
		col += w
	}
	return col
}

// Fill writes n cells of r starting at row, col in highlight hl, which is how a
// status line gets its background and how a row gets erased. It stops at the
// right edge and returns the column after the last cell written.
func (g *Grid) Fill(row, col, n int, r rune, hl HLID) int {
	for i := 0; i < n; i++ {
		w := g.Set(row, col, r, hl)
		if w == 0 {
			break
		}
		col += w
	}
	return col
}

// Span is a run of cells on one row that changed between two frames: the unit
// Diff hands back, which the terminal frontend turns into one cursor
// positioning plus a string of ANSI, and the GUI frontend turns into one rect
// to blit.
//
// Row and Col are 0-based, Len is a count of cells. A span never crosses a row
// boundary, because a run that wrapped would have to care about the grid's
// stride, and it never starts or ends in the middle of a double-width pair,
// because half a glyph is not a thing that can be drawn.
type Span struct {
	Row int
	Col int
	Len int
}

// Diff returns the spans where g differs from prev, in row then column order.
//
// A nil prev, or one of a different size, means everything changed: the caller
// resized or is painting its first frame, and there is no useful comparison to
// make against a buffer of the wrong shape.
func (g *Grid) Diff(prev *Grid) []Span {
	if prev == nil || prev.Rows != g.Rows || prev.Cols != g.Cols {
		spans := make([]Span, 0, g.Rows)
		for row := 0; row < g.Rows; row++ {
			if g.Cols > 0 {
				spans = append(spans, Span{Row: row, Col: 0, Len: g.Cols})
			}
		}
		return spans
	}

	var spans []Span
	for row := 0; row < g.Rows; row++ {
		base := row * g.Cols
		col := 0
		for col < g.Cols {
			if g.Cells[base+col] == prev.Cells[base+col] {
				col++
				continue
			}
			start := col
			for col < g.Cols && g.Cells[base+col] != prev.Cells[base+col] {
				col++
			}
			end := col // exclusive
			start, end = g.widen(row, start, end)
			spans = append(spans, Span{Row: row, Col: start, Len: end - start})
		}
	}
	return spans
}

// widen pushes a span's ends out so it holds whole double-width pairs. A span
// that begins on a trailing half or ends on a leading half would repaint half a
// glyph, and both frontends would have to know that; they should not have to.
func (g *Grid) widen(row, start, end int) (int, int) {
	if start > 0 && g.At(row, start).Tail {
		start--
	}
	if end < g.Cols && g.At(row, end).Tail {
		end++
	}
	return start, end
}
