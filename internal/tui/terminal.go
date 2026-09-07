package tui

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/pkar/pvim/internal/screen"
)

// ErrNoTerminal is what Start answers when the file descriptors it was given
// are not a terminal, which is what happens under "go test" and under the
// oracle. It is a named error so that cmd/pvim can fall back rather than
// having to match a string.
var ErrNoTerminal = errors.New("tui: not a terminal")

// ErrNotRunning is Post called on a terminal that is not in a state to deliver
// an event: before Start, after Stop, and after a hangup has ended the decoder.
//
// It is a sentinel because the caller acts on it. A socket client whose request
// arrives while the editor is on its way out has to be told no and left to
// start its own editor; a caller that blocked instead would hold a shell open
// against an editor that is already gone.
var ErrNotRunning = errors.New("tui: the terminal is not running")

// readChunk is how many bytes one read off the terminal can return. A paste
// arrives in chunks of whatever the tty buffer holds and is reassembled by the
// input decoder, so this is a throughput number and not a correctness one.
const readChunk = 4096

// eventBuffer is the depth of the event channel. It is deep enough that the
// reader never blocks on a burst -- a paste that turns into one event, a
// held-down key, a mouse drag across the screen -- and shallow enough that a
// stalled editor stops the reader instead of growing without bound.
const eventBuffer = 64

// Terminal is one terminal in raw mode: the thing Run is built out of, and the
// thing a test drives directly.
//
// Run is the loop and this is the machinery, split apart so that the frame
// diff and the key decoding can be tested against an io.Writer and a byte
// slice with no tty anywhere.
type Terminal struct {
	// In and Out are the terminal's file descriptors. They are io interfaces
	// rather than *os.File so that a test can hand it a buffer; Drv is what
	// needs a real one, and it is a separate field for exactly that reason.
	In  io.Reader
	Out io.Writer

	// Drv is the operating system underneath: raw mode, the size, and
	// SIGWINCH. A nil Drv makes Start return ErrNoTerminal, because a
	// terminal that cannot be put into raw mode is not a terminal this
	// editor can use.
	Drv Driver

	// prev is the last frame drawn, which Render diffs against. A nil prev
	// means the next Render paints everything, which is what a resize and a
	// CTRL-L both want. It belongs to the goroutine that calls Render;
	// anything else that wants a full repaint sets the repaint flag below
	// instead, because a nil written from the pump goroutine is a data race
	// with no symptom until the day it drops a frame.
	prev *screen.Grid

	// events is the channel Events returns.
	events chan Event

	// raw carries bytes from the reader goroutine to the decoding one. Two
	// goroutines rather than one because a read off a tty blocks until a key
	// is pressed and the escape timeout has to fire while it is blocked.
	raw chan []byte

	// done is closed by Stop and is how both goroutines are told to leave.
	done chan struct{}

	// posted carries events injected by Post from goroutines that are not the
	// loop. It goes to pump rather than straight onto events because pump is
	// the only goroutine allowed to write to that channel: it closes it on the
	// way out, and a second writer racing that close is a panic rather than a
	// dropped event.
	//
	// Unbuffered on purpose. Post returns once the loop has the event, so the
	// backpressure a burst produces is felt by whoever is posting -- a -s
	// script typing faster than the editor reads -- and not by an unbounded
	// queue nobody is watching.
	posted chan Event

	// gone is closed by pump when it returns, which is the one moment after
	// which nothing will ever read posted again. Post selects on it, because a
	// hangup ends pump without closing done and a Post left waiting on that
	// would block until Stop, which may be a keystroke away.
	gone chan struct{}

	// wake nudges the decoding goroutine when the options changed under it.
	// Without it a loop that is waiting with no timeout armed -- both timeout
	// options off, a partial escape sequence pending -- would go on waiting
	// after the editor turned the timeout on, because nothing else was ever
	// going to arrive.
	wake chan struct{}

	// in decodes the byte stream. It belongs to the decoding goroutine.
	in input

	// out is the frame buffer, reused between frames so that a repaint of a
	// 200x60 screen is one allocation at startup and none afterwards, and one
	// Write to the terminal rather than one per span.
	out []byte

	// outMu serialises writes to Out. Two goroutines can reach it: the editor
	// painting a frame or setting a title, and the signal handler restoring
	// the terminal on its way out. Without this the restore sequence can land
	// in the middle of a frame and leave the terminal on the alternate screen
	// with the mouse still reporting, which is the exact state this package
	// exists to never leave behind.
	outMu sync.Mutex

	// mu guards the size, the options and the shutdown flags, which are the
	// state two goroutines touch.
	mu       sync.Mutex
	opt      Options
	rows     int
	cols     int
	started  bool
	stopping bool
	// stopDone is closed when the Stop that set stopping has finished putting
	// the terminal back, and stopErr is what that Stop returned. A second Stop
	// waits on the channel and answers with the error rather than returning
	// straight away, because "somebody else is handling it" and "it is already
	// handled" are not the same sentence and the difference between them is a
	// signal handler re-raising into a tty still in raw mode. See Stop.
	stopDone chan struct{}
	stopErr  error
	// repaint is a full repaint asked for from somewhere that does not own
	// prev: SIGWINCH, and whatever the handler calls Invalidate from.
	repaint bool

	// restore undoes Start's termios changes. Nil before Start.
	restore func() error

	// stopWinch ends the SIGWINCH subscription.
	stopWinch func()
}

