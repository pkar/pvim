package text

// Marks are named positions that follow the text as it moves. Vim keeps
// twenty-six per buffer for you to set with "m", and a handful it sets itself.
//
// The half that matters is not storing them, it is moving them. Delete the line
// above a mark and the mark has to come up one; delete the line the mark is on
// and the mark is gone, not silently pointing at whatever moved into its place.
// Vim's mark_adjust() is where that lives and adjustMarks below is the same
// rules for this buffer's one mutation path.
//
// A-Z are here, and they are the same thing as a-z until there is more than one
// buffer. In vim an uppercase mark names a file as well as a position, so 'A
// from another buffer switches to the one A was set in; with one buffer that
// distinction has nothing to select between, and the position half is all of
// the behaviour. What is still missing is the file half and the jumplist, and
// both wait on there being a second buffer to jump to.
//
// The measurement that made this worth doing: on "alpha/beta/gamma",
// "jmAgg'A" puts vim on line 2 and said nothing, and 'P on a buffer where P was
// never set is "E20: Mark not set" and not "E78: Unknown mark", which is what
// pvim printed for every uppercase name while they were refused outright.

// Automatic mark names. They are bytes because that is how they arrive from the
// keyboard and from an ex range, and a named constant beats a quoted character
// at a call site three packages away.
const (
	// MarkChangeStart is '[, the first byte of the last change.
	MarkChangeStart = '['
	// MarkChangeEnd is '], the last byte of the last change.
	MarkChangeEnd = ']'
	// MarkLastChange is '., where the last change was made.
	MarkLastChange = '.'
	// MarkLastInsert is '^, where insert mode last stopped. Nothing in this
	// package sets it: the mode layer does, on leaving insert.
	MarkLastInsert = '^'
	// MarkLastExit is '", where the cursor was when this file was last left.
	// There is no viminfo yet, so it is the position a freshly read buffer
	// starts at, which is what vim reports for a file it has never seen
	// before: ":marks" on an untouched buffer lists it at line 1.
	MarkLastExit = '"'
	// MarkLastJump is '', the position before the latest jump. Nothing in this
	// package sets it either, for the same reason.
	MarkLastJump = '\''
)

// validMark reports whether name is a mark this buffer stores.
func validMark(name byte) bool {
	switch {
	case name >= 'a' && name <= 'z', name >= 'A' && name <= 'Z':
		return true
	case name == MarkChangeStart, name == MarkChangeEnd, name == MarkLastChange,
		name == MarkLastInsert, name == MarkLastJump, name == MarkLastExit:
		return true
	}
	return false
}

// Mark returns the position of a mark and whether it is set. An unset or
// deleted mark reports false, which is the E20 the caller has to print.
func (b *Buffer) Mark(name byte) (Pos, bool) {
	p, ok := b.marks[name]
	return p, ok
}

// SetMark puts a mark at p. An unknown name is ignored rather than an error,
// because the mode layer validates the key and this is the second line of
// defence, not the first.
func (b *Buffer) SetMark(name byte, p Pos) {
	if !validMark(name) {
		return
	}
	b.marks[name] = p
}

// ClearMark removes a mark, as ":delmarks" does.
func (b *Buffer) ClearMark(name byte) {
	delete(b.marks, name)
}

