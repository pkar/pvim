//go:build darwin

package gui

import (
	"image"
	"runtime"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// newTestLayer returns a standalone CALayer, which needs no window and no
// window server: the whole blit path can be exercised in a `go test` on a box
// with no display, and only the pixels reaching a screen cannot.
func newTestLayer(t testing.TB) objc.ID {
	t.Helper()
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	layer := objc.ID(objc.GetClass("CALayer")).Send(selAlloc).Send(selInit)
	if layer == 0 {
		t.Skip("CALayer alloc/init returned nil")
	}
	return layer
}

// TestBlitShow drives the real CoreGraphics path: a CGImage built over a Go
// slice with no copy, handed to a layer, released. It passing means the two
// lifetime traps documented in blit_darwin.go are at least not immediately
// fatal; it cannot prove the release callback is safe on a foreign thread,
// which is why that callback does nothing rather than being tested.
func TestBlitShow(t *testing.T) {
	layer := newTestLayer(t)
	b := newBlitter(layer)
	if b.colorSpace == 0 {
		t.Fatal("CGColorSpaceCreateDeviceRGB returned NULL")
	}

	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for i := range img.Pix {
		img.Pix[i] = byte(i)
	}
	pin(img)

	// Many frames over one buffer, which is the running editor's shape: create
	// an image, hand it over, release it, do it again. A leak or a double free
	// here shows up as a crash rather than as a failed assertion, which is the
	// only kind of assertion CoreGraphics makes.
	for i := 0; i < 200; i++ {
		b.show(frameBuf{img: img, w: 64, h: 32})
	}
	b.setBackground(0x26, 0x26, 0x26)

	runtime.GC()
	b.show(frameBuf{img: img, w: 64, h: 32})
}

// TestBlitShowIgnoresAnEmptyFrame checks the guard that keeps a resize to
// nothing, or a Draw before the first paint, out of CGImageCreate. CoreGraphics
// answers a zero width with a NULL image and a log line on stderr, and the
// gate on this frontend is that stderr stays empty.
func TestBlitShowIgnoresAnEmptyFrame(t *testing.T) {
	layer := newTestLayer(t)
	b := newBlitter(layer)
	b.show(frameBuf{})
	b.show(frameBuf{img: image.NewRGBA(image.Rect(0, 0, 0, 0)), w: 0, h: 0})
	b.show(frameBuf{img: image.NewRGBA(image.Rect(0, 0, 4, 4)), w: 0, h: 4})
}

// TestBuffersOnlyGrow is the pin list's safety property. Every buffer
// CoreGraphics may be reading is pinned, so a pool that were reallocated per
// mouse-move event and never released would leak a few megabytes per pixel
// dragged.
func TestBuffersOnlyGrow(t *testing.T) {
	before := pinnedCount()
	w := &window{}
	n := len(w.bufs)

	w.buffer(100, 100)
	w.buffer(100, 100)
	w.buffer(100, 100)
	if got := pinnedCount() - before; got != n {
		t.Errorf("three frames at one size pinned %d buffers, want %d", got, n)
	}
	was := w.bufW

	w.buffer(was+1, 100) // past the step the pool was rounded up to: a real grow
	if w.bufW <= was {
		t.Fatalf("asking for %d pixels of width left the pool %d wide", was+1, w.bufW)
	}
	if got := pinnedCount() - before; got != n {
		t.Errorf("after a grow %d buffers are pinned, want %d: the old pool was never released", got, n)
	}

	pool := w.bufs
	w.buffer(50, 50) // shrink: must not reallocate
	w.buffer(60, 40)
	if w.bufs != pool {
		t.Error("shrinking reallocated the pool")
	}
	if got := pinnedCount() - before; got != n {
		t.Errorf("shrinking pinned again: %d buffers, want %d", got, n)
	}
}

// TestGrowingDoesNotRetainTheRamp is the resize drag. Every new column of
// window width is a ResizeEvent, a Draw and a larger buffer, and a pin list
// that only ever grew would retain the sum of the whole ramp rather than the
// size in use at the end.
func TestGrowingDoesNotRetainTheRamp(t *testing.T) {
	before := pinnedCount()
	w := &window{}
	for px := 100; px <= 500; px += 4 {
		w.buffer(px, 100)
	}
	if got, want := pinnedCount()-before, len(w.bufs); got != want {
		t.Errorf("a %d-step resize ramp left %d buffers pinned, want %d", (500-100)/4+1, got, want)
	}
}

// TestBuffersRotate checks that consecutive frames land in different buffers,
// which is what stops a repaint racing the blit of the frame before it.
//
// The old shape of this test asserted a strict two-buffer alternation, so that
// the third frame came back to the first buffer. That is the bug: with
// wakeRedraw coalescing, the third frame can arrive with the layer still
// reading the first buffer.
func TestBuffersRotate(t *testing.T) {
	w := &window{}
	seen := map[*image.RGBA]bool{}
	for i := 0; i < len(w.bufs); i++ {
		b, _, _ := w.buffer(64, 64)
		if seen[b] {
			t.Fatalf("frame %d reused a buffer an earlier frame in the pool got", i)
		}
		seen[b] = true
	}
}

// TestBufferNeverReturnsTheOneOnScreen is the tearing bug. mainQueue.wakeRedraw
// coalesces, so N Draws between two run loop passes cost one blit: the editor
// can get two buffers in a row with no blit between them, and a two-buffer
// alternation hands the second of those the buffer the layer's live CGImage is
// reading. raster.Paint rewrites every row, so what the window server
// composites is half of one frame and half of another, over the whole grid.
func TestBufferNeverReturnsTheOneOnScreen(t *testing.T) {
	layer := newTestLayer(t)
	w := &window{blit: newBlitter(layer)}

	draw := func() *image.RGBA {
		img, _, _ := w.buffer(64, 64)
		w.mu.Lock()
		w.front = frameBuf{img: img, w: 64, h: 64}
		w.mu.Unlock()
		return img
	}

	onScreen := draw()
	w.blitFront()

	// Two more Draws with no blit between them, which is exactly what the
	// editor outrunning the display produces.
	second := draw()
	third := draw()
	if second == onScreen {
		t.Error("the next frame was painted into the buffer the layer is reading")
	}
	if third == onScreen {
		t.Error("the frame after next was painted into the buffer the layer is reading")
	}
}

// TestBufferUnpublishesTheFrameItReuses covers the one case where the pool has
// to hand back the buffer the pending frame points at: the layer holds one and
// a blit in flight is reading another. Repainting the pending frame is right,
// leaving it published while it is repainted is not, because the main thread
// would blit a buffer mid-paint.
func TestBufferUnpublishesTheFrameItReuses(t *testing.T) {
	w := &window{}
	w.buffer(64, 64)

	w.mu.Lock()
	w.shown = w.bufs[0]
	w.blitting = w.bufs[1]
	w.front = frameBuf{img: w.bufs[2], w: 64, h: 64}
	w.mu.Unlock()

	if got, _, _ := w.buffer(64, 64); got != w.bufs[2] {
		t.Fatalf("with two buffers in CoreGraphics' hands the pool returned %p, want the third", got)
	}
	if w.front.img != nil {
		t.Error("the frame being repainted is still published for the main thread to blit")
	}
}

// TestProviderReleaseIsNull keeps a Go callback off every frame's provider.
// CGDataProviderCreateWithData's releaseData is cg_nullable and there is
// nothing to free, so NULL is what it gets: a purego.NewCallback there, however
// empty its body, is a cgocallback, an m attach, an allocation and a
// process-global mutex on a CoreGraphics thread, once per frame.
func TestProviderReleaseIsNull(t *testing.T) {
	if providerRelease != 0 {
		t.Errorf("providerRelease is %#x, want 0: every blit arms a foreign-thread entry into the Go runtime", providerRelease)
	}
}

// TestProviderReleaseWouldFire is the evidence for the test above: it shows
// CoreGraphics really does call releaseData, so a non-NULL one is not dead
// code, it runs once per frame on a thread Go did not create.
func TestProviderReleaseWouldFire(t *testing.T) {
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	var calls int32
	cb := purego.NewCallback(func(info unsafe.Pointer, data unsafe.Pointer, size uintptr) {
		atomic.AddInt32(&calls, 1)
	})
	pix := make([]byte, 64)
	provider := cgDataProviderCreateWithData(nil, unsafe.Pointer(&pix[0]), uintptr(len(pix)), cb)
	if provider == 0 {
		t.Fatal("CGDataProviderCreateWithData returned NULL")
	}
	cgDataProviderRelease(provider)
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("releaseData ran %d times, want 1", calls)
	}
}

