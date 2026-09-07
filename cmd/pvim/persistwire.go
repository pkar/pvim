package main

import (
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/undofile"
)

// Wiring internal/undofile into the editor's life cycle.
//
// persist.go is the file layer and knows nothing about an editor; this is the
// six places the editor has to call it. They are all methods on editor and all
// of them tolerate a nil persist, because --oracle runs without one on purpose:
// a graded run that wrote a swap file into /tmp// would make two runs of the
// same script differ, and one that read an undo file would carry history
// between them.
//
// Every one of these swallows its error onto the message line rather than
// returning it. Persistence failing is not a reason to lose the buffer: a full
// disk must not stop a ":w", and an unreadable undo file must not stop the file
// opening. Vim takes the same view.

// initPersist builds the persist layer and restores what the last session left:
// the E325 prompt if that session crashed, the undo history if the file on disk
// is still the one it belongs to, the A-Z marks, and a fresh swap file.
//
// Called after the vimrc has been read, because 'undofile', 'undodir',
// 'directory' and 'swapfile' are all things the vimrc sets and all four decide
// what this does. The user's vimrc sets undofile with undodir=~/.cache/vim.
//
// It is also called before either frontend has started, which is what makes the
// prompt possible at all: see recover.go.
func (e *editor) initPersist(home string, data []byte) {
	if home == "" {
		return
	}
	e.pers = newPersist(home, e.opt)

	// The E325 prompt, first: a swap file left by a crashed session is the one
	// thing on the disk that must not be walked over, and the answer decides
	// whether this launch opens the file at all. See recover.go.
	act, on := e.recoverSwap(e.file)
	if !on {
		// (Q)uit and (A)bort. Nothing has been opened and nothing bound, so
		// there is nothing to unwind; vim answers both with exit status 1.
		exitPvim(1)
		return
	}

	if tr, ok, err := e.pers.loadUndo(e.file, data); err != nil {
		e.ed.Say(err.Error())
	} else if ok {
		nodes := make([]text.UndoNode, 0, len(tr.Nodes))
		for _, n := range tr.Nodes {
			un := text.UndoNode{
				Seq:     n.Seq,
				Parent:  n.Parent,
				Last:    n.Last,
				Cursor:  text.Pos{Line: n.Cursor.Line, Col: n.Cursor.Col},
				NoEOL:   n.NoEOL,
				Emptied: n.Emptied,
				NoLines: n.NoLines,
				When:    n.When,
			}
			for _, st := range n.Steps {
				un.Steps = append(un.Steps, text.UndoStep{
					First:    st.First,
					Lines:    st.Lines,
					Replaced: st.Replaced,
				})
			}
			nodes = append(nodes, un)
		}
		// A tree that does not validate is a file this did not write, or one a
		// half-finished write left behind. Say so and carry on with no history
		// rather than refusing to open the file.
		if err := e.buf.SetUndoTree(nodes, tr.Cur, tr.SeqMax, tr.RootLast); err != nil {
			e.ed.Say(err.Error())
		}
		// The restored tree carries the sequence number the last session
		// ended on, and ex.Buf reads "modified" as "the undo sequence has
		// moved since the last write". Without this a file whose history was
		// restored comes up with a "[+]" on a buffer nobody has touched, and
		// ":q" refuses it. The tree describes the bytes on the disk -- the
		// digest in its header is what says so -- so this buffer is saved.
		if b := e.cur(); b != nil {
			b.MarkSaved()
		}
	}

	// The answer to the prompt, on the buffer: recovered contents and a
	// modified buffer for (R)ecover, 'readonly' for [O]pen Read-Only, nothing
	// at all for (E)dit anyway and (D)elete it beyond the message.
	e.applySwap(act)

	// A-Z, and '" for this file, out of the history the last session wrote.
	e.restoreMarks()

	// [O]pen Read-Only gets no swap file of its own, which is vim: a read-only
	// buffer has nothing to recover and taking the name would be taking it off
	// the session that still needs it.
	if act.Swap {
		if err := e.pers.openSwap(e.file, e.buf, e.ed.Cursor()); err != nil {
			e.ed.Say(err.Error())
		}
	}
}

// restoreMarks puts back the marks the last session left in this file.
//
// A-Z and '", and only the ones that name this file. In vim an uppercase mark
// names a file as well as a position, so 'A from another buffer switches to the
// one A was set in; there is one buffer here and nothing to switch to, so a
// mark pointing somewhere else is left in the history file, where it stays
// valid and where a buffer list, once there is one, will find it.
//
// Every position is clamped. A mark written against a file that has
// been rewritten since can name a line that no longer exists, and a mark
// pointing past the end of the buffer would be an E20 at best.
func (e *editor) restoreMarks() {
	if e.pers == nil || e.file == "" || e.buf == nil {
		return
	}
	h, err := e.pers.loadHistory()
	if err != nil {
		return // a history that cannot be read is not a reason to fail an open
	}
	for _, m := range h.Marks {
		if m.Place.File != e.file {
			continue
		}
		e.buf.SetMark(m.Name, e.buf.Clamp(text.Pos{Line: m.Place.Pos.Line, Col: m.Place.Pos.Col}))
	}
	restoreJumps(e, h)

	// '" is where the cursor was when this file was last left, which vim keeps
	// in viminfo and which internal/text has had a name and no source for.
	// Setting the mark and not the cursor is vim: the jump to it is what the
	// "restore cursor position" autocmd everybody has in their vimrc does, and
	// this vimrc does not have one.
	for _, f := range h.Files {
		if f.File != e.file {
			continue
		}
		e.buf.SetMark(text.MarkLastExit, e.buf.Clamp(text.Pos{Line: f.Pos.Line, Col: f.Pos.Col}))
	}
}

