package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// referenceVim is the oracle every test here measures against. There is no
// point mocking it: the whole harness exists because this binary is the only
// thing that knows what vim does.
const referenceVim = "/opt/homebrew/bin/vim"

// vimrcPath is the config the vimrc profile is generated from.
var vimrcPath = defaultVimrc

// needVim skips a test on a machine that does not have the reference.
func needVim(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(referenceVim); err != nil {
		t.Skipf("no reference vim at %s", referenceVim)
	}
	if _, err := os.Stat(vimrcPath); err != nil {
		t.Skipf("no vimrc at %s", vimrcPath)
	}
}

// newOracle builds a harness whose candidate is whatever binary the test wants.
// The scratch directory is the test's own, so a failing test leaves its runs
// behind for as long as go test keeps them and no longer.
func newOracle(t *testing.T, candBin string, kind runnerKind) (*oracle, *bytes.Buffer) {
	t.Helper()
	reg := loadRegister()
	out := &bytes.Buffer{}
	return &oracle{
		ref:     runner{name: "vim", bin: referenceVim, kind: kindVim},
		cand:    runner{name: "pvim", bin: candBin, kind: kind},
		reg:     reg,
		vimrc:   vimrcPath,
		scratch: t.TempDir(),
		timeout: 30 * time.Second,
		profile: "all",
		out:     out,
	}, out
}

// wrapper writes a shell script that runs the real vim and then does something
// to what it left behind. It is how a candidate that is wrong in a known way
// gets built without an editor to be wrong with.
//
// The script runs with the run directory as its working directory, which is
// what lets it name the artifacts without knowing where they are.
func wrapper(t *testing.T, after string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wrapped-vim")
	body := "#!/bin/sh\n" + referenceVim + " \"$@\" || exit $?\n" + after + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestVimAgainstItself is the test that has to pass before the harness is worth
// anything at all: the same script through the same editor twice, and every
// artifact identical.
//
// pvim cannot edit yet, so this is the only way to know whether a diff the
// oracle reports later is the editor's fault or the harness's. It covers every
// case in testdata/keys under both profiles, so it also catches a case whose
// script over-runs its own trailer, which shows up as a reference that exits
// non-zero rather than as a diff.
func TestVimAgainstItself(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs the reference twice per case per profile")
	}

	o, _ := newOracle(t, referenceVim, kindVim)
	cases, err := loadCases("../../testdata/keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 12 {
		t.Fatalf("%d cases; six behaviours that bite and a dozen is the floor", len(cases))
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results, err := o.runCase(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 {
				t.Fatalf("%d profiles, want vanilla and vimrc", len(results))
			}
			for _, r := range results {
				if r.outcome != outcomeSame {
					t.Errorf("%s/%s: %s, want identical: %v %s",
						r.caseName, r.profile, r.outcome, r.diffs, r.note)
				}
			}
		})
	}
}

// TestCorruptedCandidateIsADifference proves the other half: a candidate that
// is wrong by one byte has to fail, and fail with 4 rather than 5.
func TestCorruptedCandidateIsADifference(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim twice")
	}

	o, out := newOracle(t, wrapper(t, "printf corrupted >> "+bufferName), kindVim)
	o.profile = vanillaProfile
	c, err := loadCase("../../testdata/keys", "dw_word")
	if err != nil {
		t.Fatal(err)
	}

	results, err := o.runCase(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.outcome != outcomeDiffer {
		t.Fatalf("outcome %s, want different", r.outcome)
	}
	if r.outcome.code() != exitDiffer {
		t.Errorf("exit code %d, want %d", r.outcome.code(), exitDiffer)
	}
	if len(r.diffs) != 1 || r.diffs[0].name != bufferName {
		t.Fatalf("diffs %v, want just the buffer", r.diffs)
	}

	o.print(r)
	report := out.String()
	if !strings.Contains(report, bufferName+" differs at byte ") {
		t.Errorf("report does not name the first differing byte:\n%s", report)
	}
	if n := strings.Count(report, "\n"); n > maxDiffLines {
		t.Errorf("report is %d lines, capped at %d", n, maxDiffLines)
	}
}

// TestMissingArtifactIsNotADiff is why 5 exists. A candidate that wrote no
// state at all has to be told apart from one that wrote the wrong state,
// because the first is a crash and the second is a bug, and a crash rendered as
// a three-line window is a morning wasted.
func TestMissingArtifactIsNotADiff(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim twice")
	}

	o, _ := newOracle(t, wrapper(t, "rm -f "+stateName), kindVim)
	o.profile = vanillaProfile
	c, err := loadCase("../../testdata/keys", "dw_word")
	if err != nil {
		t.Fatal(err)
	}

	results, err := o.runCase(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.outcome != outcomeMissing {
		t.Fatalf("outcome %s, want missing", r.outcome)
	}
	if r.outcome.code() != exitMissing {
		t.Errorf("exit code %d, want %d", r.outcome.code(), exitMissing)
	}
	if !strings.Contains(r.note, stateName) {
		t.Errorf("note %q does not name the missing artifact", r.note)
	}
}

