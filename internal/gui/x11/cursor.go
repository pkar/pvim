package x11

import (
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// rasterCursor maps the screen's idea of a caret onto the rasteriser's.
//
// The same switch internal/gui has, for the same reason: the two enums have the
// same four names in the same order and this is still a switch, because they
// are owned by different packages for different reasons -- internal/screen's is
// a mode fact and internal/raster's is a drawing instruction -- and a
// conversion that happens to be a cast is a silent wrong caret the day
// one of them grows a fifth name.
//
// The caret does not blink here either. That is deliberate (D-004) and not a
// gap in the port: there is no timer in this package that could make it, and a
// blink would be the one thing that repainted the window with nothing typed,
// which on X11 means a PutImage over the socket twice a second for the life of
// the session against a dirty-rect blit that otherwise costs nothing at all
// while the editor is idle.
func rasterCursor(c screen.CursorShape) raster.CursorShape {
	switch c {
	case screen.CursorBar:
		return raster.CursorBar
	case screen.CursorUnderline:
		return raster.CursorUnderline
	case screen.CursorHollow:
		return raster.CursorHollow
	default:
		return raster.CursorBlock
	}
}
