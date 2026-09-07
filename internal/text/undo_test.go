package text

import (
	"strings"
	"testing"
)

// vimCorpus is the file every cursor case below was measured on, with vim
// 9.2.0321 run as:
//
//	vim --clean -i NONE --not-a-term -s keys file
//
// where the keys end in :call writefile([string(getpos("."))],"state.txt").
const vimCorpus = "aaa\n    bbb\n    ccc\n    ddd\neee\nfff\n"

// Where "u" puts the cursor is the part of undo that cannot be reasoned out
// from first principles, so every case here is a measurement. The comment on
// each one is the keystrokes and the getpos() vim printed, in vim's 1-based
// columns; the want is this package's 0-based column, so a want column of 6 is
// vim's 7.
func TestUndoCursor(t *testing.T) {
	cases := []struct {
		name string
		// cursor is where the cursor was when the change began, which for every
		// operator is the start of the range it operated on.
		cursor Pos
		change func(b *Buffer)
		want   Pos
	}{
		{
			// 3G6ldw then G then u: vim reports [0, 3, 7, 0].
			name:   "dw in the middle of a line",
			cursor: Pos{3, 6},
			change: func(b *Buffer) { b.Delete(Range{Pos{3, 6}, Pos{3, 8}}) },
			want:   Pos{3, 6},
		},
		{
			// 3G11ldb then G then u: vim reports [0, 3, 7, 0], not column 12
			// where the cursor was when the keys were typed. This is what
			// "the start of the changed text" means.
			name:   "db backwards from a later column",
			cursor: Pos{3, 6},
			change: func(b *Buffer) { b.Delete(Range{Pos{3, 6}, Pos{3, 11}}) },
			want:   Pos{3, 6},
		},
		{
			// 3Gdj then G then u: vim reports [0, 3, 1, 0].
			name:   "dj over two lines",
			cursor: Pos{3, 0},
			change: func(b *Buffer) { b.DeleteLines(3, 4) },
			want:   Pos{3, 0},
		},
		{
			// 2Goxyz<Esc> then G then u: vim reports [0, 2, 1, 0], the line
			// above the one that was inserted.
			name:   "o below a line",
			cursor: Pos{2, 0},
			change: func(b *Buffer) { b.InsertLines(3, [][]byte{[]byte("xyz")}) },
			want:   Pos{2, 0},
		},
		{
			// 2G$Ju: vim reports [0, 2, 7, 0].
			name:   "J at the end of a line",
			cursor: Pos{2, 6},
			change: func(b *Buffer) { b.Replace(Range{Pos{2, 7}, Pos{3, 4}}, []byte(" ")) },
			want:   Pos{2, 6},
		},
		{
			// ggdGu: vim reports [0, 1, 1, 0].
			name:   "delete the whole buffer",
			cursor: Pos{1, 0},
			change: func(b *Buffer) { b.DeleteLines(1, 6) },
			want:   Pos{1, 0},
		},
		{
			// 6G:2,3s/b/B/e then u: vim reports [0, 2, 1, 0]. The cursor was on
			// line 6, nowhere near the change, so vim falls back to the first
			// line whose text differs, at column 0 rather than the first
			// non-blank, even though line 2 is indented four spaces.
			name:   "a change made away from the cursor",
			cursor: Pos{6, 0},
			change: func(b *Buffer) { b.SetLine(2, []byte("    BBB")) },
			want:   Pos{2, 0},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Read([]byte(vimCorpus))
			b.OpenUndoBlock(c.cursor)
			c.change(b)
			b.CloseUndoBlock()

			got, ok := b.Undo()
			if !ok {
				t.Fatal("Undo() found nothing to undo")
			}
			if got != c.want {
				t.Errorf("Undo() cursor = %v, want %v", got, c.want)
			}
			if s := string(b.Bytes()); s != vimCorpus {
				t.Errorf("undo did not restore the text:\n got %q\nwant %q", s, vimCorpus)
			}
		})
	}
}

