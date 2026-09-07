package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/undofile"
)

// Crash recovery, from the kill to the buffer.
//
// Every test here that talks about a crash makes one: a child process of this
// test binary opens the file through the editor's own initPersist, types into
// it, and is SIGKILLed. Nothing is simulated. A swap file's whole reason to
// exist is what the bytes say after the process holding them stops, and the
// only honest way to find out is to stop one.
//
// The child self-exits on a timer and the parent kills it in a defer, so a
// parent that fails early cannot leave one behind.
//
// # What was measured against vim
//
// vim 9.2.0321 at /opt/homebrew/bin/vim, by killing a real vim
// with SIGKILL and reopening the file under `script` and under `expect`:
//
//	the message E325 and the block under it, which
//	 internal/undofile/attention_test.go holds byte
//	 for byte; TestOfferMatchesVim below is the same
//	 text arriving through the editor.
//	the six choices [O]pen Read-Only, (E)dit anyway, (R)ecover,
//	 (D)elete it, (Q)uit, (A)bort:
//	the five a second vim opened while the first was alive
//	 printed "process ID: 13367 (STILL RUNNING)" and
//	 dropped (D)elete it.
//	(R)ecover the buffer came back as the crashed session had
//	 it and `:echo &modified` answered 1. The swap
//	 file was still on the disk afterwards.
//	(R)ecover, unchanged when what came out of the swap file equalled the
//	 file on the disk, vim said "Buffer contents
//	 equals file contents." and `&modified` answered
//	 0.
//	(Q)uit and (A)bort both exited with status 1 and left the swap file
//	 alone.

// recoverChildEnv tells a child of these tests that it is one, and where to
// work. recoverKeysEnv is what it types before it waits to be killed.
const (
	recoverChildEnv = "PVIM_RECOVER_CHILD_HOME"
	recoverKeysEnv  = "PVIM_RECOVER_CHILD_KEYS"
)

// crashFile is the name every test here edits, inside the child's home so that
// one temporary directory holds the file, the swap directory and the undo
// directory together.
const crashFile = "notes.txt"

