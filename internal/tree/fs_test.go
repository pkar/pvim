package tree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddAFileAndADirectory(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	tr := newTree(t, dir)

	// A name with no trailing separator makes a file, which is what the
	// prompt says: "Dirs end with a '/'".
	n, err := tr.Add(tr.Root(), "b.go")
	if err != nil {
		t.Fatal(err)
	}
	if n == nil || n.Dir {
		t.Fatalf("Add made %+v", n)
	}
	if st, err := os.Stat(filepath.Join(dir, "b.go")); err != nil || st.IsDir() {
		t.Fatalf("b.go on disk: %v", err)
	}
	if !strings.Contains(text(t, tr), "b.go") {
		t.Error("the new file is not in the tree")
	}

	// A trailing separator makes a directory, and missing parents are made
	// with it (Path.Create does mkdir with 'p').
	d, err := tr.Add(tr.Root(), "x/y/")
	if err != nil {
		t.Fatal(err)
	}
	if d == nil || !d.Dir {
		t.Fatalf("Add made %+v", d)
	}
	if st, err := os.Stat(filepath.Join(dir, "x/y")); err != nil || !st.IsDir() {
		t.Fatalf("x/y on disk: %v", err)
	}
}

func TestAddOnAFileUsesItsDirectory(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "sub/a.go")
	tr := newTree(t, dir)
	sub := tr.Find(filepath.Join(dir, "sub"))
	tr.Open(sub)
	a := tr.Find(filepath.Join(dir, "sub/a.go"))

	if _, err := tr.Add(a, "b.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub/b.go")); err != nil {
		t.Errorf("adding a node with a file selected did not use its directory: %v", err)
	}
}

func TestAddRefusesTheExistingAndTheEmpty(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	tr := newTree(t, dir)
	if _, err := tr.Add(tr.Root(), "a.go"); !errors.Is(err, ErrExists) {
		t.Errorf("adding over an existing file gave %v", err)
	}
	if _, err := tr.Add(tr.Root(), "   "); !errors.Is(err, ErrAborted) {
		t.Errorf("an empty answer gave %v", err)
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	touch(t, dir, "sub/deep/x.go")
	tr := newTree(t, dir)

	a := tr.Find(filepath.Join(dir, "a.go"))
	if err := tr.Delete(a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.go")); !os.IsNotExist(err) {
		t.Error("a.go is still there")
	}
	if strings.Contains(text(t, tr), "a.go") {
		t.Error("a.go is still in the tree")
	}

	// A directory goes with everything under it, which is the rm -rf
	// nerdtree shells out to, and it is why the caller asks for the word
	// "yes" first.
	sub := tr.Find(filepath.Join(dir, "sub"))
	if !NotEmpty(sub) {
		t.Fatal("a directory with a file in it reported empty")
	}
	if err := tr.Delete(sub); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Error("sub is still there")
	}
}

func TestDeleteRefusesTheRoot(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	tr := newTree(t, dir)
	if err := tr.Delete(tr.Root()); !errors.Is(err, ErrAborted) {
		t.Errorf("deleting the root gave %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("the root was deleted")
	}
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "old.go")
	touch(t, dir, "taken.go")
	tr := newTree(t, dir)
	old := tr.Find(filepath.Join(dir, "old.go"))

	from, to, err := tr.Rename(old, "new.go")
	if err != nil {
		t.Fatal(err)
	}
	if from != filepath.Join(dir, "old.go") || to != filepath.Join(dir, "new.go") {
		t.Fatalf("Rename reported %q -> %q", from, to)
	}
	if _, err := os.Stat(to); err != nil {
		t.Errorf("new.go is not on disk: %v", err)
	}
	got := text(t, tr)
	if strings.Contains(got, "old.go") || !strings.Contains(got, "new.go") {
		t.Errorf("the tree still shows the old name:\n%s", got)
	}

	// Into a directory that does not exist yet, which the prompt allows
	// because it is a path and not a name.
	n := tr.Find(filepath.Join(dir, "new.go"))
	if _, _, err := tr.Rename(n, "sub/moved.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub/moved.go")); err != nil {
		t.Errorf("the move did not make its parent: %v", err)
	}

	taken := tr.Find(filepath.Join(dir, "taken.go"))
	if _, _, err := tr.Rename(taken, "sub"); !errors.Is(err, ErrExists) {
		t.Errorf("renaming onto an existing path gave %v", err)
	}
	if _, _, err := tr.Rename(taken, ""); !errors.Is(err, ErrAborted) {
		t.Errorf("an empty answer gave %v", err)
	}
}

func TestMenuStringsAreNerdtrees(t *testing.T) {
	// The wording is the plugin's and a person reads it every time they press
	// "m", so it is pinned rather than left to drift.
	for _, want := range []string{"(a)dd a childnode", "(m)ove the current node", "(d)elete the current node"} {
		if !strings.Contains(MenuPrompt, want) {
			t.Errorf("the menu does not offer %q: %q", want, MenuPrompt)
		}
	}
	if !strings.Contains(DeleteDirPrompt, "type 'yes'") {
		t.Errorf("the non-empty directory prompt is %q", DeleteDirPrompt)
	}
}
