package tui

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/screen"
)

// fakeDriver is the operating system, faked. Raw mode cannot be tested under
// "go test": there is no tty and there is no way to make one without a pty, so
// the termios call is the one thing in this package with no test and
// everything above it is tested against this.
//
// It counts the restores rather than only recording them, because "restored
// once" and "restored twice" are different bugs and the second one is the one
// that unsets a terminal somebody else has since set up.
type fakeDriver struct {
	mu       sync.Mutex
	rows     int
	cols     int
	raws     int
	restores int
	rawErr   error
	winch    chan struct{}
	stopped  bool

	// restoreDelay makes the restore take a measurable amount of time, so
	// that a test can be inside the restore while it does something else. It
	// is zero everywhere except the concurrent-Stop test.
	restoreDelay time.Duration
	// entered is closed when a restore begins, which is how a test waits for
	// the window it wants to be in rather than sleeping and hoping.
	entered   chan struct{}
	enterOnce sync.Once
}

func newFakeDriver(rows, cols int) *fakeDriver {
	return &fakeDriver{
		rows:    rows,
		cols:    cols,
		winch:   make(chan struct{}, 1),
		entered: make(chan struct{}),
	}
}

func (d *fakeDriver) Raw() (func() error, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rawErr != nil {
		return nil, d.rawErr
	}
	d.raws++
	return func() error {
		d.mu.Lock()
		delay := d.restoreDelay
		d.mu.Unlock()
		d.enterOnce.Do(func() { close(d.entered) })
		if delay > 0 {
			time.Sleep(delay)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		d.restores++
		return nil
	}, nil
}

// restoring is closed once a restore has started and has not necessarily
// finished, which is the window a second Stop has to be made to wait through.
func (d *fakeDriver) restoring() <-chan struct{} { return d.entered }

func (d *fakeDriver) Size() (rows, cols int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rows, d.cols, nil
}

func (d *fakeDriver) Resized() (<-chan struct{}, func()) {
	return d.winch, func() {
		d.mu.Lock()
		d.stopped = true
		d.mu.Unlock()
	}
}

// resize changes the size the driver reports and signals it, which is what
// SIGWINCH does.
func (d *fakeDriver) resize(rows, cols int) {
	d.mu.Lock()
	d.rows, d.cols = rows, cols
	d.mu.Unlock()
	d.winch <- struct{}{}
}

func (d *fakeDriver) counts() (raws, restores int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.raws, d.restores
}

// winchStopped reports whether the SIGWINCH subscription was ended.
func (d *fakeDriver) winchStopped() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stopped
}

// vimrcOptions is the terminal half of the real ~/.vimrc: notimeout, ttimeout,
// ttimeoutlen=10 and mouse=a, which is the configuration every test here runs
// under because it is the one the editor runs under.
func vimrcOptions() Options {
	return Options{
		Timeout:     false,
		TTimeout:    true,
		TimeoutLen:  1000,
		TTimeoutLen: 10,
		Mouse:       "a",
		TTyFast:     true,
	}
}

// errDone is what a test handler returns to end the loop.
var errDone = errors.New("done")

// testTerminal returns a Terminal on a pipe and a buffer with a fake driver
// underneath, plus the writer that plays the part of somebody typing.
func testTerminal(t *testing.T, o Options) (*Terminal, *fakeDriver, *io.PipeWriter, *strings.Builder) {
	t.Helper()
	r, w := io.Pipe()
	d := newFakeDriver(24, 80)
	out := &strings.Builder{}
	term := &Terminal{In: r, Out: out, Drv: d, opt: o}
	t.Cleanup(func() { w.Close() })
	return term, d, w, out
}

