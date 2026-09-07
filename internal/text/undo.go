package text

import (
	"bytes"
	"time"
)

// Undo is a tree, not a stack. Vim's is, and the difference shows up the first
// time you undo three steps, type one character and want the three back: with a
// stack they are gone, with a tree they are a "g-" away on the other branch.
//
// A node is one undo step. Its parent is the state you get to with "u", its
// children are the states "C-r" and "g+" can reach, and its sequence number is
// the chronological order the states were created in, which is the order "g-"
// and "g+" walk regardless of branch. The root is sequence 0 and is the state
// the file was read in.
//
// The tree is parent pointers plus an index by sequence number, which is all
// that u, C-r, g- and g+ need; the children of a node are derivable from the
// index on the day something wants to print the tree.
//
// Each node also remembers a cursor position, and getting that right is most of
// the work. See cursorWalk.

// undoEntry is one contiguous run of lines swapped by one Replace.
//
// It is stored inverted relative to the buffer: lines is what has to go back
// when this node is applied, replaced is how many lines are there now. Applying
// a node swaps both, so the same node applied twice returns to where it
// started, which is how one structure serves undo and redo.
type undoEntry struct {
	first    int // 1-based line the run starts at
	lines    [][]byte
	replaced int
}

// undoNode is one state of the buffer.
type undoNode struct {
	seq    int
	parent *undoNode
	// last is the child "C-r" follows: the most recently created branch, which
	// is vim's b_u_curhead walk in the case that matters and the newest branch
	// otherwise.
	last *undoNode

	// entries are in the order applying this node must walk them, which is the
	// reverse of the order the changes were made in. They are reversed again
	// each time the node is applied, so the invariant holds in both directions.
	entries []undoEntry

	// cursor is where the cursor was when the block opened. Vim calls it
	// uh_cursor and it is the position "u" tries hardest to restore.
	cursor Pos

	// noEOL is the flag to restore with the text. A change that deletes the
	// last line of a file with no final newline changes it, and an undo that
	// leaves it changed makes the file grow a byte nobody typed.
	noEOL bool

	// emptied is the ML_EMPTY flag to restore with the text, for the same
	// reason noEOL is: an undo that puts the lines back but leaves the buffer
	// flagged empty writes a file with nothing in it.
	emptied bool

	// noLines says the block was opened by a command whose u_save saved no
	// line at all. Vim has two shapes of empty undo header and they report
	// differently. u_save_cursor(), which "X" and "~" call, saves the cursor
	// line whether or not the command goes on to change it, so undoing it
	// puts one line back and says "1 change". do_put()'s
	// u_save(lnum, lnum+1) saves the range BETWEEN two lines, which is no
	// lines, so undoing a put of an empty register says "0 changes" and
	// leaves the changelist alone. Measured, both.
	noLines bool

	// when the block was closed. Vim keeps one (uh_time) and prints it as the
	// "N seconds ago" in an undo message; nothing here reads it yet, so
	// the register D-007 still stands, but internal/undofile writes it and a
	// history restored from disk would otherwise come back claiming every step
	// happened at the epoch.
	when time.Time
}

// OpenUndoBlock starts an undo step whose remembered cursor is the given
// position, if no step is open already.
//
// The mode layer calls this before a command and CloseUndoBlock after, and
// everything in between is one "u". Vim gets the same boundaries from
// may_sync_undo(), which is worth knowing about because it explicitly does
// nothing while input is coming from a "-s" script: run two "x" commands
// through "vim -s" and a single "u" undoes both. Any oracle harness that scripts
// vim has to open and close blocks the same way or its undo counts will not
// match, and that is a harness bug that looks exactly like an editor bug.
func (b *Buffer) OpenUndoBlock(cursor Pos) {
	if b.undoOpen != nil {
		return
	}
	b.undoOpen = &undoNode{cursor: cursor, noEOL: b.noEOL, emptied: b.emptied}
	b.newChange = true
}

// ReopenUndoWithLine takes the "saved no lines" flag back off the open block,
// for the one command that sets it and then turns out to have gone further.
// See undoNode.noLines.
func (b *Buffer) ReopenUndoWithLine() {
	if b.undoOpen != nil {
		b.undoOpen.noLines = false
	}
}

