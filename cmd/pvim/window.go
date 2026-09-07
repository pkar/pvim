package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkar/pvim/internal/clip"
	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/raster"
	"github.com/pkar/pvim/internal/screen"
)

// The window frontend: the same editor the terminal drives, behind AppKit.
//
// It owns no editing and no drawing, exactly as terminal.go owns none:
// internal/gui owns the window, the keymap and the blit, the editor owns the
// buffer, draw.go composites, and what is left is the loop between them.
//
// # Why this file has a pump in it and terminal.go does not
//
// internal/tui hands out its event channel, so runTerminal can read one more
// event from inside a handler and that is how a prompt gets its answer:
// ":4,2d" asks "Backwards range given, OK to swap (y/n)?" and reads the "y"
// off the same channel the ":" came from. internal/gui has no such channel.
// Its Run pops an event, calls the handler, and calls it again when the next
// one arrives, so a handler that blocked waiting for one more key would block
// the only goroutine that could deliver it.
//
// Nor can the editor simply be moved onto a goroutine of its own with the
// handler as a thin pusher, because a handler returning an error is the ONLY
// way to stop NSApp.run: a quit decided on a goroutine while no event is in
// flight would leave the window on screen until somebody pressed a key.
//
// So the two run as coroutines. The editor gets a goroutine and a request
// channel; the handler pushes one request and waits for one answer. An answer
// of errMoreInput means "this event is digested and another one is wanted",
// and the handler returns nil and lets AppKit deliver the next one, which the
// waiting getch takes instead of the loop. Every request is answered exactly
// once, which is what makes it deadlock-free, and the socket pushes work down
// the same channel so that a file arriving from another shell is handled by
// the goroutine that owns the buffers, like everything else.
//
// The cost is one channel hop each way per keystroke, which is the same cost
// already accepted for the AppKit boundary itself.

// client is the half of a frontend the editor paints through, named here so
// that the code shared between the window and the terminal can be written
// once.
//
// It is a structural match for both gui.Client and tui.Client, which declare
// the same four methods and which internal/deps_test.go keeps identical. It is
// deliberately not an alias for either: cmd/pvim is the one place that holds
// both frontends, and a shared name here would make it look as though one of
// those packages exported a contract the other imports, which is the exact
// thing the peer arrangement forbids.
type client interface {
	Draw(s *screen.Screen) error
	Size() (rows, cols int)
	SetTitle(title string) error
	Bell()
}

// errMoreInput is the pump's answer to a handler whose event has been consumed
// by a prompt rather than finished with. It never leaves this file.
var errMoreInput = errors.New("pvim: the editor wants another key")

// pumpReq is one thing for the editor goroutine to do.
//
// Exactly one of ev and work is set. A request always carries a reply channel
// and always gets exactly one value on it; the channel is buffered so that a
// sender which has given up -- a socket client that hung up -- cannot wedge
// the editor.
type pumpReq struct {
	ev    gui.Event
	work  func(client) error
	reply chan error
}

// pump joins internal/gui's handler to the editor goroutine.
type pump struct {
	ed *editor
	in chan pumpReq

	// cl is the frontend, captured on the first event because that is the
	// first time internal/gui hands one over.
	cl client

	// reply is the shared answer channel for events, one outstanding at a
	// time by construction: internal/gui calls the handler serially and the
	// handler does not return until its request has been answered.
	reply chan error

	// cur is the reply channel of the request being worked on, or nil once it
	// has been answered. Only the editor goroutine touches it.
	cur chan error

	// ready is closed once the loop goroutine is running, which is what makes
	// the socket safe to accept on: a request pushed before then would sit in
	// the channel with nobody to take it.
	ready chan struct{}

	// script is the -s keystroke file, waiting for a window to type it into.
	// It is drained by the first event that reaches handle -- which is the
	// ResizeEvent internal/gui sends the moment the window has a size -- and
	// set back to nil there, so it is typed once and only once. Only the
	// handler goroutine touches it. See script.go.
	script []key.Key

	started bool

	// mapTimer ends an ambiguous mapping. The vimrc sets 'notimeout' only
	// inside its !has('gui_running') branch, so the terminal never waits and
	// the WINDOW does: with 'timeout' on and 'timeoutlen' at 1000, a key that
	// is the prefix of a mapping and also a command in its own right has to
	// resolve a second after it was typed. Without this the window held it
	// forever, which for this vimrc means "," -- the leader, and the prefix of
	// "," -- never doing its own job at all.
	//
	// Only the editor goroutine touches it -- loop() runs one request at a
	// time -- and the timer's own goroutine only submits work through do(),
	// which that same goroutine runs.
	mapTimer *time.Timer
}

