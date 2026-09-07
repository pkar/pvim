package gui

import "testing"

// TestCellsFor checks the window-size-to-grid-size arithmetic, including the
// two edges that matter: a partial cell is dropped, and a window too small for
// one cell still reports a grid of 1x1 rather than an empty one nothing can be
// drawn into.
func TestCellsFor(t *testing.T) {
	tests := []struct {
		name                  string
		wPx, hPx, cellW, rowH int
		wantRows, wantCols    int
	}{
		// The measurement: a 640x480 point window at retina scale is
		// 1280x960 device pixels, and Monaco 13pt at scale 2 is a 16 by 34
		// pixel cell, giving vim's 80 columns.
		{"640x480 at retina", 1280, 960, 16, 34, 28, 80},
		{"640x480 at scale 1", 640, 480, 8, 17, 28, 80},
		{"a partial row is dropped", 1280, 970, 16, 34, 28, 80},
		{"a partial column is dropped", 1290, 960, 16, 34, 28, 80},
		{"one pixel high", 1280, 1, 16, 34, 1, 80},
		{"nothing at all", 0, 0, 16, 34, 1, 1},
		{"a face with no cell", 1280, 960, 0, 0, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, cols := cellsFor(tt.wPx, tt.hPx, tt.cellW, tt.rowH)
			if rows != tt.wantRows || cols != tt.wantCols {
				t.Errorf("cellsFor(%d, %d, %d, %d) = %dx%d, want %dx%d",
					tt.wPx, tt.hPx, tt.cellW, tt.rowH, rows, cols, tt.wantRows, tt.wantCols)
			}
		})
	}
}

// TestCellAt checks the mouse-point-to-cell mapping, in logical points at
// retina scale, and the clamping that keeps a drag out of the window from
// producing a negative index.
func TestCellAt(t *testing.T) {
	const scale, cellW, rowH, rows, cols = 2, 16, 34, 28, 80

	tests := []struct {
		name             string
		x, y             float64
		wantRow, wantCol int
	}{
		{"origin", 0, 0, 0, 0},
		{"inside the first cell", 7.9, 16.9, 0, 0},
		{"the start of the second column", 8, 0, 0, 1},
		{"the start of the second row", 0, 17, 1, 0},
		{"the last cell", 639, 475, 27, 79},
		{"past the right edge clamps", 5000, 0, 0, 79},
		{"past the bottom clamps", 0, 5000, 27, 0},
		{"above the top clamps", 0, -40, 0, 0},
		{"left of the window clamps", -40, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, col := cellAt(tt.x, tt.y, scale, cellW, rowH, rows, cols)
			if row != tt.wantRow || col != tt.wantCol {
				t.Errorf("cellAt(%v, %v) = row %d col %d, want row %d col %d",
					tt.x, tt.y, row, col, tt.wantRow, tt.wantCol)
			}
		})
	}
}
