// Package raster turns a grid of cells into pixels: one glyph per cell, no
// shaping, no fallback font, deterministic output so a sha256 of the result is
// a test. It knows about fonts and images and nothing about windows, which is
// why internal/gui may import it and it may never import internal/gui.
//
// # What this rasteriser deliberately does not do
//
// Every one of these was accepted before a line was written, so they are
// settled rather than open:
//
// - No hinting. Unhinted grayscale AA at retina scale is indistinguishable
// from CoreText for a monospace face at 13pt; at scale 1 on a non-retina
// display it is softer, and that is a known cost, not a bug to chase.
// - No subpixel antialiasing and no gamma-corrected blending. Coverage is
// blended straight in sRGB space, which is what makes every output pixel an
// integer function of the mask.
// - No shaping. One rune, one glyph, one cell. A double-width rune takes two
// cells and is positioned by its own advance; a combining mark never
// reaches here, because a screen.Cell holds one rune and drops them.
// - No fallback chain. A rune Monaco has no glyph for renders as Monaco's own
// .notdef box, so emoji in a commit message is a visible box rather than a
// blank. A second face is a second sfnt.Font and a lookup per miss; it goes
// in the day a real file needs it, not before.
// - No kerning and no ligatures. A monospace grid has nowhere to put them.
// - Bold and italic are synthesised, by smearing and by shearing, because
// Monaco.ttf is one face and macOS ships no Monaco Bold on disk.
// TestMonacoHasNoBoldOrItalicFace is that measurement rather than that
// assumption, and it fails the day Apple ships a second weight. Both come
// out wider than the glyph they are made from and both are clipped to the
// cell, which costs a synthesised glyph a few percent of its ink and buys
// the property the dirty-rect path is built on; see blit.go's glyph.
// - No underline position out of the font. The line goes one device pixel
// per scale step below the baseline; Monaco's post table asks for half of
// one at 13pt. Integer positions are what keep two runs identical.
//
// # Painting less than the whole window
//
// Paint is PaintRects over every row, and PaintRects is what a frontend calls
// once it knows what changed. Working that out is damage.go: a Stamp over
// everything that changes pixels without changing cells, and a Damage that
// answers with screen.Grid.Diff when the stamp still holds and with every row
// when it does not. The property that makes it safe is that a span is a pure
// function of the frame -- painting one cell cannot depend on what was painted
// beside it or when -- which TestPaintRectsCellByCellMatchesPaint pins by
// painting a whole frame one cell at a time, in both directions, and demanding
// the bytes of a single full Paint.
//
// # Determinism
//
// Same frame in, byte-identical pixels out, on this architecture, forever.
// Every coordinate is an integer, every blend is integer arithmetic, and the
// only floating point is inside x/image's own rasteriser, which is fed
// integer-quantised dots. That is what lets the tests hash the RGBA bytes: a
// golden that changes because Apple shipped a new Monaco.ttf is the test doing
// its job, and TestMonacoIsTheFontWeThinkItIs fails first and loudly so nobody
// spends the afternoon on the hash instead of on the font.
package raster

import (
	"fmt"
	"image"

	"github.com/pkar/pvim/internal/screen"
)

// Metrics are the pixel dimensions of one cell for a particular face.
//
// All four are device pixels, already multiplied by Scale, because every caller
// is either measuring a window or writing into an RGBA buffer and neither of
// those deals in points. A window 80 columns wide is 80*CellW pixels; divide by
// Scale for the logical points AppKit wants.
type Metrics struct {
	CellW  int // horizontal advance, the same for every single-width rune
	CellH  int // baseline to baseline, before 'linespace' is added
	Ascent int // baseline measured down from the top of the cell
	Scale  int // 1 on a plain display, 2 at retina backingScaleFactor
}

