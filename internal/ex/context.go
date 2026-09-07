package ex

import (
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/quickfix"
	"github.com/pkar/pvim/internal/substitute"
	"github.com/pkar/pvim/internal/window"
)

// Context is the editor, as an ex command sees it.
//
// It is a struct of pointers rather than an interface because every field is
// state a command mutates and there is exactly one implementation: an
// interface here would be six method calls per command and a mock nobody
// wants to keep in step. The seam that matters is the other one, between the
// vimrc parser and this package, and that is an interface for a real reason
// (see internal/vimrc).
type Context struct {
	// Ed is the mode machine: the buffer, the cursor, the registers and
	// everything half-typed. A command that changes text does it through Ed
	// so that the undo blocks and the message line stay in one place.
	Ed *mode.Editor

	// Tabs is every tab page, of which one is current, and inside it one
	// window, which is the one every command acts on unless it says
	// otherwise.
	Tabs *window.Tabs

	// Opt is the option state: the globals, and the local halves belonging to
	// the current buffer and window.
	Opt *options.Options

	// QF is the quickfix stack the :c commands walk.
	QF *quickfix.Stack

	// Sub is what ":s" and ":g" remember between commands: the last pattern,
	// the last replacement, the flags an "&" inherits and how many globals are
	// running. It lives on the Context and not in internal/substitute because
	// there is one per editor and a package-level one would make two buffers
	// in two tabs share a ":s//" that neither of them typed.
	Sub *substitute.State

	// Cmds are the user commands ":command!" defined.
	Cmds UserCommands

	// Msg puts a line on the message area. It is a function rather than a
	// buffer so that the oracle can redirect it into the file it diffs, which
	// is what makes vim's messages part of the comparison and not a thing
	// nobody checks.
	Msg func(string)

	// MsgKeep is Msg for a message the redraw after the command puts back
	// once more: vim's set_keep_msg. It is a second hook rather than a flag
	// on Msg because the frontend owns the message line and this is the
	// frontend's distinction to make; when it is nil the message goes out
	// once through Msg and nothing is lost but a duplicate line in a redirect.
	MsgKeep func(string)

	// MsgRaw writes into the message stream with no newline of its own at
	// either end. One command needs it, ":!", whose echo of the command line
	// ends in a carriage return rather than a newline; when it is nil the
	// text goes through the editor's own raw writer.
	MsgRaw func(string)

	// Err puts an error on the message area. Vim highlights these
	// differently and ":silent!" swallows them, which is the only reason it
	// is not the same function as Msg.
	Err func(string)

	// Prompt asks a yes/no/cancel question on the command line and returns
	// the answer. 'confirm' is set in the vimrc, so ":q" on a modified buffer
	// goes through here rather than through an NSAlert, which is what the
	// plan means by not building the GUI confirm dialog.
	Prompt func(question string, choices string) (byte, error)

	// PromptQuiet is the ":s" "c" flag's prompt, which vim draws differently
	// from Prompt: the answer is not echoed and the prompt line is wiped
	// afterwards. It is a second hook and not a flag because the frontend
	// owns the message line and this is the frontend's difference to make;
	// when it is nil the confirm falls back to Prompt.
	PromptQuiet func(question string, choices string) (byte, error)

	// Quit is how a command stops the editor: ":q", ":wq", ":qall". It is a
	// callback rather than a returned error so that ":wq" can write first and
	// so that a ":q" inside a ":g" stops the whole thing.
	Quit func(all bool, force bool) error

	// Bufs is the buffer list: the file names, the numbers and the modified
	// flags that internal/text deliberately does not carry. ":ls", ":b",
	// ":w" and the 'confirm' prompt all read it.
	Bufs *BufList

	// Open makes b the buffer the editor and the current window act on, and
	// is how ":e", ":b" and ":bn" change what is on screen.
	//
	// It is a hook and not a method because internal/mode has no way to be
	// handed a second buffer: an Editor is built around one and there is no
	// setter, so swapping one in means building a new Editor and losing the
	// registers, the search state and the last insert with it. A frontend
	// that can do better supplies this; when it is nil the fallback in
	// swapBuffer does exactly that lossy thing, and this comment says so
	// rather than hiding it.
	Open func(b *Buf)

	// Map runs one command of the ":map" family: ":nmap", ":nnoremap",
	// ":unmap", ":mapclear" and the rest of them. The string is the command
	// line with the name resolved to its full spelling and the bang and the
	// arguments behind it, so ":nn <silent> gh:nohl<CR>" arrives as
	// "nnoremap <silent> gh:nohl<CR>".
	//
	// A string and not a parsed struct because the parse is internal/vimrc's.
	// That package measured all of it -- <buffer>, <silent>, <expr>,
	// <unique>, <nowait>, where the left-hand side stops and the right one
	// starts, and when 'mapleader' is read -- and it imports this package, so
	// this package cannot import it back. The frontend owns the map table and
	// owns the parser; this hook is the ex layer saying which command was
	// typed and handing over the text.
	//
	// A nil hook answers E319 rather than doing nothing quietly. Until this
	// existed the whole family either said E319 by having no handler at all
	// or, for ":map" itself, was not in the table and said E492.
	Map func(line string) error

	// PreWrite and PostWrite bracket a ":w".
	//
	// They exist because everything a write is supposed to trigger lives above
	// this package: the vimrc's BufWritePre autocommands, which on this config
	// strip trailing whitespace, and the format-on-save table, which runs
	// gofmt through the language server and "terraform fmt -" over a .tf
	// buffer. Until they landed, ":w" fired neither and only "ZZ" did, because
	// ZZ goes through cmd/pvim's own write path and ":w" comes here.
	//
	// PreWrite runs BEFORE the bytes are taken out of the buffer, or a strip
	// that edits the buffer would land after the copy that gets written.
	// An error from it stops the write, which is vim: a BufWritePre that fails
	// leaves the file alone.
	PreWrite  func(name string) error
	PostWrite func(name string)

	// searchSeen is the last pattern noteSearch copied out of the mode
	// machine. See noteSearch for why the copy needs a memory.
	searchSeen string

	// sourced is vim's "sourcing": the command line being run was not typed
	// at the colon prompt. A ":g" sets it around each command it runs. See
	// Context.sourcedError, which is the only thing that reads it.
	sourced bool

	// undoHeld says a ":g" is holding the buffer's undo block open across the
	// whole global. Only exGlobal sets it, and only editIfChanged reads it:
	// a hold makes text.Buffer.DropUndoBlock a no-op, so a command that wants
	// to drop an empty block has to take the hold off first. See
	// Context.editIfChanged.
	undoHeld bool

	// searchPat is vim's spats[RE_SEARCH]: the pattern the last search looked
	// for, as opposed to the last substitute pattern.
	//
	// The mode machine keeps one slot, which is vim's RE_LAST and is what "n"
	// repeats, so a ":s" writing it is correct and is why "n" after a ":s"
	// repeats the substitution. "\/" and "\?" want the other slot, which
	// only a search writes, and this is it. See Context.searchSlot.
	searchPat string
}

