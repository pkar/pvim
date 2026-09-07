// Package text holds the buffer and the coordinates every other package uses to
// talk about a place in it. It imports nothing else in this module, on purpose:
// a test in internal/ fails if it ever does.
package text

// Pos is a place in a buffer, in the coordinates vim's own commands use.
//
// Line is 1-based, because that is what every ex range, mark, `:goto` and error
// message means by a line number, and an off-by-one there would show up in the
// oracle diff of every message.
//
// Col is a 0-based BYTE offset into that line, not a rune index and not a
// display column. Three different columns exist in this editor and confusing
// them is the bug that will cost the most time:
//
// - byte column: what Pos.Col is, what the buffer slices with, what vim's
// getpos() reports (getpos() reports it 1-based, so add one at that edge and
// nowhere else).
// - rune index: never stored. Compute it when a rune count is genuinely what
// is wanted, which is almost nowhere.
// - display column: where the cell lands on screen once tabstop, wide runes
// and 'nowrap' scrolling have had their say. It is a function of the line
// and the options, never a field, so it cannot go stale.
//
// A Col equal to len(line) is the position after the last byte, which is where
// insert mode and the exclusive end of a motion sit. A Col that lands inside a
// multi-byte rune is invalid and any function taking a Pos may do anything it
// likes with one.
type Pos struct {
	Line int
	Col  int
}

// Compare orders two positions in buffer order: negative if p is before q, zero
// if they are the same place, positive if p is after q.
func (p Pos) Compare(q Pos) int {
	switch {
	case p.Line != q.Line:
		return p.Line - q.Line
	default:
		return p.Col - q.Col
	}
}

// Before reports whether p comes strictly before q.
func (p Pos) Before(q Pos) bool { return p.Compare(q) < 0 }

// Range is a half-open span of the buffer: Start is included, End is not.
//
// Half-open because that is the only convention that can name an empty span and
// the only one that composes without a special case at the end of a line. Vim's
// motions are inclusive or exclusive per motion and its visual selections are
// inclusive; converting either into a Range is the caller's job, at the one
// place it decides, and everything downstream gets the same shape.
type Range struct {
	Start Pos
	End   Pos
}

// Empty reports whether r covers no bytes.
func (r Range) Empty() bool { return r.Start.Compare(r.End) >= 0 }

// Contains reports whether p falls inside r.
func (r Range) Contains(p Pos) bool {
	return r.Start.Compare(p) <= 0 && p.Compare(r.End) < 0
}