// OpenUndoBlockNoLines is OpenUndoBlock for a command whose u_save saved no
// lines: do_put and nothing else. See undoNode.noLines.
func (b *Buffer) OpenUndoBlockNoLines(cursor Pos) {
	if b.undoOpen != nil {
		return
	}
	b.OpenUndoBlock(cursor)
	b.undoOpen.noLines = true
}

// CloseUndoBlock ends the open undo step and makes it the current state.
//
// A block that changed nothing is still recorded, because vim numbers an undo
// header at u_save and not at the change: "X" in column one, "d0" in column
// one and "~" on a digit all save undo and then find nothing to do, and a "u"
// after any of them says "1 change; before #1" and puts the cursor back where
// the command started. Dropping them here would cost nothing on the buffer and
// would answer "Already at oldest change" to a press of u that vim answers.
func (b *Buffer) CloseUndoBlock() {
	// A held block belongs to the command that held it, and the commands
	// running inside it do not get to end it. See HoldUndoBlock.
	if b.undoHold > 0 {
		return
	}
	n := b.undoOpen
	b.undoOpen = nil
	if n == nil {
		return
	}

	// Store the entries in the order an undo has to walk them: last change
	// first, because each one's line numbers are only valid once the changes
	// after it have been taken back.
	reverse(n.entries)

	n.when = nowFunc()
	b.undoSeqMax++
	n.seq = b.undoSeqMax
	n.parent = b.undoCur
	b.undoCur.last = n
	b.undoCur = n
	b.undoBySeq = append(b.undoBySeq, n)
}

// UndoBlockOpen reports whether a step is open. The ex layer asks so that a
// command which opened one and changed nothing can tell its own empty block
// from one it joined.
func (b *Buffer) UndoBlockOpen() bool { return b.undoOpen != nil }

// UndoBlockChanged reports whether the open step has recorded anything.
func (b *Buffer) UndoBlockChanged() bool { return b.undoOpen != nil && len(b.undoOpen.entries) > 0 }

// DropUndoBlock throws the open step away instead of numbering it.
//
// That is the opposite of CloseUndoBlock and it is not a disagreement: the
// commands that keep an empty step called u_save before they looked (X in
// column one, ~ on a digit), and the one command that needs this calls it only
// when it has found something to change. See internal/ex.editIfChanged.
func (b *Buffer) DropUndoBlock() {
	if b.undoHold == 0 {
		b.undoOpen = nil
	}
}

// HoldUndoBlock keeps the open undo step open until the matching
// ReleaseUndoBlock, whatever the code running in between does.
//
// ":g" is what this is for, and it is the only caller. A global runs an
// ordinary ex command once per marked line and every one of those commands
// opens and closes its own block, so without a hold ":g/x/d" on six lines
// would leave six undo steps and six presses of u to take one command back.
// Vim makes the whole global one step, measured: ":g/aaa/d" then a single "u"
// on the six-line corpus file gives every deleted line back.
//
// A hold rather than a depth count on the open, because the opens underneath
// are not balanced with their closes and cannot be made so: the mode machine
// skips its close entirely while input is coming from a "-s" script, so a
// counter would be left one short and the block would never close at all.
// Suppressing the close outright has no such failure mode -- the command that
// held it is the one that releases it.
func (b *Buffer) HoldUndoBlock() { b.undoHold++ }

// ReleaseUndoBlock ends the hold HoldUndoBlock started and closes the step.
func (b *Buffer) ReleaseUndoBlock() {
	if b.undoHold > 0 {
		b.undoHold--
	}
	if b.undoHold == 0 {
		b.CloseUndoBlock()
	}
}

// sameSingleLine reports whether two neighbouring entries save the same single
// line, which vim's u_savecommon refuses to save twice.
//
// Its comment says why: "When saving a single line, and it has been saved just
// before, it doesn't make sense saving it again. Saves a lot of memory when
// making lots of changes inside the same line." The saving is invisible until
// the undo message counts entries, and then it is the difference between
// "xxx" then "u" saying "1 change", which vim does, and "3 changes".
func sameSingleLine(e, prev *undoEntry) bool {
	return prev != nil && e.first == prev.first &&
		len(e.lines) == 1 && len(prev.lines) == 1 &&
		e.replaced == 1 && prev.replaced == 1
}

