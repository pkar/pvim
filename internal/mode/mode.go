// Package mode is the editor: the state machine that turns keystrokes into
// edits, and the one seam every frontend talks to.
//
// The whole interface is Editor.Key. A frontend -- the AppKit window, the
// terminal, cmd/pvim's headless --oracle mode, a test -- decodes a keystroke
// into a key.Key and hands it over, then reads the buffer and the cursor back
// out. Nothing above this package knows what mode the editor is in, and nothing
// in this package knows what a window is. That is what makes the oracle
// possible: the same Editor that a window drives is the one cmd/oracle feeds a
// script to with no terminal at all.
//
// The state that makes vim vim, and that a first implementation always leaves
// out, is on Editor and named in its fields: two pending counts, because 2d3w
// deletes six words and not three; a pending register, because "add is a delete
// into a; the text of the last insert, because . after an A replays the typing;
// the keys of the last change, because . replays a command and not an effect;
// and the macro recorder, because q is a second recorder running alongside the
// first with different rules about what it keeps.
package mode

import (
	"errors"
	"strconv"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/motion"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/search"
	"github.com/pkar/pvim/internal/text"
)

// Mode is which mode the editor is in.
type Mode int

const (
	// Normal is normal mode, and the mode every command comes back to.
	Normal Mode = iota
	// Insert is insert mode.
	Insert
	// Replace is R: insert that overwrites, and whose backspace restores what
	// was there rather than deleting what was typed.
	Replace
	// VisualChar, VisualLine and VisualBlock are v, V and CTRL-V. They are
	// three modes and not one with a flag because the keys mean different
	// things in each: I and A in a block insert down a column, and in the
	// other two they do not exist.
	VisualChar
	VisualLine
	VisualBlock
	// OperatorPending is the mode between d and the motion that completes it.
	// It is a real mode and not a flag on Normal because :help omap binds keys
	// in it and because CTRL-V typed here is a forced motion and not a visual
	// selection.
	OperatorPending
	// Cmdline is :, / and ?. The ex parser is not wired in yet; here it is the
	// search prompt.
	Cmdline
)

// String names the mode the way :echo mode() does, which is what the oracle
// state dump and a statusline both want.
func (m Mode) String() string {
	switch m {
	case Insert:
		return "i"
	case Replace:
		return "R"
	case VisualChar:
		return "v"
	case VisualLine:
		return "V"
	case VisualBlock:
		return "\x16"
	case OperatorPending:
		return "no"
	case Cmdline:
		return "c"
	default:
		return "n"
	}
}

// Errors Key can return.
var (
	// ErrQuit is the editor asking to stop. It is an error and not a bool
	// because it has to travel out through the same return every frontend
	// already checks, and cmd/pvim sorts it from a real failure at the one
	// place that turns an error into an exit status.
	ErrQuit = errors.New("mode: quit")
	// ErrNotImplemented is a key the mode machine does not handle yet. It is
	// deliberately not a silent no-op: an unimplemented key that does nothing
	// shows up in the oracle as a buffer that is subtly wrong three keystrokes
	// later, and one that fails shows up as itself.
	ErrNotImplemented = errors.New("mode: key not implemented")
)

// pending is the half-typed command: everything between the first keystroke of
// a command and the one that completes it.
//
// It is a struct of its own rather than fields on Editor because "abandon the
// command" is one assignment, and because 'showcmd' renders exactly this.
type pending struct {
	// count1 is the count typed before the operator and count2 the one typed
	// after it. vim multiplies them, so 2d3w is six words, and keeping them
	// apart matters because a dot repeat with its own count replaces both with
	// one.
	count1, count2 int
	// reg is the "x prefix, zero when none was typed.
	reg byte
	// op is the operator waiting for a motion.
	op operator.Op
	// opKeys is the keys that named the operator, "d" or "gu". It is kept
	// because the doubled form of a two-key operator is guu as well as gugu,
	// and answering that needs the keys and not the Op.
	opKeys string
	// force is the v, V or CTRL-V typed after the operator.
	force motion.Force
	// trimYank says the operator is "zy" and not "y": vim 9's yank without
	// trailing white space. It is a flag beside the Op rather than an Op of
	// its own because the two are the same operator everywhere except the
	// blockwise register value, and a second Op would have to be handled in
	// every switch that mentions OpYank.
	trimYank bool
	// keys is what has been typed towards this command, kept in vim's own
	// notation so that motion.ByKeys can be asked whether it is a motion yet
	// and IsPrefix whether it might still become one.
	keys []key.Key
}

// count is the two counts multiplied, with vim's default of one filled in when
// neither was typed.
func (p pending) count() int {
	switch {
	case p.count1 == 0 && p.count2 == 0:
		return 0
	case p.count1 == 0:
		return p.count2
	case p.count2 == 0:
		return p.count1
	default:
		return p.count1 * p.count2
	}
}

