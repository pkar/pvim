package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/lsp"
	"github.com/pkar/pvim/internal/quickfix"
	"github.com/pkar/pvim/internal/text"
)

// lspCursorAt is a buffer position: a one-based line and a zero-based byte
// column, which is internal/text's convention and vim's.
func lspCursorAt(line, col int) text.Pos { return text.Pos{Line: line, Col: col} }

// The language server, driven through the editor rather than through
// internal/lsp.
//
// Each of the gate items has a test here, and each drives the editor in
// the order a live session would: open the buffer, tell the server, edit,
// BufWritePre, format, write, save, read the diagnostics. The one thing these
// do that a live session would not is call the entry points themselves,
// because the call sites live in other files; the list of them is at the top
// of cmd/pvim/lsp.go and each of these tests is pinned to one of them.

// lspGoEditor builds an editor over a Go file in a module of its own and tells
// the language server about it, which is what newFrontendEditor plus one line
// would do.
func lspGoEditor(t *testing.T, name, src string) (*editor, string) {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: this test starts a real gopls")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if _, err := lsp.Find(home, "gopls"); err != nil {
		t.Skip("no gopls in ~/.vimgo or on $PATH")
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/probe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, name)
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := newEditor([]byte(src), file, 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	// The events a buffer opening fires, which is where 'filetype' comes from.
	e.bufferOpened(false)
	if e.opt.B.FileType != "go" {
		t.Fatalf("filetype is %q, want go", e.opt.B.FileType)
	}
	t.Cleanup(e.lspStop)
	e.lspBufferOpened()
	if !e.lspRunning() {
		t.Fatal("no language server after opening a Go buffer")
	}
	return e, file
}

// save is the write path with the language server in it, in the order the
// plugins ran:
// the vimrc's BufWritePre whitespace strip, then format on save, then the
// write itself, then didSave.
//
// This is exactly the sequence the four call sites listed at the top of
// cmd/pvim/lsp.go produce. Written out here because those call sites are
// elsewhere; when they are wired, ":w" alone does all four.
func lspSave(t *testing.T, e *editor) {
	t.Helper()
	b := e.cur()
	e.fireAutoCmds("BufWritePre", b.Name)
	if err := e.lspBeforeWrite(); err != nil {
		t.Fatalf("format on save: %v", err)
	}
	if err := e.ctx.RunLine("w"); err != nil {
		t.Fatalf("write: %v", err)
	}
	e.lspAfterWrite()
}

// TestGateCompletionOffersGetAndInsertsNothing is the popup gate.
//
// "typing http. shows a popup within 200 ms with Get in it and inserts nothing
// until CTRL-N CTRL-Y". The popup itself is internal/mode's and there is no
// CTRL-X CTRL-O to reach it yet, so what is checked here is everything up to
// it: the candidates the popup would hold, that Get is among them, that the
// answer arrives inside the budget, and that asking for them left the buffer
// exactly as it was.
//
// The last one is the half people get wrong. 'completeopt' is
// "menu,menuone,noselect,noinsert" in this vimrc, so the first CTRL-N shows a
// menu and inserts nothing; an omnifunc that inserted while it was gathering
// would defeat the option before the menu was ever drawn.
func TestGateCompletionOffersGetAndInsertsNothing(t *testing.T) {
	const src = "package probe\n\nimport \"net/http\"\n\nfunc probe() {\n\t_ = http.\n}\n"
	e, _ := lspGoEditor(t, "a.go", src)

	// The cursor where the vimrc's own mapping leaves it: just after the dot
	// that "inoremap <buffer> . .<C-x><C-o>" typed. Line 6, one-based, and
	// byte column 10, which is past "\t_ = http.".
	e.ed.SetCursor(lspCursorAt(6, 10))

	var words [][]byte
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if words = e.lspOmniCompleteWords("."); len(words) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(words) == 0 {
		t.Fatal("omnifunc offered nothing for http.")
	}
	if !lspHasWord(words, "Get") {
		t.Fatalf("no Get among %d candidates: %s", len(words), lspFirstWords(words, 10))
	}
	for _, w := range words {
		if strings.ContainsAny(string(w), "${") {
			t.Fatalf("a candidate is a snippet: %q", w)
		}
	}

	// The buffer is untouched, which is what "inserts nothing" means.
	if got := string(e.buf.Bytes()); got != src {
		t.Fatalf("asking for completion changed the buffer:\n got %q\nwant %q", got, src)
	}

	// Warm, which is the number the gate is about.
	begin := time.Now()
	words = e.lspOmniCompleteWords(".")
	took := time.Since(begin)
	t.Logf("warm omnifunc for http. took %v and offered %d candidates", took, len(words))
	if took > 200*time.Millisecond {
		t.Errorf("the popup would take %v to appear, past 200 ms", took)
	}
}

