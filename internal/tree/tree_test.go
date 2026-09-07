package tree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/key"
)

// vimrcIgnore is g:NERDTreeIgnore as internal/vimrc/testdata/vimrc writes it,
// copied rather than paraphrased.
var vimrcIgnore = []string{`\~$`, `\.pyc$`, `\*NTUSER*`, `\*ntuser*`, `\NTUSER.DAT`, `\ntuser.ini`}

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func touch(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// sample is the shape most of these tests want: a repository with a dotfile, a
// dot directory, a nested directory and a couple of files nerdtree's ignore
// list has an opinion about.
func sample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	touch(t, dir, ".git/HEAD")
	touch(t, dir, ".gitignore")
	touch(t, dir, "README.md")
	touch(t, dir, "main.go")
	touch(t, dir, "main.go~")
	touch(t, dir, "build.pyc")
	touch(t, dir, "internal/text/buffer.go")
	mkdir(t, dir, "node_modules")
	return dir
}

func text(t *testing.T, tr *Tree) string {
	t.Helper()
	var b strings.Builder
	for _, l := range tr.Lines() {
		b.Write(l)
		b.WriteByte('\n')
	}
	return b.String()
}

func newTree(t *testing.T, dir string) *Tree {
	t.Helper()
	tr, err := New(dir, true, vimrcIgnore)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestRenderShape pins one whole tree, header and all, because the indent and
// the arrows are the thing a person recognises and a table of separate
// assertions would let any one of them drift.
func TestRenderShape(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	touch(t, dir, "sub/b.go")
	tr := newTree(t, dir)
	tr.Open(tr.Find(filepath.Join(dir, "sub")))

	want := `" Press ? for help

.. (up a dir)
` + dir + `/
▾ sub/
    b.go
  a.go
`
	if got := text(t, tr); got != want {
		t.Errorf("the tree drew\n%s\nwant\n%s", got, want)
	}
}

// TestTheVimrcsTree is the tree half of the gate, and the place where the
// gate and the plugin disagree. Dotfiles are shown, the ignore list is
// applied, and .git and node_modules are in the tree because nerdtree under
// this vimrc puts them there. See the package comment.
func TestTheVimrcsTree(t *testing.T) {
	dir := sample(t)
	tr := newTree(t, dir)
	got := text(t, tr)

	for _, want := range []string{".git/", ".gitignore", "README.md", "main.go", "node_modules/"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the tree:\n%s", want, got)
		}
	}
	// '\~$' and '\.pyc$' are two of the vimrc's own patterns.
	for _, gone := range []string{"main.go~", "build.pyc"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived NERDTreeIgnore:\n%s", gone, got)
		}
	}
}

func TestHiddenFilesAndTheIKey(t *testing.T) {
	dir := sample(t)
	tr := newTree(t, dir)
	if !strings.Contains(text(t, tr), ".gitignore") {
		t.Fatal("NERDTreeShowHidden=1 and no dotfile in the tree")
	}
	tr.Key(key.Rune('I'), tr.FirstNodeLine())
	if strings.Contains(text(t, tr), ".gitignore") {
		t.Error("I did not hide the dotfiles")
	}
	if !strings.Contains(text(t, tr), "README.md") {
		t.Error("I hid a file that does not start with a dot")
	}
	tr.Key(key.Rune('I'), tr.FirstNodeLine())
	if !strings.Contains(text(t, tr), ".gitignore") {
		t.Error("I did not bring the dotfiles back")
	}
}

// TestIKeepsTheTreeOpen is the reason ToggleHidden reloads rather than
// rebuilding: hitting I twice must not collapse everything that was expanded.
func TestIKeepsTheTreeOpen(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "sub/deep/x.go")
	tr := newTree(t, dir)
	sub := tr.Find(filepath.Join(dir, "sub"))
	tr.Open(sub)
	tr.Open(tr.Find(filepath.Join(dir, "sub/deep")))
	before := text(t, tr)
	tr.ToggleHidden()
	tr.ToggleHidden()
	if got := text(t, tr); got != before {
		t.Errorf("I twice changed the tree:\n%s\nwas\n%s", got, before)
	}
}