// count1max is count() with a floor of 1, which is what a motion that repeats
// wants: "d3*" searches three times and "d*" once.
func (p pending) count1max() int {
	if n := p.count(); n > 0 {
		return n
	}
	return 1
}

// recorder is q: keys going into a register until the next q.
type recorder struct {
	// reg is the register being recorded into, zero when nothing is
	// recording. An uppercase name appends to the lowercase register.
	reg byte
	// keys is what has been recorded so far. The q that stops the recording is
	// not in it, which is the one rule that makes a recorded macro replayable.
	keys []key.Key
}

// dot is the last change, kept as the keys that made it.
//
// Keys, not a description of the edit. It is the only representation that
// replays cwfoo<Esc>, a "add, an operator with a forced motion and a
// count-with-a-count without a second implementation of each, and it is how
// vim itself does it. The count is kept apart because . with a count of its own
// replaces the original's count rather than multiplying by it.
type dot struct {
	keys  []key.Key
	count int
	// vis is the visual selection an operator was applied to, when that is
	// what the change was. vim does not replay the keys that made a visual
	// selection: it applies the operator to the same AMOUNT of text at the
	// cursor, which is why . after vjd deletes the same shape somewhere else
	// and not whatever a j lands on now. See :help visual-repeat.
	vis visualRepeat
	// reg is the "x the change was made with, kept apart from the keys for
	// the same reason the count is: . uses it again rather than whatever
	// register was typed on the . itself, and a numbered one is incremented.
	reg byte
}

// visualRepeat is the size and shape of a visual selection, which is all that
// . needs to do it again somewhere else.
type visualRepeat struct {
	ok   bool
	mode Mode
	op   operator.Op
	// lines is how many lines the selection spanned beyond its first, and
	// chars is how many characters it covered when it was on one line. endCol
	// is the byte column the selection ended in, which is what a multi-line
	// charwise repeat uses.
	lines  int
	chars  int
	endCol int
	// arg is the character an operator needed after itself: the replacement
	// for r.
	arg byte
	// ins is the blockwise I or A, which is not an operator at all: the
	// repeat makes the same block at the cursor and types the same text down
	// it. Zero when the change was not one.
	ins byte
	// insert is what was typed into the insert a visual c started, which the
	// repeat types again after it has made the same hole.
	insert []key.Key
	// toEOL is a selection made with $, which keeps running to the end of
	// each line however long it is.
	toEOL bool
}

