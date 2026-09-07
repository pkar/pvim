// Package gui is the AppKit frontend: purego calling objc_msgSend, an NSView
// subclass registered at runtime, and a CALayer blit of a raster-produced RGBA
// buffer. No C compiler is involved and none may become involved.
//
// This file is the part with no AppKit in it. It carries no build tag and must
// keep it that way: the event vocabulary and the Client contract are what the
// editor is written against, and they have to compile on a machine with no
// window server so that every package below the GUI stays testable. Everything
// that touches objc lives in a file tagged //go:build darwin.
//
// internal/tui is this package's peer, not its client. Neither imports the
// other; both take events in and a screen.Screen out, and the editor cannot
// tell which one it is talking to.
package gui

import (
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// Event is something that happened to the frontend. The set is closed: the six
// implementations below are all of it, and the marker method is unexported so
// no other package can add a seventh and defeat the type switch every consumer
// writes.
//
// Four of the six are internal/tui's too, name for name and field for field,
// and TestFrontendVocabulariesMatch in internal/deps_test.go fails if either
// side lets one drift. The asymmetries are deliberate and there are three of
// them: internal/tui has PasteEvent, where a paste arrives as bracketed-paste
// bytes rather than through a pasteboard, and this package has FocusEvent and
// ScaleEvent, which are facts about a window that a terminal either does not
// have or does not report.
type Event interface {
	event()
}

// KeyEvent is one keystroke, already resolved out of whatever the platform
// called it.
type KeyEvent struct {
	Key key.Key
}

// MouseButton names the button an event came from.
type MouseButton uint8

// The buttons. MouseNone is a wheel event, which has no button.
const (
	MouseNone MouseButton = iota
	MouseLeft
	MouseMiddle
	MouseRight
)

// MouseAction is what the mouse did.
type MouseAction uint8

// The mouse actions. Drag is a move with a button held; a move with no button
// held is not delivered, because nothing in this editor wants it.
const (
	MousePress MouseAction = iota
	MouseRelease
	MouseDrag
	MouseWheelUp
	MouseWheelDown
	MouseWheelLeft
	MouseWheelRight
)

// MouseEvent is a click, a drag or a wheel notch.
//
// Row and Col are 0-based grid cells, not pixels: the frontend divides by the
// cell size it already knows, and the editor never learns what a pixel is. That
// is also what lets the terminal frontend deliver the identical event from an
// SGR mouse report.
type MouseEvent struct {
	Button MouseButton
	Action MouseAction
	Row    int
	Col    int
	Mod    key.Mod
}

// ResizeEvent says the drawable area changed size, in cells. A font change and
// a window drag both produce one, and so does SIGWINCH in the terminal.
type ResizeEvent struct {
	Rows int
	Cols int
}

// CloseEvent says the frontend is going away: the red button, Cmd-Q through the
// menu, or a hangup on the terminal. The editor decides whether to obey; a
// modified buffer under 'confirm' prompts on the command line and may refuse.
type CloseEvent struct{}

// FocusEvent says the window became or stopped being the key window.
//
// The editor needs it for one visible thing and one invisible one. Visible:
// vim draws a hollow caret in a window that is not focused and a solid one in
// the window that is, which is raster.CursorHollow and is the only way to tell
// at a glance which of two editors a keystroke would go to. Invisible: 'autoread'
// and the checks vim does on regaining focus belong here when they are
// written, which they are not yet.
//
// internal/tui has no counterpart. A terminal can report focus -- it is the
// \x1b[?1004h mode -- and pvim does not ask for it, because the terminal
// emulator draws its own idea of an unfocused cursor over the top of whatever
// the editor drew and the two disagree in every terminal tried.
type FocusEvent struct {
	Focused bool
}

// ScaleEvent says the window moved to a display with a different
// backingScaleFactor: 1 on a plain display and 2 on a retina one.
//
// It is separate from ResizeEvent because the two are independent. Dragging a
// window from the built-in display to an external one usually changes the scale
// and leaves the cell count exactly where it was, since the cell grows by the
// same factor the buffer does, and that is precisely the case where nothing
// would otherwise tell the editor that every pixel it has cached is the wrong
// size. A frontend sends both when both changed.
//
// Scale is the new factor, an integer because a Face is built at an integer
// scale and macOS has never shipped a fractional backingScaleFactor.
type ScaleEvent struct {
	Scale int
}

func (KeyEvent) event()    {}
func (MouseEvent) event()  {}
func (ResizeEvent) event() {}
func (CloseEvent) event()  {}
func (FocusEvent) event()  {}
func (ScaleEvent) event()  {}

// Client is a live frontend, the handle the editor paints through.
//
// The editor calls these from its own goroutine and never from the platform's
// main thread. Getting the work onto whatever thread AppKit insists on is the
// implementation's problem and is the whole reason this is an interface and not
// a struct.
type Client interface {
	// Draw paints s. It may return before the pixels are on screen; it must not
	// hold a reference to s after it returns.
	Draw(s *screen.Screen) error

	// Size returns the current drawable size in cells, which is the same as the
	// last ResizeEvent delivered.
	Size() (rows, cols int)

	// SetTitle sets the window title. A no-op where there is no window.
	SetTitle(title string) error

	// Bell is called where vim would ring one. The vimrc sets t_vb= and
	// visualbell, so the real implementation does nothing, which is the
	// cheapest feature in this editor.
	Bell()
}

// Handler is the editor, seen from the frontend: one event in, whatever
// painting it decided on done through c, and an error to stop the loop.
//
// Events arrive one at a time and in order, so a Handler needs no locking and
// owns all editor state. Returning an error ends Run and Run returns it;
// returning nil after a CloseEvent is how a handler refuses to quit.
type Handler func(c Client, ev Event) error

// Run brings up the frontend and drives it until it quits.
//
// It takes over the calling goroutine's thread, which must be the process main
// thread with runtime.LockOSThread already called in an init: AppKit will not
// run anywhere else. The handler runs on a goroutine of Run's making, so the
// editor never touches AppKit and AppKit never blocks on the editor.
func Run(h Handler) error {
	return run(h)
}

// GUIRunning is this frontend's answer to has('gui_running'), and it is true.
//
// internal/tui declares the same constant false, for the same reason and in the
// same shape: internal/vimrc answers has() and may not import a frontend, so
// cmd/pvim is what carries the answer across. Three blocks of the vimrc hang on
// it -- the notimeout/ttimeout/ttimeoutlen=10 branch, the FastEscape augroup,
// and the <C-c>, <C-x> and <C-v> clipboard mappings -- and all three are meant
// to be off here and on in the terminal.
const GUIRunning = true

// Available reports whether this process could open a window.
//
// It answers false on a platform with no backend and false once Run has already
// been called, which are the two cases cmd/pvim has to decide between a window
// and the terminal on. It does not and cannot answer whether a window server
// will actually talk to this process: over ssh with no session, AppKit's
// frameworks load happily and NSApplication sharedApplication comes back nil,
// and the only way to find that out is to try. So a true here means "worth
// trying" and Run is still allowed to fail with a sentence.
func Available() bool { return available() }

// OnMain runs fn on the thread AppKit owns and returns when fn has returned.
//
// This is the exported half of the main-thread marshaller, and it exists for
// internal/clip: NSPasteboard is an AppKit object, every call to it goes on the
// run loop thread, and the alternative arrangement -- internal/clip importing
// this package -- would drag purego, internal/raster and internal/screen behind
// every core package that wanted a clipboard. So the pasteboard code talks to
// AppKit itself and takes this function as a plain func value from cmd/pvim,
// and the two packages never meet. internal/deps_test.go enforces that.
//
// Two rules, and getting either wrong is a wedged editor rather than an error.
// It must not be called from the run loop thread itself: it blocks until the
// work has run and the work cannot run while the thread that would run it is
// inside this call. And it is not a place for anything slow, because everything
// else the window does is behind it.
//
// It returns ErrNoMainThread when there is no run loop to get onto -- Run has
// not been called, or it has already returned -- rather than blocking forever,
// so a clipboard read during shutdown fails and the editor carries on shutting
// down.
func OnMain(fn func()) error { return onMain(fn) }

// PostMain queues fn onto the run loop thread and returns immediately.
//
// It is OnMain without the wait, for work whose answer nobody is holding: a
// window title, a bell that is not rung. Work posted this way runs in the order
// it was posted. Redraws do not go through it -- they coalesce, so the newest
// frame replaces the one nobody has blitted yet, which is a different rule and
// lives in Client.Draw.
func PostMain(fn func()) error { return postMain(fn) }

// Font is the face the window renders in, the 'guifont' the vimrc asks for.
//
// It is a package variable rather than a parameter to Run because the vimrc is
// parsed after the window is up and `:set guifont=` has to be able to change it
// later; setting it before Run picks the font the first frame is drawn in.
// After Run has been called, assigning to it does nothing: use SetFont, which
// is what relays the window out.
//
// It is equally not a record of what is in force. SetFont does not write back
// to it, so after a `:set guifont=` this still says what the FIRST frame was
// drawn in, and the live font lives on the window under the window's own lock.
// That is not tidiness: the editor goroutine is what changes the font and the
// AppKit thread is what reads it back when the window moves to a display at
// another scale, and a package variable across that seam is a data race with a
// font file in the middle of it.
var Font = raster.DefaultGUIFont

// SetFont changes 'guifont' while the editor is running.
//
// It is the run-time half of the Font variable and the two share one rule: the
// font in force is whatever was set last. Before Run it records the font the
// first frame will be drawn in; after Run it builds the new face, swaps it in
// and relays the grid out, which delivers a ResizeEvent even when the cell
// count did not change, because a window whose font changed has to be redrawn
// whatever its size did.
//
// It is called from the editor goroutine, which is where the font file is
// read; only the relayout crosses to the main thread. A font that cannot be
// loaded leaves the window in the one it has and returns the error, for the
// caller to put on the message line the way vim answers E596.
func SetFont(f raster.GUIFont) error { return setFont(f) }

// SetLinespace changes 'linespace', the extra device pixels between rows.
//
// Same contract as SetFont, and the same relayout: the row pitch is part of
// how many rows fit in the window, so changing it can change the grid.
func SetLinespace(n int) error { return setLinespace(n) }
