// Package tui is the terminal frontend: raw mode, an alternate screen, 24-bit
// colour and a cell-by-cell diff against the last frame.
//
// It is internal/gui's peer, not its client. Neither imports the other, both
// take events in and a screen.Screen out, and the editor cannot tell which one
// it is talking to. That is what makes the purego gamble survivable: if AppKit
// never works, the editor still ships here, and daily use can start in the
// terminal rather than waiting on a window.
//
// The vocabulary below is deliberately the same as internal/gui's, name for
// name: Event, KeyEvent, MouseEvent, ResizeEvent, CloseEvent, Client, Handler,
// Run. It is duplicated rather than shared because a shared package would have
// to sit under both, and the one thing worse than two copies of four structs
// is internal/screen growing an opinion about frontends. The duplication is
// checked by TestFrontendVocabulariesMatch in internal/deps_test.go, which
// fails if either side grows an event, a field or a Client method the other
// has not.
//
// Two events here have no counterpart over there and each says why on itself:
// PasteEvent, because a paste reaches the window through the pasteboard, and
// WakeEvent, because the window's editor goroutine belongs to cmd/pvim and
// this one does not. Everything else matches name for name.
//
// # Two things this frontend has to get right that the window does not
//
// The Escape ambiguity. A bare Escape and the first byte of an arrow key are
// the same byte, and only time tells them apart. The vimrc sets notimeout,
// ttimeout and ttimeoutlen=10, which says: never time out a mapping, always
// time out a key code, and give it 10 milliseconds. internal/key's decoder
// already returns an explicit "need more input" for exactly this; this package
// is what puts a 10ms timer against it.
//
// has('gui_running'). It answers false here, which is not a detail: it is what
// makes the vimrc's FastEscape augroup and its <C-c>, <C-x> and <C-v>
// clipboard mappings take effect in the terminal and not in the window, which
// is how the config behaves.
package tui

import (
	"os"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/screen"
)

// GUIRunning is this frontend's answer to has('gui_running'), and it is false.
//
// It is a constant in this package because it is a fact about this package,
// and it is exported because the thing that has to know is nowhere near here:
// internal/vimrc's Env answers has() and cannot import a frontend, so cmd/pvim
// is what carries this across. Three blocks of the vimrc hang on it -- the
// notimeout/ttimeout/ttimeoutlen=10 branch, the FastEscape augroup, and the
// <C-c>, <C-x> and <C-v> clipboard mappings -- and all three are meant to be
// on here and off in the window.
const GUIRunning = false

// Event is something that happened to the frontend. The set is closed and the
// marker method is unexported, so a type switch over it is exhaustive.
type Event interface {
	event()
}

// KeyEvent is one keystroke, already decoded out of the byte stream.
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

// The mouse actions, which are the same set internal/gui delivers. SGR mouse
// reporting gives all of them.
const (
	MousePress MouseAction = iota
	MouseRelease
	MouseDrag
	MouseWheelUp
	MouseWheelDown
	MouseWheelLeft
	MouseWheelRight
)

// MouseEvent is a click, a drag or a wheel notch, in 0-based grid cells.
type MouseEvent struct {
	Button MouseButton
	Action MouseAction
	Row    int
	Col    int
	Mod    key.Mod
}

// ResizeEvent says the terminal changed size, in cells. It arrives from
// SIGWINCH and once at startup.
type ResizeEvent struct {
	Rows int
	Cols int
}

// PasteEvent is a bracketed paste: everything between the start and end
// sequences, delivered as one event rather than as keystrokes.
//
// It has no counterpart in internal/gui, where a paste arrives through the
// pasteboard instead. The editor treats it as an insert of literal text with
// no mappings applied, which is what stops a pasted "jj" from leaving insert
// mode.
type PasteEvent struct {
	Text []byte
}

// WakeEvent says something that is not the keyboard has work for the editor:
// it is what Post carries when the thing being injected is not a keystroke.
//
// It is the second of the two events internal/gui has no counterpart for, and
// the reason is the same shape as PasteEvent's. The window's editor goroutine
// is cmd/pvim's own pump, so work for it goes down the channel cmd/pvim
// already owns; the terminal's editor goroutine is whoever called Loop, and
// the only way in is an event. It carries nothing, because what has to be done
// is the caller's business and not this package's: the handler wakes, does
// whatever it queued for itself, and repaints.
type WakeEvent struct{}

// CloseEvent says the terminal is going away: a hangup, or the editor's own
// window being closed under it. The editor decides whether to obey.
type CloseEvent struct{}

func (KeyEvent) event()    {}
func (MouseEvent) event()  {}
func (ResizeEvent) event() {}
func (PasteEvent) event()  {}
func (WakeEvent) event()   {}
func (CloseEvent) event()  {}

// Client is a live frontend, the handle the editor paints through. It is the
// same contract internal/gui's Client has, minus nothing and plus nothing.
type Client interface {
	// Draw paints s. It may return before the bytes have reached the
	// terminal; it must not hold a reference to s after it returns.
	Draw(s *screen.Screen) error

	// Size returns the current size in cells, which is the same as the last
	// ResizeEvent delivered.
	Size() (rows, cols int)

	// SetTitle sets the terminal's title through the OSC 2 sequence, which
	// every terminal this editor will run in supports and which does nothing
	// harmful in the ones that do not.
	SetTitle(title string) error

	// Bell is called where vim would ring one. The vimrc sets t_vb= and
	// 'visualbell', so the real implementation does nothing.
	Bell()
}

