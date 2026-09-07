package raster

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"image"
	"math"

	"github.com/pkar/pvim/internal/screen"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// glyphKey is the glyph cache key. Scale is not in it because a face is one
// scale: two scales are two faces with two caches, which is what a display
// change or a `:set guifont=` produces anyway.
type glyphKey struct {
	r      rune
	bold   bool
	italic bool
}

// cachedGlyph is a rasterised mask and where it sits relative to the cell's
// top-left corner. The offset is often negative in X for a glyph that leans
// out of its advance box, which for Monaco is only the punctuation.
type cachedGlyph struct {
	mask *image.Alpha
	off  image.Point
}

// face is the one real Face: a font at a size at a scale, with a glyph cache.
type face struct {
	f       *opentype.Font
	inner   font.Face
	metrics Metrics
	id      uint64
	cache   map[glyphKey]cachedGlyph

	// embolden is the smear distance in device pixels for synthetic bold, and
	// shear the italic slope numerator over 16. Both scale with the face so a
	// retina face is not a thin one.
	embolden int
}

// Face returns a face for this font at pointSize points and the given display
// scale.
//
// The face rasterises at pointSize*scale, so scale 2 is a genuine 26pt
// rendering rather than a doubled 13pt one, which is the whole reason retina
// text looks like text.
func (f *Font) Face(pointSize float64, scale int) (Face, error) {
	if scale < 1 {
		return nil, fmt.Errorf("raster: scale %d must be at least 1", scale)
	}
	if pointSize <= 0 {
		return nil, fmt.Errorf("raster: point size %v must be positive", pointSize)
	}
	// DPI 72 makes one point one pixel, so the size in points times the scale
	// is the size in device pixels and there is no second unit to get wrong.
	// Hinting is off: see the package comment.
	inner, err := opentype.NewFace(f.sf, &opentype.FaceOptions{
		Size:    pointSize * float64(scale),
		DPI:     72,
		Hinting: font.HintingNone,
	})
	if err != nil {
		return nil, fmt.Errorf("raster: making a face: %w", err)
	}

	adv, ok := inner.GlyphAdvance('M')
	if !ok || adv <= 0 {
		return nil, fmt.Errorf("raster: %s has no advance for 'M'; not a text font", f.path)
	}
	fm := inner.Metrics()

	fc := &face{
		f:     f.sf,
		inner: inner,
		id:    faceID(f.path, pointSize, scale),
		cache: map[glyphKey]cachedGlyph{},
		metrics: Metrics{
			// Ceil, not Round: a cell narrower than the advance overlaps its
			// neighbour and the overlap is what a golden diff looks like at
			// column 200. Monaco at 13pt is 7.80 wide and 8 px per cell, which
			// is the number MacVim puts on screen for Monaco:h13.
			CellW: adv.Ceil(),
			// Height is ascent+descent+lineGap, which is the font's own idea
			// of how far apart two baselines go. 'linespace' is added on top by
			// the painter and is not folded in here.
			CellH:  fm.Height.Ceil(),
			Ascent: fm.Ascent.Ceil(),
			Scale:  scale,
		},
		embolden: scale,
	}
	return fc, nil
}

// Metrics implements Face.
func (f *face) Metrics() Metrics { return f.metrics }

// ID implements Face.
func (f *face) ID() uint64 { return f.id }

// faceID hashes the three things that decide what a face draws: the file, the
// size in points and the display scale. Everything else about a face is derived
// from those, so two faces agreeing on all three agree on every pixel.
//
// A hash and not a serial, so that rebuilding the same face -- which is what a
// `:set guifont=` back to what it already was does -- is not a full repaint.
// The one thing it cannot see is the same path holding a different file than it
// did, which needs a font to be replaced on disk while pvim is running.
func faceID(path string, pointSize float64, scale int) uint64 {
	h := fnv.New64a()
	h.Write([]byte(path))
	// Fixed-width and last, so no path can be confused with a shorter path
	// plus the front of a size.
	var b [16]byte
	binary.LittleEndian.PutUint64(b[0:8], math.Float64bits(pointSize))
	binary.LittleEndian.PutUint64(b[8:16], uint64(scale))
	h.Write(b[:])
	return h.Sum64()
}

