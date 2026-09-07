package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/gui"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/mode"
	"github.com/pkar/pvim/internal/screen"
)

func TestParseArgs(t *testing.T) {
	// size is the window every case gets unless it asked for another: the
	// measured default, 24 by 80, which is what vim itself reports under "-s"
	// with no terminal.
	size := func(c config) config {
		if c.rows == 0 {
			c.rows = defaultRows
		}
		if c.cols == 0 {
			c.cols = defaultCols
		}
		return c
	}
	cases := []struct {
		name string
		args []string
		want config
	}{
		{"nothing", nil, size(config{})},
		{"file only", []string{"x.go"}, size(config{file: "x.go"})},
		{"clean", []string{"--clean", "x.go"}, size(config{clean: true, file: "x.go"})},
		// Go's flag package takes one dash or two for the same flag, so both
		// spellings of every vim-style long option have to keep working.
		{"single dash long", []string{"-clean"}, size(config{clean: true})},
		{"keys script", []string{"-s", "k.keys", "x.go"}, size(config{keys: "k.keys", file: "x.go"})},
		{"oracle", []string{"--oracle", "-s", "k.keys", "x.go"}, size(config{oracle: true, keys: "k.keys", file: "x.go"})},
		{"version", []string{"--version"}, size(config{version: true})},
		// The window size, which is what makes the scrolling keys testable
		// headlessly.
		{"rows and cols", []string{"--rows", "41", "--cols", "120", "x.go"}, config{rows: 41, cols: 120, file: "x.go"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args, io.Discard)
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", tc.args, err)
			}
			if got != tc.want {
				t.Errorf("parseArgs(%q) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"two files", []string{"a.go", "b.go"}},
		{"oracle with no script", []string{"--oracle", "a.go"}},
		{"unknown flag", []string{"--servername", "VIM"}},
		{"s with no value", []string{"-s"}},
		{"zero rows", []string{"--rows", "0"}},
		{"negative cols", []string{"--cols", "-3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseArgs(tc.args, io.Discard); err == nil {
				t.Fatalf("parseArgs(%q) = nil error, want one", tc.args)
			}
		})
	}
}

// TestParseArgsFileWithLeadingDash pins the thing that would silently break `pvim
// -- -weird-name`: flags come before the file and everything after the first
// non-flag word belongs to the file, not to the flag set.
func TestParseArgsFileWithLeadingDash(t *testing.T) {
	got, err := parseArgs([]string{"--", "-clean"}, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got.clean || got.file != "-clean" {
		t.Errorf("got %+v, want file %q and clean false", got, "-clean")
	}
}

func TestCacheDirCreates(t *testing.T) {
	home := t.TempDir()
	dir, err := cacheDir(home)
	if err != nil {
		t.Fatalf("cacheDir: %v", err)
	}
	want := filepath.Join(home, ".cache", "vim")
	if dir != want {
		t.Errorf("cacheDir = %q, want %q", dir, want)
	}
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat %s: %v", want, err)
	}
	if !fi.IsDir() {
		t.Errorf("%s is not a directory", want)
	}
}

// TestCacheDirIsIdempotent covers the ordinary case, which is every launch
// after the first: the directory is already there and that is not an error.
func TestCacheDirIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := cacheDir(home); err != nil {
		t.Fatalf("first cacheDir: %v", err)
	}
	if _, err := cacheDir(home); err != nil {
		t.Fatalf("second cacheDir: %v", err)
	}
}

// TestCacheDirReportsFailure: a HOME that cannot hold a directory has to come
// back as an error and not as a silent editor with no persistent undo, which is
// the exact bug this whole function exists to fix.
func TestCacheDirReportsFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(home, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheDir(home); err == nil {
		t.Fatal("cacheDir over a regular file returned no error")
	}
}

func TestRunOracleMissingScript(t *testing.T) {
	err := runOracle(config{oracle: true, keys: filepath.Join(t.TempDir(), "nope.keys")})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runOracle = %v, want a not-exist error", err)
	}
}

