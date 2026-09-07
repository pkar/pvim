package gui

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// The seams the editor builds against, tested for the properties that hold
// with no window server: this file has no build tag and runs on the Linux
// cross-check as well as here.

// TestEventsAreClosed pins the union. Every one of the six has to implement
// Event, and the compiler is what checks it; the test exists so that a seventh
// added without the marker method fails here rather than in a type switch in
// cmd/pvim that silently stops matching it.
func TestEventsAreClosed(t *testing.T) {
	for _, ev := range []Event{
		KeyEvent{}, MouseEvent{}, ResizeEvent{}, CloseEvent{},
		FocusEvent{}, ScaleEvent{},
	} {
		if ev == nil {
			t.Error("a nil event in the list")
		}
	}
}

// TestOnMainEitherRunsOrRefuses is the contract internal/clip is written
// against, and it is the one that holds whether or not a window is up: OnMain
// runs the closure and returns nil, or it runs nothing and returns
// ErrNoMainThread. Never both, never neither, and never a nil error with the
// work left undone, which is the failure that would show up as a clipboard
// that silently does nothing.
func TestOnMainEitherRunsOrRefuses(t *testing.T) {
	ran := false
	err := OnMain(func() { ran = true })
	if err != nil && !errors.Is(err, ErrNoMainThread) {
		t.Fatalf("OnMain returned %v, which is neither nil nor ErrNoMainThread", err)
	}
	if (err == nil) != ran {
		t.Errorf("OnMain returned %v and ran=%v; a nil error means the work ran", err, ran)
	}
}

// TestPostMainRefusesWithNoRunLoop. Unlike OnMain it cannot report whether the
// work ran, because it does not wait, so the only thing to check is that it
// refuses rather than dropping work on the floor when there is nowhere to put
// it.
func TestPostMainRefusesWithNoRunLoop(t *testing.T) {
	if err := PostMain(func() {}); err != nil && !errors.Is(err, ErrNoMainThread) {
		t.Fatalf("PostMain returned %v, which is neither nil nor ErrNoMainThread", err)
	}
}

// TestGUIRunningIsTrue is has('gui_running') from this side. internal/tui
// declares the same constant false and cmd/pvim carries whichever one it built
// a frontend from into internal/vimrc's Env, which is what makes the vimrc's
// FastEscape augroup and its clipboard mappings terminal-only.
func TestGUIRunningIsTrue(t *testing.T) {
	if !GUIRunning {
		t.Error("gui.GUIRunning is false; has('gui_running') would answer no in the window")
	}
}

// TestNoBellAndNoAlert is two of the rules as a grep over this
// package's own source, because both of them are things that would be added by
// accident and both are invisible until somebody hears or sees one.
//
// - The vimrc sets 'visualbell' and t_vb=, which together mean no bell ever,
// audible or visual. NSBeep is the one call that would break that, and it
// is what AppKit does for an unhandled key equivalent and for an unhandled
// doCommandBySelector:, which is why both of those are answered here.
// - NSAlert is on deliberately out of scope by name. Quitting with a
// modified buffer under 'confirm' prompts on the command line, the way
// terminal vim does; a modal sheet is the one MacVim feature nobody will
// miss.
// - Sending terminate: would end the process where it stands, with the
// editor's answer to the CloseEvent still on its way. What is forbidden is
// the selector as a Go string literal, which is the only way to send one,
// and not the word: -applicationShouldTerminate: is implemented here, on
// purpose, precisely so that a Quit from the Dock menu or a logout becomes
// a CloseEvent instead of a dead process, and both it and the reason it
// exists have to be sayable in a comment.
//
// Test files are excluded: a test that names the thing it forbids is how the
// forbidding is written down.
func TestNoBellAndNoAlert(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, forbidden := range []string{"NSBeep", "NSAlert", `"terminate:"`} {
			if bytes.Contains(src, []byte(forbidden)) {
				t.Errorf("%s names %s; see this test for why it may not", name, forbidden)
			}
		}
	}
}
