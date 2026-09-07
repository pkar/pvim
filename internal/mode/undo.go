package mode

import (
	"strconv"

	"github.com/pkar/pvim/internal/text"
)

// Undo, and the two things around it that are not the tree: the block
// boundaries that decide what one press of u takes back, and U, which is one
// line's worth of before and not part of the tree at all.

// openUndo starts the undo step a command is about to make. One user-visible
// change is one block whatever it does to the buffer underneath: dw splices
// once and cw splices and then types, and both are one u.
func (e *Editor) openUndo(cursor text.Pos) {
	if e.undoOpen {
		return
	}
	e.buf.OpenUndoBlock(cursor)
	e.undoOpen = true
}

// openUndoNoLines is openUndo for a put, whose u_save saves no line. See
// text.Buffer.OpenUndoBlockNoLines.
func (e *Editor) openUndoNoLines(cursor text.Pos) {
	if e.undoOpen {
		return
	}
	e.buf.OpenUndoBlockNoLines(cursor)
	e.undoOpen = true
}

// closeUndo ends the open step, unless the keys are coming from a script.
//
// Vim's may_sync_undo() does nothing at all while it is reading a "-s" file, so
// three x commands through the oracle are ONE undo step and one u takes all
// three back. That is not a detail of the harness, it is what the editor being
// compared against does, and internal/text's changelist is built for it too:
// see the comment on noteChange, which counts one changelist entry per undo
// block for the same reason. SetScriptInput is how cmd/pvim's --oracle mode
// asks for it; an editor with a person in front of it leaves it off and gets
// one undo step per command, which is what vim does interactively.
func (e *Editor) closeUndo() {
	if !e.undoOpen || e.scriptInput {
		return
	}
	e.buf.CloseUndoBlock()
	e.undoOpen = false
}

// syncUndo closes the open step whatever the input is. u, CTRL-R, g- and g+
// call it because vim's undo commands sync first however the keys arrived, and
// so does insert mode's CTRL-U, which was measured breaking the block under a
// script when nothing else did.
func (e *Editor) syncUndo() {
	if !e.undoOpen {
		return
	}
	e.buf.CloseUndoBlock()
	e.undoOpen = false
}

// syncForUndo is the first three lines of vim's u_undo(): a count
// on u or CTRL-R is thrown away when the undo state was not synced, because
// the sync this does creates a header of its own and undoing more than one
// step across it is not what the count asked for.
//
// It bites under "-s", where nothing else ever syncs: "AX CTRL-U Esc 2u"
// undoes once in vim and stops with "1 change; before #2", where a count that
// survived undoes twice and ends on an empty undo stack.
func (e *Editor) syncForUndo(count int) int {
	if e.undoOpen {
		e.syncUndo()
		return 1
	}
	e.syncUndo()
	return count
}

// rememberLine saves the line a change is about to touch, for U.
//
// U is not the undo tree: it restores the line as it was before the run of
// changes to it began, and a change to a different line starts a new run. It
// is itself an undoable change, which is why the saved text is swapped rather
// than dropped when U runs.
func (e *Editor) rememberLine() {
	if e.uheld && e.uline == e.cur.Line {
		return
	}
	e.uline = e.cur.Line
	e.ucol = e.cur.Col
	e.utext = append([]byte(nil), e.buf.Line(e.cur.Line)...)
	e.uheld = true
}

// moveToChar is moveTo clamped the way normal mode needs.
//
// An undo header remembers where the change STARTED, which is an insert-mode
// column: after "Axy" it is column 3 of "zzz", the position after the last
// byte. text.Buffer.Clamp allows that column because insert mode legitimately
// sits there and normal mode does not, so u after "Axy<Esc>" left the cursor
// one past the end. Measured: vim reports column 3 there, and column 4 after
// the CTRL-R that puts the text back.
func (e *Editor) moveToChar(p text.Pos) {
	e.moveTo(p)
	if line := e.buf.Line(e.cur.Line); e.cur.Col >= len(line) && len(line) > 0 {
		e.moveTo(text.Pos{Line: e.cur.Line, Col: prevRune(line, len(line))})
	}
}

// undo is u, count times.
func (e *Editor) undo(count int) error {
	count = e.syncForUndo(count)
	moved := false
	for i := 0; i < count; i++ {
		lines, seq := e.buf.LineCount(), e.buf.UndoSeq()
		empty := e.buf.Emptied()
		p, ok := e.buf.Undo()
		if !ok {
			if !moved {
				e.Say("Already at oldest change")
				e.beep()
				return nil
			}
			break
		}
		e.moveToChar(p)
		e.msg.sayKeep(undoMessage(e.lineDelta(lines, empty), e.buf.UndoLines(), "before", seq))
		moved = true
	}
	e.uheld = false
	e.finish(false)
	return nil
}

