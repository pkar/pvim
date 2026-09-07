package raster

import (
	"image"

	"github.com/pkar/pvim/internal/screen"
)

// painter carries the handful of things every cell needs. It exists so the
// per-cell code is not eight arguments long; it holds nothing between frames.
type painter struct {
	dst  *image.RGBA
	f    Frame
	opt  PaintOptions
	m    Metrics
	rowH int
}

// span paints one run of cells, clipped and widened by clampSpan: screen.Grid.
// Diff widens its own spans to whole double-width pairs, and this widens again
// rather than trusting that, because Paint is also called with spans a caller
// made up and half a glyph on screen is not a failure anybody enjoys
// diagnosing.
func (p *painter) span(sp screen.Span) {
	g := p.f.Grid
	lo, hi, ok := clampSpan(g, sp)
	if !ok {
		return
	}

	// Backgrounds first, coalesced into as few rectangles as the row allows.
	// A screen of ordinary text is one colour from margin to margin, and one
	// fill of a whole row costs a tenth of what 200 fills of one cell do.
	for col := lo; col < hi; {
		bg := p.bg(sp.Row, col)
		start := col
		for col++; col < hi && p.bg(sp.Row, col) == bg; col++ {
		}
		p.fill(p.boxRange(sp.Row, start, col), bg)
	}

	for col := lo; col < hi; col++ {
		if g.At(sp.Row, col).Tail {
			continue // the trailing half of a pair, drawn by its leader
		}
		p.foreground(sp.Row, col)
	}
}

// bg is the background colour of one cell, after 'reverse' has had its say.
func (p *painter) bg(row, col int) screen.RGB {
	hl := p.f.look(p.f.Grid.At(row, col).HL)
	if hl.Attr&screen.AttrReverse != 0 {
		return hl.FG
	}
	return hl.BG
}

// cells reports how many grid columns the cell at row, col occupies: two for a
// double-width rune that has its trailing half on the grid, one otherwise.
func (p *painter) cells(row, col int) int {
	g := p.f.Grid
	if col+1 < g.Cols && g.At(row, col+1).Tail {
		return 2
	}
	return 1
}

// foreground paints the glyph and any drawn decoration of one cell, over a
// background span already filled.
func (p *painter) foreground(row, col int) {
	c := *p.f.Grid.At(row, col)
	hl := p.f.look(c.HL)
	fg := hl.FG
	if hl.Attr&screen.AttrReverse != 0 {
		fg = hl.BG
	}

	box := p.box(row, col, p.cells(row, col))
	p.drawRune(box, c.Rune, hl, fg)
	p.decorate(box, hl, fg)
}

// rune draws one character into an already-filled cell box. A space and a zero
// rune draw nothing: the zero rune is a cell nobody wrote, and drawing .notdef
// for it would fill a fresh grid with boxes.
func (p *painter) drawRune(box image.Rectangle, r rune, hl screen.Highlight, fg screen.RGB) {
	if r <= ' ' {
		return
	}
	mask, off := p.opt.Face.Glyph(r,
		hl.Attr&screen.AttrBold != 0, hl.Attr&screen.AttrItalic != 0)
	p.glyph(box, mask, off, fg)
}

// boxRange is the pixel rectangle of columns c0 up to but not including c1.
func (p *painter) boxRange(row, c0, c1 int) image.Rectangle {
	return p.box(row, c0, c1-c0)
}

// box is the pixel rectangle of a run of cells on one row, including the
// 'linespace' padding below the glyphs.
func (p *painter) box(row, col, cells int) image.Rectangle {
	o := p.dst.Bounds().Min
	x := o.X + col*p.m.CellW
	y := o.Y + row*p.rowH
	return image.Rect(x, y, x+cells*p.m.CellW, y+p.rowH)
}

// fill paints a solid rectangle, clipped to dst.
//
// One pixel is written by hand and everything after it is copy, which is the
// memmove the runtime already has: the first scanline doubles out of its own
// first four bytes and every later scanline is a copy of that one. A 200x60
// screen at retina scale is 6.7 million pixels of background per frame, and
// writing the first scanline a pixel at a time instead of doubling it costs
// 0.2 ms of the 3.9 ms that frame takes.
func (p *painter) fill(r image.Rectangle, c screen.RGB) {
	r = r.Intersect(p.dst.Bounds())
	if r.Empty() {
		return
	}
	i0 := p.dst.PixOffset(r.Min.X, r.Min.Y)
	first := p.dst.Pix[i0 : i0+r.Dx()*4]
	first[0], first[1], first[2], first[3] = c.R, c.G, c.B, 0xff
	for n := 4; n < len(first); n *= 2 {
		copy(first[n:], first[:n])
	}
	for y := r.Min.Y + 1; y < r.Max.Y; y++ {
		i := p.dst.PixOffset(r.Min.X, y)
		copy(p.dst.Pix[i:i+len(first)], first)
	}
}