// TestRegisteredDifferencePasses is the third layer working: the same corrupted
// candidate, with a row in the register that covers it, is a tilde and not a
// failure.
func TestRegisteredDifferencePasses(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim twice")
	}

	o, out := newOracle(t, wrapper(t, "printf corrupted >> "+bufferName), kindVim)
	// A row that covers this case under this profile only, added to the
	// harness's own table for the length of the test.
	o.reg.byKeys["dw_word@vanilla"] = []difference{{
		id:   "D-900",
		keys: "dw_word@vanilla",
		what: "the candidate appends the word corrupted",
		why:  "it is a test",
	}}
	o.profile = vanillaProfile
	c, err := loadCase("../../testdata/keys", "dw_word")
	if err != nil {
		t.Fatal(err)
	}

	results, err := o.runCase(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	r := results[0]
	if r.outcome != outcomeRegistered {
		t.Fatalf("outcome %s, want registered", r.outcome)
	}
	// Registered is a pass, so the code is zero. The tilde line below is the
	// only place a registered difference shows up.
	if r.outcome.code() != exitSame {
		t.Errorf("exit code %d, want %d", r.outcome.code(), exitSame)
	}
	if r.entry.id != "D-900" {
		t.Errorf("entry %q, want D-900", r.entry.id)
	}

	o.print(r)
	if line := out.String(); !strings.HasPrefix(line, "~") || !strings.Contains(line, "D-900") {
		t.Errorf("registered line %q does not start with a tilde and name the id", line)
	}
}

// TestVanillaAndVimrcDisagree makes sure the second profile is doing something.
// shiftround with sw=4 over a three-space indent lands on four; --clean's sw=8
// over the same line lands somewhere else, and if the two profiles ever produce
// the same bytes here the vimrc profile has stopped being applied.
func TestVanillaAndVimrcDisagree(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim twice")
	}

	o, _ := newOracle(t, referenceVim, kindVim)
	c, err := loadCase("../../testdata/keys", "shift_round")
	if err != nil {
		t.Fatal(err)
	}
	all, err := profiles(o.vimrc, c.opts)
	if err != nil {
		t.Fatal(err)
	}

	var got [][]byte
	for _, p := range all {
		a, err := run(context.Background(), o.ref,
			filepath.Join(o.scratch, p.name), c.in, script(p.opts, c.keys), o.timeout)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, a.buffer)
	}
	if bytes.Equal(got[0], got[1]) {
		t.Errorf("both profiles produced %q; the vimrc profile is not reaching vim", got[0])
	}
}

// deadCandidate is a stand-in for a candidate that does not start: it prints a
// line and exits non-zero, leaving no state.txt and no msgs.txt behind. That is
// what `pvim --oracle` does, and every half of the harness has to say so
// rather than call it a small difference.
func deadCandidate(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dead-pvim")
	body := "#!/bin/sh\necho 'pvim: --oracle: nothing to run through yet' >&2\nexit 1\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRegisteredDifferenceExitsZero pins the contract the register states in
// its own second paragraph: a diff registered there passes. A run whose worst
// outcome is registered has to exit 0, or `make oracle` goes red on exactly the
// differences the register exists to accept and the register stops being worth
// keeping.
func TestRegisteredDifferenceExitsZero(t *testing.T) {
	for _, tc := range []struct {
		o    outcome
		want int
	}{
		{outcomeSame, exitSame},
		{outcomeRegistered, exitSame},
		{outcomeDiffer, exitDiffer},
		{outcomeMissing, exitMissing},
	} {
		if got := tc.o.code(); got != tc.want {
			t.Errorf("outcome %s exits %d, want %d", tc.o, got, tc.want)
		}
	}
}

// TestValidProfileRejectsATypo: -candidate-kind already refuses a value it does
// not know and -profile has to as well, because the flag silently naming no
// profile is a run that compares nothing.
func TestValidProfileRejectsATypo(t *testing.T) {
	for _, name := range []string{"all", vanillaProfile, vimrcProfileName} {
		if err := validProfile(name); err != nil {
			t.Errorf("validProfile(%q): %v", name, err)
		}
	}
	if err := validProfile("vimrcs"); err == nil {
		t.Error("validProfile accepted \"vimrcs\"; a typo has to be an error, not a no-op run")
	}
}

// TestUnknownProfileRunsNothingAndSaysSo: with a profile name that matches no
// profile, runCase used to skip every one of them, return no results and no
// error, and let the summary line report success over zero comparisons. A
// candidate that corrupts every buffer passes a run like that.
func TestUnknownProfileRunsNothingAndSaysSo(t *testing.T) {
	needVim(t)

	o, _ := newOracle(t, referenceVim, kindVim)
	o.profile = "vimrcs"
	c := testCase{name: "made_up", in: []byte("hello world\n"), keys: []byte("x")}

	results, err := o.runCase(context.Background(), c)
	if err == nil {
		t.Fatalf("an unknown profile gave %d results and no error", len(results))
	}
	if !strings.Contains(err.Error(), "vimrcs") {
		t.Errorf("error %q does not name the profile that matched nothing", err)
	}
}