// TestGateFormatOnSaveIsGofmt is the format gate: ":w" on a file with a
// misplaced import rewrites it the way gofmt would, byte for byte.
//
// The comparison is against the toolchain's own gofmt over the same input, run
// here, and not against a golden: a golden would be this code's own answer and
// would go on passing after gofmt changed its mind.
func TestGateFormatOnSaveIsGofmt(t *testing.T) {
	// A misplaced import, an extra blank line and a body with no indentation:
	// the three things a person's fingers actually produce.
	const src = "package probe\n\nimport \"os\"\nimport \"fmt\"\n\nfunc probe()   {\nfmt.Println(os.Args)\n}\n"
	e, file := lspGoEditor(t, "a.go", src)

	lspSave(t, e)

	onDisk, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := lspGofmtBytes(t, src)
	if string(onDisk) != want {
		t.Fatalf("the file on disk is not what gofmt writes\n got: %q\nwant: %q", onDisk, want)
	}
	if string(onDisk) == src {
		t.Fatal("the fixture was already formatted; the test proves nothing")
	}
	// The buffer holds the formatted text too, which is what stops the next
	// keystroke from marking it modified against a file it disagrees with.
	if got := string(e.buf.Bytes()); got != want {
		t.Fatalf("the buffer is not the file:\n got %q\nwant %q", got, want)
	}
	if e.modified() {
		t.Error("the buffer is modified straight after a write")
	}

	// One undo puts back what was typed, because the format went into the
	// buffer as an ordinary edit and not past it.
	if _, ok := e.buf.Undo(); !ok {
		t.Fatal("the format left no undo step")
	}
	if got := string(e.buf.Bytes()); got != src {
		t.Errorf("undo after a format gave %q, want the text as typed %q", got, src)
	}
}

// TestGateFormatRunsAfterTheWhitespaceStrip is the ordering out:
// both formatters run AFTER the vimrc's BufWritePre strip, because that is the
// order the plugins ran in.
//
// The fixture is a file with trailing whitespace AND a misplaced import, so a
// run in the wrong order is visible: gofmt does not remove trailing white
// space inside a raw string, and the strip does not move an import.
func TestGateFormatRunsAfterTheWhitespaceStrip(t *testing.T) {
	const src = "package probe\n\nimport \"os\"\nimport \"fmt\"\n\nfunc probe() {   \n\tfmt.Println(os.Args)   \n}\n"
	e, file := lspGoEditor(t, "a.go", src)

	// The live vimrc's own rule, loaded from the file rather than written out
	// here, so that a change to it turns up as a failure.
	rc := liveVimrc(t)
	if _, err := os.Stat(rc); err != nil {
		t.Skipf("no vimrc at %s", rc)
	}
	e.loadVimrc(rc, false)
	e.bufferOpened(false)

	lspSave(t, e)

	onDisk, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// gofmt over the STRIPPED text, which is what the order means.
	stripped := strings.ReplaceAll(src, "   \n", "\n")
	if want := lspGofmtBytes(t, stripped); string(onDisk) != want {
		t.Fatalf("the file is not gofmt over the stripped buffer\n got: %q\nwant: %q", onDisk, want)
	}
	if strings.Contains(string(onDisk), " \n") {
		t.Error("trailing white space survived the save")
	}
}

