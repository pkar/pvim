// Package x11 is the Linux frontend: the X11 core protocol over a socket,
// spoken by github.com/jezek/xgb, with no C compiler and no Xlib.
//
// It is a peer of internal/gui's AppKit backend and not a port of it. Both
// take a screen.Screen in and put pixels on a window; both
// hand the editor the same gui.Event vocabulary; neither knows the other
// exists at run time, and cmd/pvim picks between them once at startup. The
// event union, the Client contract and the Handler signature are internal/gui's
// and are imported from there rather than restated, because a second
// declaration of KeyEvent is a second thing to keep in step and
// internal/deps_test.go already has two of those to police.
//
// # What runs where
//
// There is no AppKit here and therefore no main thread. X11 is a socket: any
// goroutine may write a request to it and xgb serialises them, so unlike
// internal/gui this package has no thread it is forbidden to call from.
//
// It still needs one owner goroutine, for two reasons that have nothing to do
// with threads. xgb's WaitForEvent cannot be selected against anything, so
// something has to sit in it; and a window that answers nothing while the
// editor is busy is a window the window manager declares hung, so whatever
// sits in it must not also be the editor. So: one goroutine reads events, one
// handles them, the editor owns a third, and frames cross between them the way
// they do on macOS. OnMain and PostMain are the handling one, kept under
// internal/gui's names so that the two frontends present one seam to cmd/pvim
// even though only one of them has a thread rule behind it.
//
// # PutImage, and the number that decides the design
//
// There is no shared framebuffer without MIT-SHM and no MIT-SHM without a
// file descriptor passed over the socket, which xgb does not do. So a frame is
// a PutImage: the pixels go over the socket, every time, and a full 1600x1200
// frame at 32 bits per pixel is 7.7 MB. On a local server that is a memcpy
// through a unix socket at the wheel rate and it is fine. Over an ssh -X
// forward it is 7.7 MB down a TCP connection per frame and it is not fine, and
// this package does not try to make it fine: ssh forwarding is out of scope,
// and a person who wants pvim on a remote box runs internal/tui over the ssh
// session it already has.
//
// What makes it tolerable locally is that a frame is not usually a full frame.
// raster.Damage gives the spans that changed and each span becomes its own
// PutImage of one text row, so a keystroke moves a few hundred bytes and not
// seven megabytes. See image.go for the chunking that keeps each of those
// requests inside the server's maximum request length, which is the one place
// this can go silently wrong: xgb writes a request length into a uint16 and
// truncates rather than refusing.
//
// # Wayland
//
// Out of scope and not in this package. A Wayland desktop runs Xwayland,
// which is an X server, and this talks to it like any other.
package x11

import (
	"errors"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/raster"
)

// The errors this package returns as sentinels, so a caller decides what to do
// by asking which one it got rather than by matching a sentence.
var (
	// ErrNoBackend is a platform with no X11 implementation compiled in, which
	// is everything that is not linux. cmd/pvim reads it the way it reads
	// gui.ErrNoBackend: use the terminal.
	ErrNoBackend = errors.New("x11: no X11 backend on this platform")

	// ErrAlreadyRunning is a second Run in one process. The window, the
	// connection and the event loop are once-per-process things.
	ErrAlreadyRunning = errors.New("x11: Run has already been called in this process")

	// ErrNoDisplay is DISPLAY unset or a server that refused the connection.
	// It is separate from ErrNoBackend because the fixes are different: this
	// one is a box with no session attached and that one is a build without
	// the backend in it.
	ErrNoDisplay = errors.New("x11: no X display to connect to")

	// ErrNoEventLoop is OnMain or PostMain with no event loop to get onto:
	// Run has not been called, or it has already returned. It is returned
	// rather than blocked on, so a clipboard read during shutdown fails and
	// the editor carries on shutting down.
	ErrNoEventLoop = errors.New("x11: no event loop to run on")
)

// GUIRunning is this frontend's answer to has('gui_running'), and it is true.
//
// internal/gui declares the same constant true and internal/tui false, for the
// same reason and in the same shape: internal/vimrc answers has() and may not
// import a frontend, so cmd/pvim is what carries the answer across.
//
// It being true here is not a formality. Three blocks of the vimrc hang on it,
// and on a Linux box in a window they have to be off exactly as they are off
// on macOS in a window: the notimeout/ttimeout/ttimeoutlen=10 branch, the
// FastEscape augroup, and the <C-c>, <C-x> and <C-v> clipboard mappings, all
// of which exist to work around a terminal and all of which are wrong here.
const GUIRunning = true

