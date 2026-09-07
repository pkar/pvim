package text

import "bytes"

// Format is the byte sequence a buffer puts between its lines when it is
// written back out: vim's 'fileformat'.
type Format int

const (
	// Unix separates lines with LF. It is the default and what a buffer with
	// no line separator at all in it gets.
	Unix Format = iota
	// DOS separates lines with CRLF. A buffer is DOS only when every LF in the
	// file it was read from had a CR in front of it, which is the rule vim's
	// 'fileformats' of "unix,dos" applies.
	DOS
)

// Buffer is one file's text: a slice of lines, each line a slice of bytes with
// no line terminator in it.
//
// Bytes, not runes, and lines, not a rope. Vim addresses text by line number
// and byte column, getpos() reports byte columns, every ex range is a line
// number and every register is a list of lines, so a buffer shaped the same way
// makes every future oracle diff a comparison and not a translation. A rope
// would buy fast splices into a 500 MB single-line file, which is not a file
// anyone opens, at the cost of making the common operation (get me line 4,000)
// a tree walk.
//
// Every mutation goes through Replace. That is the whole reason the lines field
// is unexported: an editor where two thirds of the changes are recorded in the
// undo tree is worse than one with no undo at all, because you find out which
// third is missing at the moment you need it.
//
// A Buffer always holds at least one line, as vim's does; deleting everything
// leaves one empty line.
type Buffer struct {
	lines  [][]byte
	format Format
	// noEOL records that the file did not end with a line separator, so that
	// Bytes gives back what Read was handed. Vim's 'fixendofline' decides
	// whether a write puts one back, and that is a write-path decision the ex
	// layer makes later, not something the text is allowed to forget here.
	noEOL bool

	// emptied records that every line was deleted. Vim keeps this as ML_EMPTY
	// and it is not the same thing as a buffer holding one empty line: "dd" on
	// a one-line file writes zero bytes, while a file that was already a lone
	// newline and was never edited writes its one byte back. Measured through
	// the pty harness against vim 9.2.0321, both directions, because an editor
	// that guesses here silently adds a byte to every file it empties.
	emptied bool

	marks     map[byte]Pos
	changes   []Pos
	changeIdx int
	newChange bool

	// undoLines is how many lines the last undo or redo put back, vim's
	// u_newcount, kept for the message the mode layer prints.
	undoLines int

	// linesInsertedAt is the line InsertLines is putting whole new lines
	// ahead of, read and cleared by the one Replace it makes. See there.
	linesInsertedAt int
	// noteLine is the line a whole-line insert or delete is really happening
	// at, which is where the change belongs on the changelist however the
	// splice under it had to be spelled. vim reports a whole-line edit through
	// ml_append or ml_delete and then one changed_lines() on the line it made
	// or took, never on the end of the line above it, and an entry left at the
	// end of the line above is one vim never has: on two lines cut down to
	// one, "11S CTRL-U z Esc o" leaves the first changelist entry in column 0
	// where noting the end of "z" leaves it in column 1. Set for one Replace
	// and cleared by it.
	noteLine int
	// textWidth is 'textwidth', which the changelist needs and nothing else in
	// this package does. See newEntryFor.
	textWidth int
	// tracked is the list of positions outside this package that moves with
	// the text: internal/mode's jumplist, which belongs to a window. See
	// Track.
	tracked    Tracker
	undoCur    *undoNode
	undoOpen   *undoNode
	undoHold   int
	undoBySeq  []*undoNode
	undoSeqMax int
}

// New returns an empty buffer: one empty line, unix line endings, no history.
func New() *Buffer {
	return Read(nil)
}

