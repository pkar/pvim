package undofile

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Step is one contiguous run of lines an undo swaps, which is internal/text's
// undoEntry: put Lines back at First, over the Replaced lines that are there
// now. Applying a step twice returns to where it started, and that is what
// makes one structure serve both undo and redo.
type Step struct {
	// First is the 1-based line the run starts at.
	First int
	// Lines is what has to go back.
	Lines [][]byte
	// Replaced is how many lines are there now.
	Replaced int
}

// Node is one state of the buffer: one press of "u".
//
// Parent is the state "u" reaches from here and Last is the child CTRL-R
// follows, both as sequence numbers, with -1 for none. Sequence numbers and not
// slice indices because they are the identity vim uses -- "g-" and "g+" walk
// them chronologically across branches, ":undo N" names one, and the undo
// message prints one -- and because a file whose node order got shuffled by a
// future writer still reads back into the same tree.
type Node struct {
	Seq    int
	Parent int
	// Last is the child CTRL-R follows from here, or -1 for none. Zero means
	// none too: sequence 0 is the root and is never a redo target.
	Last int

	// Cursor is where the cursor was when the block opened, vim's uh_cursor,
	// and it is the position this node restores. Getting it right is most of
	// the work in internal/text's undo and all of it is thrown away if it is
	// not written down here.
	Cursor Pos

	// The three flags internal/text restores with the text. NoEOL and Emptied
	// are the file's missing final newline and the ML_EMPTY flag, both of
	// which change what a ":w" puts on the disk; NoLines is the header shape
	// that saved no line at all, which is what makes an undone ":put" of an
	// empty register say "0 changes".
	NoEOL   bool
	Emptied bool
	NoLines bool

	// When the step was made. Vim's uh_time, and the reason the undo message
	// can say "3 seconds ago" instead of "0 seconds ago" -- see the note on
	// Undo.Written and the register D-007.
	When time.Time

	// Steps are in the order applying this node must walk them, which is the
	// reverse of the order the changes were made in.
	Steps []Step
}

// Tree is a whole undo tree, flattened.
//
// Nodes holds every node except the root: sequence 0 is the file as it was
// read, it has no text of its own and nothing to store. Cur is the sequence
// number the buffer was sitting on when the file was written, so that a restart
// followed by CTRL-R redoes what the last session had undone -- vim keeps that
// too and it is the difference between reopening a file where you left it and
// reopening it at the tip of a branch you had deliberately walked back from.
type Tree struct {
	Nodes  []Node
	Cur    int
	SeqMax int

	// RootLast is the child CTRL-R follows from the root, which is the one
	// pointer the node list cannot carry: the root itself is not stored,
	// having no text of its own. Without it a session that was undone all the
	// way back and then quit comes up unable to redo anything, which is the
	// one case where the whole afternoon is on the other side of the pointer.
	// Zero or negative means none, sequence 0 being the root and never a redo
	// target.
	RootLast int
}

// Undo is an undo file: the tree, and the header that says which file and which
// version of it the tree belongs to.
type Undo struct {
	// File is the absolute path of the file the tree describes. It is in the
	// header and not only in the name because the name is lossy: a '%' in a
	// path is not escaped by percentPath, so two files can produce one name,
	// and a header that names the wrong file is refused rather than applied.
	File string

	// Size and SHA are the file the tree was built over. A file changed
	// outside pvim -- a git checkout, a gofmt from a shell, another editor --
	// invalidates the history rather than corrupting the buffer with it. Vim
	// invalidates on the same evidence and this is the one guarantee of vim's
	// .un~ worth keeping byte for byte.
	Size int64
	SHA  [32]byte

	// Written is the clock when the file was written.
	//
	// the register D-007 is about the missing half of this: internal/text's
	// tree keeps no timestamps, so internal/mode/undo.go prints "0 seconds
	// ago" whatever the clock says. A time here is what a restored tree needs
	// to answer honestly across a restart, and Node.When is what one needs
	// inside a session. Both are in the format; whether the message can be
	// fixed depends on internal/text keeping a time per node, which it does
	// not yet, so D-007 stands until it does.
	Written time.Time

	Tree Tree
}

// Digest is the sha256 of a file's contents, which is what an undo file's
// header holds and what invalidates it.
func Digest(data []byte) [32]byte { return sha256.Sum256(data) }