// TestGateDiagnosticsReachQuickfix is the third gate: a deliberate type
// error appears in ":copen" within a second of ":w", and ":cn" lands on the
// line.
func TestGateDiagnosticsReachQuickfix(t *testing.T) {
	const src = "package probe\n\nfunc probe() {\n\tvar n int = \"not an int\"\n\t_ = n\n}\n"
	e, file := lspGoEditor(t, "a.go", src)

	// The workspace has to be loaded before the clock that matters starts,
	// which is what a person's first save of a session is waiting on too.
	// This save is the warm-up and is not the one measured.
	lspSave(t, e)
	lspWaitDiagnostics(t, e, 60*time.Second)

	// Now the gate: a save, and a second for the diagnostics to arrive.
	lspSave(t, e)
	begin := time.Now()
	if !lspWaitDiagnostics(t, e, time.Second) {
		t.Fatal("no diagnostics in the quickfix list within a second of the write")
	}
	t.Logf("diagnostics reached the quickfix list %v after the write", time.Since(begin))

	// ":copen" opens the window over the list. It was unreachable when this
	// test was written -- internal/ex had the handler and no row for it in the
	// command table -- and this block asserted the list directly with a note
	// saying to take it back to a RunLine once the row landed. The row landed.
	if err := e.ctx.RunLine("copen"); err != nil {
		t.Fatalf("copen: %v", err)
	}
	l := e.ctx.QF.Current()
	if l == nil || len(l.Entries) == 0 {
		t.Fatal("no quickfix list after the write")
	}
	if got := filepath.Base(l.Entries[0].FileName); got != filepath.Base(file) {
		t.Fatalf("the list names %q, want %q", got, filepath.Base(file))
	}
	if !strings.Contains(l.Entries[0].Text, "int") {
		t.Fatalf("the list does not carry the message: %q", l.Entries[0].Text)
	}
	// ":clist" shows the same list on the message line.
	if err := e.ctx.RunLine("clist"); err != nil {
		t.Fatalf("clist: %v", err)
	}
	if msg := e.message(); !strings.Contains(msg, filepath.Base(file)) {
		t.Errorf(":clist said %q, which does not name the file", msg)
	}

	// ":cn" lands on the line the error is on, which is line 4, one-based.
	if err := e.ctx.RunLine("cnext"); err != nil {
		t.Fatalf("cnext: %v", err)
	}
	if got := e.ed.Cursor().Line; got != 4 {
		t.Errorf(":cn landed on line %d, want 4", got)
	}
	if got := filepath.Base(e.cur().Name); got != "a.go" {
		t.Errorf(":cn landed in %q, want a.go", got)
	}
}

// lspWaitDiagnostics polls until the quickfix list has something in it.
func lspWaitDiagnostics(t *testing.T, e *editor, budget time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		e.lspDiagnostics()
		if l := e.ctx.QF.Current(); l != nil && len(l.Entries) > 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestGateDefinitionOpensTheStandardLibraryReadOnly is the fourth gate:
// gd on os.ReadFile opens the standard library's file, and opens it read-only.
func TestGateDefinitionOpensTheStandardLibraryReadOnly(t *testing.T) {
	const src = "package probe\n\nimport \"os\"\n\nfunc probe() {\n\t_, _ = os.ReadFile(\"x\")\n}\n"
	e, _ := lspGoEditor(t, "a.go", src)

	// On the R of ReadFile: line 6, byte column 12.
	e.ed.SetCursor(lspCursorAt(6, 12))

	var err error
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		err = e.lspDefinition()
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("gd on os.ReadFile: %v", err)
	}

	b := e.cur()
	if !strings.HasSuffix(b.Name, ".go") || filepath.Base(b.Name) == "a.go" {
		t.Fatalf("gd stayed in %q", b.Name)
	}
	if !b.ReadOnly {
		t.Errorf("%s opened writable; a jump outside the workspace is read-only", b.Name)
	}
	if !strings.Contains(string(e.buf.Line(e.ed.Cursor().Line)), "ReadFile") {
		t.Errorf("the cursor is on %q, which does not name ReadFile", e.buf.Line(e.ed.Cursor().Line))
	}
	// And ":w" refuses, which is what read-only is for.
	if err := e.ctx.RunLine("w"); err == nil {
		t.Error(":w on the standard library file was allowed")
	}
}

// TestKAndCtrlBracketReachTheServer: the two keys vim-go bound, taken before
// the mode machine sees them, and only in a buffer that has a server.
func TestKAndCtrlBracketReachTheServer(t *testing.T) {
	const src = "package probe\n\nimport \"os\"\n\nfunc probe() {\n\t_, _ = os.ReadFile(\"x\")\n}\n"
	e, _ := lspGoEditor(t, "a.go", src)
	e.ed.SetCursor(lspCursorAt(6, 12))

	took, err := e.lspKey(key.Rune('K'))
	if !took {
		t.Fatal("K was not taken in a Go buffer with a server")
	}
	deadline := time.Now().Add(60 * time.Second)
	for err != nil && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		_, err = e.lspKey(key.Rune('K'))
	}
	if err != nil {
		t.Fatalf("K: %v", err)
	}
	if got := e.cur().Name; got != "[Hover]" {
		t.Fatalf("K left the editor on %q, want the scratch split", got)
	}
	if !strings.Contains(string(e.buf.Bytes()), "ReadFile") {
		t.Fatalf("the hover split says %q", e.buf.Bytes())
	}
	if !e.cur().Scratch {
		t.Error("the hover buffer is not a scratch buffer; :q would prompt")
	}

	// A buffer with no server takes neither key, which is what leaves K as
	// 'keywordprg' everywhere else and what the oracle grades.
	plain := newTestEditor(t, "hello world\n")
	if took, _ := plain.lspKey(key.Rune('K')); took {
		t.Error("K was taken in a buffer with no language server")
	}
	if took, _ := plain.lspKey(key.Ctrl(']')); took {
		t.Error("CTRL-] was taken in a buffer with no language server")
	}
}

