package screen

import (
	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// Frame is one redraw's worth of input: everything outside this package that
// decides what the next screen looks like.
//
// It is a struct of plain data and not an interface into the editor on
// purpose. A test builds one by hand, calls Render, and compares the Grid
// against a literal string; nothing here has to be mocked and nothing here can
// call back into the editor while a frame is half drawn.
type Frame struct {
	// Tabs is the tab list. The current tab's windows are what gets drawn.
	Tabs *window.Tabs

	// Opt is the editor's options. Window-local values come off each
	// window's own Opt; this is the global and buffer half.
	Opt *options.Options

	// Names is what each buffer is called, for the status line and the
	// tabline. A buffer with no entry is "[No Name]", which is what vim
	// shows.
	Names map[*text.Buffer]string

	// Modified marks the buffers with unwritten changes, which the status
	// line shows as "[+]" and the tabline as a bare "+".
	Modified map[*text.Buffer]bool

	// ReadOnly marks the buffers the status line shows "[RO]" for.
	ReadOnly map[*text.Buffer]bool

	// Diff is each window's diff state, keyed by window id. Nil, or a window
	// with no entry, is a window not in diff mode, which is almost always all
	// of them: this is what internal/diff computes and what makes ]c and do
	// visible rather than merely correct.
	Diff map[int]DiffLines

	// Folds is each window's fold tree, keyed by window id. A window with no
	// entry has no folds, which is every window until somebody presses zf.
	Folds map[int]*Folds

	// Syntax is where the colour of the text itself comes from: the spans
	// internal/syntax works out by running vim's own syntax file for the
	// buffer's filetype. Nil when ':syntax' is off, when the filetype has no
	// syntax file, or in a graded run, and then every cell is Normal.
	//
	// It sits under 'hlsearch', 'incsearch' and the Visual selection and over
	// nothing: a search match on a comment draws as a search match, which is
	// vim. It is per window rather than per frame because two windows can show
	// two buffers, and a highlighter belongs to a buffer.
	Syntax map[int]SpanSource

	// Search is where 'hlsearch' gets its matches. Nil when 'hlsearch' is off
	// or no search has been made.
	Search MatchSource

	// IncSearch is the match 'incsearch' is previewing while a search is
	// being typed, drawn in IncSearch rather than Search. Nil otherwise.
	IncSearch *Match

	// Visual is the selection to paint in the Visual highlight, nil when not
	// in visual mode.
	Visual MatchSource

	// Cmdline is the command line being typed, if any.
	Cmdline Cmdline

	// Message is what the message area holds.
	Message Message

	// ShowCmd is the partial command 'showcmd' displays: "2d", "3", the size
	// of a visual selection. Empty most of the time.
	ShowCmd string

	// Mode is the 'showmode' text: "-- INSERT --", "-- VISUAL BLOCK --",
	// "recording @q". Drawn on the message line when nothing else is.
	Mode string

	// Pum is the completion menu, nil when it is not up.
	Pum *Pum

	// bottom is the last buffer line each window actually displayed, keyed by
	// window id, filled in by the last Render. It is not the same as
	// window.BotLine(), which counts one screen row per buffer line: a
	// wrapped line takes several rows and a closed fold takes one for many,
	// so only the renderer knows. The ruler's All/Top/Bot field reads it, and
	// so should H, M and L.
	bottom map[int]int

	// CursorOver moves the drawn cursor off the current window's own cursor
	// for one frame, which is the whole of 'showmatch': the frontend draws a
	// frame with this set to the matching bracket, arms the Hop's timer, and
	// draws another with it nil when the timer fires. Nothing in the buffer
	// moves.
	CursorOver *text.Pos

	// Display is 'display'. The only value that changes anything here is
	// "truncate", which vim's own defaults.vim sets and which puts "@@@" on
	// the last row when the last buffer line does not fit under 'wrap'.
	Display string

	// ShowBreak is 'showbreak', put at the start of every continuation row
	// under 'wrap'. Empty by default and in the vimrc.
	ShowBreak string

	// BreakAt is 'breakat': the characters 'linebreak' is allowed to break a
	// row after. Empty means vim's default.
	BreakAt string
}

// DefaultBreakAt is vim's 'breakat'. It is a constant here rather than an
// option field because nothing sets it, and a table of one string in
// internal/options would be an option that parses and does nothing.
const DefaultBreakAt = " \t!@*-+;:,./?"

// Match is a run of bytes on one buffer line that carries a highlight: what
// 'hlsearch' paints, what 'incsearch' previews, what a visual selection
// covers.
//
// Start and End are byte columns, 0-based, End exclusive. A match that covers
// a tab covers every cell the tab expands to, because the match is over bytes
// and the tab is one byte.
type Match struct {
	Line       int
	Start, End int
}

// SynSpan is a run of bytes on one buffer line that carries a syntax
// highlight.
//
// Start and End are byte columns, 0-based, End exclusive, the same as Match.
// Spans on a line do not overlap: where the syntax engine had one item inside
// another, the span carries the innermost, which is the colour vim draws.
//
// The name is Syn and not Span because Span in this package is already a run of
// screen cells, which is what a frontend blits; this is a run of buffer bytes,
// which is what a highlighter answers, and the two are different lengths the
// moment a tab or a wide rune is on the line.
type SynSpan struct {
	Start, End int
	HL         HLID
}

// SpanSource hands the renderer the syntax spans on one buffer line.
//
// The same shape and the same reason as MatchSource: a 40,000-line file has to
// answer for the twenty lines on the screen and must never be asked to compute
// the other 39,980. Whoever implements it decides how to cache, and
// internal/syntax caches the state at the end of every line so that a redraw
// after a keystroke is one line of work.
type SpanSource interface {
	SpansOn(line int) []SynSpan
}

// SpanList is a SpanSource over a slice, for a test. Nothing in the editor
// should use it: it has no per-line cache at all.
type SpanList map[int][]SynSpan

// SpansOn returns the spans on the line.
func (s SpanList) SpansOn(line int) []SynSpan { return s[line] }

// MatchSource hands the renderer the matches on one buffer line.
//
// It is a method and not a slice because 'hlsearch' on a 40k-line log has to
// answer for the twenty lines on the screen and must never be asked to produce
// the other 39,980. Whoever implements it decides how to cache.
type MatchSource interface {
	MatchesOn(line int) []Match
}

// MatchList is a MatchSource over a slice, in line order or not. It is what a
// test uses and what a search that has already been resolved can be wrapped
// in; a real 'hlsearch' should not use it.
type MatchList []Match

// MatchesOn returns the matches on the line.
func (m MatchList) MatchesOn(line int) []Match {
	var out []Match
	for _, x := range m {
		if x.Line == line {
			out = append(out, x)
		}
	}
	return out
}

// Cmdline is the command line being typed.
type Cmdline struct {
	// Active is whether a command line is open at all.
	Active bool
	// Prefix is ":", "/" or "?".
	Prefix string
	// Text is what has been typed after the prefix.
	Text string
	// Pos is the cursor's byte offset in Text.
	Pos int
}

// MsgLine is one line of the message area with the highlight group it is
// drawn in. An empty Group is Normal.
type MsgLine struct {
	Text  string
	Group string
}

// Message is the message area: what ":ls" printed, what an E-code said, and
// whether the screen is waiting for the reader to acknowledge it.
//
// Vim's model, which this copies: messages accumulate on the last 'cmdheight'
// rows, and when there are more of them than rows the message area grows
// upward over the text and the reader has to press something. The prompt is
// drawn in Question, which is the group vim's hit-return uses.
type Message struct {
	Lines []MsgLine

	// PressEnter is whether "Press ENTER or type command to continue" is
	// showing, which is the state where every key is swallowed.
	PressEnter bool
}

// PressEnterText is what vim writes when the message area has overflowed.
const PressEnterText = "Press ENTER or type command to continue"

// hasText reports whether the message area has anything to say.
//
// A frontend that builds one MsgLine per frame hands over an empty one when
// there is no message, which is what cmd/pvim does, so the length of the slice
// is not the question. An empty message is no message.
func (m Message) hasText() bool {
	for _, l := range m.Lines {
		if l.Text != "" {
			return true
		}
	}
	return false
}

// rows is how many screen rows the message area wants: one per line, plus one
// for the press-enter prompt.
func (m Message) rows() int {
	n := len(m.Lines)
	if m.PressEnter {
		n++
	}
	return n
}

// msgOverflows reports whether the message area needs more rows than the
// command line has, which is the state where it grows upward over the text and
// ends in the press-enter prompt. The ruler is gone for the whole of it:
// measured on vim 9.2.0321 at 40x10, ":echo repeat('x',25)" leaves the message
// on the second-last row, the prompt on the last, and no ruler anywhere.
func (f *Frame) msgOverflows(cmdRows int) bool {
	return f.Message.rows() > cmdRows
}

// BotLine is the last buffer line the window with this id displayed in the
// frame Render last drew, or zero before the first one.
//
// It is the number window.BotLine() cannot work out, because the answer
// depends on 'wrap', on how wide the window is, and on which folds are closed.
// Whoever implements H, M and L against a wrapped window wants this one.
func (f *Frame) BotLine(id int) int { return f.bottom[id] }

// noteBotLine records what a window ended up showing.
func (f *Frame) noteBotLine(id, line int) {
	if f.bottom == nil {
		f.bottom = map[int]int{}
	}
	f.bottom[id] = line
}

// name returns what to call a buffer on the furniture.
func (f *Frame) name(b *text.Buffer) string {
	if n := f.Names[b]; n != "" {
		return n
	}
	return "[No Name]"
}

// modified reports whether the buffer has unwritten changes.
func (f *Frame) modified(b *text.Buffer) bool { return f.Modified[b] }

// readOnly reports whether the buffer is read-only.
func (f *Frame) readOnly(b *text.Buffer) bool { return f.ReadOnly[b] }

// folds returns a window's fold tree, which may be nil.
func (f *Frame) folds(w *window.Window) *Folds { return f.Folds[w.ID] }

// opts returns the editor options, never nil, so that a Frame built by hand in
// a test does not have to carry a full option set to draw one line.
func (f *Frame) opts() *options.Options {
	if f.Opt == nil {
		o := options.Defaults()
		f.Opt = &o
	}
	return f.Opt
}

// breakAt returns the 'breakat' set in use.
func (f *Frame) breakAt() string {
	if f.BreakAt == "" {
		return DefaultBreakAt
	}
	return f.BreakAt
}

// DiffLines is what a window in diff mode can be asked about its buffer.
//
// The interface is here and the implementation is internal/diff, so that this
// package keeps knowing nothing about Myers and internal/diff keeps knowing
// nothing about a Grid. cmd/pvim owns the pairing and fills Frame.Diff.
type DiffLines interface {
	// Filler is how many blank rows go ABOVE line lnum, standing where the
	// other side has lines this one does not. Measured against "vim -d": the
	// filler for a block sits below it, which is the same thing as above the
	// first line after it. A count for lnum past the last line is the filler
	// at the end of the buffer.
	Filler(lnum int) int
	// Line is the whole-line group: DiffAdd for a line only this side has,
	// DiffChange for one both sides have differently, and false for a line
	// that is the same on both.
	Line(lnum int) (DiffKind, bool)
	// Text is the byte range inside a DiffChange line that actually differs,
	// which vim paints DiffText over the DiffChange. An empty range is
	// possible and real: "aaa" against "aaaa" gives one side nothing.
	Text(lnum int) (start, end int, ok bool)
}

// DiffKind is which of the two whole-line diff groups a line takes.
type DiffKind int

const (
	// DiffKindAdd is a line only this side has.
	DiffKindAdd DiffKind = iota
	// DiffKindChange is a line both sides have, differently.
	DiffKindChange
)
