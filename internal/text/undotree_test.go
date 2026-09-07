package text

import (
	"bytes"
	"testing"
	"time"
)

// pinClock stamps every step with a fixed time so a dumped tree compares equal
// to itself. Without it this file would be testing time.Now.
func pinClock(t *testing.T) time.Time {
	t.Helper()
	at := time.Date(2026, 9, 6, 4, 30, 0, 0, time.UTC)
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
	return at
}

// edit makes one undo step out of one replacement.
func edit(b *Buffer, r Range, s string) {
	b.OpenUndoBlock(r.Start)
	b.Replace(r, []byte(s))
	b.CloseUndoBlock()
}

func lineRange(line, from, to int) Range {
	return Range{Start: Pos{Line: line, Col: from}, End: Pos{Line: line, Col: to}}
}

// TestUndoTreeRoundTrip walks a BRANCHED history, which is the case that
// separates a tree from a stack: three edits, undo twice, a fourth edit off the
// first, which leaves two children under node 1 and a redo pointer that is not
// simply "the newest node".
func TestUndoTreeRoundTrip(t *testing.T) {
	at := pinClock(t)

	b := Read([]byte("one\ntwo\nthree\n"))
	edit(b, lineRange(1, 0, 3), "ONE")
	edit(b, lineRange(2, 0, 3), "TWO")
	edit(b, lineRange(3, 0, 5), "THREE")
	b.Undo()
	b.Undo()
	edit(b, lineRange(2, 0, 3), "branch") // seq 4, a sibling of seq 2

	wantText := b.Bytes()
	nodes, cur, seqMax, rootLast := b.UndoTree()

	if len(nodes) != 4 {
		t.Fatalf("%d nodes, want 4", len(nodes))
	}
	if seqMax != 4 {
		t.Errorf("seqMax = %d, want 4", seqMax)
	}
	if cur != 4 {
		t.Errorf("cur = %d, want 4", cur)
	}
	// The branch is the whole point: node 4's parent is node 1, not node 3.
	if nodes[3].Parent != 1 {
		t.Errorf("node 4's parent = %d, want 1", nodes[3].Parent)
	}
	if !nodes[0].When.Equal(at) {
		t.Errorf("node 1 stamped %v, want %v", nodes[0].When, at)
	}

	// Restore into a buffer holding the same text and assert the history walks
	// the same way, which is a stronger check than comparing the dumps: it goes
	// through the code u and g- actually use.
	got := Read(wantText)
	if err := got.SetUndoTree(nodes, cur, seqMax, rootLast); err != nil {
		t.Fatalf("SetUndoTree: %v", err)
	}
	if !bytes.Equal(got.Bytes(), wantText) {
		t.Fatalf("restore changed the text")
	}

	for step := 0; step < 4; step++ {
		wantU, okW := b.Undo()
		gotU, okG := got.Undo()
		if okW != okG || wantU != gotU || !bytes.Equal(b.Bytes(), got.Bytes()) {
			t.Fatalf("undo %d: got %v %v, want %v %v\n got %q\nwant %q",
				step, gotU, okG, wantU, okW, got.Bytes(), b.Bytes())
		}
	}
	// And back up the other branch, which is where rootLast earns its keep.
	for step := 0; step < 4; step++ {
		wantR, okW := b.Redo()
		gotR, okG := got.Redo()
		if okW != okG || wantR != gotR || !bytes.Equal(b.Bytes(), got.Bytes()) {
			t.Fatalf("redo %d: got %v %v, want %v %v", step, gotR, okG, wantR, okW)
		}
	}
}