// New returns a Terminal on a pair of file descriptors, with the driver for
// this operating system underneath it.
//
// It does nothing to the terminal: Start does that, and separating the two is
// what lets cmd/pvim build one, discover it is not on a tty, and fall back
// without having changed anything.
func New(in, out *os.File, o Options) *Terminal {
	return &Terminal{In: in, Out: out, Drv: NewDriver(in, out), opt: o}
}

// Options returns the option state the frontend is running under.
//
// It is behind a lock because the editor changes options while the decoding
// goroutine is reading them: ":set ttimeoutlen=5" and the vimrc's FastEscape
// autocmds, which set 'timeoutlen' on every InsertEnter and InsertLeave, both
// land here from the editor's goroutine while the reader is waiting on the
// timeout they decide.
func (t *Terminal) Options() Options {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.opt
}

// SetOptions replaces the option state, turning mouse reporting on or off if
// that is what changed.
//
// The mouse is the only one of these the terminal has to be told about: the
// timeouts are read fresh every time the reader waits, and 'ttyfast' and the
// bell are read where they are used. It is called from the editor's goroutine,
// which is the only one that writes to the terminal.
func (t *Terminal) SetOptions(o Options) {
	t.mu.Lock()
	was := t.opt
	t.opt = o
	started, stopping, wake := t.started, t.stopping, t.wake
	t.mu.Unlock()

	if wake != nil {
		select {
		case wake <- struct{}{}:
		default: // one pending nudge is as good as two
		}
	}
	if !started || stopping || t.Out == nil {
		return
	}
	switch {
	case mouseEnabled(o.Mouse) && !mouseEnabled(was.Mouse):
		t.write([]byte(seqMouseOn))
	case !mouseEnabled(o.Mouse) && mouseEnabled(was.Mouse):
		t.write([]byte(seqMouseOff))
	}
}

