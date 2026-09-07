package x11

import (
	"image"
	"reflect"
	"testing"
)

// bgra is the layout every ordinary little-endian X desktop has.
var bgra = pixelFormat{depth: 24, bitsPerPixel: 32, redMask: 0xff0000, greenMask: 0x00ff00, blueMask: 0x0000ff}

func TestCheckFormat(t *testing.T) {
	if err := checkFormat(bgra); err != nil {
		t.Fatalf("the ordinary desktop format was refused: %v", err)
	}
	sixteen := bgra
	sixteen.bitsPerPixel = 16
	if err := checkFormat(sixteen); err == nil {
		t.Error("a 16-bit visual was accepted; the conversion loop cannot paint one")
	}
	gray := bgra
	gray.redMask, gray.greenMask, gray.blueMask = 0, 0, 0
	if err := checkFormat(gray); err == nil {
		t.Error("a visual with no colour masks was accepted")
	}
}

// TestConvertBGRA is the one that catches a red and blue swap, which is the
// mistake this file exists to avoid and the one that looks nearly right.
func TestConvertBGRA(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	src.Pix = []byte{
		0x11, 0x22, 0x33, 0xff,
		0x44, 0x55, 0x66, 0xff,
	}
	dst := make([]byte, 8)
	if err := convert(dst, src, src.Bounds(), bgra); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x33, 0x22, 0x11, 0xff, 0x66, 0x55, 0x44, 0xff}
	if !reflect.DeepEqual(dst, want) {
		t.Errorf("converted to %#v, want %#v", dst, want)
	}
}

// TestConvertMatchesAReference checks convert against a second implementation
// written from the protocol rather than from the code, over four visuals: the
// BGRA one that takes the shortcut, an RGBA one that does not, a big-endian
// one, and a narrow-channel one. A shortcut that agrees with nothing is a
// shortcut nobody would notice was wrong.
func TestConvertMatchesAReference(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 5, 3))
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7)
	}
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = 0xff
	}

	for _, tc := range []struct {
		name string
		f    pixelFormat
	}{
		{"the ordinary desktop visual", bgra},
		{"red and blue the other way round", pixelFormat{depth: 24, bitsPerPixel: 32,
			redMask: 0x0000ff, greenMask: 0x00ff00, blueMask: 0xff0000}},
		{"big-endian", pixelFormat{depth: 24, bitsPerPixel: 32, msbFirst: true,
			redMask: 0xff0000, greenMask: 0x00ff00, blueMask: 0x0000ff}},
		{"narrow channels", pixelFormat{depth: 16, bitsPerPixel: 32,
			redMask: 0xf800, greenMask: 0x07e0, blueMask: 0x001f}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]byte, 5*3*4)
			if err := convert(got, src, src.Bounds(), tc.f); err != nil {
				t.Fatal(err)
			}
			want := referenceConvert(src, src.Bounds(), tc.f)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("converted to\n%#v\nwant\n%#v", got, want)
			}
		})
	}
}

// referenceConvert is the conversion written out longhand from the protocol's
// description of a ZPixmap, with no shortcut and no cleverness, so that it can
// disagree with the real one.
func referenceConvert(src *image.RGBA, r image.Rectangle, f pixelFormat) []byte {
	shift := func(mask uint32) (uint, uint) {
		var s, w uint
		for m := mask; m != 0 && m&1 == 0; m >>= 1 {
			s++
		}
		for m := mask >> s; m&1 == 1; m >>= 1 {
			w++
		}
		return s, w
	}
	fit := func(c byte, w uint) uint32 {
		switch {
		case w == 0:
			return 0
		case w >= 8:
			return uint32(c) << (w - 8)
		default:
			return uint32(c) >> (8 - w)
		}
	}
	rs, rw := shift(f.redMask)
	gs, gw := shift(f.greenMask)
	bs, bw := shift(f.blueMask)

	var out []byte
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := src.RGBAAt(x, y)
			v := fit(c.R, rw)<<rs | fit(c.G, gw)<<gs | fit(c.B, bw)<<bs
			v |= ^(f.redMask | f.greenMask | f.blueMask)
			if f.msbFirst {
				out = append(out, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
			} else {
				out = append(out, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
			}
		}
	}
	return out
}

func TestConvertMSBFirst(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.Pix = []byte{0x11, 0x22, 0x33, 0xff}
	f := bgra
	f.msbFirst = true
	dst := make([]byte, 4)
	if err := convert(dst, src, src.Bounds(), f); err != nil {
		t.Fatal(err)
	}
	want := []byte{0xff, 0x11, 0x22, 0x33}
	if !reflect.DeepEqual(dst, want) {
		t.Errorf("MSBFirst converted to %#v, want %#v", dst, want)
	}
}