// Insert-mode changes are the case where vim's own clamp shows through, and it
// is worth having the difference written down rather than discovered.
func TestUndoCursorIsNotClamped(t *testing.T) {
	// 2GAzz<Esc> then G then u: vim reports [0, 2, 3, 0] on the three-byte line
	// "two", because normal mode cannot sit past the last byte and vim clamps
	// column 4 down to 3 on the way to the screen. The change started at byte
	// column 3, and that is what this package returns.
	b := Read([]byte("one\ntwo\nthree\n"))
	b.OpenUndoBlock(Pos{2, 3})
	b.Insert(Pos{2, 3}, []byte("zz"))
	b.CloseUndoBlock()

	got, ok := b.Undo()
	if !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if want := (Pos{2, 3}); got != want {
		t.Errorf("Undo() cursor = %v, want %v (vim reports column 3 after clamping)", got, want)
	}
	if len(b.Line(2)) != 3 {
		t.Fatalf("line 2 is %q", b.Line(2))
	}
}

// A change with no block opened around it still has to be undoable, because a
// caller that forgets is a caller whose edits vanish from history.
func TestChangeWithoutAnOpenBlock(t *testing.T) {
	b := Read([]byte("hello\n"))
	b.Insert(Pos{1, 5}, []byte(" there"))
	if b.UndoSeqLast() != 0 {
		t.Errorf("UndoSeqLast() = %d before the block is closed, want 0", b.UndoSeqLast())
	}
	got, ok := b.Undo()
	if !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if want := (Pos{1, 5}); got != want {
		t.Errorf("cursor = %v, want %v, the start of the change", got, want)
	}
	if s := string(b.Bytes()); s != "hello\n" {
		t.Errorf("got %q", s)
	}
}

// The straight line: several steps, all the way back, all the way forward.
func TestUndoRedoChain(t *testing.T) {
	b := Read([]byte("a\n"))
	for _, s := range []string{"b", "c", "d"} {
		b.OpenUndoBlock(b.End())
		b.Insert(b.End(), []byte(s))
		b.CloseUndoBlock()
	}
	if got, want := string(b.Bytes()), "abcd\n"; got != want {
		t.Fatalf("after the edits: %q, want %q", got, want)
	}
	if b.UndoSeq() != 3 || b.UndoSeqLast() != 3 {
		t.Fatalf("seq = %d, last = %d, want 3 and 3", b.UndoSeq(), b.UndoSeqLast())
	}

	for _, want := range []string{"abc\n", "ab\n", "a\n"} {
		if _, ok := b.Undo(); !ok {
			t.Fatal("Undo() ran out early")
		}
		if got := string(b.Bytes()); got != want {
			t.Fatalf("undo gave %q, want %q", got, want)
		}
	}
	if _, ok := b.Undo(); ok {
		t.Error("Undo() at the root reported success")
	}
	if b.UndoSeq() != 0 {
		t.Errorf("at the root UndoSeq() = %d, want 0", b.UndoSeq())
	}

	for _, want := range []string{"ab\n", "abc\n", "abcd\n"} {
		if _, ok := b.Redo(); !ok {
			t.Fatal("Redo() ran out early")
		}
		if got := string(b.Bytes()); got != want {
			t.Fatalf("redo gave %q, want %q", got, want)
		}
	}
	if _, ok := b.Redo(); ok {
		t.Error("Redo() at the tip reported success")
	}
}

// The whole reason undo is a tree. This is the vim session
//
//	:1s/l/A/ u:3s/l/B/
//
// which leaves seq 2 as the current state and seq 1 as an alternate branch off
// the root, exactly as vim's undotree() reports it:
//
//	{'seq_last': 2, 'entries': [{'seq': 2, 'alt': [{'seq': 1}], 'newhead': 1}],
//	 'seq_cur': 2}
//
// Every buffer state below was read out of vim after the same keys.
func TestUndoTreeBranch(t *testing.T) {
	const start = "l1\nl2\nl3\nl4\n"
	b := Read([]byte(start))

	b.OpenUndoBlock(Pos{1, 0})
	b.SetLine(1, []byte("A1"))
	b.CloseUndoBlock()
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	b.OpenUndoBlock(Pos{3, 0})
	b.SetLine(3, []byte("B3"))
	b.CloseUndoBlock()

	if b.UndoSeq() != 2 || b.UndoSeqLast() != 2 {
		t.Fatalf("seq = %d, last = %d, want 2 and 2", b.UndoSeq(), b.UndoSeqLast())
	}
	if got, want := line(b, 1, 3), "l1|l2|B3"; got != want {
		t.Fatalf("at seq 2: %q, want %q", got, want)
	}

	// g- crosses to the other branch, which "u" would never reach.
	if _, ok := b.Older(); !ok {
		t.Fatal("Older() reported nothing older")
	}
	if got, want := line(b, 1, 3), "A1|l2|l3"; got != want {
		t.Errorf("after g-: %q, want %q", got, want)
	}
	if _, ok := b.Older(); !ok {
		t.Fatal("Older() reported nothing older")
	}
	if got, want := line(b, 1, 3), "l1|l2|l3"; got != want {
		t.Errorf("after a second g-: %q, want %q", got, want)
	}
	if _, ok := b.Older(); ok {
		t.Error("Older() at the root reported success")
	}
	if _, ok := b.Newer(); !ok {
		t.Fatal("Newer() reported nothing newer")
	}
	if got, want := line(b, 1, 3), "A1|l2|l3"; got != want {
		t.Errorf("after g+: %q, want %q", got, want)
	}

	// "u" from seq 2 goes to the parent, which is the root, not to seq 1.
	if _, ok := b.GotoSeq(2); !ok {
		t.Fatal("GotoSeq(2) failed")
	}
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if got := string(b.Bytes()); got != start {
		t.Errorf("u from seq 2 gave %q, want the original %q", got, start)
	}
}

