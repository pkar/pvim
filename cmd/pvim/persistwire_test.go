package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/text"
)

// wireHome gives an editor a private HOME so a test never touches the real
// ~/.cache/vim. Everything persistence writes hangs off it.
func wireHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// wired builds an editor over file with persistence installed the way
// newFrontendEditor installs it, which is the path both frontends take and
// --oracle does not.
func wired(t *testing.T, home, file string) *editor {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	e, err := newEditor(data, file, 24, 80, "")
	if err != nil {
		t.Fatalf("newEditor: %v", err)
	}
	e.opt.B.UndoFile = true
	e.opt.G.UndoDir = filepath.Join(home, "undo")
	e.opt.G.Directory = filepath.Join(home, "swap") + "//"
	e.initPersist(home, data)
	if e.pers == nil {
		t.Fatal("initPersist installed nothing")
	}
	return e
}

// typeRunes feeds a string to the editor one rune at a time, through the same
// editor.key every real keystroke goes through, so the swap file is synced
// after each one. keymap_test.go's typeKeys is the other one: it reads vim's
// <> notation, which nothing here needs.
func typeRunes(t *testing.T, e *editor, s string) {
	t.Helper()
	for _, r := range s {
		e.key(key.Rune(r))
	}
}

// TestUndoSurvivesARestart is the feature in one test: edit, save, quit, come
// back, and "u" takes back's change.
//
// It matters more than it looks. The user's vimrc has set 'undofile' with
// undodir=~/.cache/vim for years and that directory never existed, so MacVim
// silently never persisted a single undo step on this machine. This is the
// first time the option does anything.
func TestUndoSurvivesARestart(t *testing.T) {
	home := wireHome(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := wired(t, home, file)
	typeRunes(t, first, "xx") // two changes on line one
	if err := first.write(); err != nil {
		t.Fatalf("write: %v", err)
	}
	afterEdit, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterEdit) != "e\ntwo\nthree\n" {
		t.Fatalf("edit gave %q", afterEdit)
	}
	first.persistOnQuit()

	// A whole new editor over the same file, as if the process had restarted.
	second := wired(t, home, file)
	if _, ok := second.buf.Undo(); !ok {
		t.Fatal("u after a restart did nothing; the history did not survive")
	}
	if got := string(second.buf.Bytes()); got != "ne\ntwo\nthree\n" {
		t.Errorf("one undo gave %q, want %q", got, "ne\ntwo\nthree\n")
	}
	if _, ok := second.buf.Undo(); !ok {
		t.Fatal("the second undo did nothing; only one step survived")
	}
	if got, want := string(second.buf.Bytes()), "one\ntwo\nthree\n"; got != want {
		t.Errorf("two undos gave %q, want %q", got, want)
	}
}