// changedLines is how many of the lines an undo entry puts back are not
// already there, which is vim's ue_size.
//
// The trim is what makes the two comparable. vim saves exactly the lines a
// change touched; this buffer splices by byte range and its saved run picks up
// the untouched line on either side of the split, so "dd" saves two lines
// where vim saves one. Counting the raw run makes "ddp" then "u" say
// "3 changes" where vim says "1 change"; counting only the lines that differ
// from what is there gives vim's number for both that and ":%!sort", which
// says "6 changes" in both.
func changedLines(put, have [][]byte) int {
	i := 0
	for i < len(put) && i < len(have) && bytes.Equal(put[i], have[i]) {
		i++
	}
	j := 0
	for j < len(put)-i && j < len(have)-i && bytes.Equal(put[len(put)-1-j], have[len(have)-1-j]) {
		j++
	}
	return len(put) - i - j
}

// UndoLines is how many lines the last Undo or Redo put back, which is vim's
// u_newcount and is the number the undo message reports as "N changes" when
// the buffer came out the same length. Anything else in the message is the
// difference in the line count, which the caller already has.
func (b *Buffer) UndoLines() int { return b.undoLines }

// UndoSeq returns the sequence number of the current state, 0 at the root. It
// is vim's undotree().seq_cur.
//
// An open block counts, whether or not anything went into it. Vim numbers an
// undo header when it creates it, at u_save and not at the change, so
// undotree().seq_cur is 1 straight after the first "x" and stays 1 through the
// second and third -- and it is also 1 after an "X" in column one, which saves
// undo and then finds nothing to delete. Reading it off undoCur alone would
// report 0 for the whole of a "vim -s" script, which never syncs.
func (b *Buffer) UndoSeq() int {
	if b.undoOpen != nil {
		return b.undoSeqMax + 1
	}
	return b.undoCur.seq
}

// UndoSeqLast returns the highest sequence number in the tree, vim's
// undotree().seq_last.
func (b *Buffer) UndoSeqLast() int { return b.undoSeqMax }

// Undo takes back one step, "u", and returns the position to put the cursor.
// It reports false when there is nothing left to undo.
//
// The open block, if any, is closed first: vim's "u" after a half-finished
// insert undoes the insert.
func (b *Buffer) Undo() (Pos, bool) {
	b.CloseUndoBlock()
	if b.undoCur.parent == nil {
		return Pos{}, false
	}
	n := b.undoCur
	p := b.applyNode(n)
	b.undoCur = n.parent
	b.undoCur.last = n
	return p, true
}

// Redo puts back the step "u" took, "C-r", and returns the cursor position.
//
// The branch it follows is the most recently created child, which is the one
// "u" just came from in the ordinary case and the newest edit in the case where
// you undid, typed something else, and undid that too.
func (b *Buffer) Redo() (Pos, bool) {
	if b.undoOpen != nil {
		// An open block means a change has been made since the last sync,
		// and in vim that is b_u_curhead == NULL: there is nothing to redo
		// and u_redo() does not sync on its way to finding that out. It
		// matters under "-s", where nothing else syncs either: "x CTRL-R d_"
		// leaves undotree().seq_cur at 1 in vim, and a redo that closed the
		// block first left it at 2.
		return Pos{}, false
	}
	n := b.undoCur.last
	if n == nil {
		return Pos{}, false
	}
	p := b.applyNode(n)
	b.undoCur = n
	return p, true
}

// Older moves to the state one sequence number back, "g-", and returns the
// cursor position. Unlike "u" it ignores the shape of the tree and walks
// chronologically, so it crosses from one branch to another.
//
// The open block is closed before the current sequence number is read, not
// after. gotoSeq closes it too, but by then the argument has been evaluated
// against the state before the pending change and the target is one too low:
// "g-" straight after typing would do nothing and "g+" would walk backwards and
// report success. Vim's undo_time() calls u_sync() first for the same reason.
func (b *Buffer) Older() (Pos, bool) {
	b.CloseUndoBlock()
	return b.gotoSeq(b.undoCur.seq - 1)
}

// Newer moves to the state one sequence number forward, "g+". It closes the
// open block before reading the sequence number, as Older does and for the
// reason given there.
func (b *Buffer) Newer() (Pos, bool) {
	b.CloseUndoBlock()
	return b.gotoSeq(b.undoCur.seq + 1)
}