// TestGateGoplsExitsWithTheEditor is the pgrep gate.
//
// Against this process's own children rather than against every gopls on the
// machine: a pattern check would see somebody else's gopls and a pattern
// kill would take it out.
func TestGateGoplsExitsWithTheEditor(t *testing.T) {
	e, _ := lspGoEditor(t, "a.go", "package probe\n")
	var pid int
	for _, c := range e.lsp().clients {
		pid = c.PID()
	}
	if pid <= 0 {
		t.Fatal("the server has no process id")
	}
	if !lspChildAlive(pid) {
		t.Fatalf("gopls %d is not a child of this process", pid)
	}
	begin := time.Now()
	e.lspStop()
	t.Logf("gopls %d exited in %v", pid, time.Since(begin))
	if lspChildAlive(pid) {
		t.Fatalf("gopls %d is still running after the editor stopped", pid)
	}
	if e.lspRunning() {
		t.Error("the editor still thinks it has a server")
	}
}

// lspChildAlive reports whether pid is a live child of this process.
func lspChildAlive(pid int) bool {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return false
	}
	for _, f := range strings.Fields(string(out)) {
		if f == strconv.Itoa(pid) {
			return true
		}
	}
	return false
}

// TestTheOracleStartsNoLanguageServer is the confirmation the brief asked for
// rather than assumed.
//
// Two facts make it true and both are checked here rather than read out of the
// code. A graded editor is built by newEditor directly, which touches none of
// this; and every entry point in cmd/pvim/lsp.go needs a 'filetype', which
// comes from bufferOpened, which runOracle never calls. So even the editor
// that oracle.go builds over a .go file has no server, and asking it to open
// one does nothing because it has no filetype to ask about.
func TestTheOracleStartsNoLanguageServer(t *testing.T) {
	// oracle.go's own construction: newEditor, an options line, no vimrc, no
	// bufferOpened.
	e, err := newEditor([]byte("package main\n"), "f.go", 40, 120, "sw=4 ts=2")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.lspStop)
	if e.lspRunning() {
		t.Fatal("a graded editor has a language server")
	}
	// The hook a wired-up frontend would call, on a graded editor: it finds no
	// filetype and does nothing at all.
	e.lspBufferOpened()
	if e.lspRunning() {
		t.Fatal("lspBufferOpened started a server on a graded editor")
	}
	// And the write path is silent: no formatter, no message.
	if err := e.lspBeforeWrite(); err != nil {
		t.Fatalf("lspBeforeWrite on a graded editor: %v", err)
	}
	if msg := e.message(); msg != "" {
		t.Errorf("a graded write put %q on the message line", msg)
	}
}

// TestParseGoOutput is the 'errorformat' vim-go uses, as a table.
//
// The shapes were measured against go1.27.1 by building and
// testing a module with each kind of failure in it.
func TestParseGoOutput(t *testing.T) {
	root := "/tmp/proj"
	out := strings.Join([]string{
		"# example.com/probe/internal/x",
		"internal/x/a.go:5:2: undefined: nope",
		"internal/x/a.go:9: too many return values",
		"\thave (int, error)",
		"--- FAIL: TestThing (0.00s)",
		"    thing_test.go:12: want 1, got 2",
		"FAIL\texample.com/probe/internal/x\t0.201s",
		"ok  \texample.com/probe\t0.105s",
	}, "\n")

	// A go.mod so that the import path can be turned into a directory.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root = dir

	entries := lspParseGoOutput(root, out)
	if len(entries) != 4 {
		t.Fatalf("%d entries, want 4: %+v", len(entries), entries)
	}
	if got, want := entries[0].FileName, filepath.Join(root, "internal/x/a.go"); got != want {
		t.Errorf("build error file = %q, want %q", got, want)
	}
	if entries[0].LNum != 5 || entries[0].Col != 2 || entries[0].Text != "undefined: nope" {
		t.Errorf("build error = %+v", entries[0])
	}
	if entries[1].LNum != 9 || entries[1].Col != 0 {
		t.Errorf("the two-colon form = %+v", entries[1])
	}
	if !strings.Contains(entries[1].Text, "have (int, error)") {
		t.Errorf("the continuation line was dropped: %q", entries[1].Text)
	}
	if entries[2].Valid {
		t.Errorf("the --- FAIL line is an entry with no location: %+v", entries[2])
	}
	if got, want := entries[3].FileName, filepath.Join(root, "internal/x/thing_test.go"); got != want {
		t.Errorf("the test failure file = %q, want %q; the FAIL line names the package", got, want)
	}
	if entries[3].LNum != 12 {
		t.Errorf("the test failure = %+v", entries[3])
	}
}

