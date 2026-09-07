package gui

// The pixel-to-cell arithmetic, kept out of the darwin file because it is four
// integer divisions and there is no reason to need a window to test them.

// cellsFor returns how many whole rows and columns of cellW by rowH device
// pixels fit in a drawable wPx by hPx pixels.
//
// Partial cells are dropped rather than rounded up: half a row at the bottom of
// the window is what vim leaves there too, and rounding up would hand the
// editor a row it cannot fully draw.
//
// The result is never smaller than 1x1. A window dragged to one pixel high
// still has to be handed a screen the editor can address, and a crash report
// about resizing to nothing starts with a zero here.
func cellsFor(wPx, hPx, cellW, rowH int) (rows, cols int) {
	if cellW <= 0 || rowH <= 0 {
		return 1, 1
	}
	cols = wPx / cellW
	rows = hPx / rowH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	return rows, cols
}

// cellAt maps a point in the view's coordinate space onto a grid cell.
//
// x and y are logical points with the origin at the top left, which is what an
// NSView reports once -isFlipped returns YES, and scale converts them to the
// device pixels the cell metrics are in. The result is clamped to the grid, so
// a drag that leaves the window through the top edge selects to the first row
// rather than to row -3.
func cellAt(x, y float64, scale, cellW, rowH, rows, cols int) (row, col int) {
	if scale < 1 {
		scale = 1
	}
	if cellW <= 0 || rowH <= 0 {
		return 0, 0
	}
	col = int(x*float64(scale)) / cellW
	row = int(y*float64(scale)) / rowH
	return clamp(row, 0, rows-1), clamp(col, 0, cols-1)
}

// clamp holds v inside [lo, hi]. An empty range collapses to lo, which is what
// a zero-row grid should give rather than a negative index.
func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