// A file with no final newline that loses its last line and gets it back has to
// come back byte-identical, or the flag lives in the buffer and not in history
// and the file grows a byte nobody typed.
func TestUndoRestoresTheMissingFinalNewline(t *testing.T) {
	const start = "one\ntwo"
	b := Read([]byte(start))
	if !b.NoEOL() {
		t.Fatal("expected NoEOL")
	}
	b.OpenUndoBlock(Pos{2, 0})
	b.DeleteLines(2, 2)
	b.SetNoEOL(false)
	b.CloseUndoBlock()

	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if got := string(b.Bytes()); got != start {
		t.Errorf("got %q, want %q", got, start)
	}
}

// An undo block that changed nothing is still a step, because vim numbers an
// undo header at u_save and not at the change. Measured through cmd/oracle
// against vim 9.2.0321: "X" in column one, "d0" in column one and "~" on a
// digit all save undo and change nothing, and the "u" after each of them says
// "1 change; before #1" and puts the cursor back where the command started.
// The block also carries the cursor the command opened it with, which is what
// that "u" restores.
func TestEmptyBlockIsAStep(t *testing.T) {
	b := Read([]byte("abc\n"))
	b.OpenUndoBlock(Pos{1, 2})
	b.CloseUndoBlock()
	if b.UndoSeqLast() != 1 {
		t.Errorf("UndoSeqLast() = %d, want 1", b.UndoSeqLast())
	}
	at, ok := b.Undo()
	if !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if at != (Pos{1, 2}) {
		t.Errorf("Undo() cursor = %v, want {1 2}", at)
	}
	if got := string(b.Bytes()); got != "abc\n" {
		t.Errorf("buffer = %q, want %q", got, "abc\n")
	}
}

// Several changes inside one block are one step, which is what makes an insert
// of ten characters a single "u" and what "vim -s" does to every change in a
// script whether you wanted it or not.
func TestBlockGroupsChanges(t *testing.T) {
	const start = "one\ntwo\nthree\n"
	b := Read([]byte(start))
	b.OpenUndoBlock(Pos{1, 0})
	b.SetLine(1, []byte("ONE"))
	b.SetLine(3, []byte("THREE"))
	b.DeleteLines(2, 2)
	b.CloseUndoBlock()

	if got, want := string(b.Bytes()), "ONE\nTHREE\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if b.UndoSeqLast() != 1 {
		t.Errorf("UndoSeqLast() = %d, want 1", b.UndoSeqLast())
	}
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if got := string(b.Bytes()); got != start {
		t.Errorf("one undo gave %q, want %q", got, start)
	}
	if _, ok := b.Redo(); !ok {
		t.Fatal("Redo() found nothing to redo")
	}
	if got, want := string(b.Bytes()), "ONE\nTHREE\n"; got != want {
		t.Errorf("redo gave %q, want %q", got, want)
	}
}

// line joins lines first..last with a pipe, for readable failure messages.
func line(b *Buffer, first, last int) string {
	var parts []string
	for n := first; n <= last; n++ {
		parts = append(parts, string(b.Line(n)))
	}
	return strings.Join(parts, "|")
}

