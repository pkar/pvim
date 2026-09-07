package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/undofile"
)

// Every test here runs against a temporary home. Nothing may touch the real
// ~/.cache/vim: these tests create swap files and undo histories, and a stray
// one there is an E325 in somebody's editor tomorrow morning.
func testPersist(t *testing.T, home string) (*persist, *options.Options) {
	t.Helper()
	o := options.Defaults()
	o.G.UndoDir = filepath.Join(home, "state", "undo") + "//"
	o.G.Directory = filepath.Join(home, "state", "swap") + "//"
	o.B.UndoFile = true
	o.B.SwapFile = true
	p := newPersist(home, &o)
	t.Cleanup(func() { p.closeSwap() })
	return p, &o
}

// The vimrc's shape: undodir and directory both ending in "//", both under a
// state directory vim would not have created.
func TestPersistMakesTheDirectories(t *testing.T) {
	home := t.TempDir()
	p, o := testPersist(t, home)

	for _, dir := range []string{
		filepath.Join(home, "state", "undo"),
		filepath.Join(home, "state", "swap"),
		filepath.Join(home, ".cache", "vim"),
	} {
		st, err := os.Stat(dir)
		if err != nil {
			t.Errorf("%s was not created: %v", dir, err)
			continue
		}
		if st.Mode().Perm() != 0o700 {
			t.Errorf("%s is mode %v, want 0700", dir, st.Mode().Perm())
		}
	}
	if want := filepath.Join(home, "state", "undo", "history"); p.histPath != want {
		t.Errorf("history file %q, want %q", p.histPath, want)
	}
	_ = o
}