// Face is one font at one size and one scale, with its glyphs cached.
//
// It is an interface only so that tests can supply a fixed-metric fake and skip
// loading Monaco; there is exactly one real implementation. A fake owes an ID
// as well as metrics, because a fake that returned a constant one would put the
// bug ID exists to stop back into whatever it is standing in for.
//
// A Face is not safe for concurrent use. The editor goroutine paints and
// nothing else does.
type Face interface {
	// Metrics returns the cell box every glyph from this face is drawn into.
	Metrics() Metrics

	// ID identifies the rasterisation and not the box it fits in: two faces
	// with the same ID draw every rune identically, and two with different
	// IDs may not.
	//
	// The metrics cannot answer that. Monaco at 13pt and at 12.8pt come out
	// 8x18 with a 13px ascent at scale 1 and 16x35 with a 26px ascent at
	// scale 2 -- the same four integers, both times -- while the glyphs
	// differ, because one is rasterised at 25.6px and the other at 26. Eight
	// of the faces on a stock macOS box share 11x16 ascent 13 at 13pt. So a
	// `:set guifont=` between any such pair changes every pixel on screen and
	// nothing in Metrics, and raster.StampOf hashes this instead of trusting
	// them; without it the window keeps the old glyphs until each cell
	// happens to change on its own.
	//
	// It is not an address: a face rebuilt from the same file at the same
	// size and scale gets the same ID and does not cost a repaint.
	ID() uint64

	// Glyph returns the coverage mask for r and the offset of that mask's
	// top-left corner from the top-left of the cell. It never returns nil: a
	// rune the font has no glyph for comes back as the font's .notdef box, so
	// a missing character is visible rather than silently blank.
	//
	// bold and italic ask for the synthesised variants. They are part of the
	// cache key, so asking twice for the same styled rune costs one map lookup.
	Glyph(r rune, bold, italic bool) (*image.Alpha, image.Point)

	// Width reports how many cells r occupies. It is screen.RuneWidth, reached
	// through the face so that a test fake can lie about it.
	Width(r rune) int
}

// CursorShape is where the caret is drawn, which vim ties to the mode: block in
// normal, bar in insert, underline in replace, hollow when the window is not
// the focused one.
//
// This lives here rather than in internal/screen because it is a drawing
// instruction and not part of the grid. Blink is not in the list and is not
// coming; that omission is deliberate and is recorded as D-004.
type CursorShape uint8

// The cursor shapes.
const (
	CursorBlock CursorShape = iota
	CursorBar
	CursorUnderline
	CursorHollow
	// CursorNone draws no caret at all, which is what a golden test and a
	// screenshot want.
	CursorNone
)

// Frame is one paintable picture: the cells, the table that says what their
// highlight ids mean, and where the caret goes.
//
// It is a plain struct of things internal/screen already owns rather than a
// screen type of its own, because the rasteriser needs exactly these four
// things and taking them by name keeps it paintable from a test with a grid
// built by hand.
type Frame struct {
	Grid *screen.Grid

	// HL may be nil, in which case every cell renders in screen.DefaultNormal.
	// That is not a convenience: it is what makes a grid built in a test
	// paintable without also building a colourscheme.
	HL *screen.Table

	CursorRow int
	CursorCol int
	Cursor    CursorShape
}

// look resolves a highlight id against the frame's table.
func (f Frame) look(id screen.HLID) screen.Highlight {
	if f.HL == nil {
		return screen.DefaultNormal
	}
	return f.HL.Look(id)
}

// PaintOptions is everything a paint needs that is not in the frame.
//
// Three things a reader looks for here and does not find, because they live
// where the thing that decides them lives. Bold, italic, underline, undercurl
// and reverse are screen.Attr bits on the highlight a cell's id resolves to,
// set by the colourscheme and read per cell in blit.go, so a group can be bold
// and the group beside it not. The cursor shape is Frame.Cursor, because it
// changes with the mode and the mode changes per frame. And the font is Face,
// which is above: a face is one font at one size at one scale with its glyphs
// cached, so changing 'guifont' or moving to a display at another scale builds
// a new one rather than setting a field.
type PaintOptions struct {
	Face Face

	// Linespace is the vimrc's 'linespace', extra device pixels between rows.
	// It is here rather than folded into Metrics.CellH so that changing it does
	// not invalidate the glyph cache. The space goes below the glyph, so the
	// baseline stays at Metrics.Ascent whatever Linespace is.
	Linespace int
}

// RowHeight is the pixel pitch between two rows: the face's cell height plus
// 'linespace'. Callers sizing a buffer want this and not Metrics.CellH.
func (o PaintOptions) RowHeight() int {
	return o.Face.Metrics().CellH + o.Linespace
}

// Size returns the pixel size of a buffer big enough to hold f under opt.
func Size(f Frame, opt PaintOptions) (w, h int) {
	m := opt.Face.Metrics()
	return f.Grid.Cols * m.CellW, f.Grid.Rows * (m.CellH + opt.Linespace)
}

