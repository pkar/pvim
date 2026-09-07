//go:build darwin

package gui

import (
	"fmt"
	"strings"
	"structs"
	"sync"
	"unicode/utf8"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// This file is the whole of the C-level surface. Everything below is a symbol
// looked up by name at run time through dlopen and dlsym, which is why there is
// no C compiler anywhere in this build and why `go list -deps` shows no package
// with CgoFiles in it.
//
// Nothing here does anything clever. It is a wall of bindings, and the rule for
// adding to it is that a symbol appears once, in a package-level var, resolved
// once in initObjC, and is never looked up again on a hot path: dlsym and
// sel_registerName both take global locks and a keystroke must not pay for one.

// NSPoint, NSSize and NSRect are the three AppKit geometry structs, passed and
// returned by value.
//
// structs.HostLayout is what tells purego these are C structs rather than Go
// ones; without it the field offsets are Go's business and the ABI marshalling
// silently reads the wrong registers.
type NSPoint struct {
	_    structs.HostLayout
	X, Y float64
}

// NSSize is a width and a height in points.
type NSSize struct {
	_             structs.HostLayout
	Width, Height float64
}

// NSRect is an origin and a size in points.
type NSRect struct {
	_      structs.HostLayout
	Origin NSPoint
	Size   NSSize
}

// AppKit and CoreGraphics constants, spelled out rather than imported because
// there is no header to import them from.
const (
	nsApplicationActivationPolicyRegular = 0

	nsWindowStyleMaskTitled          = 1 << 0
	nsWindowStyleMaskClosable        = 1 << 1
	nsWindowStyleMaskMiniaturizable  = 1 << 2
	nsWindowStyleMaskResizable       = 1 << 3
	nsWindowStyleMaskStandardWindow  = nsWindowStyleMaskTitled | nsWindowStyleMaskClosable | nsWindowStyleMaskMiniaturizable | nsWindowStyleMaskResizable
	nsBackingStoreBuffered           = 2
	nsEventModifierFlagDeviceIndepen = 0xffff0000

	// NSEventTypeApplicationDefined. AppKit's NSEventType has SystemDefined at
	// 14 and ApplicationDefined at 15, and 14 is the value a wake-up event
	// wants least: AppKit routes a system-defined event through its own
	// handling, and NSEventMaskApplicationDefined is 1<<15, so an event posted
	// as 14 is invisible to any nextEventMatchingMask: written against this
	// name.
	nsEventTypeApplicationDefined = 15

	// NSTerminateCancel, from NSApplication.h. It is what
	// -applicationShouldTerminate: answers so that a Quit from the Dock menu
	// or a logout becomes a CloseEvent for the editor to decide on rather than
	// a process that stops with an unsaved buffer in it.
	nsTerminateCancel = 0

	// A CGImage over an image.RGBA: eight bits a component, four components,
	// alpha last, and byte order 32-big so that the bytes are read in the order
	// R, G, B, A whatever the machine's endianness. image.RGBA is documented as
	// non-premultiplied but every pixel this editor draws is fully opaque, so
	// premultiplied-last describes the same bytes and composites faster.
	kCGImageAlphaPremultipliedLast = 1
	kCGBitmapByteOrder32Big        = 4 << 12
	kCGRenderingIntentDefault      = 0

	// cfStringEncodingUTF8 is CFStringBuiltInEncodings' kCFStringEncodingUTF8.
	cfStringEncodingUTF8 = 0x08000100
)

// The Objective-C selectors, registered once. Caching them is not a
// micro-optimisation: objc.RegisterName takes the runtime's global lock, and a
// key event that registered six selectors would take it six times per
// keystroke.
var (
	selAlloc                     objc.SEL
	selInit                      objc.SEL
	selRelease                   objc.SEL
	selSharedApplication         objc.SEL
	selSetActivationPolicy       objc.SEL
	selActivateIgnoringOtherApps objc.SEL
	selRun                       objc.SEL
	selStop                      objc.SEL
	selPostEventAtStart          objc.SEL
	selSetMainMenu               objc.SEL
	selAddItem                   objc.SEL
	selSetSubmenu                objc.SEL
	selSetTarget                 objc.SEL
	selInitWithTitle             objc.SEL
	selInitWithTitleActionKey    objc.SEL
	selSetKeyEquivalentModMask   objc.SEL
	selSeparatorItem             objc.SEL
	selStringWithUTF8String      objc.SEL
	selInitWithContentRect       objc.SEL
	selSetTitle                  objc.SEL
	selMakeKeyAndOrderFront      objc.SEL
	selCenter                    objc.SEL
	selSetContentView            objc.SEL
	selMakeFirstResponder        objc.SEL
	selSetDelegate               objc.SEL
	selSetReleasedWhenClosed     objc.SEL
	selBackingScaleFactor        objc.SEL
	selContentView               objc.SEL
	selInitWithFrame             objc.SEL
	selFrame                     objc.SEL
	selBounds                    objc.SEL
	selSetWantsLayer             objc.SEL
	selLayer                     objc.SEL
	selSetContents               objc.SEL
	selSetContentsScale          objc.SEL
	selSetContentsGravity        objc.SEL
	selSetMagnificationFilter    objc.SEL
	selSetMinificationFilter     objc.SEL
	selSetBackgroundColor        objc.SEL
	selSetFrameSize              objc.SEL
	selSetAutoresizingMask       objc.SEL
	selLocationInWindow          objc.SEL
	selConvertPointFromView      objc.SEL
	selModifierFlags             objc.SEL
	selCharactersIgnoringMods    objc.SEL
	selLength                    objc.SEL
	selCharacterAtIndex          objc.SEL
	selKeyCode                   objc.SEL
	selButtonNumber              objc.SEL
	selScrollingDeltaX           objc.SEL
	selScrollingDeltaY           objc.SEL
	selHasPreciseScrollingDeltas objc.SEL
	selOtherEventWithType        objc.SEL
	selBegin                     objc.SEL
	selCommit                    objc.SEL
	selSetDisableActions         objc.SEL
	selNextEventMatchingMask     objc.SEL
	selArray                     objc.SEL
	selArrayWithObject           objc.SEL
	selInterpretKeyEvents        objc.SEL
	selInputContext              objc.SEL
	selDiscardMarkedText         objc.SEL
	selConvertRectToView         objc.SEL
	selConvertRectToScreen       objc.SEL
	selRespondsToSelector        objc.SEL
	selString                    objc.SEL
)

// CoreFoundation, CoreGraphics and libdispatch entry points.
//
// These are plain C functions, so purego.RegisterLibFunc binds them to a Go
// func value and the call is a direct trampoline with no objc_msgSend in the
// way.
var (
	cgColorSpaceCreateDeviceRGB  func() uintptr
	cgColorSpaceRelease          func(uintptr)
	cgDataProviderCreateWithData func(info unsafe.Pointer, data unsafe.Pointer, size uintptr, release uintptr) uintptr
	cgDataProviderRelease        func(uintptr)
	cgImageCreate                func(w, h, bitsPerComponent, bitsPerPixel, bytesPerRow uintptr, space uintptr, bitmapInfo uint32, provider uintptr, decode uintptr, interpolate bool, intent uint32) uintptr
	cgImageRelease               func(uintptr)
	cgColorCreateGenericRGB      func(r, g, b, a float64) uintptr
	cgColorRelease               func(uintptr)

	cfRunLoopGetMain      func() uintptr
	cfRunLoopSourceCreate func(alloc uintptr, order int, ctx *cfRunLoopSourceContext) uintptr
	cfRunLoopAddSource    func(rl, src, mode uintptr)
	cfRunLoopSourceSignal func(src uintptr)
	cfRunLoopRemoveSource func(rl, src, mode uintptr)
	cfRunLoopSourceInvald func(src uintptr)
	cfRunLoopWakeUp       func(rl uintptr)
	cfRunLoopGetCurrent   func() uintptr
	cfRunLoopRunInMode    func(mode uintptr, seconds float64, returnAfterSourceHandled bool) int32
	cfStringCreateWithStr func(alloc uintptr, cstr string, encoding uint32) uintptr
	cfRelease             func(uintptr)

	dispatchAsyncF func(queue uintptr, ctx unsafe.Pointer, fn uintptr)

	// dispatchMainQueue is the address of the _dispatch_main_q symbol, which is
	// the main queue object itself: dispatch_get_main_queue() is defined as
	// &_dispatch_main_q, so the symbol address is the value and there is
	// nothing to dereference.
	dispatchMainQueue uintptr

	// runLoopModes are the modes a run loop source has to be added to so that
	// it fires whether the application is idle, tracking a mouse drag on a
	// window edge, or showing a modal panel.
	//
	// This is what kCFRunLoopCommonModes would have given, spelled out. The
	// sentinel itself cannot be used: CFRunLoopAddSource recognises
	// kCFRunLoopCommonModes by pointer identity against the constant in
	// CoreFoundation's data segment, and reaching that value means converting a
	// dlsym address to an unsafe.Pointer, which go vet refuses and is right to.
	// Mode names, unlike the sentinel, are matched by CFEqual, so a string
	// built here with the same contents is the same mode. Measured: with the
	// sentinel replaced by an equal-valued string the source never fires at
	// all, and with these three names it fires exactly as it did.
	runLoopModes []uintptr

	// runLoopDefaultMode is the mode a test drives its own loop in.
	runLoopDefaultMode uintptr
)

var (
	objcOnce sync.Once
	objcErr  error
)

// initObjC resolves every framework symbol this package uses. It runs once per
// process and every entry point that touches AppKit calls it first.
//
// It is lazy rather than an init function so that importing internal/gui on a
// machine with no window server, which every headless test and the --oracle
// mode do, costs nothing and loads no frameworks.
func initObjC() error {
	objcOnce.Do(func() { objcErr = loadFrameworks() })
	return objcErr
}

// loadFrameworks is the body of initObjC, split out only so the once wrapper
// stays readable.
func loadFrameworks() error {
	appKit, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
	if err != nil {
		return fmt.Errorf("gui: loading AppKit: %w", err)
	}
	_ = appKit
	if _, err := purego.Dlopen("/System/Library/Frameworks/QuartzCore.framework/QuartzCore", purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
		return fmt.Errorf("gui: loading QuartzCore: %w", err)
	}
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
	if err != nil {
		return fmt.Errorf("gui: loading CoreGraphics: %w", err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
	if err != nil {
		return fmt.Errorf("gui: loading CoreFoundation: %w", err)
	}

	purego.RegisterLibFunc(&cgColorSpaceCreateDeviceRGB, cg, "CGColorSpaceCreateDeviceRGB")
	purego.RegisterLibFunc(&cgColorSpaceRelease, cg, "CGColorSpaceRelease")
	purego.RegisterLibFunc(&cgDataProviderCreateWithData, cg, "CGDataProviderCreateWithData")
	purego.RegisterLibFunc(&cgDataProviderRelease, cg, "CGDataProviderRelease")
	purego.RegisterLibFunc(&cgImageCreate, cg, "CGImageCreate")
	purego.RegisterLibFunc(&cgImageRelease, cg, "CGImageRelease")
	purego.RegisterLibFunc(&cgColorCreateGenericRGB, cg, "CGColorCreateGenericRGB")
	purego.RegisterLibFunc(&cgColorRelease, cg, "CGColorRelease")

	purego.RegisterLibFunc(&cfRunLoopGetMain, cf, "CFRunLoopGetMain")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopSourceCreate, cf, "CFRunLoopSourceCreate")
	purego.RegisterLibFunc(&cfRunLoopAddSource, cf, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&cfRunLoopSourceSignal, cf, "CFRunLoopSourceSignal")
	purego.RegisterLibFunc(&cfRunLoopRemoveSource, cf, "CFRunLoopRemoveSource")
	purego.RegisterLibFunc(&cfRunLoopSourceInvald, cf, "CFRunLoopSourceInvalidate")
	purego.RegisterLibFunc(&cfRunLoopWakeUp, cf, "CFRunLoopWakeUp")
	purego.RegisterLibFunc(&cfRunLoopRunInMode, cf, "CFRunLoopRunInMode")
	purego.RegisterLibFunc(&cfStringCreateWithStr, cf, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")

	// libdispatch is inside libSystem, which is already mapped into every Go
	// darwin process, so RTLD_DEFAULT finds it with no dlopen.
	//
	// Nothing calls these: they are the wake mechanism mainthread_darwin.go
	// measured and did not keep, left bound so that the file's claim that the
	// alternative is two lines away stays true and compiled. A failure to find
	// them is therefore not a failure to start, which is why this is the one
	// lookup in this function that does not return an error.
	if sym, err := purego.Dlsym(purego.RTLD_DEFAULT, "dispatch_async_f"); err == nil {
		purego.RegisterFunc(&dispatchAsyncF, sym)
	}
	if q, err := purego.Dlsym(purego.RTLD_DEFAULT, "_dispatch_main_q"); err == nil {
		dispatchMainQueue = q
	}

	// The run loop mode names. These are created once and never released:
	// there is one of each per process and they outlive everything.
	for _, name := range []string{
		"kCFRunLoopDefaultMode",      // idle, and NSDefaultRunLoopMode
		"NSEventTrackingRunLoopMode", // dragging a window edge or a scrollbar
		"NSModalPanelRunLoopMode",    // a sheet or a modal panel is up
	} {
		mode := cfStringCreateWithStr(0, name, cfStringEncodingUTF8)
		if mode == 0 {
			return fmt.Errorf("gui: CFStringCreateWithCString returned NULL for %s", name)
		}
		runLoopModes = append(runLoopModes, mode)
	}
	runLoopDefaultMode = runLoopModes[0]

	registerSelectors()
	return nil
}

// registerSelectors fills the selector cache. One function so that a selector
// added to the var block above and forgotten here is a nil SEL and an obvious
// crash, rather than a lazily-registered one on a hot path.
func registerSelectors() {
	selAlloc = objc.RegisterName("alloc")
	selInit = objc.RegisterName("init")
	selRelease = objc.RegisterName("release")
	selSharedApplication = objc.RegisterName("sharedApplication")
	selSetActivationPolicy = objc.RegisterName("setActivationPolicy:")
	selActivateIgnoringOtherApps = objc.RegisterName("activateIgnoringOtherApps:")
	selRun = objc.RegisterName("run")
	selStop = objc.RegisterName("stop:")
	selPostEventAtStart = objc.RegisterName("postEvent:atStart:")
	selSetMainMenu = objc.RegisterName("setMainMenu:")
	selAddItem = objc.RegisterName("addItem:")
	selSetSubmenu = objc.RegisterName("setSubmenu:")
	selSetTarget = objc.RegisterName("setTarget:")
	selInitWithTitle = objc.RegisterName("initWithTitle:")
	selInitWithTitleActionKey = objc.RegisterName("initWithTitle:action:keyEquivalent:")
	selSetKeyEquivalentModMask = objc.RegisterName("setKeyEquivalentModifierMask:")
	selSeparatorItem = objc.RegisterName("separatorItem")
	selStringWithUTF8String = objc.RegisterName("stringWithUTF8String:")
	selInitWithContentRect = objc.RegisterName("initWithContentRect:styleMask:backing:defer:")
	selSetTitle = objc.RegisterName("setTitle:")
	selMakeKeyAndOrderFront = objc.RegisterName("makeKeyAndOrderFront:")
	selCenter = objc.RegisterName("center")
	selSetContentView = objc.RegisterName("setContentView:")
	selMakeFirstResponder = objc.RegisterName("makeFirstResponder:")
	selSetDelegate = objc.RegisterName("setDelegate:")
	selSetReleasedWhenClosed = objc.RegisterName("setReleasedWhenClosed:")
	selBackingScaleFactor = objc.RegisterName("backingScaleFactor")
	selContentView = objc.RegisterName("contentView")
	selInitWithFrame = objc.RegisterName("initWithFrame:")
	selFrame = objc.RegisterName("frame")
	selBounds = objc.RegisterName("bounds")
	selSetWantsLayer = objc.RegisterName("setWantsLayer:")
	selLayer = objc.RegisterName("layer")
	selSetContents = objc.RegisterName("setContents:")
	selSetContentsScale = objc.RegisterName("setContentsScale:")
	selSetContentsGravity = objc.RegisterName("setContentsGravity:")
	selSetMagnificationFilter = objc.RegisterName("setMagnificationFilter:")
	selSetMinificationFilter = objc.RegisterName("setMinificationFilter:")
	selSetBackgroundColor = objc.RegisterName("setBackgroundColor:")
	selSetFrameSize = objc.RegisterName("setFrameSize:")
	selSetAutoresizingMask = objc.RegisterName("setAutoresizingMask:")
	selLocationInWindow = objc.RegisterName("locationInWindow")
	selConvertPointFromView = objc.RegisterName("convertPoint:fromView:")
	selModifierFlags = objc.RegisterName("modifierFlags")
	selCharactersIgnoringMods = objc.RegisterName("charactersIgnoringModifiers")
	selLength = objc.RegisterName("length")
	selCharacterAtIndex = objc.RegisterName("characterAtIndex:")
	selKeyCode = objc.RegisterName("keyCode")
	selButtonNumber = objc.RegisterName("buttonNumber")
	selScrollingDeltaX = objc.RegisterName("scrollingDeltaX")
	selScrollingDeltaY = objc.RegisterName("scrollingDeltaY")
	selHasPreciseScrollingDeltas = objc.RegisterName("hasPreciseScrollingDeltas")
	selOtherEventWithType = objc.RegisterName("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:")
	selBegin = objc.RegisterName("begin")
	selCommit = objc.RegisterName("commit")
	selSetDisableActions = objc.RegisterName("setDisableActions:")
	selNextEventMatchingMask = objc.RegisterName("nextEventMatchingMask:untilDate:inMode:dequeue:")
	selArray = objc.RegisterName("array")
	selArrayWithObject = objc.RegisterName("arrayWithObject:")
	selInterpretKeyEvents = objc.RegisterName("interpretKeyEvents:")
	selInputContext = objc.RegisterName("inputContext")
	selDiscardMarkedText = objc.RegisterName("discardMarkedText")
	selConvertRectToView = objc.RegisterName("convertRect:toView:")
	selConvertRectToScreen = objc.RegisterName("convertRectToScreen:")
	selRespondsToSelector = objc.RegisterName("respondsToSelector:")
	selString = objc.RegisterName("string")
}

// nsString wraps a Go string in an autoreleased NSString.
//
// purego converts the Go string to a NUL-terminated C string for the duration
// of the call, and stringWithUTF8String: copies it, so the Go bytes are not
// retained past the message send.
//
// The bytes are coerced to valid UTF-8 first, and that is not tidiness.
// +[NSString stringWithUTF8String:] returns nil for anything that is not valid
// UTF-8, and AppKit's setters assert on nil rather than ignoring it:
// -[NSWindow setTitle:] with nil raises NSInternalInconsistencyException,
// "Invalid parameter not satisfying: aString != nil", which nothing here
// catches and which aborts the process. A path is bytes and not UTF-8, so
// `pvim $'bad\xff.txt'` would open the window and then kill the editor on the
// first retitle with the buffer still in it, on the main thread, with nothing
// on the editor's side naming the cause. Measured under purego
// v0.11.0: nil for ff 2e 74 78 74 and for 66 6f 6f c3 28, non-nil for every
// valid input including the empty string.
//
// An embedded NUL still truncates, because the C string ends where the NUL is.
// No path and no menu title can carry one, and a short title is not a crash.
func nsString(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(selStringWithUTF8String, toValidUTF8(s))
}

// toValidUTF8 replaces every byte that is not part of a valid UTF-8 sequence
// with U+FFFD, one for one.
//
// Ranging over a Go string decodes exactly that way -- an invalid byte yields
// utf8.RuneError with a size of one -- so writing the runes back out is the
// whole of it, and a U+FFFD that really was in the input is re-encoded
// unchanged. Replacing rather than dropping is what the rest of macOS shows
// for a name it cannot read, and it keeps the length of a title recognisable
// instead of silently shortening it.
func toValidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		b.WriteRune(r)
	}
	return b.String()
}

// firstUTF16 returns the first UTF-16 code unit of an NSString, and false when
// the string is empty.
//
// Reading a code unit rather than -UTF8String keeps this file free of any
// uintptr-to-unsafe.Pointer conversion. The only keys that arrive as a
// surrogate pair are astral-plane characters typed directly, which the key
// package has no representation for anyway.
func firstUTF16(s objc.ID) (uint16, bool) {
	if s == 0 {
		return 0, false
	}
	if objc.Send[int](s, selLength) == 0 {
		return 0, false
	}
	return objc.Send[uint16](s, selCharacterAtIndex, 0), true
}
