package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/server"
)

// One instance, from both ends.
//
// The socket half is exercised for real: a real unix socket, a real
// internal/server listener, a real client, and the editor behind it driven
// through the same pump the window drives it through. What no test here can
// have is the window itself, so the frame nobody looks at is a fakeClient and
// the rest is the same code.

// shortDir is a temporary directory with a short path.
//
// t.TempDir() is under /var/folders on this machine and the test's own name
// goes into it, which routinely comes to 90 bytes before a file name is added;
// a unix socket path is limited to about 100 by the kernel, and the bind fails
// with a message that reads like a permissions problem. /tmp keeps it to
// twenty-odd.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pvim")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// instance is a running editor with a socket in front of it.
type instance struct {
	ed   *editor
	pump *pump
	cl   *fakeClient
	sock string
	home string
}

// startInstance brings up an editor, a pump and a listener on a socket inside
// a temporary home directory.
func startInstance(t *testing.T, text string) *instance {
	t.Helper()
	home := shortDir(t)
	cache, err := cacheDir(home)
	if err != nil {
		t.Fatal(err)
	}
	sock := server.SocketPath(cache)

	ed := newTestEditor(t, text)
	p := newPump(ed)
	ed.pump = p
	ed.sess.getch = p.getch
	cl := &fakeClient{rows: 40, cols: 120}
	t.Cleanup(p.stop)
	if err := p.handle(cl, gui.ResizeEvent{Rows: 40, Cols: 120}); err != nil {
		t.Fatal(err)
	}

	ln, err := server.Listen(sock)
	if err != nil {
		t.Fatalf("listening on %s: %v", sock, err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() { _ = ln.Serve(ed.handleRequest) }()

	return &instance{ed: ed, pump: p, cl: cl, sock: sock, home: home}
}

// tabs is how many tab pages the editor has, asked on the editor's own
// goroutine because that is the only one allowed to look.
func (in *instance) tabs(t *testing.T) int {
	t.Helper()
	n := 0
	if err := in.pump.do(func(client) error {
		n = len(in.ed.tabs.Pages)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// waitFor polls cond until it holds, which is the shape every test that has a
// socket and a goroutine in it needs.
//
// The deadline is thirty seconds and not two, and the number is a measurement
// rather than a guess. What the second caller waits on is git forking a
// freshly built binary, which then has to be paged in, dial a unix socket and
// be answered by the editor goroutine: about 0.2s on an idle box, and the
// whole test runs in 0.7s. Under `go test ./...` it shares the machine with
// internal/motion and cmd/oracle, both of which fork /opt/homebrew/bin/vim
// thousands of times, and that run took this past two seconds
// and failed here with the editor working perfectly. A poll that has already
// succeeded costs nothing, so the only thing a long deadline buys is how long
// a genuine hang takes to report, and the package timeout bounds that anyway.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestASecondPvimOpensATabInTheRunningOne is the headline: "pvim file" from any
// shell lands in the editor that is already up.
func TestASecondPvimOpensATabInTheRunningOne(t *testing.T) {
	in := startInstance(t, "alpha\n")
	dir := shortDir(t)
	file := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(file, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := server.Open(in.sock, server.Request{Op: server.OpOpen, Path: file, Cwd: dir}); err != nil {
		t.Fatalf("the client could not hand the file over: %v", err)
	}
	if got := in.tabs(t); got != 2 {
		t.Fatalf("the running instance has %d tabs, want 2", got)
	}
	if err := in.pump.do(func(client) error {
		if got, want := string(in.ed.buf.Line(1)), "second"; got != want {
			t.Errorf("the new tab shows %q, want %q", got, want)
		}
		if got := in.ed.cur().Name; got != file {
			t.Errorf("the new tab's buffer is %q, want %q", got, file)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestAttachFindsNoInstance. With nothing listening, attach says so without an
// error and the caller becomes the editor itself. That branch is the one every
// cold start takes and getting it wrong means no editor at all.
func TestAttachFindsNoInstance(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "pvim.sock")
	handled, err := attach(sock, config{file: filepath.Join(dir, "f.txt")})
	if err != nil {
		t.Fatalf("attach with nothing listening returned %v", err)
	}
	if handled {
		t.Error("attach claimed a file was handed over with no instance running")
	}
}

// TestAttachIsSkippedByNewAndByHavingNoFile.
//
// --new is the opt-out and has to be honoured before the socket is touched at
// all. No file is the other case: "pvim" on its own from a second shell starts
// its own editor rather than opening an empty tab in somebody else's window,
// which is what gvim does with no arguments.
func TestAttachIsSkippedByNewAndByHavingNoFile(t *testing.T) {
	in := startInstance(t, "alpha\n")
	file := filepath.Join(shortDir(t), "f.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []config{
		{file: file, newInstance: true},
		{},
	} {
		handled, err := attach(in.sock, c)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if handled {
			t.Errorf("%+v attached anyway", c)
		}
	}
	if got := in.tabs(t); got != 1 {
		t.Errorf("the running instance grew to %d tabs", got)
	}
}

// TestWaitBlocksUntilTheTabCloses is what EDITOR="pvim --wait" reduces to: the
// second process stays alive until the buffer it handed over is gone, and only
// then does the shell that spawned it carry on.
func TestWaitBlocksUntilTheTabCloses(t *testing.T) {
	in := startInstance(t, "alpha\n")
	dir := shortDir(t)
	file := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(file, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- server.Open(in.sock, server.Request{Op: server.OpOpen, Path: file, Cwd: dir, Wait: true})
	}()
	waitFor(t, "the tab to open", func() bool { return in.tabs(t) == 2 })

	select {
	case err := <-done:
		t.Fatalf("the waiting client returned %v before the tab closed", err)
	case <-time.After(20 * time.Millisecond):
	}

	// Write it and close the tab, which is what a person does in the commit
	// message that git opened.
	if err := send(t, in.pump, in.cl, "ccnew\x1b:w\r:q\r"); err != nil {
		t.Fatalf("editing the tab returned %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the waiting client returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting client is still blocked after the tab closed")
	}
	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new\n" {
		t.Errorf("the file holds %q, want %q", got, "new\n")
	}
}

// TestEditorGitCommit is the gate item, end to end and with nothing
// faked but the window: a real git repository, git's own EDITOR handling, the
// real pvim binary as the client, a real unix socket, and the commit landing
// when the tab closes.
//
// It builds the binary, which is why it is behind -short: `make check-fast` is
// there to be run in a loop and a `go build` in it is seconds nobody asked for.
// `make check` runs it.
func TestEditorGitCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary and shells out to git")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this box")
	}

	in := startInstance(t, "alpha\n")
	bin := filepath.Join(shortDir(t), "pvim")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the client: %v\n%s", err, out)
	}

	repo := shortDir(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		cmd.Env = gitEnv(in.home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.invalid")
	run("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")

	// EDITOR="pvim --wait", exactly as the gate writes it. HOME points
	// at the instance's own cache directory, so the client finds this test's
	// socket and not the one belonging to whoever is running the suite.
	commit := exec.Command(git, "commit", "-q")
	commit.Dir = repo
	commit.Env = gitEnv(in.home, "EDITOR="+bin+" --wait")
	// Combined output, because when this fails the reason is nearly always
	// something git or the client said on the way past.
	var said []byte
	out := make(chan error, 1)
	go func() {
		var err error
		said, err = commit.CombinedOutput()
		out <- err
	}()
	t.Cleanup(func() {
		if commit.Process != nil {
			_ = commit.Process.Kill()
		}
	})

	waitFor(t, "git to open the commit message in a tab", func() bool {
		select {
		case err := <-out:
			t.Fatalf("git commit returned %v before opening a tab:\n%s", err, said)
		default:
		}
		return in.tabs(t) == 2
	})
	if err := in.pump.do(func(client) error {
		if got := filepath.Base(in.ed.cur().Name); got != "COMMIT_EDITMSG" {
			t.Errorf("the tab holds %q, want COMMIT_EDITMSG", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := send(t, in.pump, in.cl, "ggIfrom pvim\x1b:w\r:q\r"); err != nil {
		t.Fatalf("writing the commit message returned %v", err)
	}
	select {
	case err := <-out:
		if err != nil {
			t.Fatalf("git commit failed: %v\n%s", err, said)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("git is still waiting after the tab closed")
	}

	log := exec.Command(git, "log", "-1", "--pretty=%s")
	log.Dir = repo
	log.Env = commit.Env
	msg, err := log.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(msg)); got != "from pvim" {
		t.Errorf("the commit subject is %q, want %q", got, "from pvim")
	}
}

// gitEnv is the environment a git in this test runs with: this process's, with
// the four variables that decide which editor git launches taken out and
// whatever the caller wants put back.
//
// Taken out rather than overridden, because they cannot be overridden by
// appending. git's order is GIT_EDITOR, then core.editor, then VISUAL, then
// EDITOR, and the first one set wins; an exported GIT_EDITOR in the shell that
// ran `go test` therefore beats the EDITOR this test is here to exercise. That
// is not hypothetical: this box has GIT_EDITOR=true, and before this function
// existed the gate failed with "Aborting commit due to empty commit message"
// having never run pvim at all, which reads like a socket bug and is not one.
// core.editor is handled by the two GIT_CONFIG_ variables, which point the
// global config at a file inside the instance's home and the system one at
// /dev/null so the person running the suite keeps their own config.
func gitEnv(home string, extra ...string) []string {
	drop := map[string]bool{"GIT_EDITOR": true, "GIT_SEQUENCE_EDITOR": true, "VISUAL": true, "EDITOR": true}
	env := make([]string, 0, len(os.Environ())+len(extra)+3)
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); !ok || !drop[name] {
			env = append(env, kv)
		}
	}
	env = append(env,
		"HOME="+home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, "gitconfig"),
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	return append(env, extra...)
}

// TestEscapeExArg. The path comes from another shell and internal/ex reads a
// backslash as an escape, "%" and "#" as the current and alternate file, a
// space as the end of the argument and a bar as the end of the command.
func TestEscapeExArg(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/tmp/plain.txt", "/tmp/plain.txt"},
		{"/tmp/two words.txt", `/tmp/two\ words.txt`},
		{"/tmp/100%.txt", `/tmp/100\%.txt`},
		{"/tmp/a#b.txt", `/tmp/a\#b.txt`},
		{`/tmp/back\slash`, `/tmp/back\\slash`},
		{"/tmp/a|b", `/tmp/a\|b`},
	}
	for _, c := range cases {
		if got := escapeExArg(c.in); got != c.want {
			t.Errorf("escapeExArg(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAPathWithAwkwardCharactersOpens is the escaping the other way round: not
// what the string looks like, but that the file that ends up in the tab is the
// one that was asked for.
func TestAPathWithAwkwardCharactersOpens(t *testing.T) {
	in := startInstance(t, "alpha\n")
	dir := shortDir(t)
	file := filepath.Join(dir, "a b%c#d.txt")
	if err := os.WriteFile(file, []byte("awkward\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := server.Open(in.sock, server.Request{Op: server.OpOpen, Path: file, Cwd: dir}); err != nil {
		t.Fatalf("handing over %q: %v", file, err)
	}
	if err := in.pump.do(func(client) error {
		if got := in.ed.cur().Name; got != file {
			t.Errorf("the tab holds %q, want %q", got, file)
		}
		if got, want := string(in.ed.buf.Line(1)), "awkward"; got != want {
			t.Errorf("the tab shows %q, want %q", got, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