// Read parses a file's bytes into a buffer, detecting the line separator the
// way vim does with 'fileformats' set to "unix,dos".
//
// The rule is deliberately all-or-nothing: a file is DOS only if every LF in it
// is preceded by CR. A file with mixed endings is unix and the stray CRs stay
// in the text as ordinary bytes, which is what vim does and what makes a mixed
// file round-trip unchanged instead of being silently normalised.
func Read(data []byte) *Buffer {
	b := &Buffer{marks: map[byte]Pos{}}

	if dosEndings(data) {
		b.format = DOS
	}
	b.noEOL = len(data) > 0 && data[len(data)-1] != '\n'

	rest := data
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			b.lines = append(b.lines, rest)
			break
		}
		line := rest[:i]
		if b.format == DOS && len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		b.lines = append(b.lines, line)
		rest = rest[i+1:]
	}
	if len(b.lines) == 0 {
		// An empty file is one empty line and vim's ML_EMPTY, so that Bytes
		// gives back zero bytes and not a lone newline. It is the empty flag
		// and not noEOL: measured, typing into an empty file and writing it
		// gives "abc\n" even under 'nofixendofline', so the buffer's
		// 'endofline' is on and the emptiness is what makes the write short.
		b.lines = [][]byte{{}}
		b.emptied = true
	}

	// The previous-context mark exists from the moment the file is open, at
	// the top of it: `` and '' on a buffer nobody has jumped in put the cursor
	// on line 1 and say nothing, where an unset mark would be E20.
	b.marks[MarkLastJump] = Pos{Line: 1}
	// So do '[ , '] and '", which vim's readfile() sets over the lines it just
	// read: the first line, the last line, and the position the cursor starts
	// at. ":marks" on a buffer nothing has touched lists all three, which is
	// measured, and '] on such a buffer is the end of the file rather than
	// E20.
	b.marks[MarkChangeStart] = Pos{Line: 1}
	b.marks[MarkChangeEnd] = Pos{Line: len(b.lines)}
	b.marks[MarkLastExit] = Pos{Line: 1}

	root := &undoNode{}
	b.undoCur = root
	b.undoBySeq = []*undoNode{root}
	return b
}

// dosEndings reports whether every LF in data has a CR in front of it and there
// is at least one LF.
func dosEndings(data []byte) bool {
	seen := false
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		seen = true
		if i == 0 || data[i-1] != '\r' {
			return false
		}
	}
	return seen
}

// Bytes renders the buffer back to file bytes. Read(x).Bytes() equals x for
// every input, which is the property the undo round-trip test leans on.
func (b *Buffer) Bytes() []byte {
	eol := []byte("\n")
	if b.format == DOS {
		eol = []byte("\r\n")
	}
	n := 0
	for _, l := range b.lines {
		n += len(l) + len(eol)
	}
	if b.emptied {
		return nil
	}
	out := make([]byte, 0, n)
	for i, l := range b.lines {
		out = append(out, l...)
		if i < len(b.lines)-1 || !b.noEOL {
			out = append(out, eol...)
		}
	}
	return out
}

// LineCount returns the number of lines. It is never zero.
func (b *Buffer) LineCount() int { return len(b.lines) }

// Line returns the bytes of line n, 1-based, without any line separator.
//
// The returned slice is the buffer's own and MUST NOT be modified: writing
// through it is exactly the bypass Replace exists to prevent, and the change
// would be invisible to undo. Out of range gives nil, because a caller asking
// for line 0 or line count+1 is usually a motion that ran off the end and a
// panic there turns a cursor bug into a crash.
func (b *Buffer) Line(n int) []byte {
	if n < 1 || n > len(b.lines) {
		return nil
	}
	return b.lines[n-1]
}

// Format returns the line separator the buffer will be written with.
func (b *Buffer) Format() Format { return b.format }

// SetFormat changes the line separator. Vim's ":set ff=unix" is not undoable
// and neither is this.
func (b *Buffer) SetFormat(f Format) { b.format = f }

// NoEOL reports that the file had no separator after its last line.
func (b *Buffer) NoEOL() bool { return b.noEOL }

// Emptied is vim's ML_EMPTY: the buffer holds one empty line because the file
// was empty or because everything in it was deleted, and it writes as no bytes
// at all. A file holding a single newline is NOT this, and the difference is
// visible: "X" on the blank line of a one-newline file numbers an undo header
// in vim, and the same key on a buffer read from an empty file does not.
func (b *Buffer) Emptied() bool { return b.emptied }

// SetNoEOL sets the missing-final-separator flag: vim's ":set noeol".
func (b *Buffer) SetNoEOL(v bool) { b.noEOL = v }

// End returns the position after the last byte of the last line, which is where
// an append at the end of the buffer goes.
func (b *Buffer) End() Pos {
	return Pos{Line: len(b.lines), Col: len(b.lines[len(b.lines)-1])}
}

