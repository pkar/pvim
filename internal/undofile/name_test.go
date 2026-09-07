package undofile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The three names vim wrote, measured against vim 9.2.0321 by
// starting it on a pseudo-terminal over /private/tmp/.../work/f.txt with each
// value of 'directory' in turn and listing the directory:
//
//	directory=DIR// -> DIR/%private%tmp%...%work%f.txt.swp
//	directory=DIR -> DIR/f.txt.swp
//	directory=. -> <the file's own directory>/.f.txt.swp
//
// The first is what the vimrc asks for. TestPercentPathMatchesVim holds the
// mangling itself against the literal name that run produced, because that is
// the half a temporary directory cannot pin.
func TestSwapNameMatchesVim(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	swaps := filepath.Join(dir, "swap")
	for _, d := range []string{work, swaps} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(work, "f.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	full, err := resolve(file)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		dir  string
		want string
	}{
		{swaps + "//", filepath.Join(swaps, percentPath(full)+".swp")},
		{swaps, filepath.Join(swaps, "f.txt.swp")},
		{".", filepath.Join(filepath.Dir(full), ".f.txt.swp")},
	}
	for _, c := range cases {
		got, err := SwapName(c.dir, file)
		if err != nil {
			t.Fatalf("SwapName(%q): %v", c.dir, err)
		}
		if got != c.want {
			t.Errorf("SwapName(%q) = %q, want %q", c.dir, got, c.want)
		}
	}
}

// The literal vim wrote. A run over /private/tmp/a/b/work/f.txt left a swap
// file named %private%tmp%a%b%work%f.txt.swp: every separator a '%', nothing
// else touched, ".swp" on the end.
func TestPercentPathMatchesVim(t *testing.T) {
	const full = "/private/tmp/a/b/work/f.txt"
	const want = "%private%tmp%a%b%work%f.txt"
	if got := percentPath(full); got != want {
		t.Errorf("percentPath(%q)\n = %q\nwant %q", full, got, want)
	}
}

// 'undodir' has one shape and not two: anything other than "." encodes the
// whole path whether or not it ends in "//", which is what ":help 'undodir'"
// describes and is why the vimrc's trailing slashes there are decoration.
func TestUndoNameAlwaysEncodesThePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	full, err := resolve(file)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, percentPath(full)+undoSuffix)
	for _, d := range []string{dir, dir + "/", dir + "//"} {
		got, err := UndoName(d, file)
		if err != nil {
			t.Fatalf("UndoName(%q): %v", d, err)
		}
		if got != want {
			t.Errorf("UndoName(%q) = %q, want %q", d, got, want)
		}
	}
	got, err := UndoName(".", file)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(full), ".f.txt"+undoSuffix); got != want {
		t.Errorf("UndoName(\".\") = %q, want %q", got, want)
	}
}

// The undo file's suffix is not vim's, so that both editors keep their own
// history through the cutover instead of each answering E824 over the other's.
func TestUndoNameIsNotVimsName(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	undo, err := UndoName(dir+"//", file)
	if err != nil {
		t.Fatal(err)
	}
	full, _ := resolve(file)
	// What vim would have called it: the mangled path and nothing else.
	if vim := filepath.Join(dir, percentPath(full)); undo == vim {
		t.Errorf("pvim and vim would write the same file: %q", undo)
	}
	if filepath.Ext(undo) != undoSuffix {
		t.Errorf("undo file %q does not end in %q", undo, undoSuffix)
	}
}

// A file that does not exist yet still gets a swap file, and its name still
// resolves the directories above it: ":e newfile" in /tmp has to produce
// %private%tmp%newfile and not %tmp%newfile, or a crash before the first write
// leaves a swap file the next open never looks for.
func TestSwapNameForAFileThatIsNotThereYet(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := SwapName(dir+"//", filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, percentPath(filepath.Join(resolved, "new.txt"))+".swp")
	if got != want {
		t.Errorf("SwapName = %q, want %q", got, want)
	}
}

func TestDirs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		home string
		want []string
	}{
		{"vim's default", ".,~/tmp,/var/tmp,/tmp", "/home/p", []string{".", "/home/p/tmp", "/var/tmp", "/tmp"}},
		{"the vimrc's", "~/.vim/state/swap//", "", []string{"~/.vim/state/swap//"}},
		{"a tilde keeps its slashes", "~/.cache/vim//", "/home/p", []string{"/home/p/.cache/vim//"}},
		{"an escaped comma", `/a\,b,/c`, "", []string{"/a,b", "/c"}},
		{"empty entries dropped", ",,/tmp,", "", []string{"/tmp"}},
		{"nothing at all", "", "", nil},
		{"bare tilde", "~", "/home/p", []string{"/home/p"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Dirs(c.in, c.home); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Dirs(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNameNeedsADirectoryAndAFile(t *testing.T) {
	if _, err := SwapName("", "/tmp/f.txt"); err == nil {
		t.Error("an empty 'directory' entry produced a name")
	}
	if _, err := SwapName("/tmp//", ""); err == nil {
		t.Error("a buffer with no name produced a swap file name")
	}
}
