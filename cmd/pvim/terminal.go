package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/pkar/pvim/internal/clip"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/server"
	"github.com/pkar/pvim/internal/tui"
)

// The terminal frontend: pvim as the editor you actually type into.
//
// This is the gate. Everything before it was graded by a harness; this
// is the first path where a person presses a key and looks at the answer, and
// the exit condition is a working day inside it.
//
// It owns no editing and no drawing. internal/tui owns raw mode, the byte
// decoding, the escape timeout and the frame diff; the editor owns the buffer;
// draw.go composites. What is left, and what is here, is the loop between
// them.
//
// # Why this file has a pump in it too
//
// The editor goroutine here is whichever one called tui.Terminal.Loop, and
// three things have to reach it from outside: a file handed over the instance
// socket by a shell in another tmux pane, a -s keystroke script, and the
// answer a prompt is waiting for. tui.Terminal.Post is the seam -- it puts an
// event on the same channel the keyboard's events arrive on -- and
// terminalPump below is the small amount of bookkeeping that turns "run this
// and tell me what it said" into an event and back.
//
// It is smaller than window.go's pump and could not be much bigger, because
// the terminal has the thing the window does not: an event channel anybody can
// read. A prompt takes its own key straight off it, so there is no coroutine
// handshake and no errMoreInput here at all.

// runTerminal opens the file in the terminal pvim was started from.
func runTerminal(c config) error {
	// The terminal is built before it is started so that its size can be asked
	// for and the editor laid out at the right size from the first frame. A
	// terminal that comes up at 40 by 120 and resizes to the real size on the
	// first SIGWINCH paints one wrong screen, which is one screen too many.
	t := tui.New(os.Stdin, os.Stdout, tui.Options{})
	if r, cl, err := t.Drv.Size(); err == nil && r > 0 && cl > 0 {
		c.rows, c.cols = r, cl
	}

	// has('gui_running') is false here, which is not a detail: it is what makes
	// the vimrc's FastEscape augroup and its <C-c>, <C-x> and <C-v> clipboard
	// mappings take effect, exactly as they do in terminal vim.
	ed, err := newFrontendEditor(c, tui.GUIRunning)
	if err != nil {
		return err
	}
	defer ed.persistOnQuit()
	return ed.driveTerminal(t, c)
}

// driveTerminal is everything after the editor exists: the clipboard, the
// socket, the script and the loop.
//
// It is split out from runTerminal so that a test can drive the whole of it
// over a fake driver and a pipe, which is what makes the socket half gradeable
// at all: there is no tty under "go test" and the terminal this frontend needs
// is the one thing internal/tui cannot fake for it.
func (e *editor) driveTerminal(t *tui.Terminal, c config) error {
	t.SetOptions(tui.OptionsFrom(e.opt))

	p := &terminalPump{ed: e, term: t}

	// A prompt reads its answer off the same event stream the command came
	// from, which in the terminal means one more event out of the channel the
	// loop is reading. That is safe because the events come from another
	// goroutine, and it is what makes ":4,2d" answerable at all. The window
	// has no such channel and answers this with a coroutine instead; see
	// window.go.
	e.sess.getch = p.getch

	// "pvim -s keys file" types the script into the live terminal and then
	// hands the keyboard back, which is vim's own -s. The keys go in through
	// Post, so they arrive as events, in order, behind whatever the terminal
	// has already decoded -- the same path a person's keys take, with no
	// second way into the editor to keep in step. A script that cannot be read
	// is fatal before raw mode is entered rather than a message on a line
	// nobody is looking at yet.
	if c.keys != "" {
		keys, kerr := readScript(c.keys)
		if kerr != nil {
			return kerr
		}
		p.script = keys
	}

	// "* and "+ become the desktop pasteboard, which is what the vimrc's
	// clipboard=unnamed,unnamedplus,autoselect has been asking for all along and
	// getting nothing from in this frontend until now.
	defer e.installTerminalClipboard()()

	// The pump is stopped before the clipboard thread and after the listener,
	// which is the order these defers run in: stop accepting requests, answer
	// the ones already in hand so no shell is left waiting on an editor that
	// has gone, then let the thread go.
	defer p.stop()

	// The socket, bound before raw mode, so that a shell racing the launch
	// finds an instance rather than starting a second one. See instance.go for
	// which process ends up on which side of it.
	defer e.serve(socketPath(), c, func(req server.Request) (<-chan struct{}, error) {
		return e.request(p.do, req)
	})()

	err := t.Loop(p.event)
	if errors.Is(err, errQuit) {
		return nil
	}
	return err
}

