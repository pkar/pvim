package main

import (
	"fmt"
	"os"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/tui"
)

// Which frontend the editor runs behind, and how main holds either one.
//
// internal/gui and internal/tui are peers: neither imports the other, both take
// events in and a screen.Screen out, and internal/deps_test.go fails if that
// ever stops being true. What that costs is here. Each of them declares its own
// Event union and its own Client and Handler, identical name for name and
// checked by TestFrontendVocabulariesMatch, but they are still two sets of Go
// types and no interface can be satisfied by a func taking one and a func
// taking the other. So the seam that lets main hold either behind one variable
// is not a shared Client -- there cannot be one -- it is this interface, whose
// Run swallows the whole event loop and hands the editor back only when it
// stops.
//
// That is the right place for it anyway. The two loops differ in more than
// their event types: the window's Run must have the process main thread and the
// terminal's must not, the window is built before the file is read and the
// terminal is built before its size can be asked for, and a paste is a
// PasteEvent in one and a pasteboard read in the other. Pretending those are
// one function with a flag would be a worse lie than two implementations of a
// three-method interface.
type frontend interface {
	// Name is what this frontend is called in an error message.
	Name() string

	// GUIRunning is has('gui_running') for this frontend, which internal/vimrc
	// cannot ask a frontend for itself and which decides three blocks of the
	// real vimrc: the notimeout/ttimeout/ttimeoutlen=10 branch, the FastEscape
	// augroup and the <C-c>, <C-x> and <C-v> clipboard mappings.
	GUIRunning() bool

	// Run opens c.file and drives the editor until it quits, returning errQuit
	// for a quit the user asked for and a real error for anything else.
	Run(c config) error
}

// terminalFrontend is internal/tui: pvim in the shell it was started from, and
// the daily editor.
type terminalFrontend struct{}

func (terminalFrontend) Name() string       { return "terminal" }
func (terminalFrontend) GUIRunning() bool   { return tui.GUIRunning }
func (terminalFrontend) Run(c config) error { return runTerminal(c) }

// windowFrontend is internal/gui: the AppKit window.
type windowFrontend struct{}

func (windowFrontend) Name() string     { return "window" }
func (windowFrontend) GUIRunning() bool { return gui.GUIRunning }

// Run opens the window and drives the editor behind it. See window.go.
func (windowFrontend) Run(c config) error { return runWindow(c) }

// chooseFrontend picks the one main will hold.
//
// The rule, in one sentence: a launch that has a terminal to draw in edits in
// that terminal, and a launch that has none opens a window.
//
// That is a change from "the window wherever there is one", and the reason is
// the socket. Whichever process wins the socket is the editor every later
// "pvim file" hands its file to, so the first launch decides where everybody
// else's files open for the rest of the day. On this machine the first launch
// is a shell -- a tmux pane, and usually one of several -- and the old rule
// put the socket behind a window, so a "pvim notes.md" typed in the pane the
// person was looking at opened a tab somewhere they were not. The new rule
// gives that launch the pane it was typed in, and every pane after it attaches
// to the same instance; instance.go, not this function, is what makes the
// second one a tab rather than a second editor.
//
// A launch with no terminal still gets the window, and that is most of what -g
// was for: the Dock, an "open -a", a LaunchAgent, a "pvim" whose input and
// output are pipes. So the window is not demoted, it is where it always
// belonged.
//
// The flags still win outright. --tui is the terminal whether or not this
// process can prove it has one, because a person who typed it knows something
// about their setup that an ioctl does not; -g is the window, and it is the
// one case that is an error rather than a fallback -- a person who typed -g is
// looking at their dock, and an editor quietly appearing in the shell behind
// them is worse than a sentence. A --tui that cannot have a terminal is
// internal/tui's error to report, with the file descriptors it was handed
// named in it.
//
// gui.Available answers whether a window is worth trying: false on a platform
// with no backend at all and false once Run has already been called in this
// process. It cannot answer whether a window server will talk to this process,
// which over ssh it will not, and that failure comes back from gui.Run with a
// cause on it. Over ssh that no longer matters here, because ssh gives this
// process a terminal and the terminal is now the answer; what is left is a
// launch with no tty and no window server, which fails with the cause on it,
// and that is the honest order.
//
// has('gui_running') follows the choice and not the flag, as it always did:
// TRUE in the window, FALSE in the terminal. The vimrc branches on it for the
// FastEscape augroup, the ttimeoutlen block and the CTRL-C, CTRL-X and CTRL-V
// mappings, so a launch that lands in the terminal now gets the terminal half
// of the config, which is the point.
func chooseFrontend(c config) (frontend, error) {
	if c.tui {
		return terminalFrontend{}, nil
	}
	if !gui.Available() {
		if c.window {
			return nil, fmt.Errorf("-g asks for a window and this build has no window backend: %w", gui.ErrNoBackend)
		}
		return terminalFrontend{}, nil
	}
	if !c.window && haveTerminal() {
		return terminalFrontend{}, nil
	}
	return windowFrontend{}, nil
}

// haveTerminal reports whether this process was started from a terminal it
// could edit in: one on standard input, to read keys from, and one on standard
// output, to draw on.
//
// The question is asked with the window-size ioctl, which is what
// internal/tui's driver already uses and which answers for a terminal and for
// nothing else. os.File.Stat and ModeCharDevice would be the shorter spelling
// and it is the wrong one: /dev/null is a character device too, so a launch
// from the Dock, which is exactly the launch that wants the window, would
// answer yes.
//
// Both descriptors, because they fail differently and both failures are
// silent: internal/tui takes its keys from standard input and puts its frames
// on standard output, and "pvim file </dev/null" in a terminal would go into
// raw mode on /dev/null and never see a keystroke.
//
// It is a variable so that a test can drive both answers. Nothing else
// assigns it.
var haveTerminal = func() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

// isTerminal asks f how many rows and columns it has. Only a terminal knows.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	rows, cols, err := tui.NewDriver(f, f).Size()
	return err == nil && rows > 0 && cols > 0
}
