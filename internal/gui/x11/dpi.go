package x11

import (
	"strconv"
	"strings"
)

// The display scale, which X11 does not have.
//
// AppKit has backingScaleFactor: a window is on a display, the display is
// retina or it is not, and the answer is 1 or 2. X11 has no such concept at
// all. A HiDPI X desktop is a desktop where every toolkit was told, separately,
// to draw bigger, and the place they were told is the X resource database:
// Xft.dpi on the root window's RESOURCE_MANAGER property, set by the desktop
// environment or by the user's ~/.Xresources.
//
// So this package's scale is Xft.dpi divided by the 96 that X calls normal, and
// where there is no Xft.dpi the scale is 1 and 'guifont' height is the only
// knob, which is how X11 has always worked. The parsing is here, with no build
// tag, because a resource database is a string and reading one wrong is the
// difference between a readable window and one at half size.

// defaultDPI is what X calls a normal display and what every desktop that has
// not been told otherwise assumes.
const defaultDPI = 96

// xftDPI reads Xft.dpi out of an X resource database.
//
// The format is one resource per line, `name:` then whitespace then the value,
// with comments starting `!`. It is not INI and it is not properties: the
// separator is a colon, the value runs to the end of the line, and leading
// whitespace on the value is not part of it. A resource named anything else,
// including XTerm*dpi and Xft.dpi with a wildcard in front of it, is not this
// one and is skipped, because a wildcard binding is a match rule and matching
// them properly is the resource manager, which is not being written here.
//
// The value may be a float. `Xft.dpi: 96.0` is what at least one desktop
// writes, so it is parsed as one and truncated.
func xftDPI(resources string) (int, bool) {
	for _, line := range strings.Split(resources, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "!") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != "Xft.dpi" {
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || f <= 0 {
			continue
		}
		return int(f), true
	}
	return 0, false
}

// scaleForDPI turns a dots-per-inch figure into the integer scale
// internal/raster builds a face at.
//
// Integer, and clamped to 1 or 2, because a raster.Face is built at an integer
// scale and there is no fractional path through the rasteriser. A 144-dpi
// desktop, which is the 1.5 scaling several laptops ship with, therefore
// rounds to 2 and gets glyphs rasterised twice as large as the layout wants;
// the fix a person has for that is 'guifont' and it is the same fix they use
// for every other X11 application that only does integer scaling.
//
// The rounding is to nearest rather than down, so 120 dpi (1.25) is 1 and 144
// (1.5) is 2.
func scaleForDPI(dpi int) int {
	if dpi <= 0 {
		return 1
	}
	s := (dpi + defaultDPI/2) / defaultDPI
	if s < 1 {
		return 1
	}
	if s > 2 {
		return 2
	}
	return s
}