// terminalPump is the terminal's editor goroutine seen from outside: the queue
// a socket request or a script key joins, and the wake that runs it.
//
// The queue is a slice under a mutex and not a channel, because the wake and
// the work travel separately: Post carries a tui.WakeEvent with nothing in it,
// and the loop picks up whatever is waiting when it gets there. One wake may
// therefore find several requests, and a later wake may find none, and both are
// fine -- a wake that finds nothing is a repaint.
type terminalPump struct {
	ed   *editor
	term *tui.Terminal

	// script is the -s keystroke file, waiting for the loop to be running.
	// Only the handler goroutine touches it: it is drained by the first event
	// to reach event, which is the ResizeEvent Start always sends.
	script []key.Key

	// typing is the goroutine playing that script, waited for by stop so that
	// nothing outlives the editor.
	typing sync.WaitGroup

	mu   sync.Mutex
	q    []*termWork
	gone bool
}

// termWork is one thing for the editor goroutine to do and the answer channel
// it owes exactly one value on. The channel is buffered so that a caller that
// gave up -- a socket client that hung up -- cannot wedge the loop.
type termWork struct {
	work  func(client) error
	reply chan error
}

// do hands work to the editor goroutine and waits for it, which is how the
// socket opens a file without touching a buffer from its own goroutine.
//
// It is instance.go's dispatch for this frontend. Every path answers exactly
// once: the loop runs the work and replies, or stop replies for it, or the
// request never made it into the queue at all and the error is this one's.
func (p *terminalPump) do(work func(client) error) error {
	w := &termWork{work: work, reply: make(chan error, 1)}

	p.mu.Lock()
	if p.gone {
		p.mu.Unlock()
		return errNoEditorLeft
	}
	p.q = append(p.q, w)
	p.mu.Unlock()

	if err := p.term.Post(tui.WakeEvent{}); err != nil {
		// Nothing will come and take it, so take it back -- unless the loop
		// already has it, which a wake posted by somebody else can have done,
		// and then the answer is on its way and this error is stale.
		if p.withdraw(w) {
			return err
		}
	}
	return <-w.reply
}

// withdraw takes a request back out of the queue, and reports whether it was
// still there to take.
func (p *terminalPump) withdraw(w *termWork) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, q := range p.q {
		if q == w {
			p.q = append(p.q[:i], p.q[i+1:]...)
			return true
		}
	}
	return false
}

// drain runs everything queued, on the editor goroutine.
//
// The work comes out from under the lock before any of it runs, so a request
// that arrives while one is running joins the next wake rather than deadlocking
// against this one.
func (p *terminalPump) drain() {
	p.mu.Lock()
	work := p.q
	p.q = nil
	p.mu.Unlock()

	for _, w := range work {
		w.reply <- w.work(p.term)
	}
}

// stop ends the pump: no more work is taken, whatever is in hand is answered,
// and the script goroutine is waited for.
//
// It runs after Loop has returned, on the same goroutine, so nothing is racing
// the drain. Answering the queue matters more than it looks: a "pvim --wait"
// on the other end of one of these requests is a shell sitting there, and the
// difference between an error and silence is whether it ever comes back.
func (p *terminalPump) stop() {
	p.mu.Lock()
	p.gone = true
	work := p.q
	p.q = nil
	p.mu.Unlock()

	for _, w := range work {
		w.reply <- errNoEditorLeft
	}
	p.typing.Wait()
}

// errNoEditorLeft is a socket request with no editor goroutine to run it: one
// that arrived as the editor was leaving.
var errNoEditorLeft = errors.New("pvim: the editor is shutting down")