// EncodeUndo returns the bytes of an undo file.
//
// Separate from WriteUndo so that a test can round-trip a tree without a
// directory and so that the atomic write below has one thing to write.
func EncodeUndo(u Undo) []byte {
	var e enc
	e.raw([]byte(magicUndo))
	e.uint(Version)
	e.str(u.File)
	e.int64(u.Size)
	e.raw(u.SHA[:])
	e.int64(nanos(u.Written))
	e.int(u.Tree.Cur)
	e.int(u.Tree.SeqMax)
	e.int(u.Tree.RootLast)
	e.uint(uint64(len(u.Tree.Nodes)))
	for _, n := range u.Tree.Nodes {
		e.int(n.Seq)
		e.int(n.Parent)
		e.int(n.Last)
		e.int(n.Cursor.Line)
		e.int(n.Cursor.Col)
		e.bool(n.NoEOL)
		e.bool(n.Emptied)
		e.bool(n.NoLines)
		e.int64(nanos(n.When))
		e.uint(uint64(len(n.Steps)))
		for _, s := range n.Steps {
			e.int(s.First)
			e.int(s.Replaced)
			e.lines(s.Lines)
		}
	}
	return e.seal()
}

// DecodeUndo reads an undo file back.
//
// The tree is validated before it is returned: a node whose parent is not in
// the file, a cycle, or a current sequence number that names nothing are all
// refused here rather than found by the first press of "u". A corrupt tree that
// passes the CRC is not a thing a disk does, but it is exactly what a bug in a
// future writer looks like, and the cost of the check is one pass over a few
// hundred nodes at startup.
func DecodeUndo(p []byte) (Undo, error) {
	d, err := open(p, magicUndo)
	if err != nil {
		return Undo{}, err
	}
	var u Undo
	u.File = d.str()
	u.Size = d.int64()
	if sha := d.take(32); len(sha) == 32 {
		copy(u.SHA[:], sha)
	}
	u.Written = unixNano(d.int64())
	u.Tree.Cur = d.int()
	u.Tree.SeqMax = d.int()
	u.Tree.RootLast = d.int()
	n := d.uint()
	if n > uint64(len(d.b)) {
		return Undo{}, fmt.Errorf("%w: %d undo nodes, %d bytes left", ErrCorrupt, n, len(d.b))
	}
	u.Tree.Nodes = make([]Node, n)
	for i := range u.Tree.Nodes {
		node := &u.Tree.Nodes[i]
		node.Seq = d.int()
		node.Parent = d.int()
		node.Last = d.int()
		node.Cursor.Line = d.int()
		node.Cursor.Col = d.int()
		node.NoEOL = d.bool()
		node.Emptied = d.bool()
		node.NoLines = d.bool()
		node.When = unixNano(d.int64())
		steps := d.uint()
		if steps > uint64(len(d.b)) {
			return Undo{}, fmt.Errorf("%w: %d undo steps, %d bytes left", ErrCorrupt, steps, len(d.b))
		}
		node.Steps = make([]Step, steps)
		for j := range node.Steps {
			s := &node.Steps[j]
			s.First = d.int()
			s.Replaced = d.int()
			s.Lines = d.lines()
		}
	}
	if d.err != nil {
		return Undo{}, d.err
	}
	if len(d.b) != 0 {
		return Undo{}, fmt.Errorf("%w: %d bytes after the tree", ErrCorrupt, len(d.b))
	}
	if err := u.Tree.Validate(); err != nil {
		return Undo{}, err
	}
	return u, nil
}

// nanos is a time on its way to the disk. The zero Time stores as a zero and
// not as the nanoseconds between year 1 and 1970, which is what UnixNano
// answers for it and which reads back as a date in 1754.
func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// unixNano turns a stored time back into a Time, keeping the zero value zero.
// A node written before anything kept times reads back as a zero Time, and the
// undo message has to say something else about that rather than 1970.
func unixNano(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.Unix(0, v)
}

