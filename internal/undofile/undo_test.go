package undofile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tree is the shape every test here starts from: a root, two edits on one
// branch, and a third hanging off the first, which is the tree "u u i<Esc>"
// leaves and the one a stack cannot represent. If the branch survives a round
// trip, the format is a tree and not a list with parent pointers written on it.
func tree() Tree {
	when := time.Date(2026, 9, 6, 5, 44, 4, 0, time.UTC)
	return Tree{
		SeqMax:   3,
		Cur:      2,
		RootLast: 1,
		Nodes: []Node{
			{
				Seq: 1, Parent: 0, Last: 2,
				Cursor: Pos{Line: 1, Col: 0},
				When:   when,
				Steps:  []Step{{First: 1, Lines: [][]byte{[]byte("alpha")}, Replaced: 1}},
			},
			{
				Seq: 2, Parent: 1, Last: -1,
				Cursor: Pos{Line: 2, Col: 3},
				NoEOL:  true,
				When:   when.Add(time.Second),
				Steps: []Step{
					{First: 2, Lines: [][]byte{[]byte("bravo"), []byte("charlie")}, Replaced: 1},
					{First: 1, Lines: nil, Replaced: 0},
				},
			},
			{
				Seq: 3, Parent: 1, Last: -1,
				Cursor:  Pos{Line: 1, Col: 2},
				Emptied: true, NoLines: true,
				When:  when.Add(2 * time.Second),
				Steps: []Step{{First: 1, Lines: [][]byte{[]byte("delta")}, Replaced: 2}},
			},
		},
	}
}

func sample() Undo {
	data := []byte("alpha\nbravo\n")
	return Undo{
		File:    "/tmp/f.txt",
		Size:    int64(len(data)),
		SHA:     Digest(data),
		Written: time.Date(2026, 9, 6, 5, 45, 0, 0, time.UTC),
		Tree:    tree(),
	}
}

func TestUndoRoundTrip(t *testing.T) {
	want := sample()
	got, err := DecodeUndo(EncodeUndo(want))
	if err != nil {
		t.Fatalf("DecodeUndo: %v", err)
	}
	if got.File != want.File || got.Size != want.Size || got.SHA != want.SHA {
		t.Errorf("header came back as %q %d %x", got.File, got.Size, got.SHA[:4])
	}
	if !got.Written.Equal(want.Written) {
		t.Errorf("written %v, want %v", got.Written, want.Written)
	}
	if got.Tree.Cur != want.Tree.Cur || got.Tree.SeqMax != want.Tree.SeqMax ||
		got.Tree.RootLast != want.Tree.RootLast {
		t.Errorf("cur %d seqMax %d rootLast %d", got.Tree.Cur, got.Tree.SeqMax, got.Tree.RootLast)
	}
	if len(got.Tree.Nodes) != len(want.Tree.Nodes) {
		t.Fatalf("%d nodes, want %d", len(got.Tree.Nodes), len(want.Tree.Nodes))
	}
	for i, n := range got.Tree.Nodes {
		w := want.Tree.Nodes[i]
		if n.Seq != w.Seq || n.Parent != w.Parent || n.Last != w.Last {
			t.Errorf("node %d links %d/%d/%d, want %d/%d/%d", i, n.Seq, n.Parent, n.Last, w.Seq, w.Parent, w.Last)
		}
		if n.Cursor != w.Cursor {
			t.Errorf("node %d cursor %+v, want %+v", i, n.Cursor, w.Cursor)
		}
		if n.NoEOL != w.NoEOL || n.Emptied != w.Emptied || n.NoLines != w.NoLines {
			t.Errorf("node %d flags %v %v %v", i, n.NoEOL, n.Emptied, n.NoLines)
		}
		if !n.When.Equal(w.When) {
			t.Errorf("node %d time %v, want %v", i, n.When, w.When)
		}
		if len(n.Steps) != len(w.Steps) {
			t.Fatalf("node %d has %d steps, want %d", i, len(n.Steps), len(w.Steps))
		}
		for j, s := range n.Steps {
			ws := w.Steps[j]
			if s.First != ws.First || s.Replaced != ws.Replaced || len(s.Lines) != len(ws.Lines) {
				t.Errorf("node %d step %d %+v, want %+v", i, j, s, ws)
				continue
			}
			for k := range s.Lines {
				if !bytes.Equal(s.Lines[k], ws.Lines[k]) {
					t.Errorf("node %d step %d line %d %q, want %q", i, j, k, s.Lines[k], ws.Lines[k])
				}
			}
		}
	}
}

