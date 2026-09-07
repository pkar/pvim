package x11

import "testing"

func TestXftDPI(t *testing.T) {
	for _, tc := range []struct {
		name string
		db   string
		want int
		ok   bool
	}{
		{name: "the one line that matters", db: "Xft.dpi:\t192\n", want: 192, ok: true},
		{name: "among others", db: "*background:\t#262626\nXft.dpi:\t96\nXterm*faceName:\tMonaco\n", want: 96, ok: true},
		{name: "a float", db: "Xft.dpi: 144.0\n", want: 144, ok: true},
		{name: "no trailing newline", db: "Xft.dpi: 120", want: 120, ok: true},
		{name: "commented out", db: "! Xft.dpi: 192\n", ok: false},
		{name: "a wildcard binding is a different resource", db: "*Xft.dpi: 192\n", ok: false},
		{name: "a different resource that starts the same", db: "Xft.dpiScale: 2\n", ok: false},
		{name: "not a number", db: "Xft.dpi: large\n", ok: false},
		{name: "zero is refused", db: "Xft.dpi: 0\n", ok: false},
		{name: "an empty database", db: "", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := xftDPI(tc.db)
			if ok != tc.ok || (ok && got != tc.want) {
				t.Errorf("xftDPI(%q) = %d, %v; want %d, %v", tc.db, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestScaleForDPI(t *testing.T) {
	for _, tc := range []struct{ dpi, want int }{
		{0, 1},
		{-1, 1},
		{96, 1},
		{110, 1},
		{120, 1}, // 1.25, which rounds down
		{144, 2}, // 1.5, which rounds up
		{192, 2},
		{288, 2}, // 3x clamps to 2: the rasteriser builds faces at 1 or 2
	} {
		if got := scaleForDPI(tc.dpi); got != tc.want {
			t.Errorf("scaleForDPI(%d) = %d, want %d", tc.dpi, got, tc.want)
		}
	}
}
