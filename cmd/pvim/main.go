// Command pvim is the editor. This file parses the argument list and hands off;
// it holds no editor logic and is not the place to put any.
//
// The three ways in are a window (internal/gui), this terminal (internal/tui)
// and --oracle, which runs a keystroke script over a file with no terminal and
// no window at all so that cmd/oracle can diff the result against real vim.
// Which of the first two a launch gets is chooseFrontend's, and whether this
// process is the editor at all is instance.go's: a file handed to a running
// instance over the socket never builds one here.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// version is a constant and not a build-time variable on purpose: a binary that
// stamps the clock into itself is a binary whose output is not reproducible.
const version = "pvim 0.0.1"

// init locks the goroutine running main to the process main thread. AppKit will
// not run anywhere else, and by the time anything calls into gui it is far too
// late to ask for the main thread back.
func init() {
	runtime.LockOSThread()
}

// The window size a headless run gets, which is measured and not a guess.
//
// Vim's answer depends entirely on the terminal it is handed, and there are two
// answers on this machine, both from vim 9.2 patches 1-321, measured on
// and again:
//
//	vim --clean -i NONE --not-a-term -s KEYS FILE with no terminal at all
//	 &lines 24, &columns 80, &window 23, &scroll 11. This is the fallback
//	 vim uses when it cannot ask, and it is what a "vim -s" run from a
//	 script with no tty reports.
//	the same command line on cmd/oracle's pseudo-terminal
//	 &lines 40, &columns 120, &window 39, &scroll 19, because the harness
//	 sets the pty to 40 by 120 (cmd/oracle/script.go, ptyRows and ptyCols)
//	 so that where vim wraps a message does not depend on who ran the
//	 oracle.
//
// The second is the one that matters and so the second is the default: every
// case in testdata/keys is graded through that harness, and a candidate whose
// window is 23 rows against a reference whose window is 39 disagrees about
// CTRL-D, CTRL-F, H, M, L and every z command on any file long enough to
// scroll. --rows and --cols exist for the other answer and for a terminal of
// any other size.
//
// The measurement is repeatable: put ":echo &lines &columns &window &scroll"
// in a keys file, run it through cmd/oracle with -keep, and read the ref side's
// msgs.txt. TestHeadlessWindowIsWhatVimReports pins both halves of it.
const (
	defaultRows = 40
	defaultCols = 120
)

// config is the argument list, parsed.
type config struct {
	clean   bool   // skip ~/.vimrc
	keys    string // -s FILE, a keystroke script
	oracle  bool   // headless: run the script, write the file, exit
	window  bool   // -g: open a window even where one is not the default
	tui     bool   // --tui: stay in this terminal, whatever a window would do
	version bool   // print the version and stop
	file    string // the positional file argument, empty for a new buffer
	rows    int    // --rows, the screen height in cells
	cols    int    // --cols, the screen width in cells

	// The instance socket. --new starts an editor of this process's own;
	// --wait keeps this process alive until the file it handed over is closed
	// again, which is the whole of what EDITOR="pvim --wait" needs.
	newInstance bool
	wait        bool
}

