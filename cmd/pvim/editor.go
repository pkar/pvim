package main

import (
	"errors"
	"os"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/quickfix"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/vimrc"
	"github.com/pkar/pvim/internal/window"
)

// editor is one whole editor, assembled: a buffer, the mode machine over it,
// the option state, the window it is looked at through, the ex layer, and the
// session that joins them.
//
// There is one of these and both ways in build it the same way. --oracle runs
// a script through it with no terminal; the terminal frontend drives the same
// struct from a key channel. That is the point of the split between the
// editor and its frontends, and it is what keeps the thing the
// oracle grades and the thing a person types into from drifting apart.
type editor struct {
	buf  *text.Buffer
	ed   *mode.Editor
	opt  *options.Options
	tabs *window.Tabs
	ctx  *ex.Context
	sess *session

	// file is the name the buffer was read from, empty for a new buffer. It
	// is what :w writes back to and what the window title shows.
	file string

	// pers is the swap file, the undo history and the history file. Nil under
	// --oracle on purpose: a graded run that wrote a swap file into /tmp//
	// would make two runs of one script differ. See persistwire.go.
	pers *persist

	// ftDetect is whether ":filetype on" has run. Both vimrcs turn detection
	// on and "vim --clean" sources a defaults.vim that does too, so this is
	// almost always true; it exists so that ":filetype off" means something
	// rather than parsing, reporting and being ignored.
	ftDetect bool

	// quit is set by :q, :wq, ZZ and the window closing. The frontends read
	// it after every key.
	quit bool

	// hl is the highlight table the vimrc's "hi" lines and the colourscheme
	// fill in and the frontends draw through. It starts holding Normal in
	// nofrils-dark's colours, so a screen drawn before any vimrc has been read
	// is already the right shade of grey.
	hl *screen.Table

	// syn is the syntax engine: one rule set per filetype, one highlighter per
	// buffer. Nil under --oracle, which never builds a frontend editor, so a
	// graded run cannot be coloured differently from the one before it.
	syn *syntaxes

	// hlSet is the group names a "hi" line has defined, which is the one
	// question screen.Table cannot be asked: its ID interns a name on first
	// sight, so looking a group up to find out whether it exists creates it.
	// ":hi default" needs the answer and nothing else does.
	hlSet map[string]bool

	// What the vimrc left behind that nothing reads yet. Every one of these is
	// a feature that lands later, and they are kept rather than dropped so that
	// a test can assert the file was understood and so that
	// the day the feature lands the wiring is one line.
	vars        map[string]vimrc.Let
	maps        []vimrc.Map
	autocmds    []vimrc.AutoCmd
	ignored     []vimrc.Ignored
	colorscheme string

	// guiRunning is what has('gui_running') answered when the vimrc was read,
	// kept so that a file the vimrc sources gets the same answer. The two
	// frontends disagree about it and this vimrc has four mappings and an
	// augroup behind the disagreement.
	guiRunning bool

	// sourceDepth is how deep the loader is in files sourcing files: the
	// vimrc, then the colourscheme it names. It exists so that a colourscheme
	// naming itself is one message and not a stack overflow.
	sourceDepth int

	// title is the last window title handed to the frontend, so that a redraw
	// that did not change it does not cross the main-thread queue again. Empty
	// until the first frame, which is why the first SetTitle always happens.
	title string

	// guifont is the 'guifont' string already handed to internal/gui, and
	// linespace the 'linespace' already handed over, so that a redraw does not
	// re-parse and relayout for a font nothing changed. linespace starts at
	// zero, which is both the option's default and internal/gui's, so a
	// vimrc that sets it to zero explicitly does nothing and is right to.
	guifont   string
	linespace int

	// unfocused is the window not being the key window, which is a hollow
	// caret. Zero is focused, because that is what a window that has just been
	// ordered front is and because the terminal never sends the event at all.
	unfocused bool

	// regions is where the last frame put the four bands of the screen. The
	// mouse is what needs it: a click arrives as a screen row and column and
	// has to be turned back into a buffer position, and the text area's origin
	// is what does it.
	regions screen.Regions

	// rows and cols are the screen size the layout is worked out over, which
	// the frontend last told this editor and which --oracle takes from --rows
	// and --cols. They are kept because a split has to be given its share of a
	// screen and the tree that does the sharing is asked about on a keystroke,
	// not on a frame.
	rows, cols int

	// msgs is where internal/ex's messages are sunk, which is the mode machine
	// this editor started with and not the one it is on. See the Msg field
	// below for why the two can differ, and editor.message for what the
	// message line does about it.
	msgs *mode.Editor

	// pump is the window frontend's editor goroutine, or nil in the terminal
	// and under --oracle. The socket hands work to it; nothing else reads it.
	pump *pump

	// waiting is the "pvim --wait" clients and the buffers they are blocked
	// on. See editor.releaseClosed, which is what unblocks a git commit.
	waiting []waiter
}

