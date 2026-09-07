//go:build darwin

package clip

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// This file is the whole of the C-level surface of the clipboard, and it is
// deliberately the same shape as internal/gui's objc_darwin.go: every symbol is
// looked up by name at run time through dlopen and dlsym, so there is no C
// compiler in this build and `go list -deps` shows no package with CgoFiles.
// The two files do not share code, because sharing it would mean an import
// between internal/clip and internal/gui and that import is the thing this
// package's layout exists to avoid.
//
// The rule is internal/gui's rule: a symbol appears once, in a package-level
// var, resolved once in initObjC, and is never looked up again. objc.GetClass
// and objc.RegisterName both take the Objective-C runtime's global lock, and a
// yank under 'clipboard' must not pay for one.

// NSUTF8StringEncoding, from NSString.h. There is no header to import it from.
const nsUTF8StringEncoding = 4

// The two pasteboard type names.
//
// pbTypeString is NSPasteboardTypeString, the UTI every application on the
// system reads and writes; the old NSStringPboardType constant is an alias for
// the same string and needs no separate entry.
//
// pbTypeVim is vim's own, and it is not a guess: `strings` on
// /opt/homebrew/opt/macvim/MacVim.app/Contents/MacOS/Vim, which is the same
// build as the /opt/homebrew/bin/vim the oracle runs, has "VimPboardType" in
// it, and a copy in that vim leaves a type of that name on the general
// pasteboard holding a binary plist of [motion, text]. See motion.go for the
// measurements.
const (
	pbTypeString = "public.utf8-plain-text"
	pbTypeVim    = "VimPboardType"
)

// The Objective-C classes, looked up once.
var (
	classPasteboard objc.Class
	classString     objc.Class
	classNumber     objc.Class
	classArray      objc.Class
	classPool       objc.Class
)

// The selectors, registered once.
var (
	selAlloc   objc.SEL
	selRelease objc.SEL
	selInit    objc.SEL
	selDrain   objc.SEL

	selGeneralPasteboard objc.SEL
	selChangeCount       objc.SEL
	selClearContents     objc.SEL
	selStringForType     objc.SEL
	selSetStringForType  objc.SEL
	selPropListForType   objc.SEL
	selSetPropListForTyp objc.SEL

	selInitWithBytes   objc.SEL
	selLengthOfBytes   objc.SEL
	selGetCString      objc.SEL
	selInitWithInt     objc.SEL
	selIntValue        objc.SEL
	selInitWithObjects objc.SEL
	selCount           objc.SEL
	selObjectAtIndex   objc.SEL
	selIsKindOfClass   objc.SEL
)

// The two NSStrings naming the pasteboard types. They are made once and never
// released: there is one of each per process and they outlive everything that
// could hold them.
var (
	nameString objc.ID
	nameVim    objc.ID
)

var (
	objcOnce sync.Once
	objcErr  error
)

// initObjC loads AppKit and resolves every symbol this package uses. It runs
// once per process, and New calls it so that a box where AppKit will not load
// fails at startup with a name on it rather than on the first yank.
//
// It is lazy rather than an init function for the same reason internal/gui's
// is: importing this package on a machine with no window server, which every
// headless test and every --oracle run does, must cost nothing and load no
// framework.
func initObjC() error {
	objcOnce.Do(func() { objcErr = loadAppKit() })
	return objcErr
}