// TestRegisterClasses checks that the two runtime-registered Objective-C
// classes are accepted by the runtime. This needs no window server either:
// objc_allocateClassPair is a data structure, not a display connection.
func TestRegisterClasses(t *testing.T) {
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	view, err := registerViewClass()
	if err != nil {
		t.Fatalf("registering pvimView: %v", err)
	}
	if view == 0 {
		t.Fatal("pvimView registered as the nil class")
	}
	delegate, err := registerDelegateClass()
	if err != nil {
		t.Fatalf("registering pvimDelegate: %v", err)
	}
	if delegate == 0 {
		t.Fatal("pvimDelegate registered as the nil class")
	}

	// Registration is once per process; a second call must hand back the same
	// class rather than fail, because objc_allocateClassPair refuses a name it
	// has already seen.
	again, err := registerViewClass()
	if err != nil || again != view {
		t.Errorf("second registerViewClass gave %v, %v; want %v, nil", again, err, view)
	}
}

// TestViewRespondsToItsSelectors instantiates the registered class and checks
// the two methods that decide whether the window works at all: a view that does
// not accept first responder never sees a key, and one that is not flipped puts
// row 0 at the bottom of the window.
func TestViewRespondsToItsSelectors(t *testing.T) {
	if err := initObjC(); err != nil {
		t.Skipf("no AppKit available: %v", err)
	}
	cls, err := registerViewClass()
	if err != nil {
		t.Fatalf("registering pvimView: %v", err)
	}
	view := objc.ID(cls).Send(selAlloc)
	view = objc.Send[objc.ID](view, selInitWithFrame, NSRect{Size: NSSize{Width: 640, Height: 480}})
	if view == 0 {
		t.Fatal("pvimView initWithFrame: returned nil")
	}
	if !objc.Send[bool](view, objc.RegisterName("acceptsFirstResponder")) {
		t.Error("acceptsFirstResponder is NO; the window would beep at every keystroke")
	}
	if !objc.Send[bool](view, objc.RegisterName("isFlipped")) {
		t.Error("isFlipped is NO; the grid origin would be at the bottom left")
	}
	if !objc.Send[bool](view, objc.RegisterName("wantsUpdateLayer")) {
		t.Error("wantsUpdateLayer is NO; AppKit would call drawRect: and clobber the blit")
	}

	// setFrameSize: goes through the registered IMP, which takes an NSSize by
	// value. That is a struct passed to a Go callback across the C ABI and it
	// is the single most fragile thing in this file.
	view.Send(selSetFrameSize, NSSize{Width: 321, Height: 654})
	got := objc.Send[NSRect](view, selFrame)
	if got.Size.Width != 321 || got.Size.Height != 654 {
		t.Errorf("after setFrameSize: the frame is %vx%v, want 321x654", got.Size.Width, got.Size.Height)
	}
}