// TestRecoverEditorChild is the crashing session: a real editor with real
// persistence, one keystroke at a time, then a wait to be killed.
//
// It does nothing at all unless the environment says it is the child, so a
// plain "go test ./..." runs it as an immediate skip.
func TestRecoverEditorChild(t *testing.T) {
	home := os.Getenv(recoverChildEnv)
	if home == "" {
		t.Skip("run by the recovery tests, with " + recoverChildEnv + " set")
	}
	file := filepath.Join(home, crashFile)
	e := wired(t, home, file)
	typeRunes(t, e, os.Getenv(recoverKeysEnv))
	if err := os.WriteFile(filepath.Join(home, "ready"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Waiting to be killed. The bound is how long this can outlive a parent
	// that died before it could do the killing, and -test.timeout below is the
	// second bound under it.
	time.Sleep(20 * time.Second)
}

// crashed runs a child over a fresh file, kills it, and hands back the home and
// the file the next launch is about to open.
//
// keys is what the child types. Empty is a session that changed nothing, which
// is the case vim treats differently on recovery.
func crashed(t *testing.T, keys string) (home, file string) {
	t.Helper()
	if os.Getenv(recoverChildEnv) != "" {
		t.Skip("this process is a child")
	}
	home = t.TempDir()
	file = filepath.Join(home, crashFile)
	if err := os.WriteFile(file, []byte("alpha\nbravo\ncharlie\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRecoverEditorChild", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), recoverChildEnv+"="+home, recoverKeysEnv+"="+keys)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitReady(t, home, cmd)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatal(err)
	}
	return home, file
}

// alive is crashed's other half: a child that is still running when the test
// looks at its swap file, which is what takes "(D)elete it" off the prompt.
// The caller gets the kill in a t.Cleanup, so no path out of the test leaves a
// process behind.
func alive(t *testing.T, keys string) (home, file string) {
	t.Helper()
	if os.Getenv(recoverChildEnv) != "" {
		t.Skip("this process is a child")
	}
	home = t.TempDir()
	file = filepath.Join(home, crashFile)
	if err := os.WriteFile(file, []byte("alpha\nbravo\ncharlie\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRecoverEditorChild", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), recoverChildEnv+"="+home, recoverKeysEnv+"="+keys)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	waitReady(t, home, cmd)
	return home, file
}

// waitReady blocks until the child says it has opened the file and typed.
func waitReady(t *testing.T, home string, cmd *exec.Cmd) {
	t.Helper()
	ready := filepath.Join(home, "ready")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			return
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			cmd.Wait()
			t.Fatal("the child never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// asked is what the prompt was shown and what it answered.
type asked struct {
	times   int
	info    undofile.SwapInfo
	opening string
	message string
	// shown is what went to the terminal after the answer: vim's recovery
	// block, or the one line (D)elete it prints.
	shown string
}

// answers replaces the E325 prompt for one test.
//
// There is no terminal under "go test" and there must not be: a test that read
// os.Stdin would either hang the suite or depend on who ran it. askSwap is a
// variable for exactly this.
func answers(t *testing.T, c undofile.SwapChoice, ok bool) *asked {
	t.Helper()
	a := &asked{}
	old := askSwap
	askSwap = func(info undofile.SwapInfo, opening string) (undofile.SwapChoice, bool) {
		a.times++
		a.info, a.opening = info, opening
		a.message = info.Attention(opening)
		return c, ok
	}
	oldShow := showSwap
	showSwap = func(msg string) { a.shown += msg }
	t.Cleanup(func() {
		askSwap = old
		showSwap = oldShow
	})
	return a
}

// catchExit stops (Q)uit and (A)bort from taking the test binary with them.
func catchExit(t *testing.T) *[]int {
	t.Helper()
	var codes []int
	old := exitPvim
	exitPvim = func(code int) { codes = append(codes, code) }
	t.Cleanup(func() { exitPvim = old })
	return &codes
}

// TestOfferMatchesVim. The message a crashed session produces, arriving through
// the editor rather than through internal/undofile, still has to be vim's: the
// E-code on the first line, the swap file's name in quotes, "modified: YES"
// because the child typed, no "(STILL RUNNING)" because it was killed, and the
// six choices on the last line.
func TestOfferMatchesVim(t *testing.T) {
	home, file := crashed(t, "xxx")
	a := answers(t, undofile.SwapQuit, true)
	catchExit(t)
	wired(t, home, file)

	if a.times != 1 {
		t.Fatalf("the prompt went up %d times, want once", a.times)
	}
	if a.opening != file {
		t.Errorf("the message names %q, want the file being opened, %q", a.opening, file)
	}
	if !strings.HasPrefix(a.message, "E325: ATTENTION\nFound a swap file by the name \"") {
		t.Errorf("the message does not open the way vim's does:\n%s", a.message)
	}
	if !strings.Contains(a.message, "\n          modified: YES\n") {
		t.Errorf("a session that typed and crashed is not reported as modified:\n%s", a.message)
	}
	if strings.Contains(a.message, "STILL RUNNING") {
		t.Errorf("a killed process is reported as still running:\n%s", a.message)
	}
	// The last line is what a person actually answers, and it is vim's, from
	// the transcript in internal/undofile/attention.go.
	const want = "[O]pen Read-Only, (E)dit anyway, (R)ecover, (D)elete it, (Q)uit, (A)bort: "
	if got := lastLine(a.message); got != want {
		t.Errorf("the prompt is\n %q\nwant\n %q", got, want)
	}
}

// TestALiveEditorDropsDeleteFromTheOffer. Vim leaves "(D)elete it" off when the
// process that wrote the swap file is still there, because deleting it would
// take that editor's recovery away. Measured with two real vims on one file.
func TestALiveEditorDropsDeleteFromTheOffer(t *testing.T) {
	home, file := alive(t, "xxx")
	a := answers(t, undofile.SwapQuit, true)
	catchExit(t)
	wired(t, home, file)

	if a.times != 1 {
		t.Fatalf("the prompt went up %d times, want once", a.times)
	}
	if !a.info.Running {
		t.Fatal("a live editor's swap file is not reported as still running")
	}
	if !strings.Contains(a.message, " (STILL RUNNING)") {
		t.Errorf("no (STILL RUNNING) beside the process ID:\n%s", a.message)
	}
	const want = "[O]pen Read-Only, (E)dit anyway, (R)ecover, (Q)uit, (A)bort: "
	if got := lastLine(a.message); got != want {
		t.Errorf("the prompt is\n %q\nwant\n %q", got, want)
	}
}

// TestRecoverFillsTheBufferAndLeavesItModified is the feature: a kill -9 and
// the work is back.
//
// Modified is half the test. The file on the disk is still what the crash left
// and recovery writes nothing; whether the recovered buffer goes over it is a
// decision for the person who can read both. Measured: vim answers
// `:echo &modified` with 1 after a recovery.
func TestRecoverFillsTheBufferAndLeavesItModified(t *testing.T) {
	home, file := crashed(t, "xxx")
	a := answers(t, undofile.SwapRecover, true)
	e := wired(t, home, file)

	// "xxx" on "alpha" leaves "ha", and none of it was ever written.
	if got, want := string(e.buf.Bytes()), "ha\nbravo\ncharlie\n"; got != want {
		t.Errorf("the recovered buffer is %q, want %q", got, want)
	}
	onDisk, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(onDisk), "alpha\nbravo\ncharlie\n"; got != want {
		t.Errorf("recovery wrote the file: it is %q, want %q", got, want)
	}
	if !e.modified() {
		t.Error("the recovered buffer is not modified, so :q would throw the recovery away without asking")
	}
	// The swap file is still there. Vim leaves it, which is why its message
	// ends by saying so, and why the next launch raises E325 again.
	if _, err := os.Stat(a.info.Path); err != nil {
		t.Errorf("the recovery deleted the swap file it recovered from: %v", err)
	}
	if got := e.message(); got != "You may want to delete the .swp file now." {
		t.Errorf("the message line says %q", got)
	}
	// And the block above the editor is vim's, word for word, from the capture
	// in the file comment.
	want := "Using swap file \"" + a.info.Path + "\"\n" +
		"Original file \"" + file + "\"\n" +
		"Recovery completed. You should check if everything is OK.\n" +
		"(You might want to write out this file under another name\n" +
		"and run diff with the original file to check for changes)\n" +
		"You may want to delete the .swp file now.\n"
	if a.shown != want {
		t.Errorf("the recovery block is not vim's.\n--- got ---\n%s\n--- want ---\n%s", a.shown, want)
	}
	// This session gets a swap file of its own, under another name, because
	// the crashed one still holds the first.
	if e.pers.swap == nil {
		t.Fatal("the recovered session has no swap file; a second crash would lose it all again")
	}
	if e.pers.swap.Path() == a.info.Path {
		t.Errorf("the recovered session opened the crashed session's swap file, %q", a.info.Path)
	}
}

// TestRecoverOfAnUnchangedSessionIsNotModified is vim's exception, measured:
// when what comes out of the swap file is byte for byte what is on the disk,
// vim says "Buffer contents equals file contents." and `&modified` answers 0.
// Nothing changed, so nothing is offered to write.
func TestRecoverOfAnUnchangedSessionIsNotModified(t *testing.T) {
	home, file := crashed(t, "")
	answers(t, undofile.SwapRecover, true)
	e := wired(t, home, file)

	if got, want := string(e.buf.Bytes()), "alpha\nbravo\ncharlie\n"; got != want {
		t.Errorf("the recovered buffer is %q, want %q", got, want)
	}
	if e.modified() {
		t.Error("a recovery that changed nothing left the buffer modified")
	}
}

// TestOpenReadOnlyTakesNoSwapFile. [O]pen Read-Only is the default and the safe
// one: the file opens, 'readonly' is on, and the crashed session's swap file is
// left alone with no swap file of this launch's own beside it.
func TestOpenReadOnlyTakesNoSwapFile(t *testing.T) {
	home, file := crashed(t, "xxx")
	a := answers(t, undofile.SwapReadOnly, true)
	e := wired(t, home, file)

	if got, want := string(e.buf.Bytes()), "alpha\nbravo\ncharlie\n"; got != want {
		t.Errorf("a read-only open changed the buffer: %q", got)
	}
	if !e.opt.B.ReadOnly {
		t.Error("'readonly' is off after [O]pen Read-Only")
	}
	if b := e.cur(); b == nil || !b.ReadOnly {
		t.Error("the buffer's own readonly flag is off after [O]pen Read-Only")
	}
	if e.pers.swap != nil {
		t.Errorf("a read-only open took a swap file at %q", e.pers.swap.Path())
	}
	if _, err := os.Stat(a.info.Path); err != nil {
		t.Errorf("a read-only open removed the crashed session's swap file: %v", err)
	}
}

// TestNoTerminalOpensReadOnly. A launch with nowhere to ask -- from the Dock,
// from a pipeline, from a cron job -- takes the choice vim marks as the default
// with square brackets, because it is the one that cannot lose anything.
func TestNoTerminalOpensReadOnly(t *testing.T) {
	home, file := crashed(t, "xxx")
	a := answers(t, undofile.SwapAbort, false) // the answer is ignored: ok is false
	e := wired(t, home, file)

	if !e.opt.B.ReadOnly {
		t.Error("a launch with nobody to ask did not fall back to read-only")
	}
	if e.pers.swap != nil {
		t.Error("a launch with nobody to ask took a swap file anyway")
	}
	if _, err := os.Stat(a.info.Path); err != nil {
		t.Errorf("a launch with nobody to ask removed the swap file: %v", err)
	}
}

// TestEditAnywayGetsItsOwnSwapFile. (E)dit anyway opens the file for real, so
// it needs a swap file, and it cannot have the one that is already there.
func TestEditAnywayGetsItsOwnSwapFile(t *testing.T) {
	home, file := crashed(t, "xxx")
	a := answers(t, undofile.SwapEdit, true)
	e := wired(t, home, file)

	if got, want := string(e.buf.Bytes()), "alpha\nbravo\ncharlie\n"; got != want {
		t.Errorf("(E)dit anyway did not open the file on the disk: %q", got)
	}
	if e.modified() {
		t.Error("(E)dit anyway left the buffer modified")
	}
	if e.pers.swap == nil {
		t.Fatal("(E)dit anyway opened the file with no swap file")
	}
	if e.pers.swap.Path() == a.info.Path {
		t.Error("(E)dit anyway wrote into the crashed session's swap file")
	}
	if _, err := os.Stat(a.info.Path); err != nil {
		t.Errorf("(E)dit anyway removed the crashed session's swap file: %v", err)
	}
}

// TestDeleteItRemovesTheSwapFile, which is the answer for a swap file whose
// crash you have already recovered from, and the reason a clean exit deletes
// its own: people who see E325 after every launch learn to answer this one
// without reading it.
func TestDeleteItRemovesTheSwapFile(t *testing.T) {
	home, file := crashed(t, "xxx")
	a := answers(t, undofile.SwapDelete, true)
	e := wired(t, home, file)

	if got, want := string(e.buf.Bytes()), "alpha\nbravo\ncharlie\n"; got != want {
		t.Errorf("(D)elete it did not open the file on the disk: %q", got)
	}
	if e.pers.swap == nil {
		t.Fatal("(D)elete it opened the file with no swap file")
	}
	// This session is on the name the crashed one held, which is the proof the
	// delete happened: CreateSwap is O_EXCL, so it could not have taken that
	// name with the old file still there.
	if e.pers.swap.Path() != a.info.Path {
		t.Errorf("the new swap file is %q, want the name (D)elete it just freed, %q",
			e.pers.swap.Path(), a.info.Path)
	}
	if info := e.pers.swapCheck(file); info == nil || info.PID != os.Getpid() {
		t.Errorf("the swap file at that name is not this session's: %+v", info)
	}
	if got := e.message(); got != "Swap file deleted" {
		t.Errorf("the message line says %q", got)
	}
}

// TestQuitAndAbortStopTheLaunch. Measured: vim exits with status 1 for both and
// leaves the swap file where it is.
func TestQuitAndAbortStopTheLaunch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		choice undofile.SwapChoice
	}{
		{"quit", undofile.SwapQuit},
		{"abort", undofile.SwapAbort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, file := crashed(t, "xxx")
			a := answers(t, tc.choice, true)
			codes := catchExit(t)
			e := wired(t, home, file)

			if len(*codes) != 1 || (*codes)[0] != 1 {
				t.Errorf("pvim exited with %v, want one exit of 1", *codes)
			}
			if e.pers.swap != nil {
				t.Error("a launch that was told to stop opened a swap file anyway")
			}
			if _, err := os.Stat(a.info.Path); err != nil {
				t.Errorf("stopping the launch removed the swap file: %v", err)
			}
		})
	}
}

