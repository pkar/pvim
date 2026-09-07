package x11

// The pixel-to-cell arithmetic. It is four integer divisions and there is no
// reason to need an X server to test them, so it has no build tag.
//
// It repeats internal/gui's geometry.go rather than importing it, because those
// functions are unexported there and exporting them would make the two
// frontends' geometry one thing that both depend on -- which is fine right up
// until the day X11 needs a rule AppKit does not, at which point the shared
// function grows a platform flag and both windows get the other's bug. Twenty
// lines of arithmetic in each is the cheaper of the two.

// cellsFor returns how many whole rows and columns of cellW by rowH device
// pixels fit in a drawable wPx by hPx pixels.
//
// Partial cells are dropped rather than rounded up: half a row at the bottom of
// the window is what vim leaves there too, and rounding up would hand the
// editor a row it cannot fully draw. The result is never smaller than 1x1,
// because a window a window manager has tiled down to nothing still has to be
// handed a screen the editor can address.
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

// cellAt maps a point in the window's coordinate space onto a grid cell.
//
// x and y are device pixels with the origin at the top left, which is what X11
// reports directly: unlike AppKit there is no flipped-coordinate question and
// no logical-point-to-pixel scale, because X11 has one coordinate system and it
// is the one the pixels are in. That is why this takes no scale argument and
// internal/gui's does.
//
// The result is clamped to the grid, so a drag that leaves the window through
// the top edge selects to the first row rather than to row -3. X11 delivers
// those: a pointer grab during a drag reports negative coordinates once the
// pointer is outside the window, which is exactly what a selection drag does.
func cellAt(x, y, cellW, rowH, rows, cols int) (row, col int) {
	if cellW <= 0 || rowH <= 0 {
		return 0, 0
	}
	return clamp(divFloor(y, rowH), 0, rows-1), clamp(divFloor(x, cellW), 0, cols-1)
}

// divFloor divides rounding towards negative infinity, which Go's / does not:
// -1/8 is 0 in Go and -1 here. Without it a drag one pixel above the window
// lands on row 0 from above and row 0 from below, and a selection dragged
// upwards out of the window stops moving instead of extending.
func divFloor(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
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
