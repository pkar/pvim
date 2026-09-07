//go:build darwin

package gui

import (
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/raster"
)

// The mouse path against real AppKit, with no window server.
//
// The arithmetic these two hand off to is tested on its own -- cellAt in
// geometry_test.go and scrollState in scroll_test.go, both of which run on the
// Linux cross-check as well. What is left, and what is here, is the half that
// can only be wrong against AppKit: the selectors. A selector name with a typo
// in it is not a compile error and not a wrong answer, it is an unrecognised
// selector exception the first time somebody clicks in the window, and nothing
// in this package would have said so before shipping.

// mouseWindow returns a window with a real view, a face and a size, which is
// everything cellOf reads.
func mouseWindow(t *testing.T) (*window, objc.ID) {
	t.Helper()
	face, err := raster.DefaultFace(1)
	if err != nil {
		t.Skipf("no Monaco: %v", err)
	}
	view := testView(t)
	w := &window{q: newEventq(), view: view, face: face, scale: 1, rows: 27, cols: 80}
	withWindow(t, w)
	return w, view
}

// viewHeight is the height testView builds its view at, in points, and the
// number the flip below is around.
const viewHeight = 480

// mouseEvent builds an NSEvent of one of the mouse types at a point given in
// the VIEW's coordinate space: x across, y down from the top left, which is
// where a text grid's origin is and what -isFlipped buys.
//
// The event itself carries a window-space point, and window space is AppKit's
// own: the origin is the BOTTOM left and y grows upwards. So the y is flipped
// here, and cellOf flips it back through -convertPoint:fromView: nil. That
// round trip is most of what these tests are for. Without the view's
// -isFlipped returning YES, a click near the top of the window would place the
// cursor near the bottom of the buffer, which is the sort of defect that looks
// like a scrolling bug for a day.
func mouseEvent(t *testing.T, kind uint64, x, y float64, flags uint64, clicks int) objc.ID {
	t.Helper()
	ev := objc.Send[objc.ID](objc.ID(objc.GetClass("NSEvent")),
		objc.RegisterName("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"),
		kind, NSPoint{X: x, Y: viewHeight - y}, flags, float64(0), int64(0), objc.ID(0),
		int64(0), int64(clicks), float32(0))
	if ev == 0 {
		t.Fatalf("NSEvent mouseEventWithType: returned nil for type %d", kind)
	}
	return ev
}

// The NSEventType values for the mouse, from NSEvent.h.
const (
	nsEventTypeLeftMouseDown  = 1
	nsEventTypeLeftMouseUp    = 2
	nsEventTypeRightMouseDown = 3
	nsEventTypeLeftMouseDrag  = 6
)

// TestMouseEventLandsOnTheQueueInCells is a click, a drag and a release
// through the real selectors, in the cells the editor is handed.
//
// Monaco 13pt at scale 1 is an 8 pixel advance and an 18 pixel row, so a click
// at (100, 40) is column 12 and row 2. That is one division, and it is here
// because it is the division that says the frontend and the editor agree about
// what a cell is.
func TestMouseEventLandsOnTheQueueInCells(t *testing.T) {
	w, _ := mouseWindow(t)

	w.onMouse(mouseEvent(t, nsEventTypeLeftMouseDown, 100, 40, 0, 1), MouseLeft, MousePress)
	w.onMouse(mouseEvent(t, nsEventTypeLeftMouseDrag, 108, 76, modFlagShift, 1), MouseLeft, MouseDrag)
	w.onMouse(mouseEvent(t, nsEventTypeLeftMouseUp, 0, 0, 0, 1), MouseLeft, MouseRelease)
	w.onMouse(mouseEvent(t, nsEventTypeRightMouseDown, 8, 18, modFlagControl, 1), MouseRight, MousePress)

	want := []MouseEvent{
		{Button: MouseLeft, Action: MousePress, Row: 2, Col: 12},
		{Button: MouseLeft, Action: MouseDrag, Row: 4, Col: 13, Mod: key.ModShift},
		{Button: MouseLeft, Action: MouseRelease, Row: 0, Col: 0},
		{Button: MouseRight, Action: MousePress, Row: 1, Col: 1, Mod: key.ModCtrl},
	}
	got := drain(w.q)
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		me, ok := got[i].(MouseEvent)
		if !ok {
			t.Fatalf("event %d is %T, want a MouseEvent", i, got[i])
		}
		if me != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, me, want[i])
		}
	}
}

