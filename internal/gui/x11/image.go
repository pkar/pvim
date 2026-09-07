package x11

import (
	"fmt"
	"image"
	"math/bits"
)

// Pixels, from the RGBA buffer internal/raster paints into to the bytes an X
// server wants, and the chunking that keeps each PutImage inside the length the
// protocol can express.
//
// Nothing here talks to a connection, which is why it has no build tag: it is
// two integer loops and a rectangle split, and every one of the ways it can be
// wrong is visible in a test on a machine with no X server. The ways it can be
// wrong are worth listing, because two of them are silent.
//
// # The truncation
//
// A core-protocol request carries its length in a uint16, in units of four
// bytes, so no request may exceed 262140 bytes and the server's setup says how
// much less than that it will take. xgb writes that field as
// `xgb.Put16(buf[2:], uint16(size/4))` and does not check it. Hand
// xproto.PutImage a megabyte of pixels and the length wraps, the server reads
// the next request out of the middle of the image data, and the connection dies
// with an error about an opcode nobody sent. So chunking is not an
// optimisation, it is the difference between a window and a protocol error, and
// chunkRect below is the whole of it.
//
// BIG-REQUESTS does not help. xgb ships a bigreq package, but the extension
// changes the encoding of every request that uses it and xgb's generated
// request writers do not know about it, so enabling it buys a larger declared
// maximum that nothing in this dependency can actually send.
//
// # The byte order
//
// image.RGBA holds R, G, B, A in that order in memory. A 32-bit TrueColor X
// visual holds a pixel value whose red, green and blue live wherever the
// visual's masks say, written to the wire in the server's image-byte-order.
// On every little-endian desktop that is masks of 0xff0000, 0x00ff00, 0x0000ff
// and LSBFirst, which puts B, G, R, unused in memory: the exact reverse of the
// first three bytes. Getting it wrong gives a window with the red and blue
// channels swapped, which on this editor's colourscheme is a blue-grey
// background instead of a grey one and is easy to look straight past.

// pixelFormat is everything about a server's ZPixmap layout that this package
// has to honour: where the three components live in a pixel value, and which
// way round the value goes on the wire.
//
// depth and bitsPerPixel are separate because they are separate on the wire: a
// depth-24 visual is almost always carried in 32 bits per pixel with the top
// eight ignored, and PutImage takes the depth while the data is laid out at the
// bits per pixel of the matching pixmap format.
type pixelFormat struct {
	depth        byte
	bitsPerPixel byte
	// msbFirst is the setup's image-byte-order: false is LSBFirst, which is
	// every desktop x86 and arm64 machine.
	msbFirst  bool
	redMask   uint32
	greenMask uint32
	blueMask  uint32
}

// checkFormat refuses a visual this package cannot paint into, with a sentence
// saying which one it got.
//
// Only 32 bits per pixel is supported, which is depth 24 and depth 32 on every
// desktop shipped this century. A 16-bit visual is a real thing on a very old
// server and on some VNC configurations, and the missing code is a second
// conversion loop rather than a design problem; it is refused rather than
// half-done, because a wrong shift on a 5-6-5 visual produces a window that
// looks nearly right and is not.
func checkFormat(f pixelFormat) error {
	if f.bitsPerPixel != 32 {
		return fmt.Errorf("x11: this backend needs a 32-bits-per-pixel visual, the server offered %d at depth %d",
			f.bitsPerPixel, f.depth)
	}
	if f.redMask == 0 || f.greenMask == 0 || f.blueMask == 0 {
		return fmt.Errorf("x11: the visual is not TrueColor: red, green and blue masks are %#x, %#x, %#x",
			f.redMask, f.greenMask, f.blueMask)
	}
	return nil
}

// isBGRA reports whether f is the layout every ordinary desktop has, in which
// case the conversion is a byte swap rather than three shifts.
func (f pixelFormat) isBGRA() bool {
	return !f.msbFirst && f.redMask == 0xff0000 && f.greenMask == 0x00ff00 && f.blueMask == 0x0000ff
}

// shiftOf returns how far left an 8-bit component has to move to land in mask,
// and how many bits of it survive.
//
// A mask narrower than eight bits keeps the high bits and drops the low ones,
// which is what every X client does and what makes a 5-bit red channel a
// slightly coarse red rather than a wrapped one.
func shiftOf(mask uint32) (shift, width uint) {
	if mask == 0 {
		return 0, 0
	}
	return uint(bits.TrailingZeros32(mask)), uint(bits.OnesCount32(mask))
}

// scaleComponent fits an 8-bit colour component into a channel of the given
// width.
//
// Narrower than eight bits keeps the high bits, which is every X client's
// answer and makes a 5-bit red slightly coarse rather than wrapped. Wider than
// eight, which is a depth-30 visual on a colour-managed display, shifts up and
// leaves the low bits clear: the result is very slightly dark rather than
// wrong, and this rasteriser has no more precision to give it anyway.
func scaleComponent(c byte, width uint) uint32 {
	switch {
	case width == 0:
		return 0
	case width >= 8:
		return uint32(c) << (width - 8)
	default:
		return uint32(c) >> (8 - width)
	}
}

