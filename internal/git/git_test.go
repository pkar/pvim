package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every test in this file builds its own repository under t.TempDir and runs
// git with HOME and both config paths pointed inside it. That is not
// politeness: the checkout this package is written in is the one the work is
// committed from, and a test that ran "git add" in the wrong directory would
// stage somebody else's unfinished afternoon. Nothing here ever names a path
// outside the directory the test made.

// repo is a fresh repository with an identity, and the helpers to drive it.
type repo struct {
	*Repo
	t    *testing.T
	dir  string
	home string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	if testing.Short() {
		t.Skip("shells out to git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this box")
	}
	// os.MkdirTemp and not t.TempDir: t.TempDir puts the subtest's name into
	// the path and some of these subtests are named after status codes.
	dir, err := os.MkdirTemp("", "pvim-git")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	home := filepath.Join(dir, "home")
	work := filepath.Join(dir, "work")
	for _, d := range []string{home, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := &repo{t: t, dir: work, home: home}
	r.run("init", "-q", "-b", "master")
	r.run("config", "user.email", "test@example.invalid")
	r.run("config", "user.name", "T")
	g, err := Find(work)
	if err != nil {
		t.Fatal(err)
	}
	g.SetEnv(Env(home))
	r.Repo = g
	return r
}

// run runs a git command in the repository and fails the test if it fails.
func (r *repo) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = Env(r.home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// write puts a file in the working tree.
func (r *repo) write(name, body string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// TestFindOutsideARepository is the message ":Gstatus" gives in a directory
// that is not in one.
func TestFindOutsideARepository(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this box")
	}
	dir := t.TempDir()
	// A temporary directory can sit inside somebody's repository on a box
	// where TMPDIR has been moved, so this asserts the error only when git
	// agrees there is no repository above it.
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		t.Skip("the temporary directory is inside a repository")
	}
	if _, err := Find(dir); err != ErrNotARepo {
		t.Errorf("Find in a bare directory gave %v, want ErrNotARepo", err)
	}
}

// TestStatusHasTheThreeSections builds one file in each state git can put a
// file in and checks which list it lands in.
func TestStatusHasTheThreeSections(t *testing.T) {
	r := newRepo(t)
	r.write("tracked.txt", "one\ntwo\n")
	r.write("gone.txt", "x\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")

	r.write("tracked.txt", "one\nTWO\n") // unstaged M
	r.write("untracked.txt", "new\n")    // untracked
	r.write("staged.txt", "staged\n")    // staged A
	r.run("add", "staged.txt")
	if err := os.Remove(filepath.Join(r.dir, "gone.txt")); err != nil { // unstaged D
		t.Fatal(err)
	}
	r.write("both.txt", "both\n") // staged A and unstaged M
	r.run("add", "both.txt")
	r.write("both.txt", "both\nmore\n")

	s, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if s.Head != "master" || s.Detached {
		t.Errorf("head is %q detached=%v, want master", s.Head, s.Detached)
	}
	want := map[Section]string{
		Untracked: "? untracked.txt",
		Unstaged:  "M both.txt, D gone.txt, M tracked.txt",
		Staged:    "A both.txt, A staged.txt",
	}
	got := map[Section]string{
		Untracked: join(s.Untracked),
		Unstaged:  join(s.Unstaged),
		Staged:    join(s.Staged),
	}
	for sec, w := range want {
		if got[sec] != w {
			t.Errorf("%s holds %q, want %q", sec.Name(), got[sec], w)
		}
	}
}

func join(es []Entry) string {
	var parts []string
	for _, e := range es {
		parts = append(parts, string(e.Code)+" "+e.Path)
	}
	return strings.Join(parts, ", ")
}

// TestRenderIsFugitivesShape holds the status buffer to the shape a real
// fugitive drew for the same repository.
//
// The literal below was captured by running vim 9.2.0321 with
// vim-fugitive from ~/.vim/pack/pkar/start over a repository in exactly this
// state and reading the buffer back with writefile(getline(1,"$")). It is a
// literal and not a second run of fugitive because a test that needs a plugin
// installed is a test that is skipped on every other machine.
func TestRenderIsFugitivesShape(t *testing.T) {
	r := newRepo(t)
	r.write("tracked.txt", "one\ntwo\n")
	r.write("gone.txt", "x\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")
	r.write("tracked.txt", "one\nTWO\n")
	r.write("untracked.txt", "new\n")
	r.write("staged.txt", "staged\n")
	r.run("add", "staged.txt")
	if err := os.Remove(filepath.Join(r.dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	r.write("both.txt", "both\n")
	r.run("add", "both.txt")
	r.write("both.txt", "both\nmore\n")

	s, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	lines, refs := s.Render()
	want := []string{
		"Head: master",
		"Help: g?",
		"",
		"Untracked (1)",
		"? untracked.txt",
		"",
		"Unstaged (3)",
		"M both.txt",
		"D gone.txt",
		"M tracked.txt",
		"",
		"Staged (2)",
		"A both.txt",
		"A staged.txt",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("the status buffer is\n%s\nwant\n%s",
			strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if len(refs) != len(lines) {
		t.Fatalf("%d lines and %d refs", len(lines), len(refs))
	}
	if refs[4].Kind != File || refs[4].Section != Untracked || refs[4].Entry.Path != "untracked.txt" {
		t.Errorf("line 5 refers to %+v", refs[4])
	}
	if refs[6].Kind != Heading || refs[6].Section != Unstaged {
		t.Errorf("line 7 refers to %+v", refs[6])
	}
}

// TestCleanRepositorySaysSo is the other end of Render: a repository with
// nothing in it to show still has a buffer with something in it.
func TestCleanRepositorySaysSo(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "a\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")
	s, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Clean() {
		t.Fatalf("a fresh commit left %+v", s)
	}
	lines, _ := s.Render()
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "working tree clean") {
		t.Errorf("the buffer is %q", got)
	}
}

// TestStageAndUnstage is the "-" key, checked against what git says
// afterwards rather than against what this package thinks it did.
func TestStageAndUnstage(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "a\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")
	r.write("a.txt", "changed\n")
	r.write("b.txt", "new\n")

	// TrimRight and not TrimSpace: the leading space of " M a.txt" is the
	// staged half of the code and dropping it is dropping the answer.
	porcelain := func() string {
		return strings.TrimRight(r.run("status", "--porcelain"), "\n")
	}
	if got, want := porcelain(), " M a.txt\n?? b.txt"; got != want {
		t.Fatalf("before: %q, want %q", got, want)
	}

	s, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Toggle(Unstaged, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := r.Toggle(Untracked, "b.txt"); err != nil {
		t.Fatal(err)
	}
	if got, want := porcelain(), "M  a.txt\nA  b.txt"; got != want {
		t.Fatalf("after staging: %q, want %q", got, want)
	}

	s, err = r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ToggleSection(s, Staged); err != nil {
		t.Fatal(err)
	}
	if got, want := porcelain(), " M a.txt\n?? b.txt"; got != want {
		t.Fatalf("after unstaging the section: %q, want %q", got, want)
	}
}

// TestUnstageWithNoCommitYet is the corner git's own "reset HEAD" cannot do:
// the first commit has not happened, so there is no HEAD to reset to.
func TestUnstageWithNoCommitYet(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "a\n")
	r.run("add", "a.txt")
	s, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if got := join(s.Staged); got != "A a.txt" {
		t.Fatalf("staged holds %q", got)
	}
	if err := r.Unstage("a.txt"); err != nil {
		t.Fatalf("unstaging before the first commit: %v", err)
	}
	if got := strings.TrimRight(r.run("status", "--porcelain"), "\n"); got != "?? a.txt" {
		t.Errorf("after unstaging: %q, want %q", got, "?? a.txt")
	}
}

// TestRenameShowsBothNames is the one status code with two paths in it.
func TestRenameShowsBothNames(t *testing.T) {
	r := newRepo(t)
	r.write("old.txt", "the same contents on both sides\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")
	r.run("mv", "old.txt", "new.txt")
	s, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Staged) != 1 || s.Staged[0].Code != 'R' ||
		s.Staged[0].Path != "new.txt" || s.Staged[0].From != "old.txt" {
		t.Fatalf("staged holds %+v", s.Staged)
	}
	lines, _ := s.Render()
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "R old.txt -> new.txt") {
		t.Errorf("the buffer is\n%s", got)
	}
}