// cur is the buffer the editor is acting on: the current window's, which after
// a ":e", a ":b" or a tab opened over the socket is not the one the editor was
// built with.
//
// internal/ex owns the buffer list and the "which one is current" question, so
// this asks it rather than keeping a second answer that can disagree. A
// Context with no buffer list -- nothing in this program builds one, and a
// test could -- answers nil, and every caller treats that as "no file".
func (e *editor) cur() *ex.Buf {
	if e.ctx == nil || e.ctx.Bufs == nil {
		return nil
	}
	return e.ctx.Bufs.Cur
}

// modified reports whether the buffer has changed since it was last written,
// which is the "[+]" in the status line and the thing ":q" refuses over.
func (e *editor) modified() bool { return e.cur().Modified() }

// open is ex.Context's Open: the hook ":e", ":b", ":tabnew" and the socket all
// reach when what the editor acts on has to become a different buffer.
//
// Without it internal/ex takes the fallback in swapBuffer, which builds a new
// mode.Editor and assigns it to Context.Ed, and nothing here reads Context.Ed
// back: this struct held its own ed, buf and file from construction, so every
// key after a ":e" went on editing the buffer that was no longer on the screen
// while ":w" wrote the one that was. Measured on "alpha": ":e g.txt" then x
// leaves vim's buffer alpha with an empty unnamed register and left this one
// "lpha" with an "a" in it, and ":e g.txt" then IHELLO<Esc>:w put HELLO in
// f.txt and never created g.txt.
//
// A new mode.Editor is still what happens, because internal/mode has no setter
// for its buffer and opening one up is out of scope here. What this adds is
// that the new one is carried across into everything that holds one, and that the state a person would notice losing comes with it: the
// register file, which under this vimrc's 'clipboard' is also where "* and "+
// live, and the search pattern that "n" repeats. The register file is copied
// through its pointer rather than re-registered because there is no setter for
// it either, and a File is a plain struct whose fields are all either maps or
// values.
func (e *editor) open(b *ex.Buf) {
	if b == nil || b.Text == nil {
		return
	}
	if b.Text == e.buf {
		// The buffer this editor is already on. internal/ex swaps to it
		// anyway -- ":sp" and ":vs" go through swapBuffer so that the buffer
		// list, "% and the alternate file stay right -- and rebuilding the
		// mode machine for that would throw away the message line, the mode
		// and anything half-typed for nothing. Measured through --oracle:
		// ":sp" alone cost the message log a line, which is a diff against
		// vim in every case that splits.
		e.file = b.Name
		return
	}
	ne := mode.New(b.Text)
	ne.SetOptions(e.ed.Options())
	ne.SetScriptInput(e.ed.ScriptInput())
	*ne.Registers() = *e.ed.Registers()
	ne.SetSearchPattern(e.ed.Search().Pattern)
	ne.SetFileName(b.Name)
	ne.SetCursor(b.Cursor)
	// The message stream moves with the editor. Without this it splits in two:
	// the ex layer's Context.Msg stays bound to the editor the session started
	// with while everything after the swap writes to the new one, and a ":new"
	// followed by a ":q" loses the blank line vim leaves behind.
	ne.AdoptMessages(e.ed)
	// Macro replay goes back through the map layer, because vim applies
	// mappings at REPLAY time and not at record time: ":nnoremap, A!<Esc>",
	// record ",", then ":nunmap," and replay, and vim types a comma where a
	// record-time expansion types the mapping.
	ne.SetRemap(func(k key.Key) error { return e.sess.Key(k) })
	// CTRL-X CTRL-O asks gopls. Without this the key opened an empty menu:
	// the machinery was there and had no source, which is what the vimrc's
	// "au filetype go inoremap <buffer> . .<C-x><C-o>" has been reaching.
	ne.SetOmniFunc(func(prefix []byte) [][]byte { return e.lspOmniCompleteWords(string(prefix)) })

	e.ed = ne
	e.ctx.Ed = ne
	e.sess.ed = ne
	e.msgs = ne
	e.ctx.Msg = ne.Say
	e.ctx.MsgRaw = ne.SayRaw
	e.ctx.MsgKeep = ne.SayKeep
	e.ctx.Err = ne.Say
	e.buf = b.Text
	e.file = b.Name

	// The same events startup fires, because a buffer arriving through ":e",
	// ":b", ":tabnew" or the socket is as much a buffer being opened as the
	// first one is. Without this the vimrc's per-filetype settings applied to
	// the file named on the command line and to nothing opened afterwards.
	e.bufferOpened(b.NewFile)
	e.lspBufferOpened()
	// And the same persistence, for the same reason. See reopenPersist.
	e.reopenPersist()
}