// convert writes the pixels of src inside r as f's ZPixmap bytes into dst.
//
// dst must be at least r.Dx()*r.Dy()*4 bytes. r must be inside src.Bounds();
// a caller that clipped wrongly gets an error rather than a panic, because the
// caller that clips wrongly is a resize race and a panic there is an editor
// gone rather than a frame missed.
//
// The alpha channel is not written as alpha. A depth-24 visual has no alpha and
// the fourth byte is padding the server ignores; a depth-32 visual on a
// compositing desktop reads it as opacity, so it is set to 0xff, which is what
// makes the window opaque instead of showing the desktop through the text.
func convert(dst []byte, src *image.RGBA, r image.Rectangle, f pixelFormat) error {
	if !r.In(src.Bounds()) {
		return fmt.Errorf("x11: rect %v is not inside the buffer's %v", r, src.Bounds())
	}
	w, h := r.Dx(), r.Dy()
	if need := w * h * 4; len(dst) < need {
		return fmt.Errorf("x11: destination is %d bytes, need %d for %dx%d", len(dst), need, w, h)
	}
	if w == 0 || h == 0 {
		return nil
	}

	rs, rw := shiftOf(f.redMask)
	gs, gw := shiftOf(f.greenMask)
	bs, bw := shiftOf(f.blueMask)
	fast := f.isBGRA()

	o := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := src.Pix[src.PixOffset(r.Min.X, y):][:w*4]
		if fast {
			for x := 0; x < w*4; x += 4 {
				dst[o+0] = row[x+2]
				dst[o+1] = row[x+1]
				dst[o+2] = row[x+0]
				dst[o+3] = 0xff
				o += 4
			}
			continue
		}
		for x := 0; x < w*4; x += 4 {
			v := scaleComponent(row[x+0], rw)<<rs |
				scaleComponent(row[x+1], gw)<<gs |
				scaleComponent(row[x+2], bw)<<bs
			// Every bit outside the three masks set. On a depth-24 visual
			// those bits are padding the server ignores; on a depth-32 one the
			// top eight are alpha, and an opaque window is the one this editor
			// wants -- a compositor showing the desktop through the text is
			// not a feature anybody asked for.
			v |= ^(f.redMask | f.greenMask | f.blueMask)
			if f.msbFirst {
				dst[o+0] = byte(v >> 24)
				dst[o+1] = byte(v >> 16)
				dst[o+2] = byte(v >> 8)
				dst[o+3] = byte(v)
			} else {
				dst[o+0] = byte(v)
				dst[o+1] = byte(v >> 8)
				dst[o+2] = byte(v >> 16)
				dst[o+3] = byte(v >> 24)
			}
			o += 4
		}
	}
	return nil
}

// putImageHeader is the fixed part of a PutImage request, which comes out of
// the budget before any pixels do.
const putImageHeader = 24

// maxImageBytes is how many bytes of pixel data one PutImage may carry, given
// the server's maximum-request-length in four-byte units.
//
// The result is floored to a multiple of four because a request's length is in
// four-byte units and xgb pads the data to that anyway, and it is never
// negative: a server declaring a maximum smaller than the header is not a
// server, and the caller gets a zero it will refuse.
func maxImageBytes(maxRequestLen uint16) int {
	n := int(maxRequestLen)*4 - putImageHeader
	if n < 0 {
		return 0
	}
	return n &^ 3
}

// chunkRect splits r into pieces whose pixel data fits in maxBytes.
//
// Rows first, because a band of whole rows is one PutImage with no left-pad
// arithmetic and because a text row is the unit everything above this works in.
// Columns only when a single row of r does not fit at all, which needs a window
// more than 65529 pixels wide on a server with the largest maximum the protocol
// can express, and is here because the alternative to handling it is a silent
// truncation.
//
// A zero or negative maxBytes, or an empty r, gives no chunks at all, which the
// caller reports rather than sending.
func chunkRect(r image.Rectangle, maxBytes int) []image.Rectangle {
	if r.Empty() || maxBytes < 4 {
		return nil
	}
	const bpp = 4
	rowBytes := r.Dx() * bpp

	if rowBytes <= maxBytes {
		perChunk := maxBytes / rowBytes
		var out []image.Rectangle
		for y := r.Min.Y; y < r.Max.Y; y += perChunk {
			end := y + perChunk
			if end > r.Max.Y {
				end = r.Max.Y
			}
			out = append(out, image.Rect(r.Min.X, y, r.Max.X, end))
		}
		return out
	}

	// One row is already too wide. Split into vertical strips narrow enough
	// that a single row of each fits, then let each strip be one row at a time.
	perStrip := maxBytes / bpp
	var out []image.Rectangle
	for x := r.Min.X; x < r.Max.X; x += perStrip {
		endX := x + perStrip
		if endX > r.Max.X {
			endX = r.Max.X
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			out = append(out, image.Rect(x, y, endX, y+1))
		}
	}
	return out
}

// spanRect turns one screen.Span-shaped span of cells into the pixel rectangle
// that covers it, clipped to the buffer.
//
// The arguments are integers rather than a screen.Span so that this file needs
// no import of internal/screen and so that the arithmetic is visible: a span is
// a run of cells on one row, a cell is cellW wide, a row is rowH tall, and the
// rectangle is the product. The clip is not defensive tidiness: a Draw racing a
// resize can hand a span from a grid one column wider than the buffer that was
// allocated for it, and a PutImage past the end of the buffer is a panic in the
// conversion loop.
func spanRect(row, col, length, cellW, rowH, maxW, maxH int) image.Rectangle {
	if length <= 0 || cellW <= 0 || rowH <= 0 {
		return image.Rectangle{}
	}
	r := image.Rect(col*cellW, row*rowH, (col+length)*cellW, (row+1)*rowH)
	return r.Intersect(image.Rect(0, 0, maxW, maxH))
}
