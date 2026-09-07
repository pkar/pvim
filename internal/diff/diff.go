// Package diff is vim's diff mode: the line diff behind it, where the filler
// lines go, which lines are highlighted how, where ]c and [c land, and what
// do and dp move.
//
// It is a leaf. Nothing in this module is imported here, and nothing here
// knows what a buffer, a window or a Grid is: a caller hands over two slices
// of lines and gets back line numbers and byte ranges. That is what lets the
// whole of it be measured against `vim -d`, which is the oracle for it and a
// strong one -- diff mode is vim's own, so diff_filler() and
// diff_hlID() answer every question this package asks, per line and per
// column, for any input a test cares to build.
//
// What was measured against vim 9.2.0321, and where:
//
// - filler placement, per line, through diff_filler(). See Pair.Filler.
// - which lines are DiffAdd, DiffChange and DiffText, per column, through
// diff_hlID(). See Pair.Kind and Pair.TextSpan.
// - where ]c and [c land, including the counts and the two ends. See
// Pair.Next and Pair.Prev.
// - which block do and dp act on from each cursor line. See Pair.BlockAt.
// - what 'iwhite' calls equal. See normalize.
//
// internal/diff/vim_test.go is that measurement, run as a differential test
// against the vim on the box, and it is skipped rather than failed where
// there is no vim.
package diff

import "bytes"

// Side names one of the two buffers in a diff pair. There are exactly two:
// vim allows up to eight windows in one diff, and this editor's diff mode is
// ":Gdiff" and "vim -d a b", both of which are pairs. A third window would be
// a different data structure and not a wider index, so the type is closed
// rather than an int nobody bounds-checks.
type Side int

// The two sides. A is the left window in a vertical split, which for ":Gdiff"
// is the file as the index has it.
const (
	A Side = 0
	B Side = 1
)

// Other is the side this one is being compared against.
func (s Side) Other() Side {
	if s == A {
		return B
	}
	return A
}

// Change is one diff block: A[AStart .. AStart+ACount-1] became
// B[BStart .. BStart+BCount-1].
//
// Line numbers are 1-based, the way every line number in this editor is. A
// count of zero means the block is an insertion on the other side, and the
// start is then the line the insertion goes in front of, which can be one
// past the last line. That is vim's own representation and it is why
// Pair.Filler can be asked about line count+1.
type Change struct {
	AStart, ACount int
	BStart, BCount int
}

// Start and Count read the block's half for one side, which is most of what
// the callers of this package want and saves them a branch each time.
func (c Change) Start(s Side) int {
	if s == A {
		return c.AStart
	}
	return c.BStart
}

func (c Change) Count(s Side) int {
	if s == A {
		return c.ACount
	}
	return c.BCount
}

// Kind is what a line is highlighted as in diff mode.
type Kind uint8

// The three kinds, named for vim's highlight groups. A line with no
// counterpart on the other side is Added on the side that has it and shows as
// filler on the side that does not; a line with a counterpart that differs is
// Changed, and the part of it that differs is TextSpan's range.
const (
	Same Kind = iota
	Added
	Changed
)

// Group is the highlight group name vim paints this kind with, which is what
// a frontend looks up in its own table.
func (k Kind) Group() string {
	switch k {
	case Added:
		return "DiffAdd"
	case Changed:
		return "DiffChange"
	}
	return ""
}

// Pair is two buffers in diff mode against each other.
//
// It holds the lines it was built from, because the inline highlight range
// wants the bytes and re-deriving them from the caller on every screen line
// would be the same lookup twice. A Pair is read-only once built; do and dp
// change a buffer and the caller builds a new one, which is what vim does
// too -- ex_diffgetput ends in diff_redraw and the whole diff is recomputed.
type Pair struct {
	changes []Change
	lines   [2][][]byte
	opt     Options
}

// New diffs a against b and returns the pair.
//
// Both slices are kept, not copied: a caller that edits a line in place after
// this returns gets a Pair that disagrees with its buffer, and every caller
// in this tree builds a fresh Pair after every edit for exactly that reason.
func New(a, b [][]byte, o Options) *Pair {
	p := &Pair{lines: [2][][]byte{a, b}, opt: o}
	p.changes = Diff(a, b, o)
	return p
}

