package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestVimrcProfileCarriesWhatThePlanNames pins the options the second profile
// exists to exercise. Every one of them changes what a keystroke does, and an
// option that quietly stops being in the profile takes a whole class of cases
// down to one profile without failing anything.
func TestVimrcProfileCarriesWhatThePlanNames(t *testing.T) {
	needVim(t)

	opts, err := vimrcProfile(vimrcPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(opts)
	have := map[string]bool{}
	for _, f := range fields {
		have[f] = true
	}

	for _, want := range []string{
		"shiftround",
		"shiftwidth=4",
		"tabstop=2",
		"softtabstop=2",
		"smartcase",
		"ignorecase",
		"backspace=indent,eol,start",
		"nowrap",
		"scrolloff=3",
		"sidescrolloff=5",
		"hlsearch",
		"incsearch",
		"linebreak",
		"breakindent",
	} {
		if !have[want] {
			t.Errorf("the vimrc profile has no %s\n%s", want, opts)
		}
	}

	// scrolloff is set twice in the file, the second time inside a guard that
	// is false. A reader without the guard ends at 1.
	if have["scrolloff=1"] {
		t.Errorf("scrolloff=1 is in the profile; the `if !&scrolloff` guard was not evaluated")
	}
	for _, unwanted := range []string{"undofile", "undodir=~/.cache/vim"} {
		if have[unwanted] {
			t.Errorf("%s is in the profile; it writes an undo file per run", unwanted)
		}
	}
	if strings.Contains(opts, "Disable") {
		t.Errorf("a trailing comment leaked into the profile:\n%s", opts)
	}
}

// TestVimrcProfileAppliesInVim is the assertion that matters more than the
// string: real vim, given the generated line, ends up with those values. A
// profile that parses and is then rejected by :set would leave every case
// running under --clean defaults and passing.
func TestVimrcProfileAppliesInVim(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim")
	}

	opts, err := vimrcProfile(vimrcPath)
	if err != nil {
		t.Fatal(err)
	}
	keys := []byte(":set " + opts + "\r" +
		`:call writefile([&sw . ' ' . &ts . ' ' . &sts . ' ' . &so . ' ' . &sr . ' ' . &scs . ' ' . &wrap . ' ' . &bs], '` + stateName + `')` + "\r" +
		":wq!\r")

	ref := runner{name: "vim", bin: referenceVim, kind: kindVim}
	a, err := run(context.Background(), ref, filepath.Join(t.TempDir(), "opts"),
		[]byte("one\ntwo\n"), keys, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if a.exitCode != 0 {
		t.Fatalf("vim exited %d applying the profile", a.exitCode)
	}

	const want = "4 2 2 3 1 1 0 indent,eol,start\n"
	if got := string(a.state); got != want {
		t.Errorf("vim ended with sw ts sts so sr scs wrap bs = %q, want %q", got, want)
	}
}

func TestStripComment(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`tabstop=2       " number of visual spaces per TAB`, "tabstop=2"},
		{`noerrorbells visualbell t_vb= " Disable ALL bells"`, "noerrorbells visualbell t_vb="},
		{`wildignore=*.o,*.obj,*.bak`, "wildignore=*.o,*.obj,*.bak"},
		{`" a whole comment`, ""},
		{`scrolloff=3`, "scrolloff=3"},
	} {
		if got := stripComment(tc.in); got != tc.want {
			t.Errorf("stripComment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEvalCond(t *testing.T) {
	seen := map[string]string{"scrolloff": "3", "sidescrolloff": ""}
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{`has('gui_running')`, true},
		{`has("autocmd")`, true},
		{`!has('gui_running')`, false},
		{`has('unnamed')`, false},
		{`has('clipboard') && !has('gui_running')`, false}, // not understood, so not taken
		{`!&scrolloff`, false},
		{`!&sidescrolloff`, true},
		{`&scrolloff`, true},
	} {
		if got := evalCond(tc.in, seen); got != tc.want {
			t.Errorf("evalCond(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestVimrcProfileTouchesNothingOutsideTheScratchDirectory is the hermetic
// rule, checked against the whole generated line rather than against a list of
// options somebody remembered to write down.
//
// A run is reproducible only if everything it reads is something the harness
// put in its scratch directory. optsSkip is the register of options that break
// that, and an option in it that reaches the profile anyway is the whole gate
// quietly measuring the machine instead of the editor.
func TestVimrcProfileTouchesNothingOutsideTheScratchDirectory(t *testing.T) {
	needVim(t)

	opts, err := vimrcProfile(vimrcPath)
	if err != nil {
		t.Fatal(err)
	}
	if tok, reason, found := escapingOpt(opts); found {
		t.Errorf("the vimrc profile carries %s, which %s\n%s", tok, reason, opts)
	}

	// Named as well as swept, because the sweep above is only as strong as
	// optsSkip: an option deleted from that table stops being caught by it and
	// stops being dropped from the profile in the same edit, and the test would
	// go green on the way past.
	for _, tok := range strings.Fields(opts) {
		switch optName(tok) {
		case "clipboard", "undofile", "undodir":
			t.Errorf("the vimrc profile carries %s, which reaches outside the scratch directory\n%s", tok, opts)
		}
	}
}

// TestCaseOptsThatEscapeAreRefused covers the other door into the same script.
// The generated profile drops these options; a case's own .opts line is copied
// in as it stands, and a case that sets one of them has to stop the run rather
// than produce a diff that moves on its own.
func TestCaseOptsThatEscapeAreRefused(t *testing.T) {
	needVim(t)

	for _, opts := range []string{
		"clipboard=unnamed",
		"cb=unnamed,autoselect",
		"smartcase clipboard+=unnamedplus",
		"undofile",
	} {
		if _, err := profiles(vimrcPath, opts); err == nil {
			t.Errorf("profiles accepted case opts %q", opts)
		}
	}
	if _, err := profiles(vimrcPath, "expandtab shiftwidth=4"); err != nil {
		t.Errorf("profiles refused ordinary case opts: %v", err)
	}
}

// TestOptName pins the shapes :set allows around an option name, because the
// skip table and the refusal above both key off it and a name that comes out
// with a "no" still on it matches nothing.
func TestOptName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"clipboard=unnamed,autoselect", "clipboard"},
		{"clipboard+=unnamed", "clipboard"},
		{"clipboard-=autoselect", "clipboard"},
		{"clipboard^=unnamed", "clipboard"},
		{"undofile", "undofile"},
		{"noundofile", "undofile"},
		{"invundofile", "undofile"},
		{"undofile?", "undofile"},
		{"undofile!", "undofile"},
		{"shiftwidth=4", "shiftwidth"},
		{"backspace=indent,eol,start", "backspace"},
	} {
		if got := optName(tc.in); got != tc.want {
			t.Errorf("optName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestReferenceVimDoesNotReadTheSystemPasteboard is the mechanism, measured on
// the real thing rather than argued from the option list.
//
// With 'clipboard' containing "unnamed" the register a bare P reads is "*, and
// on this machine "* is the pasteboard the person is using. This runs the
// reference under the vimrc profile with one keystroke, P, and asserts the
// buffer came back untouched. Before the option was dropped from the profile
// this pasted whatever was on the board into the file.
//
// It reads the pasteboard and never writes it: a test that clobbers what
// somebody copied is a worse neighbour than the bug it is chasing. That is also
// why it skips on an empty board, where the assertion would hold for the wrong
// reason.
func TestReferenceVimDoesNotReadTheSystemPasteboard(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the pasteboard this is about is macOS's")
	}

	board, err := exec.Command("/usr/bin/pbpaste").Output()
	if err != nil {
		t.Skipf("no pbpaste: %v", err)
	}
	first, _, _ := strings.Cut(string(board), "\n")
	if strings.TrimSpace(first) == "" {
		t.Skip("the pasteboard is empty, so a put that read it would look like a put that did not")
	}

	all, err := profiles(vimrcPath, "")
	if err != nil {
		t.Fatal(err)
	}
	var vimrcOpts string
	for _, p := range all {
		if p.name == vimrcProfileName {
			vimrcOpts = p.opts
		}
	}
	if vimrcOpts == "" {
		t.Fatal("no vimrc profile")
	}

	in := []byte("one\ntwo\n")
	ref := runner{name: "vim", bin: referenceVim, kind: kindVim}
	a, err := run(context.Background(), ref, filepath.Join(t.TempDir(), "board"),
		in, script(vimrcOpts, []byte("P")), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if a.exitCode != 0 {
		t.Fatalf("vim exited %d", a.exitCode)
	}
	if strings.Contains(string(a.buffer), first) {
		t.Errorf("vim pasted the system pasteboard into the buffer:\n%s", a.buffer)
	}
	if !bytes.Equal(a.buffer, in) {
		t.Errorf("P with nothing yanked changed the buffer\ngot  %q\nwant %q", a.buffer, in)
	}
}
