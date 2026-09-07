package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/tui"
	"github.com/pkar/pvim/internal/undofile"
)

// Crash recovery, reachable: the E325 prompt a launch puts in front of a person
// when the file they asked for already has a swap file.
//
// internal/undofile writes the swap file and can read it back, and persist.go's
// swapCheck and chooseSwap turn a header and an answer into what this launch
// should do. This file is the half that was missing: the launch actually
// asking. Without it a kill -9 leaves a perfectly good recoverable swap file
// that pvim opens straight over and then overwrites, which is worse than never
// having written one -- a swap file nobody is offered is a swap file that only
// costs disk.
//
// # Where the prompt happens
//
// initPersist, which newFrontendEditor calls after the vimrc and before either
// frontend has started: the terminal is not in raw mode yet and the window has
// not been created. So there is one prompt for both frontends and it is on the
// terminal pvim was started from, which is also where vim's is -- vim's E325
// goes to the screen it is about to edit on, and gvim's goes to an NSAlert
// this editor does not build.
//
// It is not the editor's own command line, and it cannot be: e.sess.prompt
// reads its answer through session.getch, and getch is the frontend's, set by
// runTerminal and runWindow after newFrontendEditor has returned. A prompt that
// waited for the frontend would be a prompt after the swap file had already
// been opened, which is the one thing that must not happen -- CreateSwap is
// O_EXCL and a second editor holding the name is exactly what the person is
// being asked about.
//
// Measured against /opt/homebrew/bin/vim 9.2.0321, by killing a
// real vim with SIGKILL and reopening the file: see recover_test.go for the
// transcript and for which of vim's answers this file had to match.

// askSwap is the prompt: the E325 message in front of whoever started pvim and
// one keystroke back.
//
// A variable because there is no terminal under "go test" and a recovery test
// that wanted one would either hang or be unwritable. Tests replace it; nothing
// else does.
var askSwap = consoleSwap

// showSwap is what vim prints after the answer: the recovery block, or the one
// line (D)elete it produces. It goes to the same terminal the message went to,
// in cooked mode again, above the screen the frontend is about to take.
//
// A variable for the same reason askSwap is: a test asserts the block and does
// not want it on the suite's output.
var showSwap = func(s string) { fmt.Fprint(os.Stderr, s) }

// exitPvim is what (Q)uit and (A)bort do. Measured: vim answers both with exit
// status 1 and leaves the swap file where it is.
//
// It is os.Exit and not an error handed back up because initPersist returns
// nothing and its caller, newFrontendEditor, is window.go's. Exiting here is
// safe at exactly this point and only here: the swap file has not been opened,
// the instance socket has not been bound, the frontend has not started, and
// there is therefore nothing registered that a skipped defer would leave
// behind. One line in newFrontendEditor -- an error out of initPersist,
// returned rather than exited -- would make it ordinary; see the report.
var exitPvim = os.Exit

// consoleSwap asks on the terminal pvim was started from.
func consoleSwap(info undofile.SwapInfo, opening string) (undofile.SwapChoice, bool) {
	return promptSwapFile(os.Stdin, info, opening)
}

// promptSwapFile writes the E325 message on tty and reads the answer off it.
//
// Raw mode, because vim's dialog is answered by a single keystroke with no
// Enter after it and because a cooked terminal would echo the letter and wait
// for a line. internal/tui's driver is the same termios code the terminal
// frontend uses, borrowed rather than copied, and its ErrNoTerminal is also the
// answer to "is anybody there": a launch from the Dock, from a pipeline or from
// a test has no tty on standard input and IoctlGetTermios says so.
//
// The message still goes out when there is nobody to ask. A person who finds
// pvim's log tomorrow should be able to see that a swap file was found and not
// have to guess why the buffer came up read-only.
func promptSwapFile(tty *os.File, info undofile.SwapInfo, opening string) (undofile.SwapChoice, bool) {
	restore, err := tui.NewDriver(tty, tty).Raw()
	if err != nil {
		fmt.Fprintln(os.Stderr, info.Attention(opening))
		return 0, false
	}
	defer restore()

	// OPOST is off in raw mode, so a bare newline moves down a row and stays in
	// the column it was in. Every line of a message written to a raw terminal
	// has to end CRLF or the block comes out as a staircase.
	io.WriteString(tty, strings.ReplaceAll(info.Attention(opening), "\n", "\r\n"))

	var b [1]byte
	for {
		n, err := tty.Read(b[:])
		if err != nil || n == 0 {
			io.WriteString(tty, "\r\n")
			return 0, false
		}
		if c, ok := undofile.ParseSwapChoice(b[0], info.Running); ok {
			io.WriteString(tty, "\r\n")
			return c, true
		}
		// A key that is not a choice is not an answer and the prompt stays up,
		// which is vim: its do_dialog loops on get_keystroke until one of the
		// letters arrives.
	}
}