// Changes is the diff, in line order.
func (p *Pair) Changes() []Change { return p.changes }

// Lines is the number of lines on one side.
func (p *Pair) Lines(s Side) int { return len(p.lines[s]) }

// Filler is how many filler lines are drawn above line lnum on this side,
// which is vim's diff_filler().
//
// lnum may be one past the last line, and that is not an edge case: a block
// appended at the end of the other buffer puts its filler there and nowhere
// else, so a frontend that stops asking at the last line draws the last hunk
// of every "added at the end" diff without its filler.
func (p *Pair) Filler(s Side, lnum int) int {
	n := 0
	for _, c := range p.changes {
		mine, theirs := c.Count(s), c.Count(s.Other())
		if theirs <= mine {
			continue
		}
		// The filler goes below the block, which is to say above the first
		// line after it. Measured: a[2..3] against b[2..4] puts one filler
		// above a's line 4, not above line 2.
		if c.Start(s)+mine == lnum {
			n += theirs - mine
		}
	}
	return n
}

// Kind is how line lnum on this side is highlighted.
func (p *Pair) Kind(s Side, lnum int) Kind {
	c, ok := p.blockOf(s, lnum)
	if !ok {
		return Same
	}
	// The first min(ACount, BCount) lines of a block are pairs and are
	// Changed; whatever the longer side has past that has no counterpart and
	// is Added. Measured: a[2..3] against b[2..4] shows b's lines 2 and 3 as
	// DiffChange and line 4 as DiffAdd.
	i := lnum - c.Start(s)
	if i < min(c.ACount, c.BCount) {
		return Changed
	}
	return Added
}

// TextSpan is the byte range of line lnum that vim paints DiffText: the part
// between the common prefix and the common suffix it shares with the line it
// is paired with. The range is half-open and empty ranges come back ok false.
//
// This is vim's diff_find_change() and the transcription is deliberate,
// including the part that looks like a bug. The scan back from the end stops
// at the first differing pair OR when either index falls below the start of
// the differing run, and it is the index into THIS line that is kept, so
// "aaa" against "aaaa" gives an empty range on the three-a side and the last
// "a" alone on the four-a side. Measured, per column, with diff_hlID().
func (p *Pair) TextSpan(s Side, lnum int) (int, int, bool) {
	c, ok := p.blockOf(s, lnum)
	if !ok || p.Kind(s, lnum) != Changed {
		return 0, 0, false
	}
	i := lnum - c.Start(s)
	org := p.line(s, lnum)
	other := p.line(s.Other(), c.Start(s.Other())+i)
	if org == nil || other == nil {
		return 0, 0, false
	}
	si := p.firstDiff(org, other)
	if si >= len(org) {
		return 0, 0, false
	}
	eo, en := len(org)-1, len(other)-1
	for eo >= si && en >= si && org[eo] == other[en] {
		eo--
		en--
	}
	if eo < si {
		return 0, 0, false
	}
	return si, eo + 1, true
}

// firstDiff is the index of the first byte where two lines differ, with
// 'iwhite' folded in the way vim's diff_find_change folds it: a run of white
// space on both sides is skipped whole, so "a b" and "a b" first differ past
// the b, while "x" and " x" differ at the very first byte because a run of
// none is not a run.
func (p *Pair) firstDiff(org, other []byte) int {
	if !p.opt.IWhite && !p.opt.IWhiteAll {
		i := 0
		for i < len(org) && i < len(other) && org[i] == other[i] {
			i++
		}
		return i
	}
	io, in := 0, 0
	for {
		switch {
		case io < len(org) && in < len(other) && blank(org[io]) && blank(other[in]):
			for io < len(org) && blank(org[io]) {
				io++
			}
			for in < len(other) && blank(other[in]) {
				in++
			}
		case io < len(org) && in < len(other) && org[io] == other[in]:
			io++
			in++
		default:
			return io
		}
	}
}