// Start puts the terminal into raw mode, switches to the alternate screen,
// turns on bracketed paste and, under 'mouse', SGR mouse reporting, and starts
// the reader goroutine that fills the event channel.
//
// The sequences it writes are in escape.go, each with the capture of vim that
// says it is the right one. The order is vim's order, and the first event on
// the channel is always a ResizeEvent, so a handler learns the size the same
// way whether it started or the window was dragged.
func (t *Terminal) Start() error {
	if t.Drv == nil || t.Out == nil || t.In == nil {
		return ErrNoTerminal
	}
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("tui: already started")
	}
	t.started = true
	t.mu.Unlock()

	opt := t.Options()

	restore, err := t.Drv.Raw()
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.restore = restore
	t.mu.Unlock()

	rows, cols, err := t.Drv.Size()
	if err != nil || rows <= 0 || cols <= 0 {
		// A terminal that took raw mode but will not say how big it is. VT100
		// dimensions are the answer every program that has ever met this has
		// given, and a SIGWINCH corrects it the first time the window moves.
		rows, cols = 24, 80
	}
	t.setSize(rows, cols)

	t.events = make(chan Event, eventBuffer)
	t.raw = make(chan []byte, 8)
	t.done = make(chan struct{})
	t.wake = make(chan struct{}, 1)
	t.posted = make(chan Event)
	t.gone = make(chan struct{})

	var buf []byte
	buf = append(buf, seqAltScreenOn...)
	buf = append(buf, seqTitlePush...)
	buf = append(buf, seqWrapOff...)
	buf = append(buf, seqPasteOn...)
	if mouseEnabled(opt.Mouse) {
		buf = append(buf, seqMouseOn...)
	}
	buf = append(buf, seqClear...)
	if err := t.write(buf); err != nil {
		t.Stop()
		return err
	}

	resized, stopWinch := t.Drv.Resized()
	t.mu.Lock()
	t.stopWinch = stopWinch
	t.mu.Unlock()

	t.events <- ResizeEvent{Rows: rows, Cols: cols}

	go t.read(t.raw, t.done)
	go t.pump(resized, t.raw, t.done, t.wake, t.posted, t.gone)
	return nil
}

// Post delivers ev to the loop as though the terminal itself had produced it.
//
// It is this package's half of what cmd/pvim's window pump is: the way a
// goroutine that is not the event loop gets something in front of the editor.
// Three things need it and all three were unreachable without it. A file
// handed over the instance socket from another shell has to be opened by the
// goroutine that owns the buffers, and here that goroutine is whichever one is
// inside Loop. A -s keystroke script has to be typed into a live editor rather
// than replayed around it. And a prompt reads its answer off Events, so a key
// posted here answers ":4,2d" exactly as a typed one does, with no second path
// through the editor to keep in step.
//
// It blocks until the loop has taken the event, and returns ErrNotRunning
// rather than blocking when there is no loop to take it: before Start, after
// Stop, and after a hangup. Order is kept -- one poster's events arrive in the
// order it posted them -- and a posted event is indistinguishable from a real
// one once it is on the channel, which is the point.
//
// It must not be called from inside the handler. The handler is the loop, and
// an event it is waiting to hand over is one nothing will take until it
// returns. Work the handler wants doing it can simply do.
func (t *Terminal) Post(ev Event) error {
	if ev == nil {
		return nil
	}
	t.mu.Lock()
	// Read, do not clear, for the same reason Stop reads these: they are the
	// channels the loop goroutines live on and nilling one from here is a data
	// race with no symptom until the day it drops an event.
	posted, done, gone := t.posted, t.done, t.gone
	live := t.started && !t.stopping
	t.mu.Unlock()

	if !live || posted == nil {
		return ErrNotRunning
	}
	select {
	case posted <- ev:
		return nil
	case <-done:
		return ErrNotRunning
	case <-gone:
		return ErrNotRunning
	}
}