// The branch is the point of the whole structure: node 3 hangs off node 1, not
// off the tip. A serialiser that wrote a stack would come back with 3's parent
// as 2 and "g-" would walk somewhere nobody has been.
func TestUndoKeepsTheBranch(t *testing.T) {
	got, err := DecodeUndo(EncodeUndo(sample()))
	if err != nil {
		t.Fatal(err)
	}
	var third Node
	for _, n := range got.Tree.Nodes {
		if n.Seq == 3 {
			third = n
		}
	}
	if third.Parent != 1 {
		t.Errorf("node 3's parent is %d, want 1: the branch did not survive", third.Parent)
	}
}

// The root's redo pointer is the one the node list cannot carry, and it is what
// a session undone all the way back and then quit comes up needing.
func TestUndoKeepsTheRootRedo(t *testing.T) {
	u := sample()
	u.Tree.Cur, u.Tree.RootLast = 0, 1
	got, err := DecodeUndo(EncodeUndo(u))
	if err != nil {
		t.Fatal(err)
	}
	if got.Tree.RootLast != 1 {
		t.Errorf("the root redoes to %d, want 1", got.Tree.RootLast)
	}
}

// A zero time has to come back zero. UnixNano on a zero Time is the count of
// nanoseconds from year 1, which reads back as 1754 and would put "272 years
// ago" on an undo message.
func TestUndoZeroTimeStaysZero(t *testing.T) {
	u := sample()
	u.Written = time.Time{}
	u.Tree.Nodes[0].When = time.Time{}
	got, err := DecodeUndo(EncodeUndo(u))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Written.IsZero() || !got.Tree.Nodes[0].When.IsZero() {
		t.Errorf("zero times came back as %v and %v", got.Written, got.Tree.Nodes[0].When)
	}
}

func TestUndoFileOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.pvundo")
	if err := WriteUndo(path, sample()); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600: an undo file is the file's contents by another name", st.Mode().Perm())
	}
	if _, err := ReadUndo(path); err != nil {
		t.Fatalf("ReadUndo: %v", err)
	}
	// Nothing left behind by the atomic write.
	ents, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("%d files in the undo directory, want 1", len(ents))
	}
}

// The guarantee the header exists for: a file changed outside pvim gets its
// history refused, not applied. Three shapes -- a different length, the same
// length with different bytes, and the same file -- because the size check in
// front of the digest is an optimisation that could hide a bug.
func TestUndoRefusesAChangedFile(t *testing.T) {
	data := []byte("alpha\nbravo\n")
	path := filepath.Join(t.TempDir(), "f.pvundo")
	u := sample()
	u.Size, u.SHA = int64(len(data)), Digest(data)
	if err := WriteUndo(path, u); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadUndo(path, data); err != nil {
		t.Fatalf("the unchanged file was refused: %v", err)
	}
	for _, changed := range [][]byte{
		[]byte("alpha\nbravo\ncharlie\n"), // longer
		[]byte("alpha\nBRAVO\n"),          // same length, different bytes
		nil,                               // emptied
	} {
		if _, err := LoadUndo(path, changed); !errors.Is(err, ErrStale) {
			t.Errorf("LoadUndo over %q: %v, want ErrStale", changed, err)
		}
	}
}

func TestUndoRefusesRubbish(t *testing.T) {
	good := EncodeUndo(sample())

	if _, err := DecodeUndo([]byte("not a pvim file at all")); !errors.Is(err, ErrMagic) {
		t.Errorf("a foreign file gave %v, want ErrMagic", err)
	}
	// A vim .un~ starts with "Vim\237UnDo\345", which is neither our magic nor
	// long enough to be mistaken for one.
	if _, err := DecodeUndo([]byte("Vim\237UnDo\345\000\000\000\000")); !errors.Is(err, ErrMagic) {
		t.Errorf("a vim undo file gave %v, want ErrMagic", err)
	}

	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xff
	if _, err := DecodeUndo(bad); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a flipped checksum gave %v, want ErrCorrupt", err)
	}

	bad = append([]byte(nil), good...)
	bad[len(good)/2] ^= 0xff
	if _, err := DecodeUndo(bad); err == nil {
		t.Error("a flipped byte in the middle was accepted")
	}

	for n := range good {
		if _, err := DecodeUndo(good[:n]); err == nil {
			t.Fatalf("a file truncated to %d bytes was accepted", n)
		}
	}

	// A version this pvim does not write. The version is a uvarint right after
	// the eight magic bytes and one byte long for anything under 128.
	bad = append([]byte(nil), good...)
	bad[8] = Version + 1
	bad = (&enc{b: bad[:len(bad)-4]}).seal()
	if _, err := DecodeUndo(bad); !errors.Is(err, ErrVersion) {
		t.Errorf("a future version gave %v, want ErrVersion", err)
	}
}

