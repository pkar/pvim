package motion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/text"
)

// The oracle. Every motion in this package is checked by running the same
// keystrokes through the vim installed on this machine and comparing where the
// cursor landed, what column j and k would aim for next, and -- for the probes
// that run an operator -- what the buffer says afterwards.
//
// vim is driven with a script of typed keys rather than :normal, because
// :normal is not what a person types: it abandons an incomplete command
// silently and it has its own rules about counts. The script sets the cursor
// with cursor(), types the keys, and calls one function that appends a line to
// a list; the list is written out at the end and read back here. One vim
// process per corpus file, which is what makes a few thousand probes take a
// second instead of a minute.

// vimBin is the vim this project answers to. Every behavioural question in
// this package was decided by running it.
const vimBin = "/opt/homebrew/bin/vim"

// vimProbe is one question for vim: put the cursor here, type this, tell me
// where it went.
type vimProbe struct {
	// From is where the cursor starts, with Col a 0-based byte column as
	// everywhere else in this module.
	From text.Pos
	// Keys are typed exactly as written. \x1b is Escape.
	Keys string
	// Undo appends a u after the record, so that a probe running an operator
	// leaves the buffer as it found it for the next one.
	Undo bool
}

// vimAnswer is what vim reported for one probe.
type vimAnswer struct {
	Pos      text.Pos
	Curswant int // a display column, or CurswantEOL for $
	Win      Window
	Width    int
	Height   int
	Lines    []string
}

// String is what a failure message prints.
func (a vimAnswer) String() string {
	return fmt.Sprintf("%d:%d curswant=%d", a.Pos.Line, a.Pos.Col, a.Curswant)
}

// haveVim skips a test when the reference cannot or should not be run.
//
// Two reasons, and the order matters. Under -short the answer is wanted in a
// minute: the five tests that call this run vim a few thousand times and are
// eighty of the ninety seconds `go test -short ./...` used to take, so they sit
// out `make check-fast` and `make check` runs them. Then the reference not
// being installed, which is every box that is not this one.
//
// The guard is here rather than in each test because this is the one door all
// of them go through: a sixth test that shells out to vim gets the same
// treatment without anybody remembering to ask for it.
func haveVim(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: this test runs " + vimBin + " a few thousand times; make check runs it")
	}
	if _, err := os.Stat(vimBin); err != nil {
		t.Skipf("%s is not installed; the oracle cannot run", vimBin)
	}
}

// vimRecord is the ex command appended after each probe's keys. It records the
// cursor, the wanted column and the window in one tab-separated line, because
// reading five things out of one process beats running it five times.
const vimRecord = `:call add(g:r, printf("%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d", line('.'), col('.'), getcurpos()[4], line('w0'), line('w$'), winwidth(0), winheight(0), winsaveview().leftcol))` + "\r"

// vimRecordLines is the same with the buffer on the end, for the probes that
// run an operator. Turning the buffer into a string costs about a third of the
// running time of a sweep, so the sweeps that only move the cursor do not ask
// for it.
const vimRecordLines = `:call add(g:r, printf("%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s", line('.'), col('.'), getcurpos()[4], line('w0'), line('w$'), winwidth(0), winheight(0), winsaveview().leftcol, string(getline(1,'$'))))` + "\r"

// runVim answers every probe against the real vim, in one process.
//
// setup is a list of ex commands run before the probes, which is where a
// probe set that needs 'iskeyword' or 'scrolloff' changed says so.
func runVim(t *testing.T, content string, setup []string, probes []vimProbe, wantLines bool) []vimAnswer {
	t.Helper()
	haveVim(t)

	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var script strings.Builder
	for _, p := range probes {
		fmt.Fprintf(&script, ":call cursor(%d,%d)\r", p.From.Line, p.From.Col+1)
		script.WriteString(p.Keys)
		// Escape after the keys so that the ex command that follows is read
		// as a command and not as the argument of a half-typed one.
		script.WriteString("\x1b")
		if wantLines {
			script.WriteString(vimRecordLines)
		} else {
			script.WriteString(vimRecord)
		}
		if p.Undo {
			script.WriteString("u")
		}
	}
	script.WriteString(":call writefile(g:r,'out')\r:qa!\r")

	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(script.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	args := []string{"--clean", "-i", "NONE", "--not-a-term", "-c", "let g:r=[]"}
	for _, c := range setup {
		args = append(args, "-c", c)
	}
	args = append(args, "-s", keys, "f.txt")

	cmd := exec.Command(vimBin, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = nil, nil
	// vim exits non-zero when its input is not a terminal and the script runs
	// out, which is every run here, so the exit code says nothing and the
	// artifact says everything.
	_ = cmd.Run()

	out, err := os.ReadFile(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("vim wrote no answers: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(probes) {
		t.Fatalf("vim answered %d of %d probes; the script left it at a prompt", len(lines), len(probes))
	}

	fields := 8
	if wantLines {
		fields = 9
	}
	// The line count is needed for the window's AtBottom whether or not the
	// buffer itself was asked for.
	nlines := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		nlines++
	}

	answers := make([]vimAnswer, len(lines))
	for i, l := range lines {
		f := strings.SplitN(l, "\t", fields)
		if len(f) != fields {
			t.Fatalf("probe %d: cannot read %q", i, l)
		}
		n := func(s string) int {
			v, err := strconv.Atoi(s)
			if err != nil {
				t.Fatalf("probe %d: %v", i, err)
			}
			return v
		}
		a := vimAnswer{
			Pos:      text.Pos{Line: n(f[0]), Col: n(f[1]) - 1},
			Curswant: n(f[2]) - 1,
			Win:      Window{Top: n(f[3]), Bottom: n(f[4]), LeftCol: n(f[7])},
			Width:    n(f[5]),
			Height:   n(f[6]),
		}
		if wantLines {
			a.Lines = parseVimList(t, f[8])
		}
		if n(f[2]) == 2147483647 {
			a.Curswant = CurswantEOL
		}
		a.Win.AtTop = a.Win.Top == 1
		a.Win.AtBottom = a.Win.Bottom >= nlines
		answers[i] = a
	}
	return answers
}

// parseVimList reads what vimscript's string() prints for a list of strings:
// ['one', 'two'], with a single quote doubled inside.
func parseVimList(t *testing.T, s string) []string {
	t.Helper()
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		t.Fatalf("not a vim list: %q", s)
	}
	s = s[1 : len(s)-1]
	var out []string
	for i := 0; i < len(s); {
		switch s[i] {
		case ' ', ',':
			i++
			continue
		case '\'':
		default:
			t.Fatalf("not a vim list item at %d: %q", i, s)
		}
		i++
		var b strings.Builder
		for i < len(s) {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i += 2
					continue
				}
				i++
				break
			}
			b.WriteByte(s[i])
			i++
		}
		out = append(out, b.String())
	}
	return out
}