// event is the tui.Handler: whatever another goroutine left, then the event
// itself, then the options.
func (p *terminalPump) event(cl tui.Client, ev tui.Event) error {
	if len(p.script) > 0 {
		keys := p.script
		p.script = nil
		p.play(keys)
	}
	if _, wake := ev.(tui.WakeEvent); wake {
		p.drain()
	}

	err := p.ed.terminalEvent(cl, ev)

	// The options again, after every event and not once at startup.
	// internal/tui reads 'ttimeoutlen' and 'timeoutlen' from its decoding
	// goroutine on every wait and it writes the mouse sequences when 'mouse'
	// changes, and both of those change under this editor's feet: ":set
	// mouse=" typed at the colon prompt, and the vimrc's FastEscape augroup,
	// which sets 'timeoutlen' on every InsertEnter and InsertLeave. With a
	// single call at startup, ":set mouse=" was inert -- measured under a pty
	// with the real vimrc, the only mouse-off in a 4500 byte stream was Stop's
	// teardown at the very end, where real vim's last cycle before the ":set"
	// ends at 4537 with no re-enable at all.
	//
	// After the event rather than before, so that a ":set" typed on this
	// keystroke is the one that takes effect, and after the frame, so the
	// escape sequences do not land in the middle of one.
	p.term.SetOptions(tui.OptionsFrom(p.ed.opt))
	return err
}

// play types a -s script into the live terminal from a goroutine of its own.
//
// A goroutine and not a loop here, because Post blocks until the loop takes the
// event and this is the loop. It ends when the script does, or at the first
// Post the terminal refuses, which is what a quit half way through a script
// looks like from this side: the editor has gone and the rest of the keys are
// dropped, the same way vim abandons a script when what it was typing into is
// no longer there.
func (p *terminalPump) play(keys []key.Key) {
	p.typing.Add(1)
	go func() {
		defer p.typing.Done()
		for _, k := range keys {
			if err := p.term.Post(tui.KeyEvent{Key: k}); err != nil {
				return
			}
		}
	}()
}

// getch is what the session's prompts read their answer from: the next key off
// the same channel the command itself came from.
//
// Work that arrives while a prompt is up is run and answered as usual, and a
// resize is applied and waited past -- resizing the terminal while ":q" is
// asking about a modified buffer must not answer the question. That is the
// window's getch rule and this is the window's list, minus the coroutine
// handshake the window needs and this does not.
func (p *terminalPump) getch() (key.Key, bool) {
	for ev := range p.term.Events() {
		switch ev := ev.(type) {
		case tui.KeyEvent:
			return ev.Key, true
		case tui.WakeEvent:
			p.drain()
		case tui.ResizeEvent:
			p.ed.resize(ev.Rows, ev.Cols)
		}
	}
	return key.Key{}, false
}

// terminalEvent is the handler's body: one event in, a repaint out.
//
// The body it shares with the window's handler is in window.go -- one keystroke
// dispatched, one quit honoured, one frame drawn -- because those three are the
// editor's answer and not the platform's, and two copies of them would drift
// the first time one of the two got a fix.
func (e *editor) terminalEvent(c tui.Client, ev tui.Event) error {
	switch ev := ev.(type) {
	case tui.KeyEvent:
		e.key(ev.Key)
	case tui.PasteEvent:
		// A bracketed paste is literal text with no mappings applied, which is
		// what stops a pasted "jj" from leaving insert mode.
		e.paste(ev.Text)
	case tui.ResizeEvent:
		e.resize(ev.Rows, ev.Cols)
	case tui.MouseEvent:
		e.mouse(tuiMouse(ev))
	case tui.WakeEvent:
		// The work is already done: the pump drained it before calling this,
		// and what is left is the frame it changed, which the tail below
		// paints. A wake that found nothing queued paints the frame anyway,
		// which costs a diff of a screen nothing changed on.
	case tui.CloseEvent:
		// The terminal is going away. There is nowhere to prompt, so a
		// modified buffer is lost either way and the honest thing is to leave.
		return errQuit
	}
	return e.finish(c)
}