// GotoSeq moves to the state with the given sequence number, vim's ":undo N".
// Sequence 0 is the file as it was read.
func (b *Buffer) GotoSeq(seq int) (Pos, bool) { return b.gotoSeq(seq) }

func (b *Buffer) gotoSeq(seq int) (Pos, bool) {
	b.CloseUndoBlock()
	if seq < 0 || seq > b.undoSeqMax || seq == b.undoCur.seq {
		return Pos{}, false
	}
	target := b.undoBySeq[seq]

	// Walk up from both ends to the common ancestor, then undo down one side
	// and redo up the other. For a straight line of edits, which is almost
	// every case, one of the two halves is empty.
	up := map[*undoNode]bool{}
	for n := b.undoCur; n != nil; n = n.parent {
		up[n] = true
	}
	var meet *undoNode
	var down []*undoNode
	for n := target; n != nil; n = n.parent {
		if up[n] {
			meet = n
			break
		}
		down = append(down, n)
	}

	var p Pos
	ok := false
	for n := b.undoCur; n != meet; n = n.parent {
		p = b.applyNode(n)
		ok = true
		n.parent.last = n
	}
	for i := len(down) - 1; i >= 0; i-- {
		p = b.applyNode(down[i])
		ok = true
	}
	b.undoCur = target
	return p, ok
}

// applyNode swaps the node's saved text with what is in the buffer and returns
// the cursor position to restore. Applying the same node again undoes the swap,
// which is what makes undo and redo one function.
//
// The cursor is worked out entry by entry as the entries are applied, not
// afterwards, because vim's u_undoredo() compares each entry's saved lines
// against what is in the buffer at that moment. See cursorFor.
func (b *Buffer) applyNode(n *undoNode) Pos {
	if len(n.entries) == 0 {
		// A header that saved undo and then changed nothing: "X" in column
		// one, "d0" in column one, "~" on a digit. vim still walks it, still
		// puts the cursor back where the command opened it, and still reports
		// a change on that line in column zero. Measured: "3GXu" on a
		// three-line file leaves the cursor on line 3 and the changelist
		// holding line 3 and nothing else.
		at := Pos{Line: clampLine(n.cursor.Line, len(b.lines))}
		// One line, not none: vim's u_save stored the line whether or not the
		// command went on to change it, so the undo of an "X" in column one
		// counts one and says "1 change". Unless nothing was stored at all,
		// which is do_put's u_save over an empty range: "p" with nothing
		// yanked leaves a header that undoes to "0 changes" and adds no
		// changelist entry.
		b.undoLines = 1
		if n.noLines {
			b.undoLines = 0
		} else {
			b.noteChange(at, at)
		}
		b.noEOL, n.noEOL = n.noEOL, b.noEOL
		b.emptied, n.emptied = n.emptied, b.emptied
		b.newChange = true
		// The cursor comes back whole, column and all: "$~u" on a line ending
		// in a digit leaves the cursor on that digit, where the changelist
		// entry for the same undo is in column zero.
		at.Col = n.cursor.Col
		return at
	}
	c := cursorWalk{saved: n.cursor, newlnum: noLine, cursor: Pos{Line: 1, Col: 0}}
	// vim's u_newcount, summed over the entries as they go in. It is what the
	// undo message counts when the line count did not change: ":%s/a/X/g" over
	// six lines puts six lines back and says "6 changes".
	b.undoLines = 0
	var prev *undoEntry

	// '[ '] and '. are recomputed here rather than left to adjustMarks, which
	// drops any mark sitting on the lines an undo replaces and would leave the
	// three of them unset after undoing an insertion. Vim's u_undoredo() widens
	// b_op_start and b_op_end over every entry as it walks them, and this is
	// the same walk with the same starting values.
	opStart, opEnd, lastChange := len(b.lines), 0, 1

	for i := range n.entries {
		e := &n.entries[i]
		c.entry(b, e, i == len(n.entries)-1)

		if !sameSingleLine(&n.entries[i], prev) {
			b.undoLines += changedLines(e.lines, b.lines[e.first-1:e.first-1+e.replaced])
		}
		prev = &n.entries[i]

		now := b.lines[e.first-1 : e.first-1+e.replaced]
		held := make([][]byte, len(now))
		copy(held, now)
		b.splice(e.first, e.replaced, e.lines)
		b.adjustMarks(e.first, held, e.lines)

		top, oldsize, newsize := opBounds(e.first, held, e.lines)
		if oldsize != newsize {
			if opStart > top+oldsize {
				opStart += newsize - oldsize
			}
			if opEnd > top+oldsize {
				opEnd += newsize - oldsize
			}
		}
		if top+1 < opStart {
			opStart = top + 1
		}
		if newsize == 0 && top+1 > opEnd {
			opEnd = top + 1
		} else if top+newsize > opEnd {
			opEnd = top + newsize
		}
		lastChange = top + 1

		e.lines, e.replaced = held, len(e.lines)
	}
	reverse(n.entries)

	// '[ and '] are held inside the buffer, '. is not: vim clamps the first
	// two at the end of u_undoredo() and leaves b_last_change where it landed,
	// so getpos("'.") after undoing an append at the end of the file reports a
	// line that is no longer there. Callers clamp what they use.
	b.marks[MarkChangeStart] = Pos{Line: clampLine(opStart, len(b.lines))}
	b.marks[MarkChangeEnd] = Pos{Line: clampLine(opEnd, len(b.lines))}
	// u_undoredo ends in changed_lines(), so an undo is a change as far as the
	// changelist and '. are concerned, and it lands on the line it put back in
	// column zero: undoing an append at column 3 moves the entry from column 3
	// to column 0 and redoing it leaves it there. Unclamped, for the reason
	// above: vim leaves b_last_change where it landed.
	// One exception to leaving it where it landed: a buffer that came back
	// out of the undo flagged ML_EMPTY has one line and vim puts the change
	// on it. This walk counts the empty line as a real one and lands on line
	// two, which is a line that does not exist even in vim's terms. Measured
	// on an empty file, "i<CR><Esc>u" and "O<Esc>u" both leave the changelist
	// on line 1; the same keys over a file with one line in it leave it on
	// line 1 in both editors without this.
	// n.emptied and not b.emptied: the flag is swapped a few lines below, so
	// the node still holds the one this undo is restoring.
	if n.emptied && lastChange > 1 {
		lastChange = 1
	}
	b.marks[MarkLastChange] = Pos{Line: lastChange}
	b.ChangedAt(Pos{Line: lastChange})

	b.noEOL, n.noEOL = n.noEOL, b.noEOL
	b.emptied, n.emptied = n.emptied, b.emptied
	b.newChange = true

	cursor := c.finish()
	if cursor.Line > len(b.lines) {
		cursor = Pos{Line: len(b.lines), Col: 0}
	}
	return cursor
}

