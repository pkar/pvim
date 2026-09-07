//go:build darwin

package gui

import (
	"image"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

// The blit: a CGImage built over a Go-owned RGBA slice, assigned to a CALayer's
// contents. No pixel is ever copied by this package and none is copied by
// CoreGraphics either, which is the whole point and also the source of both
// lifetime traps below.

// pinnedBuffers is why the pixels under a live CGImage do not disappear.
//
// TRAP ONE. CGDataProviderCreateWithData is handed a raw pointer into a Go
// slice and CoreGraphics keeps that pointer, without a reference to the Go
// object, for as long as the image or anything the window server made from it
// is alive. Go's heap does not move objects, so the address stays valid, but
// the garbage collector will happily free the backing array the moment the last
// Go reference to it goes away, and CoreGraphics would then be reading whatever
// the allocator handed out next. A package-level slice holding every buffer
// CoreGraphics might still be reading is the fix: it is a GC root, and nothing
// in it can be collected.
//
// It is not append-only, and that is the second half of the fix. Growing the
// window replaces the whole pool of frame buffers, and a list that only ever
// grew would retain every intermediate size a resize drag passed through:
// dragging one window edge from 640 to 3000 logical points is a few hundred
// grow steps and gigabytes of retained ramp, against 86 MB for the buffer
// actually in use at the end. A buffer leaves the list when the window is
// finished with it, which is exactly the moment it would have been safe to
// repaint: see window.sweepRetired.
var (
	pinMu         sync.Mutex
	pinnedBuffers []*image.RGBA
)

// pin makes img reachable from a package-level variable, so that the garbage
// collector cannot free pixels CoreGraphics is reading.
func pin(img *image.RGBA) {
	pinMu.Lock()
	defer pinMu.Unlock()
	for _, p := range pinnedBuffers {
		if p == img {
			return
		}
	}
	pinnedBuffers = append(pinnedBuffers, img)
}

// unpin drops img from the pin list, after which the garbage collector may free
// it. The caller has to know CoreGraphics is finished with the pixels;
// window.sweepRetired is the only caller that holds that proof.
func unpin(img *image.RGBA) {
	pinMu.Lock()
	defer pinMu.Unlock()
	for i, p := range pinnedBuffers {
		if p == img {
			pinnedBuffers = append(pinnedBuffers[:i], pinnedBuffers[i+1:]...)
			return
		}
	}
}

// pinnedCount reports how many buffers are pinned. It exists for the test that
// asserts a resize does not leak one per frame.
func pinnedCount() int {
	pinMu.Lock()
	defer pinMu.Unlock()
	return len(pinnedBuffers)
}

// providerRelease is the CGDataProviderReleaseDataCallback show() hands to
// every provider, and it is NULL. It is a variable rather than a literal 0 so
// that the decision has a name and a test.
//
// TRAP TWO. releaseData fires when CoreGraphics drops the last reference to the
// bytes, on whatever thread did the dropping: the window server's reply thread,
// a CoreAnimation render thread, a thread Go did not create and has no
// goroutine for. An empty purego.NewCallback there is not free the way an empty
// C function would be. purego's entry path is crosscall2 into
// runtime.cgocallback, an m attach, a make([]reflect.Value, n) and a
// process-global sync.Mutex over its callback table, once per provider
// destroyed, which on this blit path is once per frame. Land that during a
// stop-the-world and a CoreGraphics thread parks in the Go scheduler still
// holding whatever lock brought it there.
//
// CGDataProvider.h declares releaseData cg_nullable, so NULL is legal, and
// there is nothing to free anyway: the memory belongs to Go and pinnedBuffers
// owns it. NULL removes the whole class of hazard and loses nothing.
var providerRelease uintptr

// frameBuf is one RGBA buffer and the rectangle of it that has been painted.
//
// The buffer is normally larger than the painted area: it is grown in coarse
// steps and never shrunk, so that dragging a window edge does not reallocate
// the pool on every mouse-move event.
type frameBuf struct {
	img  *image.RGBA
	w, h int
}

// blitter owns the CoreGraphics side of one layer.
type blitter struct {
	layer      objc.ID
	colorSpace uintptr
}

// newBlitter prepares the device RGB colour space the images are built in. The
// colour space is created once and shared: it is immutable and CoreGraphics
// caches it internally anyway.
func newBlitter(layer objc.ID) *blitter {
	return &blitter{layer: layer, colorSpace: cgColorSpaceCreateDeviceRGB()}
}

// show puts fb on screen. It must be called on the main thread.
//
// The CATransaction with actions disabled is not optional. A CALayer's contents
// is an animatable property, so assigning to it outside a transaction gives it
// the implicit quarter-second cross-fade, and every keystroke would dissolve
// into the next one.
func (b *blitter) show(fb frameBuf) {
	if fb.img == nil || fb.w <= 0 || fb.h <= 0 {
		return
	}
	pix := fb.img.Pix
	if len(pix) == 0 {
		return
	}

	transaction := objc.ID(objc.GetClass("CATransaction"))
	transaction.Send(selBegin)
	transaction.Send(selSetDisableActions, true)

	provider := cgDataProviderCreateWithData(nil, unsafe.Pointer(&pix[0]), uintptr(len(pix)), providerRelease)
	if provider != 0 {
		img := cgImageCreate(
			uintptr(fb.w), uintptr(fb.h),
			8, 32, uintptr(fb.img.Stride),
			b.colorSpace,
			kCGImageAlphaPremultipliedLast|kCGBitmapByteOrder32Big,
			provider, 0, false, kCGRenderingIntentDefault,
		)
		// The image retains the provider, so our reference goes now.
		cgDataProviderRelease(provider)
		if img != 0 {
			// setContents: retains the image; our reference goes as soon as the
			// layer has taken its own, and the previous frame's image is
			// released by the layer at the same moment.
			b.layer.Send(selSetContents, img)
			cgImageRelease(img)
		}
	}

	transaction.Send(selCommit)
}

// setBackground paints the layer's own colour, which is what shows through the
// strip of window that a live resize has exposed but the editor has not yet
// repainted. Without it that strip is transparent and the desktop shines
// through, which reads as tearing even though nothing has torn.
func (b *blitter) setBackground(r, g, bl uint8) {
	c := cgColorCreateGenericRGB(float64(r)/255, float64(g)/255, float64(bl)/255, 1)
	if c == 0 {
		return
	}
	b.layer.Send(selSetBackgroundColor, c)
	cgColorRelease(c)
}
