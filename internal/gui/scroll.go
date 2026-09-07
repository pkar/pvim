package gui

// The wheel, from a device delta to the notches the editor scrolls by.
//
// This is arithmetic over four floats and it has no business needing a window
// server to be checked, so it lives here with no build tag and the darwin file
// does nothing but read the event and call it. What the editor does with a
// notch is the editor's: 'mousescroll' is three lines and that number is not
// in this package.

// scrollState is the sub-cell remainder of a scroll gesture.
//
// A mouse wheel reports whole lines and a trackpad reports points, and the two
// have to be told apart or a trackpad flick scrolls the buffer a thousand
// lines. Either way the leftovers are kept: a slow two-finger drag is a long
// run of quarter-cell deltas and dropping each one would mean the buffer never
// moved at all.
type scrollState struct {
	x, y float64
}

// notches folds one scroll event into the accumulator and calls emit once per
// whole notch it now adds up to, in the order the notches happened.
//
// stepX and stepY are how much delta is worth one notch: one line for a
// discrete wheel, a cell's width or the row pitch in logical points for a
// trackpad's precise deltas.
//
// shift is Shift held, which is sideways scrolling and what 'nowrap' makes
// useful. A discrete mouse wheel has one axis and reports it as deltaY whatever
// is held down, so the modifier is the only thing that says which way the notch
// meant to go. The dx == 0 guard is there because AppKit does the same swap
// itself for some devices, and swapping an already-swapped delta would send a
// shift-wheel back up the buffer instead of across it.
func (s *scrollState) notches(dx, dy, stepX, stepY float64, shift bool, emit func(MouseAction)) {
	if stepX <= 0 || stepY <= 0 {
		return
	}
	if swapsAxes(dx, shift) {
		dx, dy = dy, 0
		stepX = stepY
	}
	s.x += dx
	s.y += dy

	// A positive scrollingDeltaY is content moving down the screen, which is
	// scrolling towards the start of the buffer: vim's wheel-up.
	for s.y >= stepY {
		s.y -= stepY
		emit(MouseWheelUp)
	}
	for s.y <= -stepY {
		s.y += stepY
		emit(MouseWheelDown)
	}
	for s.x >= stepX {
		s.x -= stepX
		emit(MouseWheelLeft)
	}
	for s.x <= -stepX {
		s.x += stepX
		emit(MouseWheelRight)
	}
}

// swapsAxes reports whether Shift turns this event's vertical delta into a
// horizontal one.
//
// It is a function of its own so that onScroll can ask the same question the
// arithmetic asks and take the Shift off the event it emits: on macOS Shift is
// the axis modifier and nothing else, and an editor that saw a horizontal
// wheel with Shift still set would read it as vim's <S-ScrollWheelRight>, which
// is a whole page. See cmd/pvim/mouse.go's wheel for the other half.
func swapsAxes(dx float64, shift bool) bool { return shift && dx == 0 }

// reset drops the remainder. Nothing calls it in the running editor: a gesture
// that ends leaves less than a notch behind and the next one continues from
// there, which is what makes two half-notch flicks scroll one line rather than
// none. It exists so a test can say which state it is starting from.
func (s *scrollState) reset() { s.x, s.y = 0, 0 }
