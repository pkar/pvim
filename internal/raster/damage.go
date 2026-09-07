package raster

import (
	"hash/fnv"

	"github.com/pkar/pvim/internal/screen"
)

// The dirty-rect seam: what a frontend has to know before it may paint less
// than the whole window.
//
// PaintRects has taken a span list, and painting only the changed
// cells is not a matter of diffing two grids. internal/screen's Grid.Diff
// answers "which cells hold a different rune or a different highlight id", and
// that is necessary and not sufficient: a `:colorscheme` reload, a
// `:hi Normal guibg=...`, a font change and a move to a display at another
// scale all change what every cell on the screen looks like while leaving every
// cell in the grid byte-identical. A frontend that trusted the diff would leave
// the window in the old colours until something happened to touch each cell,
// which is a bug that reproduces once a month and looks like a graphics driver
// problem.
//
// So the frontend keeps a Painted beside each buffer in its pool, and Damage
// compares stamps before it looks at cells.

// Stamp summarises everything outside the grid that decides what a cell looks
// like: the highlight table's contents, the face, 'linespace' and the cursor
// shape.
//
// It is a hash and not an identity. The highlight table is mutated in place by
// `:hi` -- Table.Set writes into the slice the previous frame was painted from
// -- so comparing pointers answers "same table" when the question is "same
// colours". Hashing its contents is what makes a recolour visible, and the
// table is sixty-odd entries so hashing it once per frame is nothing next to
// painting one row.
//
// A zero Stamp means nothing has been painted into a buffer yet, which is why
// StampOf never returns one.
type Stamp uint64

// StampOf returns the stamp for a frame about to be painted with opt.
//
// Everything it reads is a thing that changes pixels without changing cells.
// What it deliberately does not read is the grid: that is the diff's half of
// the question, and a stamp over the cells as well would make every keystroke a
// full repaint and there would be no point to any of this.
//
// The cursor shape is in here and the cursor position is not, which is the one
// asymmetry in the file. A shape change is a mode change, it happens on `i` and
// on `Esc` and not on every keystroke, and letting it cost a full repaint makes
// the stamp the whole safety net for the caret: inside the fast path the two
// frames always draw the same shape, so all Damage has to do about the caret is
// cover the cell it left and the cell it arrived on.
func StampOf(f Frame, opt PaintOptions) Stamp {
	h := fnv.New64a()
	var b [8]byte
	putU := func(v uint64) {
		for i := 0; i < 8; i++ {
			b[i] = byte(v >> (8 * i))
		}
		h.Write(b[:])
	}
	put := func(v int) { putU(uint64(v)) }

	// The face, through Face.ID and then its metrics. ID is the one that
	// matters and the metrics are belt and braces: two faces can have the same
	// four integers and draw different glyphs -- Monaco at 13pt and at 12.8pt
	// are both 8x18 ascent 13 -- so a stamp over the metrics alone leaves the
	// window in the old font after a `:set guifont=` between such a pair. See
	// Face.ID.
	if opt.Face != nil {
		putU(opt.Face.ID())
		m := opt.Face.Metrics()
		put(m.CellW)
		put(m.CellH)
		put(m.Ascent)
		put(m.Scale)
	}
	put(opt.Linespace)
	put(int(f.Cursor))

	// The whole table, in id order. Look and not the backing slice, because a
	// table can shrink under a frame already in flight and Look is the only
	// bounds-checked way in.
	n := f.HL.Len()
	put(n)
	for id := 0; id < n; id++ {
		hl := f.HL.Look(screen.HLID(id))
		put(int(hl.FG.R)<<16 | int(hl.FG.G)<<8 | int(hl.FG.B))
		put(int(hl.BG.R)<<16 | int(hl.BG.G)<<8 | int(hl.BG.B))
		put(int(hl.SP.R)<<16 | int(hl.SP.G)<<8 | int(hl.SP.B))
		put(int(hl.Attr))
	}
	// Zero is reserved for "no frame", so the one hash value that would
	// collide with it is nudged aside. One value in 2^64, at the cost of a
	// compare.
	if s := Stamp(h.Sum64()); s != 0 {
		return s
	}
	return 1
}

// Painted is what one buffer in a frontend's pool currently holds: the grid it
// was painted from, the stamp that went with it, and where the caret was left.
//
// Grid is the frontend's own snapshot and never the grid the editor is still
// writing into. Damage takes that snapshot itself, into this same grid when it
// is the right size, so a frontend keeps one of these per buffer, hands it back
// on the next frame and never allocates a grid per frame.
//
// A zero Painted is a buffer holding nothing that can be kept, which is what a
// fresh buffer, a resized one and a buffer whose paint returned an error all
// are. Record the Painted that Damage returned only when PaintRects returned
// nil; on an error record a zero one and the next frame repaints the lot.
type Painted struct {
	Grid      *screen.Grid
	Stamp     Stamp
	CursorRow int
	CursorCol int
}