// TestMouseClampsToTheGrid. A drag that leaves the window through an edge
// keeps reporting, and the cell it reports has to be one the editor can
// address: a selection to row -3 is a panic in whatever indexes it.
func TestMouseClampsToTheGrid(t *testing.T) {
	w, _ := mouseWindow(t)

	w.onMouse(mouseEvent(t, nsEventTypeLeftMouseDrag, -40, -40, 0, 1), MouseLeft, MouseDrag)
	w.onMouse(mouseEvent(t, nsEventTypeLeftMouseDrag, 1e6, 1e6, 0, 1), MouseLeft, MouseDrag)

	got := drain(w.q)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if me := got[0].(MouseEvent); me.Row != 0 || me.Col != 0 {
		t.Errorf("above and left of the window gave row %d col %d, want 0 and 0", me.Row, me.Col)
	}
	if me := got[1].(MouseEvent); me.Row != w.rows-1 || me.Col != w.cols-1 {
		t.Errorf("below and right of the window gave row %d col %d, want %d and %d",
			me.Row, me.Col, w.rows-1, w.cols-1)
	}
}

// scrollEvent builds a real scroll-wheel NSEvent.
//
// AppKit has no constructor for one -- +mouseEventWithType: rejects the scroll
// types -- so it goes the long way round: CGEventCreateScrollWheelEvent makes
// a CGEvent, +[NSEvent eventWithCGEvent:] wraps it, and the wrapper answers
// -scrollingDeltaY and -hasPreciseScrollingDeltas the way a real one does.
// Creating a CGEvent needs no permission; posting one would, and nothing here
// posts anything.
func scrollEvent(t *testing.T, lines int32, flags uint64) objc.ID {
	t.Helper()
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
	if err != nil {
		t.Skipf("no CoreGraphics: %v", err)
	}
	var create func(source uintptr, units uint32, wheels uint32, wheel1 int32) uintptr
	var release func(uintptr)
	purego.RegisterLibFunc(&create, cg, "CGEventCreateScrollWheelEvent")
	purego.RegisterLibFunc(&release, cg, "CFRelease")

	const kCGScrollEventUnitLine = 1
	cgEvent := create(0, kCGScrollEventUnitLine, 1, lines)
	if cgEvent == 0 {
		t.Skip("CGEventCreateScrollWheelEvent returned NULL")
	}
	t.Cleanup(func() { release(cgEvent) })

	ev := objc.Send[objc.ID](objc.ID(objc.GetClass("NSEvent")), objc.RegisterName("eventWithCGEvent:"), cgEvent)
	if ev == 0 {
		t.Skip("NSEvent eventWithCGEvent: returned nil")
	}
	if flags != 0 {
		// A CGEvent's flags are set on the CGEvent, not on the wrapper, and
		// this test only needs the window's own record of them, which is what
		// a device that reports no flags of its own falls back to anyway.
		current.setMods(uint(flags))
	}
	return ev
}

// TestScrollWheelReachesTheQueue drives a real scroll event through the real
// selectors.
//
// One line up is one notch up. How far a notch scrolls is the editor's
// business and is not in this package: 'mousescroll' is three lines and the
// number appears nowhere here.
func TestScrollWheelReachesTheQueue(t *testing.T) {
	w, _ := mouseWindow(t)

	w.onScroll(scrollEvent(t, 1, 0))
	got := drain(w.q)
	if len(got) != 1 {
		t.Fatalf("one line of scroll produced %d events: %v", len(got), got)
	}
	me, ok := got[0].(MouseEvent)
	if !ok {
		t.Fatalf("scroll produced a %T", got[0])
	}
	if me.Action != MouseWheelUp || me.Button != MouseNone {
		t.Errorf("scrolling up gave %+v, want a MouseNone MouseWheelUp", me)
	}

	w.onScroll(scrollEvent(t, -2, 0))
	got = drain(w.q)
	if len(got) != 2 {
		t.Fatalf("two lines down produced %d events: %v", len(got), got)
	}
	for i, ev := range got {
		if me := ev.(MouseEvent); me.Action != MouseWheelDown {
			t.Errorf("event %d of a downward scroll is %v, want MouseWheelDown", i, me.Action)
		}
	}
}

// TestShiftWheelGoesSideways is the vimrc's 'nowrap' half of the wheel.
//
// A discrete wheel has one axis and reports it as deltaY whatever is held
// down, so Shift is the only thing that says the notch meant to go across.
// The flags come from the window's own record here rather than from the event,
// which is the fallback path flagsChanged: exists for and the one a device
// that fills in no flags of its own takes.
func TestShiftWheelGoesSideways(t *testing.T) {
	w, _ := mouseWindow(t)

	w.onScroll(scrollEvent(t, 1, modFlagShift))
	got := drain(w.q)
	if len(got) != 1 {
		t.Fatalf("shift and one line produced %d events: %v", len(got), got)
	}
	me := got[0].(MouseEvent)
	if me.Action != MouseWheelLeft {
		t.Errorf("shift and a wheel up gave %v, want MouseWheelLeft", me.Action)
	}
	// And the Shift is gone with it. It has been spent turning the wheel
	// sideways: vim reads Shift on a wheel as a whole page, so an event that
	// arrived sideways AND modified would page the buffer across instead of
	// nudging it six columns.
	if me.Mod&key.ModShift != 0 {
		t.Errorf("the sideways wheel still carries Shift: %+v", me)
	}
}