// TestUndoTreeRootLastSurvives is the bug the separate rootLast return exists to
// prevent: a session undone all the way back to the root and then quit must come
// up able to redo. Nothing in the node list can carry the root's redo child.
func TestUndoTreeRootLastSurvives(t *testing.T) {
	pinClock(t)

	b := Read([]byte("one\n"))
	edit(b, lineRange(1, 0, 3), "ONE")
	b.Undo() // back at the root

	nodes, cur, seqMax, rootLast := b.UndoTree()
	if cur != 0 {
		t.Fatalf("cur = %d, want 0", cur)
	}
	if rootLast != 1 {
		t.Fatalf("rootLast = %d, want 1", rootLast)
	}

	got := Read([]byte("one\n"))
	if err := got.SetUndoTree(nodes, cur, seqMax, rootLast); err != nil {
		t.Fatalf("SetUndoTree: %v", err)
	}
	if _, ok := got.Redo(); !ok {
		t.Fatal("redo after a restore at the root did nothing")
	}
	if want := "ONE\n"; string(got.Bytes()) != want {
		t.Errorf("redo gave %q, want %q", got.Bytes(), want)
	}
}

// TestSetUndoTreeRefusesJunk keeps the validation honest. A file that is corrupt
// or from another editor must leave the buffer with the history it had, so every
// check has to fire before anything is written.
func TestSetUndoTreeRefusesJunk(t *testing.T) {
	pinClock(t)

	good := func() ([]UndoNode, int, int, int) {
		b := Read([]byte("one\n"))
		edit(b, lineRange(1, 0, 3), "ONE")
		edit(b, lineRange(1, 0, 3), "TWO")
		return b.UndoTree()
	}

	for _, tc := range []struct {
		name string
		bend func(n []UndoNode, cur, seqMax, rootLast int) ([]UndoNode, int, int, int)
	}{
		{"cur past seqMax", func(n []UndoNode, _, sm, rl int) ([]UndoNode, int, int, int) {
			return n, sm + 1, sm, rl
		}},
		{"negative cur", func(n []UndoNode, _, sm, rl int) ([]UndoNode, int, int, int) {
			return n, -1, sm, rl
		}},
		{"sequence numbers out of order", func(n []UndoNode, c, sm, rl int) ([]UndoNode, int, int, int) {
			n[0].Seq, n[1].Seq = n[1].Seq, n[0].Seq
			return n, c, sm, rl
		}},
		{"parent is a later node", func(n []UndoNode, c, sm, rl int) ([]UndoNode, int, int, int) {
			n[0].Parent = 2
			return n, c, sm, rl
		}},
		{"parent is itself", func(n []UndoNode, c, sm, rl int) ([]UndoNode, int, int, int) {
			n[1].Parent = 2
			return n, c, sm, rl
		}},
		{"redo child is an earlier node", func(n []UndoNode, c, sm, rl int) ([]UndoNode, int, int, int) {
			n[1].Last = 1
			return n, c, sm, rl
		}},
		{"redo child is off the end", func(n []UndoNode, c, sm, rl int) ([]UndoNode, int, int, int) {
			n[0].Last = 99
			return n, c, sm, rl
		}},
		{"root redo child is off the end", func(n []UndoNode, c, sm, _ int) ([]UndoNode, int, int, int) {
			return n, c, sm, 99
		}},
		{"cur names a node that is not here", func(n []UndoNode, _, _, rl int) ([]UndoNode, int, int, int) {
			return n, 9, 9, rl
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nodes, cur, seqMax, rootLast := tc.bend(good())

			b := Read([]byte("kept\n"))
			edit(b, lineRange(1, 0, 4), "MINE")
			before := b.UndoSeqLast()

			if err := b.SetUndoTree(nodes, cur, seqMax, rootLast); err == nil {
				t.Fatal("junk was accepted")
			}
			// The buffer must still have its own history, not half of the junk.
			if got := b.UndoSeqLast(); got != before {
				t.Errorf("history was mutated: seqMax %d, want %d", got, before)
			}
			if _, ok := b.Undo(); !ok {
				t.Error("the buffer's own undo stopped working")
			}
			if want := "kept\n"; string(b.Bytes()) != want {
				t.Errorf("undo gave %q, want %q", b.Bytes(), want)
			}
		})
	}
}