// Editor is the mode machine: one buffer, one cursor, one set of registers,
// and everything half-typed.
//
// One Editor is one window on one buffer. A later change grows a window list
// above it and the registers move up with it, because vim's registers are
// global and a yank in one window pastes in another.
type Editor struct {
	buf  *text.Buffer
	cur  text.Pos
	mode Mode
	opt  Options

	regs *register.File
	// mctx is the motion state that outlives one motion: the last f, the
	// column j and k are aiming for, and what the window is showing.
	mctx motion.Context
	// search is the last pattern, its direction and its offset: what n
	// repeats and what "/ holds.
	search search.State

	pend pending
	rec  recorder
	dot  dot
	// replaying is true while the dot record or a macro is being fed back
	// through Key, which stops the replay from recording itself.
	replaying bool
	// atMacro narrows that to @: keys coming out of a register rather than out
	// of the redo record. The two differ on the message line and only there.
	// vim's skip_showmode() gives up when characters are waiting in the
	// typeahead that nobody typed, which a macro replay is and a dot repeat is
	// not, so 3@a over a recorded "I-<Esc>" prints no mode message at all and
	// "." over the same insert prints "-- INSERT --" again.
	atMacro bool
	// aborted is set by the beep a failed command makes. It is how a macro
	// stops: playKeys checks it after every key, so a recorded search that
	// finds nothing ends the replay, and the script that was feeding the
	// editor carries on, which is what vim does by throwing away its
	// typeahead and leaving the script alone.
	aborted bool

	// insert is the text typed during the insert that is running or that just
	// ended, which is what ". holds and what a dot repeat of an insert needs.
	insert []byte
	// visual is the anchor of the current visual selection, the end the cursor
	// is not on. Meaningless outside the three visual modes.
	visual text.Pos

	// msg is the message line: the log a frontend shows and the byte stream
	// vim's:redir would have caught, which is what the oracle diffs.
	msg messages

	// wait is set by a command that needs the next keystroke raw rather than
	// dispatched: the target of f, the name of a mark, the character r
	// replaces with. It is a closure and not a state enum because there are
	// fifteen of them and each wants a different captured value.
	wait func(k key.Key) error

	// cmdKeys is every key typed towards the command in progress, with the
	// count and the register prefix left out. It is what dot repeat records,
	// which is why the two that . replaces are the two that are missing.
	cmdKeys []key.Key
	// numbered is set while an operator runs over a motion that forces its
	// delete into "1 however short it was: the ten in
	// register.MotionForcesNumbered. It is a field rather than an argument
	// because it has to reach operator.Request through three call sites that
	// otherwise know nothing about it.
	numbered bool

	// dotFromVisual says the command that is finishing already recorded
	// itself as a visual repeat, so the keys it was made of are not the
	// change. Without it the finish of a visual d would overwrite the shape
	// with a bare "d", which replayed in normal mode waits for a motion that
	// never comes.
	dotFromVisual bool
	// fromPos says the command in progress was completed by a POSITION rather
	// than by keys, which is a mouse click standing in for a motion. There is
	// nothing in cmdKeys that would replay it, so recordDot leaves the last
	// change alone rather than recording a bare "d" that would swallow the
	// next key. See position.go for what vim does instead.
	fromPos bool
	// inDot is true while . is replaying. It stops the replay from becoming
	// the new last change, which would leave . repeating itself and a count
	// on it lost. A macro replay does not set it: vim's . after @a repeats
	// the last change the macro made.
	inDot bool
	// lastAt is the register the last @ played, which is what @@ repeats.
	lastAt byte
	// remap is the frontend's map layer, for the one replay that goes through
	// it. See SetRemap and playKey.
	remap func(key.Key) error
	// mapReplay says the keys being played back came out of a register and
	// are to be mapped again. It is a field of its own and not atMacro
	// because a "." inside an "@a" is still a dot repeat: vim replays it out
	// of the stuff buffer with mapping off, and atMacro is true for both.
	mapReplay bool

	// ins is the insert or replace session in progress, and the one that just
	// ended: the count still to be replayed, the autoindent that will be
	// dropped if nothing is typed on top of it, and the stack replace mode's
	// backspace restores from.
	ins insState
	// comp is the CTRL-N and CTRL-P completion in progress.
	comp completion

	// omni is the CTRL-X CTRL-O source, nil until SetOmniFunc installs one.
	omni func(prefix []byte) [][]byte

	// pendingCtrlX is CTRL-X waiting for the key that says which completion.
	pendingCtrlX bool
	// cmd is the / and ? command line.
	cmd cmdline

	// lastVis is the selection '< and '> name and gv brings back: the two
	// ends and which of the three visual modes made it.
	lastVis visualSpan

	// clipSel is the selection 'autoselect' last put on the clipboard, which
	// is vim's clip_star.start, .end and .vmode. See autoSelect: it is
	// compared and not just remembered, and it is deliberately not cleared
	// when visual mode ends, because vim does not clear it either.
	clipSel visualSpan

	// scriptInput says the keys are coming from a file rather than a person,
	// which is what stops the undo blocks being closed between commands. See
	// closeUndo.
	scriptInput bool

	// undoOpen says an undo block is open. One user-visible change is one
	// block, and c is the case that makes it a field: the block opens on the
	// operator and does not close until the insert it started ends.
	undoOpen bool

	// keyLines is how many lines the buffer had when the current key arrived,
	// which is where an insert started by an operator takes its own idea of
	// "before" from. See Key and enterInsert.
	keyLines int

	// uline and utext are U's memory: the line most recently changed and what
	// it held before the first change to it. U is not the undo tree, it is one
	// line's worth of before, and it is itself an undoable change.
	uline int
	utext []byte
	uheld bool
	// ucol is the column the cursor was in when the line was saved, which is
	// where U puts it back. vim's b_u_line_colnr, set by u_saveline alongside
	// the text: "AX<Esc>U" leaves the cursor in column 3 of "aaa", where the
	// insert started, and not in column 1.
	ucol int

	// prevJump is where the last-jump mark was before the command in progress
	// moved it, which is vim's w_prev_pcmark and is how a jump that did not
	// move gives the mark back. Line zero means there is nothing to give
	// back. See setPCMark and checkPCMark.
	prevJump text.Pos

	// jumps is the jumplist and jumpIdx the index CTRL-O walks back from,
	// which is vim's w_jumplist, w_jumplistlen and w_jumplistidx. They are on
	// the Editor and not on the Buffer because in vim a jumplist belongs to a
	// WINDOW: two windows on one file have two of them, and a split copies
	// the list rather than sharing it. See jumps.go.
	jumps   []Jump
	jumpIdx int

	// name is the file the buffer was read from, which one thing in the mode
	// machine needs: "[I" prints it above the list of matching lines, the way
	// vim does. Empty until the frontend says otherwise, and printed as
	// "[No Name]" then, which is what vim calls a buffer with no file.
	name string
}

