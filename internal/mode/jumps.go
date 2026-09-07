package mode

import "github.com/pkar/pvim/internal/text"

// The jumplist: where the cursor was before each jump, walked backwards with
// CTRL-O and forwards with CTRL-I.
//
// It is not the same thing as the ' mark, and that is the first thing to get
// right. The ' mark is one position, vim's w_pcmark, and it is what '' and ``
// go to; the jumplist is a hundred of them with a cursor into the middle. Both
// are written by the same call -- vim's setpcmark(), which is Editor.setPCMark
// here -- and only one of them is ever taken back: checkpcmark() hands the '
// mark to whatever set it last when a jump did not move the cursor, and does
// not touch the list. Measured, "100GyG" on 200 lines leaves '' on line 1 and
// leaves line 100 in the jumplist, which is exactly that asymmetry.
//
// Three rules were measured against vim 9.2 rather than read out of the help,
// because the help describes the effect and not the mechanism:
//
// - The list is NOT deduplicated when an entry is pushed. It is deduplicated
// when it is READ, by :jumps, by getjumplist() and by CTRL-O and CTRL-I on
// their way in, keeping the LAST entry of each line and moving the index
// along with it. This is vim's cleanup_jumplist(). "100G50G100G50G" leaves
// four entries in the array and reports three, in the order 1, 50, 100.
// - A jump from the middle of the list does not truncate it. The entries
// after the index stay where they are and the new one is appended past
// them, which is why the dedup exists at all. 'jumpoptions' "stack" is the
// other behaviour and is not implemented; the option is not in
// internal/options and vim's default is empty.
// - The first CTRL-O or CTRL-I after a jump pushes the cursor position so
// that there is something to come back to, and it does that INSIDE the
// range check, so a count that overshoots beeps and pushes nothing.
// Measured: "100G50G9<C-O>" leaves the cursor on 50 and the list at two
// entries, where "100G50G<C-O>" leaves it at three.
//
// The buffer is opened with one entry already in it. That is not an invention:
// vim's do_ecmd() calls setpcmark() as it starts editing a file, so a file
// opened and never jumped in has a jumplist of one line-1 entry, and CTRL-O
// pressed as the very first key moves nowhere and leaves the index at 0 rather
// than beeping. Measured on a fresh "vim --clean file" with a single "j" typed.

// jumpListMax is how many entries the list holds. It is JUMPLISTSIZE in vim's
// C, a compile-time 100 and not an option.
const jumpListMax = 100

// Jump is one entry: a position and the file it is in.
//
// The file is carried even though this editor has one buffer, because the
// entry outlives the buffer twice over: the persistence in internal/undofile
// stores a file with each jump, and the day there is a second buffer an entry
// naming no file is an entry that cannot be jumped to.
type Jump struct {
	File string
	Pos  text.Pos
}

// pushJump is the jumplist half of vim's setpcmark(): the position is appended
// and the index moves past the end of the list.
//
// Nothing is deduplicated here and nothing after the index is dropped. Both of
// those are cleanupJumps' job and both happen later; see the note above.
func (e *Editor) pushJump(p text.Pos) {
	e.jumps = append(e.jumps, Jump{File: e.name, Pos: p})
	if len(e.jumps) > jumpListMax {
		// The oldest goes, not the newest. vim shifts the array down by one
		// and keeps the length at JUMPLISTSIZE.
		e.jumps = append(e.jumps[:0], e.jumps[len(e.jumps)-jumpListMax:]...)
	}
	e.jumpIdx = len(e.jumps)
}

// cleanupJumps is vim's cleanup_jumplist(): every entry that another entry
// later in the list repeats is dropped, and the index follows whatever it was
// pointing at.
//
// Two entries repeat when they are in the same file and on the same LINE. The
// column is not compared, which is why "100G0" then "100G$" then a jump away
// leaves one entry and not two.
//
// The index has to be walked across rather than recomputed, because it can be
// pointing at an entry that is about to be dropped: it lands on the entry that
// took its place. An index sitting past the end of the list stays past the end
// of the shorter one.
func (e *Editor) cleanupJumps() {
	to := 0
	for from := range e.jumps {
		if e.jumpIdx == from {
			e.jumpIdx = to
		}
		dup := false
		for i := from + 1; i < len(e.jumps); i++ {
			if e.jumps[i].File == e.jumps[from].File &&
				e.jumps[i].Pos.Line == e.jumps[from].Pos.Line {
				dup = true
				break
			}
		}
		if !dup {
			e.jumps[to] = e.jumps[from]
			to++
		}
	}
	if e.jumpIdx == len(e.jumps) {
		e.jumpIdx = to
	}
	e.jumps = e.jumps[:to]
}