// recoverSwap is the E325 prompt at startup, from the stat to the decision.
//
// The second return is whether this launch goes on at all: false is (Q)uit or
// (A)bort, and the caller exits rather than opening the file.
func (e *editor) recoverSwap(opening string) (swapAction, bool) {
	// No swap file is the ordinary launch, and it wants a swap file of its own.
	if e.pers == nil || opening == "" {
		return swapAction{Swap: true}, true
	}
	info := e.pers.swapCheck(opening)
	if info == nil {
		return swapAction{Swap: true}, true
	}

	c, ok := askSwap(*info, opening)
	if !ok {
		// Nobody to ask. Take the choice vim marks as the default -- the one in
		// square brackets, which do_dialog's dfltbutton is -- because it is the
		// one that cannot lose anything: a read-only buffer, no swap file of
		// this launch's own, and the crashed session's still on the disk to
		// recover from later.
		c = undofile.SwapReadOnly
	}

	act, err := e.pers.chooseSwap(c, *info, opening)
	if act.Message != "" {
		// Vim prints the whole recovery block and then waits on "Press ENTER".
		// There is nowhere to wait here -- the frontend has not started -- so
		// it is printed and the last line of it also goes on the message line,
		// which is where a person looks next.
		showSwap(act.Message)
	}
	if err != nil {
		// A swap file that cannot be read -- vim's own, or one a half-finished
		// write left -- is a message and not a refusal to open the file. The
		// swap file is left alone, because the person may still want "vim -r"
		// at it.
		e.ed.Say(err.Error())
		return swapAction{Swap: true}, true
	}
	return act, !act.Quit && !act.Abort
}

// applySwap carries out the answer on the buffer that has just been read.
//
// It runs after the undo history has been restored and before a swap file of
// this session's own is opened, which is the only order that works: the undo
// tree describes the file on the disk, and a recovery is one more change on top
// of it.
func (e *editor) applySwap(act swapAction) {
	if act.ReadOnly {
		// 'readonly' and the buffer's own flag, so that ":set ro?" and ":ls"
		// both answer right. Neither is enforced anywhere yet: nothing in
		// internal/ex answers E45 to a ":w" over it, so this is a label and not
		// a lock until it does.
		e.opt.B.ReadOnly = true
		if b := e.cur(); b != nil {
			b.ReadOnly = true
		}
	}
	if act.Data != nil {
		e.recoverInto(act.Data, act.NoEOL, act.Cursor)
	}
	if act.Message != "" {
		// The whole block went to the terminal above the editor, the way vim's
		// does; the message line gets the last line of it, which for a recovery
		// is "You may want to delete the .swp file now." -- the line people
		// miss and the reason the next launch raises E325 again and looks like
		// the recovery failed.
		e.ed.Say(lastLine(act.Message))
	}
}

// recoverInto puts the recovered contents in the buffer and leaves it modified.
//
// Modified is the whole point. Recovery writes nothing: the file on the disk is
// still what it was before the crash, and whether the recovered buffer goes
// over it is a decision for the person who can read both. Vim does the same and
// ":q" after a recovery answers E37.
//
// The one exception is vim's too, and it is measured: when what came out of the
// swap file is byte for byte what is on the disk, vim says "Buffer contents
// equals file contents" and leaves the buffer unmodified. Nothing changed, so
// nothing is offered to write.
func (e *editor) recoverInto(data []byte, noEOL bool, cursor text.Pos) {
	if e.buf == nil {
		return
	}
	if !bytes.Equal(e.buf.Bytes(), data) {
		// text.Buffer holds lines with no terminator on them, so the trailing
		// newline Join put on comes back off. A file with no final newline
		// never had one to take off, which is what NoEOL is.
		body := data
		if !noEOL {
			body = bytes.TrimSuffix(body, []byte("\n"))
		}
		at := text.Pos{Line: 1}
		e.buf.OpenUndoBlock(at)
		e.buf.Replace(text.Range{Start: at, End: e.buf.End()}, body)
		e.buf.SetNoEOL(noEOL)
		e.buf.CloseUndoBlock()
	}
	e.ed.SetCursor(e.buf.Clamp(cursor))
}

// lastLine is the last non-empty line of a multi-line message, which is what
// fits on a one-row command line.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}