// installTerminalClipboard makes "* and "+ the desktop pasteboard here too,
// and returns the call that puts the thread it needs away again.
//
// # Which thread, and why it is not the window's
//
// internal/clip talks to NSPasteboard itself, through purego, and takes the
// thread it has to be touched from as a Runner. The window passes gui.OnMain,
// because AppKit's run loop is right there. This frontend has no run loop and
// must not go looking for one: internal/tui and internal/gui are enforced
// peers (internal/deps_test.go's TestFrontendsAreStrangers), and gui.OnMain in
// a process that never called gui.Run answers ErrNoMainThread anyway, so a
// clipboard built over it would install happily and then fail every "+y.
//
// So the terminal brings a thread of its own: one goroutine, locked to one OS
// thread, doing nothing but running pasteboard closures one at a time. Locked
// because a Go goroutine hops between OS threads at any preemption point and
// NSPasteboard is not thread-safe; one thread rather than a pool because that
// is what "serialised" means and internal/clip's own cache assumes it. It is
// the same shape as the window's Runner -- hand the work across, wait for it --
// with a plain thread where the window has a run loop.
//
// A platform with no clipboard installs nothing, drops the thread, and leaves
// "* and "+ as ordinary registers, which is a working editor without desktop
// integration rather than a broken one.
//
// It costs a startup: internal/clip loads the frameworks here rather than on
// the first yank, so that a box where AppKit is not where it should be says so
// while there is still a shell to say it to. Measured on this machine, 2.9 ms
// against the 50 ms launch, which is a twentieth of the budget for the
// clipboard working the first time it is asked rather than the second.
func (e *editor) installTerminalClipboard() func() {
	th := newClipThread()
	cb, err := clip.New(th.run)
	if err != nil {
		th.stop()
		return func() {}
	}
	e.ed.Registers().SetClipboard(cb)
	return th.stop
}

// clipThread is one OS thread that does nothing but touch the pasteboard.
type clipThread struct {
	work chan func()
	quit chan struct{}
	once sync.Once
}

// newClipThread starts the thread. It is idle until something is yanked.
func newClipThread() *clipThread {
	th := &clipThread{work: make(chan func()), quit: make(chan struct{})}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		for {
			select {
			case fn := <-th.work:
				fn()
			case <-th.quit:
				return
			}
		}
	}()
	return th
}

// run is the clip.Runner: hand fn over, wait for it to have run.
//
// The wait is on the closure's own channel and not on quit as well, because
// once the thread has taken the work it runs it to the end; the close is
// deferred, so a panic inside a pasteboard call releases the caller rather than
// hanging the editor on the way to the crash.
func (th *clipThread) run(fn func()) error {
	done := make(chan struct{})
	select {
	case th.work <- func() { defer close(done); fn() }:
	case <-th.quit:
		return errClipboardGone
	}
	<-done
	return nil
}

// stop ends the thread. Safe to call twice, because the editor's shutdown path
// is not the only thing that can reach it.
func (th *clipThread) stop() { th.once.Do(func() { close(th.quit) }) }

// errClipboardGone is a yank or a put after the editor has started shutting
// down. internal/clip puts it on the message line rather than losing the
// register silently.
var errClipboardGone = errors.New("pvim: the clipboard thread has gone")

// paste inserts text with no mapping and no interpretation, which is what
// stops a pasted "jj" from leaving insert mode.
//
// Insert and replace mode only. What vim does with a bracketed paste in normal
// mode has not been measured, and a guess here is the expensive kind: it would
// look right on a one-line paste and quietly do something else on a paste that
// starts with a "d". So it says so instead.
func (e *editor) paste(b []byte) {
	if m := e.ed.Mode(); m != mode.Insert && m != mode.Replace {
		e.ed.Say("paste outside insert mode is not implemented")
		return
	}
	for _, r := range string(b) {
		if r == '\n' || r == '\r' {
			_ = e.sess.Key(key.Key{Special: key.KeyCR})
			continue
		}
		_ = e.sess.Key(key.Rune(r))
	}
}

// readFile reads the file named on the command line, if there was one.
//
// A file that is not there is not an error: "pvim newfile" opens an empty
// buffer that writes to that name, which is what vim does and what half of
// every editing session starts with. A directory is an error for now
// puts the tree browser behind one.
func readFile(name string) ([]byte, string, error) {
	if name == "" {
		return nil, "", nil
	}
	abs, err := filepath.Abs(name)
	if err != nil {
		abs = name
	}
	info, err := os.Stat(abs)
	switch {
	case err == nil && info.IsDir():
		return nil, "", fmt.Errorf("%s is a directory; directories are opened by the tree browser", name)
	case err != nil && os.IsNotExist(err):
		return nil, abs, nil
	case err != nil:
		return nil, "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, "", err
	}
	return data, abs, nil
}
