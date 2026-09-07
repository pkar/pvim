package text

import "time"

// Serialising the undo tree, for internal/undofile.
//
// The tree itself is parent POINTERS plus an index by sequence number, which is
// the right shape for u, C-r, g- and g+ and the wrong shape for a file. These
// two methods convert between it and a flat node list addressed by sequence
// number, which is a shape a file can hold and a reader can validate.
//
// The exported types are a copy rather than the internal ones on purpose. A
// format is a promise and the internal node is not: undoNode has grown noEOL,
// emptied and noLines as measurements against vim demanded them, and every one
// of those would have been a format break if the file had been writing the
// struct directly.
//
// The direction of a node's entries is not part of this interface, and that is
// the subtle part. undoNode.entries are stored in the order applying them must
// walk, which reverses each time the node is applied, so a node that has been
// undone holds its entries the other way round from one that has not. Applying
// is an involution, so a dump and a restore that keep the entries in whatever
// order they were found, and restore undoCur along with them, is correct: the
// pair (entries, cur) is consistent however the session was left.

// UndoStep is one recorded replacement inside an undo node: the lines that were
// there before, and how many lines they stood in for.
type UndoStep struct {
	// First is the 1-based line the run starts at.
	First int
	// Lines is the text to put back. A nil Lines with a non-zero Replaced is a
	// deletion; the reverse is an insertion.
	Lines [][]byte
	// Replaced is how many lines the run covers now.
	Replaced int
}

// UndoNode is one undo step, flattened. Parent and Last are sequence numbers
// rather than pointers, and -1 means none. The root is sequence 0 and is never
// in a node list: it is the state the file was read in and it holds nothing but
// its Last, which UndoTree returns separately.
type UndoNode struct {
	Seq    int
	Parent int
	Last   int
	Cursor Pos
	// NoEOL, Emptied and NoLines are the flags this step restores. See the
	// comments on undoNode; each one is a measurement against vim and skipping
	// it writes a file with a byte nobody typed, or none at all.
	NoEOL   bool
	Emptied bool
	NoLines bool
	// When the step was closed. Kept so a restored tree can answer "N seconds
	// ago" honestly; nothing reads it yet, which is why the register D-007
	// still stands.
	When  time.Time
	Steps []UndoStep
}

// UndoTree flattens the tree for writing. cur is the sequence number the buffer
// is currently at, seqMax the highest ever allocated, and rootLast the child the
// root's "C-r" follows.
//
// rootLast is returned separately because the root is not in the node list and
// nothing else can carry it. Without it a session undone all the way back and
// then quit comes up unable to redo, which is a bug that only shows itself the
// morning after.
func (b *Buffer) UndoTree() (nodes []UndoNode, cur, seqMax, rootLast int) {
	b.CloseUndoBlock()

	seqOf := func(n *undoNode) int {
		if n == nil {
			return -1
		}
		return n.seq
	}

	// undoBySeq is indexed by sequence number with the root at 0, so the node
	// list is everything after it.
	for _, n := range b.undoBySeq[1:] {
		un := UndoNode{
			Seq:     n.seq,
			Parent:  seqOf(n.parent),
			Last:    seqOf(n.last),
			Cursor:  n.cursor,
			NoEOL:   n.noEOL,
			Emptied: n.emptied,
			NoLines: n.noLines,
			When:    n.when,
		}
		for _, e := range n.entries {
			st := UndoStep{First: e.first, Replaced: e.replaced}
			for _, ln := range e.lines {
				st.Lines = append(st.Lines, append([]byte(nil), ln...))
			}
			un.Steps = append(un.Steps, st)
		}
		nodes = append(nodes, un)
	}
	return nodes, b.undoCur.seq, b.undoSeqMax, seqOf(b.undoBySeq[0].last)
}

// SetUndoTree replaces the buffer's history with a tree read from a file. It
// validates before it mutates, so a corrupt or hostile file leaves the buffer
// with the history it already had rather than half of somebody else's.
//
// The caller has already checked that the file on disk is the one this history
// belongs to; that is a sha256 in the undo file's header, not this method's job.
func (b *Buffer) SetUndoTree(nodes []UndoNode, cur, seqMax, rootLast int) error {
	if seqMax < 0 || cur < 0 || cur > seqMax {
		return &UndoTreeError{"cur out of range"}
	}
	// Sequence numbers index the slice, so they must be exactly 1..len(nodes)
	// in order. A file that says otherwise is not one this wrote.
	for i, n := range nodes {
		if n.Seq != i+1 {
			return &UndoTreeError{"sequence numbers are not 1..n in order"}
		}
		if n.Parent < 0 || n.Parent >= n.Seq {
			return &UndoTreeError{"parent is not an earlier node"}
		}
		if n.Last != -1 && (n.Last <= n.Seq || n.Last > len(nodes)) {
			return &UndoTreeError{"redo child is not a later node"}
		}
	}
	if len(nodes) > seqMax {
		return &UndoTreeError{"more nodes than sequence numbers"}
	}
	if cur > len(nodes) {
		return &UndoTreeError{"cur names a node that is not here"}
	}
	if rootLast != -1 && (rootLast < 1 || rootLast > len(nodes)) {
		return &UndoTreeError{"root's redo child is not a node"}
	}

	// Parent is always an earlier node, checked above, so one forward pass
	// resolves every pointer without a second walk.
	root := &undoNode{seq: 0}
	bySeq := make([]*undoNode, len(nodes)+1)
	bySeq[0] = root
	for _, n := range nodes {
		node := &undoNode{
			seq:     n.Seq,
			parent:  bySeq[n.Parent],
			cursor:  n.Cursor,
			noEOL:   n.NoEOL,
			emptied: n.Emptied,
			noLines: n.NoLines,
			when:    n.When,
		}
		for _, st := range n.Steps {
			e := undoEntry{first: st.First, replaced: st.Replaced}
			for _, ln := range st.Lines {
				e.lines = append(e.lines, append([]byte(nil), ln...))
			}
			node.entries = append(node.entries, e)
		}
		bySeq[n.Seq] = node
	}
	for _, n := range nodes {
		if n.Last != -1 {
			bySeq[n.Seq].last = bySeq[n.Last]
		}
	}
	if rootLast != -1 {
		root.last = bySeq[rootLast]
	}

	b.undoOpen = nil
	b.undoHold = 0
	b.undoBySeq = bySeq
	b.undoCur = bySeq[cur]
	b.undoSeqMax = seqMax
	return nil
}

// UndoTreeError is a refusal to load a history file. It is deliberately its own
// type: the caller deletes the file and carries on with no history, which is a
// different response from a read error it should report.
type UndoTreeError struct{ Reason string }

func (e *UndoTreeError) Error() string { return "undo tree: " + e.Reason }