// Redrawer is a Client that can be told to forget what it has on screen, so
// that the next Draw paints every cell.
//
// It is not part of Client and cannot be. internal/gui defines the same Client
// contract name for name and TestFrontendVocabulariesMatch in
// internal/deps_test.go fails if either side grows a method the other has not,
// and a window has no frame diff to throw away in the first place. So a
// handler asks, and a frontend that has nothing stale to fix does not answer:
//
//	if r, ok:= c.(tui.Redrawer); ok {
//		r.Invalidate()
//	}
//
// vim has two ways to ask for this, CTRL-L and ":redraw!", and both of them
// are this call. Neither is written yet -- CTRL-L belongs to internal/mode and
// ":redraw" to internal/ex -- and until one of them is, a terminal whose
// screen something else corrupted has no way back. That is the whole reason
// this interface is exported rather than being a method nobody outside this
// package can reach.
type Redrawer interface {
	Client

	// Invalidate throws away the record of what is on screen. The next Draw
	// writes every cell rather than the cells that changed.
	Invalidate()
}

// Handler is the editor, seen from the frontend: one event in, whatever
// painting it decided on done through c, and an error to stop the loop.
//
// Events arrive one at a time and in order, so a Handler needs no locking and
// owns all editor state. Returning an error ends Run and Run returns it;
// returning nil after a CloseEvent is how a handler refuses to quit.
type Handler func(c Client, ev Event) error

// Run brings the terminal up and drives it until the handler stops.
//
// Unlike internal/gui's Run it does not need the main thread and does not take
// it over: there is no AppKit here and no reason to. It does need a terminal
// on the file descriptors it was given, and it restores the terminal state on
// the way out however it leaves, including through a panic, because a raw-mode
// editor that dies without restoring leaves a shell nobody can type into.
//
// It runs on os.Stdin and os.Stdout. Not stderr: a message written to stderr
// while the alternate screen is up should land in the shell's scrollback where
// it can be read after the editor exits, and it will, because stderr was never
// redirected here.
func Run(h Handler, o Options) error {
	return New(os.Stdin, os.Stdout, o).Loop(h)
}

// Loop is Run against a Terminal that already exists, which is what a test and
// cmd/pvim's fallback path both want.
//
// Every way out restores the terminal. A handler that returns an error and one
// that panics both go through the deferred Stop; a SIGTERM or a hangup goes
// through onFatalSignal, which restores and then re-raises so the process
// still dies of the signal it was sent. os.Exit is the one exit this cannot
// cover, which is why nothing under this package calls it.
func (t *Terminal) Loop(h Handler) error {
	if err := t.Start(); err != nil {
		return err
	}
	// The handler is subscribed first so that it is unsubscribed last: defers
	// run in reverse, so the Stop below runs while the handler is still
	// installed, and the handler comes off only once the terminal is already
	// back. The other order leaves a window between the unsubscribe and the
	// restore where a SIGTERM kills the process with its default disposition
	// and nothing puts termios back at all.
	defer onFatalSignal(func() { t.Stop() })()
	defer t.Stop()

	for ev := range t.Events() {
		if err := h(t, ev); err != nil {
			return err
		}
	}
	return nil
}

// Options are what the frontend needs out of the option state.
//
// A struct of the handful of options this package reads, filled by the caller
// from an *options.Options, rather than the whole thing: the frontend is the
// one place that should be constructible in a test with six fields and no
// editor.
type Options struct {
	// Timeout, TTimeout, TimeoutLen and TTimeoutLen are the Escape ambiguity,
	// and are why this struct exists. The vimrc sets notimeout, ttimeout and
	// ttimeoutlen=10 in its !has('gui_running') branch. All four are here
	// because vim's rule reads all four; see keyCodeTimeout, which quotes the
	// two tables it comes from.
	Timeout     bool
	TTimeout    bool
	TimeoutLen  int
	TTimeoutLen int
	// Mouse is 'mouse'. Any non-empty value turns SGR mouse reporting on; the
	// vimrc sets "a".
	Mouse string
	// TTyFast is 'ttyfast'. It is carried because the caller has it and
	// because a frontend that grew a slow path would read it here, and it
	// changes nothing: vim uses it to decide whether to scroll the
	// terminal in hardware rather than repaint, and this renderer has no
	// hardware scroll to decide about. It sends the diff and only the diff,
	// which is what 'ttyfast' would have asked for.
	TTyFast bool
	// Bell is 'errorbells' and 'visualbell' together: with the vimrc's
	// settings there is no bell of either kind, ever.
	Bell bool
}

// OptionsFrom fills the frontend's options out of the editor's.
func OptionsFrom(o *options.Options) Options {
	if o == nil {
		return Options{}
	}
	return Options{
		Timeout:     o.G.Timeout,
		TTimeout:    o.G.TTimeout,
		TimeoutLen:  o.G.TimeoutLen,
		TTimeoutLen: o.G.TTimeoutLen,
		Mouse:       o.G.Mouse,
		TTyFast:     o.G.TTyFast,
		Bell:        o.G.ErrorBells && !o.G.VisualBell,
	}
}
