package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/clip"
	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/tui"
)

// The terminal frontend as an instance: it listens on the socket, it types a
// -s script into itself, and the seam both of those go through is
// tui.Terminal.Post.
//
// There is no tty under "go test", so the terminal here is a real
// *tui.Terminal over a pipe with the driver faked -- the one seam internal/tui
// exists to have -- and everything above it is the real thing: the real event
// loop, the real editor, a real unix socket, and for the headline case a real
// second process running the real binary.

// termDriver is the operating system under a Terminal, faked. Raw mode is a
// no-op, the size is fixed, and SIGWINCH never arrives.
type termDriver struct{ rows, cols int }

func (d termDriver) Raw() (func() error, error)         { return func() error { return nil }, nil }
func (d termDriver) Size() (int, int, error)            { return d.rows, d.cols, nil }
func (d termDriver) Resized() (<-chan struct{}, func()) { return make(chan struct{}), func() {} }

// syncBuf is the terminal's output, readable from the test goroutine while the
// event loop is writing to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// testTerminal returns a Terminal on a pipe, the writer that plays the part of
// somebody typing, and what the editor painted.
func testTerminal(t *testing.T) (*tui.Terminal, *io.PipeWriter, *syncBuf) {
	t.Helper()
	r, w := io.Pipe()
	out := &syncBuf{}
	t.Cleanup(func() { w.Close() })
	return &tui.Terminal{In: r, Out: out, Drv: termDriver{rows: 24, cols: 80}}, w, out
}

// liveTerminal is a terminal frontend running on a goroutine, and the one way
// to stop it again.
type liveTerminal struct {
	out  *syncBuf
	w    *io.PipeWriter
	done chan error
	once sync.Once
	err  error
}

// startTerminal drives ed in a terminal of its own and registers the stop, so
// that a test which fails half way through still leaves no goroutine, no
// listener and no socket behind.
func startTerminal(t *testing.T, ed *editor, c config) *liveTerminal {
	t.Helper()
	term, w, out := testTerminal(t)
	lt := &liveTerminal{out: out, w: w, done: make(chan error, 1)}
	go func() { lt.done <- ed.driveTerminal(term, c) }()
	t.Cleanup(func() { lt.stop(t) })
	return lt
}

// stop hangs the terminal up and waits for the frontend to put everything
// back, which is what a shell closing under the editor does.
//
// Idempotent and answering the same error every time, because the test calls
// it to assert on that error and the cleanup calls it again to be sure: a
// second caller taking the answer off the channel the first one is waiting on
// is a test that hangs where the editor did not.
func (lt *liveTerminal) stop(t *testing.T) error {
	t.Helper()
	lt.once.Do(func() {
		lt.w.Close()
		select {
		case lt.err = <-lt.done:
		case <-time.After(30 * time.Second):
			lt.err = errors.New("the terminal frontend never stopped")
		}
	})
	return lt.err
}