// New returns an editor over a buffer, in normal mode with the cursor at the
// start of it and vim's own default options.
func New(b *text.Buffer) *Editor {
	if b == nil {
		b = text.New()
	}
	e := &Editor{
		buf:  b,
		cur:  text.Pos{Line: 1},
		mode: Normal,
		opt:  DefaultOptions(),
		regs: register.NewFile(),
		// vim starts with w_set_curswant TRUE, so the first j or k asks the
		// cursor where it is instead of aiming at column zero. See
		// motion.CurswantUnset for the measurement that says it matters.
		mctx: motion.Context{Curswant: motion.CurswantUnset},
		// One entry already, because vim's do_ecmd() calls setpcmark() as it
		// starts editing a file: a fresh buffer has a jumplist of one and a
		// CTRL-O typed as the first key moves nowhere rather than beeping.
		// Measured on "vim --clean file" with one "j" typed.
		jumps:   []Jump{{Pos: text.Pos{Line: 1}}},
		jumpIdx: 1,
	}
	// The buffer moves the jumplist as the text moves, which is the half of
	// vim's mark_adjust() that reaches into a window. See text.Tracker.
	b.Track(jumpTracker{e})
	return e
}

// Key feeds one keystroke to the editor.
//
// This is the whole seam between the mode machine and every frontend. It
// returns nil for a key it handled, including one it deliberately ignored and
// one it beeped at; ErrQuit when the editor has been asked to stop; and any
// other error when something went wrong that the caller has to know about.
//
// It is not re-entrant. A macro replay feeds keys back through this method from
// inside itself and guards that with the replaying flag; a caller must not.
func (e *Editor) Key(k key.Key) error {
	// The recorder sees the key before anything else does and sees it raw,
	// because a macro is the keys that were typed and not the commands they
	// turned into: q records the f and the character f is looking for, and a
	// macro that recorded "the last find repeated" would replay something
	// else. Keys arriving from a replay are not recorded, or @a inside a
	// recording would land in the register twice.
	if e.rec.reg != 0 && !e.replaying {
		e.rec.keys = append(e.rec.keys, k)
	}
	return e.command(func() error { return e.dispatch(k) })
}

// command runs one keystroke's worth of work with everything vim's main loop
// does around normal_cmd() wrapped about it.
//
// It is split out of Key because MotionToPos is the other way in: a mouse
// click completing an operator is a whole command and wants the same
// bookkeeping a key gets, and the alternative is these five lines written
// twice with one of the copies drifting.
func (e *Editor) command(run func() error) error {
	mode, lines, said := e.modeMessage(), e.buf.LineCount(), e.msg.writes
	// The line count before the command, which enterInsert wants and cannot
	// take for itself: "2C" deletes a line and then starts inserting, so by
	// the time enterInsert runs the count it would measure is already the one
	// after the change, and the mode message that depends on it comes out
	// once where vim says it twice.
	e.keyLines = lines
	err := run()
	e.checkPCMark()
	e.autoSelect()
	e.redraw(mode, lines, said)
	return err
}

// setPCMark is vim's setpcmark(): it puts the last-jump mark where the cursor
// was before a jump, remembers where the mark was so that checkPCMark can put
// it back, and pushes the jumplist.
//
// Every jump goes through here rather than through Buffer.SetMark, because the
// remembering is half of what the mark means and the jumplist is the other
// half. The two come apart again in checkPCMark, which gives the mark back and
// leaves the list alone. See checkPCMark and jumps.go.
func (e *Editor) setPCMark(p text.Pos) {
	e.pushJump(p)
	old, ok := e.buf.Mark(text.MarkLastJump)
	if !ok {
		// vim's w_pcmark starts as line zero and checkpcmark() reads that as
		// "there is nothing to put back", which is the same thing this is.
		old = text.Pos{}
	}
	e.prevJump = old
	e.buf.SetMark(text.MarkLastJump, p)
}

// checkPCMark is vim's checkpcmark(), which normal_cmd runs at the end of
// every command: a jump that left the cursor exactly where it started hands
// the last-jump mark back to whatever set it last, so it does not consume the
// mark.
//
// It is a rule about jumps in general and not about H, M and L, and vim's
// EQUAL_POS compares the column as well as the line. Measured on 400 lines in
// a 39-row window, the buffer written out and diffed for the line that lost a
// byte to the x:
//
//	100GM``x line 1, because M was already on line 100
//	100G100G``x line 1
//	GG``x line 1
//	100GH``x line 100, because H did move
//	100G0%``x line 100 when it holds "(abcdefg)": the % moved the column
//	 and nothing else, which is enough to keep the mark, so a
//	 rule written on the line number alone gets this one wrong
//
// The second half of vim's condition is a pcmark whose line was deleted by the
// command that set it, which is a mark this buffer reports as unset.
func (e *Editor) checkPCMark() {
	prev := e.prevJump
	e.prevJump = text.Pos{}
	if prev.Line == 0 || !e.betweenCommands() {
		return
	}
	if at, ok := e.buf.Mark(text.MarkLastJump); !ok || at == e.cur {
		e.buf.SetMark(text.MarkLastJump, prev)
	}
}