// adjustMarks moves the marks over a replacement of the lines starting at first
// with repl, and sets the marks that follow a change.
//
// The identical lines at each end of the replacement are peeled off before
// anything moves, and that peeling is the whole trick. "O" on line 4 arrives
// here as line 4 replaced by two lines whose second is line 4 again; peel the
// common tail and it is what it really is, one line inserted before line 4, so
// a mark on line 4 moves down to 5. Without the peeling it would look like an
// in-place change plus an append, and the mark would stay on 4 pointing at the
// newly typed line. Vim gets this for free because its "O" calls ml_append
// directly; a buffer with one mutation path has to work it out.
func (b *Buffer) adjustMarks(first int, old, repl [][]byte) {
	// Where new lines go in, before any peeling: vim appends them after the
	// line the change started on, so a mark on that line never moves. The
	// peeling below cannot see that. Splitting a line in column zero -- "i"
	// then CR on the first character -- leaves the same before-and-after as
	// opening a line above it, and vim moves a mark on line one for the
	// second and not for the first. Measured both ways.
	after := first
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

	switch o, n := len(old), len(repl); {
	case o == n:
		// Lines replaced where they stand. Vim's ml_replace does not move a
		// mark and neither does this: a mark inside a line that :s rewrote
		// keeps its column, wrong or not, exactly as vim leaves it.
	case o > n:
		b.markLinesDeleted(first+n, first+o-1)
	default:
		b.markLinesInserted(max(first+o, after+1), n-o)
	}
}

// Tracker is a list of buffer positions kept outside this package that has to
// move as the text moves.
//
// The jumplist is the one that exists. It belongs to a WINDOW and not to a
// buffer, so internal/mode holds it, and vim moves it anyway: mark_adjust()
// walks every window's jumplist alongside the buffer's own marks, which is why
// ":help jumplist" says the line numbers are adjusted for deleted and inserted
// lines. Measured: "8G2Gdd<C-O>" lands on line 7 in vim, and on line 8 in an
// editor whose list did not move.
type Tracker interface {
	// AdjustLines rewrites every position in the list through move, in
	// place. The rule the mover implements is vim's one_adjust_nodel: an
	// entry on a line that went away is kept and pinned to the top of the
	// hole rather than dropped, which is how the changelist below behaves
	// and is not how a named mark behaves.
	AdjustLines(move func(Pos) Pos)
}

// Track registers the list of positions this buffer is to keep in step.
//
// One slot, and a second call replaces the first. That is not a limit anybody
// has hit: cmd/pvim builds one mode.Editor at a time and builds a new one when
// it swaps buffers, so a registry would grow an entry per swap and pin every
// editor that had ever been current. The day two windows on one buffer each
// have their own machine, this becomes a list and the day it does the two
// jumplists start behaving like vim's; until then the current editor is the
// only one whose list anybody can see.
func (b *Buffer) Track(t Tracker) { b.tracked = t }

// adjustTracked runs the mover over the tracked list, if there is one.
func (b *Buffer) adjustTracked(move func(Pos) Pos) {
	if b.tracked != nil {
		b.tracked.AdjustLines(move)
	}
}

// markLinesDeleted drops the marks on lines first..last and pulls the marks
// below them up.
func (b *Buffer) markLinesDeleted(first, last int) {
	count := last - first + 1
	for name, p := range b.marks {
		switch {
		case p.Line >= first && p.Line <= last:
			delete(b.marks, name)
		case p.Line > last:
			p.Line -= count
			b.marks[name] = p
		}
	}
	b.adjustTracked(func(p Pos) Pos {
		switch {
		case p.Line >= first && p.Line <= last:
			return Pos{Line: first, Col: 0}
		case p.Line > last:
			p.Line -= count
		}
		return p
	})
	// The changelist moves with the text too, and it moves by vim's other
	// rule: mark_adjust walks b_changelist with one_adjust_nodel, which keeps
	// an entry on a deleted line instead of dropping it and pins it to the
	// first line of the hole. That is why "AX <C-U> Esc O" leaves two entries
	// in vim -- the first one, on what was line 1, is on line 2 by the time
	// the O has finished -- where a changelist that did not move left one.
	for i := range b.changes {
		switch {
		case b.changes[i].Line >= first && b.changes[i].Line <= last:
			b.changes[i].Line = first
			b.changes[i].Col = 0
		case b.changes[i].Line > last:
			b.changes[i].Line -= count
		}
	}
}