// TestPathWithASpace is why the porcelain is read with -z.
func TestPathWithASpace(t *testing.T) {
	r := newRepo(t)
	r.write("a file with spaces.txt", "x\n")
	s, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Untracked) != 1 || s.Untracked[0].Path != "a file with spaces.txt" {
		t.Errorf("untracked holds %+v", s.Untracked)
	}
}

// TestIndexBlobIsWhatGitShows is the left half of ":Gdiff".
func TestIndexBlobIsWhatGitShows(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "one\ntwo\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")
	r.write("a.txt", "one\nTWO\nthree\n")

	got, err := r.IndexBlob("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := r.run("show", ":a.txt"); string(got) != want {
		t.Errorf("the index holds %q, git show says %q", got, want)
	}
	// A file that is not in the index at all is not an error: the diff shows
	// every line as added.
	r.write("new.txt", "brand new\n")
	got, err = r.IndexBlob("new.txt")
	if err != nil {
		t.Fatalf("a file not in the index gave %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a file not in the index gave %q", got)
	}
}

// TestCommitGoesThroughTheEditor is the ":Gstatus" "cc" round trip with a
// shell script standing in for "pvim --wait".
//
// The real round trip -- git, a unix socket, a tab in a running instance and
// the commit landing when the tab closes -- is TestEditorGitCommit in
// cmd/pvim, and it passes. This is the half that belongs to this package:
// that Commit hands git an editor and waits for it.
func TestCommitGoesThroughTheEditor(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "a\n")
	r.run("add", "-A")

	editor := filepath.Join(r.dir, "..", "editor.sh")
	script := "#!/bin/sh\nprintf 'from the editor\\n' > \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := r.Commit(editor)
	if err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(r.run("log", "-1", "--pretty=%s")); got != "from the editor" {
		t.Errorf("the commit subject is %q", got)
	}
}