// betweenCommands reports whether the key just handled finished the command it
// belonged to, which is where vim's normal_cmd returns and where checkPCMark
// has a cursor to compare against.
//
// It is not commandDone: insert mode is inside a command as far as the message
// line is concerned, and it is not as far as the mark is, because CTRL-O runs
// one normal-mode command in the middle of an insert and vim checks the mark
// after it like any other.
func (e *Editor) betweenCommands() bool {
	return e.wait == nil && e.pend.op == operator.OpNone && e.mode != Cmdline
}

// autoSelect is 'clipboard' containing "autoselect": the visual selection goes
// onto the clipboard as it CHANGES, which is vim's clip_update_selection() and
// is not the same thing as once per keystroke.
//
// This runs at the end of every Key, as vim runs clip_update_selection() at the
// end of every normal_cmd(), and like vim it compares the two ends and the
// visual mode against what it last wrote and does nothing when all three are
// the same. That is not an optimisation dressed up as fidelity: measured
// against /opt/homebrew/bin/vim through a pty, reading NSPasteboard's
// changeCount either side, "v2" copies once and not twice, "v22" once and not
// three times, "vo" once, "vaw" twice and not three times. The keys that do
// not change a selection -- a count digit, a "x prefix, the g of a half-typed
// g command, the a of a text object, and a command that beeped -- are most of
// what gets typed in visual mode, and each one used to cost a main-thread
// round trip to the pasteboard, which internal/gui's own benchmark puts at
// 215us to 240us. "v10j" paid two of them for the digits alone.
//
// The comparison is on the selection and not on the text, which is why "vlh"
// writes three times in both editors: back at the one-character selection it
// started from, but not at the selection it wrote last.
//
// What is still not vim: vim copies once more when Visual mode is LEFT through
// a command line, which is clip_auto_select() out of end_visual_mode(). Nothing
// here does that.
//
// It matters headlessly and not only on a desktop. Under "unnamed" the
// clipboard IS the register a bare p reads, so with the vimrc's
// clipboard=unnamed,unnamedplus,autoselect the selection this writes is what
// the next put pastes: "yy j V p" leaves the buffer alone, because V wrote
// "beta" over the "alpha" that yy had put there. Measured against vim; it
// reads like a bug in the config and it is the config that is being run.
func (e *Editor) autoSelect() {
	switch e.mode {
	case VisualChar, VisualLine, VisualBlock:
	default:
		return
	}
	if !hasFlag(e.opt.Clipboard, "autoselect") {
		return
	}
	// The three fields vim compares, named one at a time rather than by
	// comparing the whole struct, because visualSpan carries a curswant that
	// clip_update_selection() does not look at.
	start, end := e.selection()
	if e.clipSel.ok && e.clipSel.start == start && e.clipSel.end == end && e.clipSel.mode == e.mode {
		return
	}
	e.clipSel = visualSpan{start: start, end: end, mode: e.mode, ok: true}
	e.regs.SetSelection(operator.SpanValue(e.buf, e.visualSpanNow(), e.opt.Operator()))
}

// dispatch hands one key to whichever mode is running.
func (e *Editor) dispatch(k key.Key) error {
	switch e.mode {
	case Insert, Replace:
		return e.insertKey(k)
	case Cmdline:
		return e.cmdlineKey(k)
	}

	err := e.normalKey(k)
	// CTRL-O left insert mode for exactly one normal-mode command. This is
	// where it comes back, and the conditions are what "one command" means:
	// the command finished, it did not start a mode of its own, and it is not
	// still waiting for a motion or a character.
	if e.ins.oneShot && err == nil && e.mode == Normal &&
		e.wait == nil && e.pend.op == operator.OpNone && len(e.cmdKeys) == 0 {
		e.resumeInsert()
	}
	return err
}