// NewImage allocates an RGBA buffer of exactly the size Paint wants.
//
// A frontend allocates once per resize and keeps the buffer, because a CGImage
// is built over it and reallocating means rebuilding that. This is for tests
// and for the first frame.
func NewImage(f Frame, opt PaintOptions) *image.RGBA {
	w, h := Size(f, opt)
	return image.NewRGBA(image.Rect(0, 0, w, h))
}

// Paint draws the whole frame into dst, which must be at least Size() pixels.
//
// It is a plain function rather than a method on a painter because it holds no
// state between frames: the dirty-rect diffing that makes a scroll cheap
// happens above this, in the frontend, which is the only thing that knows what
// the last frame looked like.
func Paint(dst *image.RGBA, f Frame, opt PaintOptions) error {
	if err := check(dst, f, opt); err != nil {
		return err
	}
	return PaintRects(dst, f, opt, allRows(f.Grid))
}

// PaintRects redraws only the given spans, which is what a frontend does with
// the screen.Grid.Diff of this frame against the last one. Everything outside
// them is left exactly as it was.
//
// The caret is redrawn when a span covers its cell and not otherwise, so a
// frame in which nothing moved costs nothing.
func PaintRects(dst *image.RGBA, f Frame, opt PaintOptions, spans []screen.Span) error {
	if err := check(dst, f, opt); err != nil {
		return err
	}
	m := opt.Face.Metrics()
	p := painter{dst: dst, f: f, opt: opt, m: m, rowH: m.CellH + opt.Linespace}
	for _, sp := range spans {
		p.span(sp)
	}
	if f.Cursor != CursorNone && coversCursor(f, spans) {
		p.cursor()
	}
	return nil
}

// check is the guard both paint entry points run first.
//
// A buffer one pixel short is a resize race in the frontend, and the error says
// both sizes because the useful question is which of the two is stale.
func check(dst *image.RGBA, f Frame, opt PaintOptions) error {
	if opt.Face == nil {
		return fmt.Errorf("raster: PaintOptions.Face is nil")
	}
	if f.Grid == nil {
		return fmt.Errorf("raster: Frame.Grid is nil")
	}
	m := opt.Face.Metrics()
	if m.CellW <= 0 || m.CellH <= 0 {
		return fmt.Errorf("raster: face has an empty cell, %dx%d", m.CellW, m.CellH)
	}
	needW, needH := Size(f, opt)
	if b := dst.Bounds(); b.Dx() < needW || b.Dy() < needH {
		return fmt.Errorf("raster: dst is %dx%d, need %dx%d for %dx%d cells",
			b.Dx(), b.Dy(), needW, needH, f.Grid.Cols, f.Grid.Rows)
	}
	return nil
}

// coversCursor reports whether any span, once it has been clipped and widened
// the way the painter will widen it, covers the cell the caret is drawn on.
//
// It asks about the widened span and not the one the caller passed, because
// painting a cell is what erases a caret already on it: a span that reaches the
// caret's cell only by being widened onto a double-width leader would otherwise
// wipe the caret and not draw it back, and the caret would blink out whenever
// the cell to its right happened to change.
func coversCursor(f Frame, spans []screen.Span) bool {
	col := leaderCol(f.Grid, f.CursorRow, f.CursorCol)
	for _, sp := range spans {
		lo, hi, ok := clampSpan(f.Grid, sp)
		if ok && sp.Row == f.CursorRow && col >= lo && col < hi {
			return true
		}
	}
	return false
}

// clampSpan clips sp to the grid and widens it to whole double-width pairs.
// Half a glyph is not a thing that can be drawn, and a caller that made its own
// spans rather than taking them from Grid.Diff, which widens already, would
// otherwise get one.
func clampSpan(g *screen.Grid, sp screen.Span) (lo, hi int, ok bool) {
	if sp.Row < 0 || sp.Row >= g.Rows || sp.Len <= 0 {
		return 0, 0, false
	}
	lo, hi = sp.Col, sp.Col+sp.Len
	if lo < 0 {
		lo = 0
	}
	if hi > g.Cols {
		hi = g.Cols
	}
	if lo >= hi {
		return 0, 0, false
	}
	if lo > 0 && g.At(sp.Row, lo).Tail {
		lo--
	}
	if hi < g.Cols && g.At(sp.Row, hi).Tail {
		hi++
	}
	return lo, hi, true
}

// leaderCol is the column a caret parked at row, col is actually drawn in: the
// leading half of a double-width pair when col is its trailing half.
func leaderCol(g *screen.Grid, row, col int) int {
	if g.InBounds(row, col) && g.At(row, col).Tail && col > 0 {
		return col - 1
	}
	return col
}
