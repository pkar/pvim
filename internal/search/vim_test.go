package search

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pkar/pvim/internal/text"
)

// vimPath is the oracle. Every rule in this package was decided by running
// this binary, and this test is that run kept, so a rule that was right in
// September and is wrong after a brew upgrade says so with a name on it
// instead of turning into an oracle diff three packages away.
const vimPath = "/opt/homebrew/bin/vim"

// kind is which search command a step is.
type kind int

const (
	doSearch kind = iota // / or ? with a command line
	doNext               // n
	doPrev               // N
	doStar               // * or #
	doGStar              // g* or g#
)

// step is one command in a case.
type step struct {
	kind  kind
	dir   Direction
	line  string // what follows the / or the ?, for doSearch
	count int    // 0 means none typed, which is 1
}

// keys renders the step as the bytes vim -s reads.
func (s step) keys() string {
	count := ""
	if s.count > 0 {
		count = strconv.Itoa(s.count)
	}
	switch s.kind {
	case doSearch:
		return count + s.dir.String() + s.line + "\r"
	case doNext:
		return count + "n"
	case doPrev:
		return count + "N"
	case doStar:
		if s.dir == Backward {
			return count + "#"
		}
		return count + "*"
	default:
		if s.dir == Backward {
			return count + "g#"
		}
		return count + "g*"
	}
}

// run applies the step to the package's own state and reports where the cursor
// ends up, whether it wrapped, and the message if it failed.
func (s step) run(st *State, b *text.Buffer, at text.Pos, opt Options) (text.Pos, bool, string) {
	var (
		res Result
		err error
	)
	switch s.kind {
	case doSearch:
		res, err = st.Do(b, at, s.dir, s.line, s.count, opt)
	case doNext:
		res, err = st.Next(b, at, false, s.count, opt)
	case doPrev:
		res, err = st.Next(b, at, true, s.count, opt)
	case doStar:
		res, err = st.Word(b, at, s.dir, true, s.count, opt)
	default:
		res, err = st.Word(b, at, s.dir, false, s.count, opt)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		if res.Pos.Line >= 1 {
			at = res.Pos
		}
		return at, res.Wrapped, msg
	}
	return res.Pos, res.Wrapped, ""
}

// vimCase is one differential case: a buffer, a cursor, some options and a
// sequence of search commands.
type vimCase struct {
	name  string
	in    string
	line  int // 1-based cursor line
	col   int // 1-based cursor column, as vim's cursor() takes it
	opts  string
	steps []step

	// gap names a difference from vim that is known, is not this package's to
	// fix, and is kept in the table rather than deleted so that the day it is
	// fixed the row starts passing instead of being forgotten.
	gap string
}

func fwd(line string) step             { return step{kind: doSearch, dir: Forward, line: line} }
func back(line string) step            { return step{kind: doSearch, dir: Backward, line: line} }
func cnt(n int, s step) step           { s.count = n; return s }
func next() step                       { return step{kind: doNext} }
func prev() step                       { return step{kind: doPrev} }
func star(d Direction) step            { return step{kind: doStar, dir: d} }
func gstar(d Direction) step           { return step{kind: doGStar, dir: d} }
func lines(s ...string) string         { return strings.Join(s, "\n") + "\n" }
func opts(c vimCase, o string) vimCase { c.opts = o; return c }

// TestAgainstVim runs every case in the table through vim and through this
// package and diffs the cursor, the last pattern and the messages.
//
// The cursor is compared after clamping, because vim's check_cursor() pulls a
// normal-mode cursor back onto a real character and this package deliberately
// does not: /$ leaves the position after the last byte, which is the right
// answer for d/$ and not a place a cursor sits.
func TestAgainstVim(t *testing.T) {
	if _, err := os.Stat(vimPath); err != nil {
		t.Skipf("no vim at %s: %v", vimPath, err)
	}

	for _, tc := range vimCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAgainstVim(t, tc)
		})
	}
}

// checkAgainstVim runs one case both ways and reports every difference it
// finds, rather than the first, because a cursor that is wrong usually makes
// the pattern and the messages wrong too and all three together say what
// happened.
func checkAgainstVim(t *testing.T, tc vimCase) {
	t.Helper()
	if tc.gap != "" {
		t.Skip(tc.gap)
	}

	wantLine, wantCol, wantPat, wantMsgs := runVim(t, tc)

	b := text.Read([]byte(tc.in))
	opt := optionsFor(tc.opts)
	at := clamp(b, text.Pos{Line: tc.line, Col: tc.col - 1})
	st := &State{}
	var gotMsgs []string
	for _, s := range tc.steps {
		pos, wrapped, msg := s.run(st, b, at, opt)
		at = pos
		if wrapped {
			gotMsgs = append(gotMsgs, WrapMessage(dirOf(st, s)))
		}
		if msg != "" {
			gotMsgs = append(gotMsgs, msg)
		}
		// Normal mode puts the cursor on a real character before the next
		// command reads it.
		at = clamp(b, at)
	}

	if at.Line != wantLine || at.Col+1 != wantCol {
		t.Errorf("cursor = %d,%d, vim says %d,%d", at.Line, at.Col+1, wantLine, wantCol)
	}
	if st.Pattern != wantPat {
		t.Errorf("@/ = %q, vim says %q", st.Pattern, wantPat)
	}
	if got, want := unique(gotMsgs), unique(wantMsgs); !equal(got, want) {
		t.Errorf("messages = %q, vim says %q", got, want)
	}
	if t.Failed() {
		t.Logf("buffer %q cursor %d,%d opts %q keys %q", tc.in, tc.line, tc.col, tc.opts, keysOf(tc))
	}
}