// Font is the face the window renders in, the 'guifont' the vimrc asks for.
//
// It follows internal/gui's variable exactly, including the part that reads
// like an oversight and is not: after Run has been called, assigning to it does
// nothing and it stops being a record of what is in force. SetFont is the
// run-time half. The reason is the same on both platforms, which is that the
// editor goroutine changes the font and the event loop reads it back, and a
// package variable across that seam is a data race with a font file in it.
//
// The default differs from internal/gui's, and that is the one substantive
// difference between the two frontends' fonts. Monaco is a macOS font, the
// vimrc names it, and it is not on a Linux box; DefaultFont resolves the name
// through this package's own search of ~/.fonts and /usr/share/fonts and falls
// back to DejaVu Sans Mono. See font.go.
var Font = raster.DefaultGUIFont

// Run brings up the window and drives it until it quits.
//
// Unlike internal/gui's Run it does not need the main thread and does not care
// which goroutine calls it: there is no AppKit here and no toolkit that has an
// opinion about threads. It still blocks until the window closes, and the
// handler still runs on a goroutine of Run's making, so the editor never blocks
// the event loop and the event loop never blocks the editor.
//
// cmd/pvim calling runtime.LockOSThread in an init for the macOS build costs
// nothing here.
func Run(h gui.Handler) error { return run(h) }

// Available reports whether this process could open a window.
//
// It answers false on a platform with no backend and false once Run has been
// called. On linux it answers whether DISPLAY is set to something, which is
// "worth trying" and not "will work": a DISPLAY naming a server that refuses
// the connection, or one whose authority cookie is missing, is a Run that fails
// with a sentence, and that is the only way to find out.
func Available() bool { return available() }

// OnMain runs fn on the goroutine that owns the X connection's event loop and
// returns when fn has returned.
//
// It carries internal/gui's name because cmd/pvim hands one of the two to
// internal/clip as a Runner and the two must be interchangeable. What it is
// for differs: on macOS the pasteboard is an AppKit object that must be touched
// from the run loop thread, and here it is that a reply to a SelectionRequest
// has to be written by whoever is reading the events that request arrived in.
//
// The same two rules apply and getting either wrong is a wedged editor rather
// than an error. It must not be called from the event loop goroutine itself,
// because it waits for work that goroutine has to run. And it is not a place
// for anything slow, because every other event is behind it.
//
// On linux internal/clip does not in fact use it: the clipboard there opens a
// connection of its own rather than sharing this one, for the reason spelled
// out in internal/clip's pasteboard_linux.go. It is here because the seam is
// the seam, and because a window that wants to put something on a selection
// itself has somewhere to do it from.
func OnMain(fn func()) error { return onMain(fn) }

// PostMain queues fn onto the event loop goroutine and returns immediately.
// It is OnMain without the wait, for work whose answer nobody is holding.
func PostMain(fn func()) error { return postMain(fn) }

// SetFont changes 'guifont' while the editor is running.
//
// Same contract as internal/gui's: it builds the new face, swaps it in and
// relays the grid out, which delivers a ResizeEvent even when the cell count
// did not change, because a window whose font changed has to be redrawn
// whatever its size did. A font that cannot be loaded leaves the window in the
// one it has and returns the error, for the caller to put on the message line
// the way vim answers E596.
func SetFont(f raster.GUIFont) error { return setFont(f) }

// SetLinespace changes 'linespace', the extra device pixels between rows. Same
// contract and the same relayout: the row pitch is part of how many rows fit.
func SetLinespace(n int) error { return setLinespace(n) }

// InForce is the 'guifont' the window is actually drawing in, and whether there
// is a window to ask.
//
// internal/gui has no counterpart and does not need one: on macOS a 'guifont'
// either loads or it does not, and a failure is an error the caller puts on the
// message line. Here it can succeed as something else. The vimrc asks for
// Monaco, a Linux box does not have Monaco, and ResolveFont answers with DejaVu
// Sans Mono rather than refusing to open a window over a font name -- so the
// family in force is not the family that was set, and `:set guifont?` has to be
// able to print the truth rather than the request.
//
// The second return is false before Run and after it, when there is no window
// and the answer would be a guess.
func InForce() (raster.GUIFont, bool) { return inForce() }
