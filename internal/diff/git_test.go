package diff

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestHunksAgainstGitDiff is the other half of the oracle: a hunk this
// package finds has to be the hunk `git diff` prints, because ":Gdiff" is a
// diff of a file against its index and a person reading it will compare it
// with the git command they would otherwise have run.
//
// It uses `git diff --no-index -U0`, whose "@@ -a,b +c,d @@" headers are the
// block boundaries with no context around them, so the comparison is exact
// and not a reading of the surrounding lines. --no-index makes git diff two
// paths outside any repository, which is what keeps this test from needing
// one and, more to the point, from being able to touch one.
//
// Git and vim both use xdiff, so where this passes the vim test passes too;
// where they would disagree is git's indent heuristic, which is on by default
// in git and off by default in vim. A fixture that separates them would fail
// one of the two tests and there is no fixture in the corpus that does. Said
// out loud because it is a real difference between the two oracles and not
// something this package has resolved.
func TestHunksAgainstGitDiff(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to git")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this box")
	}
	for _, f := range fixtures {
		// git diff has no 'diffopt': the flags that change what counts as
		// equal are command-line switches with different names, and folding
		// them in would be testing the mapping and not the diff.
		if f.opt != "internal,filler" {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			dir := writeFixture(t, f)
			cmd := exec.Command(git, "diff", "--no-index", "-U0", "--", "a.txt", "b.txt")
			cmd.Dir = dir
			// git diff exits 1 when the files differ, which is not an error.
			out, err := cmd.Output()
			if err != nil {
				if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
					t.Fatalf("git diff: %v", err)
				}
			}
			want := parseHunks(t, string(out))
			got := Diff(byteLines(f.a), byteLines(f.b), Options{})
			if len(got) != len(want) {
				t.Fatalf("this package found %d hunks, git %d\ngot  %v\nwant %v\n%s",
					len(got), len(want), got, want, out)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("hunk %d: %+v, git says %+v\n%s", i, got[i], want[i], out)
				}
			}
		})
	}
}

// parseHunks reads the "@@ -a,b +c,d @@" headers of a unified diff.
//
// Git writes "-a" for a one-line range and "-a,0" for an empty one, and an
// empty range is anchored on the line BEFORE the insertion point, which is
// this package's start minus one. That off-by-one is git's format and not a
// disagreement about where the hunk is.
func parseHunks(t *testing.T, out string) []Change {
	t.Helper()
	var cs []Change
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "@@") {
			continue
		}
		var a, b string
		if _, err := fmt.Sscanf(line, "@@ -%s +%s @@", &a, &b); err != nil {
			t.Fatalf("parsing %q: %v", line, err)
		}
		as, an := span(t, a)
		bs, bn := span(t, b)
		if an == 0 {
			as++
		}
		if bn == 0 {
			bs++
		}
		cs = append(cs, Change{AStart: as, ACount: an, BStart: bs, BCount: bn})
	}
	return cs
}

// span reads one "12,3" or "12" half of a hunk header.
func span(t *testing.T, s string) (int, int) {
	t.Helper()
	start, count := s, "1"
	if i := strings.IndexByte(s, ','); i >= 0 {
		start, count = s[:i], s[i+1:]
	}
	a, err := strconv.Atoi(start)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(count)
	if err != nil {
		t.Fatal(err)
	}
	return a, n
}