// newPump returns a pump over ed. The loop goroutine does not start until the
// first event arrives, because that is when there is a client to give it.
func newPump(ed *editor) *pump {
	return &pump{
		ed:    ed,
		in:    make(chan pumpReq),
		reply: make(chan error, 1),
		ready: make(chan struct{}),
	}
}

// handle is the gui.Handler: push one event, wait for one answer.
func (p *pump) handle(cl gui.Client, ev gui.Event) error {
	p.start(cl)
	p.in <- pumpReq{ev: ev, reply: p.reply}
	err := <-p.reply
	// A -s script is typed here and nowhere else. It has to be on this
	// goroutine, because a quit decided anywhere else cannot stop NSApp.run:
	// returning an error from the handler is the only thing that can, which is
	// the same reason the editor is a coroutine rather than a goroutine of its
	// own. errMoreInput means the event was swallowed by a prompt and the
	// prompt wants a key, and the script is exactly where the next one is.
	if len(p.script) > 0 && (err == nil || errors.Is(err, errMoreInput)) {
		keys := p.script
		p.script = nil
		err = playScript(keys, p.feed)
	}
	if errors.Is(err, errMoreInput) {
		return nil
	}
	return err
}

// feed types one key, the same way an event carrying one would.
//
// It is do's shape with an event instead of a func: the request goes down the
// same channel, is answered exactly once, and is taken either by the loop or by
// a getch waiting inside a prompt, which is what lets a script answer a
// question the script itself asked.
func (p *pump) feed(k key.Key) error {
	reply := make(chan error, 1)
	p.in <- pumpReq{ev: gui.KeyEvent{Key: k}, reply: reply}
	return <-reply
}

// start brings the loop goroutine up on the first event.
func (p *pump) start(cl client) {
	if p.started {
		return
	}
	p.started = true
	p.cl = cl
	// The clipboard needs a run loop to marshal onto and there is one now, so
	// this is the first moment "* and "+ can be the macOS pasteboard rather
	// than ordinary registers. See internal/clip for why the call goes this
	// way round.
	p.ed.installClipboard(gui.OnMain)
	go p.loop()
	close(p.ready)
}

// loop is the editor goroutine: one request at a time, in order, forever.
func (p *pump) loop() {
	for r := range p.in {
		p.cur = r.reply
		p.answer(p.run(r))
	}
}

// armMapTimeout starts or stops the wait on an ambiguous mapping.
//
// Called after every key. A key that leaves a mapping half-matched arms the
// timer; anything else stops it, including the key that resolved the mapping,
// so a timer never fires against a machine that has already moved on. The work
// re-checks MapPending anyway, because the timer can be in flight when the next
// key lands.
func (p *pump) armMapTimeout() {
	if p.mapTimer != nil {
		p.mapTimer.Stop()
		p.mapTimer = nil
	}
	if !p.ed.sess.MapPending() {
		return
	}
	d, on := p.ed.mapTimeout()
	if !on {
		return
	}
	p.mapTimer = time.AfterFunc(d, func() {
		// Errors here are the editor's own message line, not the timer's, and
		// errNoEditorYet cannot happen: a mapping cannot be pending before the
		// first key.
		_ = p.do(func(client) error {
			if !p.ed.sess.MapPending() {
				return nil
			}
			if err := p.ed.sess.MapTimeout(); err != nil {
				p.ed.ed.Say(err.Error())
			}
			return nil
		})
	})
}

// run does one request.
func (p *pump) run(r pumpReq) error {
	if r.work != nil {
		return r.work(p.cl)
	}
	if _, isKey := r.ev.(gui.KeyEvent); isKey {
		defer p.armMapTimeout()
	}
	return p.ed.windowEvent(p.cl, r.ev)
}

// answer replies to the request being worked on, once.
func (p *pump) answer(err error) {
	if p.cur == nil {
		return
	}
	p.cur <- err
	p.cur = nil
}

// stop ends the loop goroutine, which happens when NSApp.run has returned and
// no further event can arrive.
func (p *pump) stop() {
	if p.started {
		close(p.in)
	}
}