// BenchmarkFrame is the frame gate: a full 200x60 repaint and blit, which has
// to come in under 4ms.
//
// It measures what a keystroke actually costs, rasterising and handing the
// pixels to CoreGraphics, and it deliberately does not measure the window
// server compositing them, which is not this process's time.
func BenchmarkFrame(b *testing.B) {
	const scale = 2
	face, err := raster.DefaultFace(scale)
	if err != nil {
		b.Skipf("no font: %v", err)
	}
	layer := newTestLayer(b)
	bl := newBlitter(layer)

	s := screen.NewScreen(60, 200)
	for row := 0; row < s.Grid.Rows; row++ {
		for col := 0; col < s.Grid.Cols; col++ {
			s.Grid.Set(row, col, 'a', screen.Normal)
		}
	}
	opt := raster.PaintOptions{Face: face}
	frame := raster.Frame{Grid: &s.Grid, HL: s.HL, Cursor: raster.CursorBlock}
	img := raster.NewImage(frame, opt)
	pin(img)
	w, h := raster.Size(frame, opt)
	b.Logf("200x60 cells at scale %d is %dx%d pixels", scale, w, h)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := raster.Paint(img, frame, opt); err != nil {
			b.Fatal(err)
		}
		bl.show(frameBuf{img: img, w: w, h: h})
	}
}

// BenchmarkBlit is the CoreGraphics half on its own, so that a regression in
// the frame budget can be attributed to the rasteriser or to the blit without
// guessing.
func BenchmarkBlit(b *testing.B) {
	layer := newTestLayer(b)
	bl := newBlitter(layer)
	img := image.NewRGBA(image.Rect(0, 0, 3200, 2040))
	pin(img)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bl.show(frameBuf{img: img, w: 3200, h: 2040})
	}
}