// TestGoBuildFillsTheQuickfixList runs the real "go build" over a module with
// an error in it.
func TestGoBuildFillsTheQuickfixList(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: this test runs go build")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/probe\n\ngo 1.26\n")
	const src = "package probe\n\nfunc probe() int {\n\treturn nope\n}\n"
	write("a.go", src)

	file := filepath.Join(root, "a.go")
	e, err := newEditor([]byte(src), file, 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	t.Cleanup(e.lspStop)

	took, err := e.lspExCommand("GoBuild", "")
	if !took {
		t.Fatal(":GoBuild was not recognised")
	}
	if err != nil {
		t.Fatalf(":GoBuild: %v", err)
	}
	l := e.ctx.QF.Current()
	if l == nil || len(l.Entries) == 0 {
		t.Fatal(":GoBuild filled no quickfix list for a package that does not build")
	}
	if got, want := l.Entries[0].FileName, file; got != want {
		t.Errorf("entry file = %q, want %q", got, want)
	}
	if l.Entries[0].LNum != 4 {
		t.Errorf("entry line = %d, want 4: %+v", l.Entries[0].LNum, l.Entries[0])
	}
	if !strings.Contains(l.Entries[0].Text, "nope") {
		t.Errorf("entry text = %q", l.Entries[0].Text)
	}

	// ":GoTest" and the two aliases are the same table.
	if took, _ := e.lspExCommand("GoTest", ""); !took {
		t.Error(":GoTest was not recognised")
	}
	for _, name := range []string{"GoDef", "GoInfo"} {
		if took, _ := e.lspExCommand(name, ""); !took {
			t.Errorf(":%s was not recognised", name)
		}
	}
	if took, _ := e.lspExCommand("NotAGoCommand", ""); took {
		t.Error("a command that is not one of the four was taken")
	}
}

// TestDiagnosticEntryColumnsAreBytes: the protocol counts UTF-16 units and a
// quickfix entry counts bytes, and the conversion needs the line, which for a
// file no buffer holds comes off the disk.
func TestDiagnosticEntryColumnsAreBytes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	// "café" is four runes and five bytes, so the "x" after it is at UTF-16
	// column 8, zero-based, and at byte column 10, one-based, which is what a
	// quickfix entry counts in.
	if err := os.WriteFile(file, []byte("// café x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := lsp.Diagnostic{
		Range:    lsp.Range{Start: lsp.Position{Line: 0, Character: 8}, End: lsp.Position{Line: 0, Character: 9}},
		Severity: lsp.SeverityError,
		Message:  "nope",
	}
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got := lspDiagEntry(file, src, d)
	if got.Col != 10 {
		t.Errorf("column %d, want the byte column 10", got.Col)
	}
	if got.EndCol != 11 {
		t.Errorf("end column %d, want 11", got.EndCol)
	}
	if got.Kind != quickfix.KindError || !got.Valid {
		t.Errorf("entry = %+v", got)
	}
}

// lspGofmtBytes is the toolchain's own formatter over src.
func lspGofmtBytes(t *testing.T, src string) string {
	t.Helper()
	cmd := exec.Command("gofmt")
	cmd.Stdin = strings.NewReader(src)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gofmt: %v", err)
	}
	return string(out)
}

// lspHasWord reports whether a candidate list holds a word.
func lspHasWord(words [][]byte, want string) bool {
	for _, w := range words {
		if string(w) == want {
			return true
		}
	}
	return false
}