func TestIgnoreSuffixes(t *testing.T) {
	dir := t.TempDir()
	mkdir(t, dir, "build")
	touch(t, dir, "build.txt")
	touch(t, dir, "keep.txt")

	// [[dir]] means directories only, [[file]] files only, and a bare pattern
	// means both. lib/nerdtree/path.vim:502.
	cases := []struct {
		pattern    string
		gone, kept string
	}{
		{`^build`, "build/", "keep.txt"},
		{`^build$[[dir]]`, "build/", "build.txt"},
		{`^build\.txt$[[file]]`, "build.txt", "build/"},
	}
	for _, c := range cases {
		tr, err := New(dir, true, []string{c.pattern})
		if err != nil {
			t.Fatalf("%s: %v", c.pattern, err)
		}
		got := text(t, tr)
		if strings.Contains(got, c.gone) {
			t.Errorf("%s kept %q:\n%s", c.pattern, c.gone, got)
		}
		if !strings.Contains(got, c.kept) {
			t.Errorf("%s dropped %q:\n%s", c.pattern, c.kept, got)
		}
	}
}

func TestIgnoreIsCaseSensitive(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "NOTES.md")
	tr, err := New(dir, true, []string{`notes\.md`})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text(t, tr), "NOTES.md") {
		t.Error("a lower-case pattern hid an upper-case name; nerdtree matches with =~#")
	}
}

// TestSortOrder is nerdtree's default g:NERDTreeSortOrder: directories first,
// then everything else, then swap files, backups and tilde files, with the
// names compared case-insensitively and dotfiles keeping their dot.
func TestSortOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"Zeta.go", "alpha.go", ".hidden", "notes.bak", "old.swp", "tilde~"} {
		touch(t, dir, n)
	}
	mkdir(t, dir, "zzz_dir")
	mkdir(t, dir, "Adir")
	tr, err := New(dir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range tr.Root().Children() {
		names = append(names, c.Name)
	}
	want := []string{"Adir", "zzz_dir", ".hidden", "alpha.go", "Zeta.go", "old.swp", "notes.bak", "tilde~"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order is %q, want %q", names, want)
	}
}

func TestExecutableAndLinkDecoration(t *testing.T) {
	dir := t.TempDir()
	script := touch(t, dir, "run.sh")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(script, filepath.Join(dir, "link")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	tr := newTree(t, dir)
	got := text(t, tr)
	if !strings.Contains(got, "run.sh*") {
		t.Errorf("no executable marker:\n%s", got)
	}
	if !strings.Contains(got, " -> "+script) {
		t.Errorf("no link target:\n%s", got)
	}
}

func TestOpenAndCloseADirectory(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "sub/x.go")
	tr := newTree(t, dir)
	sub := tr.Find(filepath.Join(dir, "sub"))
	line := tr.LineOf(sub)
	if line == 0 {
		t.Fatal("sub is not on any line")
	}
	r := tr.Key(key.Rune('o'), line)
	if !r.Handled || r.Action != Redraw {
		t.Fatalf("o on a directory gave %+v", r)
	}
	if !strings.Contains(text(t, tr), "x.go") {
		t.Error("o did not open the directory")
	}
	tr.Key(key.Rune('o'), line)
	if strings.Contains(text(t, tr), "x.go") {
		t.Error("o did not close the directory")
	}
	// The root refuses to close, which is nerdtree's "cannot close tree root".
	tr.Key(key.Rune('o'), tr.LineOf(tr.Root()))
	if !strings.Contains(text(t, tr), "sub/") {
		t.Error("o on the root line closed the root")
	}
}

func TestOpenKeysOnAFile(t *testing.T) {
	dir := t.TempDir()
	f := touch(t, dir, "a.go")
	tr := newTree(t, dir)
	line := tr.LineOf(tr.Find(f))
	cases := []struct {
		k    key.Key
		want Action
	}{
		{key.Rune('o'), Open},
		{key.Key{Special: key.KeyCR}, Open},
		{key.Rune('t'), OpenTab},
		{key.Rune('s'), OpenVSplit},
		{key.Rune('i'), OpenSplit},
	}
	for _, c := range cases {
		r := tr.Key(c.k, line)
		if !r.Handled || r.Action != c.want || r.Node == nil || r.Node.Path != f {
			t.Errorf("%v on a file gave %+v, want %v", c.k, r, c.want)
		}
	}
	// t, s and i are file keys: nerdtree scopes them to FileNode, so on a
	// directory they do nothing.
	for _, k := range []key.Key{key.Rune('t'), key.Rune('s'), key.Rune('i')} {
		if r := tr.Key(k, tr.LineOf(tr.Root())); r.Action != None {
			t.Errorf("%v on a directory gave %v", k, r.Action)
		}
	}
}

