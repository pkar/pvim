package gui

import (
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// The caret, which this package decides nothing about.
//
// Vim's four shapes -- block in normal, bar in insert, underline in replace,
// hollow in a window that is not focused -- are the editor's answer to the mode
// it is in and to the FocusEvent this package sent it, and they arrive here on
// the Screen as screen.CursorShape. All the window does is name the same shape
// in the rasteriser's vocabulary.
//
// It does not blink, and that is deliberate (D-004) rather than an omission:
// vim's GUI blinks by default, there is no timer in this package that could
// make it, and nobody wants it. A blink would also be the one thing that
// repainted the window with nothing typed, which is a redraw per half second
// for the life of the session against a dirty-rect blit that otherwise costs
// nothing at all while the editor is idle.

// rasterCursor maps the screen's idea of a caret onto the rasteriser's.
//
// The two enums have the same four names in the same order, and this is a
// switch anyway: they are owned by different packages for different reasons,
// internal/screen's is a mode fact and internal/raster's is a drawing
// instruction, and a conversion that happens to be a cast is a silent
// wrong caret the day one of them grows a fifth name.
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