// markLinesInserted pushes the marks at or below line at down by count, and
// the changelist with them.
func (b *Buffer) markLinesInserted(at, count int) {
	for name, p := range b.marks {
		if p.Line >= at {
			p.Line += count
			b.marks[name] = p
		}
	}
	b.adjustTracked(func(p Pos) Pos {
		if p.Line >= at {
			p.Line += count
		}
		return p
	})
	for i := range b.changes {
		if b.changes[i].Line >= at {
			b.changes[i].Line += count
		}
	}
}

// changeListMax is how many entries vim keeps, and it is a fixed 100 in the C
// rather than an option.
const changeListMax = 100

// noteChange records a change in the marks and the changelist. start is the
// first byte the change touched and end the position just after the text it
// left behind.
func (b *Buffer) noteChange(start, end Pos) {
	// '] is the last byte of the new text, not the one after it. A change that
	// inserted nothing leaves both marks on the same byte, which is what vim
	// reports after "dd".
	last := end
	if last != start {
		switch {
		case last.Col > 0:
			last.Col--
		case last.Line > 1:
			last.Line--
			last.Col = 0
			if n := len(b.lines[last.Line-1]); n > 0 {
				last.Col = n - 1
			}
		}
	}
	b.marks[MarkChangeStart] = start
	b.marks[MarkChangeEnd] = last
	b.marks[MarkLastChange] = start

	// One changelist entry per undo block, updated in place while the block
	// stays open. That is vim's rule via b_new_change and it is why three "x"
	// commands run through "vim -s", which never syncs undo, leave a changelist
	// with exactly one entry in it.
	//
	// A new block does not always get a new entry either. vim refuses one for a
	// change on the same line and within a screen's width of the last, so that
	// typing "xxxxx" with an undo sync between each does not fill the list:
	// CTRL-U in insert mode does sync, and the X typed after it still shares
	// the entry the typing before it made.
	at := start
	if b.noteLine > 0 {
		at = Pos{Line: b.noteLine}
		b.noteLine = 0
	}
	b.noteChangeAt(at)
}

// noteChangeAt is the changelist half of noteChange: one entry per undo block,
// a new one when the block is new and the position is far enough from the last.
func (b *Buffer) noteChangeAt(p Pos) {
	if b.newChange || len(b.changes) == 0 {
		if b.newEntryFor(p) {
			if len(b.changes) == changeListMax {
				copy(b.changes, b.changes[1:])
				b.changes = b.changes[:changeListMax-1]
			}
			b.changes = append(b.changes, p)
			// Only an entry that was actually added spends the flag. A new
			// undo block whose first change lands next to the entry the last
			// one left gets no entry of its own AND stays owed one, so the
			// next change in that same block, if it is far enough away, gets
			// it instead. Measured: "1Gx A CTRL-U Esc 3Gx" leaves two entries
			// in vim, at line 1 and line 3, where clearing the flag on the
			// near change leaves only the one at line 3.
			b.newChange = false
		}
	}
	b.changes[len(b.changes)-1] = p
	b.changeIdx = len(b.changes)
}

// newEntryFor is vim's "add" test in changed_common: a change that starts a new
// undo-able change gets its own changelist entry when it is on a different line
// from the last one, or on the same line but further from it than a line of
// text. The distance is 'textwidth' when it is set and 79 when it is not, which
// is comp_textwidth(FALSE) with the fallback its one caller applies.
func (b *Buffer) newEntryFor(p Pos) bool {
	if len(b.changes) == 0 {
		return true
	}
	last := b.changes[len(b.changes)-1]
	if last.Line != p.Line {
		return true
	}
	cols := b.textWidth
	if cols <= 0 {
		cols = 79
	}
	return last.Col+cols < p.Col || p.Col+cols < last.Col
}