// redraw is what vim's main loop does between one command and the next, and
// the two halves of it are both visible in the redirect cmd/oracle diffs.
//
// showmode() runs when the screen moved under the mode message: the mode
// changed, the command said something, or lines came or went. In normal mode
// with nothing recording it writes no bytes, so the call is free everywhere
// except where it matters, which is that "recording @a" reappears after every
// such command and is a third of what the macro cases diff on. Then keep_msg
// is printed again and forgotten, which is the second "3 fewer lines".
//
// Neither happens while a DOT REPEAT is replaying, which is the one that rides
// vim's stuff buffer: do_pending_operator stuffs the redo record into it and
// main_loop's whole block is guarded by stuff_empty(). A macro replay is not
// the stuff buffer -- do_execreg pushes the register into the typeahead, where
// stuff_empty() is still true -- so keep_msg is redisplayed after every
// command inside an @x, which is one line of the redirect per iteration. What
// a macro does suppress is showmode, through skip_showmode()'s char_avail()
// test, and Editor.atMacro already handles that inside showMode.
func (e *Editor) redraw(mode string, lines, said int) {
	if (e.replaying && !e.atMacro) || !e.commandDone() {
		return
	}
	// The mode is compared as the message showmode would print and not as the
	// Mode value, because operator-pending is not a mode the message line has
	// ever heard of: d then w is one command with one redraw at the end of it,
	// and comparing Mode values makes the w look like a change from
	// operator-pending back to normal and prints "recording @a" for it.
	if e.modeMessage() != mode || e.buf.LineCount() != lines || e.msg.writes != said {
		e.showMode()
	}
	e.msg.redisplay()
}

// commandDone reports whether the editor is between commands rather than in
// the middle of one. Insert mode is inside a command as far as the main loop is
// concerned -- vim's edit() runs under normal_cmd() and does its own
// redrawing -- and so is a half-typed operator and anything waiting on the
// character after it.
func (e *Editor) commandDone() bool {
	switch e.mode {
	case Insert, Replace, Cmdline, OperatorPending:
		return false
	}
	// CTRL-O is between the two halves of one insert: vim's edit() has already
	// said "-- (insert) --" on its way out and the main loop does not say it
	// again. A visual mode started under it is a mode of its own and the main
	// loop does say that: i CTRL-O v l l y leaves "-- (insert) VISUAL --" in
	// the redirect between the two.
	if e.ins.oneShot && e.mode == Normal {
		return false
	}
	return e.wait == nil && e.pend.op == operator.OpNone
}

// Keys feeds a whole script, stopping at the first error. It is what a macro
// replay, a dot repeat and cmd/pvim's --oracle loop all want, and having it
// here means none of them writes the loop again.
func (e *Editor) Keys(keys []key.Key) error {
	for _, k := range keys {
		if err := e.Key(k); err != nil {
			return err
		}
	}
	return nil
}

// Buffer is the text being edited. It is handed out rather than copied because
// internal/screen has to read it every frame and the undo tree lives on it; a
// caller that writes to it behind the editor's back gets the cursor and the
// undo history it deserves.
func (e *Editor) Buffer() *text.Buffer { return e.buf }

// Cursor is where the cursor is.
func (e *Editor) Cursor() text.Pos { return e.cur }

// SetCursor moves the cursor, clamped into the buffer. It is for a frontend
// that placed the cursor with a mouse and for a test; a key never calls it.
func (e *Editor) SetCursor(p text.Pos) { e.cur = e.buf.Clamp(p) }

// Mode is which mode the editor is in.
func (e *Editor) Mode() Mode { return e.mode }

// Registers is the register file, which the frontend needs so it can install a
// clipboard behind "* and "+.
func (e *Editor) Registers() *register.File { return e.regs }

// Options is the current settings.
func (e *Editor) Options() Options { return e.opt }

// SetOptions replaces the settings. internal/options' :set and the vimrc
// loader are the callers; it pushes the parts each package cares about down to
// it, so that nothing below has to be told twice.
func (e *Editor) SetOptions(o Options) {
	e.opt = o
	e.regs.SetOptions(o.Register())
	e.buf.SetTextWidth(o.TextWidth)
}

// SetScriptInput says the keys are arriving from a script rather than from a
// person, which is what cmd/pvim's --oracle mode does.
//
// It changes one thing: undo steps are not closed between commands, because
// vim's may_sync_undo() does nothing while it is reading a "-s" file. Three x
// commands from a script are one undo step in vim and one u takes all three
// back; three x commands from a keyboard are three. Without this the editor is
// right and every undo case in the oracle is a false diff.
func (e *Editor) SetScriptInput(on bool) { e.scriptInput = on }

// ScriptInput reports whether the keys are arriving from a script. The ex
// layer asks because the same rule applies to a colon command: two ":d" from a
// "-s" file are one undo step, and one "u" takes both back.
func (e *Editor) ScriptInput() bool { return e.scriptInput }

// Sync closes the undo step that is open, if there is one.
//
// A frontend calls it when it wants the buffer's committed state rather than
// the one a half-finished command is building: cmd/pvim's --oracle mode does,
// before it dumps undotree().seq_cur, because vim gives a change its sequence
// number as soon as it is saved and internal/text gives it one when the block
// closes. Without this call a scripted run reports sequence 0 for a buffer it
// has changed.
func (e *Editor) Sync() { e.syncUndo() }

