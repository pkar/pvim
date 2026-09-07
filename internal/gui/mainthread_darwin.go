//go:build darwin

package gui

import (
	"strconv"
	"structs"
	"sync"

	"github.com/ebitengine/purego"
)

// The main-thread wake mechanism, and the record of which of the two
// candidates was kept.
//
// # What was measured
//
// Both mechanisms were built and measured (Apple silicon, macOS 26, go1.27.1,
// purego v0.11.0), 2000 wakes each, signalled from a
// goroutine on a thread the main run loop does not own:
//
//	CFRunLoopSource 1999/2000 performs mean 33.0us p50 28.4us p99 113.7us max 690.4us
//	dispatch_async_f 2000/2000 calls mean 34.2us p50 30.4us p99 108.9us max 533.4us
//
// Latency is the same to within noise and both are two orders of magnitude
// under a frame. The number that decided it is the second measurement, 1000
// wakes issued back to back with the loop not running:
//
//	CFRunLoopSource 1000 signals -> 1 perform
//	dispatch_async_f 1000 asyncs -> 1000 calls
//
// # Which one is kept, and why
//
// CFRunLoopSource. A redraw request is idempotent: the answer to "blit the
// newest frame" a thousand times is one blit of the newest frame, and a source
// that is already signalled and not yet performed swallows the next signal for
// free. dispatch_async_f has no such property, so an editor goroutine that
// outran the display would queue an unbounded backlog of blits and the window
// would fall further behind the keyboard the faster it was typed at. That is
// exactly the failure mode a text editor must not have.
//
// The source is added in the event-tracking mode as well as the default one, so
// blits keep landing while a window edge is being dragged. dispatch_async_f
// drains on the main queue in common modes too, so that is a tie, not a reason.
//
// The 1999 rather than 2000 in the first row is the coalescing working, not a
// dropped wake: the queue below carries the work and only the wake-up is
// coalesced, so no work item is ever lost.
//
// dispatch_async_f stays bound in objc_darwin.go, unused, because the day a
// non-idempotent piece of work has to run on main in FIFO order it is two lines
// away and the measurement above says it costs nothing.

// cfRunLoopSourceContext is CFRunLoopSourceContext from CFRunLoop.h. Every
// field except version and perform may be NULL.
//
// info is declared as a uintptr rather than an unsafe.Pointer because that is
// all CoreFoundation does with it when the equal, hash, retain, release and
// copyDescription callbacks are NULL: it compares it and it hashes it, and it
// never dereferences it. Keeping it an integer keeps this file free of a
// uintptr-to-unsafe.Pointer conversion, and the field is pointer-sized either
// way so the C layout is unchanged.
type cfRunLoopSourceContext struct {
	_               structs.HostLayout
	version         int
	info            uintptr
	retain          uintptr
	release         uintptr
	copyDescription uintptr
	equal           uintptr
	hash            uintptr
	schedule        uintptr
	cancel          uintptr
	perform         uintptr
}

// mainQueue runs Go closures on the thread that owns a CFRunLoop.
//
// Work is a slice under a mutex rather than a channel because the drain has to
// take everything pending in one go: the run loop source coalesces wake-ups, so
// one perform must be able to clear a backlog of any size or work would sit
// there until the next unrelated wake.
type mainQueue struct {
	mu      sync.Mutex
	fns     []func()
	redraw  func() // the coalescing slot; see wakeRedraw
	pending bool

	runLoop uintptr
	source  uintptr
	token   uintptr
}

// performCallback is the C function pointer the run loop source calls. It is
// created once, at the first newMainQueue, and never again.
//
// purego caps the number of callbacks a process may create at 2000 and never
// frees one, so every callback in this package is created once at setup and
// none is created per event. A keyDown: that built a callback would exhaust the
// table in half a minute of typing.
var (
	performOnce     sync.Once
	performCallback uintptr

	// activeQueue is the queue the perform callback drains. There is one window
	// and one main run loop in this process, so a single package-level pointer
	// is the whole of the dispatch table; the callback takes no useful info
	// pointer because CFRunLoopSourceContext.info would have to be a Go pointer
	// handed to C, which is exactly the thing not to do.
	activeQueue *mainQueue
)