func TestUnboundKeysGoBackToTheEditor(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	tr := newTree(t, dir)
	for _, k := range []key.Key{key.Rune('j'), key.Rune('k'), key.Rune('G'), key.Ctrl('d')} {
		if r := tr.Key(k, 1); r.Handled {
			t.Errorf("the tree swallowed %v", k)
		}
	}
}

func TestChangeRootAndUp(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "sub/deep/x.go")
	tr := newTree(t, dir)
	sub := tr.Find(filepath.Join(dir, "sub"))

	tr.Key(key.Rune('C'), tr.LineOf(sub))
	if tr.Root().Path != sub.Path {
		t.Fatalf("C left the root at %q", tr.Root().Path)
	}
	if !strings.Contains(text(t, tr), "deep/") {
		t.Error("the new root was not opened")
	}
	// C on a file changes the root to the file's directory.
	tr.Key(key.Rune('u'), 1)
	if tr.Root().Path != dir {
		t.Fatalf("u left the root at %q, want %q", tr.Root().Path, dir)
	}
	if !strings.Contains(text(t, tr), "sub/") {
		t.Error("the old root is not a child of the new one")
	}
	// "u" closes the directory it came from; "U" would leave it open.
	if strings.Contains(text(t, tr), "deep/") {
		t.Error("u left the old root expanded")
	}
}

func TestUpAtTheFilesystemRootDoesNothing(t *testing.T) {
	tr, err := New("/", true, nil)
	if err != nil {
		t.Skipf("cannot read /: %v", err)
	}
	tr.Up(false)
	if tr.Root().Path != "/" {
		t.Errorf("up from / went to %q", tr.Root().Path)
	}
}

func TestRefreshPicksUpANewFile(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "sub/a.go")
	tr := newTree(t, dir)
	sub := tr.Find(filepath.Join(dir, "sub"))
	tr.Open(sub)
	touch(t, dir, "sub/b.go")
	if strings.Contains(text(t, tr), "b.go") {
		t.Fatal("the tree saw a file nobody told it about")
	}
	tr.Key(key.Rune('R'), 1)
	got := text(t, tr)
	if !strings.Contains(got, "b.go") {
		t.Errorf("R did not pick up the new file:\n%s", got)
	}
	if !strings.Contains(got, "a.go") {
		t.Errorf("R collapsed the directory that was open:\n%s", got)
	}
}

func TestCloseParentAndJumpToParent(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "sub/x.go")
	tr := newTree(t, dir)
	sub := tr.Find(filepath.Join(dir, "sub"))
	tr.Open(sub)
	x := tr.Find(filepath.Join(dir, "sub/x.go"))

	r := tr.Key(key.Rune('p'), tr.LineOf(x))
	if r.Action != Move || r.Line != tr.LineOf(sub) {
		t.Errorf("p gave %+v, want a move to line %d", r, tr.LineOf(sub))
	}
	r = tr.Key(key.Rune('x'), tr.LineOf(x))
	if r.Action != Redraw {
		t.Errorf("x gave %+v", r)
	}
	if strings.Contains(text(t, tr), "x.go") {
		t.Error("x did not close the parent")
	}
	// x under a node whose parent is the root does nothing, because the root
	// cannot be closed.
	if r := tr.Key(key.Rune('x'), tr.LineOf(sub)); r.Action != None {
		t.Errorf("x on a child of the root gave %v", r.Action)
	}
}

func TestHelpBanner(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	tr := newTree(t, dir)
	if !strings.HasPrefix(text(t, tr), HelpLine) {
		t.Error("no help hint at the top")
	}
	tr.Key(key.Rune('?'), 1)
	if !strings.Contains(text(t, tr), "toggle help") {
		t.Error("? did not open the banner")
	}
	if tr.NodeAt(tr.FirstNodeLine()) != tr.Root() {
		t.Error("the first node line is not the root after the banner grew")
	}
	tr.Key(key.Rune('?'), 1)
	if strings.Contains(text(t, tr), "toggle help") {
		t.Error("? did not close the banner")
	}
}

func TestNodeAtHeaderLinesIsNil(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "a.go")
	tr := newTree(t, dir)
	for _, l := range []int{1, 2, 3} {
		if n := tr.NodeAt(l); n != nil {
			t.Errorf("line %d is a node: %q", l, n.Path)
		}
	}
	if tr.NodeAt(4) != tr.Root() {
		t.Error("line 4 is not the root")
	}
	if tr.NodeAt(0) != nil || tr.NodeAt(9999) != nil {
		t.Error("a line outside the buffer answered a node")
	}
}
