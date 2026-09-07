package lsp

import (
	"errors"
	"sort"
	"unicode/utf8"
)

// Positions, offsets and applying a list of edits.
//
// This is the file where the protocol's one genuinely awkward decision is
// dealt with: a Position's Character counts UTF-16 code units, and every
// column this editor has is a byte column. internal/text addresses lines and
// byte columns, vim's getpos() returns byte columns, and the oracle grades
// byte columns, so the conversion happens here, at the wire, and nothing above
// this package ever sees a UTF-16 number.
//
// The three cases the tests pin, because they are the three that are wrong in
// most clients: a line of ASCII, where the two columns are equal and a bug is
// invisible; a line with a two-byte rune, where a byte column is one ahead of
// a UTF-16 column per rune; and a line with an astral rune -- an emoji -- which
// is four bytes and TWO UTF-16 units, so the byte column runs ahead by two and
// a client that assumed one unit per rune is off by one for the rest of the
// line.

// ErrBadPosition is a Position that does not point into the document it came
// with. A server should never send one; one that does gets its edit refused
// rather than applied at a guessed place, because a formatting reply applied
// at the wrong offset is a silently corrupted file.
var ErrBadPosition = errors.New("lsp: position outside the document")

// UTF16Column converts a byte column on line into the UTF-16 code unit column
// the protocol wants. A byte column past the end of the line is clamped to its
// end, which is what a cursor sitting on the newline means.
func UTF16Column(line []byte, byteCol int) int {
	if byteCol > len(line) {
		byteCol = len(line)
	}
	n := 0
	for i := 0; i < byteCol; {
		r, size := utf8.DecodeRune(line[i:])
		if size == 0 {
			break
		}
		// A byte column landing INSIDE a rune is not a column: it is what a
		// caller gets when it measured a prefix that cut a rune in half. It
		// rounds down to the rune's start, which is the only answer that makes
		// this and ByteColumn inverses -- rounding up would move a position
		// forward through the text, and a completion applied one rune to the
		// right of where it was asked for is a corrupted line.
		if i+size > byteCol {
			break
		}
		// An invalid byte decodes as RuneError with size 1, which is one unit,
		// and that is the right answer: the server was sent the same byte and
		// will have counted it the same way.
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// ByteColumn is UTF16Column backwards: the byte column on line that the given
// UTF-16 code unit column names. A column past the end of the line is the end
// of the line.
func ByteColumn(line []byte, u16 int) int {
	if u16 <= 0 {
		return 0
	}
	n := 0
	for i := 0; i < len(line); {
		if n >= u16 {
			return i
		}
		r, size := utf8.DecodeRune(line[i:])
		if size == 0 {
			return i
		}
		if r > 0xFFFF {
			n += 2
			// A column landing between the two halves of a surrogate pair is
			// not a place in the text. It rounds to the start of the rune,
			// which is the only answer that leaves valid UTF-8 behind.
			if n > u16 {
				return i
			}
		} else {
			n++
		}
		i += size
	}
	return len(line)
}

// lineStarts is the byte offset of the start of every line in src, plus a
// final entry at len(src) so that the last line has an end.
//
// Lines are split at '\n' and a '\r' before it stays on the line, which is
// deliberate: the protocol counts lines and this editor reads a CRLF file as
// lines whose last byte is '\r' (see internal/text), so the two agree about
// where line 3 starts and about how wide it is.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// Offset turns a Position into a byte offset into src.
//
// A Line one past the last is the end of the document, which is what a server
// sends for an edit appended at the bottom of a file, and is why this is not
// simply a bounds check.
func Offset(src []byte, p Position) (int, error) {
	if p.Line < 0 || p.Character < 0 {
		return 0, ErrBadPosition
	}
	starts := lineStarts(src)
	if p.Line >= len(starts) {
		if p.Line == len(starts) && p.Character == 0 {
			return len(src), nil
		}
		return 0, ErrBadPosition
	}
	begin := starts[p.Line]
	end := len(src)
	if p.Line+1 < len(starts) {
		end = starts[p.Line+1] - 1 // without the '\n'
	}
	return begin + ByteColumn(src[begin:end], p.Character), nil
}

// PositionAt is Offset backwards: the Position naming byte offset off in src.
func PositionAt(src []byte, off int) Position {
	if off < 0 {
		off = 0
	}
	if off > len(src) {
		off = len(src)
	}
	starts := lineStarts(src)
	// The last line whose start is at or before off.
	line := sort.Search(len(starts), func(i int) bool { return starts[i] > off }) - 1
	if line < 0 {
		line = 0
	}
	begin := starts[line]
	end := len(src)
	if line+1 < len(starts) {
		end = starts[line+1] - 1
	}
	// The whole line and not src[begin:off]: cutting the line at off would put
	// half a rune at its end, which UTF16Column would then count as a byte of
	// its own. Handing it the real line and a byte column inside a rune is the
	// case UTF16Column rounds down for.
	return Position{Line: line, Character: UTF16Column(src[begin:end], off-begin)}
}

// ApplyEdits applies a formatting reply to a document and returns the result.
//
// The edits are applied back to front so that each one's offsets are still
// measured against the text it was computed for, which is what the
// specification requires and what makes a gofmt reply -- typically one edit
// per moved import line, a dozen of them, all through the file -- come out
// byte-identical to running gofmt over the same input.
//
// Overlapping edits are a server bug and are refused rather than merged. An
// empty list is not: gopls answers a file that is already formatted with an
// empty array, and the right result is the input unchanged.
func ApplyEdits(src []byte, edits []TextEdit) ([]byte, error) {
	if len(edits) == 0 {
		return src, nil
	}

	type span struct {
		start, end int
		text       string
		order      int
	}
	spans := make([]span, 0, len(edits))
	for i, e := range edits {
		start, err := Offset(src, e.Range.Start)
		if err != nil {
			return nil, err
		}
		end, err := Offset(src, e.Range.End)
		if err != nil {
			return nil, err
		}
		if end < start {
			return nil, ErrBadPosition
		}
		spans = append(spans, span{start: start, end: end, text: e.NewText, order: i})
	}

	// Descending by start, and for two edits at one offset the later one in
	// the list goes first, so that applying them in this order leaves them in
	// the list's order in the text. Two inserts at the same point is the one
	// case the specification allows to touch.
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start > spans[j].start
		}
		return spans[i].order > spans[j].order
	})

	out := src
	last := len(src) + 1
	for _, s := range spans {
		if s.end > last {
			return nil, errors.New("lsp: overlapping edits in the reply")
		}
		last = s.start
		next := make([]byte, 0, len(out)-(s.end-s.start)+len(s.text))
		next = append(next, out[:s.start]...)
		next = append(next, s.text...)
		next = append(next, out[s.end:]...)
		out = next
	}
	return out, nil
}