// do hands work to the editor goroutine and waits for it, which is how the
// socket opens a file without touching a buffer from its own goroutine.
//
// It returns errNoInstanceLoop when nothing is draining the channel yet, which
// is a request that arrived between the socket being bound and the window
// producing its first event. That window is real and short: internal/gui sends
// a ResizeEvent before any key, so it closes as soon as the window is on
// screen.
func (p *pump) do(work func(client) error) error {
	select {
	case <-p.ready:
	default:
		return errNoEditorYet
	}
	reply := make(chan error, 1)
	p.in <- pumpReq{work: work, reply: reply}
	return <-reply
}

// errNoEditorYet is a socket request that beat the window onto the screen.
var errNoEditorYet = errors.New("pvim: the editor is still starting up")

// getch is what the session's prompts read their answer from.
//
// It answers the request in flight with errMoreInput, which lets the handler
// return and AppKit deliver the next event, and then takes that event off the
// channel itself. Work requests that arrive while a prompt is up are run and
// answered as usual, and events that are not keys are applied and waited past:
// resizing the window while ":q" is asking about a modified buffer must not
// answer the question.
func (p *pump) getch() (key.Key, bool) {
	p.answer(errMoreInput)
	for {
		r, ok := <-p.in
		if !ok {
			return key.Key{}, false
		}
		p.cur = r.reply
		if r.work != nil {
			p.answer(r.work(p.cl))
			continue
		}
		if k, isKey := r.ev.(gui.KeyEvent); isKey {
			// The reply channel stays: whatever the command finally decides
			// is this event's answer.
			return k.Key, true
		}
		p.ed.aside(r.ev)
		p.answer(errMoreInput)
	}
}

// aside applies an event that arrived while a prompt was waiting for a key.
//
// A resize has to be applied or the prompt is answered against a stale layout;
// everything else is dropped, including a close, because a window closing
// while it is asking whether to save is a question that has to be answered
// before it can be obeyed.
func (e *editor) aside(ev gui.Event) {
	if r, ok := ev.(gui.ResizeEvent); ok {
		e.resize(r.Rows, r.Cols)
	}
}

// runWindow opens the file in an AppKit window and drives the editor behind it.
func runWindow(c config) error {
	ed, err := newFrontendEditor(c, gui.GUIRunning)
	if err != nil {
		return err
	}
	// Before the window, because internal/gui reads gui.Font once when it
	// builds the first face and the vimrc has already had its say by now. A
	// ":set guifont=" typed later goes through the same function.
	ed.applyGUIFont()

	p := newPump(ed)
	ed.sess.getch = p.getch
	ed.pump = p
	defer p.stop()

	// "pvim -g -s keys file" plays the script into the live window and then
	// hands the keyboard back, which is vim's own -s and is the window's smoke
	// gate: a real window, a real buffer, keys through the real editor
	// goroutine, and an exit status. A script that cannot be read is
	// fatal before the window opens rather than a message on a line nobody is
	// looking at yet.
	if c.keys != "" {
		keys, kerr := readScript(c.keys)
		if kerr != nil {
			return kerr
		}
		p.script = keys
	}

	// The socket is bound before the window comes up, so that a shell racing
	// the launch finds an instance rather than starting a second one. A
	// request that lands before the first event is refused with a sentence and
	// the client starts its own editor, which is the right answer for the
	// fraction of a second that window is open.
	defer ed.serveSocket(socketPath(), c)()
	// A clean exit takes its swap file with it. One left behind is an E325 on
	// the next open about a crash that never happened, which trains people to
	// answer (D)elete without reading, which is how a real recovery gets
	// thrown away.
	defer ed.persistOnQuit()

	err = gui.Run(p.handle)
	if errors.Is(err, errQuit) {
		return nil
	}
	return err
}

