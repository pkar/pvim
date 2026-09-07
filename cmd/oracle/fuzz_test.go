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

// TestGeneratorIsReproducible is the property that makes a fuzz finding worth
// having. A seed and an index name one script for ever, so a failure that
// arrives as "seed 7 index 4103" can be re-run on its own.
func TestGeneratorIsReproducible(t *testing.T) {
	first := bytes.Join(newGenerator(7, 4103).script(), nil)
	again := bytes.Join(newGenerator(7, 4103).script(), nil)
	if !bytes.Equal(first, again) {
		t.Errorf("the same seed and index gave %q then %q", first, again)
	}

	if other := bytes.Join(newGenerator(7, 4104).script(), nil); bytes.Equal(first, other) {
		t.Errorf("index 4103 and 4104 both gave %q", first)
	}
	if other := bytes.Join(newGenerator(8, 4103).script(), nil); bytes.Equal(first, other) {
		t.Errorf("seed 7 and 8 both gave %q", first)
	}
}

// TestGrammarCannotQuitOrShellOut checks the tables rather than the output,
// because a key that quits vim or records a macro shows up once in ten thousand
// scripts and the run it ruins is the one nobody can explain.
//
// Bursts are not checked: everything inside an insert burst is text, and the
// item that opened it always closes it.
func TestGrammarCannotQuitOrShellOut(t *testing.T) {
	// Never, in any position, because none of these is ever the second half of
	// something harmless.
	never := map[byte]string{
		':':  "an ex command line, which can do anything at all",
		'!':  "a filter through the shell",
		'Z':  "ZZ and ZQ quit",
		'Q':  "ex mode, which the script never leaves",
		'@':  "replays a register the script did not write",
		0x03: "interrupt",
		0x1a: "suspend",
	}
	// Only as the first byte of an item. gq is an operator and its q is a
	// second byte; a bare q starts recording over a register the trailer reads.
	neverFirst := map[byte]string{
		'q': "starts recording over a register the trailer reads",
	}

	for _, table := range [][]string{fuzzMotions, fuzzCharMotions, fuzzTextObjects, fuzzOperators, fuzzSimple, fuzzInserts, fuzzRegisters, fuzzCounts} {
		for _, key := range table {
			for i := range len(key) {
				if why, bad := never[key[i]]; bad {
					t.Errorf("the grammar contains %q, and %q is %s", key, key[i], why)
				}
			}
			if key == "" {
				continue
			}
			if why, bad := neverFirst[key[0]]; bad {
				t.Errorf("the grammar has an item starting %q, and that %s", key, why)
			}
		}
	}
}

// TestInsertItemsCloseThemselves: a script that ends inside insert mode types
// the trailer into the buffer, and then the case is about the trailer.
func TestInsertItemsCloseThemselves(t *testing.T) {
	g := newGenerator(11, 0)
	for range 500 {
		item := g.insert()
		if item[len(item)-1] != 0x1b {
			t.Fatalf("insert item %q does not end in Escape", item)
		}
		item = g.change()
		if item[len(item)-1] != 0x1b {
			t.Fatalf("change item %q does not end in Escape", item)
		}
	}
}

// TestCorpusCoversTheShapes pins the eight buffers. Each one is
// an edge in vim's line model and dropping one silently narrows every fuzz run
// after it.
func TestCorpusCoversTheShapes(t *testing.T) {
	needVim(t)

	files := builtinCorpus(vimrcPath)
	if len(files) != 8 {
		t.Fatalf("%d corpus files, want the eight shapes", len(files))
	}

	by := map[string][]byte{}
	for _, f := range files {
		by[f.name] = f.data
	}
	if len(by["empty.txt"]) != 0 {
		t.Error("empty.txt is not empty")
	}
	if bytes.HasSuffix(by["noeol.txt"], []byte("\n")) {
		t.Error("noeol.txt ends in a newline")
	}
	if !bytes.Contains(by["crlf.txt"], []byte("\r\n")) {
		t.Error("crlf.txt has no CRLF in it")
	}
	if !bytes.HasSuffix(by["blanklast.txt"], []byte("\n\n")) {
		t.Error("blanklast.txt does not end in a blank line")
	}
	if !bytes.Contains(by["wide.txt"], []byte("\t")) || !bytes.Contains(by["wide.txt"], []byte("日")) {
		t.Error("wide.txt has no tabs or no wide runes")
	}
	if n := bytes.Count(by["log.txt"], []byte("\n")); n != 40000 {
		t.Errorf("log.txt is %d lines, want 40000", n)
	}
}