// Search is the last search: the pattern, the direction and the offset that n
// repeats.
func (e *Editor) Search() search.State { return e.search }

// SetSearchPattern makes p the pattern n repeats and the contents of "/.
//
// It is how ":s" and ":g" publish the pattern they used, which vim does too:
// ":s/foo/bar/" leaves "/ holding "foo" and a following "n" searches for it.
// The two are set together because in vim they are one thing, and setting only
// the register would give an editor whose @/ and whose n disagree.
//
// The direction is left alone on purpose. A ":s" does not turn a backwards
// search forwards, measured with "?a" then ":s/b/c/" then "n".
func (e *Editor) SetSearchPattern(p string) {
	if p == "" {
		return
	}
	e.search.Pattern = p
	e.regs.SetLastSearch(p)
}

// Window tells the editor which buffer lines are on screen, which is the only
// thing H, M and L need and the only thing the mode machine cannot work out
// for itself. A frontend calls it on every scroll and resize; a headless run
// never does, and H, M and L fail there, which is correct.
func (e *Editor) SetWindow(w motion.Window) {
	// A scroll that happens while an insert is running is remembered, because
	// endInsert asks whether the screen moved and not where it ended up. See
	// insState.scrolled.
	if (e.mode == Insert || e.mode == Replace) && w.Top != e.ins.top {
		e.ins.scrolled = true
	}
	e.mctx.Window = w
}

// Messages is everything the message line has said, oldest first. A frontend
// shows the last one; the oracle diffs Redirect instead, which is the stream
// and not the list.
func (e *Editor) Messages() []string { return e.msg.log }

// Message is the last thing said, or empty.
func (e *Editor) Message() string {
	if len(e.msg.log) == 0 {
		return ""
	}
	return e.msg.log[len(e.msg.log)-1]
}

// ExecNormal runs keys the way ":normal" does: from a stuffed buffer rather
// than from a keyboard.
//
// The difference that shows is the mode message. vim's showmode() starts with
// skip_showmode(), which is true while char_avail() is -- and inside
// ":%normal A!" the whole argument is in the typeahead, so every one of those
// inserts happens without "-- INSERT --" ever reaching the message line. The
// same flag that keeps a macro quiet keeps this quiet, which is what it is
// doing here.
func (e *Editor) ExecNormal(keys []key.Key) error {
	was := e.atMacro
	e.atMacro = true
	defer func() { e.atMacro = was }()
	return e.Keys(keys)
}

// Waiting reports whether the editor is holding the next keystroke for a
// command that has already started: the register name after a quote, the
// target of an f, the name of a mark. The frontend asks because the keys it
// takes for itself -- z, CTRL-W, the scrolling family -- are not those keys,
// and "\"z3g~j" was intercepting the z of the register name as the start of a
// z command.
func (e *Editor) Waiting() bool { return e.wait != nil }

// SetRemap installs the frontend's map layer, which is the one thing a macro
// replay needs from above this package.
//
// A mapping is not the mode machine's business and must not become it: the
// table and the resolver live beside the editor, and every typed key arrives
// here already mapped. A macro is the exception. vim's do_execreg() pushes the
// register into the TYPEAHEAD, so the keys it holds are mapped again on every
// replay, where a dot repeat rides the stuff buffer and is not. Without this
// hook the register's keys go straight into Key, below the map layer, and the
// two editors part company the moment a mapping is changed between the
// recording and the replay: with "nnoremap, A!<Esc>" recorded as "qq,q", the
// register holds a comma in both, and a ":nunmap," before the "@q" makes vim
// replay a comma that is no longer a command.
//
// run is handed one key and is expected to do with it whatever the frontend
// does with a keystroke: push it into the resolver and drain whatever comes
// out, which will normally end up back in Key. Re-entrancy is the caller's to
// keep straight; nil means there is no map layer, which is what a test and the
// headless run have.
func (e *Editor) SetRemap(run func(key.Key) error) { e.remap = run }

// MarkJump records the cursor as the position a jump is leaving: it moves the
// ' mark and pushes the jumplist, which is the whole of vim's setpcmark().
//
// It is exported for the ex layer, which owns three of the commands :help
// jump-motions calls jumps and which this package cannot see. ":s" calls it
// once before the first substitution; ":g" calls it once for the whole global
// and the ":s" inside it must not call it again, which is vim's global_busy
// guard and the comment above global_exe() in the C ("Set current position
// only once for a global command"). Measured on ten lines: "5G:8s/eight/E/"
// then CTRL-O lands on line 5, and so does the same with the substitute inside
// a ":8,9g/e/".
func (e *Editor) MarkJump() { e.setPCMark(e.cur) }

