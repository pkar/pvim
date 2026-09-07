//go:build darwin

package gui

import (
	"fmt"
	"image"
	"sync"
	"sync/atomic"

	"github.com/ebitengine/purego/objc"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// The AppKit frontend. This is where the macOS window is built, and the file
// whose failure would have cost the project a window.
//
// The shape is fixed by two facts that cannot be negotiated with. AppKit runs
// on the process's first thread and nowhere else, and the editor is a
// single-goroutine state machine that must never be blocked by a window. So:
// main owns NSApp.run forever and touches no editor state; the editor owns a
// goroutine and touches no AppKit; events cross from main to the editor through
// eventq, and frames cross back through mainQueue. Neither side ever waits on
// the other.

// runOnce guards against a second Run. The view class registration, the shared
// NSApplication and the main run loop are all once-per-process things.
var runOnce sync.Once

// window is the whole of one pvim window: the AppKit objects, the frame buffers
// and the editor's view of the size.
//
// It implements Client. Every Client method is called from the editor goroutine
// and every one of them either touches only the mutex-guarded fields or posts a
// closure to the main thread.
type window struct {
	q    *eventq
	main *mainQueue

	nsApp    objc.ID
	nsWindow objc.ID
	view     objc.ID
	blit     *blitter

	mu sync.Mutex

	face raster.Face

	// font is the 'guifont' the face was built from, which the main thread
	// needs when the window moves to a display at another scale and has to
	// rebuild the face at the new one. It is here rather than read off the
	// package-level Font because that variable is written by the editor
	// goroutine and this is read by AppKit's, and a race with a font file in
	// the middle of it is a window with no glyphs.
	font raster.GUIFont

	// linespace is the vimrc's 'linespace' in device pixels, extra space
	// between rows. It is zero and stays zero until:set linespace exists,
	// but every place that turns pixels into rows already reads it, because
	// a row height that is right in three places and wrong in the fourth is
	// the classic off-by-one-row resize bug.
	linespace int

	scale int
	rows  int
	cols  int

	// front is the newest painted frame, waiting for the main thread to blit
	// it, and bufs is the pool it was painted into. CGImageCreate does not
	// copy, so a buffer the layer's contents point into is still being read by
	// the window server long after show() returned, and raster.Paint rewrites
	// every row, so painting into that buffer tears the whole grid and not a
	// rectangle of it.
	//
	// Three buffers, not two. Two would be enough if every Draw were followed
	// by a blit, but mainQueue.wakeRedraw exists to make sure it is not: N
	// Draws between two run loop passes are one blit of the newest, so a strict
	// alternation hands the second of them the buffer the layer is reading.
	// That is reachable with no exotic timing at all, since one wheel event
	// pushes several notches and the handler draws per event. At most two
	// buffers are in CoreGraphics' hands at once, shown and blitting, so a
	// third is always free.
	front    frameBuf
	bufs     [3]*image.RGBA
	bufW     int
	bufH     int
	next     int
	shown    *image.RGBA
	blitting *image.RGBA
	retired  []*image.RGBA

	// painted says what each buffer of the pool currently holds, which is what
	// makes a partial repaint safe: the diff is against the buffer this frame
	// is going into and not against the last frame drawn. See paint_darwin.go.
	painted [3]raster.Painted
	stats   frameStats

	// curRow and curCol are where the caret was drawn in the last frame. The
	// input method asks for it, in firstRectForCharacterRange:, to place the
	// accent popup, and asking the editor instead would mean blocking AppKit
	// on the editor goroutine.
	curRow int
	curCol int

	// lastMode is as much of the editor's mode as the last frame revealed. The
	// Edit menu reads it to pick between "+gP and CTRL-R CTRL-O +; see
	// menukeys.go for what a frontend can and cannot know about the mode.
	lastMode inputMode

	// bg is the Normal background last seen on a screen, used for the strip of
	// window a live resize exposes before the editor has repainted.
	bg    screen.RGB
	bgSet bool

	// mods is the modifier state from the last flagsChanged:, for the input
	// devices whose scroll events do not carry their own.
	mods uint

	// scroll accumulates sub-cell trackpad scrolling until it is worth a
	// notch. The arithmetic is in scroll.go, where it can be tested.
	scroll scrollState

	// ime is the input-method composition in flight. Main thread only, and
	// deliberately outside the mutex: see ime_darwin.go.
	ime imeState

	// dead is closed when NSApp.run has returned and nothing will ever drain
	// the main queue again. OnMain selects on it, so a clipboard read issued
	// while the window is going away fails instead of blocking the editor
	// goroutine forever on work that cannot run.
	dead chan struct{}
}

// run is the darwin implementation of Run.
func run(h Handler) error {
	var err error
	ran := false
	runOnce.Do(func() {
		ran = true
		started.Store(true)
		err = runWindow(h)
	})
	if !ran {
		return ErrAlreadyRunning
	}
	return err
}

// runWindow brings up the application, the window and the view, starts the
// handler goroutine and hands the thread to AppKit.
func runWindow(h Handler) error {
	if h == nil {
		return fmt.Errorf("gui: Run needs a handler")
	}
	if err := initObjC(); err != nil {
		return err
	}
	viewCls, err := registerViewClass()
	if err != nil {
		return fmt.Errorf("gui: registering the view class: %w", err)
	}
	delegateCls, err := registerDelegateClass()
	if err != nil {
		return fmt.Errorf("gui: registering the delegate class: %w", err)
	}

	mq, err := newMainQueue(cfRunLoopGetMain())
	if err != nil {
		return err
	}

	// NSApplicationActivationPolicyRegular is what gives a bare binary with no
	// .app bundle a Dock icon, a menu bar and the ability to become the active
	// application. Without it the window appears behind everything and never
	// takes focus, which looks exactly like a broken key handler.
	app := objc.ID(objc.GetClass("NSApplication")).Send(selSharedApplication)
	if app == 0 {
		return fmt.Errorf("gui: NSApplication sharedApplication returned nil, which means no window server for this process")
	}
	app.Send(selSetActivationPolicy, nsApplicationActivationPolicyRegular)

	delegate := objc.ID(delegateCls).Send(selAlloc).Send(selInit)
	// One object is both delegates. As the window's it answers the red button
	// and the focus notifications; as the application's it answers -terminate:,
	// which is how a Quit from the Dock menu and a logout arrive, and which
	// without an answer would end the process with the editor's own decision
	// still unmade.
	app.Send(selSetDelegate, delegate)
	buildMenu(app, delegate)

	// 640 by 480 logical points, which at Monaco 13pt's 8-point advance is 80
	// columns, the width this editor measures against MacVim.
	rect := NSRect{Size: NSSize{Width: 640, Height: 480}}
	const styleMask = nsWindowStyleMaskStandardWindow
	win := objc.ID(objc.GetClass("NSWindow")).Send(selAlloc)
	win = objc.Send[objc.ID](win, selInitWithContentRect, rect, styleMask, nsBackingStoreBuffered, false)
	if win == 0 {
		return fmt.Errorf("gui: NSWindow initWithContentRect: returned nil")
	}
	win.Send(selSetReleasedWhenClosed, false)
	win.Send(selSetTitle, nsString("pvim"))
	win.Send(selSetDelegate, delegate)

	view := objc.ID(viewCls).Send(selAlloc)
	view = objc.Send[objc.ID](view, selInitWithFrame, rect)
	// An autoresizing mask of width|height keeps the view filling the window,
	// which is what makes setFrameSize: fire on every resize.
	const nsViewWidthSizable, nsViewHeightSizable = 1 << 1, 1 << 4
	view.Send(selSetAutoresizingMask, nsViewWidthSizable|nsViewHeightSizable)
	view.Send(selSetWantsLayer, true)
	layer := view.Send(selLayer)
	if layer == 0 {
		return fmt.Errorf("gui: the view has no layer after setWantsLayer:")
	}

	w := &window{q: newEventq(), main: mq, nsApp: app, nsWindow: win, view: view, blit: newBlitter(layer), dead: make(chan struct{})}
	current = w

	scale := w.readScale()
	face, err := raster.NewFace(Font, scale)
	if err != nil {
		return fmt.Errorf("gui: loading %s: %w", Font.String(), err)
	}
	w.mu.Lock()
	w.face, w.scale, w.font = face, scale, Font
	w.mu.Unlock()

	// contentsScale tells CoreAnimation the image it is handed is already in
	// device pixels, so a retina window shows a 1280x960 image at 640x480
	// points instead of blowing up a 640x480 one. topLeft gravity keeps the
	// last frame pinned to the corner the text starts at while a resize is in
	// progress, rather than stretching it; nearest filtering means that even if
	// it were scaled, glyph edges would not be smeared.
	//
	// The two constants are passed as their literal contents rather than read
	// out of QuartzCore's data segment: kCAGravityTopLeft is the string
	// "topLeft" and CoreAnimation compares it by value, so building the string
	// here avoids a uintptr-to-pointer conversion for no loss at all.
	layer.Send(selSetContentsScale, float64(scale))
	layer.Send(selSetContentsGravity, nsString("topLeft"))
	layer.Send(selSetMagnificationFilter, nsString("nearest"))
	layer.Send(selSetMinificationFilter, nsString("nearest"))
	w.blit.setBackground(screen.DefaultNormal.BG.R, screen.DefaultNormal.BG.G, screen.DefaultNormal.BG.B)

	win.Send(selSetContentView, view)
	win.Send(selMakeFirstResponder, view)
	win.Send(selCenter)
	win.Send(selMakeKeyAndOrderFront, objc.ID(0))
	app.Send(selActivateIgnoringOtherApps, true)

	// The first event the editor ever sees is its size, before any key, so a
	// handler can allocate its screen in the ResizeEvent case and nowhere else.
	w.recount()

	var handlerErr error
	go func() {
		handlerErr = w.serve(h)
		w.main.do(w.stop)
	}()

	app.Send(selRun)
	// The run loop is gone, so nothing will drain the main queue again. Both
	// closes are what stops the two goroutines that could otherwise wait
	// forever: the handler, blocked in eventq.pop, and anything inside OnMain.
	close(w.dead)
	w.q.close()
	return handlerErr
}

// started is whether Run has been called in this process. It is read by
// Available from whatever goroutine cmd/pvim is on and written once, on main,
// so it is atomic rather than plain.
var started atomic.Bool

// available is the darwin implementation of Available.
//
// Two questions and not three. Can the frameworks be loaded at all, which is
// initObjC, and has this process already spent its one Run. It deliberately
// does not send sharedApplication: that has side effects on a process that then
// decides to run in the terminal instead, and the answer it would give -- nil
// on a box with no window server -- is the one thing Run reports properly with
// a sentence naming the cause.
func available() bool {
	if started.Load() {
		return false
	}
	return initObjC() == nil
}

// onMain is the darwin implementation of OnMain.
//
// The wait is a channel and not a WaitGroup so that it can be selected against
// w.dead: a run loop that has stopped will never run the closure, and the
// caller has to be told that rather than parked on it. The closure closes done
// with a defer, so a panic inside fn on the main thread still releases the
// caller before it takes the process down.
func onMain(fn func()) error {
	w := current
	if w == nil || w.main == nil {
		return ErrNoMainThread
	}
	select {
	case <-w.dead:
		return ErrNoMainThread
	default:
	}
	done := make(chan struct{})
	w.main.do(func() {
		defer close(done)
		fn()
	})
	select {
	case <-done:
		return nil
	case <-w.dead:
		return ErrNoMainThread
	}
}

// postMain is the darwin implementation of PostMain.
func postMain(fn func()) error {
	w := current
	if w == nil || w.main == nil {
		return ErrNoMainThread
	}
	select {
	case <-w.dead:
		return ErrNoMainThread
	default:
	}
	w.main.do(fn)
	return nil
}

// serve is the handler goroutine: pop, call, repeat, until the handler returns
// an error or the queue closes.
func (w *window) serve(h Handler) error {
	for {
		ev, ok := w.q.pop()
		if !ok {
			return nil
		}
		if err := h(w, ev); err != nil {
			return err
		}
	}
}

// stop ends NSApp.run. It must run on the main thread.
//
// -stop: alone is not enough: it sets a flag that -run checks after it finishes
// dispatching the current event, and if the queue is empty -run sits in
// mach_msg forever and never gets to the check. The application-defined event
// posted at the head of the queue is the standard way to give it something to
// finish dispatching.
func (w *window) stop() {
	w.nsApp.Send(selStop, objc.ID(0))
	ev := objc.Send[objc.ID](objc.ID(objc.GetClass("NSEvent")), selOtherEventWithType,
		uint64(nsEventTypeApplicationDefined),
		NSPoint{},
		uint64(0),
		float64(0), // timestamp; zero rather than a clock read, which this package is not allowed
		int64(0),
		objc.ID(0),
		int16(0),
		int64(0),
		int64(0),
	)
	if ev != 0 {
		w.nsApp.Send(selPostEventAtStart, ev, true)
	}
}

// Size returns the drawable size in cells.
func (w *window) Size() (rows, cols int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rows, w.cols
}

// SetTitle sets the window title.
func (w *window) SetTitle(title string) error {
	w.main.do(func() { w.nsWindow.Send(selSetTitle, nsString(title)) })
	return nil
}

// Bell does nothing, which is what the vimrc's `set visualbell t_vb=` asks for
// and the cheapest feature in this editor.
func (w *window) Bell() {}

// readScale returns the window's backingScaleFactor as an integer.
//
// It is 1 or 2 on every Mac shipped; a fractional value would mean a display
// mode this rasteriser has no answer for, and rounding down to 1 gives a
// readable window rather than a wrong one.
func (w *window) readScale() int {
	f := objc.Send[float64](w.nsWindow, selBackingScaleFactor)
	s := int(f)
	if s < 1 {
		s = 1
	}
	return s
}

// recount recomputes the grid size from the view's bounds and delivers a
// ResizeEvent when it changed. Main thread only.
//
// It reports whether it pushed one, which is what stops relayout pushing a
// second event for the same change: two events mean the editor handles the
// stale one last, and then the size it was told is not the size the window
// has. Measured against a live window before it was fixed -- ':set guifont='
// followed by ':set linespace=' left the handler holding a ResizeEvent saying
// 40 rows while Size() said 32.
func (w *window) recount() bool {
	b := objc.Send[NSRect](w.view, selBounds)

	w.mu.Lock()
	face, scale, linespace := w.face, w.scale, w.linespace
	w.mu.Unlock()
	if face == nil {
		return false
	}
	m := face.Metrics()

	// Bounds are logical points; the metrics are device pixels.
	wPx := int(b.Size.Width * float64(scale))
	hPx := int(b.Size.Height * float64(scale))
	rows, cols := cellsFor(wPx, hPx, m.CellW, m.CellH+linespace)

	w.mu.Lock()
	changed := rows != w.rows || cols != w.cols
	w.rows, w.cols = rows, cols
	w.mu.Unlock()

	if changed {
		w.q.push(ResizeEvent{Rows: rows, Cols: cols})
	}
	return changed
}

// resized is setFrameSize:'s hook.
func (w *window) resized() { w.recount() }

// backingChanged rebuilds the face after the window moved to a display with a
// different scale factor.
//
// Loading a font on the main thread blocks the window for the length of a file
// read, which is wrong in general and fine here: it happens when a window is
// dragged between displays and at no other time, and the alternative is a frame
// drawn at the wrong scale.
func (w *window) backingChanged() {
	scale := w.readScale()
	w.mu.Lock()
	same := scale == w.scale
	font := w.font
	w.mu.Unlock()
	if same {
		return
	}
	face, err := raster.NewFace(font, scale)
	if err != nil {
		// Keep the old face. A wrong-sized window beats no window.
		return
	}
	w.mu.Lock()
	w.face, w.scale = face, scale
	w.mu.Unlock()
	w.view.Send(selLayer).Send(selSetContentsScale, float64(scale))
	// The scale first and the size second, in that order and both of them:
	// dragging a window between two displays usually leaves the cell count
	// alone, so recount pushes nothing and this is the only thing that tells
	// the editor every pixel it has cached is now the wrong size.
	w.q.push(ScaleEvent{Scale: scale})
	w.recount()
}

// setMods records the modifier state from flagsChanged:.
func (w *window) setMods(flags uint) {
	w.mu.Lock()
	w.mods = flags
	w.mu.Unlock()
}

// pushKey queues one key for the editor.
func (w *window) pushKey(k key.Key) { w.q.push(KeyEvent{Key: k}) }

// onMenuAction is what an Edit menu item does: it types.
//
// The mode is the one the last painted frame showed, and menuKeys is where the
// bindings and their caveats live. A binding that is empty in this mode -- Cut
// in insert mode, Select All on the command line -- delivers nothing, which is
// what vim's own menu does when the item has no mapping for the current mode.
func (w *window) onMenuAction(a editAction) {
	w.mu.Lock()
	mode := w.lastMode
	w.mu.Unlock()
	w.pushKeys(menuKeys(a, mode))
}

// onKeyEquivalent is performKeyEquivalent:, which is the Cmd-key path.
//
// Returning false hands the event on to the next responder and eventually to
// the menu bar, which is what has to happen for the five keys the menu owns.
// Returning true consumes it, and consuming is also how a Cmd key this editor
// has no name for stops being a system beep: the vimrc sets 'visualbell' and
// t_vb= and there is no bell in this editor at all.
func (w *window) onKeyEquivalent(ev objc.ID) bool {
	flags := uint(objc.Send[uint64](ev, selModifierFlags))
	ch, ok := eventChar(ev)
	if !ok {
		return false
	}
	if menuOwnsKeyEquivalent(rune(ch), flags) {
		return false
	}
	w.setMods(flags)
	if k, ok := keyFromNS(rune(ch), flags); ok {
		w.pushKey(k)
	}
	return true
}

// onFocus tells the editor the window became or stopped being the key window,
// which is what draws vim's hollow caret.
func (w *window) onFocus(focused bool) { w.q.push(FocusEvent{Focused: focused}) }

// onMouse turns a click or a drag into a MouseEvent in grid cells.
//
// The click count is deliberately not carried. AppKit knows a double click is
// a double click -- NSEvent has -clickCount -- and MouseEvent has nowhere to
// put it, because internal/tui defines the identical struct and a terminal
// never reports one: xterm's SGR protocol has press, release and drag and
// nothing else, so terminal vim times the second press itself against
// 'mousetime'. Adding a field here would split the two frontends' vocabularies
// for a fact one of them cannot supply, so the second click arrives as a
// second press, a millisecond or two after the first, and the editor does the
// timing once for both frontends.
func (w *window) onMouse(ev objc.ID, button MouseButton, action MouseAction) {
	row, col := w.cellOf(ev)
	w.q.push(MouseEvent{
		Button: button,
		Action: action,
		Row:    row,
		Col:    col,
		Mod:    modFromNS(uint(objc.Send[uint64](ev, selModifierFlags))),
	})
}

// scrollMods is the modifier state to read a wheel event with.
//
// The event's own -modifierFlags is right on every device that fills it in,
// and the fallback is why flagsChanged: is implemented at all: some input
// devices report a scroll event with no modifier bits set, and the difference
// between a shift-wheel and a wheel is whether the buffer scrolls sideways or
// down. A flagsChanged: arrives on press and on release, so the recorded state
// is current.
func (w *window) scrollMods(ev objc.ID) uint {
	if flags := uint(objc.Send[uint64](ev, selModifierFlags)); flags&nsEventModifierFlagDeviceIndepen != 0 {
		return flags
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mods
}

// cellOf converts an event's window-relative location to a grid cell.
func (w *window) cellOf(ev objc.ID) (row, col int) {
	pt := objc.Send[NSPoint](ev, selLocationInWindow)
	local := objc.Send[NSPoint](w.view, selConvertPointFromView, pt, objc.ID(0))

	w.mu.Lock()
	face, scale, linespace, rows, cols := w.face, w.scale, w.linespace, w.rows, w.cols
	w.mu.Unlock()
	if face == nil {
		return 0, 0
	}
	m := face.Metrics()
	return cellAt(local.X, local.Y, scale, m.CellW, m.CellH+linespace, rows, cols)
}

// onScroll turns wheel notches and trackpad gestures into wheel events.
//
// The event is read here and the arithmetic is in scrollState.notches, which is
// where the shift swap, the two step sizes and the remainder live. A mouse
// wheel reports whole lines and a trackpad reports points; the editor gets a
// notch either way and decides for itself that a notch is three lines.
func (w *window) onScroll(ev objc.ID) {
	flags := w.scrollMods(ev)
	dx := objc.Send[float64](ev, selScrollingDeltaX)
	dy := objc.Send[float64](ev, selScrollingDeltaY)
	precise := objc.Send[bool](ev, selHasPreciseScrollingDeltas)

	w.mu.Lock()
	face, scale, linespace := w.face, w.scale, w.linespace
	w.mu.Unlock()
	if face == nil {
		return
	}
	m := face.Metrics()

	// A discrete wheel's delta is already in lines, so one is one notch. A
	// trackpad's is in logical points, so a notch is a cell.
	stepX, stepY := 1.0, 1.0
	if precise {
		stepX = float64(m.CellW) / float64(scale)
		stepY = float64(m.CellH+linespace) / float64(scale)
	}

	row, col := w.cellOf(ev)
	shift := flags&modFlagShift != 0
	mod := modFromNS(flags)
	if swapsAxes(dx, shift) {
		// The Shift has been spent turning the wheel sideways and must not be
		// delivered as well. macOS makes Shift the axis modifier for a scroll
		// gesture -- every AppKit app scrolls sideways under it -- and vim
		// makes Shift on a wheel a whole page, so leaving the bit on would
		// page the buffer sideways instead of nudging it six columns.
		mod &^= key.ModShift
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.scroll.notches(dx, dy, stepX, stepY, shift, func(action MouseAction) {
		w.q.push(MouseEvent{Button: MouseNone, Action: action, Row: row, Col: col, Mod: mod})
	})
}
