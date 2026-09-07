package options

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The oracle for this package is the same one the rest of the tree grades
// against: /opt/homebrew/bin/vim, 9.2 patches 1-321, run headless.
//
// Two shapes of test live here and they do different jobs. The recorded ones
// read a .tsv under testdata and need no vim, so they run on any machine and
// fail on the line somebody changed; the live one runs vim and regenerates the
// same .tsv, so a brew upgrade to a vim with a different default fails with the
// option's name on it rather than going unnoticed until an oracle case does
// something strange much later.
const (
	vimBin        = "/opt/homebrew/bin/vim"
	defaultsFile  = "testdata/vim-9.2.0321-defaults.tsv"
	vimrcFile     = "testdata/vim-9.2.0321-vimrc.tsv"
	scopesFile    = "testdata/vim-9.2.0321-scopes.tsv"
	profileFile   = "testdata/vimrc-profile.txt"
	recordedPatch = "9.2.0321"
)

// unmeasurable is every option whose ":set name?" line this package cannot be
// expected to match at startup, with the reason. None of the five is a property
// of vim: each one depends on the machine, the window or the file vim happened
// to be given.
//
// Keeping them in the recorded .tsv rather than out of it is deliberate: the
// file is what vim said, all of it, and this list is what pvim does not promise
// about it. A row that quietly vanished from the file would look the same as a
// row that never existed.
//
// Three of the five stay unmeasurable under the vimrc profile and two do not,
// because the vimrc sets them itself: line 129 gives 'shell' a value and line 73
// gives 'fileencoding' one, and from then on both are just options and have to
// round-trip like any other.
var unmeasurable = map[string]string{
	"shell":        "vim takes $SHELL and this machine's is zsh; Builtin uses the documented fallback so a test does not depend on the box it runs on",
	"scroll":       "half the window height, and no window exists in an Options; the screen model sets it when it lays one out",
	"filetype":     "detected from the file vim was given, which for the measurement was a .txt",
	"syntax":       "follows 'filetype', same reason",
	"fileencoding": "detected from the file's contents; empty in a new buffer, which is what Builtin holds",
}

// unmeasurableUnderVimrc is the same list minus the two the vimrc sets.
var unmeasurableUnderVimrc = map[string]string{
	"scroll":   unmeasurable["scroll"],
	"filetype": unmeasurable["filetype"],
	"syntax":   unmeasurable["syntax"],
}

// TestDefaultsMatchRecordedVim checks every option's startup value against what
// "vim --clean -i NONE --not-a-term -s" printed for it, line for line, in the
// text vim prints and not in a shape of this package's choosing.
//
// This is the test the package's whole default table exists to pass. Reading
// the value back through Apply and not through the field means the print format
// is under test too: " tabstop=8" with two spaces, "nowrap" with none.
func TestDefaultsMatchRecordedVim(t *testing.T) {
	o := Defaults()
	compareShow(t, defaultsFile, &o, unmeasurable)
}

// TestVimrcProfileMatchesRecordedVim is the round trip the gate asks for:
// every ":set" line in the real ~/.vimrc, applied to this package and to vim,
// and then every option in the table asked for its value in both.
//
// It covers more than the options the vimrc names, on purpose. A ":set" that
// reaches an option it was not aimed at shows up here and nowhere else.
func TestVimrcProfileMatchesRecordedVim(t *testing.T) {
	o := Defaults()
	for _, line := range profileLines(t) {
		if _, err := o.ApplyLine(line, Both); err != nil {
			t.Fatalf("the vimrc's %q, which vim takes without complaint: %v", line, err)
		}
	}
	compareShow(t, vimrcFile, &o, unmeasurableUnderVimrc)
}

// compareShow asks o for every option named in a recorded file and diffs the
// line against the one vim printed.
func compareShow(t *testing.T, path string, o *Options, skipped map[string]string) {
	t.Helper()
	for _, row := range readTSV(t, path, 2) {
		name, want := row[0], row[1]
		if why, skip := skipped[name]; skip {
			t.Logf("%s: not compared, %s", name, why)
			continue
		}
		got, err := o.Apply(name+"?", Both)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf(":set %s? is\n  pvim %q\n  vim  %q", name, got, want)
		}
	}
}

