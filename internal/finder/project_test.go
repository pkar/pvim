package finder

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The two lines of the vimrc this package exists to obey, copied as written
// from internal/vimrc/testdata/vimrc so that a test failure names the vimrc
// and not a paraphrase of it.
const (
	vimrcWildIgnore = `*.o,*.obj,*.bak,*.exe,*.pyc,*.swp,*/tmp/*,*.so,*.swp,*.zip,*.tgz,*.tar.gz,*.iso`
	vimrcIgnoreDir  = `\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$`
	vimrcIgnoreFile = `\.so$\|\.dat$|\.DS_Store|\.pyc$`
)

// vimrcRules is the finder's rule set as this vimrc leaves it.
func vimrcRules() Rules {
	return Rules{
		WildIgnore: []string{vimrcWildIgnore},
		Dir:        []string{vimrcIgnoreDir},
		File:       []string{vimrcIgnoreFile},
		IgnoreCase: true,
	}
}

// write makes a file with its parents, and returns the tree it was made in.
func write(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRootIsTheNearestAncestorWithAGit(t *testing.T) {
	dir := t.TempDir()
	// A real repository shape: a .git at the top, a nested one three levels
	// down, and a file under each.
	write(t, dir, ".git/HEAD")
	write(t, dir, "internal/finder/project.go")
	write(t, dir, "vendor/dep/.git/HEAD")
	write(t, dir, "vendor/dep/dep.go")

	cases := []struct {
		start, want string
	}{
		{filepath.Join(dir, "internal/finder/project.go"), dir},
		{filepath.Join(dir, "internal/finder"), dir},
		{dir, dir},
		{filepath.Join(dir, "vendor/dep/dep.go"), filepath.Join(dir, "vendor/dep")},
	}
	for _, c := range cases {
		if got := Root(c.start); got != c.want {
			t.Errorf("Root(%q) = %q, want %q", c.start, got, c.want)
		}
	}
}

func TestRootWithNoMarkerIsTheStartingDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a/b/c.go")
	// No .git anywhere above this, all the way to "/". The answer must be the
	// file's own directory and not the volume root, or a finder would walk
	// the whole disk.
	want := filepath.Join(dir, "a/b")
	if got := Root(filepath.Join(dir, "a/b/c.go")); got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
}

func TestGlobToPattern(t *testing.T) {
	cases := []struct{ glob, want string }{
		{"*.o", `^.*\.o$`},
		{"*/tmp/*", `^.*/tmp/.*$`},
		{"*.tar.gz", `^.*\.tar\.gz$`},
		{"file?.txt", `^file.\.txt$`},
		{"[abc]x", `^[abc]x$`},
		{"a~b", `^a\~b$`},
		{"[", `^\[$`},
	}
	for _, c := range cases {
		if got := GlobToPattern(c.glob); got != c.want {
			t.Errorf("GlobToPattern(%q) = %q, want %q", c.glob, got, c.want)
		}
	}
}

// TestWildIgnoreTailAndPath is vim's match_file_pat rule: a pattern with a
// path separator matches the whole path, one without it matches the tail.
func TestWildIgnoreTailAndPath(t *testing.T) {
	m := Rules{WildIgnore: []string{vimrcWildIgnore}}.MustCompile()
	cases := []struct {
		path string
		skip bool
	}{
		{"main.go", false},
		{"main.o", true},
		{"deep/down/main.o", true},    // no separator in "*.o": matched on the tail
		{"tmp/x.go", true},            // "*/tmp/*" against the rooted path
		{"a/tmp/x.go", true},          //
		{"a/tmpfoo/x.go", false},      // "*/tmp/*" wants the whole component
		{"dist/pvim.tar.gz", true},    //
		{"dist/pvim.tar.gzip", false}, // anchored at the end, as vim anchors
	}
	for _, c := range cases {
		name := c.path
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if got := m.SkipFile(c.path, name); got != c.skip {
			t.Errorf("SkipFile(%q) = %v, want %v", c.path, got, c.skip)
		}
	}
}

// TestCtrlpDictNeedsTheLeadingSeparator is the reason Matcher.subject glues a
// "/" on the front. ctrlp's own default dictionary is `\v[\/](\.git|...)$` and
// matching a bare ".git" against it finds nothing.
func TestCtrlpDictNeedsTheLeadingSeparator(t *testing.T) {
	m := Rules{Dir: []string{`\v[\/](\.git|\.hg|\.svn)$`}, IgnoreCase: true}.MustCompile()
	for _, p := range []string{".git", "vendor/dep/.git"} {
		name := p
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if !m.SkipDir(p, name) {
			t.Errorf("SkipDir(%q) kept a directory ctrlp's own default ignores", p)
		}
	}
}

// TestWalkUnderTheVimrcsRules is the gate for the finder half: a file
// inside .git is not in the listing.
func TestWalkUnderTheVimrcsRules(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		".git/HEAD",
		".git/objects/ab/cdef",
		"cmd/pvim/main.go",
		"internal/finder/project.go",
		"internal/finder/project.o",
		"tmp/scratch.go",
		"docs/notes.md",
		"vendor/x/y.pyc",
		"lib/libfoo.so",
		".hidden.go",
	} {
		write(t, dir, f)
	}
	got, err := Walk(dir, vimrcRules().MustCompile(), DefaultWalk())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"cmd/pvim/main.go",
		"docs/notes.md",
		"internal/finder/project.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Walk gave\n\t%q\nwant\n\t%q", got, want)
	}
	for _, p := range got {
		if strings.HasPrefix(p, ".git/") {
			t.Errorf("a file inside .git is in the finder: %q", p)
		}
	}
}

func TestWalkShowHiddenKeepsDotfiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".vimrc")
	write(t, dir, "a.go")
	r := Rules{ShowHidden: true}
	got, err := Walk(dir, r.MustCompile(), DefaultWalk())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{".vimrc", "a.go"}) {
		t.Errorf("Walk with ShowHidden gave %q", got)
	}
}

func TestWalkStopsAtMaxFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a", "b", "c", "d", "e"} {
		write(t, dir, f)
	}
	got, err := Walk(dir, Rules{}.MustCompile(), WalkOptions{MaxFiles: 3, MaxDepth: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("Walk with MaxFiles 3 gave %d entries: %q", len(got), got)
	}
}

func TestWalkDoesNotFollowADirectoryLink(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "real/a.go")
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "loop")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	got, err := Walk(dir, Rules{}.MustCompile(), DefaultWalk())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"real/a.go"}) {
		t.Errorf("Walk followed a directory link: %q", got)
	}
}

func TestUnusableIgnorePatternIsAnError(t *testing.T) {
	// \zs is valid vim that internal/regex refuses, which is exactly the case
	// that must not be swallowed: a vimrc ignoring nothing has to say so.
	if _, err := (Rules{Dir: []string{`foo\zsbar`}}).Compile(); err == nil {
		t.Fatal("a refused pattern compiled quietly")
	}
}