// reopenPersist gives a buffer that arrived after startup the swap check, the
// undo history and the marks the one named on the command line gets.
//
// This is the hole `pvim file` in a second shell fell through. The socket hands
// the file to the running instance, the instance opens it through ex and lands
// here, and initPersist -- which newFrontendEditor calls once, before the
// window exists -- had already run against the file the instance was started
// on. So the second file got no E325 on a crashed session's swap file, no undo
// history from the last time it was edited, and none of its A-Z marks, and
// nothing said so. ":e", ":b" and ":tabnew" fell through the same hole, which
// is why the hook is here and not in the socket handler: this is the one place
// every one of them passes through.
//
// The swap file of the buffer being left is closed first. A persist holds one
// swap file and this editor holds one persist, so opening the new one without
// closing the old one leaves a swap file on the disk that nothing will ever
// take away, and the next session opens that file to an E325 about a crash
// that never happened.
//
// Nil persist is --oracle, and it stays nil: a graded run that wrote a swap
// file into /tmp// would make two runs of one script differ.
//
// Two things this inherits from initPersist and does not fix, both reported
// rather than worked around here, because persistwire.go and persist.go belong
// to whoever owns persistence:
//
// - The E325 prompt answered (Q)uit or (A)bort calls exitPvim(1), which in a
// running instance takes the whole editor down and not just the file that
// was being opened. At startup that is right and vim does the same; on the
// seventh file of a live session it is not.
// - Closing the swap file of a buffer that is still in the buffer list is
// the best a one-swap-per-editor persist can do, and it is not vim, which
// keeps one per loaded buffer. The fix is a persist that holds a map, and
// it is the same change that would let this call the per-file half of
// initPersist instead of the whole of it.
func (e *editor) reopenPersist() {
	if e.pers == nil || e.file == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	// The bytes on disk and not the buffer's: the undo file's header holds the
	// sha256 of the file the history belongs to, and the buffer has had
	// 'fixendofline' applied to it on the way in.
	data, err := os.ReadFile(e.file)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	_ = e.pers.closeSwap()
	e.initPersist(home, data)
}