// Diff is the incremental edit between two versions of a document: the common
// prefix, the common suffix, and one replacement of the range between them.
//
// This is what an editor with no change feed sends. internal/text has none and
// should not grow one for this: a buffer that had to tell somebody about every
// edit would have a listener list and an ordering problem, and what is wanted
// here is one message per coalescing window and not one per keystroke. Two
// byte scans over the buffer answer it exactly, and typing a character in a
// 4,000-line file comes out as about twenty bytes on the wire rather than as
// the file.
//
// The boundaries are pulled back to rune starts, because a Position inside a
// UTF-8 sequence is not a place in the text and because the new text would
// otherwise carry a fragment of a rune. The bounds checks are not decoration
// either: an append at the end of the document leaves the prefix at the end of
// the old bytes, and a delete of everything leaves the suffix at the start.
//
// It reports false when nothing moved, which is the common case on a keystroke
// that was a cursor motion.
func Diff(old, next []byte) (ContentChange, bool) {
	if string(old) == string(next) {
		return ContentChange{}, false
	}
	n := len(old)
	if len(next) < n {
		n = len(next)
	}
	pre := 0
	for pre < n && old[pre] == next[pre] {
		pre++
	}
	for pre > 0 && pre < len(old) && old[pre]&0xC0 == 0x80 {
		pre--
	}
	suf := 0
	for suf < n-pre && old[len(old)-1-suf] == next[len(next)-1-suf] {
		suf++
	}
	for suf > 0 && old[len(old)-suf]&0xC0 == 0x80 {
		suf--
	}
	return ContentChange{
		Range: &Range{Start: PositionAt(old, pre), End: PositionAt(old, len(old)-suf)},
		Text:  string(next[pre : len(next)-suf]),
	}, true
}