// collect runs the loop until it has n events or the deadline passes, and
// returns them. The deadline is the test failing rather than hanging, which
// matters because everything here is a goroutine and a channel.
func collect(t *testing.T, term *Terminal, n int) []Event {
	t.Helper()
	var got []Event
	err := term.Loop(func(c Client, ev Event) error {
		got = append(got, ev)
		if len(got) >= n {
			return errDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDone) {
		t.Fatalf("Loop: %v", err)
	}
	return got
}

// TestStartAndStopBookendTheTerminal asserts the exact bytes, in order,
// because the order is vim's and because a terminal left with mouse reporting
// on or on the alternate screen is the failure this package exists to avoid.
func TestStartAndStopBookendTheTerminal(t *testing.T) {
	term, d, _, out := testTerminal(t, vimrcOptions())

	if err := term.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := seqAltScreenOn + seqTitlePush + seqWrapOff + seqPasteOn + seqMouseOn + seqClear
	if got := out.String(); got != want {
		t.Errorf("Start wrote\n %q\nwant %q", got, want)
	}
	if rows, cols := term.Size(); rows != 24 || cols != 80 {
		t.Errorf("Size = %d,%d, want 24,80", rows, cols)
	}

	out.Reset()
	if err := term.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want = seqMouseOff + seqPasteOff + seqTitlePop + seqReset + seqCursorDefault +
		seqWrapOn + seqAltScreenOff + seqCursorShow
	if got := out.String(); got != want {
		t.Errorf("Stop wrote\n %q\nwant %q", got, want)
	}
	if raws, restores := d.counts(); raws != 1 || restores != 1 {
		t.Errorf("raw mode entered %d times and left %d, want 1 and 1", raws, restores)
	}
	if !d.winchStopped() {
		t.Error("Stop left the SIGWINCH subscription in place")
	}
}

// TestStopIsIdempotent. Stop is called from a defer, from the signal handler
// and from Start's own error path, and more than one of those can happen.
func TestStopIsIdempotent(t *testing.T) {
	term, d, _, out := testTerminal(t, vimrcOptions())
	if err := term.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out.Reset()

	for i := 0; i < 3; i++ {
		if err := term.Stop(); err != nil {
			t.Fatalf("Stop %d: %v", i, err)
		}
	}
	if _, restores := d.counts(); restores != 1 {
		t.Errorf("three Stops restored the terminal %d times, want 1", restores)
	}
}

// TestASecondStopWaitsForTheFirstToFinishRestoring.
//
// Idempotence is not the same as ordering. The guard used to let a second Stop
// see the flag and answer nil while the first was still writing the restore
// sequences and had not called the termios restore at all, and the caller that
// acts on that answer is onFatalSignal: it restores, then re-raises the signal
// with the handler off. Told "already done" too early it kills the process
// with the tty still in raw mode, which is a shell with no echo.
//
// The assertion is the restore count at the moment the second Stop returns.
// One means it waited; zero means it believed the flag.
func TestASecondStopWaitsForTheFirstToFinishRestoring(t *testing.T) {
	term, d, _, _ := testTerminal(t, vimrcOptions())
	d.mu.Lock()
	d.restoreDelay = 100 * time.Millisecond
	d.mu.Unlock()

	if err := term.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	first := make(chan error, 1)
	go func() { first <- term.Stop() }()

	select {
	case <-d.restoring():
	case <-time.After(5 * time.Second):
		t.Fatal("the first Stop never reached the restore")
	}

	if err := term.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if _, restores := d.counts(); restores != 1 {
		t.Errorf("the second Stop returned with %d restores finished, want 1: it did not wait for the first", restores)
	}
	if err := <-first; err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if _, restores := d.counts(); restores != 1 {
		t.Errorf("the terminal was restored %d times, want 1", restores)
	}
}

// TestMouseIsOnlyTurnedOnWhenAskedFor: 'mouse' empty means a terminal that
// still lets the user select text with the mouse, which is what somebody who
// unset it wanted.
func TestMouseIsOnlyTurnedOnWhenAskedFor(t *testing.T) {
	o := vimrcOptions()
	o.Mouse = ""
	term, _, _, out := testTerminal(t, o)

	if err := term.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if strings.Contains(out.String(), "1006") {
		t.Errorf("mouse reporting turned on with 'mouse' empty: %q", out.String())
	}
	out.Reset()
	term.Stop()
	if strings.Contains(out.String(), "1006") {
		t.Errorf("mouse reporting turned off when it was never on: %q", out.String())
	}
}

// TestNoDriverIsNotATerminal is cmd/pvim's fallback path: built, discovered to
// be a pipe, and nothing changed on the way through.
func TestNoDriverIsNotATerminal(t *testing.T) {
	term := &Terminal{In: strings.NewReader(""), Out: &strings.Builder{}}
	if err := term.Start(); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("Start with no driver = %v, want ErrNoTerminal", err)
	}
	if err := term.Stop(); err != nil {
		t.Errorf("Stop after a failed Start = %v", err)
	}
}

// TestRawModeFailureIsReported. A file descriptor that is not a tty fails
// here, and the editor has to be able to tell that from a real error so it can
// fall back instead of dying.
func TestRawModeFailureIsReported(t *testing.T) {
	term, d, _, _ := testTerminal(t, vimrcOptions())
	d.rawErr = ErrNoTerminal
	if err := term.Start(); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("Start = %v, want ErrNoTerminal", err)
	}
}