// persistAfterKey syncs the swap file. It is called after every key because
// syncSwap writes nothing when nothing changed; see the note on it for why that
// is different from vim's 'updatecount' window, in which a crash costs work.
func (e *editor) persistAfterKey() {
	if e.pers == nil {
		return
	}
	_ = e.pers.syncSwap(e.buf, e.ed.Cursor())
}

// persistAfterWrite records that the buffer on disk now matches the buffer in
// memory, and saves the undo history against the bytes just written.
//
// The order matters: the undo file's header holds the sha256 of the file it
// belongs to, so it has to be written with the bytes that just landed on disk
// and not the ones that were there before.
func (e *editor) persistAfterWrite(file string, data []byte) {
	if e.pers == nil {
		return
	}
	if err := e.pers.savedSwap(file); err != nil {
		e.ed.Say(err.Error())
	}
	if err := e.pers.saveUndo(file, data, e.undoTree()); err != nil {
		e.ed.Say(err.Error())
	}
}

// persistOnQuit is the clean exit: the swap file goes away, because one left
// behind is an E325 on the next open about a crash that never happened, and the
// history and file marks are written.
//
// The undo history is NOT written here. It is written by a ":w", against the
// bytes that were written, and a session that quits without saving has a buffer
// whose history does not match anything on disk.
func (e *editor) persistOnQuit() {
	if e.pers == nil {
		return
	}
	_ = e.pers.closeSwap()

	h, err := e.pers.loadHistory()
	if err != nil {
		return // a history that cannot be read is not a reason to fail an exit
	}
	h = recordExit(h, e.file, e.ed.Cursor())
	h.Marks = mergeMarks(h.Marks, e.file, marksOf(e.file, e.buf))
	h.Jumps = jumpsOf(e.ed)
	_ = e.pers.saveHistory(h, e.opt.G.History)
}

// undoTree converts internal/text's flattened tree into the file layer's.
//
// Two packages with the same shape and no shared type is deliberate: text's is
// its internal model made public, undofile's is a format. Tying them together
// would mean a field added to the editor's undo node became a format break, and
// that node has grown three flags already.
func (e *editor) undoTree() undofile.Tree {
	nodes, cur, seqMax, rootLast := e.buf.UndoTree()
	tr := undofile.Tree{Cur: cur, SeqMax: seqMax, RootLast: rootLast}
	for _, n := range nodes {
		un := undofile.Node{
			Seq:     n.Seq,
			Parent:  n.Parent,
			Last:    n.Last,
			Cursor:  undofile.Pos{Line: n.Cursor.Line, Col: n.Cursor.Col},
			NoEOL:   n.NoEOL,
			Emptied: n.Emptied,
			NoLines: n.NoLines,
			When:    n.When,
		}
		for _, st := range n.Steps {
			un.Steps = append(un.Steps, undofile.Step{
				First:    st.First,
				Lines:    st.Lines,
				Replaced: st.Replaced,
			})
		}
		tr.Nodes = append(tr.Nodes, un)
	}
	return tr
}

// jumpsOf turns the live jumplist into the history's shape.
//
// An entry with no file name is dropped rather than written with an empty one:
// a jump the session made before the buffer was named cannot be returned to
// after a restart, and an empty name in the file would restore as a jump to
// whatever file happened to be open next.
func jumpsOf(ed *mode.Editor) []undofile.Place {
	list, _ := ed.JumpList()
	out := make([]undofile.Place, 0, len(list))
	for _, j := range list {
		if j.File == "" {
			continue
		}
		out = append(out, undofile.Place{
			File: j.File,
			Pos:  undofile.Pos{Line: j.Pos.Line, Col: j.Pos.Col},
		})
	}
	return out
}

// restoreJumps puts the last session's jumps under this one's.
//
// A buffer opens with one entry already in it, because vim's do_ecmd calls
// setpcmark, and that entry is the newest: the restored ones go underneath it
// so CTRL-O walks back out of this file and into the last session's. The index
// lands past the end, which is where a list nobody has walked yet sits, so the
// first CTRL-O reaches the newest entry rather than the one after it.
//
// No index is stored, which is what viminfo does: where the cursor was in a
// jumplist is a property of a session and not of a file.
func restoreJumps(e *editor, h undofile.History) {
	if len(h.Jumps) == 0 {
		return
	}
	open, _ := e.ed.JumpList()
	list := make([]mode.Jump, 0, len(h.Jumps)+len(open))
	for _, p := range h.Jumps {
		list = append(list, mode.Jump{
			File: p.File,
			Pos:  text.Pos{Line: p.Pos.Line, Col: p.Pos.Col},
		})
	}
	list = append(list, open...)
	e.ed.SetJumpList(list, len(list))
}