// The "//" in both options means the whole path is in the name, which is what
// keeps two files called main.go in two repositories apart.
func TestPersistPathsEncodeTheWholePath(t *testing.T) {
	home := t.TempDir()
	p, _ := testPersist(t, home)
	file := filepath.Join(home, "work", "main.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	swap := p.swapPath(file)
	undo := p.undoPath(file)
	if !strings.HasSuffix(swap, ".swp") || !strings.Contains(filepath.Base(swap), "%work%main.go") {
		t.Errorf("swap path %q", swap)
	}
	if !strings.HasSuffix(undo, ".pvundo") || !strings.Contains(filepath.Base(undo), "%work%main.go") {
		t.Errorf("undo path %q", undo)
	}
	if filepath.Dir(swap) != filepath.Join(home, "state", "swap") {
		t.Errorf("swap file is not in 'directory': %q", swap)
	}
	if filepath.Dir(undo) != filepath.Join(home, "state", "undo") {
		t.Errorf("undo file is not in 'undodir': %q", undo)
	}
}

// A vimrc that names nothing usable still gets persistence, in ~/.cache/vim,
// which is where it and where the instance socket already is.
func TestPersistFallsBackToTheCache(t *testing.T) {
	home := t.TempDir()
	o := options.Defaults()
	o.G.UndoDir = "/proc/nowhere/that/exists"
	o.G.Directory = "/proc/nowhere/that/exists//"
	o.B.UndoFile, o.B.SwapFile = true, true
	p := newPersist(home, &o)
	t.Cleanup(func() { p.closeSwap() })

	file := filepath.Join(home, "f.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(home, ".cache", "vim")
	if got := filepath.Dir(p.swapPath(file)); got != cache {
		t.Errorf("swap fell back to %q, want %q", got, cache)
	}
	if got := filepath.Dir(p.undoPath(file)); got != cache {
		t.Errorf("undo fell back to %q, want %q", got, cache)
	}
}

func TestPersistSwapRoundTrip(t *testing.T) {
	home := t.TempDir()
	p, _ := testPersist(t, home)
	file := filepath.Join(home, "f.txt")
	if err := os.WriteFile(file, []byte("alpha\nbravo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf := text.Read([]byte("alpha\nbravo\n"))

	if err := p.openSwap(file, buf, text.Pos{Line: 1}); err != nil {
		t.Fatal(err)
	}
	if p.swapCheck(file) == nil {
		t.Fatal("the swap file was not found by swapCheck")
	}
	buf.SetLine(2, []byte("BRAVO"))
	buf.InsertLines(2, [][]byte{[]byte("charlie")})
	if err := p.syncSwap(buf, text.Pos{Line: 3, Col: 1}); err != nil {
		t.Fatal(err)
	}

	info := p.swapCheck(file)
	if info == nil {
		t.Fatal("no swap file")
	}
	if !info.Modified {
		t.Error("the swap file does not know the buffer is modified")
	}
	act, err := p.chooseSwap(undofile.SwapRecover, *info, file)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(act.Data); got != string(buf.Bytes()) {
		t.Errorf("recovered %q, buffer holds %q", got, buf.Bytes())
	}
	if act.Cursor != (text.Pos{Line: 3, Col: 1}) {
		t.Errorf("recovered cursor %+v", act.Cursor)
	}
	if !strings.Contains(act.Message, "Recovery completed") {
		t.Errorf("recovery said %q", act.Message)
	}

	// A clean exit takes the swap file with it, or the next launch cries wolf.
	if err := p.closeSwap(); err != nil {
		t.Fatal(err)
	}
	if p.swapCheck(file) != nil {
		t.Error("the swap file outlived a clean exit")
	}
}

func TestPersistSwapChoices(t *testing.T) {
	home := t.TempDir()
	p, _ := testPersist(t, home)
	file := filepath.Join(home, "f.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf := text.Read([]byte("alpha\n"))
	if err := p.openSwap(file, buf, text.Pos{Line: 1}); err != nil {
		t.Fatal(err)
	}
	info := *p.swapCheck(file)

	t.Run("read only", func(t *testing.T) {
		act, err := p.chooseSwap(undofile.SwapReadOnly, info, file)
		if err != nil || !act.ReadOnly || act.Swap {
			t.Errorf("%+v %v", act, err)
		}
	})
	t.Run("edit anyway", func(t *testing.T) {
		act, err := p.chooseSwap(undofile.SwapEdit, info, file)
		if err != nil || !act.Swap || act.ReadOnly {
			t.Errorf("%+v %v", act, err)
		}
	})
	t.Run("quit", func(t *testing.T) {
		act, _ := p.chooseSwap(undofile.SwapQuit, info, file)
		if !act.Quit || act.Abort {
			t.Errorf("%+v", act)
		}
	})
	t.Run("abort", func(t *testing.T) {
		act, _ := p.chooseSwap(undofile.SwapAbort, info, file)
		if !act.Abort {
			t.Errorf("%+v", act)
		}
	})
	// Delete last: it removes the file the others were reading.
	t.Run("delete it", func(t *testing.T) {
		act, err := p.chooseSwap(undofile.SwapDelete, info, file)
		if err != nil {
			t.Fatal(err)
		}
		if !act.Swap {
			t.Error("after deleting the old swap file this session gets none")
		}
		if _, err := os.Stat(info.Path); !os.IsNotExist(err) {
			t.Errorf("the swap file is still there: %v", err)
		}
		// And again, on a file that is already gone, which is what two
		// editors answering the same prompt looks like.
		if _, err := p.chooseSwap(undofile.SwapDelete, info, file); err != nil {
			t.Errorf("deleting a swap file that is already gone: %v", err)
		}
	})
}

// The undo history across a restart, and the one guarantee that matters: a file
// changed outside pvim gets its history refused rather than applied.
func TestPersistUndoRoundTrip(t *testing.T) {
	home := t.TempDir()
	p, _ := testPersist(t, home)
	file := filepath.Join(home, "f.txt")
	data := []byte("alpha\nbravo\n")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}

	tr := undofile.Tree{SeqMax: 2, Cur: 2, Nodes: []undofile.Node{
		{Seq: 1, Parent: 0, Last: 2, Cursor: undofile.Pos{Line: 1},
			Steps: []undofile.Step{{First: 1, Replaced: 1, Lines: [][]byte{[]byte("alpha")}}}},
		{Seq: 2, Parent: 1, Last: -1, Cursor: undofile.Pos{Line: 2, Col: 2},
			Steps: []undofile.Step{{First: 2, Replaced: 1, Lines: [][]byte{[]byte("bravo")}}}},
	}}
	if err := p.saveUndo(file, data, tr); err != nil {
		t.Fatal(err)
	}

	got, ok, err := p.loadUndo(file, data)
	if err != nil || !ok {
		t.Fatalf("loadUndo: %v %v", ok, err)
	}
	if got.Cur != 2 || len(got.Nodes) != 2 || got.Nodes[1].Cursor.Col != 2 {
		t.Errorf("tree came back as %+v", got)
	}

	// Changed behind the editor's back: the history is refused and the file
	// holding it is taken away, because it will never match again.
	changed := []byte("alpha\nBRAVO\n")
	if err := os.WriteFile(file, changed, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := p.loadUndo(file, changed); ok || err != nil {
		t.Errorf("a changed file kept its history: %v %v", ok, err)
	}
	if _, err := os.Stat(p.undoPath(file)); !os.IsNotExist(err) {
		t.Errorf("the stale undo file was left behind: %v", err)
	}
}

// A file opened, looked at and closed leaves no history file. Otherwise every
// launch accumulates one and ~/.cache/vim fills up with empty trees.
func TestPersistUndoWritesNothingForNoHistory(t *testing.T) {
	home := t.TempDir()
	p, _ := testPersist(t, home)
	file := filepath.Join(home, "f.txt")
	data := []byte("alpha\n")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.saveUndo(file, data, undofile.Tree{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.undoPath(file)); !os.IsNotExist(err) {
		t.Errorf("an empty tree was written to disk: %v", err)
	}
	// And an existing history is removed when the tree becomes empty, which is
	// what ":e!" on a file leaves behind.
	if err := p.saveUndo(file, data, undofile.Tree{SeqMax: 1, Cur: 1, Nodes: []undofile.Node{
		{Seq: 1, Parent: 0, Last: -1},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.undoPath(file)); err != nil {
		t.Fatalf("nothing was written: %v", err)
	}
	if err := p.saveUndo(file, data, undofile.Tree{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.undoPath(file)); !os.IsNotExist(err) {
		t.Errorf("the old history survived an empty tree: %v", err)
	}
}

// 'noundofile' and 'noswapfile' are off switches and have to be honoured, or an
// editor with the options off is still writing to somebody's disk.
func TestPersistOptionsOff(t *testing.T) {
	home := t.TempDir()
	o := options.Defaults()
	o.G.UndoDir = filepath.Join(home, "undo") + "//"
	o.G.Directory = filepath.Join(home, "swap") + "//"
	o.B.UndoFile, o.B.SwapFile = false, false
	p := newPersist(home, &o)
	t.Cleanup(func() { p.closeSwap() })

	file := filepath.Join(home, "f.txt")
	data := []byte("alpha\n")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.openSwap(file, text.Read(data), text.Pos{Line: 1}); err != nil {
		t.Fatal(err)
	}
	if p.swap != nil {
		t.Error("'noswapfile' still opened a swap file")
	}
	if p.swapCheck(file) != nil {
		t.Error("'noswapfile' still looked for one")
	}
	if err := p.saveUndo(file, data, undofile.Tree{SeqMax: 1, Cur: 1,
		Nodes: []undofile.Node{{Seq: 1, Parent: 0, Last: -1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.undoPath(file)); !os.IsNotExist(err) {
		t.Errorf("'noundofile' still wrote a history: %v", err)
	}
}

func TestPersistHistory(t *testing.T) {
	home := t.TempDir()
	p, _ := testPersist(t, home)

	if h, err := p.loadHistory(); err != nil || len(h.Command) != 0 {
		t.Errorf("the first launch found a history: %+v %v", h, err)
	}
	h := undofile.History{
		Command: []string{"w", "JsonPretty"},
		Search:  []string{`\s\+$`},
		Pattern: `\s\+$`,
		Jumps:   []undofile.Place{{File: "/tmp/a.go", Pos: undofile.Pos{Line: 40}}},
	}
	if err := p.saveHistory(h, options.Defaults().G.History); err != nil {
		t.Fatal(err)
	}
	got, err := p.loadHistory()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Command) != 2 || got.Command[1] != "JsonPretty" || got.Pattern != `\s\+$` {
		t.Errorf("history came back as %+v", got)
	}
	if len(got.Jumps) != 1 || got.Jumps[0].Pos.Line != 40 {
		t.Errorf("jumplist came back as %+v", got.Jumps)
	}

	// A history file somebody has scribbled on is thrown away, not fatal: the
	// alternative is an editor that will not start.
	if err := os.WriteFile(p.histPath, []byte("# vim viminfo file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := p.loadHistory(); err != nil || len(got.Command) != 0 {
		t.Errorf("a corrupt history file gave %+v %v", got, err)
	}
	if _, err := os.Stat(p.histPath); !os.IsNotExist(err) {
		t.Errorf("the corrupt history file was left behind: %v", err)
	}
}

// The marks that outlive a session are A-Z and 0-9. A lowercase mark belongs to
// a buffer and vim drops it at exit; one that came back pointing into a file
// somebody has rewritten since would be worse than none.
func TestPersistMarks(t *testing.T) {
	buf := text.Read([]byte("alpha\nbravo\ncharlie\n"))
	buf.SetMark('A', text.Pos{Line: 2, Col: 3})
	buf.SetMark('a', text.Pos{Line: 1})

	got := marksOf("/tmp/f.txt", buf)
	byName := map[byte]undofile.Place{}
	for _, m := range got {
		byName[m.Name] = m.Place
	}
	if p, ok := byName['A']; !ok || p.Pos.Line != 2 || p.Pos.Col != 3 || p.File != "/tmp/f.txt" {
		t.Errorf("mark A came back as %+v (%v)", p, ok)
	}
	if _, ok := byName['a']; ok {
		t.Error("a lowercase mark was written to the history file")
	}
	if marksOf("", buf) != nil {
		t.Error("a buffer with no name produced marks")
	}
}

// '0 is where the cursor was when the editor last exited, '1 the time before
// that. One entry per file: a file edited every day does not fill all ten.
func TestPersistRecordExit(t *testing.T) {
	var h undofile.History
	h = recordExit(h, "/tmp/a.go", text.Pos{Line: 10})
	h = recordExit(h, "/tmp/b.go", text.Pos{Line: 20})
	h = recordExit(h, "/tmp/a.go", text.Pos{Line: 30})
	if len(h.Files) != 2 {
		t.Fatalf("%d file marks, want 2: the second visit to a.go should replace the first", len(h.Files))
	}
	last := h.Files[len(h.Files)-1]
	if last.File != "/tmp/a.go" || last.Pos.Line != 30 {
		t.Errorf("the newest file mark is %+v", last)
	}
	if last.When.IsZero() {
		t.Error("no time on the file mark")
	}
	if got := recordExit(h, "", text.Pos{Line: 1}); len(got.Files) != 2 {
		t.Error("a buffer with no name got a file mark")
	}
}

// A swap file opened over a file that already has one gets its own name, which
// is what "(E)dit anyway" needs and what stops two editors writing one log.
func TestPersistSecondEditorGetsItsOwnSwap(t *testing.T) {
	home := t.TempDir()
	first, _ := testPersist(t, home)
	second, _ := testPersist(t, home)
	file := filepath.Join(home, "f.txt")
	data := []byte("alpha\n")
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := first.openSwap(file, text.Read(data), text.Pos{Line: 1}); err != nil {
		t.Fatal(err)
	}
	if err := second.openSwap(file, text.Read(data), text.Pos{Line: 1}); err != nil {
		t.Fatal(err)
	}
	if first.swap.Path() == second.swap.Path() {
		t.Fatalf("both editors are writing %q", first.swap.Path())
	}
}

// TestPersistRecoversAfterARealKill kills a real process mid-edit and rebuilds
// the buffer from what it left behind.
//
// A child process of this test binary opens a swap file through persist,
// types into a real *text.Buffer -- the same type the editor edits -- syncs
// after every change, and then waits. The parent SIGKILLs it, finds the swap
// file the way a launch would, answers the E325 prompt with (R)ecover and reads
// the result back into a buffer. Nothing is simulated: a swap file's whole
// reason to exist is what the bytes on the disk say after the process holding
// them stops, and the only honest way to find out is to stop one.
func TestPersistRecoversAfterARealKill(t *testing.T) {
	if os.Getenv(persistKillEnv) != "" {
		t.Skip("this process is the child")
	}
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestPersistKillChild", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), persistKillEnv+"="+home)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	killed := false
	defer func() {
		if !killed {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()

	ready := filepath.Join(home, "ready")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	killed = true
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatal(err)
	}

	// From here on this is what the next launch does.
	p, _ := testPersist(t, home)
	file := filepath.Join(home, "f.txt")
	info := p.swapCheck(file)
	if info == nil {
		t.Fatal("the killed session left no swap file for the next launch to find")
	}
	if info.Running {
		t.Error("the killed process is reported as still running")
	}
	msg := info.Attention(file)
	if !strings.HasPrefix(msg, "E325: ATTENTION") {
		t.Errorf("no E325 for a crashed session:\n%s", msg)
	}
	choice, ok := undofile.ParseSwapChoice('r', info.Running)
	if !ok || choice != undofile.SwapRecover {
		t.Fatalf("r is not (R)ecover: %v %v", choice, ok)
	}
	act, err := p.chooseSwap(choice, *info, file)
	if err != nil {
		t.Fatal(err)
	}

	// The file on the disk was never written. What comes back has to be the
	// buffer as the child had it, in a real *text.Buffer.
	if onDisk, err := os.ReadFile(file); err != nil || len(onDisk) != 0 {
		t.Fatalf("the file on the disk is %q (%v); the child never wrote it", onDisk, err)
	}
	buf := text.Read(act.Data)
	if buf.LineCount() < 10 {
		t.Fatalf("recovered %d lines, the child had synced at least ten", buf.LineCount())
	}
	for i := 1; i <= buf.LineCount(); i++ {
		if want := "line " + strconv.Itoa(i-1); string(buf.Line(i)) != want {
			t.Fatalf("recovered line %d is %q, want %q", i, buf.Line(i), want)
		}
	}
	if act.Cursor.Line != buf.LineCount() {
		t.Errorf("the crashed session's cursor came back on line %d of %d", act.Cursor.Line, buf.LineCount())
	}
	if !strings.Contains(act.Message, "Recovery completed. You should check if everything is OK.") {
		t.Errorf("recovery said %q", act.Message)
	}
	if err := os.Remove(info.Path); err != nil {
		t.Error(err)
	}
}

// persistKillEnv tells the child of TestPersistRecoversAfterARealKill that it
// is one, and where to work.
const persistKillEnv = "PVIM_PERSIST_KILL_HOME"

// TestPersistKillChild is that child: a buffer, a swap file, a sync after every
// change, and then a wait to be killed. It does nothing unless the environment
// says it is the child.
func TestPersistKillChild(t *testing.T) {
	home := os.Getenv(persistKillEnv)
	if home == "" {
		t.Skip("run by TestPersistRecoversAfterARealKill, with " + persistKillEnv + " set")
	}
	file := filepath.Join(home, "f.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := testPersist(t, home)
	buf := text.Read(nil)
	if err := p.openSwap(file, buf, text.Pos{Line: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		// A buffer read from an empty file is one empty line, which is vim's
		// too; the first change writes into it and the rest append.
		if i == 0 {
			buf.SetLine(1, []byte("line 0"))
		} else {
			buf.InsertLines(buf.LineCount()+1, [][]byte{[]byte("line " + strconv.Itoa(i))})
		}
		if err := p.syncSwap(buf, text.Pos{Line: buf.LineCount()}); err != nil {
			t.Fatal(err)
		}
		if i == 9 {
			if err := os.WriteFile(filepath.Join(home, "ready"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Waiting to be killed. The bound is how long this can outlive a parent
	// that died before it could do the killing.
	time.Sleep(30 * time.Second)
}

// A vim swap file cannot be recovered from here and must say so rather than
// hand back an empty buffer. The formats are not compatible and
// they never will be; what pvim owes the person is the E325 and an honest
// refusal, not a file with nothing in it.
func TestPersistWillNotRecoverAVimSwapFile(t *testing.T) {
	home := t.TempDir()
	p, _ := testPersist(t, home)
	file := filepath.Join(home, "f.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := p.swapPath(file)
	if err := os.WriteFile(path, append([]byte("b0VIM 9.2\x00"), make([]byte, 4086)...), 0o600); err != nil {
		t.Fatal(err)
	}
	info := p.swapCheck(file)
	if info == nil {
		t.Fatal("a vim swap file was ignored; somebody may be editing this file")
	}
	if !info.Foreign {
		t.Error("a vim swap file was not marked foreign")
	}
	if msg := info.Attention(file); !strings.Contains(msg, "E325: ATTENTION") {
		t.Errorf("no E325 for a vim swap file:\n%s", msg)
	}
	if _, err := p.chooseSwap(undofile.SwapRecover, *info, file); err == nil {
		t.Error("recovering a vim swap file was reported as working")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the failed recovery removed somebody else's swap file: %v", err)
	}
}