// TestScopesMatchVimsDocumentation checks the name, abbreviation, type and
// scope of every option in the table against vim's own options.txt, which is
// generated from the C table it describes and is the closest thing to a source
// of truth that ships on this machine. testdata/extract.sh is what turned that
// file into the .tsv.
//
// 'cmdheight' is the one row that disagrees and the table says why: vim scopes
// it to the tab page, a fifth scope nothing else in the table uses.
func TestScopesMatchVimsDocumentation(t *testing.T) {
	doc := map[string][3]string{}
	for _, row := range readTSV(t, scopesFile, 4) {
		doc[row[0]] = [3]string{row[1], row[2], row[3]}
	}
	kinds := map[Kind]string{Bool: "boolean", Number: "number", String: "string"}
	scopes := map[Scope]string{
		ScopeGlobal:       "global",
		ScopeBuffer:       "buffer",
		ScopeWindow:       "window",
		ScopeGlobalBuffer: "global-buffer",
		ScopeGlobalWindow: "global-window",
	}
	// The one option whose scope this package deliberately gets wrong, with
	// the reason in table.go: vim scopes 'cmdheight' to the tab page, a fifth
	// scope nothing else in the table uses and this editor has nowhere to put.
	deliberate := map[string]string{"cmdheight": "global-tabpage"}

	for i := range specs {
		s := &specs[i]
		want, ok := doc[s.Name]
		if !ok {
			t.Errorf("%q is in this table and not in vim's options.txt", s.Name)
			continue
		}
		if s.Abbrev != want[0] {
			t.Errorf("%s: abbreviation %q, vim documents %q", s.Name, s.Abbrev, want[0])
		}
		if kinds[s.Kind] != want[1] {
			t.Errorf("%s: type %s, vim documents %s", s.Name, kinds[s.Kind], want[1])
		}
		if scopes[s.Scope] != want[2] && deliberate[s.Name] != want[2] {
			t.Errorf("%s: scope %s, vim documents %s", s.Name, scopes[s.Scope], want[2])
		}
	}
}

// TestProfileCoversTheVimrc keeps the recorded profile honest. Every option the
// real vimrc sets has to be in the profile the two recorded files were measured
// under, or this package is being graded against a config the user does not
// have.
//
// The profile is a file and not a parse of the vimrc because parsing it is
// internal/vimrc's job and this package may not import it. What the profile
// does encode, which no parse would, is the branches: has('gui_running') is
// true here, the way cmd/oracle answers it, so the 'guifont' line is in and the
// terminal-only 'notimeout' block is out.
func TestProfileCoversTheVimrc(t *testing.T) {
	inProfile := map[string]bool{}
	for _, line := range profileLines(t) {
		for _, a := range splitArgs(line) {
			p, err := parse(a.text)
			if err != nil {
				t.Fatalf("the profile has %q: %v", a.text, err)
			}
			inProfile[p.name] = true
		}
	}
	// The two lines the profile does not take, because they are inside the
	// vimrc's "if !has('gui_running')" and has('gui_running') answers true
	// here, the way cmd/oracle answers it. They are not missing from the
	// profile, they are on the other side of a branch.
	branched := map[string]bool{"timeout": true, "ttimeoutlen": true}

	for _, name := range vimrcOptionNames(t) {
		if branched[name] {
			continue
		}
		if !inProfile[name] {
			t.Errorf("the vimrc sets %q and %s does not, so the recorded measurement is of a different config", name, profileFile)
		}
	}
}