// keysOf renders the whole case as the keystrokes that reproduce it by hand.
func keysOf(tc vimCase) string {
	var b strings.Builder
	for _, s := range tc.steps {
		b.WriteString(s.keys())
	}
	return b.String()
}

// dirOf is the direction the message a step printed belongs to, which for N is
// the reverse of the state's.
func dirOf(st *State, s step) Direction {
	if s.kind == doPrev {
		return st.Dir.Reverse()
	}
	return st.Dir
}

// optionsFor turns the case's:set line into the options this package takes.
// Only the ones the cases use are understood, on purpose: an option that is in
// a case and not here would silently do nothing.
func optionsFor(set string) Options {
	opt := DefaultOptions()
	for _, f := range strings.Fields(set) {
		switch {
		case f == "nowrapscan" || f == "nows":
			opt.WrapScan = false
		case f == "ignorecase" || f == "ic":
			opt.IgnoreCase = true
		case f == "smartcase" || f == "scs":
			opt.SmartCase = true
		case strings.HasPrefix(f, "iskeyword="):
			opt.IsKeyword = strings.TrimPrefix(f, "iskeyword=")
		case strings.HasPrefix(f, "iskeyword+="):
			opt.IsKeyword = defaultIsKeyword + "," + strings.TrimPrefix(f, "iskeyword+=")
		default:
			panic("vim_test: unknown option " + f)
		}
	}
	return opt
}

// clamp is vim's check_cursor(): a normal-mode cursor sits on a character, so
// the position after a line's last byte comes back to the byte the last
// character starts at.
func clamp(b *text.Buffer, p text.Pos) text.Pos {
	if p.Line < 1 {
		p.Line = 1
	}
	if p.Line > b.LineCount() {
		p.Line = b.LineCount()
	}
	line := b.Line(p.Line)
	if p.Col > len(line) {
		p.Col = len(line)
	}
	if p.Col >= len(line) && len(line) > 0 {
		_, n := utf8.DecodeLastRune(line)
		p.Col = len(line) - n
	}
	if p.Col < 0 {
		p.Col = 0
	}
	return p
}

// runVim drives the real editor over the same case and reads back the cursor,
// the last pattern and the messages it printed.
func runVim(t *testing.T, tc vimCase) (line, col int, pattern string, msgs []string) {
	t.Helper()
	dir := t.TempDir()
	buf := filepath.Join(dir, "buf.txt")
	if err := os.WriteFile(buf, []byte(tc.in), 0o600); err != nil {
		t.Fatal(err)
	}

	var k bytes.Buffer
	k.WriteString(":redir! > msgs.txt\r")
	if tc.opts != "" {
		k.WriteString(":set " + tc.opts + "\r")
	}
	fmt.Fprintf(&k, ":call cursor(%d,%d)\r", tc.line, tc.col)
	for _, s := range tc.steps {
		k.WriteString(s.keys())
		// An Escape between commands, because a command that printed an error
		// leaves vim at a hit-enter prompt and the prompt swallows the next
		// keystroke: without this, the g of a g* that follows a failed * is
		// eaten and a plain * runs instead. Escape is what the prompt eats and
		// a no-op in normal mode, so a case with no error is unaffected,
		// checked both ways.
		k.WriteString("\x1b")
	}
	k.WriteString("\x1b\x1b:redir END\r")
	k.WriteString(`:call writefile([line('.') . ' ' . col('.'), @/], 'state.txt')` + "\r")
	k.WriteString(":q!\r")
	if err := os.WriteFile(filepath.Join(dir, "keys"), k.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(vimPath, "--clean", "-i", "NONE", "--not-a-term", "-s", "keys", "buf.txt")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = nil, nil
	_ = cmd.Run() // vim's exit status says nothing useful under -s

	state, err := os.ReadFile(filepath.Join(dir, "state.txt"))
	if err != nil {
		t.Fatalf("vim wrote no state: %v", err)
	}
	// Not TrimRight: an empty last pattern is an empty second line and
	// trimming it turns "nothing was searched for" into a broken state file.
	rows := strings.Split(string(state), "\n")
	if len(rows) < 2 {
		t.Fatalf("vim state is %q", state)
	}
	if _, err := fmt.Sscanf(rows[0], "%d %d", &line, &col); err != nil {
		t.Fatalf("vim cursor is %q: %v", rows[0], err)
	}
	pattern = rows[1]

	raw, _ := os.ReadFile(filepath.Join(dir, "msgs.txt"))
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "search hit ") || isECode(l) {
			msgs = append(msgs, l)
		}
	}
	return line, col, pattern, msgs
}

// isECode spots vim's error messages in the redirected output, which also
// carries every command line the script typed.
func isECode(s string) bool {
	if len(s) < 2 || s[0] != 'E' || !isDigit(s[1]) {
		return false
	}
	i := 1
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i < len(s) && s[i] == ':'
}

// unique drops repeats. Vim's:redir records a message twice whenever the
// screen is redrawn under -s, so the count of a message says nothing and only
// the set of them does.
func unique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