// Stop puts everything Start changed back: the termios settings, the main
// screen, the cursor, bracketed paste and mouse reporting.
//
// It is safe to call twice, safe to call after a failed Start, and safe to
// call from a signal handler's goroutine, because the alternative is an editor
// that dies on the way out and leaves a shell with no echo. That is this
// package's worst failure and every path out of it -- a handler returning an
// error, a panic through Run's defer, a SIGTERM, a hangup -- lands here.
//
// A second Stop waits for the first one to finish and returns its error. The
// caller that makes that necessary is onFatalSignal, which restores and then
// re-raises the signal with the handler taken off: a Stop that saw the flag
// and answered nil at once would tell that handler the terminal was safe while
// the first Stop was still half way through writing the restore sequences, and
// the re-raise would kill the process before termios was put back. That is the
// raw-mode shell this whole path exists to prevent, and it is reachable
// whenever the editor is already on its way out when the SIGTERM lands.
//
// The cost is that a second caller blocks for as long as the first one does,
// and the first one writes to the terminal, which over a wedged ssh link is
// not instant. A shutdown that is slow and correct beats one that is fast and
// leaves the tty in raw mode.
func (t *Terminal) Stop() error {
	t.mu.Lock()
	if t.stopping {
		wait := t.stopDone
		t.mu.Unlock()
		<-wait
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.stopErr
	}
	t.stopping = true
	// stopDone is made here rather than in Start because Start's own error
	// path calls Stop, and so does a Stop after a Start that never reached
	// the point of making channels at all.
	stopDone := make(chan struct{})
	t.stopDone = stopDone
	// Read, do not clear. The two goroutines select on t.done for as long as
	// they live, and a Stop that nils the field out from under them is a data
	// race with no symptom until the day it drops a keystroke. The stopping
	// flag above is what makes this run once.
	done, stopWinch, restore := t.done, t.stopWinch, t.restore
	t.mu.Unlock()

	// However this leaves, a panic out of restore included: a waiter that
	// never wakes is a hung shutdown, which is worse than the error it would
	// otherwise have been handed.
	defer close(stopDone)

	if done != nil {
		close(done)
	}
	if stopWinch != nil {
		stopWinch()
	}

	var err error
	if t.Out != nil {
		var buf []byte
		if mouseEnabled(t.Options().Mouse) {
			buf = append(buf, seqMouseOff...)
		}
		buf = append(buf, seqPasteOff...)
		buf = append(buf, seqTitlePop...)
		buf = append(buf, seqReset...)
		buf = append(buf, seqCursorDefault...)
		buf = append(buf, seqWrapOn...)
		buf = append(buf, seqAltScreenOff...)
		buf = append(buf, seqCursorShow...)
		err = t.write(buf)
	}
	if restore != nil {
		if rerr := restore(); rerr != nil && err == nil {
			err = rerr
		}
	}

	t.mu.Lock()
	t.stopErr = err
	t.mu.Unlock()
	return err
}

// Events returns the channel every event arrives on: keys decoded from the
// byte stream, mouse reports, SIGWINCH as a ResizeEvent, and a hangup as a
// CloseEvent.
//
// The channel is closed when the terminal stops, so a "for ev:= range" over
// it is the whole event loop.
func (t *Terminal) Events() <-chan Event { return t.events }

// Size returns the terminal size in cells.
func (t *Terminal) Size() (rows, cols int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rows, t.cols
}

// write is the one way bytes reach the terminal.
func (t *Terminal) write(b []byte) error {
	if t.Out == nil || len(b) == 0 {
		return nil
	}
	t.outMu.Lock()
	defer t.outMu.Unlock()
	_, err := t.Out.Write(b)
	return err
}

// setSize records a new size.
func (t *Terminal) setSize(rows, cols int) {
	t.mu.Lock()
	t.rows, t.cols = rows, cols
	t.mu.Unlock()
}

// Render paints a screen, writing only the cells that changed since the last
// frame.
//
// The diff is the whole point and it is already written: screen.Grid.Diff
// returns the changed spans, so this walks them, moves the cursor with a CUP
// and writes the run. That is what makes a 40,000-line file scroll without
// flicker over SSH, and it is why the editor hands over a Screen rather than a
// list of drawing calls.
//
// One Write per frame. Half a frame on the wire is a torn screen, and a write
// per span is a syscall per span, which over ssh is a round trip per span.
func (t *Terminal) Render(s *screen.Screen) error {
	if s == nil {
		return nil
	}
	if t.Out == nil {
		return ErrNoTerminal
	}
	prev := t.prev
	if t.takeRepaint() {
		prev = nil
	}
	t.out = frame(t.out[:0], s, prev)
	if err := t.write(t.out); err != nil {
		return err
	}
	t.snapshot(&s.Grid)
	return nil
}

// snapshot keeps a copy of the frame just painted, because the next diff is
// against what is on the terminal and the editor is free to redraw its own
// grid in place.
func (t *Terminal) snapshot(g *screen.Grid) {
	if t.prev == nil || t.prev.Rows != g.Rows || t.prev.Cols != g.Cols {
		t.prev = screen.NewGrid(g.Rows, g.Cols)
	}
	copy(t.prev.Cells, g.Cells)
}