// noLine stands in for vim's MAXLNUM: no line has been chosen yet.
const noLine = -1

// cursorWalk works out where vim would leave the cursor after an undo or redo.
//
// This is the one piece of vim behaviour in this package that cannot be guessed
// and it was checked against vim 9.2 rather than reasoned about. The short
// version, which is what: "u" puts the cursor at the START of the
// changed text, not where it was when the change was made. "3G11ldb" deletes
// backwards from column 12 to column 7 and "u" leaves the cursor at column 7,
// not 12.
//
// The long version, which is what is implemented, is vim's u_undoredo(). Vim
// saves the cursor into the undo header at the moment the change begins, and by
// then every operator has already moved it to the start of its range, which is
// where the "start of the changed text" behaviour actually comes from. Two
// cases fall out of that:
//
// - the saved cursor is inside the block being restored: use it whole, line
// and column. This is the common case and covers dd, dw, x, i, a, o and J.
// - it is not, because the change happened somewhere the cursor was not, as
// ":2,3s/x/y/" typed from line 6 does: use the first line whose text
// actually differs, at column 0. Column 0 and not the first non-blank,
// because vim calls beginline(BL_SOL|BL_FIX) and 'startofline' is on.
//
// The column comes back unclamped. Normal mode may not sit past the last byte
// of a line and insert mode may, so the clamp belongs to whoever knows which
// mode is running; vim does it in check_cursor_col() for the same reason. That
// is why "2GA" on a three-byte line reports column 3 from vim and column 4 from
// here: column 4 is where the change started and vim clamps it on the way to
// the screen.
type cursorWalk struct {
	saved   Pos // the node's remembered cursor, vim's uh_cursor
	newlnum int
	cursor  Pos
}