// newFrontendEditor is the part of starting up that both frontends do: read
// the file, build the editor, read the vimrc with this frontend's answer to
// has('gui_running'), and lay it out.
//
// guiRunning is not a detail. It decides three blocks of the real vimrc -- the
// notimeout/ttimeout/ttimeoutlen=10 branch, the FastEscape augroup and the
// <C-c>, <C-x> and <C-v> clipboard mappings -- and the two frontends have to
// give different answers or the window gets the terminal's Escape timeout and
// the terminal loses its clipboard maps.
func newFrontendEditor(c config, guiRunning bool) (*editor, error) {
	// "pvim <dir>" opens the tree and not netrw. readFile still refuses a
	// directory -- opening one as text is how a file browser gets forgotten
	// about -- so the argument is taken off here and handed to
	// cmd/pvim/finder.go once the editor exists.
	dirArg := ""
	if c.file != "" {
		if st, serr := os.Stat(c.file); serr == nil && st.IsDir() {
			dirArg, c.file = c.file, ""
		}
	}
	data, name, err := readFile(c.file)
	if err != nil {
		return nil, err
	}
	ed, err := newEditor(data, name, c.rows, c.cols, "")
	if err != nil {
		return nil, err
	}
	if !c.clean {
		ed.loadVimrc(vimrcPath(), guiRunning)
		// Then the events for a buffer that has just been read: BufNewFile or
		// BufRead, detection, FileType, BufEnter. After the vimrc, because the
		// autocommands it registers are what fire, and after nothing else,
		// because vim fires them here too -- before the layout, before the
		// swap file, before the first frame. See cmd/pvim/filetype.go.
		//
		// Both frontends come through here and --oracle does not, which is
		// what keeps the graded runs where they were: oracle.go builds its
		// editor with newEditor directly and reads no vimrc, so a graded run
		// has no autocommands to fire and no filetype to set.
		//
		// Inside the "--clean" guard with the vimrc, which is a difference
		// from vim worth naming: "vim --clean" sources defaults.vim,
		// defaults.vim says "filetype plugin indent on", so vim detects
		// filetypes with no vimrc and this does not. It costs nothing --
		// with no vimrc there are no autocommands to fire, and nothing in the
		// editor reads 'filetype' for itself -- and it keeps a flag whose job
		// is to be inert inert.
		ed.bufferOpened(ed.cur() != nil && ed.cur().NewFile)
	}
	// The vimrc may have moved 'cmdheight', which changes how many rows the
	// text area gets, so the layout is redone after it rather than before.
	ed.resize(c.rows, c.cols)
	// Persistence goes here and not in newEditor, and that placement is the
	// whole of how --oracle stays reproducible: oracle.go builds its editor
	// with newEditor directly, so a graded run writes no swap file and reads
	// no history, while both real frontends come through here and get both.
	// After the vimrc, because 'undofile', 'undodir', 'directory' and
	// 'swapfile' are four things it sets and all four decide what this does.
	if home, err := os.UserHomeDir(); err == nil {
		ed.initPersist(home, data)
	}
	// The finder and the tree, after the vimrc because every value they read
	// -- g:ctrlp_max_height, g:ctrlp_custom_ignore, the four NERDTree ones and
	// 'wildignore' -- comes out of it. --oracle does not come through this
	// function and so has neither, which is what keeps the graded runs where
	// they were. See cmd/pvim/finder.go.
	p := installPlugins(ed)
	if dirArg != "" {
		p.openTree(dirArg)
	}
	return ed, nil
}

// windowEvent is the window's handler: one event in, a repaint out.
func (e *editor) windowEvent(c client, ev gui.Event) error {
	switch ev := ev.(type) {
	case gui.KeyEvent:
		e.key(ev.Key)
	case gui.ResizeEvent:
		e.resize(ev.Rows, ev.Cols)
	case gui.ScaleEvent:
		// Nothing. The grid is counted in cells and internal/gui has already
		// rebuilt the face and re-counted them; a ResizeEvent follows if the
		// count moved. This case exists so that the day something here does
		// cache a pixel, the event it needs is already arriving.
	case gui.FocusEvent:
		// vim draws a hollow caret in a window that is not the key window,
		// which is the only way to tell at a glance which of two editors a
		// keystroke would go to.
		e.unfocused = !ev.Focused
	case gui.MouseEvent:
		e.mouse(guiMouse(ev))
	case gui.CloseEvent:
		// The red button and the menu's Quit both arrive here, and
		// internal/gui refuses to close on its own so that this can say no.
		// ":qall" is the command with the right rules already in it: it
		// prompts once per modified buffer under 'confirm' and answers E37
		// without it, and a "C" at the prompt leaves everything where it is.
		e.closeRequested()
	}
	return e.finish(c)
}