// TestLoadCorpusPrefersTheDirectory: the built-in shapes are a fallback so the
// fuzzer runs in a fresh checkout, not a thing that overrides a corpus somebody
// put on disk.
func TestLoadCorpusPrefersTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "only.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadCorpus(dir, vimrcPath)
	if len(got) != 1 || got[0].name != "only.txt" {
		t.Errorf("loadCorpus took %d files from a directory holding one", len(got))
	}
	if n := len(loadCorpus(filepath.Join(dir, "nope"), vimrcPath)); n != 8 {
		t.Errorf("an absent corpus directory gave %d files, want the eight built in", n)
	}
}

// TestMinimiserShrinksAScript drives both minimisation passes against a
// candidate that is wrong no matter what it is given. Every subset still
// disagrees, so a working minimiser has to end at one keystroke and a
// one-line buffer; a broken one gives back what it was handed.
func TestMinimiserShrinksAScript(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim once per candidate reduction")
	}

	o, _ := newOracle(t, wrapper(t, "printf corrupted >> "+bufferName), kindVim)
	cf := corpusFile{"lines.txt", []byte("alpha\nbeta\ngamma\ndelta\n")}
	p := profile{name: vanillaProfile}
	items := [][]byte{[]byte("x"), []byte("j"), []byte("dw")}

	got := o.minimiseScript(context.Background(), cf, p, items)
	flat := bytes.Join(got, nil)
	if len(flat) != 1 {
		t.Errorf("minimised to %q; every subset reproduces, so one keystroke is the answer", flat)
	}

	shrunk := o.shrinkInput(context.Background(), cf, p, got)
	if bytes.Count(shrunk, []byte("\n")) > 1 {
		t.Errorf("input shrank to %q, want one line", shrunk)
	}
}