// Clamp moves p to the nearest position that actually exists in the buffer.
// Every entry point takes positions from motions and ranges that are allowed to
// overshoot, so clamping in one place beats bounds checks in twenty.
func (b *Buffer) Clamp(p Pos) Pos {
	if p.Line < 1 {
		return Pos{Line: 1, Col: 0}
	}
	if p.Line > len(b.lines) {
		return b.End()
	}
	l := b.lines[p.Line-1]
	if p.Col < 0 {
		p.Col = 0
	}
	if p.Col > len(l) {
		p.Col = len(l)
	}
	return p
}

// Replace swaps the half-open range r for text and returns the position just
// after the inserted bytes. It is the only mutation in this package; Insert,
// Delete and the line helpers are wrappers, and nothing else touches lines.
//
// text may contain LF, each one starting a new line. It may not usefully
// contain CR: a DOS buffer stores its lines without separators and puts them
// back on the way out, so a CR here is a literal CR in the text, same as vim.
//
// The change joins the undo block that is currently open, or opens one whose
// remembered cursor is the start of the change. That default is not arbitrary:
// vim saves the cursor into the undo header at the moment the first line of a
// change is saved, and for every operator it has already moved the cursor to
// the start of the operated range by then. See Undo for what that costs.
func (b *Buffer) Replace(r Range, text []byte) Pos {
	r.Start = b.Clamp(r.Start)
	r.End = b.Clamp(r.End)
	if r.End.Before(r.Start) {
		r.End = r.Start
	}

	if b.undoOpen == nil {
		b.OpenUndoBlock(r.Start)
	}

	first := r.Start.Line
	last := r.End.Line
	old := b.lines[first-1 : last]

	head := old[0][:r.Start.Col]
	tail := old[len(old)-1][r.End.Col:]

	// Build the replacement lines fresh. Nothing here may alias a line the undo
	// entry is about to take ownership of, or an undo would hand back a slice
	// the buffer is still writing through.
	joined := make([]byte, 0, len(head)+len(text)+len(tail))
	joined = append(joined, head...)
	joined = append(joined, text...)
	joined = append(joined, tail...)

	var repl [][]byte
	for {
		i := bytes.IndexByte(joined, '\n')
		if i < 0 {
			repl = append(repl, joined)
			break
		}
		repl = append(repl, joined[:i:i])
		joined = joined[i+1:]
	}

	// The end position: the last line of the replacement, at the offset the
	// inserted text stops at.
	end := Pos{Line: first + len(repl) - 1, Col: len(repl[len(repl)-1]) - len(tail)}

	saved := make([][]byte, len(old))
	copy(saved, old)
	b.undoOpen.entries = append(b.undoOpen.entries, undoEntry{
		first:    first,
		lines:    saved,
		replaced: len(repl),
	})

	// Any edit at all takes the buffer out of the emptied state: type one
	// character into a file that was just cleared and vim writes it with a
	// trailing newline again. DeleteLines puts the flag back when the edit it
	// is making removes the last line.
	b.emptied = false

	b.splice(first, len(old), repl)
	if at := b.linesInsertedAt; at > 0 {
		// Whole lines put in ahead of an existing one, which adjustMarks
		// cannot recover from the before-and-after alone: an empty line
		// inserted above an empty line looks exactly like one inserted below
		// it, the two move vim's marks differently, and the guess is wrong
		// half the time. "O" on line one of a buffer whose first line is
		// empty is that case, and it left the changelist entry for the line
		// above on the line that had moved.
		b.linesInsertedAt = 0
		b.markLinesInserted(at, len(repl)-len(old))
	} else {
		b.adjustMarks(first, saved, repl)
	}
	b.noteChange(r.Start, end)
	return end
}

// splice replaces count lines starting at the 1-based line first with repl.
//
// Three cases rather than one because the common one, a change inside a single
// line, has to be O(1) and not O(lines in the file). A 40,000-line log gets
// edited a character at a time like everything else, and a splice that copies
// the whole line index each time turns a fuzz run into a coffee break.
func (b *Buffer) splice(first, count int, repl [][]byte) {
	at := first - 1
	switch {
	case len(repl) == count:
		copy(b.lines[at:], repl)
	case len(repl) < count:
		copy(b.lines[at:], repl)
		copy(b.lines[at+len(repl):], b.lines[at+count:])
		b.lines = b.lines[:len(b.lines)-count+len(repl)]
	default:
		b.lines = append(b.lines, make([][]byte, len(repl)-count)...)
		copy(b.lines[at+len(repl):], b.lines[at+count:])
		copy(b.lines[at:], repl)
	}
}

