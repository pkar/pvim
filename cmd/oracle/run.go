package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// runnerKind says which command line a binary wants. The two shapes are not
// interchangeable and neither is guessable from the binary's name.
type runnerKind int

const (
	// kindVim is the reference: real vim, driven headless.
	kindVim runnerKind = iota
	// kindPvim is the candidate: pvim's own headless mode.
	kindPvim
)

// runner is one side of a diff.
type runner struct {
	name string // "vim" or "pvim", used in reports
	bin  string
	kind runnerKind
}

// argv builds the command line for one run.
//
// The reference gets --clean and deliberately not -u NONE. -u NONE leaves
// 'compatible' set, and compatible changes cw, changes what backspace may
// delete, and changes <. Every one of those is a diff the candidate would be
// blamed for and none of them is a difference between the two editors, so the
// whole run would be noise. --clean starts vim with no vimrc, no plugins and
// nocompatible, which is the state the .opts line then builds on.
//
// -i NONE keeps viminfo out of it, so a run cannot inherit a register or a mark
// from the run before it. --not-a-term suppresses the "input is not from a
// terminal" warning; the terminal itself comes from openPTY, because vim needs
// a real one on stdin whatever this flag says.
func (r runner) argv(keysPath, filePath string) []string {
	switch r.kind {
	case kindVim:
		return []string{"--clean", "-i", "NONE", "--not-a-term", "-s", keysPath, filePath}
	default:
		return []string{"--oracle", "-s", keysPath, filePath}
	}
}

// timeoutError is a side that never finished within the per-run timeout.
//
// It is a type and not a formatted string because the fuzz half has to tell two
// things apart that both arrive as an error from run: a generated script that
// left vim sitting at a prompt with no keystrokes left to answer it, which is a
// hole in the grammar to count, and a harness that cannot run vim at all, which
// is a run to stop.
type timeoutError struct {
	name    string
	timeout time.Duration
}

// Error names the side and the limit it went past.
func (e timeoutError) Error() string {
	return fmt.Sprintf("%s did not finish in %s", e.name, e.timeout)
}

// artifacts is everything one side left behind.
type artifacts struct {
	buffer   []byte
	state    []byte
	messages []byte
	// missing names the artifacts that were not written at all. This is kept
	// apart from the contents because a candidate that writes nothing must
	// never render as a small diff.
	missing  []string
	exitCode int
	// terminal is the tail of what the process painted, kept only so that a
	// harness failure can be explained. It is never diffed: it is full of
	// cursor addressing and says nothing about the edit.
	terminal []byte
}

// has reports whether every artifact was produced.
func (a artifacts) has() bool { return len(a.missing) == 0 }

// terminalTail is how much of a side's screen output is kept for error
// reporting. vim paints about 200KB over a short script and all but the end of
// it is redraw.
const terminalTail = 16 << 10

// drainGrace is how long run waits for the pty to reach end of file after the
// child has been reaped, before giving up on the terminal tail.
const drainGrace = 2 * time.Second

// run executes one side in its own directory and collects what it left.
//
// dir is created fresh. The buffer copy, the keys file and the two artifacts
// the trailer writes all live in it, and so does the HOME the process sees, so
// that a profile with undodir=~/.cache/vim in it cannot touch the real one.
func run(ctx context.Context, r runner, dir string, in, keys []byte, timeout time.Duration) (artifacts, error) {
	var a artifacts

	// Fresh, always. A directory left over from an earlier run still holds
	// that run's state.txt and msgs.txt, and a side that crashes before writing
	// its own would be judged on somebody else's.
	if err := os.RemoveAll(dir); err != nil {
		return a, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
		return a, err
	}
	bufPath := filepath.Join(dir, bufferName)
	if err := os.WriteFile(bufPath, in, 0o644); err != nil {
		return a, err
	}
	keysPath := filepath.Join(dir, "run.keys")
	if err := os.WriteFile(keysPath, keys, 0o644); err != nil {
		return a, err
	}

	master, slave, err := openPTY()
	if err != nil {
		return a, err
	}
	defer master.Close()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.bin, r.argv("run.keys", bufferName)...)
	cmd.Dir = dir
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	// A fixed, minimal environment. Anything the editor can read that the
	// harness did not set is a way for one machine's run to differ from
	// another's, and $HOME points into the scratch directory so that a case
	// cannot write into the real one.
	cmd.Env = []string{
		"HOME=" + filepath.Join(dir, "home"),
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
		"TERM=xterm",
		"LC_ALL=en_US.UTF-8",
		"LANG=en_US.UTF-8",
	}

	// The child's output has to be drained or the pty buffer fills and the
	// editor blocks painting a screen nobody is looking at.
	drained := make(chan []byte, 1)
	go func() {
		tail := make([]byte, 0, terminalTail)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				tail = append(tail, buf[:n]...)
				if len(tail) > terminalTail {
					tail = append(tail[:0], tail[len(tail)-terminalTail:]...)
				}
			}
			if err != nil {
				drained <- tail
				return
			}
		}
	}()

	runErr := cmd.Start()
	slave.Close() // the child owns it now; our copy would keep the drain open forever
	if runErr == nil {
		runErr = cmd.Wait()
	}
	// The drain ends when the last slave descriptor closes, which is normally
	// the moment the child exits. It is not guaranteed to be: a child that was
	// killed on the deadline can leave a grandchild holding the pty, and then
	// this read never returns and the timeout that was supposed to bound the
	// run bounds nothing. The tail is only ever used to explain a failure, so
	// waiting a little for it and going on without it is right, and the
	// deferred master.Close unblocks the reader on the way out.
	select {
	case a.terminal = <-drained:
	case <-time.After(drainGrace):
	}

	switch {
	case runErr == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return a, timeoutError{name: r.name, timeout: timeout}
	default:
		var ee *exec.ExitError
		if !errors.As(runErr, &ee) {
			return a, fmt.Errorf("running %s: %w", r.name, runErr)
		}
		a.exitCode = ee.ExitCode()
	}

	for _, f := range []struct {
		name string
		dst  *[]byte
	}{
		{bufferName, &a.buffer},
		{stateName, &a.state},
		{messagesName, &a.messages},
	} {
		b, err := os.ReadFile(filepath.Join(dir, f.name))
		switch {
		case err == nil:
			*f.dst = b
		case os.IsNotExist(err):
			a.missing = append(a.missing, f.name)
		default:
			return a, err
		}
	}
	return a, nil
}