// glyph composites a coverage mask in colour fg over whatever is already in the
// cell.
//
// It clips to the cell box and not to dst, which is what makes a PaintRects of
// a span produce the same pixels as a Paint of the whole screen: a glyph that
// could bleed into the cell beside it would look different depending on whether
// that cell happened to be repainted afterwards, and the goldens would stop
// being reproducible.
//
// Monaco's own glyphs stay inside their advance and lose nothing to that clip.
// A synthesised bold or italic does not, because a smear and a shear both come
// out wider than the glyph they are made from, and what leans past the advance
// is cut off here rather than drawn over the neighbour.
// TestSyntheticStylesStayInsideTheirCell measures how much: nothing plain, 1%
// of bold ink, 2% of italic, 8% of both together, and 21% of the worst rune
// there is, which is a bold italic 'M' at retina scale.
func (p *painter) glyph(box image.Rectangle, mask *image.Alpha, off image.Point, fg screen.RGB) {
	if mask == nil || mask.Rect.Empty() {
		return
	}
	clip := box.Intersect(p.dst.Bounds())
	if clip.Empty() {
		return
	}
	w, h := mask.Rect.Dx(), mask.Rect.Dy()

	// The x range is clipped once, outside the loop, because it is the same for
	// every scanline and the inner loop runs once per ink pixel per cell per
	// frame, which is the hottest loop in the editor.
	x0, x1 := 0, w
	if left := clip.Min.X - (box.Min.X + off.X); left > x0 {
		x0 = left
	}
	if right := clip.Max.X - (box.Min.X + off.X); right < x1 {
		x1 = right
	}
	if x0 >= x1 {
		return
	}

	// This is the hottest loop in the editor: about three million mask pixels
	// for a full 200x60 repaint at retina scale. The destination scanline is
	// sliced once and indexed four bytes at a time so the compiler can drop the
	// bounds checks; done the obvious way it is half again as slow.
	for j := 0; j < h; j++ {
		y := box.Min.Y + off.Y + j
		if y < clip.Min.Y || y >= clip.Max.Y {
			continue
		}
		row := mask.Pix[j*mask.Stride+x0 : j*mask.Stride+x1]
		o := p.dst.PixOffset(box.Min.X+off.X+x0, y)
		line := p.dst.Pix[o : o+4*len(row) : o+4*len(row)]
		for i, a := range row {
			if a == 0 {
				// Most of a glyph's bounding box is empty. Skipping it is the
				// difference between blending the ink and blending the box.
				continue
			}
			d := line[i*4 : i*4+4 : i*4+4]
			if a == 0xff {
				d[0], d[1], d[2], d[3] = fg.R, fg.G, fg.B, 0xff
				continue
			}
			d[0] = blend(d[0], fg.R, a)
			d[1] = blend(d[1], fg.G, a)
			d[2] = blend(d[2], fg.B, a)
			d[3] = 0xff
		}
	}
}

// blend mixes src over dst at coverage a, rounded, in integer arithmetic.
//
// No gamma correction and no premultiplied intermediate: text at 13pt on a dark
// background comes out a shade lighter than CoreText's and every pixel is an
// exact function of its inputs, which is the trade the goldens are built on.
func blend(dst, src, a uint8) uint8 {
	return uint8((uint32(src)*uint32(a) + uint32(dst)*uint32(255-a) + 127) / 255)
}