// TestNoSwapFileNeverAsks. The prompt is the loudest thing pvim does and it has
// to stay rare: an ordinary launch over an ordinary file must not see it.
func TestNoSwapFileNeverAsks(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, crashFile)
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := answers(t, undofile.SwapAbort, true)
	catchExit(t)
	e := wired(t, home, file)

	if a.times != 0 {
		t.Errorf("the E325 prompt went up %d times over a file with no swap file", a.times)
	}
	if e.pers.swap == nil {
		t.Error("an ordinary launch opened no swap file")
	}
}

// TestAVimSwapFileIsOfferedAndRefusedHonestly. pvim cannot read vim's format
// and it never will. What it owes the person is the E325 -- the
// point of which is that somebody else may be editing this file -- and, if they
// ask for a recovery, a refusal rather than an empty buffer.
func TestAVimSwapFileIsOfferedAndRefusedHonestly(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, crashFile)
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A persist built the way wired builds one, to find where the swap file
	// for this file would go.
	e0, err := newEditor([]byte("alpha\n"), file, 24, 80, "")
	if err != nil {
		t.Fatal(err)
	}
	e0.opt.G.Directory = filepath.Join(home, "swap") + "//"
	p := newPersist(home, e0.opt)
	path := p.swapPath(file)
	if err := os.WriteFile(path, append([]byte("b0VIM 9.2\x00"), make([]byte, 4086)...), 0o600); err != nil {
		t.Fatal(err)
	}

	a := answers(t, undofile.SwapRecover, true)
	e := wired(t, home, file)

	if a.times != 1 {
		t.Fatalf("a vim swap file was not offered; somebody may be editing this file")
	}
	if !a.info.Foreign {
		t.Error("a vim swap file was not marked foreign")
	}
	if got, want := string(e.buf.Bytes()), "alpha\n"; got != want {
		t.Errorf("a failed recovery left the buffer %q, want the file on the disk, %q", got, want)
	}
	if !strings.HasPrefix(e.message(), "E306: Cannot open ") {
		t.Errorf("a failed recovery said %q", e.message())
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a failed recovery removed somebody else's swap file: %v", err)
	}
}

// TestPromptWithoutATerminalDoesNotHang. The real prompt reads a keystroke off
// the tty in raw mode, and there is no tty under "go test": a pipe stands in
// for one, IoctlGetTermios refuses it, and the answer is "nobody to ask"
// straight away rather than a blocking read on a pipe nothing will ever write.
//
// This is the one test that runs the real consoleSwap path, which is why it
// exists: askSwap being a variable everywhere else would otherwise mean the
// shipped path had no test at all.
func TestPromptWithoutATerminalDoesNotHang(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	// The message goes to standard error when there is nobody to ask, which is
	// right and is not something the suite's output needs a copy of.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	stderr := os.Stderr
	os.Stderr = null
	defer func() { os.Stderr = stderr }()

	done := make(chan bool, 1)
	go func() {
		_, ok := promptSwapFile(r, undofile.SwapInfo{Path: "/tmp/x.swp"}, "x.txt")
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Error("a pipe answered the E325 prompt")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the prompt blocked on something that is not a terminal")
	}
}