// Damage returns the spans a frontend has to repaint to turn what one of its
// buffers already holds into f, the Painted to record for that buffer once the
// paint has landed, and whether this frame is a full repaint.
//
// A zero prev, a stamp that does not match, a grid of a different size, or a
// prev.Grid that is f.Grid itself all mean the buffer holds nothing that can be
// kept, and the answer is every row and full true. The caller passes the spans
// straight to PaintRects either way, so the two cases differ in cost and not in
// code, and full is there for a caller that wants to know whether the buffer
// was completely overwritten.
//
// The spans always cover the cursor cell and the cell the caret was last drawn
// on, because PaintRects only draws the caret where a span covers it and only
// erases the old one where a span covers that. Everything else is Grid.Diff,
// which has already widened its spans to whole double-width pairs.
func Damage(prev Painted, f Frame, opt PaintOptions) (spans []screen.Span, now Painted, full bool) {
	now = Painted{
		Stamp:     StampOf(f, opt),
		CursorRow: f.CursorRow,
		CursorCol: f.CursorCol,
	}
	if f.Grid == nil {
		return nil, Painted{}, true
	}

	if prev.Grid == f.Grid {
		// A caller that handed back the editor's live grid as its own snapshot
		// would get an empty diff and a window that never changed again. Cheap
		// to catch and impossible to diagnose from the outside; dropping it
		// here costs one full repaint and leaves the frame after it healthy,
		// because the snapshot below is then one this package owns.
		prev.Grid = nil
	}
	full = prev.Grid == nil ||
		prev.Stamp == 0 ||
		prev.Stamp != now.Stamp ||
		prev.Grid.Rows != f.Grid.Rows ||
		prev.Grid.Cols != f.Grid.Cols

	if full {
		// prev.Grid is still worth reusing here even though nothing on it can
		// be kept: a recolour and a font change leave a grid of the right size
		// to copy into, and only a resize allocates.
		now.Grid = snapshot(prev.Grid, f.Grid)
		return allRows(f.Grid), now, true
	}

	spans = f.Grid.Diff(prev.Grid)
	if f.Cursor != CursorNone {
		spans = addCell(spans, f.Grid, prev.CursorRow, prev.CursorCol)
		spans = addCell(spans, f.Grid, f.CursorRow, f.CursorCol)
	}
	now.Grid = snapshot(prev.Grid, f.Grid)
	return spans, now, false
}

// snapshot copies g into dst, reusing dst when it is the right size. It is
// called after the diff has been taken, because dst is the grid the diff was
// taken against.
func snapshot(dst, g *screen.Grid) *screen.Grid {
	if dst == nil || dst.Rows != g.Rows || dst.Cols != g.Cols {
		dst = screen.NewGrid(g.Rows, g.Cols)
	}
	copy(dst.Cells, g.Cells)
	return dst
}

// addCell adds the single cell at row, col to a span list already ordered by
// row and then column, merging it into whatever it touches so no cell is
// painted twice.
//
// Painting a cell twice would be harmless -- a span is a pure function of the
// frame, which is what TestPaintRectsCellByCellMatchesPaint pins -- so this is
// about not doing the work rather than about correctness.
func addCell(spans []screen.Span, g *screen.Grid, row, col int) []screen.Span {
	if !g.InBounds(row, col) {
		return spans
	}
	sp := screen.Span{Row: row, Col: col, Len: 1}
	for i, s := range spans {
		if s.Row < sp.Row || (s.Row == sp.Row && s.Col+s.Len < sp.Col) {
			continue // entirely before the new cell
		}
		if s.Row > sp.Row || s.Col > sp.Col+sp.Len {
			// Entirely after it, and nothing before it touched: insert here.
			spans = append(spans, screen.Span{})
			copy(spans[i+1:], spans[i:])
			spans[i] = sp
			return spans
		}
		// They touch or overlap. Grow s over the cell, then absorb whatever
		// that ran into: Diff can leave two spans on a row exactly touching,
		// and a cell landing on the seam joins all three.
		spans[i] = join(s, sp)
		for i+1 < len(spans) && spans[i+1].Row == row && spans[i+1].Col <= spans[i].Col+spans[i].Len {
			spans[i] = join(spans[i], spans[i+1])
			spans = append(spans[:i+1], spans[i+2:]...)
		}
		return spans
	}
	return append(spans, sp)
}

// join is the smallest span covering two on the same row.
func join(a, b screen.Span) screen.Span {
	lo := min(a.Col, b.Col)
	hi := max(a.Col+a.Len, b.Col+b.Len)
	return screen.Span{Row: a.Row, Col: lo, Len: hi - lo}
}

// allRows is every cell of g as one span per row, which is what Paint itself
// passes to PaintRects.
func allRows(g *screen.Grid) []screen.Span {
	if g == nil || g.Cols == 0 {
		return nil
	}
	spans := make([]screen.Span, 0, g.Rows)
	for row := 0; row < g.Rows; row++ {
		spans = append(spans, screen.Span{Row: row, Col: 0, Len: g.Cols})
	}
	return spans
}