func TestConvertRefusesBadArguments(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	if err := convert(make([]byte, 64), src, image.Rect(0, 0, 8, 8), bgra); err == nil {
		t.Error("a rect outside the buffer was accepted")
	}
	if err := convert(make([]byte, 4), src, src.Bounds(), bgra); err == nil {
		t.Error("a destination too small was accepted")
	}
}

func TestScaleComponent(t *testing.T) {
	for _, tc := range []struct {
		c     byte
		width uint
		want  uint32
	}{
		{0xff, 8, 0xff},
		{0x80, 8, 0x80},
		{0xff, 5, 0x1f},
		{0x00, 5, 0x00},
		{0xff, 10, 0x3fc},
		{0xff, 0, 0},
	} {
		if got := scaleComponent(tc.c, tc.width); got != tc.want {
			t.Errorf("scaleComponent(%#x, %d) = %#x, want %#x", tc.c, tc.width, got, tc.want)
		}
	}
}

func TestMaxImageBytes(t *testing.T) {
	// 65535 four-byte units is the largest a core request can declare, which
	// is 262140 bytes, less the 24-byte header.
	if got, want := maxImageBytes(65535), 262116; got != want {
		t.Errorf("maxImageBytes(65535) = %d, want %d", got, want)
	}
	// The smallest maximum the protocol allows a server to declare.
	if got, want := maxImageBytes(4096), 16360; got != want {
		t.Errorf("maxImageBytes(4096) = %d, want %d", got, want)
	}
	if got := maxImageBytes(4); got != 0 {
		t.Errorf("maxImageBytes(4) = %d, want 0 for a maximum that cannot hold the header", got)
	}
}

// TestChunkRectFitsEveryChunk is the property that matters: not what the split
// looks like, but that no piece of it can overflow a request length.
func TestChunkRectFitsEveryChunk(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    image.Rectangle
		max  int
	}{
		{"a whole 1600x1200 frame", image.Rect(0, 0, 1600, 1200), maxImageBytes(65535)},
		{"the same on a stingy server", image.Rect(0, 0, 1600, 1200), maxImageBytes(4096)},
		{"one text row", image.Rect(0, 0, 1600, 18), maxImageBytes(65535)},
		{"a rect not at the origin", image.Rect(64, 90, 1600, 108), maxImageBytes(4096)},
		{"a row wider than one request", image.Rect(0, 0, 100000, 3), maxImageBytes(4096)},
		{"one pixel", image.Rect(5, 5, 6, 6), maxImageBytes(65535)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chunks := chunkRect(tc.r, tc.max)
			if len(chunks) == 0 {
				t.Fatal("no chunks at all")
			}
			var area int
			union := chunks[0]
			for _, c := range chunks {
				if n := c.Dx() * c.Dy() * 4; n > tc.max {
					t.Fatalf("chunk %v is %d bytes, over the %d-byte maximum", c, n, tc.max)
				}
				if !c.In(tc.r) {
					t.Fatalf("chunk %v is outside %v", c, tc.r)
				}
				area += c.Dx() * c.Dy()
				union = union.Union(c)
			}
			if want := tc.r.Dx() * tc.r.Dy(); area != want {
				t.Errorf("the chunks cover %d pixels, want exactly %d with no overlap and no gap", area, want)
			}
			if union != tc.r {
				t.Errorf("the chunks span %v, want %v", union, tc.r)
			}
		})
	}
}

func TestChunkRectRefusesTheImpossible(t *testing.T) {
	if got := chunkRect(image.Rect(0, 0, 0, 0), 1024); got != nil {
		t.Errorf("an empty rect gave %v, want nothing", got)
	}
	if got := chunkRect(image.Rect(0, 0, 4, 4), 0); got != nil {
		t.Errorf("a zero maximum gave %v, want nothing", got)
	}
}

func TestSpanRect(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		row, col, length        int
		cellW, rowH, maxW, maxH int
		want                    image.Rectangle
	}{
		{name: "an ordinary span", row: 2, col: 3, length: 4, cellW: 8, rowH: 18, maxW: 640, maxH: 480,
			want: image.Rect(24, 36, 56, 54)},
		{name: "clipped at the right edge", row: 0, col: 78, length: 8, cellW: 8, rowH: 18, maxW: 640, maxH: 480,
			want: image.Rect(624, 0, 640, 18)},
		{name: "a row past the buffer is empty", row: 100, col: 0, length: 4, cellW: 8, rowH: 18, maxW: 640, maxH: 480,
			want: image.Rectangle{}},
		{name: "a negative column clips to zero", row: 0, col: -1, length: 3, cellW: 8, rowH: 18, maxW: 640, maxH: 480,
			want: image.Rect(0, 0, 16, 18)},
		{name: "a zero-length span is nothing", row: 0, col: 0, length: 0, cellW: 8, rowH: 18, maxW: 640, maxH: 480,
			want: image.Rectangle{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := spanRect(tc.row, tc.col, tc.length, tc.cellW, tc.rowH, tc.maxW, tc.maxH)
			if got.Empty() && tc.want.Empty() {
				return
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