// TestUndoIsRefusedWhenTheFileChangedUnderneath is the safety half. History that
// no longer matches the bytes on disk would put back lines that were never
// there, so the sha256 in the undo file's header has to refuse it.
func TestUndoIsRefusedWhenTheFileChangedUnderneath(t *testing.T) {
	home := wireHome(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := wired(t, home, file)
	typeRunes(t, first, "x")
	if err := first.write(); err != nil {
		t.Fatalf("write: %v", err)
	}
	first.persistOnQuit()

	// Somebody else edits the file: git checkout, another editor, a script.
	if err := os.WriteFile(file, []byte("something else entirely\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := wired(t, home, file)
	if _, ok := second.buf.Undo(); ok {
		t.Error("stale history was applied to a file that had changed underneath")
	}
	if got, want := string(second.buf.Bytes()), "something else entirely\n"; got != want {
		t.Errorf("buffer is %q, want %q", got, want)
	}
}

// TestSwapGoesAwayOnACleanExit. A swap file left behind by an editor that quit
// properly raises E325 on the next open about a crash that never happened, and
// people learn to answer that prompt without reading it.
func TestSwapGoesAwayOnACleanExit(t *testing.T) {
	home := wireHome(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := wired(t, home, file)
	typeRunes(t, e, "x")

	if e.pers.swapCheck(file) == nil {
		t.Fatal("no swap file while editing; a crash here would lose the buffer")
	}
	e.persistOnQuit()
	if info := e.pers.swapCheck(file); info != nil {
		t.Error("the swap file outlived a clean exit")
	}
}

// TestOracleGetsNoPersistence pins the boundary that keeps graded runs
// reproducible: --oracle builds its editor through newEditor directly, so it
// writes no swap file and reads no history. A run that did either would make
// two runs of one script differ and the whole oracle with it.
func TestOracleGetsNoPersistence(t *testing.T) {
	e, err := newEditor([]byte("one\n"), "notes.txt", 24, 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.pers != nil {
		t.Fatal("newEditor installed persistence; --oracle would write a swap file")
	}
	// Every hook has to tolerate that rather than panic.
	e.key(key.Rune('x'))
	e.persistAfterWrite("notes.txt", []byte("ne\n"))
	e.persistOnQuit()
}

// TestPersistHooksSurviveNoHome. A launch with no home directory gets no
// persistence and must still edit; losing the buffer over a missing cache
// directory would be a worse bug than the one persistence fixes.
func TestPersistHooksSurviveNoHome(t *testing.T) {
	e, err := newEditor([]byte("one\n"), "notes.txt", 24, 80, "")
	if err != nil {
		t.Fatal(err)
	}
	e.initPersist("", nil)
	if e.pers != nil {
		t.Error("an empty home installed persistence")
	}
	e.key(key.Rune('x'))
	if got, want := string(e.buf.Bytes()), "ne\n"; got != want {
		t.Errorf("editing broke without a home: %q, want %q", got, want)
	}
}

// TestUndoTreeCrossesTheFormatIntact walks a branched history through the
// editor's own converters. The two packages keep separate node types on
// purpose, and a field added to one and forgotten in the other is exactly the
// bug that would show up as an undo landing on the wrong line months later.
func TestUndoTreeCrossesTheFormatIntact(t *testing.T) {
	home := wireHome(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := wired(t, home, file)
	typeRunes(t, e, "x")
	e.key(key.Rune('j'))
	typeRunes(t, e, "x")
	e.buf.Undo()
	typeRunes(t, e, "x") // a branch: a sibling of the step just undone

	tr := e.undoTree()
	if len(tr.Nodes) != 3 {
		t.Fatalf("%d nodes, want 3", len(tr.Nodes))
	}
	// Node 3 branches off node 1, which is the shape a stack cannot hold.
	if tr.Nodes[2].Parent != 1 {
		t.Errorf("node 3's parent is %d, want 1", tr.Nodes[2].Parent)
	}
	for i, n := range tr.Nodes {
		if n.When.IsZero() {
			t.Errorf("node %d has no timestamp; the format would restore it at the epoch", i+1)
		}
		if len(n.Steps) == 0 {
			t.Errorf("node %d crossed with no steps", i+1)
		}
	}
	if tr.Cur != 3 || tr.SeqMax != 3 {
		t.Errorf("cur=%d seqMax=%d, want 3 and 3", tr.Cur, tr.SeqMax)
	}

	// And the cursor each node restores has to cross too: it is most of the
	// work in internal/text's undo and all of it is wasted if it is dropped
	// here.
	var zero text.Pos
	for i, n := range tr.Nodes {
		if (text.Pos{Line: n.Cursor.Line, Col: n.Cursor.Col}) == zero {
			t.Errorf("node %d crossed with no cursor", i+1)
		}
	}
}

// The marks that outlive a session: A-Z, which vim calls file marks because
// each one names a file as well as a position, and '", where the cursor was
// when the file was last left.
//
// internal/text has stored A-Z and persist.go has written them to
// the history file; what was missing until now is the launch
// reading them back, so a mark set on Monday was on the disk all week and
// nothing ever looked at it.

// TestFileMarksSurviveARestart is the feature in one test: mA on Monday, 'A on
// Tuesday.
func TestFileMarksSurviveARestart(t *testing.T) {
	home := wireHome(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := wired(t, home, file)
	typeRunes(t, first, "jjmA") // line three, mark A
	if at, ok := first.buf.Mark('A'); !ok || at.Line != 3 {
		t.Fatalf("mA in the first session put A at %+v (set=%v)", at, ok)
	}
	first.persistOnQuit()

	second := wired(t, home, file)
	at, ok := second.buf.Mark('A')
	if !ok {
		t.Fatal("'A after a restart is E20; the mark did not survive")
	}
	if at.Line != 3 {
		t.Errorf("A came back on line %d, want 3", at.Line)
	}
}

// TestMarksInOtherFilesAreNotLost. A-Z are one set across all files in vim, not
// one per file, so exiting an editor on one file must not empty the set.
//
// This was a real bug and it is the reason mergeMarks exists: persistOnQuit
// wrote marksOf straight into History.Marks, so the first exit threw away every
// mark that named any other file.
func TestMarksInOtherFilesAreNotLost(t *testing.T) {
	home := wireHome(t)
	dir := t.TempDir()
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	for _, f := range []string{one, two} {
		if err := os.WriteFile(f, []byte("a\nb\nc\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a := wired(t, home, one)
	typeRunes(t, a, "jmA") // A is in one.txt, line two
	a.persistOnQuit()

	b := wired(t, home, two)
	typeRunes(t, b, "jjmB") // B is in two.txt, line three
	b.persistOnQuit()

	// One more session on one.txt, which is both what reads the history back
	// and what the marks have to land in. Two editors would leave the first
	// one's swap file open and raise E325 on the second, which is right and is
	// not what this test is about.
	third := wired(t, home, one)
	h, err := third.pers.loadHistory()
	if err != nil {
		t.Fatal(err)
	}
	got := map[byte]string{}
	for _, m := range h.Marks {
		got[m.Name] = m.Place.File
	}
	if got['A'] != one {
		t.Errorf("A names %q, want %q", got['A'], one)
	}
	if got['B'] != two {
		t.Errorf("B names %q, want %q; exiting one.txt's editor lost it", got['B'], two)
	}

	// And a mark that names another file is not put into this buffer: there is
	// one buffer here and 'B would have to open two.txt to be honest.
	if at, ok := third.buf.Mark('B'); ok {
		t.Errorf("B, which is in two.txt, was restored into one.txt at %+v", at)
	}
	if at, ok := third.buf.Mark('A'); !ok || at.Line != 2 {
		t.Errorf("A came back at %+v (set=%v), want line 2", at, ok)
	}
}

// TestLastExitMarkComesBack. '" is where the cursor was when the file was last
// left, which vim keeps in viminfo and which internal/text has had a name and
// no source for: a freshly read buffer put it on line one because
// there was nothing else to put it on.
func TestLastExitMarkComesBack(t *testing.T) {
	home := wireHome(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := wired(t, home, file)
	typeRunes(t, first, "jjj")
	first.persistOnQuit()

	second := wired(t, home, file)
	at, ok := second.buf.Mark(text.MarkLastExit)
	if !ok {
		t.Fatal(`'" is not set after a restart`)
	}
	if at.Line != 4 {
		t.Errorf(`'" came back on line %d, want 4`, at.Line)
	}
	// The cursor itself stays on line one. Jumping to '" is what the "restore
	// cursor position" autocmd in everybody's vimrc does, and this vimrc does
	// not have one.
	if got := second.ed.Cursor().Line; got != 1 {
		t.Errorf("the cursor started on line %d; pvim jumped to '\" by itself", got)
	}
}

// TestRestoredHistoryIsNotModified. A buffer whose undo history was restored
// matches the bytes on the disk -- the digest in the undo file's header is what
// says so -- and must not come up with a "[+]" on it.
//
// Before this, SetUndoTree put the last session's sequence number on a buffer
// whose savedSeq was zero, so every launch of a file with a history showed it
// as modified and ":q" refused it.
func TestRestoredHistoryIsNotModified(t *testing.T) {
	home := wireHome(t)
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := wired(t, home, file)
	typeRunes(t, first, "x")
	if err := first.write(); err != nil {
		t.Fatal(err)
	}
	first.persistOnQuit()

	second := wired(t, home, file)
	if _, ok := second.buf.Undo(); !ok {
		t.Fatal("the history did not survive; this test is not measuring what it thinks")
	}
	second.buf.Redo()
	if second.modified() {
		t.Error("a buffer with a restored history came up modified")
	}
}
