package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The buffer swap hook, which is editor.open and the two things it has to do
// for a buffer that arrives after startup: fire the events a buffer opening
// fires, and give it the persistence the file on the command line got.
//
// cmd/pvim/filetype_test.go owns the first half. This file owns the second.

// TestASecondFileGetsItsUndoHistory is the hole `pvim file` in a second shell
// fell through.
//
// The socket hands the file to the running instance, the instance opens it
// through the ex layer, and initPersist had already run against the file the
// instance was started on -- so the second file came up with no undo history
// from the last time it was edited, no swap check and none of its marks, and
// nothing said so. ":e" fell through the same hole, which is what this test
// drives, because it is the same hook and it needs no socket.
func TestASecondFileGetsItsUndoHistory(t *testing.T) {
	home := wireHome(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	for _, f := range []string{first, second} {
		if err := os.WriteFile(f, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A session that edits second.txt and leaves history behind for it.
	past := wired(t, home, second)
	typeRunes(t, past, "x")
	if err := past.write(); err != nil {
		t.Fatalf("write: %v", err)
	}
	past.persistOnQuit()

	// A new session over first.txt, which is the file the instance was
	// started on, and then second.txt arriving afterwards.
	e := wired(t, home, first)
	if err := e.ctx.Run("edit " + second); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := filepath.Base(e.file); got != "second.txt" {
		t.Fatalf("the editor is on %q after the ':e'", got)
	}
	if _, ok := e.buf.Undo(); !ok {
		t.Fatal("u on the file that arrived second did nothing; it got no undo history")
	}
	if got, want := string(e.buf.Bytes()), "one\ntwo\nthree\n"; got != want {
		t.Errorf("one undo gave %q, want %q", got, want)
	}
}

// TestASecondFileTakesTheSwapFileWithIt.
//
// A persist holds one swap file and an editor holds one persist, so the buffer
// being left has to give its swap file up before the one arriving takes it.
// Without that the first file's swap stays on the disk with nothing left to
// remove it, and the next session that opens it answers an E325 about a crash
// that never happened.
func TestASecondFileTakesTheSwapFileWithIt(t *testing.T) {
	home := wireHome(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	for _, f := range []string{first, second} {
		if err := os.WriteFile(f, []byte("one\ntwo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	e := wired(t, home, first)
	firstSwap := e.pers.swapPath(first)
	if firstSwap == "" {
		t.Fatal("no swap path for the first file")
	}
	if _, err := os.Stat(firstSwap); err != nil {
		t.Fatalf("the first file has no swap file: %v", err)
	}

	if err := e.ctx.Run("edit " + second); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if _, err := os.Stat(firstSwap); !os.IsNotExist(err) {
		t.Errorf("the first file's swap file is still there after the buffer was left: %v", err)
	}
	secondSwap := e.pers.swapPath(second)
	if _, err := os.Stat(secondSwap); err != nil {
		t.Errorf("the file that arrived second has no swap file: %v", err)
	}
}

// TestOracleStillGetsNoPersistence is the invariant the hook must not break:
// an editor built the way --oracle builds one has a nil persist, and a nil
// persist has to stay nil however many buffers arrive. A graded run that wrote
// a swap file into /tmp// would make two runs of one script differ.
func TestOracleStillGetsNoPersistence(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t, "aaaa\nbbbb\n")
	if e.pers != nil {
		t.Fatal("an editor built without initPersist already has a persist")
	}
	if err := e.ctx.Run("edit " + other); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if e.pers != nil {
		t.Error("opening a second buffer installed persistence on a run that must not have any")
	}
}