// loadAppKit is the body of initObjC, split out only so the once wrapper stays
// readable.
func loadAppKit() error {
	// NSPasteboard is AppKit's; NSString, NSNumber, NSArray and
	// NSAutoreleasePool are Foundation's, which AppKit links, but naming it
	// here means a failure says which framework did not load.
	for _, path := range []string{
		"/System/Library/Frameworks/Foundation.framework/Foundation",
		"/System/Library/Frameworks/AppKit.framework/AppKit",
	} {
		if _, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
			return fmt.Errorf("clip: loading %s: %w", path, err)
		}
	}

	for _, c := range []struct {
		name string
		into *objc.Class
	}{
		{"NSPasteboard", &classPasteboard},
		{"NSString", &classString},
		{"NSNumber", &classNumber},
		{"NSArray", &classArray},
		{"NSAutoreleasePool", &classPool},
	} {
		*c.into = objc.GetClass(c.name)
		if *c.into == 0 {
			return fmt.Errorf("clip: objc_getClass(%s) returned nil", c.name)
		}
	}

	selAlloc = objc.RegisterName("alloc")
	selRelease = objc.RegisterName("release")
	selInit = objc.RegisterName("init")
	selDrain = objc.RegisterName("drain")

	selGeneralPasteboard = objc.RegisterName("generalPasteboard")
	selChangeCount = objc.RegisterName("changeCount")
	selClearContents = objc.RegisterName("clearContents")
	selStringForType = objc.RegisterName("stringForType:")
	selSetStringForType = objc.RegisterName("setString:forType:")
	selPropListForType = objc.RegisterName("propertyListForType:")
	selSetPropListForTyp = objc.RegisterName("setPropertyList:forType:")

	selInitWithBytes = objc.RegisterName("initWithBytes:length:encoding:")
	selLengthOfBytes = objc.RegisterName("lengthOfBytesUsingEncoding:")
	selGetCString = objc.RegisterName("getCString:maxLength:encoding:")
	selInitWithInt = objc.RegisterName("initWithInt:")
	selIntValue = objc.RegisterName("intValue")
	selInitWithObjects = objc.RegisterName("initWithObjects:count:")
	selCount = objc.RegisterName("count")
	selObjectAtIndex = objc.RegisterName("objectAtIndex:")
	selIsKindOfClass = objc.RegisterName("isKindOfClass:")

	nameString = newNSString([]byte(pbTypeString))
	nameVim = newNSString([]byte(pbTypeVim))
	if nameString == 0 || nameVim == 0 {
		return fmt.Errorf("clip: could not make the pasteboard type names")
	}
	return nil
}

// withPool runs fn under an NSAutoreleasePool, on one OS thread from the push
// to the drain.
//
// Both halves of that are needed and the second one was found the hard way.
//
// The pool, because stringForType: and propertyListForType: hand back
// autoreleased objects and there is nothing this package can do about that. On
// the run loop thread AppKit's own pool would collect them; a Runner that is a
// plain call on whatever goroutine asked -- which is what a test passes -- has
// no pool at all, and the Objective-C runtime writes "autoreleased with no pool
// in place - just leaking" on stderr for every one. A pool per call costs two
// message sends and makes the two cases the same.
//
// The lock, because an NSAutoreleasePool belongs to the thread it was pushed
// on: the runtime keeps the pool stack in thread-local storage, and popping a
// pool from a thread that does not have it on its stack is undefined. A Go
// goroutine is not a thread and moves between them at any preemption point, so
// a push and a drain either side of a Send is a coin toss. It comes up tails:
// with the pool and no lock, `go test -bench Pasteboard -benchtime 1000x`
// segfaults in objc_autoreleasePoolPop about two runs in five, in drain, at
// address 0x10. With the lock, twenty runs of the same command are clean.
//
// It costs nothing anywhere it matters. Under gui.OnMain the work is already on
// the locked main thread and the lock is a counter increment; the count is
// nested, so the unlock puts the thread back where cmd/pvim's own
// runtime.LockOSThread left it rather than releasing it.
func withPool(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	pool := objc.ID(classPool).Send(selAlloc).Send(selInit)
	if pool != 0 {
		defer pool.Send(selDrain)
	}
	fn()
}

// newNSString wraps Go bytes in an NSString the caller owns and must release.
//
// initWithBytes:length:encoding: rather than stringWithUTF8String: for two
// reasons: the result is owned rather than autoreleased, so a caller in a test
// with no run loop leaks nothing, and a length is passed rather than inferred,
// so a register line holding a NUL byte survives instead of being cut short.
//
// The buffer is one byte longer than the text so that &buf[0] is always a valid
// pointer to Go memory, including for empty text, which unsafe.Pointer of an
// empty slice is not.
func newNSString(b []byte) objc.ID {
	buf := make([]byte, len(b)+1)
	copy(buf, b)
	s := objc.ID(classString).Send(selAlloc)
	return s.Send(selInitWithBytes, unsafe.Pointer(&buf[0]), len(b), nsUTF8StringEncoding)
}

// goBytes copies an NSString out as UTF-8.
//
// getCString:maxLength:encoding: writes into a Go buffer, which is a pointer
// going the safe way: Go memory handed to C for the length of one call. The
// alternative, reading the char* that UTF8String returns, needs a uintptr
// turned back into an unsafe.Pointer, which go vet refuses and is right to.
//
// The slice returned is buf[:n] and not the C string, so a NUL inside the text
// survives; getCString still NUL-terminates after it, which is why the buffer
// is n+1 long.
func goBytes(s objc.ID) []byte {
	if s == 0 {
		return nil
	}
	n := objc.Send[int](s, selLengthOfBytes, nsUTF8StringEncoding)
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n+1)
	if !objc.Send[bool](s, selGetCString, unsafe.Pointer(&buf[0]), n+1, nsUTF8StringEncoding) {
		return nil
	}
	return buf[:n]
}