// key dispatches one keystroke and puts whatever the editor said about it on
// the message line.
//
// A key this editor has not implemented is a line on the message line and not
// the end of the session: an editor that exited because somebody pressed
// CTRL-W v would lose the buffer over a missing feature.
func (e *editor) key(k key.Key) {
	// The finder and the tree see the key first. The finder takes every one
	// of them while its window is up, the tree takes the twelve it binds, and
	// CTRL-P in normal mode opens the finder; everything else falls straight
	// through and the editor never learns this happened. See
	// cmd/pvim/finder.go.
	if pluginKey(e, k) {
		return
	}
	if err := e.sess.Key(k); err != nil {
		if errors.Is(err, mode.ErrQuit) {
			e.quit = true
			return
		}
		e.ed.Say(err.Error())
	}
	// Both frontends funnel every key through here, so this is the one place
	// the swap file needs syncing. It writes nothing when nothing changed.
	e.persistAfterKey()
	// And the one place the highlighter has to be told the text moved. The
	// changelist's newest entry is the line the last change touched, which is
	// exactly what internal/syntax wants to invalidate from; a key that changed
	// nothing leaves it where it was and the call is a map lookup.
	if e.syn != nil {
		if p, ok := e.buf.ChangeNewer(); ok {
			e.syn.changed(e.buf, p.Line)
		}
	}
}

// closeRequested is Cmd-Q, the red button and a terminal hangup that can still
// ask: run ":qall" and let the ex layer's own 'confirm' rules decide.
func (e *editor) closeRequested() {
	if e.ctx == nil {
		e.quit = true
		return
	}
	e.ed.NewMessageLine()
	if err := e.ctx.RunLine("qall"); err != nil {
		if msg := errorMessage(err, "qall"); msg != "" {
			e.ed.Say(msg)
		}
	}
	e.sess.sync()
	e.ed.Redisplay()
}

// finish is the tail every event goes through: honour a quit, then repaint.
func (e *editor) finish(c client) error {
	// Every "pvim --wait" whose tab has gone is released here, which is one
	// check per event and is where it has to be: a buffer can close on a key,
	// on an ex command or on the editor quitting, and there is no one place
	// further in that all three go through.
	e.releaseClosed(e.quit)
	if e.quit {
		// The finder and the tree go with the editor. See the note on
		// pluginState in cmd/pvim/finder.go for why this is not a field.
		uninstallPlugins(e)
	}
	if e.quit || e.sess.writeOnQuit {
		// ZZ writes and ZQ does not, and internal/mode answers both with the
		// same ErrQuit, so which one it was is the session's to remember.
		if e.sess.writeOnQuit && e.modified() {
			if err := e.write(); err != nil {
				e.ed.Say(err.Error())
				e.sess.writeOnQuit = false
				e.quit = false
				return e.repaint(c)
			}
		}
		return errQuit
	}
	return e.repaint(c)
}

// repaint draws the editor and keeps the window title in step with it.
func (e *editor) repaint(c client) error {
	e.applyGUIFont()
	e.retitle(c)
	rows, cols := c.Size()
	return c.Draw(e.draw(rows, cols))
}

// retitle sets the window title, and only when it has changed.
//
// Only when it has changed because a title crosses to the run loop thread and
// a redraw happens on every keystroke: setting the same string sixty times a
// second is sixty pointless hops and, in the terminal, sixty OSC 2 sequences
// down a pipe that may be an ssh connection.
func (e *editor) retitle(c client) {
	t := e.windowTitle()
	if t == e.title {
		return
	}
	e.title = t
	_ = c.SetTitle(t)
}

// installClipboard makes "* and "+ the system clipboard.
//
// run is the thread the platform's clipboard has to be touched from, which on
// macOS is the AppKit run loop and which only the window frontend has. A
// platform with no clipboard, or a frontend with no run loop, installs
// nothing and leaves "* and "+ as ordinary registers, which is a working
// editor without desktop integration rather than a broken one -- and is
// exactly what every headless oracle run wants.
func (e *editor) installClipboard(run clip.Runner) {
	if run == nil {
		return
	}
	// Probed and not assumed. gui.OnMain is a perfectly good func value on a
	// process that never opened a window, and it refuses every call; a
	// clipboard built over it would install happily and then answer every "+y
	// with an error on the message line. One empty closure says whether there
	// is a run loop behind it.
	if err := run(func() {}); err != nil {
		return
	}
	cb, err := clip.New(run)
	if err != nil {
		return
	}
	e.ed.Registers().SetClipboard(cb)
}

