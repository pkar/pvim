package gui

import "testing"

// collect runs one scroll event through an accumulator and returns the notches
// it produced.
func collect(s *scrollState, dx, dy, stepX, stepY float64, shift bool) []MouseAction {
	var got []MouseAction
	s.notches(dx, dy, stepX, stepY, shift, func(a MouseAction) { got = append(got, a) })
	return got
}

// same reports whether two notch runs are equal.
func same(a, b []MouseAction) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWheelNotches is one event in and the notches out, for the shapes the two
// kinds of device produce.
//
// The step sizes are the two the darwin caller passes: 1 for a discrete wheel,
// whose deltas are already in lines, and a cell for a trackpad's precise
// deltas, which are in logical points. Monaco 13pt at scale 2 is a 16 pixel
// cell, which is 8 points, and the row is 35 pixels, which is 17.5.
func TestWheelNotches(t *testing.T) {
	const (
		wheelStep = 1.0
		padX      = 8.0
		padY      = 17.5
	)
	up, down := MouseWheelUp, MouseWheelDown
	left, right := MouseWheelLeft, MouseWheelRight

	cases := []struct {
		name           string
		dx, dy         float64
		stepX, stepY   float64
		shift          bool
		want           []MouseAction
		restX, restY   float64
		startX, startY float64
	}{
		{
			name: "one wheel notch up",
			dy:   1, stepX: wheelStep, stepY: wheelStep,
			want: []MouseAction{up},
		},
		{
			name: "one wheel notch down",
			dy:   -1, stepX: wheelStep, stepY: wheelStep,
			want: []MouseAction{down},
		},
		{
			// A wheel that reports three lines at once is three notches and
			// not one: the editor turns each into 'mousescroll' lines, so
			// collapsing them here would scroll a third as far as the device
			// asked for.
			name: "three lines in one event",
			dy:   3, stepX: wheelStep, stepY: wheelStep,
			want: []MouseAction{up, up, up},
		},
		{
			name: "a trackpad flick under one cell moves nothing",
			dy:   4, stepX: padX, stepY: padY,
			want: nil, restY: 4,
		},
		{
			// Two half-notch flicks scroll one line. Dropping the remainder
			// would mean a slow two-finger drag never moved the buffer at all.
			name:   "the remainder is kept",
			startY: 14,
			dy:     4, stepX: padX, stepY: padY,
			want: []MouseAction{up}, restY: 0.5,
		},
		{
			name: "a trackpad drag downwards",
			dy:   -40, stepX: padX, stepY: padY,
			want: []MouseAction{down, down}, restY: -5,
		},
		{
			// Shift and a wheel is sideways, which is what 'nowrap' makes
			// useful and what the vimrc sets.
			name: "shift turns a wheel notch sideways",
			dy:   1, stepX: wheelStep, stepY: wheelStep, shift: true,
			want: []MouseAction{left},
		},
		{
			name: "shift and a wheel notch the other way",
			dy:   -2, stepX: wheelStep, stepY: wheelStep, shift: true,
			want: []MouseAction{right, right},
		},
		{
			// AppKit swaps the axes itself for some devices. Swapping an
			// already-swapped delta would send a shift-wheel back up the
			// buffer instead of across it.
			name: "shift leaves an already-horizontal delta alone",
			dx:   -8, stepX: padX, stepY: padY, shift: true,
			want: []MouseAction{right},
		},
		{
			// A trackpad reports both axes at once on a diagonal drag, and
			// both are worth notches.
			name: "both axes in one event",
			dx:   -16, dy: 35, stepX: padX, stepY: padY,
			want: []MouseAction{up, up, right, right},
		},
		{
			// A zero step is a face that has not been built yet. Dividing by
			// it would loop forever.
			name: "a zero step produces nothing",
			dy:   100, stepX: 0, stepY: 0,
			want: nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &scrollState{x: c.startX, y: c.startY}
			got := collect(s, c.dx, c.dy, c.stepX, c.stepY, c.shift)
			if !same(got, c.want) {
				t.Errorf("notches = %v, want %v", got, c.want)
			}
			if s.x != c.restX || s.y != c.restY {
				t.Errorf("remainder = (%v, %v), want (%v, %v)", s.x, s.y, c.restX, c.restY)
			}
		})
	}
}

// TestWheelIsNotStuckOnADeadStep pins the loop guard rather than the arithmetic.
// A step of zero comes from a face that has not been built, and the loops in
// notches subtract the step, so a zero one never terminates: this test hangs
// rather than fails if the guard goes.
func TestWheelIsNotStuckOnADeadStep(t *testing.T) {
	s := &scrollState{}
	if got := collect(s, 5, 5, 0, 4, false); got != nil {
		t.Errorf("a zero horizontal step produced %v", got)
	}
	if got := collect(s, 5, 5, 4, -1, false); got != nil {
		t.Errorf("a negative vertical step produced %v", got)
	}
}

// TestScrollResetDropsTheRemainder. reset is only ever called by a test, and
// this is the test that says what it means.
func TestScrollResetDropsTheRemainder(t *testing.T) {
	s := &scrollState{}
	collect(s, 3, 3, 100, 100, false)
	if s.x == 0 && s.y == 0 {
		t.Fatal("a sub-notch delta left no remainder to drop")
	}
	s.reset()
	if s.x != 0 || s.y != 0 {
		t.Errorf("after reset the remainder is (%v, %v)", s.x, s.y)
	}
}