// Aborted reports whether the command that just ran ended in a beep.
//
// It is vim's "aborted" flag, and the map layer is what wants it. Vim throws
// the rest of a mapping's right-hand side away when a command in it fails, and
// a failure is a beep as often as it is a message: ":nnoremap gx /nosuch<CR>x"
// on a buffer with no match beeps at the search and never runs the x. The map
// layer already flushes on an error message; without a way to ask about the
// beep it runs on where vim stops.
//
// Ask it straight after the key. The flag is set by beep() and cleared when
// the next normal-mode command starts, which is where this package's own
// reader of it looks: playKeys checks it after every key, and that is how a
// recorded search that finds nothing ends an "@a". A caller that lets a few
// keys go by before asking is reading a flag about one of them and cannot
// tell which.
func (e *Editor) Aborted() bool { return e.aborted }

// Beep is vim's clearopbeep(): the half-typed command is thrown away, the bell
// is rung, and nothing is said. It is exported because the frontend owns the
// keys that need a window -- the z family and CTRL-W -- and vim beeps at the
// ones that are not commands, which the frontend has to be able to do without
// reaching into this package.
func (e *Editor) Beep() error { return e.beep() }

// Say puts a line on the message line. It is exported because the ex layer and
// the frontends have things to say too, and because a package-level logger
// would not be diffable.
func (e *Editor) Say(msg string) { e.msg.say(msg) }

// SayKeep is Say for a message the redraw after the command puts back once
// more: vim's set_keep_msg, which msgmore() and the shift report call and
// which is why ":2d 3" leaves "3 fewer lines" twice in the redirect and
// ":1,3y" leaves "3 lines yanked" once.
func (e *Editor) SayKeep(msg string) { e.msg.sayKeep(msg) }

// SayRaw writes text into the message stream exactly as given, with no
// newline of its own in front of it.
//
// It exists for ":!", which is the one command whose message is not a line:
// vim echoes ":!cmd" followed by a carriage return where the command line was,
// and prints "shell returned N" wrapped in newlines of its own, so the shape
// of that whole exchange belongs to the caller and not to msg(). Measured:
// ":!false" leaves "\n:!false\r\n\nshell returned 1\n" in the redirect.
func (e *Editor) SayRaw(text string) { e.msg.raw(text) }

// SetFileName tells the editor what file the buffer came from. Two things read
// it: "[I" prints the name above the lines it lists, and every jumplist entry
// carries it.
//
// The jumplist entries made before the name arrived are filled in here. New()
// seeds one -- the position the buffer was opened at, which is vim's do_ecmd
// setpcmark() -- and a frontend that reads the file first and names it second
// would otherwise persist that entry with no file on it. Only the blank ones
// are touched: an entry restored from another file keeps the name it came
// with.
func (e *Editor) SetFileName(name string) {
	e.name = name
	for i := range e.jumps {
		if e.jumps[i].File == "" {
			e.jumps[i].File = name
		}
	}
}

// SayPrompt puts a question on the message line: an ordinary message, plus
// vim's msg_scroll, which stops the command behind the prompt from keeping its
// report across the redraw.
func (e *Editor) SayPrompt(msg string) { e.msg.prompt(msg) }

// SayAnswer echoes the key that answered a prompt onto the end of the prompt
// line, with no newline. The redirect holds "(y/n)?y".
func (e *Editor) SayAnswer(c byte) { e.msg.answer(c) }

// NewMessageLine is the bare newline an accepted ex command line leaves
// behind: the cursor was on the command line, the command is about to write
// where the command line was, and vim moves off it first. It is one byte of
// every ":" case the oracle runs and it belongs to the prompt rather than to
// anything the command said, which is why the frontend that owns the prompt
// calls it and internal/ex does not.
func (e *Editor) NewMessageLine() { e.msg.empty() }

// Redisplay is the "display message after redraw" half of vim's main loop,
// exported for the ex layer's sake: a colon command runs outside Key, so
// nothing else would put the kept message back.
func (e *Editor) Redisplay() { e.msg.redisplay() }

// Showcmd is the half-typed command, as 'showcmd' renders it in the bottom
// right: the count, the register prefix and the operator typed so far.
func (e *Editor) Showcmd() string {
	var out []byte
	if e.pend.count1 > 0 {
		out = strconv.AppendInt(out, int64(e.pend.count1), 10)
	}
	if e.pend.reg != 0 {
		out = append(out, '"', e.pend.reg)
	}
	out = append(out, e.pend.opKeys...)
	if e.pend.count2 > 0 {
		out = strconv.AppendInt(out, int64(e.pend.count2), 10)
	}
	return string(out) + cmdFormat(e.pend.keys)
}

// Recording is the register q is recording into, or zero. The statusline shows
// "recording @a" from it.
func (e *Editor) Recording() byte { return e.rec.reg }