// Insert puts text at p. It is Replace over an empty range and exists because
// an empty Range at a point reads badly at a call site.
func (b *Buffer) Insert(p Pos, text []byte) Pos {
	return b.Replace(Range{Start: p, End: p}, text)
}

// Delete removes the bytes in r and returns the position they were at.
func (b *Buffer) Delete(r Range) Pos {
	return b.Replace(r, nil)
}

// SetLine replaces the whole of line n. Vim's ml_replace, and the shape almost
// every :s and every indent change wants.
func (b *Buffer) SetLine(n int, text []byte) {
	if n < 1 || n > len(b.lines) {
		return
	}
	b.Replace(Range{Pos{n, 0}, Pos{n, len(b.lines[n-1])}}, text)
}

// InsertLines puts lines in before line n, so InsertLines(1, ...) puts them at
// the top and InsertLines(LineCount()+1, ...) at the bottom. Those are vim's
// "O" and "o", and this is a separate entry point because the arithmetic for "a
// new line before this one" is wrong more often than it is right when it is
// spelled out at the call site.
func (b *Buffer) InsertLines(n int, lines [][]byte) {
	if len(lines) == 0 {
		return
	}
	if n < 1 {
		n = 1
	}
	if n > len(b.lines) {
		// Past the end: the separator goes in front of the new text, or the
		// buffer grows a trailing empty line that was never in the file.
		text := []byte{'\n'}
		for i, l := range lines {
			if i > 0 {
				text = append(text, '\n')
			}
			text = append(text, l...)
		}
		at := len(b.lines) + 1
		b.noteLine = at
		b.Replace(Range{b.End(), b.End()}, text)
		b.ChangedAt(Pos{Line: at})
		return
	}
	var text []byte
	for _, l := range lines {
		text = append(text, l...)
		text = append(text, '\n')
	}
	// The marks move as vim's mark_adjust moves them for an ml_append before
	// line n, which is not what the before-and-after of the splice would
	// suggest when the new line and the old one are identical. Set for the
	// one Replace below and cleared by it.
	b.linesInsertedAt = n
	b.Replace(Range{Pos{n, 0}, Pos{n, 0}}, text)
	b.ChangedAt(Pos{Line: n})
}

// DeleteLines removes lines first through last inclusive, 1-based, the way
// ":first,last d" does. Deleting every line leaves one empty line behind,
// because a vim buffer is never empty.
func (b *Buffer) DeleteLines(first, last int) {
	if first < 1 {
		first = 1
	}
	if last > len(b.lines) {
		last = len(b.lines)
	}
	if first > last {
		return
	}
	// vim's deleted_lines_mark reports the first line that went, whatever
	// range of bytes the splice below actually covered: taking the last line
	// of the buffer means splicing from the end of the line above it, and vim
	// still says the line that is now gone.
	defer b.ChangedAt(Pos{Line: first})
	b.noteLine = first
	if last < len(b.lines) {
		b.Replace(Range{Pos{first, 0}, Pos{last + 1, 0}}, nil)
		return
	}
	if first == 1 {
		b.Replace(Range{Pos{1, 0}, b.End()}, nil)
		// Every line went, so the buffer is vim's ML_EMPTY and writes as no
		// bytes rather than as the lone newline the one empty line left behind
		// would otherwise render to.
		b.emptied = true
		return
	}
	b.Replace(Range{Pos{first - 1, len(b.lines[first-2])}, b.End()}, nil)
}

// Text returns the bytes covered by r, with LF between lines whatever the
// buffer's format is. A register holds text, not a file, so it holds LF.
func (b *Buffer) Text(r Range) []byte {
	r.Start = b.Clamp(r.Start)
	r.End = b.Clamp(r.End)
	if r.End.Before(r.Start) {
		return nil
	}
	if r.Start.Line == r.End.Line {
		return append([]byte(nil), b.lines[r.Start.Line-1][r.Start.Col:r.End.Col]...)
	}
	var out []byte
	out = append(out, b.lines[r.Start.Line-1][r.Start.Col:]...)
	for n := r.Start.Line + 1; n < r.End.Line; n++ {
		out = append(out, '\n')
		out = append(out, b.lines[n-1]...)
	}
	out = append(out, '\n')
	out = append(out, b.lines[r.End.Line-1][:r.End.Col]...)
	return out
}