// Window returns the window every command acts on: the current tab's current
// window.
func (c *Context) Window() *window.Window {
	if c == nil || c.Tabs == nil {
		return nil
	}
	return c.Tabs.Window()
}

// Sync reconciles the two places the cursor lives.
//
// internal/mode owns it while keys are being dispatched and internal/window
// owns it while the window is being scrolled, and neither package can import
// the other. Every ex command that moves the cursor calls this on the way out:
// the editor's cursor is pushed into the window, the window is scrolled to
// follow it, and what the window now shows is pushed back so that the next H,
// M or L measures against it.
//
// It is a function rather than three assignments at every call site because
// the day a second thing needs reconciling -- 'curswant', probably -- there is
// one place to put it.
func (c *Context) Sync() {
	w := c.Window()
	if c.Ed == nil || w == nil {
		return
	}
	w.View.Cursor = c.Ed.Cursor()
	w.ScrollToCursor(w.ScrollOff(c.Opt))
	c.Ed.SetWindow(c.MotionWindow())
}

// MotionWindow converts the current window's Visible into what internal/motion
// wants for H, M and L.
//
// This function is the reason the 43 fuzz failures on H, M and L are fixable:
// internal/motion has taken a motion.Window and nothing has ever
// filled one in, so all three fail with Ok false and cmd/pvim beeps and says
// nothing. internal/window cannot fill it in without importing internal/motion,
// and it must not; this package imports both, so it is the place.
func (c *Context) MotionWindow() motion.Window {
	w := c.Window()
	if w == nil {
		return motion.Window{}
	}
	v := w.Visible()
	return motion.Window{
		Top:      v.Top,
		Bottom:   v.Bottom,
		AtTop:    v.AtTop,
		AtBottom: v.AtBottom,
		LeftCol:  v.LeftCol,
		Height:   v.Height,
	}
}