// message is what the message line shows.
//
// The live mode machine's, and the sink's when the live one has said nothing.
// The two are the same object until a ":e" or a ":b" swaps buffers, and after
// that the command's own message -- `"g.txt" [New]` -- is on the sink while
// every mode message is on the new machine. Falling back rather than choosing
// is also vim's behaviour on the line itself: a message stays there until
// something overwrites it.
func (e *editor) message() string {
	if m := e.ed.Message(); m != "" {
		return m
	}
	if e.msgs != nil {
		return e.msgs.Message()
	}
	return ""
}

// newEditor builds an editor over data, which is the file's contents or nil
// for an empty buffer.
//
// rows and cols are the screen in cells. The text area is the screen less the
// command line, which is 'cmdheight' rows; with one window and one tab there
// is neither a status line nor a tabline, which is why a 40-row screen gives
// 39 rows of text and vim, asked through cmd/oracle, answers &window 39.
//
// optLine is a ":set" line applied before anything else: the oracle's profile
// under --oracle, and empty in ordinary use, where the vimrc does that job
// instead.
func newEditor(data []byte, file string, rows, cols int, optLine string) (*editor, error) {
	e := &editor{
		buf: text.Read(data),
		// On by default, which is vim: "vim --clean" sources a defaults.vim
		// that runs ":filetype plugin indent on", and both of a user's
		// vimrcs run it themselves. ":filetype off" turns it back off.
		ftDetect: true,
		file:     file,
		hl:       screen.NewTable(),
		vars:     map[string]vimrc.Let{},
	}

	o := options.Defaults()
	e.opt = &o
	if optLine != "" {
		if _, err := e.opt.ApplyLine(optLine, options.Both); err != nil {
			return nil, err
		}
	}

	win := window.New(1, e.buf, e.opt.GW)
	win.SetHeight(textRows(rows, e.opt))
	win.View.Width = cols
	e.tabs = window.NewTabs(window.NewTabPage(win))

	e.ed = mode.New(e.buf)
	e.ed.SetOptions(ex.ModeOptions(e.opt, win))
	// "[I" prints the file name above the lines it lists, and it is the one
	// thing in the mode machine that has to know what file this is.
	e.ed.SetFileName(file)

	// The buffer list starts with the file the editor was opened on, so that
	// ":ls" names it and ":b#" has something to alternate with. Without this
	// the first command that needs a Buf invents one with no name and ":ls"
	// prints "[No Name]" over a file that has one.
	bufs := ex.NewBufList()
	bufs.Cur = bufs.Add(file, e.buf)
	if file != "" {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			bufs.Cur.NewFile = true
		}
	}

	e.ctx = &ex.Context{
		Ed:   e.ed,
		Bufs: bufs,
		Tabs: e.tabs,
		Opt:  e.opt,
		QF:   &quickfix.Stack{},
		Cmds: ex.UserCommands{},
		// Method values, which bind the mode machine this editor started
		// with and go on doing so after a ":e" has swapped it. That is on
		// purpose and it is the smaller of two wrongs: internal/mode keeps
		// the message log inside the machine, so a message stream that
		// followed the swap would be split in half, and every ":!" and "K"
		// case in testdata/keys grades that stream against vim's, which has
		// one message line for the session and not one per buffer. What it
		// costs is a message printed by a command that swapped buffers on its
		// way -- `:e g.txt` says `"g.txt" [New]` -- landing in a log the
		// frontend is no longer drawing from. The fix is a way to hand a log
		// from one mode machine to the next, and it belongs in internal/mode.
		Msg: e.ed.Say,
		// The ":!" echo, which is not a line: see mode.Editor.SayRaw.
		MsgRaw: e.ed.SayRaw,
		// The messages vim keeps across the redraw after the command, which
		// :redir catches twice. See mode.Editor.SayKeep.
		MsgKeep: e.ed.SayKeep,
		// The same function for both, for now. Vim draws an error in
		// ErrorMsg and an ordinary message in Normal, and the message area
		// carries a highlight group per line for exactly that, so this is a
		// line to change when the two are told apart -- not a reason to hold
		// a second message list.
		Err:  e.ed.Say,
		Quit: e.stop,
	}

	// After e.hl and e.hlSet, because a rule set resolves its groups through
	// the highlight table as it is loaded.
	e.syn = newSyntaxes(e.hl, func(n string) bool { return e.hlSet[n] })

	e.msgs = e.ed
	e.rows, e.cols = rows, cols
	e.sess = newSession(e.ed, win, e.ctx, e.opt.ScrollOffValue())
	e.sess.relayout = e.relayout
	e.ctx.Prompt = e.sess.prompt
	e.ctx.PromptQuiet = e.sess.promptQuiet
	// The buffer swap hook, which is what stops a ":e" from leaving the keys
	// editing one buffer and ":w" writing another. See editor.open.
	e.ctx.Open = e.open
	// ":nnoremap zz x" typed at the cmdline, which the ex layer parses and
	// hands back here because internal/vimrc imports internal/ex and the
	// dependency cannot go the other way.
	e.ctx.Map = e.runMap
	// ":w" fires the vimrc's BufWritePre autocommands and the format-on-save
	// table. Before this only "ZZ" did, because ZZ goes through editor.write
	// and ":w" goes through internal/ex, so the whitespace strip a user has
	// had for years ran on one of the two ways they save a file.
	e.ctx.PreWrite = func(name string) error {
		e.fireAutoCmds("BufWritePre", name)
		return e.lspBeforeWrite()
	}
	e.ctx.PostWrite = func(name string) {
		e.lspAfterWrite()
		e.fireAutoCmds("BufWritePost", name)
	}
	e.ed.SetRemap(func(k key.Key) error { return e.sess.Key(k) })
	e.ed.SetOmniFunc(func(prefix []byte) [][]byte { return e.lspOmniCompleteWords(string(prefix)) })
	return e, nil
}

