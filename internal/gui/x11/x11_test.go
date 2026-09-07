package x11

import (
	"errors"
	"reflect"
	"runtime"
	"testing"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// TestTheSeamIsGuisSeam is the check that keeps the two frontends
// interchangeable from cmd/pvim's side.
//
// It is a compile-time assertion written as a test so that the failure names
// what changed. If internal/gui grows a seventh event or a fifth Client method
// and this package does not, cmd/pvim cannot hand the same handler to both, and
// the day that happens is the day somebody adds a platform switch to the editor
// rather than to the frontend.
func TestTheSeamIsGuisSeam(t *testing.T) {
	var (
		_ func(gui.Handler) error    = Run
		_ func() bool                = Available
		_ func(func()) error         = OnMain
		_ func(func()) error         = PostMain
		_ func(raster.GUIFont) error = SetFont
		_ func(int) error            = SetLinespace
		_ raster.GUIFont             = Font
	)
	if !GUIRunning {
		t.Error("GUIRunning is false; the vimrc's terminal-only branches would take effect in a window")
	}
	if reflect.TypeOf(Run) != reflect.TypeOf(gui.Run) {
		t.Errorf("Run is %v and gui.Run is %v; cmd/pvim cannot pick between them",
			reflect.TypeOf(Run), reflect.TypeOf(gui.Run))
	}
}

// TestDefaultFontIsTheVimrcs says the thing about the font that is easy to get
// backwards. This package's Font starts as the vimrc's Monaco, exactly as
// internal/gui's does, and the substitution happens in ResolveFont at the point
// the box turns out not to have it. Defaulting to DejaVu here instead would
// mean `:set guifont?` printed a font nobody asked for even on a box that does
// have Monaco installed.
func TestDefaultFontIsTheVimrcs(t *testing.T) {
	if Font != raster.DefaultGUIFont {
		t.Errorf("Font is %v, want the vimrc's %v", Font, raster.DefaultGUIFont)
	}
	if Font.Family != "Monaco" || Font.Size != raster.DefaultPointSize {
		t.Errorf("Font is %v, want Monaco at %v points", Font, raster.DefaultPointSize)
	}
}

// TestRefusesWhereThereIsNoBackend runs on everything that is not Linux, which
// is the machine this was written on. It is the one behavioural test of the
// backend seam that can run here at all, and what it pins is that a wrong-platform
// call is an error rather than a panic: cmd/pvim asks Available first, and a
// caller that does not has to get a sentence back.
func TestRefusesWhereThereIsNoBackend(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this platform has a backend")
	}
	if Available() {
		t.Error("Available is true on a platform with no X11 backend compiled in")
	}
	if err := Run(func(gui.Client, gui.Event) error { return nil }); !errors.Is(err, ErrNoBackend) {
		t.Errorf("Run gave %v, want %v", err, ErrNoBackend)
	}
	if err := OnMain(func() {}); !errors.Is(err, ErrNoEventLoop) {
		t.Errorf("OnMain gave %v, want %v", err, ErrNoEventLoop)
	}
	if err := PostMain(func() {}); !errors.Is(err, ErrNoEventLoop) {
		t.Errorf("PostMain gave %v, want %v", err, ErrNoEventLoop)
	}
	if err := SetFont(raster.DefaultGUIFont); !errors.Is(err, ErrNoBackend) {
		t.Errorf("SetFont gave %v, want %v", err, ErrNoBackend)
	}
	if err := SetLinespace(2); !errors.Is(err, ErrNoBackend) {
		t.Errorf("SetLinespace gave %v, want %v", err, ErrNoBackend)
	}
	if f, ok := InForce(); ok {
		t.Errorf("InForce claims %v is on screen with no window open", f)
	}
}

// TestEventqIsFifoAndDoesNotBlock is the queue between the X event loop and the
// editor. The property that matters is the second one: a push must return
// whatever the editor is doing, because the event loop is what answers the
// window manager.
func TestEventqIsFifoAndDoesNotBlock(t *testing.T) {
	q := newEventq()
	for i := 0; i < 10000; i++ {
		q.push(gui.ResizeEvent{Rows: i})
	}
	for i := 0; i < 10000; i++ {
		ev, ok := q.pop()
		if !ok {
			t.Fatalf("the queue ran dry at %d", i)
		}
		if r, isResize := ev.(gui.ResizeEvent); !isResize || r.Rows != i {
			t.Fatalf("event %d is %v, want ResizeEvent{Rows: %d}", i, ev, i)
		}
	}
	q.push(gui.CloseEvent{})
	q.close()
	if ev, ok := q.pop(); !ok {
		t.Error("an event pushed before close was lost")
	} else if _, isClose := ev.(gui.CloseEvent); !isClose {
		t.Errorf("got %v, want the CloseEvent", ev)
	}
	if _, ok := q.pop(); ok {
		t.Error("the queue answered after it was closed and drained")
	}
}

func TestRasterCursor(t *testing.T) {
	for _, tc := range []struct {
		in   screen.CursorShape
		want raster.CursorShape
	}{
		{screen.CursorBlock, raster.CursorBlock},
		{screen.CursorBar, raster.CursorBar},
		{screen.CursorUnderline, raster.CursorUnderline},
		{screen.CursorHollow, raster.CursorHollow},
		{screen.CursorShape(200), raster.CursorBlock},
	} {
		if got := rasterCursor(tc.in); got != tc.want {
			t.Errorf("rasterCursor(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