// moveMark is vim's movemark(): walk count entries through the list and
// answer where that lands, or false when it lands outside it.
//
// count is negative for CTRL-O and positive for CTRL-I. The order of the two
// tests is vim's and it is load-bearing: the range is checked against the list
// as it stands, and only then does the first walk after a jump push the
// cursor. A CTRL-O with a count bigger than the list therefore leaves the list
// alone, where a CTRL-O that will succeed grows it by one.
func (e *Editor) moveMark(count int) (text.Pos, bool) {
	e.cleanupJumps()
	if len(e.jumps) == 0 {
		return text.Pos{}, false
	}
	if e.jumpIdx+count < 0 || e.jumpIdx+count >= len(e.jumps) {
		return text.Pos{}, false
	}
	if e.jumpIdx == len(e.jumps) {
		// The first CTRL-O or CTRL-I after a jump: where the cursor is now
		// goes on the end so that the walk can come back to it, and the index
		// steps over the entry that was just made. This is a whole setpcmark
		// and not a bare push, so it moves the ' mark too.
		e.setPCMark(e.cur)
		e.jumpIdx--
		if e.jumpIdx+count < 0 {
			return text.Pos{}, false
		}
	}
	e.jumpIdx += count
	return e.jumps[e.jumpIdx].Pos, true
}

// jumpOlder is CTRL-O and jumpNewer is CTRL-I, which is the same key as <Tab>.
//
// Neither is a motion; :help jump-motions says so in as many words, and vim's
// nv_pcmark starts with checkclearopq(), so "d<C-O>" beeps and deletes
// nothing. Neither sets the ' mark for itself either -- nv_cursormark only
// does that for ' ` [ and ] -- so the only ' mark either of them writes is the
// one moveMark makes on the first walk.
func (e *Editor) jumpOlder(count int) error { return e.jumpBy(-count) }

// jumpNewer is CTRL-I and <Tab>.
func (e *Editor) jumpNewer(count int) error { return e.jumpBy(count) }

// jumpBy is the shared half: walk, move, or beep at the end of the list.
func (e *Editor) jumpBy(count int) error {
	p, ok := e.moveMark(count)
	if !ok {
		return e.beep()
	}
	e.moveTo(p)
	e.finish(false)
	return nil
}

// JumpList is the jumplist as :jumps and getjumplist() see it: the entries
// oldest first, and the index of the one the next CTRL-O walks back from.
//
// It cleans the list up before answering, because both of vim's readers do:
// ex_jumps() and f_getjumplist() each call cleanup_jumplist() first, so the
// duplicates a caller would otherwise have to filter are already gone. The
// index equals the length when nothing has walked back yet, which is the ">"
// on a line of its own that ":jumps" prints under the last entry.
//
// The slice is a copy; the caller is welcome to it.
func (e *Editor) JumpList() ([]Jump, int) {
	e.cleanupJumps()
	return append([]Jump(nil), e.jumps...), e.jumpIdx
}

// SetJumpList replaces the list, which is how a jumplist read back out of the
// history file gets into a window. The index is clamped into the list rather
// than trusted: a file written by an older pvim, or edited by hand, must not
// be able to index past the end of the slice.
func (e *Editor) SetJumpList(list []Jump, idx int) {
	if len(list) > jumpListMax {
		list = list[len(list)-jumpListMax:]
	}
	e.jumps = append([]Jump(nil), list...)
	e.jumpIdx = min(max(idx, 0), len(e.jumps))
}

// ClearJumps empties the list, which is ":clearjumps".
func (e *Editor) ClearJumps() {
	e.jumps, e.jumpIdx = nil, 0
}

// jumpTracker is how the buffer moves the jumplist when the text moves.
//
// It is a type of its own rather than a method on Editor because
// text.Tracker's one method would then be exported off the editor, and
// "AdjustLines" is not something a frontend has any business calling.
type jumpTracker struct{ e *Editor }

// AdjustLines is text.Tracker: every entry goes through the mover, and no
// entry is dropped, which is vim's one_adjust_nodel. An entry whose line was
// deleted is pinned to the top of the hole and stays in the list, so a CTRL-O
// after a "dd" over the place it pointed at still goes somewhere.
func (j jumpTracker) AdjustLines(move func(text.Pos) text.Pos) {
	for i := range j.e.jumps {
		j.e.jumps[i].Pos = move(j.e.jumps[i].Pos)
	}
}