// textRows is how many rows of the screen the text area gets.
//
// 'cmdheight' rows go to the command line and at least one always does, so a
// screen shorter than that leaves one row of text rather than none: an editor
// that renders no buffer at all because the terminal is two rows high is worse
// than one that renders a single line badly.
func textRows(rows int, o *options.Options) int {
	cmdHeight := o.G.CmdHeight
	if cmdHeight < 1 {
		cmdHeight = 1
	}
	if n := rows - cmdHeight; n >= 1 {
		return n
	}
	return 1
}

// resize lays the editor out for a new screen size, which is SIGWINCH in the
// terminal and a dragged window corner in the GUI.
func (e *editor) resize(rows, cols int) {
	e.rows, e.cols = rows, cols
	e.relayout()
	e.sess.sync()
}

// relayout gives every window in the current tab its share of the screen.
//
// It is internal/screen's own LayoutTree over internal/screen's own bands,
// which is to say it is the layout the next frame will be drawn with, worked
// out one step early. That is the whole point: the height of a window is what
// H, M, L, CTRL-D, CTRL-F and every z command are measured against, and those
// are answered on the keystroke, before anything is drawn. Until this existed
// cmd/pvim set one window's height by hand and internal/ex's ":sp" made a
// second window that inherited it, so both halves of a split thought they were
// the whole screen: on 60 lines, ":sp" then L then ":only" left vim on line 14
// and this on 34, and a bare L with no split answered 34 in both, which is the
// tell.
//
// The band is Regions() with the text and the last status line added back
// together, because LayoutTree wants the area the windows share and takes each
// window's own status line out of the bottom of its rectangle.
//
// Called on every keystroke through session.sync, which sounds wasteful and is
// not: it is one walk of a tree that has one node in it in every window this
// editor has opened so far, and Window.SetHeight already knows to leave
// 'scroll' alone when the height it is given is the height it had.
func (e *editor) relayout() {
	tab := e.tabs.Current()
	if tab == nil || e.rows <= 0 || e.cols <= 0 {
		return
	}
	l := screen.Layout{
		Rows:        e.rows,
		Cols:        e.cols,
		ShowTabline: e.opt.G.ShowTabline,
		Tabs:        len(e.tabs.Pages),
		LastStatus:  e.opt.G.LastStatus,
		Windows:     len(tab.Windows()),
		CmdHeight:   e.opt.G.CmdHeight,
	}
	r := l.Regions()
	band := screen.Region{
		Row:  r.Text.Row,
		Rows: r.Text.Rows + r.Status.Rows,
		Cols: e.cols,
	}
	// internal/window's own layout first, internal/screen's over the top of
	// it. Two calls where there used to be one, and the second one is not
	// redundant: they are the two layouts TestScreenAndWindowAgreeOnSizedNodes
	// pins together, and each does something the other cannot.
	//
	// window.Layout is the one that knows which window is current, and vim
	// gives the odd row to the frame holding it. internal/screen's LayoutTree
	// passes nil for that -- it draws a frame and has no opinion about which
	// window a keystroke is in -- so with nothing but LayoutTree the odd row
	// went to whichever frame came first. Measured on a 60-line file in a
	// 40-row screen: ":set splitbelow" then CTRL-W s then L lands on line 14
	// in vim and landed on 13 here, and CTRL-W s then CTRL-W J then L did the
	// same, because in both the current window is the second of the two.
	//
	// It is also what gives the tab page its area and its options, which is
	// what every command in internal/window calls Relayout through. Without
	// this, TabPage.Relayout had no area to lay out in and did nothing, so a
	// tree that changed shape got its sizes from LayoutTree alone.
	tab.Layout(window.Rect{Row: band.Row, Col: band.Col, Rows: band.Rows, Cols: band.Cols}, e.opt)
	screen.LayoutTree(tab.Root, band, r.Status.Rows > 0)
}