// TestCommitWithNothingStagedFails is the message the status buffer shows
// when "cc" is pressed with an empty index.
func TestCommitWithNothingStagedFails(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "a\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")

	editor := filepath.Join(r.dir, "..", "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := r.Commit(editor)
	if err == nil {
		t.Fatalf("a commit with nothing staged succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "nothing to commit") {
		t.Errorf("git said %q", out)
	}
}

// TestRunReturnsBothStreams is what ":Git" puts in a scratch split.
func TestRunReturnsBothStreams(t *testing.T) {
	r := newRepo(t)
	out, err := r.Run("no-such-subcommand")
	if err == nil {
		t.Fatal("a bad subcommand succeeded")
	}
	if !strings.Contains(string(out), "no-such-subcommand") {
		t.Errorf("the output is %q, and git's complaint is not in it", out)
	}
}

// TestRelAndAbs is the path arithmetic every command does on the way in.
func TestRelAndAbs(t *testing.T) {
	r := newRepo(t)
	r.write("sub/deep.txt", "x\n")
	rel, err := r.Rel(filepath.Join(r.dir, "sub", "deep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if rel != "sub/deep.txt" {
		t.Errorf("Rel gave %q", rel)
	}
	if got := r.Abs(rel); got != filepath.Join(r.Root, "sub", "deep.txt") {
		t.Errorf("Abs gave %q", got)
	}
	if _, err := r.Rel(filepath.Join(r.dir, "..", "outside.txt")); err == nil {
		t.Error("a path outside the working tree was accepted")
	}
}