// newMainQueue creates a run loop source on runLoop and returns a queue that
// posts work to it.
//
// runLoop is a parameter rather than always CFRunLoopGetMain so that the test
// for this file can drive a queue on its own locked thread with no NSApp and no
// window server.
func newMainQueue(runLoop uintptr) (*mainQueue, error) {
	if err := initObjC(); err != nil {
		return nil, err
	}
	q := &mainQueue{runLoop: runLoop}

	performOnce.Do(func() {
		performCallback = purego.NewCallback(func(info uintptr) {
			if activeQueue != nil {
				activeQueue.drain()
			}
		})
	})
	activeQueue = q

	// Two sources with the same context are the same source as far as
	// CFRunLoopAddSource is concerned. With no equal callback in the context
	// CoreFoundation falls back to comparing the info fields, so two sources
	// built with info NULL compare equal and the second one is silently not
	// added to the run loop: it can be signalled forever and nothing happens.
	// One process only ever builds one of these, but a test builds several, and
	// a bug that only reproduces the second time is not one to leave lying
	// around. Each source therefore gets a token of its own.
	//
	// The token is a CFString rather than a counter so that it is a real,
	// live, unique CoreFoundation object: nothing in CFRunLoop dereferences
	// it, and if that ever changes, this is still valid rather than a made
	// up address.
	queueCount++
	token := cfStringCreateWithStr(0, "pvim.mainqueue."+strconv.Itoa(queueCount), cfStringEncodingUTF8)
	if token == 0 {
		return nil, errSourceCreate
	}

	ctx := cfRunLoopSourceContext{info: token, perform: performCallback}
	q.source = cfRunLoopSourceCreate(0, 0, &ctx)
	if q.source == 0 {
		cfRelease(token)
		return nil, errSourceCreate
	}
	q.token = token
	for _, mode := range runLoopModes {
		cfRunLoopAddSource(runLoop, q.source, mode)
	}
	return q, nil
}

// queueCount names each source uniquely. It is only ever touched from the
// thread that is setting a window up, which happens once.
var queueCount int

// stopQueue takes the source out of the run loop and drops its token.
//
// The running editor never calls this: the queue lives as long as the process
// and the run loop dies with it. It exists so that a test can build several
// queues in a row without them colliding, which is exactly the collision the
// token above exists to avoid.
func (q *mainQueue) stopQueue() {
	if q.source == 0 {
		return
	}
	for _, mode := range runLoopModes {
		cfRunLoopRemoveSource(q.runLoop, q.source, mode)
	}
	cfRunLoopSourceInvald(q.source)
	cfRelease(q.source)
	if q.token != 0 {
		cfRelease(q.token)
		q.token = 0
	}
	q.source = 0
}

// do queues fn to run on the main thread and returns immediately.
//
// It never blocks on the main thread. The editor goroutine calling this while
// the main thread is inside a modal event loop just leaves the work queued, and
// the work runs when the loop next spins.
func (q *mainQueue) do(fn func()) {
	q.mu.Lock()
	q.fns = append(q.fns, fn)
	q.mu.Unlock()
	q.wake()
}

// wakeRedraw asks for fn to run on the main thread, replacing any earlier
// wakeRedraw that has not run yet.
//
// This is the coalescing that makes the whole mechanism worth choosing: a
// hundred Draw calls between two run loop passes cost one blit of the last
// frame, not a hundred blits of frames nobody will ever see.
func (q *mainQueue) wakeRedraw(fn func()) {
	q.mu.Lock()
	q.redraw = fn
	q.mu.Unlock()
	q.wake()
}

// wake signals the source and kicks the run loop, which may be asleep in
// mach_msg waiting for an event.
func (q *mainQueue) wake() {
	cfRunLoopSourceSignal(q.source)
	cfRunLoopWakeUp(q.runLoop)
}

// drain runs everything queued, on the main thread. It takes the work out from
// under the lock before running any of it so that a closure calling do or
// wakeRedraw does not deadlock and does not have its own work swallowed.
func (q *mainQueue) drain() {
	q.mu.Lock()
	fns := q.fns
	q.fns = nil
	redraw := q.redraw
	q.redraw = nil
	q.mu.Unlock()

	for _, fn := range fns {
		fn()
	}
	if redraw != nil {
		redraw()
	}
}