// write puts the buffer on disk and marks it unmodified.
//
// fileBytes is where 'fixendofline' is applied, shared with the --oracle path
// so that the two ways pvim writes a file cannot disagree about whether the
// last line gets a separator.
func (e *editor) write() error {
	b := e.cur()
	if b == nil || b.Name == "" {
		return errNoFileName
	}
	// BufWritePre BEFORE the bytes are taken, or the vimrc's own
	// ":%s/\s\+$//e" would strip the trailing whitespace out of the buffer
	// after the copy that gets written had already been made.
	e.fireAutoCmds("BufWritePre", b.Name)
	if err := e.lspBeforeWrite(); err != nil {
		return err
	}

	data := fileBytes(e.ed)
	if err := os.WriteFile(b.Name, data, 0o644); err != nil {
		return err
	}
	b.MarkSaved()
	b.NewFile = false
	// After the bytes are down, not before: the undo file's header holds the
	// sha256 of the file it belongs to.
	e.persistAfterWrite(b.Name, data)
	e.lspAfterWrite()
	e.fireAutoCmds("BufWritePost", b.Name)
	return nil
}

// errNoFileName is E32, which is what ":w" on a buffer nobody has named says.
var errNoFileName = errors.New("E32: No file name")

// stop is ex.Context's Quit: the callback :q, :wq and :qall reach the frontend
// through.
//
// It records the decision rather than acting on it, because the frontend owns
// the loop and this is called from inside a command that may still have work
// to do: ":wq" writes after the quit is decided, and a ":q" inside a ":g" has
// to stop the whole global and not just the one line.
func (e *editor) stop(all, force bool) error {
	// Neither argument is read. "all" is ":qall" against ":q", which needs
	// more than one window to mean anything, and "force" is the bang, which
	// decides whether a modified buffer is prompted about -- and the prompt
	// happens on the ex side, through Prompt, before this is called. Both are
	// in the signature because internal/ex has them and dropping them here
	// would mean adding them back when the second window arrives.
	e.quit = true
	return nil
}