// ApplyWindowOptions copies the window half of the option state onto the
// window itself, which is the other place a window-local option lives.
//
// It is the twin of ModeOptions and it is exported for the same reason: it is
// the one place a typed option becomes a window's own, and every caller that
// has just moved the option state has to run both or the move is half done.
// internal/ex's ":set" does, through pushOptions. cmd/pvim's applyOptions --
// the vimrc loader and the autocommand dispatcher -- did not, which is why
// the vimrc's "au BufEnter *.txt,*.md setlocal spell" reached opt.W and never
// reached the window; that is what this being exported is for.
//
// 'scroll' is the exception and it goes the other way. The WINDOW writes that
// one: SetHeight seeds it from the height and a count on CTRL-U or CTRL-D
// replaces it, so a zero in the option state means nothing has ever set it
// there and the window's own value is the live one.
func ApplyWindowOptions(o *options.Options, w *window.Window) {
	if o == nil || w == nil {
		return
	}
	scroll := w.Opt.Scroll
	w.Opt = o.W
	if w.Opt.Scroll == 0 {
		w.Opt.Scroll = scroll
	}
}

// ModeOptions maps the option state onto the flat struct internal/mode reads.
//
// The adapter lives here and not in either package because internal/options is
// a leaf that must not know what a mode is, and internal/mode is owned
// elsewhere and is not edited from here. So this is the single place a typed option
// becomes a mode option, and an option added to internal/options that the mode
// machine needs is wired in one function.
//
// TODO: unimplemented beyond the fields that already exist
// on both sides. Every field of mode.Options has a home in internal/options
// and the mapping is mechanical; it is a stub rather than written blind
// because the mode machine's Width and Wrap come from the window and not from
// the options, and getting that wrong makes gj and g$ silently wrong.
func ModeOptions(o *options.Options, w *window.Window) mode.Options {
	m := mode.DefaultOptions()
	if o == nil {
		return m
	}
	m.TabStop = o.B.TabStop
	m.ShiftWidth = o.B.ShiftWidth
	m.SoftTabStop = o.B.SoftTabStop
	m.ExpandTab = o.B.ExpandTab
	m.ShiftRound = o.G.ShiftRound
	m.AutoIndent = o.B.AutoIndent
	m.SmartIndent = o.B.SmartIndent
	m.CinWords = o.B.CinWords
	m.Backspace = o.G.Backspace
	m.TextWidth = o.B.TextWidth
	m.JoinSpaces = o.G.JoinSpaces
	m.IgnoreCase = o.G.IgnoreCase
	m.SmartCase = o.G.SmartCase
	m.WrapScan = o.G.WrapScan
	m.NoMagic = !o.G.Magic
	m.HlSearch = o.G.HlSearch
	m.IncSearch = o.G.IncSearch
	m.IsKeyword = o.B.IsKeyword
	m.WhichWrap = o.G.WhichWrap
	m.StartOfLine = o.G.StartOfLine
	m.MatchPairs = o.B.MatchPairs
	m.Paragraphs = o.G.Paragraphs
	m.Sections = o.G.Sections
	m.Selection = o.G.Selection
	m.VirtualEdit = o.VirtualEditValue()
	m.Clipboard = o.G.Clipboard
	m.FixEndOfLine = o.B.FixEndOfLine
	m.CompleteOpt = o.CompleteOptValue()
	m.Report = o.G.Report
	if w != nil {
		// Wrap and Width are what gj, gk and g$ mean by a display line, and
		// both belong to the window rather than to the option state: two
		// windows on one buffer have two widths.
		m.Wrap = w.Opt.Wrap
		m.Width = w.View.Width
		m.ScrollOff = w.ScrollOff(o)
		m.ConcealLevel = w.Opt.ConcealLevel
	} else {
		m.Wrap = o.W.Wrap
		m.ScrollOff = o.ScrollOffValue()
		m.ConcealLevel = o.W.ConcealLevel
	}
	return m
}