// parseArgs parses args (os.Args[1:]) and writes usage and errors to out.
//
// It is a function rather than a package-level flag.Parse so that the tests can
// drive it without touching the process, which is also what keeps main short
// enough to read in one go.
func parseArgs(args []string, out io.Writer) (config, error) {
	var c config
	fs := flag.NewFlagSet("pvim", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.BoolVar(&c.clean, "clean", false, "do not read ~/.vimrc")
	fs.StringVar(&c.keys, "s", "", "read keystrokes from `file`, as vim -s does")
	fs.BoolVar(&c.oracle, "oracle", false, "headless: run the -s script, write the file, exit")
	fs.BoolVar(&c.window, "g", false, "open a window (the default where there is a window backend)")
	fs.BoolVar(&c.tui, "tui", false, "run in this terminal instead of opening a window")
	fs.BoolVar(&c.newInstance, "new", false, "start a new instance instead of using the running one")
	fs.BoolVar(&c.wait, "wait", false, "with a running instance, block until the file is closed again")
	fs.BoolVar(&c.version, "version", false, "print the version and exit")
	fs.IntVar(&c.rows, "rows", envInt("PVIM_ROWS", defaultRows), "screen height in cells, as vim's &lines")
	fs.IntVar(&c.cols, "cols", envInt("PVIM_COLS", defaultCols), "screen width in cells, as vim's &columns")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if c.rows < 1 || c.cols < 1 {
		return c, fmt.Errorf("--rows and --cols must be at least 1, got %d and %d", c.rows, c.cols)
	}
	switch fs.NArg() {
	case 0:
	case 1:
		c.file = fs.Arg(0)
	default:
		// One file. The socket is how a second file gets opened, and
		// gvim's multi-file argument list is not something this vimrc uses.
		return c, fmt.Errorf("pvim takes at most one file, got %d", fs.NArg())
	}
	if c.oracle && c.keys == "" {
		return c, errors.New("--oracle needs -s FILE")
	}
	if c.window && c.tui {
		return c, errors.New("-g asks for a window and --tui asks for this terminal; pick one")
	}
	return c, nil
}

// envInt reads an integer out of the environment, falling back to def.
//
// The environment as well as a flag because the oracle runs pvim through a
// script that already has an argument list of its own, and PVIM_ROWS is one
// less thing for that script to splice in. A value that does not parse is
// ignored rather than fatal: an editor that refuses to start because a
// variable in somebody's shell profile has a typo in it is worse than one that
// opens at 24 by 80.
func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// cacheDir is ~/.cache/vim, created if it is not there.
//
// The vimrc sets undodir=~/.cache/vim and vim will not create that directory
// itself, so on this machine 'undofile' has been on and silently doing nothing
// for years. One MkdirAll fixes it. Undo history is written here and the
// instance socket lives here too.
func cacheDir(home string) (string, error) {
	dir := filepath.Join(home, ".cache", "vim")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// errQuit is the handler asking Run to return, not a failure. gui.Run hands the
// editor's error straight back to its caller, so a deliberate quit has to
// travel out through the same return the real failures use; exitStatus is where
// the two are sorted apart again.
var errQuit = errors.New("quit")

// exitStatus is the process status for whatever the editor stopped with. A quit
// the user asked for is a zero and says nothing; anything else is a one with a
// line on stderr, because a stack trace here is not something anyone can act
// on.
func exitStatus(err error) int {
	if err == nil || errors.Is(err, errQuit) {
		return 0
	}
	return 1
}

func main() {
	c, err := parseArgs(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2) // flag has already said what was wrong
	}
	if c.version {
		fmt.Println(version)
		return
	}
	// --oracle touches nothing outside the three artifacts it is asked for.
	// Every other launch makes ~/.cache/vim, because the vimrc points undodir
	// at a directory vim will not create and has therefore never used.
	if c.oracle {
		err = runOracle(c)
	} else {
		home, herr := os.UserHomeDir()
		if herr == nil {
			_, herr = cacheDir(home)
		}
		if herr != nil {
			fmt.Fprintln(os.Stderr, "pvim:", herr)
		}
		// One variable, either frontend behind it, and the loop below does not
		// know which one it got. See frontend.go for why the seam is a whole
		// Run and not a shared Client.
		// The socket, before the frontend: a file the running instance takes
		// is a tab in the editor already on the screen and nothing to start
		// here at all. See instance.go for which process ends up on which
		// side of it.
		sock := socketPath()
		handled, aerr := attach(sock, c)
		switch {
		case aerr != nil:
			err = aerr
		case handled:
			err = nil
		default:
			fe, ferr := chooseFrontend(c)
			if ferr != nil {
				err = ferr
			} else {
				err = fe.Run(c)
			}
		}
	}
	if code := exitStatus(err); code != 0 {
		// No window backend, or the editor stopped on a real one.
		fmt.Fprintln(os.Stderr, "pvim:", err)
		os.Exit(code)
	}
}