// entry folds one undo entry into the decision, before that entry is applied.
func (c *cursorWalk) entry(b *Buffer, e *undoEntry, last bool) {
	top := e.first - 1      // the line above the changed block
	newsize := len(e.lines) // lines there once this entry is applied
	oldsize := e.replaced   // lines there now

	if c.newlnum != noLine && top >= c.newlnum {
		return
	}
	if c.saved.Line >= top && c.saved.Line <= top+newsize+1 {
		c.cursor = c.saved
		c.newlnum = c.cursor.Line - 1
		return
	}
	// The cursor was somewhere else entirely. Find the first line whose content
	// actually changes, which is what stops an undo of a reformat from landing
	// on the untouched line above it.
	k := 0
	for k < newsize && k < oldsize {
		if !bytes.Equal(e.lines[k], b.lines[top+k]) {
			break
		}
		k++
	}
	switch {
	case k == newsize && c.newlnum == noLine && last:
		c.newlnum = top
		c.cursor.Line = top + 1
	case k < newsize:
		c.newlnum = top + k
		c.cursor.Line = c.newlnum + 1
	}
}

// finish applies the two adjustments vim makes once every entry is in.
func (c *cursorWalk) finish() Pos {
	if c.saved.Line+1 == c.cursor.Line && c.cursor.Line > 1 {
		c.cursor.Line--
	}
	if c.saved.Line == c.cursor.Line {
		c.cursor.Col = c.saved.Col
	} else {
		c.cursor.Col = 0
	}
	if c.cursor.Line < 1 {
		c.cursor.Line = 1
	}
	return c.cursor
}

func reverse(e []undoEntry) {
	for i, j := 0, len(e)-1; i < j; i, j = i+1, j-1 {
		e[i], e[j] = e[j], e[i]
	}
}

// opBounds restates one entry the way vim's undo header states it: the line
// above the change, how many lines are there now, and how many go back.
//
// The lines the two sides have in common at each end are peeled off first, the
// same peeling and for the same reason as adjustMarks. A buffer with one
// mutation path records "yy p" on line 1 as lines 2 and 3 replaced by line 3
// alone, and vim records it as one line inserted after line 1; peel both ends
// and the two agree, which is what puts '[ and '] on the same line vim puts
// them on. Without it they land a line off in one direction or the other
// depending on which end of the file the change was at.
//
// One shape this cannot get right: a line inserted next to an identical line.
// Nothing in the entry says which of the two copies was typed, vim knows
// because its undo header came from the operation rather than from the text,
// and '. ends up on the lower one. '[ and '] are unaffected.
func opBounds(first int, old, repl [][]byte) (top, oldsize, newsize int) {
	// An entry that puts back exactly what is there is not peeled at all.
	// vim's undo header comes from the operation and its ue_top is the line
	// above the entry however the text turned out, and the shift that moved
	// no bytes is a real entry: "<<" on a buffer of one empty line, undone,
	// leaves vim's changelist on line 1 where peeling puts it on line 2, a
	// line the buffer does not have.
	if sameLines(old, repl) {
		return first - 1, len(old), len(repl)
	}
	lead := 0
	for lead < len(old) && lead < len(repl) && string(old[lead]) == string(repl[lead]) {
		lead++
	}
	old, repl = old[lead:], repl[lead:]
	first += lead

	for len(old) > 0 && len(repl) > 0 &&
		string(old[len(old)-1]) == string(repl[len(repl)-1]) {
		old, repl = old[:len(old)-1], repl[:len(repl)-1]
	}
	return first - 1, len(old), len(repl)
}

// clampLine holds a line number inside a buffer that always has at least one
// line, which is what vim does to '[ and '] at the end of an undo.
func clampLine(lnum, count int) int {
	if lnum > count {
		lnum = count
	}
	if lnum < 1 {
		lnum = 1
	}
	return lnum
}

// sameLines reports whether two runs of lines hold the same bytes.
func sameLines(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

// nowFunc is the clock the undo tree stamps steps with. It is a variable so a
// test can pin it: every other clock read in this tree would otherwise make a
// serialised tree differ from itself between two runs.
var nowFunc = time.Now