// decorate draws the parts of a highlight that are lines rather than colours.
//
// Vim calls the colour 'guisp' and falls back to the foreground where it is
// unset. screen.Highlight has no unset, so a zero SP means fall back, which
// costs the ability to ask for a pure black undercurl and is worth it for never
// carrying a second bool per highlight.
//
// Undercurl wins over underline when a group asks for both, the way vim's own
// GUI draws whichever it drew last rather than two lines in the same place.
//
// screen.AttrStandout is not drawn. Vim's GUI renders standout as reverse
// video, and the two colourschemes this editor has to run set `gui=reverse`
// where they mean it, so honouring the bit would only ever differ from vim on a
// group nothing defines.
func (p *painter) decorate(box image.Rectangle, hl screen.Highlight, fg screen.RGB) {
	const drawn = screen.AttrUnderline | screen.AttrUndercurl | screen.AttrStrikethrough
	if hl.Attr&drawn == 0 {
		return
	}
	sp := hl.SP
	if sp == (screen.RGB{}) {
		sp = fg
	}
	thick := p.m.Scale

	switch {
	case hl.Attr&screen.AttrUndercurl != 0:
		p.undercurl(box, sp, thick)
	case hl.Attr&screen.AttrUnderline != 0:
		y := box.Min.Y + p.m.Ascent + thick
		p.fill(image.Rect(box.Min.X, y, box.Max.X, y+thick).Intersect(box), sp)
	}
	if hl.Attr&screen.AttrStrikethrough != 0 {
		y := box.Min.Y + p.m.Ascent*2/3
		p.fill(image.Rect(box.Min.X, y, box.Max.X, y+thick).Intersect(box), sp)
	}
}

// undercurl draws the wavy underline a spelling error gets.
//
// The wave is a four-step triangle and not a sine: at one device pixel of
// amplitude per scale step there is nothing on screen to tell them apart, and a
// table of four integers cannot drift between two runs. Its phase comes from
// the absolute x coordinate, so the curls under a misspelled word join up
// across cell boundaries instead of restarting at every one.
func (p *painter) undercurl(box image.Rectangle, sp screen.RGB, thick int) {
	steps := []int{0, 1, 2, 1}
	top := box.Min.Y + p.m.Ascent + thick
	clip := box.Intersect(p.dst.Bounds())

	for x := clip.Min.X; x < clip.Max.X; x++ {
		// The phase runs off the image origin and not off the cell, so the
		// curls under a misspelled word join across cell boundaries; off the
		// origin and not off dst's absolute coordinates, so painting into a
		// sub-image of a larger buffer draws the same wave.
		y := top + steps[((x-p.dst.Bounds().Min.X)/p.m.Scale)%len(steps)]*p.m.Scale
		p.fill(image.Rect(x, y, x+1, y+thick).Intersect(box), sp)
	}
}

// cursor draws the caret over the cell the editor put it on.
//
// The colour is the cell's own foreground rather than a Cursor highlight group,
// because a block caret in vim is reverse video and nofrils-dark's Cursor group
// amounts to the same thing. When there is a real Cursor group to read, it goes
// in Frame beside the shape and this reads it instead.
func (p *painter) cursor() {
	g := p.f.Grid
	row, col := p.f.CursorRow, p.f.CursorCol
	if !g.InBounds(row, col) {
		return
	}
	// A caret parked on the trailing half of a wide pair belongs to the leader.
	if g.At(row, col).Tail && col > 0 {
		col--
	}
	c := *g.At(row, col)
	hl := p.f.look(c.HL)
	fg, bg := hl.FG, hl.BG
	if hl.Attr&screen.AttrReverse != 0 {
		fg, bg = bg, fg
	}
	box := p.box(row, col, p.cells(row, col))
	thick := 2 * p.m.Scale

	switch p.f.Cursor {
	case CursorBlock:
		p.fill(box, fg)
		p.drawRune(box, c.Rune, hl, bg)
	case CursorBar:
		p.fill(image.Rect(box.Min.X, box.Min.Y, box.Min.X+thick, box.Max.Y), fg)
	case CursorUnderline:
		p.fill(image.Rect(box.Min.X, box.Max.Y-thick, box.Max.X, box.Max.Y), fg)
	case CursorHollow:
		t := p.m.Scale
		p.fill(image.Rect(box.Min.X, box.Min.Y, box.Max.X, box.Min.Y+t), fg)
		p.fill(image.Rect(box.Min.X, box.Max.Y-t, box.Max.X, box.Max.Y), fg)
		p.fill(image.Rect(box.Min.X, box.Min.Y, box.Min.X+t, box.Max.Y), fg)
		p.fill(image.Rect(box.Max.X-t, box.Min.Y, box.Max.X, box.Max.Y), fg)
	}
}