// generalPasteboard is [NSPasteboard generalPasteboard], the one board the
// whole desktop shares. It is a singleton this package does not own and must
// not release.
func generalPasteboard() objc.ID {
	return objc.ID(classPasteboard).Send(selGeneralPasteboard)
}

// changeCount is the pasteboard's serial number: it goes up by one every time
// any process calls clearContents, and by nothing at all otherwise.
//
// This is the whole of how a read decides whether its own last write is still
// what is on the board. See Pasteboard.Read.
func changeCount(pb objc.ID) int { return objc.Send[int](pb, selChangeCount) }

// contents reads the text and vim's motion number off the pasteboard in one
// go, because they are one round trip and are useless apart.
func contents(pb objc.ID) ([]byte, int) {
	text := goBytes(pb.Send(selStringForType, nameString))
	return text, motionFrom(pb)
}

// motionFrom reads the first element of the VimPboardType plist, which is the
// charwise, linewise or blockwise number the pasteboard string cannot carry.
//
// Every step is guarded by isKindOfClass:, because the plist is written by
// another process and a wrong assumption about its shape is not a wrong answer,
// it is objc_msgSend into an object that does not have the selector, which
// takes the editor down. Anything unexpected reads as motionNone and the
// trailing-newline rule decides, which is what happens for every application
// that is not vim anyway.
func motionFrom(pb objc.ID) int {
	list := pb.Send(selPropListForType, nameVim)
	if list == 0 || !isKind(list, classArray) {
		return motionNone
	}
	if objc.Send[int](list, selCount) < 1 {
		return motionNone
	}
	first := list.Send(selObjectAtIndex, 0)
	if first == 0 || !isKind(first, classNumber) {
		return motionNone
	}
	return objc.Send[int](first, selIntValue)
}

// isKind is [obj isKindOfClass:c].
func isKind(obj objc.ID, c objc.Class) bool {
	return objc.Send[bool](obj, selIsKindOfClass, c)
}

// put writes the text and the motion number, and returns the changeCount the
// board is left at.
//
// clearContents first is not optional. Without it the previous owner's other
// representations survive -- an image, a file URL, another application's
// private type -- and whoever pastes next may take one of those instead of the
// string just written. It is also what moves the changeCount, which is what
// makes the next Read know the board is this package's own.
//
// The plist is [NSNumber, NSString], which is the array vim writes: measured
// off the real pasteboard after "*yy in /opt/homebrew/bin/vim, a binary plist
// of two elements whose first is 1 and whose second is "alpha\n".
func put(pb objc.ID, text []byte, motion int) (int, bool) {
	pb.Send(selClearContents)

	s := newNSString(text)
	if s == 0 {
		// Belt and braces: Write refuses text that is not valid UTF-8 before it
		// ever gets here, and that is the only reason initWithBytes: is
		// documented to answer nil. Passing the nil on to setString: would
		// raise an Objective-C exception, and there is no @catch in a Go
		// process.
		return changeCount(pb), false
	}
	defer s.Send(selRelease)
	if !objc.Send[bool](pb, selSetStringForType, s, nameString) {
		return changeCount(pb), false
	}

	num := objc.ID(classNumber).Send(selAlloc).Send(selInitWithInt, motion)
	defer num.Send(selRelease)
	items := [2]objc.ID{num, s}
	// initWithObjects:count: and not the variadic arrayWithObjects:, which
	// purego cannot call: a nil-terminated variadic list is not something
	// objc_msgSend can be given a signature for from Go.
	arr := objc.ID(classArray).Send(selAlloc).Send(selInitWithObjects, unsafe.Pointer(&items[0]), 2)
	defer arr.Send(selRelease)

	// A refused private type is not a failed write. The string is on the board
	// and every application including vim can read it; what is lost is the
	// charwise-or-linewise distinction on the way back, which is exactly the
	// state of affairs on every desktop that is not this one.
	objc.Send[bool](pb, selSetPropListForTyp, arr, nameVim)
	return changeCount(pb), true
}