// applyGUIFont pushes 'guifont' and 'linespace' at internal/gui, parsing and
// checking the font first.
//
// Vim's answer to a font it cannot load is E596 with the string that was asked
// for, and the option keeps its old value; this does the same, because a typo
// in a ":set guifont=" that left the window with no face at all would be a
// window nobody could read the error on.
//
// Before the window is up this only records what the first frame will be drawn
// in. After it, gui.SetFont builds the face, swaps it in and relays the grid
// out, which arrives back here as a ResizeEvent even when the cell count did
// not move -- a window whose font changed has to be repainted whatever its size
// did. Either way the call is made from the editor goroutine, which is where
// the font file gets read; only the relayout crosses to the run loop thread.
//
// It runs on every frame rather than being hooked to ":set", and that is
// deliberate: a font can arrive from the vimrc, from a colon command, from a
// file the vimrc sourced or from a ":set" inside a ":g", and one comparison
// against what was last applied catches all four where four call sites would
// catch three.
func (e *editor) applyGUIFont() {
	if e.opt == nil {
		return
	}
	e.applyLinespace()
	name := strings.TrimSpace(e.opt.G.GuiFont)
	if name == "" || name == e.guifont {
		return
	}
	g, err := raster.ParseGUIFont(name)
	if err == nil {
		// Checked here as well as inside gui.SetFont, and not by accident.
		// Before Run there is no window to build a face for, so SetFont takes
		// the font on trust and records it for the first frame; a name that
		// resolves to no file at all would then be discovered when the window
		// opened, which is a launch with an empty window and an error nobody
		// saw. One load at scale 1 answers it here instead, and it is the same
		// answer vim gives before it has a window: E596 and the old value kept.
		_, err = raster.NewFace(g, 1)
	}
	if err == nil {
		err = gui.SetFont(g)
	}
	switch {
	case err == nil, errors.Is(err, gui.ErrNoBackend), errors.Is(err, gui.ErrNoMainThread):
		// No window to relayout is not a failure: the font is recorded, the
		// terminal goes on drawing in whatever the terminal draws in, and the
		// next window to open uses it.
		e.guifont = name
	default:
		e.ed.Say("E596: Invalid font(s): " + name)
	}
}

// applyLinespace is 'linespace', which changes the row pitch and therefore how
// many rows fit in the window.
func (e *editor) applyLinespace() {
	n := e.opt.G.LineSpace
	if n == e.linespace {
		return
	}
	err := gui.SetLinespace(n)
	if err != nil && !errors.Is(err, gui.ErrNoBackend) && !errors.Is(err, gui.ErrNoMainThread) {
		e.ed.Say("E487: Argument must be positive: linespace=" + strconv.Itoa(n))
		return
	}
	e.linespace = n
}

// windowTitle is what the title bar says, which is vim's shape with this
// editor's name at the end.
//
// Measured against vim 9.2.0321 with 'title' on, reading the OSC 2 sequence
// off a pty:
//
//	bar.txt (~/.pvimtitle) - VIM a file under $HOME, unmodified
//	foo.txt + (/tmp/x) - VIM the same file with unwritten changes
//	[No Name] - VIM a buffer that has never had a name
//
// So the marker is a " +" after the name and before the directory, the
// directory is home-replaced, and a nameless buffer has no parenthesis at all.
// vim also truncates the middle of a long title to 'titlelen'; that is a
// terminal's problem with a fixed-width title bar and not a window's, and it
// is not reproduced.
func (e *editor) windowTitle() string {
	b := e.cur()
	if b == nil || b.Name == "" {
		return "[No Name] - pvim"
	}
	mark := ""
	if b.Modified() {
		mark = " +"
	}
	return filepath.Base(b.Name) + mark + " (" + homeShort(filepath.Dir(b.Name)) + ") - pvim"
}

// userHome is $HOME, or nothing when the process has none. It is a variable so
// that a test can pin it: the title of a file under the home directory depends
// on it and hard-coding the tester's own home would make the test pass for one
// person.
var userHome = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// homeShort is vim's home_replace: a path under $HOME is printed with a "~".
func homeShort(path string) string {
	home := userHome()
	if home == "" || path == home {
		if path == home && home != "" {
			return "~"
		}
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}