// redo is CTRL-R, count times.
func (e *Editor) redo(count int) error {
	// No sync, unlike u. vim's u_undo() opens with "if (b_u_synced == FALSE)
	// { u_sync(TRUE); count = 1; }" and u_redo() does not, so a CTRL-R with
	// nothing to redo leaves the undo state exactly as it found it and the
	// next change joins the header that was already open. Measured under
	// "-s", where nothing else syncs: "x CTRL-R d_" leaves
	// undotree().seq_cur at 1, and syncing here left it at 2.
	moved := false
	for i := 0; i < count; i++ {
		lines, empty := e.buf.LineCount(), e.buf.Emptied()
		p, ok := e.buf.Redo()
		if !ok {
			if !moved {
				e.Say("Already at newest change")
				e.beep()
				return nil
			}
			break
		}
		e.moveToChar(p)
		e.msg.sayKeep(undoMessage(e.lineDelta(lines, empty), e.buf.UndoLines(), "after", e.buf.UndoSeq()))
		moved = true
	}
	e.finish(false)
	return nil
}

// lineDelta is how many lines an undo or redo added, counted the way vim's
// u_newcount and u_oldcount count them.
//
// A plain subtraction of the line counts is wrong at exactly one place: an
// empty buffer is one empty line that is not there. vim's ML_EMPTY flag says
// so and u_undo_end subtracts it back out, which is why "dG" then "u" on a
// three-line file says "3 more lines" and a subtraction says two, and why
// "dd" on a file holding one newline says "1 more line" where a subtraction
// says none at all. Measured on both, and on the CTRL-R that redoes them.
func (e *Editor) lineDelta(before int, wasEmptied bool) int {
	delta := e.buf.LineCount() - before
	if wasEmptied {
		delta++
	}
	if e.buf.Emptied() {
		delta--
	}
	return delta
}

// undoMessage is what u and CTRL-R print, which the oracle diffs.
//
// Measured, because the wording is not symmetrical and no reading of :help
// would produce it: one line gained is "1 more line" and one lost is "1 line
// less", while three lost are "3 fewer lines". The trailing "0 seconds ago" is
// two spaces after the sequence number, and it is always zero here because the
// undo tree keeps no timestamps; a real elapsed time is the day that matters
// and is where this will need one.
func undoMessage(delta, lines int, when string, seq int) string {
	// The time is empty at the root, because there is no header there to have
	// a time: "g-" on a buffer nothing has changed says "0 changes; before #0"
	// and two trailing spaces where every other one says "0 seconds ago".
	ago := "0 seconds ago"
	if seq == 0 {
		ago = ""
	}
	var what string
	switch {
	case delta == 1:
		what = "1 more line"
	case delta > 1:
		what = strconv.Itoa(delta) + " more lines"
	case delta == -1:
		what = "1 line less"
	case delta < -1:
		what = strconv.Itoa(-delta) + " fewer lines"
	case lines == 1:
		what = "1 change"
	default:
		// Same length as before, so vim counts the LINES the undo put back
		// rather than the commands that changed them: ":%s/a/X/g" over six
		// lines and ":%normal A!" over the same six both say "6 changes", and
		// a "g-" that had nowhere to go says "0 changes".
		what = strconv.Itoa(lines) + " changes"
	}
	return what + "; " + when + " #" + strconv.Itoa(seq) + "  " + ago
}

// undoTime is g- and g+, which walk the undo tree by sequence number rather
// than by branch, so they cross from one branch to another where u will not.
func (e *Editor) undoTime(count int, forward bool) error {
	e.syncUndo()
	lines, moved, put := e.buf.LineCount(), 0, 0
	empty := e.buf.Emptied()
	for i := 0; i < count; i++ {
		var p text.Pos
		var ok bool
		if forward {
			p, ok = e.buf.Newer()
		} else {
			p, ok = e.buf.Older()
		}
		if !ok {
			break
		}
		e.moveToChar(p)
		put += e.buf.UndoLines()
		moved++
	}
	// One message for the whole command, which is vim's u_undo_end called
	// once after the loop, and not one per step the way "u" and CTRL-R print
	// theirs. "g+" with nowhere to go says so and beeps; "g-" with nowhere to
	// go still reports, which is the asymmetry vim has and this does not
	// invent.
	if forward && moved == 0 {
		e.Say("Already at newest change")
		e.beep()
		e.finish(false)
		return nil
	}
	seq, when := e.buf.UndoSeq(), "after"
	if !forward {
		// "before #N" names the change that separates where the cursor is
		// from the state one step newer, which is the next sequence number
		// when there is one and 0 over a tree with nothing in it.
		when = "before"
		if seq++; seq > e.buf.UndoSeqLast() {
			seq = e.buf.UndoSeqLast()
		}
	}
	e.msg.sayKeep(undoMessage(e.lineDelta(lines, empty), put, when, seq))
	e.finish(false)
	return nil
}

// undoLine is U: the line as it was before the run of changes to it, put back
// as a change of its own so that u takes back the U.
func (e *Editor) undoLine() error {
	if !e.uheld || e.uline > e.buf.LineCount() {
		e.beep()
		return nil
	}
	was := append([]byte(nil), e.buf.Line(e.uline)...)
	wasCol := e.cur.Col
	e.cur = text.Pos{Line: e.uline, Col: e.ucol}
	e.openUndo(e.cur)
	e.buf.SetLine(e.uline, e.utext)
	e.closeUndo()
	e.utext, e.ucol = was, wasCol
	// check_cursor_col: the saved column may be past the end of the line that
	// came back, and normal mode does not stand there. "AX<Esc>U" over "aaa"
	// saved column 3 and vim reports column 3 of a three-byte line, which is
	// the last character and not the NUL after it.
	e.moveTo(normalPos(e.buf, e.cur))
	e.finish(false)
	return nil
}