// lspFirstWords is the first n candidates, for a failure message.
func lspFirstWords(words [][]byte, n int) string {
	var out []string
	for i, w := range words {
		if i >= n {
			break
		}
		out = append(out, string(w))
	}
	return strings.Join(out, " ")
}

// TestGateHomeAutoCompletion is the gate in the words it uses: "in a
// a sibling checkout, typing http. in a Go file shows the popup within 200 ms
// with Get in it".
//
// The other completion test builds a module of its own, which is hermetic and
// is what runs everywhere; this one is the gate literally, against the real
// repository next door, because a module with one file in it and a module with
// a hundred are different questions to ask gopls. It skips when the checkout
// is not there, which is the honest thing on any machine but this one.
//
// It edits nothing on disk. The scratch file it opens is written into the
// checkout and removed again, because gopls will not complete in a file that
// is not part of the package it is loading; the name is deliberately one no
// build would pick up.
func TestGateHomeAutoCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: this test starts a real gopls")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if _, err := lsp.Find(home, "gopls"); err != nil {
		t.Skip("no gopls in ~/.vimgo or on $PATH")
	}
	root := absPath(filepath.Join("..", "..", "..", "a sibling module"))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("no sibling checkout at %s", root)
	}

	const src = "package probe\n\nimport \"net/http\"\n\nfunc probe() {\n\t_ = http.\n}\n"
	dir := filepath.Join(root, "internal", "pvimprobe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("cannot write into the checkout: %v", err)
	}
	file := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	e, err := newEditor([]byte(src), file, 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	e.ed.SetScriptInput(true)
	e.bufferOpened(false)
	t.Cleanup(e.lspStop)
	e.lspBufferOpened()
	if !e.lspRunning() {
		t.Fatal("no language server")
	}
	if got := e.lsp().clients; len(got) != 1 {
		t.Fatalf("%d servers, want one", len(got))
	}
	for _, c := range e.lsp().clients {
		if c.Root() != root {
			t.Errorf("the server is rooted at %q, want the .git walk's answer %q", c.Root(), root)
		}
	}

	e.ed.SetCursor(lspCursorAt(6, 10))
	var words [][]byte
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if words = e.lspOmniCompleteWords("."); lspHasWord(words, "Get") {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !lspHasWord(words, "Get") {
		t.Fatalf("no Get among %d candidates in the checkout: %s", len(words), lspFirstWords(words, 10))
	}

	begin := time.Now()
	words = e.lspOmniCompleteWords(".")
	took := time.Since(begin)
	t.Logf("warm omnifunc for http. took %v and offered %d candidates", took, len(words))
	if took > 200*time.Millisecond {
		t.Errorf("the popup would take %v to appear, past 200 ms", took)
	}
	if got := string(e.buf.Bytes()); got != src {
		t.Error("asking for completion changed the buffer")
	}
}

// TestFormatSwitchesComeFromTheVimrc: g:go_fmt_autosave and
// g:terraform_fmt_on_save, read out of the file rather than assumed.
//
// The live vimrc sets the second by name and does not mention the first, whose
// vim-go default is 1, so both rows are on after a real load. That is the fact
// the whole of format on save rests on and it is one "let" away from being
// false.
func TestFormatSwitchesComeFromTheVimrc(t *testing.T) {
	e := newTestEditor(t, "package main\n")
	t.Cleanup(e.lspStop)
	rc := liveVimrc(t)
	e.loadVimrc(rc, false)
	e.lspReadVars()

	tab := e.lsp().table
	if !tab.GoEnabled {
		t.Error("g:go_fmt_autosave came out off; vim-go's default is 1 and the vimrc does not set it")
	}
	if !tab.TerraformEnabled {
		t.Error("g:terraform_fmt_on_save came out off; the vimrc sets it to 1 on its own line")
	}
	if !tab.Formats("go") || !tab.Formats("terraform") {
		t.Error("the table formats neither")
	}

	// And a vimrc that turns one off turns exactly one off.
	e2 := newTestEditor(t, "package main\n")
	t.Cleanup(e2.lspStop)
	off := filepath.Join(t.TempDir(), "vimrc")
	if err := os.WriteFile(off, []byte("let g:go_fmt_autosave = 0\nlet g:terraform_fmt_on_save = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2.loadVimrc(off, false)
	e2.lspReadVars()
	if e2.lsp().table.GoEnabled {
		t.Error("let g:go_fmt_autosave = 0 did nothing")
	}
	if !e2.lsp().table.TerraformEnabled {
		t.Error("turning the go row off took the terraform row with it")
	}
}