// TestTheFirstEventIsAlwaysASize. A handler learns how big the screen is the
// same way at startup as it does when the window is dragged, so there is one
// path and not two.
func TestTheFirstEventIsAlwaysASize(t *testing.T) {
	term, _, _, _ := testTerminal(t, vimrcOptions())
	got := collect(t, term, 1)
	if len(got) != 1 {
		t.Fatalf("got %d events", len(got))
	}
	if ev, ok := got[0].(ResizeEvent); !ok || ev.Rows != 24 || ev.Cols != 80 {
		t.Errorf("first event is %#v, want ResizeEvent{24, 80}", got[0])
	}
}

// TestKeysReachTheHandler, through raw mode's own path: a read, a decode and a
// channel.
func TestKeysReachTheHandler(t *testing.T) {
	term, _, w, _ := testTerminal(t, vimrcOptions())
	go func() {
		w.Write([]byte("ab"))
		w.Write([]byte("\x1b[A"))
	}()

	got := collect(t, term, 4)
	if len(got) != 4 {
		t.Fatalf("got %d events: %#v", len(got), got)
	}
	if names := joined(keyNames(got[1:])); names != "a b <Up>" {
		t.Errorf("keys reached the handler as %q, want \"a b <Up>\"", names)
	}
}

// TestEscapeArrivesAfterTheTimeout is the vimrc's ten milliseconds, end to
// end: the byte goes in, nothing follows it, and the Escape comes out the
// other side without anything else being typed.
//
// This is the one test in the package that has to sleep, and it sleeps for as
// long as the vimrc says: 10ms.
func TestEscapeArrivesAfterTheTimeout(t *testing.T) {
	term, _, w, _ := testTerminal(t, vimrcOptions())
	go w.Write([]byte("\x1b"))

	start := time.Now()
	got := collect(t, term, 2)
	elapsed := time.Since(start)

	if len(got) != 2 {
		t.Fatalf("got %d events: %#v", len(got), got)
	}
	if names := joined(keyNames(got[1:])); names != "<Esc>" {
		t.Errorf("a lone Escape arrived as %q, want <Esc>", names)
	}
	if elapsed > time.Second {
		t.Errorf("the Escape took %v; 'ttimeoutlen' is 10ms and is not being read", elapsed)
	}
}

// TestNothingTimesOutWithBothOptionsOff is the other half of vim's table, and
// the behaviour its own documentation warns about: "if both options are off,
// Vim waits forever after an entered <Esc> if there are key codes that start
// with <Esc>. You will have to type <Esc> twice."
func TestNothingTimesOutWithBothOptionsOff(t *testing.T) {
	o := vimrcOptions()
	o.Timeout, o.TTimeout = false, false
	term, _, w, _ := testTerminal(t, o)

	go func() {
		w.Write([]byte("\x1b"))
		time.Sleep(30 * time.Millisecond)
		w.Write([]byte("[A"))
	}()

	got := collect(t, term, 2)
	if len(got) != 2 {
		t.Fatalf("got %d events: %#v", len(got), got)
	}
	if names := joined(keyNames(got[1:])); names != "<Up>" {
		t.Errorf("with no timeout the Escape resolved to %q, want <Up>: it timed out anyway", names)
	}
}

// TestResizeIsAskedForAndNotCarried. Dragging a window edge delivers a stream
// of SIGWINCHes and only the last size matters, so the signal carries nothing
// and the driver is asked.
func TestResizeIsAskedForAndNotCarried(t *testing.T) {
	term, d, _, _ := testTerminal(t, vimrcOptions())
	go func() {
		time.Sleep(time.Millisecond)
		d.resize(40, 120)
	}()

	got := collect(t, term, 2)
	if len(got) != 2 {
		t.Fatalf("got %d events: %#v", len(got), got)
	}
	ev, ok := got[1].(ResizeEvent)
	if !ok {
		t.Fatalf("second event is %#v, want a ResizeEvent", got[1])
	}
	if ev.Rows != 40 || ev.Cols != 120 {
		t.Errorf("resized to %d,%d, want 40,120", ev.Rows, ev.Cols)
	}
	if rows, cols := term.Size(); rows != 40 || cols != 120 {
		t.Errorf("Size after the resize = %d,%d, want 40,120", rows, cols)
	}
}

