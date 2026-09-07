package x11

import "testing"

func TestCellsFor(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		wPx, hPx, cellW, rowH int
		wantRows, wantCols    int
	}{
		{"an exact fit", 640, 432, 8, 18, 24, 80},
		{"a partial cell is dropped", 645, 440, 8, 18, 24, 80},
		{"a window smaller than one cell still has a grid", 3, 3, 8, 18, 1, 1},
		{"a zero cell does not divide by zero", 640, 480, 0, 18, 1, 1},
		{"a zero row height does not divide by zero", 640, 480, 8, 0, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, cols := cellsFor(tc.wPx, tc.hPx, tc.cellW, tc.rowH)
			if rows != tc.wantRows || cols != tc.wantCols {
				t.Errorf("got %dx%d, want %dx%d", rows, cols, tc.wantRows, tc.wantCols)
			}
		})
	}
}

func TestCellAt(t *testing.T) {
	const cellW, rowH, rows, cols = 8, 18, 24, 80
	for _, tc := range []struct {
		name     string
		x, y     int
		row, col int
	}{
		{"the origin", 0, 0, 0, 0},
		{"inside the first cell", 7, 17, 0, 0},
		{"the start of the second", 8, 18, 1, 1},
		{"clamped at the right edge", 5000, 0, 0, 79},
		{"clamped at the bottom", 0, 5000, 23, 0},
		{"a drag above the window clamps to the first row", 16, -40, 0, 2},
		{"a drag left of the window clamps to the first column", -3, 36, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row, col := cellAt(tc.x, tc.y, cellW, rowH, rows, cols)
			if row != tc.row || col != tc.col {
				t.Errorf("got row %d col %d, want row %d col %d", row, col, tc.row, tc.col)
			}
		})
	}
}

// TestDivFloorRoundsTowardsNegative is the rule Go's own division does not
// have, and the reason a selection dragged out of the top of the window keeps
// extending instead of sticking on row 0.
func TestDivFloorRoundsTowardsNegative(t *testing.T) {
	for _, tc := range []struct{ a, b, want int }{
		{16, 8, 2},
		{15, 8, 1},
		{0, 8, 0},
		{-1, 8, -1},
		{-8, 8, -1},
		{-9, 8, -2},
	} {
		if got := divFloor(tc.a, tc.b); got != tc.want {
			t.Errorf("divFloor(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestClamp(t *testing.T) {
	for _, tc := range []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{11, 0, 10, 10},
		{5, 0, -1, 0}, // an empty range collapses to lo
	} {
		if got := clamp(tc.v, tc.lo, tc.hi); got != tc.want {
			t.Errorf("clamp(%d, %d, %d) = %d, want %d", tc.v, tc.lo, tc.hi, got, tc.want)
		}
	}
}