// Validate is what stops a tree that passed its checksum from hanging the
// editor. Every one of these is a file a buggy writer could produce and none of
// them is a file a disk could.
func TestValidateRefusesABrokenTree(t *testing.T) {
	cases := []struct {
		name string
		tree Tree
	}{
		{"a parent that is not there", Tree{SeqMax: 2, Cur: 2, Nodes: []Node{
			{Seq: 2, Parent: 1, Last: -1},
		}}},
		{"a redo that is not there", Tree{SeqMax: 1, Cur: 1, Nodes: []Node{
			{Seq: 1, Parent: 0, Last: 9},
		}}},
		{"a root redo that is not there", Tree{SeqMax: 1, Cur: 1, RootLast: 9, Nodes: []Node{
			{Seq: 1, Parent: 0, Last: -1},
		}}},
		{"two nodes with one sequence", Tree{SeqMax: 1, Cur: 1, Nodes: []Node{
			{Seq: 1, Parent: 0, Last: -1}, {Seq: 1, Parent: 0, Last: -1},
		}}},
		{"a current state that is not there", Tree{SeqMax: 1, Cur: 7, Nodes: []Node{
			{Seq: 1, Parent: 0, Last: -1},
		}}},
		{"a sequence above the highest", Tree{SeqMax: 1, Cur: 1, Nodes: []Node{
			{Seq: 1, Parent: 0, Last: -1}, {Seq: 5, Parent: 1, Last: -1},
		}}},
		{"the root stored as a node", Tree{SeqMax: 1, Cur: 0, Nodes: []Node{
			{Seq: 0, Parent: 0, Last: -1},
		}}},
		{"a cycle", Tree{SeqMax: 2, Cur: 1, Nodes: []Node{
			{Seq: 1, Parent: 2, Last: -1}, {Seq: 2, Parent: 1, Last: -1},
		}}},
		{"a step before line one", Tree{SeqMax: 1, Cur: 1, Nodes: []Node{
			{Seq: 1, Parent: 0, Last: -1, Steps: []Step{{First: 0}}},
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.tree.Validate(); err == nil {
				t.Error("accepted")
			}
			u := sample()
			u.Tree = c.tree
			if _, err := DecodeUndo(EncodeUndo(u)); err == nil {
				t.Error("accepted through the decoder")
			}
		})
	}
}

// An empty tree is a file nothing has been done to and is valid: it is what the
// first ":w" of an untouched buffer writes.
func TestValidateAcceptsAnEmptyTree(t *testing.T) {
	if err := (Tree{}).Validate(); err != nil {
		t.Errorf("the empty tree was refused: %v", err)
	}
}

// A 40k-line file's history is not a thing to be careless with: a tree holding
// one step per line has to encode and decode in a time nobody notices at
// startup. Not a benchmark with a gate, just a size that would show a quadratic.
func TestUndoBigTree(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	u := sample()
	u.Tree = Tree{SeqMax: 2000, Cur: 2000}
	for i := 1; i <= 2000; i++ {
		u.Tree.Nodes = append(u.Tree.Nodes, Node{
			Seq: i, Parent: i - 1, Last: -1,
			Steps: []Step{{First: 1, Replaced: 1, Lines: [][]byte{bytes.Repeat([]byte("x"), 80)}}},
		})
	}
	p := EncodeUndo(u)
	got, err := DecodeUndo(p)
	if err != nil {
		t.Fatalf("DecodeUndo of %d bytes: %v", len(p), err)
	}
	if len(got.Tree.Nodes) != 2000 {
		t.Errorf("%d nodes back", len(got.Tree.Nodes))
	}
}