// TestRecordedVimIsStillWhatVimSays runs the vim on this machine and rebuilds
// both recorded files from it.
//
// It is the test that catches a brew upgrade. Without it the .tsv files are a
// snapshot of an afternoon and nothing ever asks whether they are still true;
// with it, a vim that changed a default fails here with the option's name and
// both values, and the fix is to regenerate the file in the same commit as the
// code that follows the change.
func TestRecordedVimIsStillWhatVimSays(t *testing.T) {
	if _, err := os.Stat(vimBin); err != nil {
		t.Skipf("no vim at %s: %v", vimBin, err)
	}
	if got := vimPatch(t); got != recordedPatch {
		t.Fatalf("the recorded files are from vim %s and this machine has %s; regenerate them and read the diff before trusting it", recordedPatch, got)
	}

	var names []string
	for _, row := range readTSV(t, defaultsFile, 2) {
		names = append(names, row[0])
	}

	for _, tc := range []struct {
		file  string
		setup []string
	}{
		{defaultsFile, nil},
		{vimrcFile, profileLines(t)},
	} {
		var setup []string
		for _, line := range tc.setup {
			setup = append(setup, "set "+line)
		}
		got := askVim(t, setup, names)
		for _, row := range readTSV(t, tc.file, 2) {
			if got[row[0]] != row[1] {
				t.Errorf("%s: %s is recorded as %q and this vim says %q", tc.file, row[0], row[1], got[row[0]])
			}
		}
	}
}

// askVim runs one vim, applies the setup commands and returns the line
// ":set name?" prints for each name.
func askVim(t *testing.T, setup, names []string) map[string]string {
	t.Helper()

	dir := t.TempDir()
	out := filepath.Join(dir, "out.tsv")
	file := filepath.Join(dir, "measure.txt")
	if err := os.WriteFile(file, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var keys strings.Builder
	for _, c := range setup {
		keys.WriteString(":" + c + "\n")
	}
	keys.WriteString(":let g:r = []\n")
	keys.WriteString(":for n in ['" + strings.Join(names, "','") + "']\n")
	keys.WriteString(`:call add(g:r, n . "\t" . substitute(execute('set ' . n . '?'), '^\n', '', ''))` + "\n")
	keys.WriteString(":endfor\n")
	keys.WriteString(":call writefile(g:r, '" + out + "')\n")
	keys.WriteString(":qa!\n")

	keysPath := filepath.Join(dir, "measure.keys")
	if err := os.WriteFile(keysPath, []byte(keys.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// The exit status is not checked. vim -s reaches the end of the script,
	// finds no more input and exits 1 with "Error reading input"; the file it
	// wrote on the way is the answer, and cmd/oracle reads vim the same way.
	cmd := exec.Command(vimBin, "--clean", "-i", "NONE", "--not-a-term", "-s", keysPath, file)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = nil, nil
	_ = cmd.Run()

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("vim wrote no answers: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		name, value, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("vim printed %q, which has no tab in it", line)
		}
		got[name] = value
	}
	if len(got) != len(names) {
		t.Fatalf("asked vim for %d options and got %d back", len(names), len(got))
	}
	return got
}

// vimPatch returns the v:versionlong of the vim on this machine, spelled the
// way the recorded file names are.
func vimPatch(t *testing.T) string {
	t.Helper()
	got := askVim(t, nil, []string{"tabstop"}) // one option, to prove the pipe works
	if len(got) != 1 {
		t.Fatal("vim answered nothing at all")
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "version.txt")
	keys := ":call writefile([printf('%d.%d.%04d', v:version / 100, v:version % 100, v:versionlong % 10000)], '" + out + "')\n:qa!\n"
	keysPath := filepath.Join(dir, "v.keys")
	if err := os.WriteFile(keysPath, []byte(keys), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(vimBin, "--clean", "-i", "NONE", "--not-a-term", "-s", keysPath, filepath.Join(dir, "empty.txt"))
	cmd.Dir = dir
	_ = cmd.Run()
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("vim would not say its version: %v", err)
	}
	return strings.TrimSpace(string(body))
}

// readTSV reads one of the recorded files and checks its shape.
func readTSV(t *testing.T, path string, columns int) [][]string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if line == "" {
			continue
		}
		cols := strings.SplitN(line, "\t", columns)
		if len(cols) != columns {
			t.Fatalf("%s: %q has %d columns, want %d", path, line, len(cols), columns)
		}
		rows = append(rows, cols)
	}
	if len(rows) == 0 {
		t.Fatalf("%s is empty", path)
	}
	if !sort.SliceIsSorted(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] }) {
		t.Errorf("%s is not sorted by option name, which makes a regenerated diff unreadable", path)
	}
	return rows
}

// profileLines reads the ":set" argument lines the vimrc runs.
func profileLines(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(profileFile)
	if err != nil {
		t.Fatalf("reading %s: %v", profileFile, err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
