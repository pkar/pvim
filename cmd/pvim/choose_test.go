package main

import (
	"io"
	"os"
	"testing"

	"github.com/pkar/pvim/internal/gui"
)

// guiAvailable is gui.Available, read once: it answers false after Run has been
// called, and no test here calls Run, so the answer is the box and not the
// order the tests ran in.
func guiAvailable() bool { return gui.Available() }

// withTerminal drives haveTerminal for one test, so that both branches of the
// rule are exercised on any box the suite runs on.
//
// Faking it is the only way to test it. Under "go test" the answer is false --
// the test binary's output is a pipe -- so a test that read the real one would
// check the window branch on a developer's Mac, the terminal branch nowhere,
// and neither on CI.
func withTerminal(t *testing.T, on bool) {
	t.Helper()
	old := haveTerminal
	haveTerminal = func() bool { return on }
	t.Cleanup(func() { haveTerminal = old })
}

// TestChooseFrontendPrefersTheTerminalItWasStartedFrom is the rule: a launch
// with a terminal on its standard input and output edits in that terminal.
//
// It matters because of the socket. The first launch of the day wins it and
// every "pvim file" after that hands its file to whatever holds it, so a rule
// that put the first launch in a window sent every file from every tmux pane
// to a window nobody was looking at.
func TestChooseFrontendPrefersTheTerminalItWasStartedFrom(t *testing.T) {
	withTerminal(t, true)
	fe, err := chooseFrontend(config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fe.(terminalFrontend); !ok {
		t.Fatalf("a launch from a terminal picked %s", fe.Name())
	}
	if fe.GUIRunning() {
		t.Error("has('gui_running') answers true in the terminal, which turns off the vimrc's FastEscape augroup")
	}
}

// TestChooseFrontendWithNoTerminalOpensAWindow is the other half: the Dock, an
// "open -a", a LaunchAgent, a pipeline. There is nothing to draw on, so the
// window is the only answer there is.
//
// It is written to survive both outcomes, because this file is one of the ones
// the Linux cross-build compiles and "go test" runs on boxes with no window
// backend: what is asserted is that the choice and has('gui_running') agree,
// which is the thing the vimrc branches on and the thing that would silently
// go wrong.
func TestChooseFrontendWithNoTerminalOpensAWindow(t *testing.T) {
	withTerminal(t, false)
	fe, err := chooseFrontend(config{})
	if err != nil {
		t.Fatal(err)
	}
	switch fe.(type) {
	case windowFrontend:
		if !fe.GUIRunning() {
			t.Error("has('gui_running') answers false in the window, which turns on the vimrc's terminal-only mappings")
		}
	case terminalFrontend:
		if guiAvailable() {
			t.Error("chooseFrontend picked the terminal with no terminal to pick, on a box that has a window backend")
		}
		if fe.GUIRunning() {
			t.Error("has('gui_running') answers true in the terminal, which turns off the vimrc's FastEscape augroup")
		}
	default:
		t.Fatalf("chooseFrontend picked %s", fe.Name())
	}
}

// TestChooseFrontendTuiStaysInTheTerminal. --tui is the flag for the shell that
// wants to keep the editor in it, and it wins whether or not this process can
// prove it has a terminal: a person who typed it knows something about their
// setup that an ioctl does not, and a --tui with no terminal under it is
// internal/tui's error to report with the descriptors named in it.
func TestChooseFrontendTuiStaysInTheTerminal(t *testing.T) {
	for _, term := range []bool{true, false} {
		withTerminal(t, term)
		fe, err := chooseFrontend(config{tui: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := fe.(terminalFrontend); !ok {
			t.Fatalf("--tui with haveTerminal %v picked %s", term, fe.Name())
		}
		if fe.GUIRunning() {
			t.Error("has('gui_running') answers true under --tui")
		}
	}
}

// TestMinusGWinsOverATerminal. -g is the whole of what is left of "I want the
// window": typed in a shell that could perfectly well have held the editor, it
// still opens one.
func TestMinusGWinsOverATerminal(t *testing.T) {
	withTerminal(t, true)
	fe, err := chooseFrontend(config{window: true})
	if err != nil {
		if guiAvailable() {
			t.Fatalf("-g failed on a box with a window backend: %v", err)
		}
		return
	}
	if _, ok := fe.(windowFrontend); !ok {
		t.Fatalf("-g in a terminal picked %s", fe.Name())
	}
	if !fe.GUIRunning() {
		t.Error("has('gui_running') answers false under -g")
	}
}

// TestChooseFrontendNeverFallsBackFromMinusG. A -g that cannot open a window is
// an error with a sentence, not an editor quietly appearing in the terminal
// while the person is looking at their dock. Without -g the same box gets the
// terminal and no error at all, which is the difference the flag makes.
func TestChooseFrontendNeverFallsBackFromMinusG(t *testing.T) {
	withTerminal(t, false)
	fe, err := chooseFrontend(config{window: true})
	if err != nil {
		if fe != nil {
			t.Errorf("chooseFrontend returned both %s and %v", fe.Name(), err)
		}
		if guiAvailable() {
			t.Errorf("-g failed on a box with a window backend: %v", err)
		}
		return
	}
	if _, ok := fe.(windowFrontend); !ok {
		t.Fatalf("-g picked %s", fe.Name())
	}
}

// TestHaveTerminalRefusesWhatIsNotATerminal. The check that matters is the one
// that says no: a launch from the Dock arrives with /dev/null on standard
// input, which is a character device, and an os.ModeCharDevice test would call
// it a terminal and send the editor into it.
func TestHaveTerminalRefusesWhatIsNotATerminal(t *testing.T) {
	dev, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	if isTerminal(dev) {
		t.Errorf("isTerminal(%s) is true", os.DevNull)
	}
	if isTerminal(nil) {
		t.Error("isTerminal(nil) is true")
	}
	// And "go test" itself: the binary's output is a pipe, so the real
	// predicate has to answer no here or every other test in this file is
	// checking the wrong branch.
	if haveTerminal() {
		t.Error("haveTerminal() is true under go test, where stdout is a pipe")
	}
}

// TestMinusGAndTuiTogetherIsAnError. They ask for opposite things and there is
// no sensible winner, so the argument list is refused rather than one of them
// being silently ignored.
func TestMinusGAndTuiTogetherIsAnError(t *testing.T) {
	if _, err := parseArgs([]string{"-g", "-tui"}, io.Discard); err == nil {
		t.Error("-g with --tui was accepted")
	}
}
