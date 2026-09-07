package text

import "testing"

// The whole point of Compare is that it orders by line first and column second.
// Getting that backwards produces a Range that looks fine on one line and
// silently covers the wrong half of a buffer on two, which is why the cases
// that cross a line boundary are here and not just the easy ones.
func TestPosCompare(t *testing.T) {
	cases := []struct {
		name string
		p, q Pos
		want int // sign only
	}{
		{"same place", Pos{3, 4}, Pos{3, 4}, 0},
		{"earlier column", Pos{3, 2}, Pos{3, 9}, -1},
		{"later column", Pos{3, 9}, Pos{3, 2}, +1},
		{"earlier line beats later column", Pos{2, 99}, Pos{3, 0}, -1},
		{"later line beats earlier column", Pos{4, 0}, Pos{3, 99}, +1},
		{"first line first column", Pos{1, 0}, Pos{1, 0}, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.p.Compare(c.q)
			if sign(got) != c.want {
				t.Errorf("Pos%v.Compare(Pos%v) = %d, want sign %d", c.p, c.q, got, c.want)
			}
			if want := c.want < 0; c.p.Before(c.q) != want {
				t.Errorf("Pos%v.Before(Pos%v) = %v, want %v", c.p, c.q, !want, want)
			}
		})
	}
}

// Range is half-open, and an empty one has to stay empty: a motion that ends
// where it started must delete nothing rather than one byte.
func TestRangeHalfOpen(t *testing.T) {
	cases := []struct {
		name  string
		r     Range
		empty bool
		in    []Pos
		out   []Pos
	}{
		{
			name:  "empty at a point",
			r:     Range{Pos{2, 5}, Pos{2, 5}},
			empty: true,
			out:   []Pos{{2, 5}, {2, 4}, {2, 6}},
		},
		{
			name:  "one byte",
			r:     Range{Pos{2, 5}, Pos{2, 6}},
			empty: false,
			in:    []Pos{{2, 5}},
			out:   []Pos{{2, 6}, {2, 4}},
		},
		{
			name:  "across two lines, end excluded",
			r:     Range{Pos{2, 5}, Pos{4, 0}},
			empty: false,
			in:    []Pos{{2, 5}, {3, 0}, {3, 80}},
			out:   []Pos{{2, 4}, {4, 0}, {4, 1}},
		},
		{
			name:  "reversed is empty, not backwards",
			r:     Range{Pos{4, 0}, Pos{2, 5}},
			empty: true,
			out:   []Pos{{3, 0}},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.Empty(); got != c.empty {
				t.Errorf("Range%v.Empty() = %v, want %v", c.r, got, c.empty)
			}
			for _, p := range c.in {
				if !c.r.Contains(p) {
					t.Errorf("Range%v.Contains(Pos%v) = false, want true", c.r, p)
				}
			}
			for _, p := range c.out {
				if c.r.Contains(p) {
					t.Errorf("Range%v.Contains(Pos%v) = true, want false", c.r, p)
				}
			}
		})
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return +1
	}
	return 0
}