// TestASameSizeSIGWINCHIsStillAResize replaces a test that asserted the
// opposite of this, which was a guess about vim and was wrong.
//
// vim's set_shellsize calls screenclear() and update_screen() and never
// compares the size it was told about against the size it had. Measured
// through a pty at 24x80 against vim 9.2.0321: a SIGWINCH whose size did not
// change gets 1824 bytes back, beginning "\x1b[H\x1b[2J", a clear and a
// repaint of every row. The same signal into pvim as it was got zero bytes.
//
// The signal says the emulator did something to the screen, not that the
// number changed. "tmux attach" from a second client at the same dimensions is
// the everyday one and needs no drag and no burst of signals.
func TestASameSizeSIGWINCHIsStillAResize(t *testing.T) {
	term, d, w, _ := testTerminal(t, vimrcOptions())

	// The watchdog is what makes a regression a failure rather than a hang:
	// with no second ResizeEvent this loop would sit on the event channel
	// until the whole test binary timed out, so a keystroke arrives to end it
	// and the count below says what was missing.
	watchdog := time.AfterFunc(2*time.Second, func() { w.Write([]byte("x")) })
	defer watchdog.Stop()

	var resizes int
	err := term.Loop(func(c Client, ev Event) error {
		switch ev.(type) {
		case ResizeEvent:
			resizes++
			if resizes == 1 {
				d.resize(24, 80) // the size it already has, which is the point
				return nil
			}
			return errDone
		case KeyEvent:
			return errDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDone) {
		t.Fatalf("Loop: %v", err)
	}
	if resizes != 2 {
		t.Errorf("a SIGWINCH at the size already recorded produced %d resize events, want 2", resizes)
	}
}

// TestASameSizeSIGWINCHRepaintsEveryCell is the other half of the one above,
// and it is the half that matters: the event is worth nothing if the frame it
// produces is still a diff against what the terminal threw away.
//
// Drag a window edge in and back out in one gesture, or attach a second tmux
// client at the same size. The emulator discarded the rows the shrink cut off;
// t.prev still records them as being on screen; every later diff writes
// nothing for them and they stay blank for the rest of the session.
func TestASameSizeSIGWINCHRepaintsEveryCell(t *testing.T) {
	term, d, w, out := testTerminal(t, vimrcOptions())
	s := newTestScreen(24, 80)
	s.Grid.SetString(0, 0, "hello", screen.Normal)

	watchdog := time.AfterFunc(2*time.Second, func() { w.Write([]byte("x")) })
	defer watchdog.Stop()

	var frames []int
	err := term.Loop(func(c Client, ev Event) error {
		switch ev.(type) {
		case ResizeEvent:
			out.Reset()
			if err := c.Draw(s); err != nil {
				return err
			}
			frames = append(frames, out.Len())
			if len(frames) == 1 {
				d.resize(24, 80)
				return nil
			}
			return errDone
		case KeyEvent:
			return errDone
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDone) {
		t.Fatalf("Loop: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("painted %d frames, want 2: the same-size SIGWINCH produced no event to paint on", len(frames))
	}
	if frames[1] != frames[0] {
		t.Errorf("the frame after a same-size SIGWINCH wrote %d bytes where the first wrote %d: it diffed against a screen the terminal no longer has", frames[1], frames[0])
	}
}

// TestAHandlerCanForceARepaint. Invalidate is the only thing that throws the
// last frame away, and a handler holding a Client with no way to reach it has
// no answer at all to a screen something else corrupted: vim's two recovery
// paths are CTRL-L and ":redraw!", and both of them are a call to this.
//
// It is not on Client because internal/gui defines the same Client contract
// name for name and TestFrontendVocabulariesMatch in internal/deps_test.go
// fails if either side grows a method the other has not. Redrawer is how a
// handler asks a frontend that has a diff to throw away.
func TestAHandlerCanForceARepaint(t *testing.T) {
	var out strings.Builder
	term := &Terminal{Out: &out}
	s := newTestScreen(4, 8)
	s.Grid.SetString(0, 0, "hello", screen.Normal)

	var c Client = term
	if err := c.Draw(s); err != nil {
		t.Fatalf("first Draw: %v", err)
	}
	first := out.Len()

	r, ok := c.(Redrawer)
	if !ok {
		t.Fatal("a tui.Client cannot be asked for a repaint, so CTRL-L and :redraw have nothing to call")
	}
	out.Reset()
	r.Invalidate()
	if err := r.Draw(s); err != nil {
		t.Fatalf("Draw after Invalidate: %v", err)
	}
	if out.Len() != first {
		t.Errorf("Draw after Invalidate wrote %d bytes, want the full frame's %d", out.Len(), first)
	}
}

// TestHangupBecomesACloseEvent. The terminal going away is the editor being
// told to decide what to do about a modified buffer, not the process dying
// with the decision unmade.
func TestHangupBecomesACloseEvent(t *testing.T) {
	term, _, w, _ := testTerminal(t, vimrcOptions())
	go func() {
		time.Sleep(time.Millisecond)
		w.Close()
	}()

	got := collect(t, term, 2)
	if len(got) != 2 {
		t.Fatalf("got %d events: %#v", len(got), got)
	}
	if _, ok := got[1].(CloseEvent); !ok {
		t.Errorf("second event is %#v, want a CloseEvent", got[1])
	}
}

// TestAPanicStillRestoresTheTerminal. A terminal left in raw mode is this
// package's worst failure -- a shell with no echo, no line editing and no
// CTRL-C -- and a panic in the editor is the likeliest way to get one.
func TestAPanicStillRestoresTheTerminal(t *testing.T) {
	term, d, w, out := testTerminal(t, vimrcOptions())
	go w.Write([]byte("x"))

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not come back out of Loop")
			}
		}()
		term.Loop(func(c Client, ev Event) error {
			if _, ok := ev.(KeyEvent); ok {
				panic("the editor fell over")
			}
			return nil
		})
	}()

	if _, restores := d.counts(); restores != 1 {
		t.Errorf("the terminal was restored %d times after a panic, want 1", restores)
	}
	if !strings.HasSuffix(out.String(), seqAltScreenOff+seqCursorShow) {
		t.Errorf("the panic left the alternate screen up: %q", out.String())
	}
}

// TestPasteReachesTheHandlerWhole. It is one event and not a burst of
// keystrokes, which is what stops a pasted "jj" from leaving insert mode.
func TestPasteReachesTheHandlerWhole(t *testing.T) {
	term, _, w, _ := testTerminal(t, vimrcOptions())
	go func() {
		w.Write([]byte("\x1b[200~jj"))
		time.Sleep(time.Millisecond)
		w.Write([]byte(" and more\x1b[201~"))
	}()

	got := collect(t, term, 2)
	if len(got) != 2 {
		t.Fatalf("got %d events: %#v", len(got), got)
	}
	p, ok := got[1].(PasteEvent)
	if !ok {
		t.Fatalf("second event is %#v, want a PasteEvent", got[1])
	}
	if string(p.Text) != "jj and more" {
		t.Errorf("pasted %q, want %q", p.Text, "jj and more")
	}
}

// TestSetTitle writes OSC 2 and drops anything that could end it early.
func TestSetTitle(t *testing.T) {
	out := &strings.Builder{}
	term := &Terminal{Out: out}
	if err := term.SetTitle("main.go (internal/tui) - pvim"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	want := "\x1b]2;main.go (internal/tui) - pvim\a"
	if got := out.String(); got != want {
		t.Errorf("SetTitle wrote %q, want %q", got, want)
	}

	out.Reset()
	term.SetTitle("a\x1b]0;evil\ab")
	if got, want := out.String(), "\x1b]2;a]0;evilb\a"; got != want {
		t.Errorf("SetTitle with control characters wrote %q, want %q", got, want)
	}
}

// TestRenderThroughTheClientInterface: the editor is written against Client
// and hands the same Screen to whichever frontend it has, so the terminal has
// to paint through that interface and not through a method only it has.
func TestRenderThroughTheClientInterface(t *testing.T) {
	out := &strings.Builder{}
	var c Client = &Terminal{Out: out}
	s := newTestScreen(2, 4)
	s.Grid.SetString(0, 0, "ok", 0)
	if err := c.Draw(s); err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Errorf("Draw wrote %q, which does not hold the text", out.String())
	}
}

// TestTheVimrcNeverRingsTheBell. "set noerrorbells visualbell t_vb=" is three
// options that add up to silence, and this is the one place the sum of them
// can be observed.
func TestTheVimrcNeverRingsTheBell(t *testing.T) {
	out := &strings.Builder{}
	term := &Terminal{Out: out, opt: vimrcOptions()}
	term.Bell()
	if out.Len() != 0 {
		t.Errorf("the vimrc's options rang the bell: %q", out.String())
	}

	out.Reset()
	term.SetOptions(Options{Bell: true})
	term.Bell()
	if got := out.String(); got != "\a" {
		t.Errorf("'errorbells' on wrote %q, want a BEL", got)
	}
}

// TestUnsubscribingTheSignalHandlerDoesNotRestore. The handler is installed
// for the life of the loop and taken off at the end of it; taking it off must
// not look like the signal arriving, because that would restore a terminal the
// deferred Stop is about to restore again.
func TestUnsubscribingTheSignalHandlerDoesNotRestore(t *testing.T) {
	restored := 0
	stop := onFatalSignal(func() { restored++ })
	if stop == nil {
		t.Fatal("onFatalSignal returned no way to unsubscribe")
	}
	stop()
	stop2 := onFatalSignal(func() { restored++ })
	stop2()
	time.Sleep(time.Millisecond)
	if restored != 0 {
		t.Errorf("restore ran %d times with no signal sent", restored)
	}
}

// TestOptionsChangeUnderTheRunningLoop. ":set ttimeoutlen=" and the vimrc's
// FastEscape autocmds change the timeouts from the editor's goroutine while
// the reader is waiting on them, so the reader has to read them fresh rather
// than having taken a copy at startup.
//
// The vimrc's own FastEscape is inert, which is worth saying somewhere:
// "notimeout" means mappings never time out whatever 'timeoutlen' is, and
// "ttimeoutlen=10" being non-negative means key codes never consult
// 'timeoutlen' either, so the augroup's "set timeoutlen=0" and "set
// timeoutlen=1000" change nothing at all in this configuration. It is
// implemented anyway, because the day the vimrc drops "notimeout" is the day
// it starts mattering and not the day anybody would look here.
func TestOptionsChangeUnderTheRunningLoop(t *testing.T) {
	o := vimrcOptions()
	o.Timeout, o.TTimeout = false, false // nothing times out
	term, _, w, _ := testTerminal(t, o)

	go func() {
		w.Write([]byte("\x1b"))
		time.Sleep(5 * time.Millisecond)
		// The editor turns the key-code timeout on under the running loop.
		term.SetOptions(vimrcOptions())
	}()

	got := collect(t, term, 2)
	if len(got) != 2 {
		t.Fatalf("got %d events: %#v", len(got), got)
	}
	if names := joined(keyNames(got[1:])); names != "<Esc>" {
		t.Errorf("after ttimeout was turned on the Escape arrived as %q, want <Esc>", names)
	}
}

// TestSetOptionsTurnsTheMouseOffAndOn. ":set mouse=" mid-session has to reach
// the terminal, because the point of turning it off is getting the terminal's
// own selection back.
func TestSetOptionsTurnsTheMouseOffAndOn(t *testing.T) {
	term, _, _, out := testTerminal(t, vimrcOptions())
	if err := term.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer term.Stop()

	out.Reset()
	off := vimrcOptions()
	off.Mouse = ""
	term.SetOptions(off)
	if got := out.String(); got != seqMouseOff {
		t.Errorf("'mouse=' wrote %q, want %q", got, seqMouseOff)
	}

	out.Reset()
	term.SetOptions(off) // no change
	if out.Len() != 0 {
		t.Errorf("setting the same options again wrote %q", out.String())
	}

	out.Reset()
	term.SetOptions(vimrcOptions())
	if got := out.String(); got != seqMouseOn {
		t.Errorf("'mouse=a' wrote %q, want %q", got, seqMouseOn)
	}
}