// Draw is Render under the name the Client interface uses, so that a
// *Terminal is a Client.
func (t *Terminal) Draw(s *screen.Screen) error { return t.Render(s) }

// SetTitle writes the OSC 2 sequence.
//
// Control characters are dropped rather than escaped: a title is a line of
// text from a file name, and the one thing that must not happen is a byte in a
// path ending the sequence early and leaving the rest of it on the screen.
func (t *Terminal) SetTitle(title string) error {
	if t.Out == nil {
		return nil
	}
	buf := make([]byte, 0, len(title)+len(seqTitleStart)+len(seqTitleEnd))
	buf = append(buf, seqTitleStart...)
	for _, r := range title {
		if r < 0x20 || r == 0x7f {
			continue
		}
		buf = append(buf, string(r)...)
	}
	buf = append(buf, seqTitleEnd...)
	return t.write(buf)
}

// Bell rings the terminal's bell, if the options ask for one.
//
// With the vimrc they do not: "set noerrorbells visualbell t_vb=" means an
// error flashes rather than beeps, and the flash is empty, so nothing happens
// at all. Options.Bell is 'errorbells' and not 'visualbell' folded into one
// answer, which leaves this the cheapest feature in the editor and leaves the
// option doing something rather than being documentation.
func (t *Terminal) Bell() {
	if !t.Options().Bell {
		return
	}
	t.write([]byte("\a"))
}

// Invalidate throws away the last frame so that the next Render paints every
// cell. A resize does this, and so does CTRL-L and ":redraw!" through
// Redrawer.
//
// It sets a flag rather than clearing prev because it is called from two
// goroutines -- the editor's, through Redrawer, and pump's, on a SIGWINCH --
// and prev belongs to whichever one calls Render.
func (t *Terminal) Invalidate() {
	t.mu.Lock()
	t.repaint = true
	t.mu.Unlock()
}

// takeRepaint reports whether a full repaint was asked for and clears the
// request, so that one Invalidate throws away one frame and not every frame
// after it.
func (t *Terminal) takeRepaint() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.repaint
	t.repaint = false
	return r
}

// read moves bytes off the terminal and onto the raw channel.
//
// It is its own goroutine because the read blocks: a terminal in raw mode with
// VMIN=1 returns only when a key is pressed, and the escape timeout has to
// fire while it is blocked. When the read fails -- a hangup, a closed
// descriptor, EOF under a test -- it sends nil, which pump turns into a
// CloseEvent.
//
// It can outlive Stop. A blocked read on a tty does not return because
// somebody closed a channel, and the only ways to unblock it are to close the
// descriptor, which belongs to whoever passed it in, or to press a key. The
// goroutine is idle, holds one buffer, and dies with the process; every
// terminal editor written in Go has this goroutine and none of them can end it
// either.
func (t *Terminal) read(raw chan<- []byte, done <-chan struct{}) {
	buf := make([]byte, readChunk)
	for {
		n, err := t.In.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			select {
			case raw <- chunk:
			case <-done:
				return
			}
		}
		if err != nil {
			select {
			case raw <- nil:
			case <-done:
			}
			return
		}
	}
}