// TestRunOracleWritesArtifacts is the wiring and not the editor: it pins that
// a run leaves the three artifacts cmd/oracle diffs, whatever the mode machine
// made of the keys.
//
// It deliberately does not assert what dw did. That is the oracle's job and it
// answers it against real vim; asserting it here as well would put a second,
// hand-written copy of vim's behaviour in a package that has none of it, and
// would go red for a bug in internal/mode rather than for one here. What has to
// hold either way is that a candidate which got the edit wrong still writes
// three files, because cmd/oracle reads a missing artifact as exit code 5, "the
// candidate did not start", and that has to stay a different report from "the
// candidate got it wrong" for as long as the mode machine is half written.
func TestRunOracleWritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "buf.txt")
	if err := os.WriteFile(file, []byte("hello world\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "run.keys")
	keys := ":set sw=4\r:redir! > msgs.txt\rdw" + string(oracleTrailer) + ":wq!\r"
	if err := os.WriteFile(script, []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runOracle(config{oracle: true, keys: script, file: file})
	if err != nil && !errors.Is(err, mode.ErrNotImplemented) {
		t.Fatalf("runOracle = %v, want nil or mode.ErrNotImplemented", err)
	}
	for _, name := range []string{"buf.txt", "state.txt", "msgs.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// vim's:redir leaves one newline in the file whether or not anything was
	// said, and a log that starts empty instead differs by a byte on every run.
	msgs, err := os.ReadFile(filepath.Join(dir, "msgs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) == 0 || msgs[0] != '\n' {
		t.Errorf("msgs.txt = %q, want it to open with a newline", msgs)
	}
	// The state dump is the format cmd/oracle's trailer makes vim write, and
	// its first line is the cursor whatever the keys did.
	state, err := os.ReadFile(filepath.Join(dir, "state.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(state), "cursor\t") {
		t.Errorf("state.txt starts %.20q, want a cursor line", state)
	}
}

// TestSplitScript pins the recogniser against the exact bytes
// cmd/oracle/script.go writes. The two live in different main packages and
// cannot share a constant, so the strings in this table are the only place the
// agreement is written down.
func TestSplitScript(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want oracleScript
	}{
		{
			"full harness script",
			":set sw=4 ts=2\r:redir! > msgs.txt\rdw" + string(oracleTrailer) + ":wq!\r",
			oracleScript{Opts: "sw=4 ts=2", Messages: "msgs.txt", Keys: []byte("dw"), Trailer: true},
		},
		{
			// The vanilla profile with a case that has no .opts line.
			"no options",
			":redir! > msgs.txt\rx" + string(oracleTrailer),
			oracleScript{Messages: "msgs.txt", Keys: []byte("x"), Trailer: true},
		},
		{
			// Somebody's own keys file, run by hand. Nothing is stripped and
			// no state.txt is written, because nobody asked for one.
			"bare keys",
			"dwZZ",
			oracleScript{Keys: []byte("dwZZ")},
		},
		{
			// A colon command that is not the harness prologue stays in the
			// keystrokes, where the ex layer will see it.
			"colon command of its own",
			":s/a/b/\rx",
			oracleScript{Keys: []byte(":s/a/b/\rx")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitScript([]byte(tc.in))
			if got.Opts != tc.want.Opts || got.Messages != tc.want.Messages ||
				got.Trailer != tc.want.Trailer || string(got.Keys) != string(tc.want.Keys) {
				t.Errorf("splitScript = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestScriptKeysEscapeIsEscape is the decoding rule a keystroke file needs and
// a terminal does not. Every byte is already there, so 0x1b is an Escape and
// never the first byte of an arrow key, and the Escape that ends every insert
// in testdata/keys comes back as one.
func TestScriptKeysEscapeIsEscape(t *testing.T) {
	got := scriptKeys([]byte("A\x1b"))
	want := []key.Key{key.Rune('A'), {Special: key.KeyEsc}}
	if len(got) != len(want) {
		t.Fatalf("scriptKeys = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("scriptKeys = %v, want %v", got, want)
		}
	}
}

// fakeClient is a Client that records what was painted through it. The real
// backend wants a window server and the process main thread; draw wants
// neither, which is the whole point of the Client interface.
type fakeClient struct {
	rows, cols int
	drawn      *screen.Screen
	title      string
	bells      int
}

func (c *fakeClient) Draw(s *screen.Screen) error { c.drawn = s; return nil }
func (c *fakeClient) Size() (int, int)            { return c.rows, c.cols }
func (c *fakeClient) SetTitle(t string) error     { c.title = t; return nil }
func (c *fakeClient) Bell()                       { c.bells++ }

// TestExitStatus is the other half of the quit path. gui.Run hands back
// whatever the handler stopped with, so the sentinel and a real failure arrive
// by the same route and only this decides which is which.
func TestExitStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"clean", nil, 0},
		{"quit", errQuit, 0},
		{"wrapped quit", fmt.Errorf("gui: %w", errQuit), 0},
		{"failure", errors.New("no window server"), 1},
		{"no backend", gui.ErrNoBackend, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitStatus(tc.err); got != tc.want {
				t.Errorf("exitStatus(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// TestEndOfRun pins the one exit status --oracle is allowed to soften. vim
// exits 0 on ZZ, so a script that quits has to leave pvim at 0 too, and
// everything else has to reach cmd/oracle as the non-zero it was.
func TestEndOfRun(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"ran to the end", nil, nil},
		{"quit", mode.ErrQuit, nil},
		{"quit wrapped", fmt.Errorf("ZZ: %w", mode.ErrQuit), nil},
		{"unimplemented key", mode.ErrNotImplemented, mode.ErrNotImplemented},
		{"anything else", boom, boom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// errors.Is with a nil target is an equality test, so the nil
			// rows assert exactly what they read as.
			if got := endOfRun(tc.in); !errors.Is(got, tc.want) {
				t.Errorf("endOfRun(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// keysDir is the case corpus cmd/oracle runs, relative to this package.
const keysDir = "../../testdata/keys"

// TestKeyCorpusWellFormed guards testdata/keys against the two ways a case
// disappears without anything going red.
//
// A case is three files sharing a base name and cmd/oracle finds them by the
// .keys one, so a case whose .in never got written is a harness error at run
// time and a case whose .opts is missing runs under the wrong profile. Worse,
// this filesystem is case-insensitive: obj_iw_word and obj_iW_word are one set
// of files, the second silently overwrites the first, and the oracle then
// reports every remaining case green over a corpus that quietly lost one. That
// happened while this corpus was being written, which is why the check is here
// and not in a comment.
//
// It lives in cmd/pvim because cmd/pvim owns the corpus. Nothing in this
// package reads it at run time.
func TestKeyCorpusWellFormed(t *testing.T) {
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		t.Fatal(err)
	}
	lower := map[string]string{}
	var names []string
	for _, e := range entries {
		n, ok := strings.CutSuffix(e.Name(), ".keys")
		if !ok {
			continue
		}
		names = append(names, n)
		if first, dup := lower[strings.ToLower(n)]; dup {
			t.Errorf("%s and %s differ only in case; on a case-insensitive "+
				"filesystem they are one case and one of them is lost", first, n)
		}
		lower[strings.ToLower(n)] = n
	}
	// The floor is the number of cases in the tree, so that a case deleted by
	// accident is a failure and not a quiet loss of coverage.
	if len(names) < 455 {
		t.Errorf("%d cases in %s, want at least 455", len(names), keysDir)
	}
	for _, n := range names {
		for _, ext := range []string{".in", ".opts"} {
			if _, err := os.Stat(filepath.Join(keysDir, n+ext)); err != nil {
				t.Errorf("%s: %v", n+ext, err)
			}
		}
		keys, err := os.ReadFile(filepath.Join(keysDir, n+".keys"))
		if err != nil {
			t.Error(err)
			continue
		}
		// An empty script edits nothing and passes for the wrong reason.
		if len(keys) == 0 {
			t.Errorf("%s.keys is empty", n)
		}
	}
}
