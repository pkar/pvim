package mode

import (
	"bytes"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/operator"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// insCmd is what started an insert: which key, how many times it is to happen,
// and whether the undo block is already open because an operator opened it.
type insCmd struct {
	// cmd is the key that started the insert. The two-key ones are the sum of
	// their bytes, which is unique among them and never leaves this file, and
	// 'r' is the insert that r<CR> runs: vim stuffs the newline and an Escape
	// into edit() rather than looping in it, so that insert says the mode once
	// where an "o" typed by hand says it twice.
	cmd byte
	// count is the count typed before the command. 3ax<Esc> types the x once
	// and repeats the whole inserted text twice more on the way out.
	count int
	// replace is R rather than i.
	replace bool
	// keepUndo says an undo block is already open, which is the c case: the
	// delete and the insert that follows it are one press of u.
	keepUndo bool
	// block, when set, is the blockwise I, A or c: the text typed goes into
	// every line of the block when the insert ends.
	block *blockInsert
}

// blockInsert is the geometry a blockwise I or A repeats down.
type blockInsert struct {
	first, last int
	// col is the display column the text goes in at.
	col int
	// append says the text goes after the block's right edge, which is A, and
	// that short lines are padded with spaces to reach it. I skips them
	// instead, which is the difference measured on a three-line block over a
	// one-character line.
	append bool
	// home is the block's left display column, where vim leaves the cursor
	// when the insert ends however far right the typing happened. Measured:
	// CTRL-V jj A X Escape on a block starting in column 1 leaves the cursor
	// in column 1 and not next to the X. It is -1 for a block that came from
	// a change rather than from I or A, where the cursor stays where the
	// typing left it.
	home int
	// toEOL is the $ block: every line takes the text at its own end.
	toEOL bool
}

// overwritten is one character replace mode covered up, and the whole reason
// replace mode's backspace is a stack rather than a delete.
type overwritten struct {
	// text is the bytes that were there, empty when the character was typed
	// past the end of the line and so covered nothing.
	text []byte
	// had says there was a character here. Without it an overwritten empty
	// string and an appended character look the same, and backspacing over the
	// end of the original line puts a byte back that was never there.
	had bool
}

// insState is one insert or replace session.
type insState struct {
	cmd insCmd
	// start is where the insert began, as vim's Insstart: it moves up with the
	// text when a backspace in column zero joins two lines.
	start text.Pos
	// startOrig is the same position as vim's Insstart_orig, which is the one
	// CTRL-W and CTRL-U stop at and the one 'backspace' without "start" in it
	// will not let a backspace cross. It is NOT moved by the join, and that is
	// the whole of the difference between the two: after a CTRL-U in column
	// zero has joined the line onto the one above, the cursor is on a line
	// Insstart_orig is not on, vim's stop never fires, and the next CTRL-U
	// takes the whole line. Measured on a tab-indented line above a line of
	// Japanese: "12e 2I CTRL-U K" leaves "K日本語..." in vim, where a stop that
	// moved with the join leaves the first line's text in front of it.
	startOrig text.Pos
	// text is what has been typed this session, which is what ". holds, what
	// CTRL-A inserts and what a count repeats.
	text []byte
	// ai is the number of bytes of autoindent put on the current line that
	// will be taken away again if nothing is typed on top of them. It is -1
	// when there is no such indent pending.
	ai int
	// over is replace mode's stack.
	over []overwritten
	// needUndo says the undo block for this insert has not been opened yet,
	// because nothing has changed yet. See enterInsert.
	needUndo bool
	// lines is how many lines the buffer had when the insert started, which
	// endInsert compares against to decide whether to say the mode message one
	// more time on the way out.
	lines int
	// opened is how many lines the buffer had once the insert command's own
	// opening was done, which for o and O is one more than it started with.
	// endInsert compares against it to answer "did the typing itself change
	// the line count", which is half of the 'conceallevel' rule on the mode
	// message. See endInsert.
	opened int
	// top is the window's first displayed line when the insert started. The
	// same message asks about it: an insert that pushed the cursor off the
	// bottom of the window scrolled the whole screen, and vim says the mode
	// again for that even when nothing else about the insert asked it to.
	// Zero when nothing has told the editor about a window, which is every
	// unit test in this package and is why a zero compares equal to itself
	// and changes nothing.
	top int
	// scrolled says the window moved at some point during the insert, which
	// is not the same question as whether it is somewhere else now: on a
	// 39-line file in a 39-row window, "Gi<CR><BS>" pushes the window down a
	// row and then pulls it back, and vim says the mode message twice for the
	// row that moved. Comparing the top at the end sees nothing at all. Set by
	// SetWindow, which the frontend calls after every keystroke.
	scrolled bool
	// run is where the current run of typed characters started, and inRun
	// says there is one. vim reports a whole run of typed characters to the
	// changelist and the '. mark at its FIRST character: measured, "5|axy" on
	// "hello" leaves `. in column 6 and not 7, and a backspace, a CTRL-W, a
	// CTRL-O motion or an Enter in the middle of the typing starts the count
	// again from wherever the next character lands. Replace mode does not do
	// this and reports every character, which is measured too.
	run   text.Pos
	inRun bool
	// noStop says a CTRL-U joined this line onto the one above it, which
	// leaves nothing for a later CTRL-W or CTRL-U on the line to stop at. A
	// backspace or a CTRL-W doing the same join leaves the stop at the join
	// column. See insertStop.
	noStop bool
	// quiet says the last thing the insert did was put an ordinary character
	// in, which is the one thing that stops endInsert saying the mode message
	// a second time. It is not inRun: replace mode deliberately never starts a
	// run, and a CTRL-U is quiet whatever it deleted. Measured against vim for
	// every insert command against every burst ending in CR, Tab, BS, CTRL-W
	// and CTRL-U.
	quiet bool
	// trimmed says the last thing the insert did was take back text it had
	// typed. vim's redo buffer holds the keystrokes and not the text they left
	// behind, so a count replays the backspace too and the changelist ends up
	// where the replayed backspace deleted rather than where the replayed
	// characters started. See repeatInsert.
	trimmed bool
	// keys is every keystroke this insert session has been given, which is
	// what the count on the command replays. vim's start_redo_ins() stuffs
	// the redo buffer back into edit(), and the redo buffer is keys and not
	// text: "3iab<BS>" leaves "aa" three times over and "2A<C-W>" deletes a
	// word twice. Replaying the text instead gets both wrong and is what this
	// replaced.
	keys []key.Key
	// replaying says repeatInsert is feeding keys back in, which stops the
	// replay from being recorded as more typing and stops it from growing
	// what ". will hold.
	replaying bool
	// oneShot is CTRL-O: one normal-mode command and then straight back here.
	oneShot bool
	// openedTop says O opened the buffer's first line, which is the one shape
	// that never says the mode message a second time on the way out. See
	// endInsert.
	openedTop bool
	// dotCount and dotReg are the count and register of the command that
	// started the insert, kept because the insert is only half of the change
	// that . repeats.
	dotCount int
	dotReg   byte
}

// enterInsert starts an insert or replace session at the place the command
// says.
func (e *Editor) enterInsert(c insCmd) error {
	// Where the undo block remembers the cursor is where "u" puts it back, and
	// vim's u_save runs at different moments for different insert commands. o
	// and O save before they open the line, with the cursor still on the line
	// the command was typed on: u after "oNEW" leaves the cursor there.
	// Everything else saves at the first character typed, by which time i, a,
	// A, I and gI have already moved: u after "AX" leaves the cursor at the end
	// of the line and not in column 0. Both measured.
	if !c.keepUndo && (c.cmd == 'o' || c.cmd == 'O') {
		e.openUndo(e.cur)
		e.rememberLine()
	}
	// The line count is the one from before the whole command and not from
	// here: "2C" has already deleted a line by the time this runs, and the
	// mode message endInsert decides on compares against what was on the
	// screen when the command was typed.
	before := e.keyLines
	if before == 0 {
		before = e.buf.LineCount()
	}
	e.ins = insState{cmd: c, ai: -1, dotCount: e.pend.count(), dotReg: e.pend.reg,
		lines: before, top: e.mctx.Window.Top}
	// o and O saved undo above, before they opened the line. Everything else
	// waits: vim's stop_arrow() calls u_save_cursor() at the first character
	// typed, so "a" followed by an Escape numbers no undo header at all and
	// undotree().seq_cur is still 0 over a buffer nothing touched.
	e.ins.needUndo = !c.keepUndo && c.cmd != 'o' && c.cmd != 'O'
	line := e.buf.Line(e.cur.Line)

	switch c.cmd {
	case 'i', 'c', 'r':
	case 'I':
		e.cur.Col = firstNonBlank(line)
	case 'g' + 'I':
		e.cur.Col = 0
	case 'g' + 'i':
		if p, ok := e.buf.Mark(text.MarkLastInsert); ok {
			e.cur = e.buf.Clamp(p)
		}
	case 'a':
		if len(line) > 0 && e.cur.Col < len(line) {
			e.cur.Col = nextRune(line, e.cur.Col)
		}
	case 'A':
		e.cur.Col = len(line)
	case 'o', 'O':
		at := e.cur.Line + 1
		if c.cmd == 'O' {
			at = e.cur.Line
		}
		indent := autoIndent(line, len(line), e.opt)
		e.buf.InsertLines(at, [][]byte{indent})
		e.ins.openedTop = at == 1
		e.cur = text.Pos{Line: at, Col: len(indent)}
		// U works on the line the insert is ON, which for o and O is the one
		// just opened and not the one the command was typed on: vim's
		// u_saveline runs with the cursor already moved, so it saves the new
		// empty line. Measured, "oX<Esc>U" leaves an empty line 2 where
		// remembering the old line left the X in place. uheld is cleared
		// because O opens AT the cursor's own line number and rememberLine
		// would otherwise think it already had that line.
		e.uheld = false
		e.rememberLine()
		if len(indent) > 0 {
			e.ins.ai = len(indent)
		}
	}

	e.ins.start, e.ins.startOrig = e.cur, e.cur
	e.ins.opened = e.buf.LineCount()
	e.mode = Insert
	if c.replace {
		e.mode = Replace
	}
	// vim's edit() says what mode it is in before it reads a key, which is why
	// the mode message lands here and not at the end of the command that
	// started the insert.
	e.showMode()
	e.pend = pending{}
	return nil
}

// The mode messages vim puts on the message line, which cmd/oracle diffs
// against what :redir caught. They are not decoration: a case whose buffer and
// registers match and whose msgs.txt is empty still fails, and fifty-three of
// the two hundred and fifty cases say one of these and nothing else.
const (
	insertMessage  = "-- INSERT --"
	replaceMessage = "-- REPLACE --"
	// oneShotMessage is what CTRL-O shows while the one normal-mode command
	// runs. Measured: i CTRL-O dw X leaves "-- INSERT ---- (insert) ---- INSERT --"
	// in the redirect, so all three are said and the middle one is this.
	oneShotMessage = "-- (insert) --"
)

// insertKey feeds one key to insert or replace mode.
func (e *Editor) insertKey(k key.Key) error {
	if !e.ins.replaying && (e.wait != nil || (k != keyEsc && k != key.Ctrl('c'))) {
		// Recorded before the wait dispatch, because the character after a
		// CTRL-R or a CTRL-V belongs to the replay as much as the CTRL-R does,
		// and that is also why the two keys that END the insert are only
		// skipped when nothing is waiting for them. Escape is not in vim's
		// redo buffer either: start_redo_ins() copies what was typed and the
		// caller supplies the end.
		e.ins.keys = append(e.ins.keys, k)
	}
	if e.wait != nil {
		f := e.wait
		e.wait = nil
		return f(k)
	}
	if !e.ins.replaying {
		// The replay is not typing: what "." repeats is the keys the person
		// pressed and the count that was in front of them, and putting the
		// replayed copies in as well would make every dot repeat longer than
		// the change it repeats.
		e.cmdKeys = append(e.cmdKeys, k)
	}

	// Any key that is not CTRL-N or CTRL-P ends the completion first, and vim
	// says so before it does anything with the key: CTRL-N then X leaves
	// "The only match" a second time and then "-- INSERT --" in the redirect,
	// ahead of the X.
	if e.pendingCtrlX {
		e.pendingCtrlX = false
		if k == key.Ctrl('o') {
			return e.Omni()
		}
		return e.beep()
	}
	if e.comp.active && k != key.Ctrl('n') && k != key.Ctrl('p') {
		e.endCompletion()
	}

	switch {
	case k == keyEsc:
		return e.endInsert()
	case k == keyCR || k == keyNL || k == key.Ctrl('m') || k == key.Ctrl('j'):
		return e.insertNewline()
	case k == keyBS || k == key.Ctrl('h'):
		return e.insertBackspace()
	case k == keyDel:
		return e.insertDelete()
	case k == key.Ctrl('w'):
		return e.deleteWordBack()
	case k == key.Ctrl('u'):
		return e.deleteLineBack()
	case k == key.Ctrl('t'):
		return e.shiftLine(1)
	case k == key.Ctrl('d'):
		return e.shiftLine(-1)
	case k == key.Ctrl('a'):
		return e.insertLastInsert()
	case k == key.Ctrl('e'):
		return e.insertFromLine(1)
	case k == key.Ctrl('y'):
		return e.insertFromLine(-1)
	case k == key.Ctrl('r'):
		e.wait = e.insertRegister
		return nil
	case k == key.Ctrl('o'):
		return e.insertOneShot()
	case k == key.Ctrl('v') || k == key.Ctrl('q'):
		e.wait = e.literalFirst
		return nil
	case k == key.Ctrl('c'):
		// CTRL-C leaves insert mode without replaying the count, which is the
		// whole of what it is for.
		e.ins.cmd.count = 1
		return e.endInsert()
	case k == key.Ctrl('x'):
		// CTRL-X is a submode: vim reads a second key deciding WHICH
		// completion. Only CTRL-O is implemented, because it is the only one
		// this config asks for -- the vimrc maps "." in a Go buffer to
		// ".<C-x><C-o>" -- and every other second key falls through to the
		// beep vim gives an unknown one.
		e.pendingCtrlX = true
		return nil
	case k == key.Ctrl('n'):
		return e.complete(true)
	case k == key.Ctrl('p'):
		return e.complete(false)
	case k == keyTab:
		return e.insertTab()
	case k.Special != key.KeyNone:
		return e.insertSpecial(k)
	case k.Mod&^key.ModShift != 0:
		return ErrNotImplemented
	}
	e.insertTyped(k)
	return nil
}

// insertTyped puts one typed character in, with the one rule about which
// characters the changelist counts as part of a run.
//
// A run of typed characters is reported at the column the run started in, and
// three characters are not part of one: a Tab, a "0" and a "^". Each reports
// its own column and starts the count again, so "ia\tb" lands the change on
// column 2 and "iab" lands it on column 0, and "o0a" on column 1 where "oxa"
// is on column 0. Measured over the digits, the letters and twenty punctuation
// marks; those three and nothing else. They are also the three characters
// vim's edit() looks at before inserting anything, Tab through ins_tab() and
// the other two remembered for the "0 CTRL-D" that would delete them again,
// which is where the accident comes from.
func (e *Editor) insertTyped(k key.Key) {
	special := k.IsRune() && k.Rune < 0x80 && runBreaker(byte(k.Rune))
	if special {
		e.breakRun()
	}
	e.insertBytes(runeBytes(k))
	if special {
		e.breakRun()
	}
}

// insertSpecial handles the named keys that are not text: the arrows move the
// cursor and, as in vim, break the insert so that backspace and the repeat
// count start again from where the cursor now is.
func (e *Editor) insertSpecial(k key.Key) error {
	line := e.buf.Line(e.cur.Line)
	switch k.Special {
	case key.KeyLeft:
		if e.cur.Col > 0 {
			e.cur.Col = prevRune(line, e.cur.Col)
		}
	case key.KeyRight:
		if e.cur.Col < len(line) {
			e.cur.Col = nextRune(line, e.cur.Col)
		}
	case key.KeyUp:
		if e.cur.Line > 1 {
			e.cur.Line--
		}
	case key.KeyDown:
		if e.cur.Line < e.buf.LineCount() {
			e.cur.Line++
		}
	case key.KeyHome:
		e.cur.Col = 0
	case key.KeyEnd:
		e.cur.Col = len(line)
	case key.KeyF2, key.KeyF3, key.KeyF4, key.KeyF5, key.KeyF6, key.KeyF7,
		key.KeyF8, key.KeyF9, key.KeyF10, key.KeyF11, key.KeyF12:
		// A function key insert mode has nothing to do with is inserted as
		// its own name: vim's insert_special() puts "<F4>" in the buffer, four
		// characters, and the cursor ends on the ">". Measured for F2, F3, F4
		// through their SS3 spellings and F5 through "\x1b[15~". F1 is not
		// here because vim maps it to :help and opens a window instead, which
		// is a command this editor has not written and refuses loudly.
		// In two pieces, because vim inserts it in two: insert_special()
		// calls ins_str() for everything up to the last character and
		// ins_char() for the ">" itself. That is what the changelist reports,
		// and it reports the second call: "i<F4><Esc>" leaves the entry on
		// column 3 of "<F4>alpha" and not on column 0, where a single run
		// would put it.
		name := k.String()
		e.breakRun()
		e.insertBytes([]byte(name[:len(name)-1]))
		e.breakRun()
		e.insertBytes([]byte(name[len(name)-1:]))
		return nil
	default:
		return ErrNotImplemented
	}
	e.cur = e.clampInsert(e.cur)
	e.ins.start, e.ins.startOrig = e.cur, e.cur
	e.ins.ai = -1
	e.breakRun()
	e.ins.text = nil
	return nil
}

// insertBytes puts text in at the cursor, overwriting rather than inserting
// when this is replace mode.
func (e *Editor) insertBytes(b []byte) {
	if len(b) == 0 {
		return
	}
	e.ins.ai = -1
	e.comp = completion{}
	if e.mode == Replace {
		e.replaceBytes(b)
		return
	}
	e.insertRun(b)
	if !e.ins.replaying {
		// ". and CTRL-A hold one copy of what was typed, not one per repeat:
		// vim's redo buffer is written while the keys come in and read while
		// they are replayed, so the replay adds nothing to it.
		e.ins.text = append(e.ins.text, b...)
	}
}

// insertNewline is Enter: split the line, carry the indent when 'autoindent'
// is on, and take back an indent that was put on the line just left and never
// typed over.
func (e *Editor) insertNewline() error {
	e.insUndo()
	e.breakRun()
	e.dropPendingIndent()
	line := e.buf.Line(e.cur.Line)
	indent := autoIndent(line, e.cur.Col, e.opt)
	// Replace mode's stack gets an entry for the line break too, and it is the
	// "covered nothing" kind, because a CR in replace mode inserts a break
	// rather than replacing anything. Without it the backspace that takes the
	// break back pops the entry belonging to the character before it and the
	// next backspace has nothing to put back: measured, "Ra<CR><BS><BS>" puts
	// the a back to what it covered and pvim used to leave the a there.
	if e.mode == Replace {
		e.ins.over = append(e.ins.over, overwritten{})
	}
	e.cur = e.buf.Insert(e.cur, append([]byte{'\n'}, indent...))
	e.ins.trimmed = false
	if !e.ins.replaying {
		e.ins.text = append(e.ins.text, '\n')
		e.ins.text = append(e.ins.text, indent...)
	}
	e.ins.ai = -1
	if len(indent) > 0 {
		e.ins.ai = len(indent)
	}
	e.comp = completion{}
	return nil
}

// dropPendingIndent takes back an autoindent nothing was typed on top of,
// which is what leaves a blank line blank rather than full of spaces.
func (e *Editor) dropPendingIndent() {
	if e.ins.ai <= 0 {
		return
	}
	e.insUndo()
	line := e.buf.Line(e.cur.Line)
	if e.cur.Col == e.ins.ai && len(line) == e.ins.ai {
		e.buf.SetLine(e.cur.Line, nil)
		e.cur.Col = 0
	}
	e.ins.ai = -1
}

// canBackspaceOver reports whether 'backspace' allows crossing one of its
// three boundaries: "indent", "eol" and "start".
func (e *Editor) canBackspaceOver(what string) bool {
	return hasFlag(e.opt.Backspace, what) || e.opt.Backspace == "2"
}

// insertBackspace is BS and CTRL-H.
func (e *Editor) insertBackspace() error {
	// vim's ins_bs() returns before u_save() at the very start of the buffer,
	// so a backspace there leaves undotree().seq_cur at 0 where one anywhere
	// else leaves it at 1 over a buffer it did not change.
	if e.cur.Line == 1 && e.cur.Col == 0 {
		e.breakRun()
		return e.unrecordKey()
	}
	e.insUndo()
	e.breakRun()
	if e.mode == Replace {
		return e.replaceBackspace()
	}
	if e.cur.Col == 0 {
		return e.joinBack(false)
	}
	if !e.beforeInsertStart() && !e.canBackspaceOver("start") {
		return e.unrecordKey()
	}
	line := e.buf.Line(e.cur.Line)
	col := prevRune(line, e.cur.Col)
	if e.ins.ai > 0 && !e.canBackspaceOver("indent") && col < e.ins.ai {
		return e.unrecordKey()
	}
	n := e.cur.Col - col
	e.cur = e.buf.Delete(text.Range{
		Start: text.Pos{Line: e.cur.Line, Col: col},
		End:   text.Pos{Line: e.cur.Line, Col: e.cur.Col},
	})
	if e.ins.ai > 0 {
		e.ins.ai -= n
		if e.ins.ai < 0 {
			e.ins.ai = 0
		}
	}
	e.trimTyped(n)
	return nil
}

// joinBack is the line break in front of the cursor deleted, which is what
// every insert-mode backspace does in column zero.
//
// vim's ins_bs() asks about the column before it asks which backspace this is:
// the whole of BS, CTRL-W and CTRL-U goes through one "if (col == 0) { join;
// return }" and only the else branch tells them apart. That is why CTRL-U at
// the start of a line joins it onto the one above rather than doing nothing.
// 'backspace' has to contain "eol" or none of them cross, which is the one
// guard here.
func (e *Editor) joinBack(fromLine bool) error {
	if e.cur.Line == 1 || !e.canBackspaceOver("eol") {
		return e.unrecordKey()
	}
	gone := e.cur.Line
	prev := e.buf.Line(e.cur.Line - 1)
	// The marks and the changelist entries on the line about to disappear
	// move along the line it is folded into, exactly as they do under "gJ":
	// vim's ins_bs() joins through do_join() and gets mark_col_adjust() with
	// it. Measured, "2Go CTRL-U" moves the changelist entry the o left at
	// {3,0} onto {2,3} rather than leaving it behind or dropping it.
	e.buf.JoinMarks(gone, -1, len(prev), 0, 0)
	e.cur = e.buf.Delete(text.Range{
		Start: text.Pos{Line: e.cur.Line - 1, Col: len(prev)},
		End:   text.Pos{Line: e.cur.Line},
	})
	// The insert moves up with the text. vim's ins_bs does the same two
	// assignments -- --Insstart.lnum and Insstart.col = the length of the line
	// joined onto -- and without them a later CTRL-W measures against a line
	// number that no longer exists and eats the whole line. Measured on
	// "2GO<BS>l<C-W>", which leaves "alpha" alone in vim.
	if e.ins.start.Line == gone {
		e.ins.start = text.Pos{Line: gone - 1, Col: len(prev)}
	}
	// A CTRL-U doing the join is the one that does not leave the stop at the
	// join column. It hands it back to where the insert began when the insert
	// began on the line that is being joined onto, and leaves none at all when
	// it did not. See insertStop for the nine shapes that were measured.
	e.ins.noStop = false
	if fromLine {
		if e.ins.startOrig.Line == gone-1 {
			e.ins.start = e.ins.startOrig
		} else {
			e.ins.noStop = true
		}
	}
	// A backspace over a line break is a join, and a join reports the line
	// it removed rather than the one it joined onto: same rule as "J".
	e.buf.ChangedAt(text.Pos{Line: gone})
	e.trimTyped(1)
	return nil
}

// beforeInsertStart reports whether the cursor is still inside the text this
// insert session typed, which is the boundary 'backspace' without "start" in
// it refuses to cross.
func (e *Editor) beforeInsertStart() bool {
	return e.cur.Line != e.ins.start.Line || e.cur.Col > e.ins.start.Col
}

// trimTyped takes n bytes back off the record of what was typed, so that ".
// and a repeat count do not replay text that was deleted again.
func (e *Editor) trimTyped(n int) {
	e.ins.trimmed = true
	if n >= len(e.ins.text) {
		e.ins.text = nil
		return
	}
	e.ins.text = e.ins.text[:len(e.ins.text)-n]
}

// insertDelete is the Del key: take the character under the cursor.
func (e *Editor) insertDelete() error {
	e.insUndo()
	e.breakRun()
	line := e.buf.Line(e.cur.Line)
	if e.cur.Col >= len(line) {
		return nil
	}
	e.buf.Delete(text.Range{
		Start: e.cur,
		End:   text.Pos{Line: e.cur.Line, Col: nextRune(line, e.cur.Col)},
	})
	return nil
}

// deleteWordBack is CTRL-W: the white space before the cursor and then the
// word before that, where a word is a run of keyword characters or a run of
// other non-blanks, which is why abc.def leaves abc. behind.
func (e *Editor) deleteWordBack() error {
	// vim's ins_bs() takes its "nothing before the first character in the
	// buffer" return before u_save, for CTRL-W as much as for BS, so a CTRL-W
	// there numbers no undo header: measured, "i CTRL-W" leaves
	// undotree().seq_cur at 0.
	if e.cur.Line == 1 && e.cur.Col == 0 {
		e.breakRun()
		return e.unrecordKey()
	}
	e.insUndo()
	e.breakRun()
	if e.cur.Col == 0 {
		return e.insertBackspace()
	}
	line := e.buf.Line(e.cur.Line)
	col := e.cur.Col
	for col > 0 && isSpaceByte(line[prevRune(line, col)]) {
		col = prevRune(line, col)
	}
	if col > 0 {
		kw := isKeywordByte(line[prevRune(line, col)], e.opt.IsKeyword)
		for col > 0 {
			p := prevRune(line, col)
			if isSpaceByte(line[p]) || isKeywordByte(line[p], e.opt.IsKeyword) != kw {
				break
			}
			col = p
		}
	}
	// CTRL-W stops once at the point the insert began, whatever 'backspace'
	// says. That is vim's loop condition -- it walks back while the cursor is
	// not exactly on Insstart -- and it is why "A zz" then two CTRL-W leaves
	// "alpha" and a third one takes that too. 'backspace' without "start" in
	// it turns the stop into a wall, which is the same clamp applied every
	// time rather than only when there is somewhere left to stop.
	if stop, ok := e.insertStop(); ok && col < stop &&
		(!e.canBackspaceOver("start") || e.cur.Col > stop) {
		col = stop
	}
	if col >= e.cur.Col {
		return e.unrecordKey()
	}
	n := e.cur.Col - col
	e.cur = e.buf.Delete(text.Range{
		Start: text.Pos{Line: e.cur.Line, Col: col},
		End:   text.Pos{Line: e.cur.Line, Col: e.cur.Col},
	})
	e.trimTyped(n)
	e.ins.ai = -1
	// No followInsertStart here, and it was tried. Under the vimrc profile on
	// "\tfmt.Println(count(os.Args[1], os.Args[2]))", "A CTRL-W Tab CTRL-W Tab"
	// leaves exactly what one "A CTRL-W Tab" leaves: the second CTRL-W takes
	// the white space the Tab put in and stops, which is a start that moved
	// back with the first CTRL-W. The same keys under the vanilla profile
	// carry on into the "2", which is a start that did not. 'backspace' is not
	// what tells them apart -- at ts=8 sts=0 both profiles carry on -- so what
	// moves it is something 'softtabstop' does, and the likeliest candidate is
	// the run of spaces ins_tab deletes when it squashes them back into tabs.
	return nil
}

// deleteLineBack is CTRL-U: everything typed this session on this line, or,
// when nothing was typed, everything before the cursor that 'backspace' allows.
//
// It ends the undo block, which vim does and CTRL-W does not: measured, u
// after Afoo bar CTRL-U X Escape puts "foo bar" back rather than undoing the
// whole insert. In column zero it is a join, because vim's ins_bs() asks about
// the column before it asks which backspace it is looking at.
func (e *Editor) deleteLineBack() error {
	e.breakRun()
	// CTRL-U is the one key that is not an ordinary character and still leaves
	// the insert quiet: measured over every insert command, a burst ending in
	// CTRL-U says the mode message once whatever it deleted, where the same
	// burst ending in BS or CTRL-W says it twice.
	e.ins.quiet = true
	// CTRL-U ends the undo step whatever it goes on to do, and the next change
	// opens the next one. BS and CTRL-W do not: measured, two CTRL-U over one
	// insert leave undotree().seq_cur at 3 where four backspaces over the same
	// text leave 1, and three CTRL-U of which the last two delete nothing at
	// all still leave 3.
	if !e.ins.replaying && !e.inDot {
		// Not while the count is replaying. vim's redo stuffs the keys back
		// into the same edit() and the second CTRL-U does not number a third
		// undo header: measured, "2I <C-U>Z" leaves undotree().seq_cur at 2
		// where syncing on the replay too leaves 3.
		//
		// And not while "." is, for the same reason and the same buffer: a
		// dot repeat rides the same stuff buffer the count does. Measured on
		// "abc", "i<CR><C-U><Esc>." leaves undotree().seq_cur at 2 with two
		// changelist entries where syncing on the repeat leaves 3 and three.
		// A macro is not on the list: it is typeahead and not the stuff
		// buffer, and its CTRL-U numbers a header like a typed one.
		e.syncUndo()
		e.ins.needUndo = true
	}
	if e.mode == Replace {
		return e.replaceLineBack()
	}
	if e.cur.Col == 0 {
		// Measured: "A abc<CR>def" then two CTRL-U leaves one line, "zzz abc",
		// because the second one is in column zero and joins.
		if e.cur.Line == 1 || !e.canBackspaceOver("eol") {
			return e.unrecordKey()
		}
		e.insUndo()
		return e.joinBack(true)
	}
	line := e.buf.Line(e.cur.Line)
	// The two boundaries 'backspace' owns, which are ins_bs()'s early return
	// and not the stop below: without "start" nothing before the point the
	// insert began at may go, and without "indent" nothing of an autoindent
	// this insert put on may.
	if !e.canBackspaceOver("start") && e.cur.Line == e.ins.startOrig.Line && e.cur.Col <= e.ins.startOrig.Col {
		return e.unrecordKey()
	}
	if !e.canBackspaceOver("indent") && e.ins.ai > 0 && e.cur.Col <= e.ins.ai {
		return e.unrecordKey()
	}
	// The stop at the first non-blank is 'autoindent' and not 'backspace'.
	// vim's ins_bs() computes mincol from "curbuf->b_p_ai || cindent_on()"
	// and never asks 'backspace' about it; 'cindent' does not exist here yet
	// and belongs in this test the day it does. Measured both ways on
	// " foobar": under 'autoindent' A CTRL-U X leaves " X" whatever
	// 'backspace' says, and under 'noautoindent' it leaves "X".
	col := 0
	if e.opt.AutoIndent {
		if nb := firstNonBlank(line); nb < e.cur.Col {
			col = nb
		}
	}
	// And the deletion also stops dead on the point the insert began at, which
	// is a stop rather than a boundary: it applies whatever 'backspace' says.
	if stop, ok := e.insertStop(); ok && stop > col && stop < e.cur.Col {
		col = stop
	}
	if col >= e.cur.Col {
		return nil
	}
	n := e.cur.Col - col
	e.insUndo()
	e.cur = e.buf.Delete(text.Range{
		Start: text.Pos{Line: e.cur.Line, Col: col},
		End:   text.Pos{Line: e.cur.Line, Col: e.cur.Col},
	})
	e.trimTyped(n)
	e.ins.ai = -1
	e.followInsertStart()
	return nil
}

// followInsertStart is the tail of vim's ins_bs(): a deletion that went back
// past where the insert began moves the start back with it, so that everything
// typed after it counts as entered text for the next backspace.
//
// It moves the column and only on its own line. Moving it onto whatever line
// the cursor is now on instead loses the stop for every later CTRL-U:
// "A abc<CR>def" then three CTRL-U leaves "zzz" and not "".
//
// CTRL-U calls it and BS and CTRL-W do not, which is not what a reading of
// vim's one ins_bs() with one tail suggests and is what was measured. Calling
// it from all three was tried and is wrong three ways:
// "A BS C CTRL-W" over "map } gt" leaves "map } " in vim and "map } g" with
// it, "A BS C CTRL-U" leaves the line empty in vim and "map } g" with it, and
// "a X BS BS v: CTRL-U" leaves "vap } gt" in vim, where the same keys with a
// CTRL-U in place of the first BS leave "ap } gt". The buffer and the cursor
// are byte-identical before that last CTRL-U in both, so the difference is
// entirely in where vim thinks the insert began, and what moves it is not
// simply "the cursor went before it". The measurements are in the notes under
// the fuzz report; whatever the rule is, it is not this one applied
// everywhere.
//
// Tried again on the same day after the CTRL-U join rule below was measured
// and Insstart_orig was split out, in case the split was what the first
// attempt was missing. It is not. Calling this from the backspace fixes
// "a3 CTRL-U BS v: CTRL-U" on "map } gt", which vim leaves as "ap } gt" and
// which stops at column 1 without it, and breaks the same three cases again:
// the stop is at column 0 for that one and at column 1 for
// "aX BS BS v: CTRL-U", whose cursor and buffer are identical when the last
// CTRL-U is typed. The only thing that differs between them is that the first
// deleted with a CTRL-U before the backspace and the second with a backspace,
// so whatever moves it is something the CTRL-U did and not the position the
// backspace left.
// insertStop is the column on the cursor's own line that CTRL-W and CTRL-U
// stop deleting at, and whether there is one at all.
//
// Ordinarily it is where the insert began, moved along by a join. What breaks
// that is a join done by CTRL-U rather than by a backspace or a CTRL-W: on
// "alpha / beta / gamma", where the join is the second keystroke every time,
// "2GO CTRL-U l CTRL-U" and "2GO CTRL-U l CTRL-W" both leave line 1 empty
// where "2GO BS l CTRL-U" and "2GO CTRL-W l CTRL-U" leave "alpha", and the
// emptiness survives typing in between ("2GO CTRL-U lz CTRL-U" empties it
// too). It is not that a CTRL-U join leaves no stop ever: on "zzz",
// "A abc CR def" then three CTRL-U leaves "zzz", stopping at the column the A
// began in, because there the insert began on the line the join joined ONTO.
// On "zzz / yyy", where it began on the line that went, "jI CTRL-U CTRL-U X"
// leaves "Xyyy" and stops nowhere.
//
// It is what makes a CTRL-U in a counted insert take the whole line the second
// time round: "12e 2I CTRL-U K" over a tab-indented line above a line of
// Japanese leaves "K" and the Japanese, which pvim used to leave the first
// line's text in front of.
func (e *Editor) insertStop() (int, bool) {
	if e.ins.noStop || e.cur.Line != e.ins.start.Line {
		return 0, false
	}
	return e.ins.start.Col, true
}

func (e *Editor) followInsertStart() {
	if e.cur.Line == e.ins.start.Line && e.cur.Col < e.ins.start.Col {
		e.ins.start.Col = e.cur.Col
	}
	if e.cur.Line == e.ins.startOrig.Line && e.cur.Col < e.ins.startOrig.Col {
		e.ins.startOrig.Col = e.cur.Col
	}
}

// shiftLine is CTRL-T and CTRL-D: one 'shiftwidth' of indent added or taken
// away, rounded to a multiple of it whether or not 'shiftround' is set, which
// is what makes CTRL-T on a two-space indent with sw=4 give four and not six.
func (e *Editor) shiftLine(dir int) error {
	e.insUndo()
	e.breakRun()
	line := e.buf.Line(e.cur.Line)
	sw := e.opt.ShiftWidth
	if sw <= 0 {
		sw = e.opt.TabStop
	}
	nb := firstNonBlank(line)
	width := text.DisplayCol(line, nb, e.opt.TabStop)
	var want int
	if dir > 0 {
		want = (width/sw)*sw + sw
	} else {
		if width == 0 {
			return nil
		}
		want = (width - 1) / sw * sw
	}
	indent := makeIndent(want, e.opt)
	e.buf.SetLine(e.cur.Line, append(append([]byte(nil), indent...), line[nb:]...))
	e.cur.Col += len(indent) - nb
	if e.cur.Col < 0 {
		e.cur.Col = 0
	}
	e.ins.ai = -1
	return nil
}

// insertLastInsert is CTRL-A: the text of the previous insert, which lives in
// the ". register.
func (e *Editor) insertLastInsert() error {
	v, err := e.regs.Get(register.LastInsert)
	if err != nil || v.Empty() {
		return nil
	}
	e.insertBytes(v.Bytes())
	return nil
}

// insertFromLine is CTRL-E and CTRL-Y: the character below or above the
// cursor, taken at the same display column.
func (e *Editor) insertFromLine(dir int) error {
	ln := e.cur.Line + dir
	if ln < 1 || ln > e.buf.LineCount() {
		return nil
	}
	here := e.buf.Line(e.cur.Line)
	other := e.buf.Line(ln)
	disp := text.DisplayCol(here, e.cur.Col, e.opt.TabStop)
	col := text.ByteColForDisplay(other, disp, e.opt.TabStop)
	if col >= len(other) {
		return nil
	}
	e.insertBytes(other[col:nextRune(other, col)])
	return nil
}

// insRegKind is which of the four CTRL-R commands in insert mode is running.
//
// They are one key apart and three behaviours apart, and the middle two keys
// are where a first implementation loses them: CTRL-R CTRL-O is not CTRL-R
// with the register named "O", it is a third command whose second key says
// how the text goes in. The vimrc on this machine has
// "inoremap <C-v> <C-r><C-o>+" in it, so every paste made by that mapping was
// landing as a literal "+" until this existed.
type insRegKind int

const (
	// insRegTyped is CTRL-R x: the text goes in as though it had been typed,
	// so a <BS> in the register deletes and 'autoindent' and 'textwidth'
	// apply to it.
	insRegTyped insRegKind = iota
	// insRegLiteral is CTRL-R CTRL-R x: the same path with the control
	// characters escaped, so the <BS> is inserted rather than obeyed. The
	// newline between two of the register's lines is NOT escaped, which is
	// why this still autoindents: measured, a charwise "XX\nYY" put in with
	// either key at the end of " foo" leaves " YY" under it.
	insRegLiteral
	// insRegPut is CTRL-R CTRL-O x: not typing at all. vim's ins_reg() calls
	// do_put(BACKWARD, PUT_CURSEND) for this one, which is gP, so a linewise
	// register goes in ABOVE the line the insert is on and nothing is
	// indented.
	insRegPut
	// insRegPutIndent is CTRL-R CTRL-P x: the same put with PUT_FIXINDENT,
	// which is "[p.
	insRegPutIndent
)

// insertRegister is the key after CTRL-R: either a register name, or one of
// the three keys that name a different command and take the register name
// after themselves.
//
// vim's ins_reg() reads exactly one extra key and only for these three, so
// "CTRL-R CTRL-A" is not a fourth form: CTRL-A is not a register name either,
// and it beeps.
func (e *Editor) insertRegister(k key.Key) error {
	switch k {
	case key.Ctrl('r'):
		e.wait = func(k key.Key) error { return e.registerInto(k, insRegLiteral) }
		return nil
	case key.Ctrl('o'):
		e.wait = func(k key.Key) error { return e.registerInto(k, insRegPut) }
		return nil
	case key.Ctrl('p'):
		e.wait = func(k key.Key) error { return e.registerInto(k, insRegPutIndent) }
		return nil
	}
	return e.registerInto(k, insRegTyped)
}

// registerInto is the register name and what to do with what is in it.
func (e *Editor) registerInto(k key.Key, kind insRegKind) error {
	e.breakRun()
	if k == keyEsc || !k.IsRune() || k.Rune > 0x7f {
		return nil
	}
	if kind == insRegPut || kind == insRegPutIndent {
		return e.putRegisterInline(byte(k.Rune), kind == insRegPutIndent)
	}
	v, err := e.regs.Get(byte(k.Rune))
	// The count replays the TEXT and not the two keys. vim's insert_reg()
	// pushes the register through stuffescaped(), so what reaches the redo
	// buffer is the characters it produced and never the CTRL-R: "2i1 CTRL-R
	// 0 d" with register 0 empty replays "1d" as one run of typing, where
	// replaying the CTRL-R breaks the run and reports a column further on.
	e.unrecordRegister(kind)
	if err != nil {
		// Silent. vim's ins_reg() calls vim_beep(BO_REG) for a name it does
		// not know and says nothing at all, where the same name after "@"
		// gets E354: measured, "i CTRL-R >" leaves the message line empty.
		return e.beep()
	}
	if v.Empty() {
		return nil
	}
	return e.stuffRegister(v, kind == insRegLiteral)
}

// stuffRegister feeds a register back through the editor as though it had been
// typed, which is vim's stuffescaped() and the newline its caller writes
// between the register's lines.
//
// Two things follow from it being the key path and not a byte splice, and both
// were measured against vim. A newline arrives as an Enter, so 'autoindent'
// puts the current line's indent on the line it starts: with autoindent set, a
// charwise "XX\nYY" typed in at the end of " foo" leaves " YY" and not
// "YY". And a control character in the register is obeyed unless it is
// escaped, so a register holding "ab" 0x08 "c" goes in as "ac" through CTRL-R
// and as "ab^Hc" through CTRL-R CTRL-R.
func (e *Editor) stuffRegister(v register.Value, literally bool) error {
	for i, ln := range v.Lines {
		for _, k := range decodeKeys(ln) {
			if literally && needsLiteralPrefix(k) {
				// vim's stuffescaped() puts a CTRL-V in front of a control
				// character when "literally" is set and leaves everything
				// else alone, so this is that CTRL-V and not a second way of
				// inserting text.
				if err := e.stuffKey(key.Ctrl('v')); err != nil {
					return err
				}
			}
			if err := e.stuffKey(k); err != nil {
				return err
			}
		}
		// A newline between the lines, and one after the last line when the
		// register is linewise. Never escaped, in either form.
		if v.Type == register.TypeLine || i < len(v.Lines)-1 {
			if err := e.stuffKey(keyCR); err != nil {
				return err
			}
		}
	}
	return nil
}

// stuffKey feeds one key back in. It goes through the insert-mode handler
// while the editor is still inserting and through the normal dispatch when it
// is not, because a register holding an Escape leaves insert mode in vim too
// and the keys after it are then normal-mode commands.
func (e *Editor) stuffKey(k key.Key) error {
	switch e.mode {
	case Insert, Replace:
		return e.insertKey(k)
	}
	return e.dispatch(k)
}

// needsLiteralPrefix reports whether stuffescaped() would put a CTRL-V in
// front of this key: a control character other than Tab, or DEL. Everything
// else goes in as itself in both forms.
func needsLiteralPrefix(k key.Key) bool {
	b := k.ToBytes()
	if len(b) != 1 {
		return false
	}
	return (b[0] < ' ' && b[0] != '\t') || b[0] == 0x7f
}

// putRegisterInline is CTRL-R CTRL-O and CTRL-R CTRL-P: the register put with
// do_put rather than typed.
//
// vim's ins_reg() calls do_put(BACKWARD, PUT_CURSEND), which is gP, and adds
// PUT_FIXINDENT for CTRL-P, which is "[p. The three differences that shows are
// all measured: a linewise register goes in above the line the insert is on
// and pushes it down, nothing is autoindented, and in Replace mode it inserts
// rather than overwriting, because do_put has never heard of replace mode.
//
// It also appends CTRL-R, the second key and the register name to the record
// rather than the text, which is vim's three AppendCharToRedobuff calls: a "."
// after this one reads the register AGAIN, so changing the register between
// the two changes what the repeat puts. Measured both ways against the typed
// form, which replays the text it produced the first time.
func (e *Editor) putRegisterInline(name byte, fixIndent bool) error {
	second, secondByte := key.Ctrl('o'), byte(0x0f)
	if fixIndent {
		second, secondByte = key.Ctrl('p'), byte(0x10)
	}
	e.cmdKeys = append(e.cmdKeys, second, key.Rune(rune(name)))
	if !e.ins.replaying {
		// ". holds the keys and not the text for these two, which is what
		// :help i_CTRL-R_CTRL-O says in as many words and what getreg('.')
		// reports: "^R^Oa" and not what was in a.
		e.ins.text = append(e.ins.text, 0x12, secondByte, name)
	}
	v, err := e.regs.Get(name)
	if err != nil {
		return e.beep()
	}
	if v.Empty() {
		// do_put's own message, which the typed form never prints: measured,
		// "i CTRL-R CTRL-O z" with z empty leaves "E353: Nothing in register
		// z" where "i CTRL-R z" leaves the message line untouched.
		e.Say("E353: Nothing in register " + string(rune(name)))
		return e.beep()
	}
	e.insUndo()
	e.ins.ai = -1
	e.comp = completion{}
	if v.Type == register.TypeChar {
		// Charwise is done here rather than through operator.Put for the
		// cursor: PUT_CURSEND leaves it one PAST the last byte, which is
		// where the insert carries on typing, and operator.Put clamps its
		// answer to where normal mode may stand. Buffer.Insert already
		// returns the position just after what it wrote, so this is the whole
		// of it.
		lines := append([][]byte(nil), v.Lines...)
		if fixIndent {
			e.fixPutIndent(lines)
		}
		e.cur = e.buf.Insert(e.cur, bytes.Join(lines, []byte{'\n'}))
		return nil
	}
	res, err := operator.Put(operator.PutRequest{
		Buf:    e.buf,
		Val:    v,
		At:     e.cur,
		Before: true,
		After:  true,
		Count:  1,
		Indent: fixIndent,
		Opt:    e.opt.Operator(),
	})
	if err != nil {
		return e.beep()
	}
	e.cur = e.buf.Clamp(res.Cursor)
	return nil
}

// fixPutIndent is PUT_FIXINDENT over a charwise register: every line after the
// first is shifted by the difference between the indent of the line the cursor
// is on and the indent of the register's first line, so the block keeps its
// shape and lands under the text it was put next to.
//
// The first line is left alone because it is not on a line of its own: a
// charwise put goes in at the cursor, in the middle of whatever is there.
// Measured against "[p" on a tab-indented line with a register whose first
// line has two spaces of indent: the second line comes out with a tab, which
// is the delta and not the target indent copied over.
func (e *Editor) fixPutIndent(lines [][]byte) {
	if len(lines) < 2 {
		return
	}
	ts := e.opt.TabStop
	width := func(l []byte) int {
		return text.DisplayCol(l, firstNonBlank(l), ts)
	}
	delta := width(e.buf.Line(e.cur.Line)) - width(lines[0])
	if delta == 0 {
		return
	}
	for i := 1; i < len(lines); i++ {
		want := width(lines[i]) + delta
		if want < 0 {
			want = 0
		}
		lines[i] = append(makeIndent(want, e.opt), lines[i][firstNonBlank(lines[i]):]...)
	}
}

// unrecordRegister drops the keys that named the register from the record a
// count replays. See registerInto.
func (e *Editor) unrecordRegister(kind insRegKind) {
	if e.ins.replaying {
		return
	}
	// The CTRL-R itself, the second key when there was one, and the name.
	drop := 2
	if kind == insRegLiteral {
		drop = 3
	}
	if n := len(e.ins.keys); n >= drop {
		e.ins.keys = e.ins.keys[:n-drop]
	}
	// The CTRL-R reached cmdKeys through insertKey; the name did not, because
	// a key that a wait consumes returns before that append. The text the
	// stuffing is about to type puts itself there.
	if n := len(e.cmdKeys); n > 0 {
		e.cmdKeys = e.cmdKeys[:n-1]
	}
}

// recordText puts bytes into the replay record as though they had been typed,
// which is what vim's stuff buffer does to them on the way in.
func (e *Editor) recordText(b []byte) {
	if e.ins.replaying {
		return
	}
	for _, r := range string(b) {
		e.ins.keys = append(e.ins.keys, key.Rune(r))
	}
}

// insertOneShot is CTRL-O: the next command is a normal-mode one and then
// insert mode resumes where it left the cursor.
func (e *Editor) insertOneShot() error {
	e.ins.oneShot = true
	e.breakRun()
	e.mode = Normal
	e.pend = pending{}
	e.showMode()
	return nil
}

// resumeInsert is how a CTRL-O command hands control back.
func (e *Editor) resumeInsert() {
	e.ins.oneShot = false
	e.breakRun()
	e.mode = Insert
	if e.ins.cmd.replace {
		e.mode = Replace
	}
	e.showMode()
	e.ins.start, e.ins.startOrig = e.cur, e.cur
	e.ins.text = nil
}

// literalFirst is the first key after CTRL-V: a decimal, hexadecimal or
// Unicode code point, or any other key taken as itself.
func (e *Editor) literalFirst(k key.Key) error {
	e.cmdKeys = append(e.cmdKeys, k)
	if k.IsRune() {
		switch {
		case k.Rune >= '0' && k.Rune <= '9':
			return e.literalDigits(10, 3, int(k.Rune-'0'), 1, false)
		case k.Rune == 'x' || k.Rune == 'X':
			return e.literalDigits(16, 2, 0, 0, false)
		case k.Rune == 'u':
			return e.literalDigits(16, 4, 0, 0, true)
		case k.Rune == 'U':
			return e.literalDigits(16, 8, 0, 0, true)
		}
	}
	if k == keyCR || k == keyNL {
		e.insertLiteral([]byte{'\r'})
		return nil
	}
	e.insertLiteral(key.Bytes([]key.Key{k}))
	return nil
}

// insertLiteral puts in what a CTRL-V collected, with the changelist run
// broken around it the way vim breaks it.
//
// vim's insertchar() batches a run of pending ordinary input into one ins_str()
// and reports the change at the column that run STARTED in, which is why
// "A[xy]" leaves the changelist on the "[". The batching begins at the
// character insertchar() was called with, and a CTRL-V calls it on its own, so
// the character CTRL-V produced starts a run of its own however ordinary it
// is. A control character then ends one as well, exactly as a Tab does.
//
// Measured on "zz", the column being the one getchangelist() reports:
//
//	A[^Vy] column 3, the y: broken in front of it, the ] joined it
//	A[^V065] column 3, the A it made: the same, so this is not about
//	 the character being unprintable
//	A[^V^H] column 4, the ]: broken in front of the ^H and behind it
//	A[^V^H column 3, the ^H itself
//	A[^V^H x ^V^H y column 6, the y
func (e *Editor) insertLiteral(b []byte) {
	e.breakRun()
	e.insertBytes(b)
	if len(b) == 1 && (b[0] < 0x20 || b[0] == 0x7f) {
		e.breakRun()
	}
}

// literalDigits collects the rest of a CTRL-V code point. A key that is not a
// digit ends the number and is then handled as itself.
func (e *Editor) literalDigits(base, width, value, have int, char bool) error {
	e.wait = func(k key.Key) error {
		e.cmdKeys = append(e.cmdKeys, k)
		if d, ok := digitValue(k, base); ok {
			value = value*base + d
			have++
			if have < width {
				return e.literalDigits(base, width, value, have, char)
			}
			e.insertCode(value, char)
			return nil
		}
		e.insertCode(value, char)
		if have == 0 {
			return nil
		}
		return e.insertKey(k)
	}
	return nil
}

// insertCode puts the code point CTRL-V collected in.
//
// The u and U forms name a character and the decimal and x forms name a byte,
// which is the difference between CTRL-V u00e9 giving an e-acute in UTF-8 and
// CTRL-V 233 giving the single byte 0xe9. Both measured.
func (e *Editor) insertCode(value int, char bool) {
	if value == 0 {
		return
	}
	if char || value >= 0x100 {
		e.insertLiteral([]byte(string(rune(value))))
		return
	}
	e.insertLiteral([]byte{byte(value)})
}

// digitValue reads one digit in the given base.
func digitValue(k key.Key, base int) (int, bool) {
	if !k.IsRune() {
		return 0, false
	}
	var v int
	switch c := k.Rune; {
	case c >= '0' && c <= '9':
		v = int(c - '0')
	case base == 16 && c >= 'a' && c <= 'f':
		v = int(c-'a') + 10
	case base == 16 && c >= 'A' && c <= 'F':
		v = int(c-'A') + 10
	default:
		return 0, false
	}
	return v, true
}

// insertTab is the Tab key under 'expandtab' and 'softtabstop'.
//
// Two paths, and they report the change in two different places. With
// 'noexpandtab' and 'softtabstop' at zero vim's ins_tab() returns straight
// away and edit() puts one Tab byte in with ins_char(), which reports the
// column that byte went to. Every other setting goes down ins_tab()'s own
// path, which inserts the whole width as spaces one at a time and only then
// squashes the run back into a Tab when 'noexpandtab' says to; the LAST of
// those spaces is the change vim reports, so the column is the start plus the
// width minus one whatever the bytes end up being.
//
// Measured with ts=2 sts=2 over an empty line: "S<Tab>" leaves one Tab byte
// and reports column 1, "S<Tab><Tab>" leaves two and reports 2, and
// "Sab<Tab>" reports 3 over a three-byte line, which is a column that is not
// in the buffer at all. Under "vim --clean", where sts is 0, the same three
// report 0, 1 and 2.
func (e *Editor) insertTab() error {
	// A Tab is not part of a run of typed characters as far as the changelist
	// is concerned: it reports its own column and the character after it
	// reports its own too, where "ab" reports only the a. Measured on "ia\tb",
	// which lands on column 2, against "iab", which lands on column 0.
	e.breakRun()
	defer func() { e.breakRun() }()

	sts := e.opt.SoftTabStop
	if !e.opt.ExpandTab && sts == 0 {
		e.insertBytes([]byte{'\t'})
		return nil
	}
	if sts == 0 {
		sts = e.opt.TabStop
	}
	at := e.cur
	disp := text.DisplayCol(e.buf.Line(e.cur.Line), e.cur.Col, e.opt.TabStop)
	n := sts - disp%sts
	if e.opt.ExpandTab {
		e.insertBytes(spaces(n))
	} else {
		e.insTabSpaces(n)
		e.retabBeforeCursor()
	}
	// Replace mode reports it the same way, which the bytes on the line do not
	// say on their own: under ts=2 sts=2 'noexpandtab', "R<Tab>" on "a b"
	// reports column 1 over a line whose first byte is a tab, and "R<Tab><Tab>"
	// reports 2. Measured with 'expandtab' on and off and at ts=8 sts=4.
	e.buf.ChangedAt(text.Pos{Line: at.Line, Col: at.Col + n - 1})
	return nil
}

// insTabSpaces is the middle of vim's ins_tab: the width the Tab is worth, one
// space at a time.
//
// The first space goes in through ins_char() and the rest through ins_str(),
// which are the same thing in insert mode and are not in replace mode: only
// the first one covers a character, and the ones after it are pushed in front
// of the line's own text. Measured under ts=2 sts=2 on "a b": "R<Tab>" leaves
// "\t b", a line one byte SHORTER than it started, which an ins_char for
// every space cannot produce.
func (e *Editor) insTabSpaces(n int) {
	if n <= 0 {
		return
	}
	e.insertBytes(spaces(1))
	if n == 1 {
		return
	}
	if e.mode != Replace {
		e.insertBytes(spaces(n - 1))
		return
	}
	for range n - 1 {
		e.ins.over = append(e.ins.over, overwritten{})
		e.cur = e.buf.Insert(e.cur, []byte{' '})
		e.ins.text = append(e.ins.text, ' ')
	}
}

// retabBeforeCursor is the tail of vim's ins_tab: "when 'expandtab' is not set,
// replace spaces by TABs where possible".
//
// It takes the whole run of white space in front of the cursor -- not just what
// the Tab put there -- rebuilds it out of as many whole tabs as fit up to the
// column the cursor is in, and deletes what is left between them and the
// cursor. That is why the line can come out shorter than the spaces made it.
//
// Measured under ts=2 sts=2 'noexpandtab'. On "package main", "A CTRL-W Tab"
// leaves "package\t\t": the space CTRL-W left behind is part of the run and
// disappears into the tabs. On "a b", "4| i Tab" leaves "a\t\t b" and
// "5| i Tab" leaves "a\t\t\tb", both of which reach back over spaces that
// were in the file. Replace mode stops at the column the insert started in,
// which is vim's "don't delete characters before the insert point": on the same
// line "4| R Tab" leaves "a \tb" and rebuilds one column and not four.
func (e *Editor) retabBeforeCursor() {
	ts := e.opt.TabStop
	if ts < 1 {
		ts = 8
	}
	line := e.buf.Line(e.cur.Line)
	end := min(e.cur.Col, len(line))
	start := end
	for start > 0 && (line[start-1] == ' ' || line[start-1] == '\t') {
		start--
	}
	if e.mode == Replace && e.cur.Line == e.ins.start.Line && start < e.ins.start.Col {
		start = min(e.ins.start.Col, end)
	}
	if start >= end {
		return
	}
	want := text.DisplayCol(line, end, ts)
	vcol := text.DisplayCol(line, start, ts)
	white := make([]byte, 0, end-start)
	for vcol+ts-vcol%ts <= want {
		white = append(white, '\t')
		vcol += ts - vcol%ts
	}
	for ; vcol < want; vcol++ {
		white = append(white, ' ')
	}
	if bytes.Equal(white, line[start:end]) {
		return
	}
	// Replace mode's stack loses one entry per byte the rebuild dropped: the
	// bytes are gone, and a backspace that popped an entry with nothing left
	// to put it back on would put a character back in the wrong column.
	if dropped := (end - start) - len(white); dropped > 0 && e.mode == Replace {
		if dropped > len(e.ins.over) {
			dropped = len(e.ins.over)
		}
		e.ins.over = e.ins.over[:len(e.ins.over)-dropped]
	}
	e.cur = e.buf.Replace(text.Range{
		Start: text.Pos{Line: e.cur.Line, Col: start},
		End:   text.Pos{Line: e.cur.Line, Col: end},
	}, white)
}

// endInsert leaves insert or replace mode: the count is replayed, the
// autoindent nobody typed on is dropped, the block insert is spread down its
// column, ". is set and the undo block closes.
func (e *Editor) endInsert() error {
	e.comp = completion{}
	e.dropPendingIndent()

	// Whether the typing itself added or removed a line, asked before the
	// count is replayed: "3o" repeats the open and does not count, "2o<CR>"
	// does. Only the 'conceallevel' rule below reads it.
	typedLines := e.buf.LineCount() != e.ins.opened

	// o and O repeat their line even with nothing typed on it: "3o" on an
	// empty buffer leaves three empty lines where "3i" leaves none. Measured.
	opened := e.ins.cmd.cmd == 'o' || e.ins.cmd.cmd == 'O'
	repeated := false
	if n := e.ins.cmd.count; n > 1 && (len(e.ins.keys) > 0 || opened) {
		repeated = opened && len(e.ins.text) == 0
		e.repeatInsert(n - 1)
	}
	if b := e.ins.cmd.block; b != nil {
		e.spreadBlock(b)
	}

	e.regs.SetLastInsert(e.ins.text)
	e.insert = append([]byte(nil), e.ins.text...)
	e.buf.SetMark(text.MarkLastInsert, e.cur)

	// Vim leaves the cursor on the last character typed rather than after it,
	// in replace mode as well as insert mode, and leaves it alone in column
	// zero because there is nowhere to the left of that.
	at := e.cur
	if b := e.ins.cmd.block; b != nil && b.home >= 0 {
		// I and A on a block leave the cursor on the block's left edge.
		line := e.buf.Line(b.first)
		at = text.Pos{Line: b.first, Col: text.ByteColForDisplay(line, b.home, e.opt.TabStop)}
	} else if line := e.buf.Line(at.Line); at.Col > 0 {
		at.Col = prevRune(line, min(at.Col, len(line)))
	}
	e.moveTo(at)

	// One more mode message when the insert moved lines around and the last
	// thing done was not an ordinary typed character. "o" with nothing after
	// it, "o<Tab>", "o0" and "a<CR>" all leave "-- INSERT ---- INSERT --" in
	// the redirect where "oab" leaves one, and an insert that changed no line
	// count never does it however it ended.
	//
	// It is a screen effect, which is why the exemptions look arbitrary: what
	// vim is really asking is whether the redraw at the end of the insert had
	// to shift rows the terminal was already showing, because that is what
	// clobbers the message line and makes showmode() say it again. Three
	// shapes of insert do not shift anything and are exempt, each measured
	// against vim rather than reasoned about: one that ends on the LAST line
	// of the buffer, because there is no displayed line under it to move; one
	// where O opened the buffer's first line; and CTRL-U, which is quiet
	// whatever it deleted. r is exempt because it is one keystroke and not an
	// insert.
	//
	// The window is the other half, and it is what the exemption for the last
	// line of the buffer misses on its own: "G o" on a 38-line file in a
	// 39-row window says the mode once, and the same keys on a 39-line file
	// say it twice, because there the new line pushed the window down a row
	// and every row on the screen moved with it. Measured at exactly that
	// boundary, both sides of it.
	//
	// A window that moved is enough on its own, line count or no line count:
	// on a 39-line log in the same window, "Gi<CR><BS>" and "}i<CR><BS>" end
	// with the buffer the length it started and vim still says the mode twice,
	// because the CR pushed the screen down before the BS pulled it back. The
	// same keys from line 1, where nothing scrolls, say it once.
	// Not while a dot repeat is replaying, for the same reason the main loop's
	// redraw is not: the whole block is behind vim's stuff_empty(), and the
	// stuff buffer is what a dot repeat rides. A macro is the typeahead and
	// not the stuff buffer, so it still says it. Measured: "3o<Esc>." says the
	// mode message twice and then once.
	//
	// "quiet" does not decide WHETHER the redraw says it again, only WHEN: an
	// insert that ended on an ordinary character says it after ins_esc has put
	// the state back to normal, where showmode writes the recording indicator
	// and nothing else. That is why it looks like suppression everywhere
	// except under a recording, and why "qaoX<Esc>q" leaves five
	// "recording @a" in the redirect and "qaS!<Esc>q", which moved no lines,
	// leaves four.
	scrolled := e.ins.scrolled || e.mctx.Window.Top != e.ins.top
	// 'conceallevel' takes the last-line exemption away from an o whose own
	// typing moved a line. vim redraws the cursor line while an insert runs
	// when 'conceallevel' is set, and that redraw says the mode again.
	// Measured under "conceallevel=2" on a one-line file: "o<CR>" and "o<BS>"
	// say it twice where "o", "o<Tab>", "A<CR><CR>", "i<CR><CR>", "s<CR>",
	// "C<CR>", "S<CR>" and "cc<CR>" all say it once. The vimrc sets
	// conceallevel=2, so this is the profile pvim runs under every day.
	// o and not O: measured both ways. "2GO<BS>l<C-W>" under the vimrc
	// profile says the mode once where the same keys after an "o" say it
	// twice.
	concealed := e.opt.ConcealLevel > 0 && e.ins.cmd.cmd == 'o' && typedLines
	again := (e.buf.LineCount() != e.ins.lines || scrolled || concealed) && e.ins.cmd.cmd != 'r' &&
		(e.cur.Line != e.buf.LineCount() || scrolled || concealed) &&
		!e.ins.openedTop && !(e.replaying && !e.atMacro)
	if again && !e.ins.quiet {
		e.showMode()
	}

	e.mode = Normal
	// vim's ins_esc: showmode() when there is still something to show, which
	// is a recording, and msg("") to wipe the mode message when there is not.
	// The bare newline that msg("") leaves in the redirect is the whole of the
	// difference between "-- INSERT --\n" and "-- INSERT --" on 37 runs.
	if e.rec.reg != 0 {
		e.showMode()
	} else if !e.atMacro {
		e.msg.empty()
	}
	if again && e.ins.quiet {
		e.showMode()
	}
	// An o or O repeated with nothing typed on the line says it once more
	// again. vim's start_redo_ins() stuffs a newline for the open commands and
	// nothing for the others, and that newline is one more trip round edit()'s
	// loop; with text typed after it the trip ends on an ordinary character
	// and there is nothing left to say. Measured: "qa2o<Esc>q" leaves six
	// "recording @a" where "qao<Esc>q", "qa2oX<Esc>q" and "qa2iZ<Esc>q" leave
	// five.
	if repeated {
		e.showMode()
	}
	e.closeUndo()
	e.recordDot(e.ins.dotCount, e.ins.dotReg)
	e.pend = pending{}
	e.cmdKeys = nil
	return nil
}

// repeatInsert is the count on i, a, o and R: the text is typed again, and for
// o and O a line is opened for each repeat.
func (e *Editor) repeatInsert(times int) {
	open := e.ins.cmd.cmd == 'o' || e.ins.cmd.cmd == 'O'
	// A snapshot, because the replay goes back through insertKey and a key
	// that reaches the wait dispatch would otherwise be appended to the slice
	// being walked.
	keys := append([]key.Key(nil), e.ins.keys...)
	e.ins.replaying = true
	defer func() { e.ins.replaying = false }()
	for i := 0; i < times; i++ {
		// Each copy is its own run as far as the changelist is concerned:
		// "3AX" reports the third X and not the first, where typing "XXX" by
		// hand reports the first.
		e.breakRun()
		if open {
			// vim does not repeat the o: start_redo_ins() stuffs a newline
			// into insert mode and copies the typed text after it, so the
			// repeat is an Enter, pressed where the cursor is, and it reports
			// itself to the changelist there. Measured, "2o" on a three-line
			// file leaves the changelist on line 2 and not on the line it
			// opened, and "2O<C-U>" on line one leaves one entry and not two.
			if err := e.insertNewline(); err != nil {
				return
			}
		}
		for _, k := range keys {
			// The Escape that ended the insert is not in the record and
			// nothing else here leaves insert mode, so this cannot re-enter
			// endInsert. An error from a replayed key is dropped for the same
			// reason vim drops it: the keys already ran once.
			_ = e.insertKey(k)
		}
	}
}

// spreadBlock copies the text typed at the top of a blockwise insert into
// every other line of the block.
//
// Vim does not do it at all when the text has a line break in it, which is
// measured and not guessed: CTRL-V jj I X CR Y Escape leaves the other two
// lines alone.
func (e *Editor) spreadBlock(b *blockInsert) {
	if len(e.ins.text) == 0 || hasNewline(e.ins.text) {
		return
	}
	e.insUndo()
	ts := e.opt.TabStop
	for ln := b.first + 1; ln <= b.last && ln <= e.buf.LineCount(); ln++ {
		line := e.buf.Line(ln)
		switch {
		case b.toEOL:
			e.buf.Insert(text.Pos{Line: ln, Col: len(line)}, e.ins.text)
		case b.append:
			width := text.DisplayWidth(line, ts)
			if width < b.col {
				line = append(append([]byte(nil), line...), spaces(b.col-width)...)
				e.buf.SetLine(ln, line)
			}
			col := text.ByteColForDisplay(line, b.col, ts)
			if col > len(line) {
				col = len(line)
			}
			e.buf.Insert(text.Pos{Line: ln, Col: col}, e.ins.text)
		default:
			// vim's block_prep sets is_short when the line ENDS BEFORE the
			// block's column, not when it ends on it, and block_insert and
			// op_change both skip on that. Measured on a three-line block at
			// column 1: a line holding one character takes the text at its
			// end where an empty line is left alone. The off-by-one is what
			// made a blockwise c drop every line but the first, because a
			// block delete leaves the lower lines exactly that wide.
			if text.DisplayWidth(line, ts) < b.col {
				continue
			}
			e.buf.Insert(text.Pos{Line: ln, Col: text.ByteColForDisplay(line, b.col, ts)}, e.ins.text)
		}
	}
	// vim's block_insert() copies the text down and then reports the whole
	// thing once, from the line BELOW the one the typing happened on, in
	// column zero. That is why CTRL-V jj I X leaves the change on line 2.
	e.buf.ChangedAt(text.Pos{Line: b.first + 1})
}

// blockInsert is I and A on a blockwise selection: the insert happens on the
// first line and is copied down the block when it ends.
func (e *Editor) blockInsert(appendAfter bool) error {
	if e.mode != VisualBlock {
		// I and A on a charwise or linewise selection are vim's I and A on the
		// first line of it.
		start, _ := e.selection()
		e.endVisual()
		e.mode = Normal
		e.cur = start
		cmd := byte('I')
		if appendAfter {
			cmd = 'A'
		}
		return e.enterInsert(insCmd{cmd: cmd, count: 1})
	}
	span := e.visualSpanNow()
	cmd := byte('I')
	if appendAfter {
		cmd = 'A'
	}
	// Before endVisual, because the repeat needs the mode the selection was
	// made in and endVisual has already put it back to normal.
	e.recordVisualBlockInsert(cmd)
	e.endVisual()
	e.mode = Normal

	b := &blockInsert{
		first: span.Block.First,
		last:  span.Block.Last,
		col:   span.Block.Left,
		home:  span.Block.Left,
		toEOL: span.Block.ToEOL && appendAfter,
	}
	if appendAfter {
		b.append = true
		b.col = span.Block.Right + 1
	}
	line := e.buf.Line(b.first)
	col := text.ByteColForDisplay(line, b.col, e.opt.TabStop)
	if b.toEOL || col > len(line) {
		col = len(line)
	}
	e.cur = text.Pos{Line: b.first, Col: col}
	return e.enterInsert(insCmd{cmd: 'i', count: 1, block: b})
}

// spaces is n space bytes.
func spaces(n int) []byte {
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return b
}

// makeIndent builds an indent of the given display width out of tabs and
// spaces, or out of spaces alone under 'expandtab'.
func makeIndent(width int, opt Options) []byte {
	if width <= 0 {
		return nil
	}
	if opt.ExpandTab || opt.TabStop <= 0 {
		return spaces(width)
	}
	var b []byte
	for i := 0; i+opt.TabStop <= width; i += opt.TabStop {
		b = append(b, '\t')
	}
	return append(b, spaces(width%opt.TabStop)...)
}

// autoIndent is the indent 'autoindent' puts on a line opened below or above
// another, or on the second half of a line broken with Enter. upto is how much
// of the old line the indent may come from, which matters only when Enter is
// pressed inside the indent itself.
//
// It carries the WIDTH and not the bytes. vim's open_line takes the indent it
// found and hands it to set_indent(), which lays it out again under
// 'expandtab' and 'tabstop', so a four-space indent copied onto a line under
// ts=2 comes out as two tabs and a tab copied under 'expandtab' comes out as
// spaces. Both measured. A linewise change is the exception and keeps the
// bytes as they were, which is why cc does not go through here.
func autoIndent(line []byte, upto int, opt Options) []byte {
	if !opt.AutoIndent {
		return nil
	}
	end := firstNonBlank(line)
	if upto < end {
		end = upto
	}
	return makeIndent(text.DisplayCol(line, end, opt.TabStop), opt)
}

// insUndo opens the undo block for this insert at the first change it makes.
//
// vim's stop_arrow() calls u_save_cursor() when the first character is typed
// and not when insert mode starts, and where the cursor is at that moment is
// what "u" restores. Every insert-mode operation that touches the buffer calls
// this before it does.
func (e *Editor) insUndo() {
	if !e.ins.needUndo {
		return
	}
	e.ins.needUndo = false
	e.openUndo(e.cur)
	e.rememberLine()
}

// unrecordKey drops the key being handled from the record a count replays.
//
// vim's ins_bs() ends with AppendCharToRedobuff(c) and takes every one of its
// early returns before reaching it, so a backspace that had nothing to delete
// is not in the redo buffer and a count does not replay it. Measured on "2O"
// followed by CTRL-U in column zero of line one, where vim opens two lines:
// the CTRL-U did nothing, so the repeat is the stuffed newline alone.
//
// It returns nil so that the no-op returns it replaces stay one line.
func (e *Editor) unrecordKey() error {
	if !e.ins.replaying && len(e.ins.keys) > 0 {
		e.ins.keys = e.ins.keys[:len(e.ins.keys)-1]
	}
	return nil
}

// insertRun puts bytes in at the cursor and keeps the changelist's idea of a
// run of typed characters, without recording them as text that was typed. It
// is what insertBytes and the count repeat share.
func (e *Editor) insertRun(b []byte) {
	e.insUndo()
	at := e.cur
	e.cur = e.buf.Insert(e.cur, b)
	if e.ins.inRun {
		e.buf.ChangedAt(e.ins.run)
	} else {
		e.ins.run, e.ins.inRun = at, true
	}
	e.ins.quiet = true
	e.ins.trimmed = false
}

// breakRun ends the run of typed characters the changelist reports at one
// column, and with it the quiet the mode message asks about. Every key that is
// not an ordinary character calls it.
func (e *Editor) breakRun() { e.ins.inRun, e.ins.quiet = false, false }

// runBreaker reports whether a byte ends the run of typed characters the
// changelist reports at the column the run started in. See insertTyped.
func runBreaker(c byte) bool { return c == '\t' || c == '0' || c == '^' || c == '\n' }

// insertReplay types text again the way vim replays the redo buffer for a
// count: one character at a time, so the run rules land where they landed the
// first time. "2A0<Tab>" reports the Tab of the second copy and not the start
// of it.
func (e *Editor) insertReplay(typed []byte) {
	for i := 0; i < len(typed); {
		n := 1
		if r, w := utf8.DecodeRune(typed[i:]); r != utf8.RuneError || w > 1 {
			n = w
		}
		if brk := runBreaker(typed[i]); brk {
			e.breakRun()
			e.insertRun(typed[i : i+n])
			e.breakRun()
		} else {
			e.insertRun(typed[i : i+n])
		}
		i += n
	}
}

// hasNewline reports whether the typed text broke a line.
func hasNewline(b []byte) bool {
	for _, c := range b {
		if c == '\n' {
			return true
		}
	}
	return false
}

// clampInsert keeps a position on a line, allowing the column just past the
// last byte, which is where insert mode's cursor legitimately sits.
func (e *Editor) clampInsert(p text.Pos) text.Pos {
	if p.Line < 1 {
		p.Line = 1
	}
	if n := e.buf.LineCount(); p.Line > n {
		p.Line = n
	}
	if line := e.buf.Line(p.Line); p.Col > len(line) {
		p.Col = len(line)
	}
	if p.Col < 0 {
		p.Col = 0
	}
	return p
}