// TestASecondPvimOpensATabInTheRunningTerminal is the headline, and it is the
// one this frontend could not do at all before: "pvim file" typed in a second
// tmux pane opens a tab in the terminal instance running in the first, rather
// than starting an editor of its own on top of it.
//
// Nothing is faked but the tty. The second process is the real binary, built
// here, finding the socket the way it finds it in a shell: through $HOME.
func TestASecondPvimOpensATabInTheRunningTerminal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary and runs it")
	}
	// Built before $HOME moves, and that order is the whole of it: go build
	// puts its module cache under $HOME, so a build run with the temporary
	// home downloads every dependency again into a directory this test then
	// deletes. Measured the wrong way round at 100 MB of x/image and purego
	// per run.
	bin := filepath.Join(shortDir(t), "pvim")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("building the client: %v\n%s", err, out)
	}

	// The socket is found through $HOME, by this process and by the one below,
	// so this is what makes the two meet on a socket that is not the one
	// belonging to whoever is running the suite.
	home := shortDir(t)
	t.Setenv("HOME", home)

	dir := shortDir(t)
	file := filepath.Join(dir, "handed-over.txt")
	if err := os.WriteFile(file, []byte("from the other pane\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	live := startTerminal(t, newTestEditor(t, "alpha\n"), config{})

	sock := socketPath()
	if sock == "" {
		t.Fatal("no socket path with $HOME set")
	}
	waitFor(t, "the terminal instance to bind the socket", func() bool {
		_, err := os.Stat(sock)
		return err == nil
	})

	// The second shell. It should hand the file over and exit, saying nothing.
	client := exec.Command(bin, file)
	if said, err := client.CombinedOutput(); err != nil {
		t.Fatalf("the second pvim exited %v:\n%s", err, said)
	} else if len(said) > 0 {
		t.Errorf("the second pvim said %q", said)
	}

	// What proves it is the screen: the running instance repainted with the
	// handed-over file on it. Asserting the tab count would need the editor's
	// own goroutine; the frame is what a person in the other pane sees.
	waitFor(t, "the handed-over file to appear on the screen", func() bool {
		return strings.Contains(live.out.String(), "from the other pane")
	})

	if err := live.stop(t); err != nil {
		t.Fatalf("the editor stopped with %v", err)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Error("the socket outlived the editor")
	}
}

// TestTerminalScriptTypesItselfIn is -s in this frontend: the keys go in
// through the same event channel the keyboard uses, so what they do is what a
// person doing it would do -- including writing the file and quitting.
func TestTerminalScriptTypesItselfIn(t *testing.T) {
	home := shortDir(t)
	t.Setenv("HOME", home)

	dir := shortDir(t)
	file := filepath.Join(dir, "typed.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte("IX\x1bZZ"), 0o644); err != nil {
		t.Fatal(err)
	}

	ed, err := newEditor([]byte("alpha\n"), file, 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	term, _, _ := testTerminal(t)

	// --new, because a script that quits should not need a socket and because
	// two of these tests running in one package must not fight over one.
	if err := ed.driveTerminal(term, config{keys: keys, newInstance: true}); err != nil {
		t.Fatalf("driveTerminal: %v", err)
	}

	got, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Xalpha\n"; string(got) != want {
		t.Errorf("the script left %q in the file, want %q", got, want)
	}
}

// TestTerminalScriptFileMissingIsFatalBeforeRawMode. A -s that cannot be read
// has to fail while there is still a shell to print to; discovering it after
// the alternate screen is up puts the message where nobody will see it.
func TestTerminalScriptFileMissingIsFatalBeforeRawMode(t *testing.T) {
	home := shortDir(t)
	t.Setenv("HOME", home)

	ed := newTestEditor(t, "alpha\n")
	term, _, out := testTerminal(t)
	err := ed.driveTerminal(term, config{keys: filepath.Join(shortDir(t), "nope"), newInstance: true})
	if err == nil {
		t.Fatal("a missing -s file started the editor anyway")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("driveTerminal = %v, want it to name the missing file", err)
	}
	if out.String() != "" {
		t.Errorf("the terminal was touched before the script was read: %q", out.String())
	}
}

// TestTerminalPumpAnswersWhenThereIsNoLoop. A socket request that arrives
// before the loop is up or after it has gone is answered with an error rather
// than parked, because the thing waiting on it is somebody's shell.
func TestTerminalPumpAnswersWhenThereIsNoLoop(t *testing.T) {
	term, _, _ := testTerminal(t)
	p := &terminalPump{ed: newTestEditor(t, "alpha\n"), term: term}

	ran := false
	if err := p.do(func(client) error { ran = true; return nil }); err == nil {
		t.Error("do with no loop running returned no error")
	}
	if ran {
		t.Error("do ran the work with no editor goroutine to run it on")
	}

	p.stop()
	if err := p.do(func(client) error { return nil }); !errors.Is(err, errNoEditorLeft) {
		t.Errorf("do after stop = %v, want errNoEditorLeft", err)
	}
}

// TestClipThreadRunsAndStops. The clipboard's Runner has to run the work and
// come back, and it has to answer rather than block once the thread is gone --
// a yank during shutdown is a message on the message line, not a hang.
func TestClipThreadRunsAndStops(t *testing.T) {
	th := newClipThread()
	ran := 0
	for i := 0; i < 3; i++ {
		if err := th.run(func() { ran++ }); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if ran != 3 {
		t.Errorf("the thread ran %d closures, want 3", ran)
	}
	th.stop()
	th.stop() // twice, because more than one path reaches it
	if err := th.run(func() { ran++ }); !errors.Is(err, errClipboardGone) {
		t.Errorf("run after stop = %v, want errClipboardGone", err)
	}
	if ran != 3 {
		t.Errorf("the thread ran a closure after it was stopped")
	}
}

// TestTerminalYankReachesTheDesktopPasteboard is the pasteboard gate
// for this frontend: "+yy in the terminal puts the line on the same board
// MacVim and every other application read, with the linewise type still on it.
//
// Behind the same environment variable internal/clip's own write tests are
// behind, and for the same reason: a test that writes the general pasteboard
// destroys whatever the person running "make check" had copied, and only the
// string could be put back. Reproduce it with
//
//	PVIM_CLIP_PASTEBOARD=1 go test -run TestTerminalYankReaches ./cmd/pvim/
//
// The read is through a second clip.Clipboard rather than the editor's own,
// because the editor's caches its last write against the pasteboard's
// changeCount and would answer out of that cache without the board being
// touched at all -- which is exactly the shape of clipboard bug that takes a
// week to notice.
func TestTerminalYankReachesTheDesktopPasteboard(t *testing.T) {
	if os.Getenv("PVIM_CLIP_PASTEBOARD") == "" {
		t.Skip("set PVIM_CLIP_PASTEBOARD=1 to let this test replace the desktop clipboard")
	}
	ed := newTestEditor(t, "alpha\nbeta\n")
	defer ed.installTerminalClipboard()()

	if err := ed.sess.Run(keysOf(t, "\"+yy")); err != nil {
		t.Fatalf("\"+yy: %v", err)
	}

	th := newClipThread()
	defer th.stop()
	cb, err := clip.New(th.run)
	if err != nil {
		t.Fatalf("reading the board back: %v", err)
	}
	v, err := cb.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if v.Type != register.TypeLine {
		t.Errorf("the board says the yank is type %v, want linewise", v.Type)
	}
	if len(v.Lines) != 1 || string(v.Lines[0]) != "alpha" {
		t.Errorf("the board holds %q, want one line of alpha", v.Lines)
	}
}