// Validate reports whether the tree hangs together: every parent and every
// "last" names a node in the file or the root, no sequence number is repeated,
// nothing is above SeqMax, the current state exists, and walking parents from
// every node reaches the root without going round.
func (t Tree) Validate() error {
	seen := map[int]bool{0: true}
	parent := map[int]int{0: -1}
	for _, n := range t.Nodes {
		if n.Seq <= 0 {
			return fmt.Errorf("%w: undo node with sequence %d; the root is 0 and is not stored", ErrCorrupt, n.Seq)
		}
		if seen[n.Seq] {
			return fmt.Errorf("%w: two undo nodes with sequence %d", ErrCorrupt, n.Seq)
		}
		if n.Seq > t.SeqMax {
			return fmt.Errorf("%w: undo node %d is above the highest sequence %d", ErrCorrupt, n.Seq, t.SeqMax)
		}
		seen[n.Seq] = true
		parent[n.Seq] = n.Parent
		for _, s := range n.Steps {
			if s.First < 1 || s.Replaced < 0 {
				return fmt.Errorf("%w: undo step at line %d replacing %d lines", ErrCorrupt, s.First, s.Replaced)
			}
		}
	}
	if t.RootLast > 0 && !seen[t.RootLast] {
		return fmt.Errorf("%w: the root redoes to %d, which is not in the tree", ErrCorrupt, t.RootLast)
	}
	if !seen[t.Cur] {
		return fmt.Errorf("%w: current undo state %d is not in the tree", ErrCorrupt, t.Cur)
	}
	for _, n := range t.Nodes {
		if !seen[n.Parent] {
			return fmt.Errorf("%w: undo node %d has parent %d, which is not in the tree", ErrCorrupt, n.Seq, n.Parent)
		}
		if n.Last > 0 && !seen[n.Last] {
			return fmt.Errorf("%w: undo node %d redoes to %d, which is not in the tree", ErrCorrupt, n.Seq, n.Last)
		}
		// Up to the root, counting: a cycle cannot take more steps than
		// there are nodes.
		at, steps := n.Seq, 0
		for at != 0 {
			at = parent[at]
			steps++
			if steps > len(t.Nodes) {
				return fmt.Errorf("%w: undo node %d is in a cycle", ErrCorrupt, n.Seq)
			}
		}
	}
	return nil
}

// WriteUndo writes an undo file, atomically.
//
// Temp file then rename, because the alternative is a truncated undo file the
// next launch refuses -- and the launch after a crash is exactly when the
// history is worth having. 0600 and not 0644: undo history is the file's
// contents by another name, and a world-readable copy of a file in a mode 600
// directory is a way to leak one.
func WriteUndo(path string, u Undo) error { return writeFileAtomic(path, EncodeUndo(u)) }

// ReadUndo reads an undo file with no reference to the file it describes. Use
// LoadUndo to apply one to a buffer; this is for a test and for a caller that
// wants the header to say what it knows.
func ReadUndo(path string) (Undo, error) {
	p, err := os.ReadFile(path)
	if err != nil {
		return Undo{}, err
	}
	return DecodeUndo(p)
}

// LoadUndo reads the undo file for a file whose contents are data, and refuses
// one that does not belong to it.
//
// ErrStale is the refusal that matters and it is not a failure: a file edited
// by something else since pvim last wrote its history has a history that no
// longer describes it, and replaying it would fill the buffer with text that
// was never in the file. Vim says nothing at all in that case and starts a
// fresh history, which is what the caller should do with this.
func LoadUndo(path string, data []byte) (Undo, error) {
	u, err := ReadUndo(path)
	if err != nil {
		return Undo{}, err
	}
	if err := u.Match(data); err != nil {
		return Undo{}, err
	}
	return u, nil
}

// Match reports whether the header still describes data.
//
// Size first and the digest second, which is not an optimisation worth naming
// on a 4 KB source file and is worth naming on the 40k-line log in the corpus:
// a changed file almost always changed length, and a length that differs costs
// nothing to notice where a sha256 costs a pass over every byte.
func (u Undo) Match(data []byte) error {
	if u.Size != int64(len(data)) {
		return fmt.Errorf("%w: %d bytes now, %d when the history was written", ErrStale, len(data), u.Size)
	}
	if Digest(data) != u.SHA {
		return fmt.Errorf("%w: same length, different contents", ErrStale)
	}
	return nil
}

// writeFileAtomic writes p to path through a temp file in the same directory.
//
// Same directory because rename is only atomic within a file system, and the
// undo directory and the temp directory are routinely on different ones.
func writeFileAtomic(path string, p []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".pvim-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once the rename has happened
	if _, err := f.Write(p); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