// "g-" and "g+" with a change still sitting in an open block.
//
// That is not a corner: nothing closes a block until Undo, Redo or a time
// travel does, and vim behaves the same way because may_sync_undo() explicitly
// does nothing while input comes from a "-s" script. So this is the ordinary
// state after any edit, and the sequence number has to be read after the block
// closes, not before. Read before, "g-" silently does nothing and "g+" walks
// the buffer a state BACKWARDS and reports success.
//
// Every case is vim 9.2.0321 on a file holding "one\n", run as
//
//	vim --clean -n -i NONE --not-a-term -s keys f.txt
//
// reporting getline(1,"$") and undotree().seq_cur.
func TestTimeTravelClosesTheBlockFirst(t *testing.T) {
	cases := []struct {
		name string
		// keys is the vim script the want values were measured from.
		keys string
		// edit leaves the buffer in the state those keys leave it in, with the
		// undo block still open, as script input does.
		edit func(b *Buffer)
		// walk is the "g-" or "g+" under test, run after edit.
		walk    func(b *Buffer) (Pos, bool)
		wantBuf string
		wantSeq int
		wantOK  bool
	}{
		{
			name:    `AA<Esc>g-`,
			keys:    "AA\x1bg-",
			edit:    func(b *Buffer) { b.Insert(Pos{1, 3}, []byte("A")) },
			walk:    (*Buffer).Older,
			wantBuf: "one\n",
			wantSeq: 0,
			wantOK:  true,
		},
		{
			name:    `AA<Esc>g+`,
			keys:    "AA\x1bg+",
			edit:    func(b *Buffer) { b.Insert(Pos{1, 3}, []byte("A")) },
			walk:    (*Buffer).Newer,
			wantBuf: "oneA\n",
			wantSeq: 1,
			// Already at the newest state: vim moves nothing and neither does
			// this, but the block still closed on the way through.
			wantOK: false,
		},
		{
			name: `AA<Esc>uAB<Esc>g-`,
			keys: "AA\x1buAB\x1bg-",
			edit: func(b *Buffer) {
				b.Insert(Pos{1, 3}, []byte("A"))
				b.Undo()
				b.Insert(Pos{1, 3}, []byte("B"))
			},
			walk:    (*Buffer).Older,
			wantBuf: "oneA\n",
			wantSeq: 1,
			wantOK:  true,
		},
		{
			name: `AA<Esc>uAB<Esc>g+`,
			keys: "AA\x1buAB\x1bg+",
			edit: func(b *Buffer) {
				b.Insert(Pos{1, 3}, []byte("A"))
				b.Undo()
				b.Insert(Pos{1, 3}, []byte("B"))
			},
			walk:    (*Buffer).Newer,
			wantBuf: "oneB\n",
			wantSeq: 2,
			wantOK:  false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Read([]byte("one\n"))
			c.edit(b)
			_, ok := c.walk(b)
			if ok != c.wantOK {
				t.Errorf("%s reported ok = %v, want %v", c.keys, ok, c.wantOK)
			}
			if got := string(b.Bytes()); got != c.wantBuf {
				t.Errorf("%s left %q, vim leaves %q", c.keys, got, c.wantBuf)
			}
			if got := b.UndoSeq(); got != c.wantSeq {
				t.Errorf("%s left seq %d, vim leaves %d", c.keys, got, c.wantSeq)
			}
		})
	}
}

// An undo that restores an ML_EMPTY buffer puts the last change on line 1.
//
// The walk that recomputes '. counts the one empty line as a real one and
// lands on line two, which does not exist even in vim's terms. Measured on an
// empty file: "i<CR><Esc>u" and "O<Esc>u" both leave getchangelist() holding
// line 1, and the same keys over a file with one line in it leave it on line 1
// without the clamp.
func TestUndoToEmptyBufferReportsLineOne(t *testing.T) {
	b := Read(nil)
	if !b.Emptied() {
		t.Fatal("a buffer read from nothing is not flagged empty")
	}
	b.OpenUndoBlock(Pos{Line: 1})
	b.InsertLines(1, [][]byte{[]byte("")})
	b.CloseUndoBlock()

	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() found nothing to undo")
	}
	if !b.Emptied() {
		t.Fatal("the undo did not restore the empty flag")
	}
	if got, ok := b.Mark(MarkLastChange); !ok || got.Line != 1 {
		t.Errorf("'. is on line %d, want 1", got.Line)
	}
	cl := b.ChangeList()
	if len(cl) != 1 || cl[0].Line != 1 {
		t.Errorf("changelist is %v, want one entry on line 1", cl)
	}
}
