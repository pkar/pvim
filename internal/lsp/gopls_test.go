package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests that need the real gopls.
//
// Every one of them is behind testing.Short(), because starting a language
// server and letting it load a workspace is seconds and `make check-fast` is
// meant to be twenty. What they check is the half a fake cannot: that this
// client's handshake is one gopls accepts, that its capabilities produce
// completion items an editor can insert rather than snippets it cannot, that
// a formatting reply applied by ApplyEdits is byte-identical to gofmt, and
// that the process is gone when the client is closed.

// goplsBin finds the server the way cmd/pvim will, and skips the test when
// there is none.
func goplsBin(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: this test starts a real gopls")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	bin, err := Find(home, "gopls")
	if err != nil {
		t.Skip("no gopls in ~/.vimgo or on $PATH")
	}
	return bin
}

// module writes a small module into a temporary directory and returns its
// root. The ".git" is what Root walks for and what makes this a workspace
// rather than a loose file.
func module(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/probe\n\ngo 1.26\n"
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// start brings a client up on a root and registers its shutdown.
func start(t *testing.T, root string) *Client {
	t.Helper()
	bin := goplsBin(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Start(ctx, bin, Options{Root: root})
	if err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return c
}

// TestGoplsHandshake: the server accepts this client's initialize and answers
// with incremental sync and "." as a completion trigger, which are the two
// capabilities everything else is built on.
func TestGoplsHandshake(t *testing.T) {
	root := module(t, map[string]string{"a.go": "package probe\n"})
	c := start(t, root)
	if !c.Incremental() {
		t.Error("gopls did not offer incremental sync; didChange would have to send whole documents")
	}
	trigger := c.TriggerCharacters()
	found := false
	for _, s := range trigger {
		if s == "." {
			found = true
		}
	}
	if !found {
		t.Errorf("completion trigger characters are %v; the vimrc's own mapping fires on \".\"", trigger)
	}
}

// TestGoplsCompletionHasGet is the popup gate, minus the popup: typing
// "http." and asking for completion offers Get.
//
// It also measures how long the answer takes once the workspace is warm, and
// fails past the 200 ms. The first request after a cold start is not
// measured and cannot be: gopls is still loading the module then, and what the
// gate is about is the second and every one after it.
func TestGoplsCompletionHasGet(t *testing.T) {
	const src = "package probe\n\nimport \"net/http\"\n\nfunc probe() {\n\t_ = http.\n}\n"
	root := module(t, map[string]string{"a.go": src})
	c := start(t, root)
	file := filepath.Join(root, "a.go")
	if err := c.DidOpen(file, "go", []byte(src)); err != nil {
		t.Fatal(err)
	}

	// Line 5 is "\t_ = http.", and the cursor is after the dot: one tab plus
	// "_ = http." is 10 bytes and 10 UTF-16 units.
	pos := Position{Line: 5, Character: 10}

	var items []CompletionItem
	warm := time.Now().Add(60 * time.Second)
	for time.Now().Before(warm) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		got, err := c.Complete(ctx, file, pos, ".")
		cancel()
		if err != nil {
			t.Fatalf("completion: %v", err)
		}
		if len(got) > 0 {
			items = got
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(items) == 0 {
		t.Fatal("gopls never offered a completion for http.")
	}

	var get *CompletionItem
	for i := range items {
		if items[i].Label == "Get" {
			get = &items[i]
		}
	}
	if get == nil {
		t.Fatalf("no Get among %d candidates; first ten are %v", len(items), labels(items, 10))
	}
	// SnippetSupport false is what makes this true, and it is the difference
	// between a popup an editor can insert from and one full of "${1:url}".
	insert := get.InsertText
	if get.TextEdit != nil {
		insert = get.TextEdit.NewText
	}
	if insert == "" {
		insert = get.Label
	}
	if strings.ContainsAny(insert, "${") {
		t.Errorf("Get inserts %q, which is a snippet; SnippetSupport must stay false", insert)
	}

	// Warm now. This is the number the gate is about.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	begin := time.Now()
	if _, err := c.Complete(ctx, file, pos, "."); err != nil {
		t.Fatal(err)
	}
	took := time.Since(begin)
	t.Logf("warm completion for http. took %v", took)
	if took > 200*time.Millisecond {
		t.Errorf("warm completion took %v, past 200 ms", took)
	}
}

// labels is the first n labels, for a failure message.
func labels(items []CompletionItem, n int) []string {
	var out []string
	for i, it := range items {
		if i >= n {
			break
		}
		out = append(out, it.Label)
	}
	return out
}

// TestGoplsFormatIsGofmt is the format gate at the protocol level: a
// file with a misplaced import, formatted through textDocument/formatting and
// ApplyEdits, comes out byte-identical to gofmt over the same input.
//
// gofmt and not "gofmt -w": the flag writes the file and this compares bytes,
// and the two are the same transformation. The gofmt run is the one in
// $PATH's Go toolchain, which is what "byte-identical to gofmt -w on the same
// input" means here.
func TestGoplsFormatIsGofmt(t *testing.T) {
	const src = "package probe\n\nimport \"os\"\nimport \"fmt\"\n\nfunc probe()   {\nfmt.Println(os.Args)\n}\n"
	root := module(t, map[string]string{"a.go": src})
	c := start(t, root)
	file := filepath.Join(root, "a.go")
	if err := c.DidOpen(file, "go", []byte(src)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, err := c.Format(ctx, file, []byte(src))
	if err != nil {
		t.Fatalf("formatting: %v", err)
	}

	want := gofmt(t, src)
	if string(got) != want {
		t.Fatalf("gopls formatting is not gofmt\n got: %q\nwant: %q", got, want)
	}
	if string(got) == src {
		t.Fatal("the fixture was already formatted; the test proves nothing")
	}
}

// gofmt runs the toolchain's own formatter over src.
func gofmt(t *testing.T, src string) string {
	t.Helper()
	cmd := exec.Command("gofmt")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gofmt: %v", err)
	}
	return string(out)
}

// TestGoplsDiagnostics: a deliberate type error is published, with a range
// that names the line it is on.
func TestGoplsDiagnostics(t *testing.T) {
	const src = "package probe\n\nfunc probe() {\n\tvar n int = \"not an int\"\n\t_ = n\n}\n"
	root := module(t, map[string]string{"a.go": src})
	c := start(t, root)
	file := filepath.Join(root, "a.go")
	if err := c.DidOpen(file, "go", []byte(src)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(60 * time.Second)
	var ds []Diagnostic
	for time.Now().Before(deadline) {
		if ds = c.Diagnostics(file); len(ds) > 0 {
			break
		}
		select {
		case <-c.Updates():
		case <-time.After(200 * time.Millisecond):
		}
	}
	if len(ds) == 0 {
		t.Fatal("no diagnostics for a file with a type error in it")
	}
	// Line 3, zero-based, is the "var n int" line.
	if ds[0].Range.Start.Line != 3 {
		t.Errorf("diagnostic on line %d, want 3: %+v", ds[0].Range.Start.Line, ds[0])
	}
	if ds[0].Severity != SeverityError {
		t.Errorf("severity %d, want %d", ds[0].Severity, SeverityError)
	}
}

// TestGoplsDefinitionReachesTheStandardLibrary is the "gd on os.ReadFile"
// half of the gate at the protocol level: the definition of a standard
// library symbol is a file under GOROOT.
func TestGoplsDefinitionReachesTheStandardLibrary(t *testing.T) {
	const src = "package probe\n\nimport \"os\"\n\nfunc probe() {\n\t_, _ = os.ReadFile(\"x\")\n}\n"
	root := module(t, map[string]string{"a.go": src})
	c := start(t, root)
	file := filepath.Join(root, "a.go")
	if err := c.DidOpen(file, "go", []byte(src)); err != nil {
		t.Fatal(err)
	}

	// Line 5 is "\t_, _ = os.ReadFile(\"x\")"; the cursor sits on the R of
	// ReadFile, which is column 12.
	pos := Position{Line: 5, Character: 12}
	var locs []Location
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		got, err := c.Definition(ctx, file, pos)
		cancel()
		if err != nil {
			t.Fatalf("definition: %v", err)
		}
		if len(got) > 0 {
			locs = got
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(locs) == 0 {
		t.Fatal("no definition for os.ReadFile")
	}
	p := Path(locs[0].URI)
	if !strings.HasSuffix(p, ".go") {
		t.Fatalf("definition is not a Go file: %q", p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("definition points at a file that is not there: %v", err)
	}
	goroot := strings.TrimSpace(run(t, "go", "env", "GOROOT"))
	if goroot != "" && !strings.HasPrefix(p, goroot) {
		t.Errorf("definition of os.ReadFile is %q, which is not under GOROOT %q", p, goroot)
	}
}

// TestGoplsHoverSaysWhatItIs: K over a symbol has something to put in the
// scratch split.
func TestGoplsHoverSaysWhatItIs(t *testing.T) {
	const src = "package probe\n\nimport \"os\"\n\nfunc probe() {\n\t_, _ = os.ReadFile(\"x\")\n}\n"
	root := module(t, map[string]string{"a.go": src})
	c := start(t, root)
	file := filepath.Join(root, "a.go")
	if err := c.DidOpen(file, "go", []byte(src)); err != nil {
		t.Fatal(err)
	}
	var text string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && text == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		got, err := c.Hover(ctx, file, Position{Line: 5, Character: 12})
		cancel()
		if err != nil {
			t.Fatalf("hover: %v", err)
		}
		text = got
		if text == "" {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !strings.Contains(text, "ReadFile") {
		t.Fatalf("hover over os.ReadFile says %q", text)
	}
}

// TestGoplsExitsWithTheClient is the pgrep gate, run against the
// process this test started rather than against every gopls on the machine: a
// pattern kill would take out a sibling's server and a pattern check would see
// one.
func TestGoplsExitsWithTheClient(t *testing.T) {
	root := module(t, map[string]string{"a.go": "package probe\n"})
	bin := goplsBin(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Start(ctx, bin, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	pid := c.PID()
	if pid <= 0 {
		t.Fatal("no process id")
	}
	if !alive(pid) {
		t.Fatalf("gopls %d is not running after Start", pid)
	}
	begin := time.Now()
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	t.Logf("gopls %d exited in %v", pid, time.Since(begin))
	// Close waits for the process, so this is a check and not a poll: if it
	// is still there now, Close returned before the server was gone.
	if alive(pid) {
		t.Fatalf("gopls %d is still running after Close", pid)
	}
	if out := strings.TrimSpace(run(t, "pgrep", "-P", strconv.Itoa(os.Getpid()))); out != "" {
		t.Errorf("this process still has children after Close: %q", out)
	}
}

// alive reports whether a process id is one of this process's live children.
//
// os.FindProcess always succeeds on unix and Signal(0) answers for any process
// on the machine, which would be a false positive the moment the id is reused.
// pgrep -P against this process's own id cannot be: a gopls this client
// started is a child of this process and nothing else is.
func alive(pid int) bool {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Fields(string(out)) {
		if line == strconv.Itoa(pid) {
			return true
		}
	}
	return false
}

// run is a command's standard output, or "" when it fails.
func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// TestRootIsTheGitWalkAndNotTheCwd is the hard problem: with
// 'autochdir' on, a server rooted at the working directory opens a fresh
// workspace per directory. Root walks the FILE's path.
func TestRootIsTheGitWalkAndNotTheCwd(t *testing.T) {
	root := module(t, map[string]string{
		"internal/deep/a.go": "package deep\n",
		"b.go":               "package probe\n",
	})
	for _, f := range []string{"internal/deep/a.go", "b.go"} {
		if got := Root(filepath.Join(root, f)); got != root {
			t.Errorf("Root(%s) = %s, want %s", f, got, root)
		}
	}
	// A directory answers for itself.
	if got := Root(filepath.Join(root, "internal/deep")); got != root {
		t.Errorf("Root of a directory = %s, want %s", got, root)
	}
}

// TestRootTakesAGitFile: "git worktree add" leaves a .git FILE, not a
// directory, and a walk that only looks for directories roots the whole
// worktree at the filesystem root.
func TestRootTakesAGitFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Root(file); got != root {
		t.Fatalf("Root with a .git file = %s, want %s", got, root)
	}
}

// TestFindPrefersTheBinDir: g:go_bin_path first, then $PATH, because the gopls
// in ~/.vimgo is the binary vim-go has been running and there is no gopls on
// $PATH on this machine at all.
func TestFindPrefersTheBinDir(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".vimgo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "gopls")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(home, "gopls")
	if err != nil || got != fake {
		t.Fatalf("Find = %q, %v; want %q", got, err, fake)
	}

	// A file that is there and not executable is not a server.
	if err := os.Chmod(fake, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Find(home, "gopls")
	if err == nil && got == fake {
		t.Fatal("a non-executable file was taken for a server")
	}

	// Nothing anywhere is ErrNoServer and not a guess.
	if _, err := Find(home, "no-such-language-server-anywhere"); err != ErrNoServer {
		t.Fatalf("Find of a missing server = %v, want ErrNoServer", err)
	}
}
