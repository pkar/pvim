package git

import (
	"strconv"
	"strings"
	"testing"
)

// The blame measurement. There is no fugitive oracle here and
// so, but there is a better one: `git blame` itself. A blame line in this
// editor has to name the commit git names, and since this package renders
// git's own format the whole line can be compared and not only the hash.

// TestBlameIsGitBlame runs `git blame` and this package over the same file
// and compares every line, with the source text taken off git's.
//
// Two commits by two authors of different name lengths and a file long
// enough for the line number column to need two digits, because both of
// those change the padding and both are things this package works out for
// itself rather than reading off git.
func TestBlameIsGitBlame(t *testing.T) {
	r := newRepo(t)
	var first []string
	for i := 1; i <= 12; i++ {
		first = append(first, "line "+strconv.Itoa(i))
	}
	r.write("many.txt", strings.Join(first, "\n")+"\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")

	second := append(append([]string{}, first[:5]...), "changed by someone else")
	second = append(second, first[5:]...)
	r.write("many.txt", strings.Join(second, "\n")+"\n")
	r.run("add", "-A")
	r.run("-c", "user.name=LongerName", "-c", "user.email=other@example.invalid",
		"commit", "-qm", "second")

	bl, err := r.Blame("many.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := RenderBlame(bl)
	want := blamePrefixes(t, r.run("blame", "many.txt"))
	if len(got) != len(want) {
		t.Fatalf("this package rendered %d lines and git printed %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\ngit %q", i+1, got[i], want[i])
		}
	}
	// The hashes, said separately, because that is what the gate says:
	// ":Gblame on Makefile shows the same commit per line".
	for i, b := range bl {
		if !strings.HasPrefix(want[i], b.Abbrev) {
			t.Errorf("line %d is blamed on %s and git says %q", i+1, b.Abbrev, want[i])
		}
		if b.Line != i+1 {
			t.Errorf("line %d says it is line %d", i+1, b.Line)
		}
	}
}

// TestBlameFillsInTheSummary is the part of a blame line that is not on a
// blame line: the subject of the commit, which is what makes an eight
// character hash mean something on the message line.
func TestBlameFillsInTheSummary(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "one\ntwo\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "the first subject")
	r.write("a.txt", "one\nTWO\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "the second subject")

	bl, err := r.Blame("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(bl) != 2 {
		t.Fatalf("blame gave %d lines", len(bl))
	}
	if bl[0].Summary != "the first subject" || bl[1].Summary != "the second subject" {
		t.Errorf("the summaries are %q and %q", bl[0].Summary, bl[1].Summary)
	}
	if bl[0].Hash == bl[1].Hash {
		t.Errorf("both lines are blamed on %s", bl[0].Hash)
	}
	// The summary of the first commit is carried over from the first time
	// that commit was seen, which is the part of the porcelain format this
	// parser has to remember. Two lines from one commit prove it.
	r.write("a.txt", "one\nTWO\nthree\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "the third subject")
	bl, err = r.Blame("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(bl) != 3 || bl[2].Summary != "the third subject" {
		t.Fatalf("blame gave %+v", bl)
	}
}

// TestBlameOfAnUncommittedLine is what git shows for a line that is only in
// the working tree: the zero hash and "Not Committed Yet".
func TestBlameOfAnUncommittedLine(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "one\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")
	r.write("a.txt", "one\nbrand new\n")

	bl, err := r.Blame("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(bl) != 2 {
		t.Fatalf("blame gave %d lines", len(bl))
	}
	if !bl[1].Uncommitted() {
		t.Errorf("line 2 is blamed on %s, want the zero hash", bl[1].Hash)
	}
	if bl[0].Uncommitted() {
		t.Errorf("line 1 is blamed on the zero hash")
	}
	got := RenderBlame(bl)
	want := blamePrefixes(t, r.run("blame", "a.txt"))
	for i := range got {
		g, w := got[i], want[i]
		if bl[i].Uncommitted() {
			// git stamps an uncommitted line with the time it ran, so this
			// run and the one that produced `want` are a second apart on a
			// slow box. Everything else on the line is compared.
			g, w = stripTime(g), stripTime(w)
		}
		if g != w {
			t.Errorf("line %d:\n got %q\ngit %q", i+1, got[i], want[i])
		}
	}
}

// stripTime takes the clock time out of a blame line, leaving the date, the
// zone and the line number.
func stripTime(s string) string {
	i := strings.Index(s, " ")
	if j := strings.Index(s[i+1:], ":"); j >= 0 {
		k := i + 1 + j
		return s[:k-2] + s[k+6:]
	}
	return s
}

// TestBlameOfAMissingFile is the error ":Gblame" reports.
func TestBlameOfAMissingFile(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "one\n")
	r.run("add", "-A")
	r.run("commit", "-qm", "first")
	if _, err := r.Blame("nope.txt"); err == nil {
		t.Error("blaming a file that is not there succeeded")
	}
}

// blamePrefixes strips the source text off `git blame`'s lines, leaving the
// part this package renders.
//
// The split is on the first ")", which is safe because the fixtures' authors
// and dates have no bracket in them and their source lines are never reached.
func blamePrefixes(t *testing.T, out string) []string {
	t.Helper()
	var want []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		i := strings.IndexByte(line, ')')
		if i < 0 {
			t.Fatalf("git blame printed %q with no bracket in it", line)
		}
		want = append(want, line[:i+1])
	}
	return want
}