// SetTextWidth tells the buffer 'textwidth', which it needs for one rule and
// one only: how far apart two changes to the same line have to be before the
// changelist gives the second one an entry of its own. Everything else that
// depends on the option is above this package.
func (b *Buffer) SetTextWidth(n int) { b.textWidth = n }

// ChangedAt moves the position the last change reported to the one a
// line-oriented command reports instead.
//
// vim has two of these. changed_bytes(lnum, col) is what an edit inside a line
// reports and it is what Replace already records; changed_lines(lnum, ...) is
// what a command that added, removed or rewrote whole lines reports, and its
// column is always zero and its line is the first one the command touched
// rather than the first byte that moved. "dd" on the last of three lines
// splices from the end of line 2 and reports line 3; "J" rewrites line 1 and
// reports line 2, because the last thing a join does is delete the line it
// joined. Neither is derivable from the byte range, so the caller says.
//
// It moves the '. mark with the entry, because in vim both come off the same
// changed_common() call.
// It also makes the entry when the buffer has none. A command can report a
// change it did not make: vim's do_join clamped down to one line rewrites that
// line with itself, and its changed_bytes() lands on the changelist exactly as
// a real join would. Measured: "3G3J" on a three-line file reports line 3
// column 3 over a buffer nothing touched.
func (b *Buffer) ChangedAt(p Pos) {
	b.marks[MarkLastChange] = p
	b.noteChangeAt(p)
}

// ChangeList returns the positions of the recent changes, oldest first. The
// slice is a copy; the caller is welcome to it.
func (b *Buffer) ChangeList() []Pos {
	return append([]Pos(nil), b.changes...)
}

// ChangeOlder walks back through the changelist, "g;", and returns the position
// to jump to. It reports false at the far end, which is vim's E662.
func (b *Buffer) ChangeOlder() (Pos, bool) {
	if b.changeIdx <= 0 {
		return Pos{}, false
	}
	b.changeIdx--
	return b.changes[b.changeIdx], true
}

// ChangeNewer walks forward through the changelist, "g,", and returns the
// position. It reports false at the near end, vim's E663.
func (b *Buffer) ChangeNewer() (Pos, bool) {
	if b.changeIdx >= len(b.changes)-1 {
		return Pos{}, false
	}
	b.changeIdx++
	return b.changes[b.changeIdx], true
}

// JoinMarks moves the marks and the changelist entries on a line that a join is
// about to fold into the one above it. Vim's mark_col_adjust(), which do_join()
// calls once per line it swallows and before it rewrites anything.
//
// A join is the one edit where a mark does not simply die with its line or
// shift by a whole number of lines: the text it was pointing at is still there,
// somewhere along a line that is now longer. The three numbers say where.
// textStart is the byte column the line's text lands at in the joined line,
// stripped is how many bytes of leading white space J took off it (zero for
// gJ), and gap is how many spaces J put in front of it.
//
// A mark inside the white space that was removed has nothing left to point at,
// and vim does not put it where the text went: it puts it on the last space of
// the gap, which is textStart-gap. Measured on "aaa" over " bbb", where the
// marks in columns 1, 2 and 3 all come out in column 4 of "aaa bbb" and the
// mark on the b comes out in column 5.
//
// Call it before the lines are spliced, while the mark is still on the line
// this is being told about.
func (b *Buffer) JoinMarks(line, lineAmount, textStart, stripped, gap int) {
	move := func(p Pos) Pos {
		p.Line += lineAmount
		if p.Col < stripped-gap {
			p.Col = textStart - gap
		} else {
			p.Col += textStart - stripped
		}
		if p.Col < 0 {
			p.Col = 0
		}
		return p
	}
	for name, p := range b.marks {
		if p.Line == line {
			b.marks[name] = move(p)
		}
	}
	for i, p := range b.changes {
		if p.Line == line {
			b.changes[i] = move(p)
		}
	}
	b.adjustTracked(func(p Pos) Pos {
		if p.Line == line {
			return move(p)
		}
		return p
	})
}