// TestFuzzPromotesWhatItFinds is the last step of the loop: a finding is not a
// finding until it is a case in testdata/keys that runs on its own.
func TestFuzzPromotesWhatItFinds(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim many times")
	}

	corpus := t.TempDir()
	if err := os.WriteFile(filepath.Join(corpus, "small.txt"), []byte("one two\nthree four\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promote := t.TempDir()

	o, out := newOracle(t, wrapper(t, "printf corrupted >> "+bufferName), kindVim)
	code, err := o.fuzz(context.Background(), 0, 1, 42, corpus, promote)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitDiffer {
		t.Fatalf("fuzz returned %d, want %d", code, exitDiffer)
	}

	c, err := loadCase(promote, "fuzz-42-0")
	if err != nil {
		t.Fatalf("nothing promoted: %v", err)
	}
	if len(c.keys) == 0 {
		t.Error("the promoted case has no keys in it")
	}
	// This candidate is wrong whatever it is handed, so every reduction
	// reproduces and the minimum really is one keystroke over nothing. The
	// assertion is that minimisation happened, not what it landed on.
	if len(c.keys) != 1 {
		t.Errorf("promoted keys %q, want one keystroke", c.keys)
	}
	if len(c.in) != 0 {
		t.Errorf("promoted buffer %q, want the empty buffer", c.in)
	}
	if !strings.Contains(out.String(), "minimised to") {
		t.Errorf("the run did not say what it minimised to:\n%s", out.String())
	}
}

// TestFuzzCleanRunReportsSame: the same editor on both sides, so nothing to
// find, and the run has to say so and exit 0 rather than finding something.
func TestFuzzCleanRunReportsSame(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim twice per script")
	}

	corpus := t.TempDir()
	if err := os.WriteFile(filepath.Join(corpus, "small.txt"), []byte("one two\nthree four\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	o, out := newOracle(t, referenceVim, kindVim)
	code, err := o.fuzz(context.Background(), 0, 6, 3, corpus, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if code != exitSame {
		t.Fatalf("fuzz returned %d over vim against itself, want 0:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "no unregistered diff") {
		t.Errorf("clean run said %q", out.String())
	}
}

// TestFuzzMissingArtifactIsNotADiff is finding 1 in a test. The fuzz half has
// to tell a candidate that wrote nothing apart from one that wrote the wrong
// thing, the same way runOne does, because folding the two together minimises
// against a predicate that holds for every script over every buffer and then
// promotes a case built out of nothing at all into testdata/keys.
func TestFuzzMissingArtifactIsNotADiff(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim once")
	}

	corpus := t.TempDir()
	if err := os.WriteFile(filepath.Join(corpus, "small.txt"), []byte("one two\nthree four\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promote := t.TempDir()

	o, out := newOracle(t, deadCandidate(t), kindVim)
	code, err := o.fuzz(context.Background(), 0, 1, 1, corpus, promote)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitMissing {
		t.Errorf("fuzz returned %d over a candidate that wrote nothing, want %d:\n%s", code, exitMissing, out.String())
	}

	report := out.String()
	if !strings.Contains(report, stateName) {
		t.Errorf("the run does not name the artifact that was never written:\n%s", report)
	}
	if !strings.Contains(report, "exit 1") {
		t.Errorf("the run does not say the candidate exited non-zero:\n%s", report)
	}
	if strings.Contains(report, "minimised to") {
		t.Errorf("a candidate that wrote nothing was minimised, which reduces against a predicate every input satisfies:\n%s", report)
	}
	entries, err := os.ReadDir(promote)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d files promoted from a candidate that produced no artifact", len(entries))
	}
}

// TestReproducesSeparatesMissingFromDiffering pins the split at the level the
// bug lived at, one call below the fuzz loop. Three candidates over the same
// script: the reference itself, one that corrupts the buffer, and one that does
// not start. Only the middle one is a thing to minimise towards.
func TestReproducesSeparatesMissingFromDiffering(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim six times")
	}

	cf := corpusFile{"lines.txt", []byte("one two\nthree four\n")}
	p := profile{name: vanillaProfile}
	items := [][]byte{[]byte("dw")}

	for _, tc := range []struct {
		what string
		bin  string
		want fuzzVerdict
	}{
		{"the reference against itself", referenceVim, fuzzAgreed},
		{"a candidate that corrupts the buffer", wrapper(t, "printf corrupted >> "+bufferName), fuzzDiffer},
		{"a candidate that does not start", deadCandidate(t), fuzzMissing},
	} {
		o, _ := newOracle(t, tc.bin, kindVim)
		got, cand, err := o.reproduces(context.Background(), cf, p, items)
		if err != nil {
			t.Fatalf("%s: %v", tc.what, err)
		}
		if got != tc.want {
			t.Errorf("%s: verdict %d, want %d", tc.what, got, tc.want)
		}
		if tc.want == fuzzMissing && cand.has() {
			t.Errorf("%s: the artifacts came back complete, so the caller cannot name what was never written", tc.what)
		}
	}
}

// stallingVim writes a stub that never exits, which is what real vim does when
// a generated script leaves it at the 'confirm' prompt with the keys file
// exhausted: it sits under the pty waiting for a Y nobody will type.
func stallingVim(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stalling-vim")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReferenceStallIsAGrammarHoleAndNotAHarnessFailure.
//
// A reference that never finishes used to come back from run as an error, and
// the fuzz loop turns any error into exit 1 and stops. That made one script in
// a few hundred end the whole run: at seed 11 index 77 the generated keys reach
// the 'confirm' prompt in the vimrc profile, vim waits for an answer, and 10,000
// scripts never get past 77. There is no reference answer for such a script, so
// there is nothing to diff and nothing to blame the candidate for; it is
// counted and skipped, the way an over-run trailer already was.
func TestReferenceStallIsAGrammarHoleAndNotAHarnessFailure(t *testing.T) {
	corpus := t.TempDir()
	if err := os.WriteFile(filepath.Join(corpus, "small.txt"), []byte("one two\nthree four\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	o, out := newOracle(t, stallingVim(t), kindVim)
	o.ref = runner{name: "vim", bin: stallingVim(t), kind: kindVim}
	o.timeout = time.Second

	cf := corpusFile{"lines.txt", []byte("one two\n")}
	got, _, err := o.reproduces(context.Background(), cf, profile{name: vanillaProfile}, [][]byte{[]byte("dw")})
	if err != nil {
		t.Fatalf("a reference that stalled came back as a harness error: %v", err)
	}
	if got != fuzzStalled {
		t.Errorf("verdict %d over a reference that never finished, want fuzzStalled (%d)", got, fuzzStalled)
	}

	code, err := o.fuzz(context.Background(), 0, 2, 5, corpus, t.TempDir())
	if err != nil {
		t.Fatalf("the fuzz loop stopped on a stalled reference: %v", err)
	}
	if code != exitSame {
		t.Errorf("fuzz returned %d, want %d: a script the reference cannot finish is a grammar hole", code, exitSame)
	}
	if !strings.Contains(out.String(), "2 left vim at a prompt") {
		t.Errorf("the run did not count the stalled scripts, so the hole is invisible:\n%s", out.String())
	}
}

// onceWrongVim writes a candidate that corrupts the buffer on its first run and
// behaves on every run after it, which is the shape a timing-dependent
// disagreement has: real once, gone the moment anybody looks again.
func onceWrongVim(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "once-wrong-vim")
	marker := filepath.Join(dir, "fired")
	body := "#!/bin/sh\n" + referenceVim + " \"$@\" || exit $?\n" +
		"if [ ! -f " + marker + " ]; then : > " + marker + "; printf corrupted >> " + bufferName + "; fi\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFuzzDropsADiffThatDoesNotRepeat.
//
// Two runs of one script through the same vim do not always agree. Running vim
// against vim at 10,000 scripts hit it at seed 1 index 41: three disagreements
// out of three attempts on a loaded machine, none out of ten on a quiet one.
// The damage was not the false report. The minimiser reduced against a
// predicate that held at random and promoted a four-keystroke case into
// testdata/keys that passes for anyone who runs it, so a diff has to survive a
// second run before it is worth minimising.
func TestFuzzDropsADiffThatDoesNotRepeat(t *testing.T) {
	needVim(t)
	if testing.Short() {
		t.Skip("runs vim several times")
	}

	corpus := t.TempDir()
	if err := os.WriteFile(filepath.Join(corpus, "small.txt"), []byte("one two\nthree four\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	promote := t.TempDir()

	o, out := newOracle(t, onceWrongVim(t), kindVim)
	code, err := o.fuzz(context.Background(), 0, 1, 42, corpus, promote)
	if err != nil {
		t.Fatal(err)
	}
	if code != exitSame {
		t.Errorf("fuzz returned %d over a candidate that was wrong once, want %d", code, exitSame)
	}
	if !strings.Contains(out.String(), "unrepeatable diff") {
		t.Errorf("the run did not say it threw a diff away:\n%s", out.String())
	}
	if strings.Contains(out.String(), "minimised to") {
		t.Errorf("a diff that does not repeat was minimised:\n%s", out.String())
	}
	entries, err := os.ReadDir(promote)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d files promoted from a diff that does not repeat", len(entries))
	}
}