// pump decodes the byte stream into events and applies the key-code timeout.
//
// The loop is the Escape ambiguity written out: decode everything complete,
// and if what is left is the start of something, wait for the rest of it for
// as long as 'ttimeoutlen' and then decide it was an Escape. With the vimrc's
// notimeout, ttimeout and ttimeoutlen=10 that wait is ten milliseconds; with
// both timeout options off there is no timer at all and a lone Escape waits
// for the next key, which is what vim does and what its documentation warns
// about.
func (t *Terminal) pump(resized <-chan struct{}, raw <-chan []byte, done, wake <-chan struct{}, posted <-chan Event, gone chan<- struct{}) {
	// gone before events, so that a Post which lost the race is answered with
	// ErrNotRunning rather than parked on a channel with no reader left.
	defer close(t.events)
	defer close(gone)

	var pending []byte

	// armed says the bytes left over are worth another timeout. A flush that
	// resolves nothing and consumes nothing -- the first byte of a multibyte
	// character and then silence, which is what a slow link does -- would
	// otherwise re-arm the timer every 'ttimeoutlen' forever. The next read
	// arms it again, because the next read is the thing that could change the
	// answer.
	armed := true

	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		evs, used := t.in.feed(pending, false)
		pending = consume(pending, used)
		for _, ev := range evs {
			if !t.emit(ev, done) {
				return
			}
		}

		var expired <-chan time.Time
		if len(pending) > 0 && armed {
			if d, ok := t.Options().keyCodeTimeout(); ok {
				if timer == nil {
					timer = time.NewTimer(d)
				} else {
					timer.Reset(d)
				}
				expired = timer.C
			}
		}

		select {
		case chunk, ok := <-raw:
			if !ok || chunk == nil {
				t.emit(CloseEvent{}, done)
				return
			}
			pending = append(pending, chunk...)
			armed = true
		case <-resized:
			t.onResize(done)
		case ev := <-posted:
			// Straight through: an injected event is one this terminal did
			// not decode, so there is nothing pending to reconsider and
			// nothing about the timeout that changed.
			if !t.emit(ev, done) {
				return
			}
		case <-expired:
			evs, used := t.in.feed(pending, true)
			pending = consume(pending, used)
			armed = used > 0 || len(evs) > 0
			for _, ev := range evs {
				if !t.emit(ev, done) {
					return
				}
			}
		case <-wake:
			// The options changed; go round and work out the timeout again.
			armed = true
		case <-done:
			return
		}

		if timer != nil {
			// No drain after this. Since Go 1.23 a Stop or a Reset on a timer
			// from NewTimer removes a value already sent, so the next Reset
			// cannot see the last fire; go.mod says go 1.26.
			timer.Stop()
		}
	}
}

// consume drops the first used bytes of b, keeping the rest at the front of
// the same backing array.
//
// A "b = b[used:]" would be shorter and would walk the backing array forward
// one keystroke at a time forever, so a session of a few hundred thousand
// keys reallocates the read buffer a few hundred thousand times. Copying the
// tail down is a handful of bytes moved per key and an allocation never.
func consume(b []byte, used int) []byte {
	if used >= len(b) {
		return b[:0]
	}
	return append(b[:0], b[used:]...)
}

// onResize asks the driver how big the terminal is now, throws the last frame
// away, and says so.
//
// The size is asked for rather than carried on the signal because a size
// delivered with the signal is already stale: dragging a window edge produces
// a stream of SIGWINCHes and only the last answer matters, and the driver
// keeps at most one pending notification for the same reason.
//
// It does all of that when the answer is the size that was already recorded
// too, and that is not a redraw per stray signal, it is vim's behaviour and it
// is load bearing. vim's set_shellsize calls screenclear() and update_screen()
// and never compares the new size against the old one; measured through a pty
// at 24x80, a SIGWINCH whose size did not change gets 1824 bytes out of vim
// 9.2.0321 beginning "\x1b[H\x1b[2J", a clear and a repaint of every row.
//
// The signal means the emulator did something to the screen, not that the
// number changed. Drag an edge in and back out inside one repaint and the
// rows the shrink cut off have already been discarded; attach a second tmux
// client at the same dimensions and the same thing happens with no drag at
// all. Diffing the next frame against a screen the terminal no longer has
// leaves those rows blank until something else happens to write to them, and
// there is no CTRL-L and no ":redraw" in this editor yet to ask for it.
func (t *Terminal) onResize(done <-chan struct{}) {
	rows, cols, err := t.Drv.Size()
	if err != nil || rows <= 0 || cols <= 0 {
		return
	}
	t.setSize(rows, cols)
	t.Invalidate()
	t.emit(ResizeEvent{Rows: rows, Cols: cols}, done)
}

// emit delivers one event, or reports that the terminal is stopping.
func (t *Terminal) emit(ev Event, done <-chan struct{}) bool {
	select {
	case t.events <- ev:
		return true
	case <-done:
		return false
	}
}

// assertClient is the compile-time check that a *Terminal really is a Client,
// and a Redrawer, which is the only thing keeping the definitions in step.
var (
	_ Client   = (*Terminal)(nil)
	_ Redrawer = (*Terminal)(nil)
)