// Width implements Face.
func (f *face) Width(r rune) int { return screen.RuneWidth(r) }

// Glyph implements Face. The returned mask belongs to the cache and must not be
// written to.
func (f *face) Glyph(r rune, bold, italic bool) (*image.Alpha, image.Point) {
	k := glyphKey{r: r, bold: bold, italic: italic}
	if g, ok := f.cache[k]; ok {
		return g.mask, g.off
	}

	g := f.rasterise(r)
	if bold {
		g = embolden(g, f.embolden)
	}
	if italic {
		g = shear(g, f.metrics.Ascent)
	}
	f.cache[k] = g
	return g.mask, g.off
}

// rasterise draws one plain glyph at the origin of a cell.
//
// The dot is placed at integer pixel (0, Ascent), so the mask's rectangle comes
// back already in cell coordinates and there is no sub-pixel positioning to
// make two identical cells differ. A rune the font has no glyph for rasterises
// glyph 0, the .notdef box, which is exactly the behaviour we want and is why
// the ok result is ignored.
func (f *face) rasterise(r rune) cachedGlyph {
	dr, mask, maskp, _, _ := f.inner.Glyph(fixed.P(0, f.metrics.Ascent), r)
	src, ok := mask.(*image.Alpha)
	if !ok || dr.Empty() {
		return cachedGlyph{mask: &image.Alpha{}, off: image.Point{}}
	}

	// The face reuses one mask buffer between calls, so the cache gets a copy.
	w, h := dr.Dx(), dr.Dy()
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		si := src.PixOffset(maskp.X, maskp.Y+y)
		copy(out.Pix[y*out.Stride:y*out.Stride+w], src.Pix[si:si+w])
	}
	return cachedGlyph{mask: out, off: dr.Min}
}

// embolden fakes a bold face by drawing the glyph over itself, offset right by
// d pixels and taking the darker of the two.
//
// Monaco ships one weight and macOS has no Monaco Bold on disk, so the choice
// is this or bold text that is not bold. Maximum rather than a saturating sum
// because a sum turns the antialiased edges into a fringe.
func embolden(g cachedGlyph, d int) cachedGlyph {
	if g.mask == nil || g.mask.Rect.Empty() || d <= 0 {
		return g
	}
	w, h := g.mask.Rect.Dx(), g.mask.Rect.Dy()
	out := image.NewAlpha(image.Rect(0, 0, w+d, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := g.mask.Pix[y*g.mask.Stride+x]
			for _, ox := range []int{0, d} {
				o := y*out.Stride + x + ox
				if out.Pix[o] < a {
					out.Pix[o] = a
				}
			}
		}
	}
	return cachedGlyph{mask: out, off: g.off}
}

// shear fakes an italic face by sliding each scanline sideways in proportion to
// its distance above the baseline: a 1-in-5 slope, which is close enough to the
// 12 degrees a real oblique uses that nobody has ever measured it.
func shear(g cachedGlyph, ascent int) cachedGlyph {
	if g.mask == nil || g.mask.Rect.Empty() {
		return g
	}
	w, h := g.mask.Rect.Dx(), g.mask.Rect.Dy()

	// One pass to find how far the extreme rows move, so the new mask is
	// exactly as wide as it needs to be and the offset stays honest.
	lo, hi := 0, 0
	for y := 0; y < h; y++ {
		d := shift(g.off.Y+y, ascent)
		if d < lo {
			lo = d
		}
		if d > hi {
			hi = d
		}
	}

	out := image.NewAlpha(image.Rect(0, 0, w+hi-lo, h))
	for y := 0; y < h; y++ {
		d := shift(g.off.Y+y, ascent) - lo
		copy(out.Pix[y*out.Stride+d:y*out.Stride+d+w], g.mask.Pix[y*g.mask.Stride:y*g.mask.Stride+w])
	}
	return cachedGlyph{mask: out, off: image.Point{X: g.off.X + lo, Y: g.off.Y}}
}

// shift is the italic slope: how far right the scanline at cell-space y moves,
// zero on the baseline and negative below it. Integer division rounding toward
// zero is fine here and is what keeps two runs identical.
func shift(y, ascent int) int {
	return (ascent - y) / 5
}
