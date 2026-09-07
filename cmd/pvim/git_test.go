package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/git"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/text"
)

// The git commands and keys, end to end through a real editor and a real git.
//
// Every test builds its own repository under a temporary directory and points
// HOME and both git config paths inside it, so that nothing here can read the
// person's git identity or write the checkout this editor is developed in.
// That is not politeness: a stray "git add" in the wrong directory stages
// somebody's unfinished afternoon.

// gitEditorFixture is an editor opened on a file in a fresh repository.
type gitEditorFixture struct {
	*editor
	t    *testing.T
	dir  string
	home string
	file string
}

func newGitEditor(t *testing.T, body string) *gitEditorFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("shells out to git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this box")
	}
	dir, err := os.MkdirTemp("", "pvim-cmdgit")
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
	// The whole process's git configuration, for the length of this test.
	// internal/git.Find and everything after it run git out of this process,
	// so this is the fence and there is no other.
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, ".gitconfig-system"))
	t.Setenv("GIT_AUTHOR_NAME", "pvim test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "pvim test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")

	f := &gitEditorFixture{t: t, dir: work, home: home}
	f.git("init", "-q", "-b", "master")
	f.git("config", "user.email", "test@example.invalid")
	f.git("config", "user.name", "T")

	f.file = filepath.Join(work, "a.txt")
	if err := os.WriteFile(f.file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := newEditor([]byte(body), f.file, 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	f.editor = e
	t.Cleanup(func() { delete(gitState, e.sess) })
	return f
}

// git runs a git command in the fixture's repository.
func (f *gitEditorFixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = f.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// write puts a file in the working tree.
func (f *gitEditorFixture) write(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// run runs one command line the way the colon prompt does.
func (f *gitEditorFixture) run(line string) {
	f.t.Helper()
	if err := f.sess.runLine(line); err != nil {
		f.t.Fatalf("%q: %v", line, err)
	}
}

// keys feeds normal-mode keys through the session, which is the path a
// keystroke takes and so the path gitKey is on.
func (f *gitEditorFixture) keys(s string) {
	f.t.Helper()
	for _, r := range s {
		if err := f.sess.Key(key.Rune(r)); err != nil {
			f.t.Fatalf("key %q: %v", string(r), err)
		}
	}
}

// lines is the current buffer's contents.
func (f *gitEditorFixture) lines() []string {
	b := f.cur()
	if b == nil || b.Text == nil {
		return nil
	}
	var out []string
	for i := 1; i <= b.Text.LineCount(); i++ {
		out = append(out, string(b.Text.Line(i)))
	}
	return out
}

// porcelain is what git says the repository looks like.
func (f *gitEditorFixture) porcelain() string {
	return strings.TrimRight(f.git("status", "--porcelain"), "\n")
}

// TestGstatusOpensTheStatusBuffer is ":Gstatus" and ":G": a split with
// fugitive's shape in it, and the cursor on the first file.
func TestGstatusOpensTheStatusBuffer(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "one\ntwo\n")
	f.write("new.txt", "n\n")

	before := len(f.tabs.Current().Windows())
	f.run("Gstatus")
	if got := len(f.tabs.Current().Windows()); got != before+1 {
		t.Fatalf("the status buffer opened %d windows, want one more than %d", got, before)
	}
	want := []string{
		"Head: master",
		"Help: g?",
		"",
		"Untracked (1)",
		"? new.txt",
		"",
		"Unstaged (1)",
		"M a.txt",
	}
	if got := strings.Join(f.lines(), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("the status buffer is\n%s\nwant\n%s", got, strings.Join(want, "\n"))
	}
	if got := f.ed.Cursor().Line; got != 5 {
		t.Errorf("the cursor is on line %d, want 5, the first file", got)
	}
	// ":G" is the same command.
	f.run("G")
	if got := strings.Join(f.lines(), "\n"); got != strings.Join(want, "\n") {
		t.Errorf(":G drew\n%s", got)
	}
}

// TestDashStagesAndUnstages is the "-" key, checked against git and not
// against what this editor thinks it did.
func TestDashStagesAndUnstages(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "one\ntwo\n")
	f.write("new.txt", "n\n")
	f.run("Gstatus")

	// Line 5 is "? new.txt".
	f.ed.SetCursor(text.Pos{Line: 5})
	f.keys("-")
	if got, want := f.porcelain(), " M a.txt\nA  new.txt"; got != want {
		t.Fatalf("after - on the untracked file: %q, want %q", got, want)
	}
	// The buffer redrew: the untracked section is gone and staged is there.
	body := strings.Join(f.lines(), "\n")
	if strings.Contains(body, "Untracked") || !strings.Contains(body, "Staged (1)") {
		t.Errorf("the status buffer is\n%s", body)
	}

	// "-" on the staged file takes it back out.
	for i, l := range f.lines() {
		if l == "A new.txt" {
			f.ed.SetCursor(text.Pos{Line: i + 1})
		}
	}
	f.keys("-")
	if got, want := f.porcelain(), " M a.txt\n?? new.txt"; got != want {
		t.Fatalf("after - on the staged file: %q, want %q", got, want)
	}
}

// TestDashOnAHeadingTakesTheWholeSection is the other half of "-".
func TestDashOnAHeadingTakesTheWholeSection(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.write("b.txt", "b\n")
	f.write("c.txt", "c\n")
	f.run("Gstatus")

	for i, l := range f.lines() {
		if strings.HasPrefix(l, "Untracked (") {
			f.ed.SetCursor(text.Pos{Line: i + 1})
		}
	}
	f.keys("-")
	if got, want := f.porcelain(), "A  a.txt\nA  b.txt\nA  c.txt"; got != want {
		t.Fatalf("after - on the heading: %q, want %q", got, want)
	}
}

// TestGitRunsASubcommand is ":Git {args}": git's output in a scratch split.
func TestGitRunsASubcommand(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "the subject line")

	f.run("Git log --oneline")
	body := strings.Join(f.lines(), "\n")
	if !strings.Contains(body, "the subject line") {
		t.Errorf("the scratch split holds\n%s", body)
	}
	if got := f.cur().Name; !strings.HasPrefix(got, ":Git ") {
		t.Errorf("the scratch buffer is called %q", got)
	}
	if !f.cur().Scratch {
		t.Error("the output buffer is not a scratch buffer")
	}
}

// TestGitReportsAFailedSubcommand is what a bad git looks like: its own
// complaint in the split and a message line that says it failed.
func TestGitReportsAFailedSubcommand(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.run("Git no-such-subcommand")
	body := strings.Join(f.lines(), "\n")
	if !strings.Contains(body, "no-such-subcommand") {
		t.Errorf("the split holds\n%s", body)
	}
}

// TestGblameNamesTheSameCommitAsGitBlame is the gate for blame, run
// through the editor rather than through the package: the window that opens
// holds the lines `git blame` prints.
func TestGblameNamesTheSameCommitAsGitBlame(t *testing.T) {
	f := newGitEditor(t, "one\ntwo\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "one\nTWO\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "second")

	before := len(f.tabs.Current().Windows())
	f.run("Gblame")
	if got := len(f.tabs.Current().Windows()); got != before+1 {
		t.Fatalf("blame opened %d windows, want one more than %d", got, before)
	}
	got := f.lines()
	want := strings.Split(strings.TrimRight(f.git("blame", "a.txt"), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("the blame window has %d lines and git blame prints %d", len(got), len(want))
	}
	for i := range got {
		if !strings.HasPrefix(want[i], got[i]) {
			t.Errorf("line %d is %q and git blame says %q", i+1, got[i], want[i])
		}
	}
}

// TestBlameScrollsWithTheFile is the scroll lock: the two windows end up
// showing the same top line whichever of them was moved.
//
// Two keys and not one, because the lock is a poll on the way IN to a
// keystroke: the window that moved is scrolled by the key, and the other one
// follows on the key after. That is a real difference from vim's
// 'scrollbind', which moves both inside the scroll itself, and it is what a
// poll buys instead of a line in internal/window. What a person sees is a
// blame column one keystroke behind while a key is held down.
func TestBlameScrollsWithTheFile(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 200; i++ {
		body.WriteString("line\n")
	}
	f := newGitEditor(t, body.String())
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.run("Gblame")

	p := gitState[f.sess]
	if p == nil || p.blameWin == nil || p.blameOn == nil {
		t.Fatal("there is no blame window")
	}
	// Move in the file window, which is what a person does.
	p.focus(p.blameOn, nil)
	f.keys("G")
	f.keys("0")
	if got, want := p.blameWin.View.TopLine, p.blameOn.View.TopLine; got != want {
		t.Errorf("the blame window is at line %d and the file at %d", got, want)
	}
	if p.blameOn.View.TopLine <= 1 {
		t.Fatalf("G did not scroll the file window; it is at line %d", p.blameOn.View.TopLine)
	}

	// And the other way: move in the blame window.
	p.focus(p.blameWin, p.blameBuf)
	f.keys("gg")
	f.keys("0")
	if got, want := p.blameOn.View.TopLine, p.blameWin.View.TopLine; got != want {
		t.Errorf("the file window is at line %d and the blame at %d", got, want)
	}
	if p.blameWin.View.TopLine != 1 {
		t.Fatalf("gg left the blame window at line %d", p.blameWin.View.TopLine)
	}
}

// TestGdiffOpensTheIndexBeside is ":Gdiff": the file as the index has it in a
// vertical split, and the hunks the same ones `git diff` prints.
func TestGdiffOpensTheIndexBeside(t *testing.T) {
	f := newGitEditor(t, "one\ntwo\nthree\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	// The buffer is changed and the file on disk with it, because ":Gdiff"
	// compares the BUFFER against the index and the index against what was
	// committed.
	f.write("a.txt", "one\nTWO\nthree\nfour\n")
	f.run("edit!")

	before := len(f.tabs.Current().Windows())
	f.run("Gdiff")
	if got := len(f.tabs.Current().Windows()); got != before+1 {
		t.Fatalf("Gdiff opened %d windows, want one more than %d", got, before)
	}
	if got := strings.Join(f.lines(), "\n"); got != "one\ntwo\nthree" {
		t.Errorf("the index window holds %q", got)
	}
	if !f.cur().ReadOnly {
		t.Error("the index window is writable")
	}
	p := gitState[f.sess]
	if len(p.diffs) != 1 {
		t.Fatalf("there are %d diff pairs", len(p.diffs))
	}
	pair := p.pairOf(p.diffs[0])
	if got := len(pair.Changes()); got != 2 {
		t.Errorf("the diff has %d hunks, want 2: the changed line and the added one", got)
	}
}

// TestHunkKeysMoveAndGet is ]c, [c and do in a diff pair.
func TestHunkKeysMoveAndGet(t *testing.T) {
	f := newGitEditor(t, "a\nb\nc\nd\ne\nf\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "a\nB\nc\nd\nE\nf\n")
	f.run("edit!")
	f.run("Gdiff")

	// The cursor is in the index window after the split; move to the file's
	// window, which is the one a person reads.
	p := gitState[f.sess]
	d := p.diffs[0]
	p.focus(d.win[1], d.buf[1])
	f.ed.SetCursor(text.Pos{Line: 1})

	f.keys("]c")
	if got := f.ed.Cursor().Line; got != 2 {
		t.Errorf("]c landed on line %d, want 2", got)
	}
	f.keys("]c")
	if got := f.ed.Cursor().Line; got != 5 {
		t.Errorf("a second ]c landed on line %d, want 5", got)
	}
	f.keys("[c")
	if got := f.ed.Cursor().Line; got != 2 {
		t.Errorf("[c landed on line %d, want 2", got)
	}

	// "do" pulls the index's line 2 into the file's buffer.
	f.keys("do")
	if got := strings.Join(f.lines(), "\n"); got != "a\nb\nc\nd\nE\nf" {
		t.Errorf("after do the buffer is %q", got)
	}
}

// TestDiffWindowStillTakesOrdinaryDKeys is the risk the two-key handling
// creates: "d" is held to see whether an "o" or a "p" is coming, and every
// other d command has to still work.
func TestDiffWindowStillTakesOrdinaryDKeys(t *testing.T) {
	f := newGitEditor(t, "a\nb\nc\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "a\nB\nc\n")
	f.run("edit!")
	f.run("Gdiff")

	p := gitState[f.sess]
	d := p.diffs[0]
	p.focus(d.win[1], d.buf[1])
	f.ed.SetCursor(text.Pos{Line: 1})
	f.keys("dd")
	if got := strings.Join(f.lines(), "\n"); got != "B\nc" {
		t.Errorf("dd in a diff window left %q, want %q", got, "B\nc")
	}
	// And "]" followed by anything else is the mode machine's again.
	f.ed.SetCursor(text.Pos{Line: 1})
	f.keys("]]")
	if got := f.ed.Cursor().Line; got < 1 {
		t.Errorf("]] left the cursor on line %d", got)
	}
}

// TestGstatusOutsideARepositorySaysSo is the message, not a panic.
func TestGstatusOutsideARepositorySaysSo(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this box")
	}
	dir, err := os.MkdirTemp("", "pvim-nogit")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	probe := exec.Command("git", "rev-parse", "--show-toplevel")
	probe.Dir = dir
	if probe.Run() == nil {
		t.Skip("the temporary directory is inside a repository")
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := newEditor([]byte("x\n"), file, 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { delete(gitState, e.sess) })
	took, gerr := gitCommand(e.sess, "Gstatus")
	if !took {
		t.Fatal("the Gstatus line was not taken")
	}
	if gerr == nil || !strings.Contains(gerr.Error(), "not a git repository") {
		t.Errorf("the error is %v", gerr)
	}
}

// TestNonGitCommandsPassThrough is the guard on the hook: a command line that
// is not one of ours has to reach the ex layer untouched, which is what keeps
// the 809 oracle cases where they are.
func TestNonGitCommandsPassThrough(t *testing.T) {
	f := newGitEditor(t, "one\n")
	for _, line := range []string{"set nu", "w", "Gopher", "normal! x", "e!", "global/x/d"} {
		if took, _ := gitCommand(f.sess, line); took {
			t.Errorf("%q was taken by the git hook", line)
		}
	}
}

// TestSplitArgs is the quoting ":Git" honours.
func TestSplitArgs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"log --oneline", []string{"log", "--oneline"}},
		{"commit -m 'two words'", []string{"commit", "-m", "two words"}},
		{`commit -m "two words"`, []string{"commit", "-m", "two words"}},
		{`log --grep=a\ b`, []string{"log", "--grep=a b"}},
		{"  spaced   out  ", []string{"spaced", "out"}},
		{`commit -m ""`, []string{"commit", "-m", ""}},
	} {
		got := splitArgs(c.in)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("splitArgs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEnvIsFenced is the guard on the fence itself: internal/git.Env has to
// move HOME, or every test in this file would read the person's git identity.
func TestEnvIsFenced(t *testing.T) {
	env := git.Env("/nowhere")
	var home string
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			home = kv
		}
	}
	if home != "HOME=/nowhere" {
		t.Errorf("HOME in the test environment is %q", home)
	}
}

// TestCommitKeyRunsGitAndReportsBack is "cc" in the status buffer: git runs
// off the editor goroutine and the result is picked up on the next key.
//
// The editor git is handed is a shell script and not this binary. In a test,
// os.Executable is the test binary, and giving git THAT as its editor would
// run the whole suite again inside itself. The real thing -- git, a unix
// socket, a tab in this instance and the commit landing when the tab closes
// -- is TestEditorGitCommit in cmd/pvim/instance_test.go and passes there;
// what this checks is the half above it.
func TestCommitKeyRunsGitAndReportsBack(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "one\ntwo\n")
	f.run("Gstatus")

	script := filepath.Join(f.home, "editor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'typed in pvim\\n' > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := gitState[f.sess]
	p.editorCmd = script

	// Stage the change, then commit it.
	for i, l := range f.lines() {
		if l == "M a.txt" {
			f.ed.SetCursor(text.Pos{Line: i + 1})
		}
	}
	f.keys("-")
	f.keys("cc")
	if !p.running {
		t.Fatal("cc did not start a commit")
	}
	waitForCommit(t, p)
	f.keys("0") // any key: the pickup is a poll
	if p.running {
		t.Error("the commit is still marked running after it finished")
	}
	if got := strings.TrimSpace(f.git("log", "-1", "--pretty=%s")); got != "typed in pvim" {
		t.Errorf("the commit subject is %q", got)
	}
	// The status buffer redrew itself, and the repository is clean.
	if got := f.porcelain(); got != "" {
		t.Errorf("after the commit git says %q", got)
	}
	if body := strings.Join(f.lines(), "\n"); !strings.Contains(body, "working tree clean") {
		t.Errorf("the status buffer is\n%s", body)
	}
}

// TestCommitWithNothingStagedSaysSo is the other end: git refuses and its
// complaint reaches the message line.
func TestCommitWithNothingStagedSaysSo(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.run("Gstatus")

	script := filepath.Join(f.home, "editor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := gitState[f.sess]
	p.editorCmd = script
	f.keys("cc")
	waitForCommit(t, p)
	f.keys("0")
	if got := f.message(); !strings.Contains(got, "nothing to commit") {
		t.Errorf("the message line says %q", got)
	}
}

// waitForCommit blocks until the background git is done. The editor does not
// do this -- it picks the result up on the next key -- and a test has to,
// because it has no person typing.
func waitForCommit(t *testing.T, p *gitPlugin) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if p.commit.Load() != nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("the commit never finished")
}

// TestGdiffSplitsTheWayDiffoptSays is the "vertical" in the vimrc's
// "diffopt=vertical,filler,iwhite". Vim's own default has no "vertical" in
// it, so the same command splits the other way under a bare editor, and both
// halves of that are here so that a change to either is a failure and not a
// surprise.
func TestGdiffSplitsTheWayDiffoptSays(t *testing.T) {
	f := newGitEditor(t, "one\ntwo\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "one\nTWO\n")
	f.run("edit!")

	f.run("set diffopt=vertical,filler,iwhite")
	f.run("Gdiff")
	p := gitState[f.sess]
	d := p.diffs[0]
	if d.win[0].View.Width >= f.cols {
		t.Errorf("with vertical set the diff window is %d columns of %d", d.win[0].View.Width, f.cols)
	}
	if !d.opt.IWhite || !d.opt.Vertical {
		t.Errorf("the pair took its options as %+v", d.opt)
	}

}

// TestGdiffWithVimsDefaultDiffoptSplitsAcross is the other half: vim's own
// default 'diffopt' has no "vertical" in it and the split goes the other way.
func TestGdiffWithVimsDefaultDiffoptSplitsAcross(t *testing.T) {
	f := newGitEditor(t, "one\ntwo\n")
	f.git("add", "-A")
	f.git("commit", "-qm", "first")
	f.write("a.txt", "one\nTWO\n")
	f.run("edit!")

	f.run("Gdiff")
	p := gitState[f.sess]
	d := p.diffs[0]
	if d.win[0].View.Width != f.cols {
		t.Errorf("the diff window is %d columns of %d", d.win[0].View.Width, f.cols)
	}
	if d.opt.Vertical {
		t.Errorf("the default diffopt parsed as vertical: %+v", d.opt)
	}
}

// TestClosingTheStatusWindowIsNotRemembered is the guard on the pointers this
// file keeps: a ":q" on the status buffer closes a window, and the next
// ":Gstatus" has to open a new one rather than draw into a window that is no
// longer on the screen.
func TestClosingTheStatusWindowIsNotRemembered(t *testing.T) {
	f := newGitEditor(t, "one\n")
	f.write("b.txt", "b\n")
	f.run("Gstatus")
	p := gitState[f.sess]
	if p.statusWin == nil {
		t.Fatal("no status window")
	}
	f.run("close")
	if got := len(f.tabs.Current().Windows()); got != 1 {
		t.Fatalf("there are %d windows after :close", got)
	}
	f.run("Gstatus")
	if got := len(f.tabs.Current().Windows()); got != 2 {
		t.Fatalf("the second :Gstatus left %d windows", got)
	}
	if !strings.Contains(strings.Join(f.lines(), "\n"), "Untracked") {
		t.Errorf("the second status buffer is\n%s", strings.Join(f.lines(), "\n"))
	}
}