// line is one line of one side, or nil when lnum is off the end.
func (p *Pair) line(s Side, lnum int) []byte {
	if lnum < 1 || lnum > len(p.lines[s]) {
		return nil
	}
	return p.lines[s][lnum-1]
}

// blockOf is the change containing line lnum on this side, for highlighting:
// the half-open range [start, start+count).
func (p *Pair) blockOf(s Side, lnum int) (Change, bool) {
	for _, c := range p.changes {
		start, n := c.Start(s), c.Count(s)
		if lnum >= start && lnum < start+n {
			return c, true
		}
	}
	return Change{}, false
}

// BlockAt is the change do and dp act on with the cursor on line lnum, which
// is a wider window than blockOf's.
//
// Measured on vim 9.2.0321 with ":diffget" run from every line of several
// fixtures: the block is reachable from its own lines, from the line just
// after it, and -- when the block sits past the end of the buffer, which is
// what an append at the end of the other file makes -- from the last line.
// The three collapse into one rule if both ends are clamped to the last line,
// which is how it is written here. Vim's own guard is a "> line2 + 1" on one
// side and a "< line1" on the other; this is the same window arrived at from
// the outside, because vim's source was not to hand to transcribe.
func (p *Pair) BlockAt(s Side, lnum int) (Change, bool) {
	last := len(p.lines[s])
	for _, c := range p.changes {
		lo := min(c.Start(s), last)
		hi := min(c.Start(s)+c.Count(s), last)
		if lnum >= lo && lnum <= hi {
			return c, true
		}
	}
	return Change{}, false
}

// Next is ]c: the line count changes forward of lnum on this side, and
// whether it moved at all.
//
// A block is "forward of" the cursor when it starts past it, so ]c from
// inside a block goes to the next one rather than to the top of the one the
// cursor is in. The answer is clamped to the last line, because a block
// appended past the end of this buffer is a change a person can see and ]c
// goes as close to it as a cursor can get. Measured both ways.
func (p *Pair) Next(s Side, lnum, count int) (int, bool) {
	if count < 1 {
		count = 1
	}
	last := max(1, len(p.lines[s]))
	at, moved := lnum, false
	for ; count > 0; count-- {
		found := false
		for _, c := range p.changes {
			if c.Start(s) > at {
				at = min(c.Start(s), last)
				found, moved = true, true
				break
			}
		}
		if !found {
			// Vim moves as far as it can and then beeps, rather than
			// refusing the whole count: measured, "3]c" over two changes
			// lands on the second one and complains.
			return at, moved
		}
	}
	return at, moved
}

// Prev is [c, the mirror of Next: the last block that starts before the
// cursor, so [c from inside a block goes to the top of that block only when
// the cursor is below its first line.
func (p *Pair) Prev(s Side, lnum, count int) (int, bool) {
	if count < 1 {
		count = 1
	}
	last := max(1, len(p.lines[s]))
	at, moved := lnum, false
	for ; count > 0; count-- {
		found := false
		for i := len(p.changes) - 1; i >= 0; i-- {
			if start := p.changes[i].Start(s); start < at {
				at = min(start, last)
				found, moved = true, true
				break
			}
		}
		if !found {
			return at, moved
		}
	}
	return at, moved
}

// Get is do and dp: what the block c looks like when the "from" side's lines
// are moved onto the other one.
//
// It returns the 1-based line range to replace in the destination and the
// lines to put there, and it does not touch either buffer: the caller owns
// the text and this package owns the arithmetic. A count of zero on the
// destination means an insertion, and first is then the line to insert in
// front of, so the caller's replace is the same call either way as long as it
// treats last < first as "insert".
func Get(c Change, from Side, src [][]byte) (first, last int, lines [][]byte) {
	to := from.Other()
	first = c.Start(to)
	last = first + c.Count(to) - 1
	fs, fn := c.Start(from), c.Count(from)
	lines = make([][]byte, 0, fn)
	for i := 0; i < fn; i++ {
		if fs+i-1 < len(src) {
			// Cloned, because the caller is about to splice these into the
			// other buffer and both sides go on being edited afterwards.
			lines = append(lines, bytes.Clone(src[fs+i-1]))
		}
	}
	return first, last, lines
}
