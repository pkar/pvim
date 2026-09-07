package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestScriptOrder pins the three things that have to be in the right order or
// the case measures something else: options before the redirect so a bad :set
// does not land in every message file, the case's own keys in the middle, and
// the trailer last.
func TestScriptOrder(t *testing.T) {
	got := script("sw=4 ts=2", []byte("dw"))

	set := bytes.Index(got, []byte(":set sw=4 ts=2\r"))
	redir := bytes.Index(got, []byte(":redir! > "+messagesName))
	keys := bytes.Index(got, []byte("dw"))
	tail := bytes.Index(got, trailer)

	switch {
	case set != 0:
		t.Errorf("the options line is at %d, want the start of the script", set)
	case redir < set:
		t.Error("the redirect comes before the options line")
	case keys < redir:
		t.Error("the case keys come before the redirect")
	case tail < keys:
		t.Error("the trailer comes before the case keys")
	case tail+len(trailer) != len(got):
		t.Error("something follows the trailer")
	}
}

// TestVanillaScriptHasNoSetLine: an empty profile must not send a bare :set,
// which prints the whole option list and leaves a hit-enter prompt that eats
// the first keystroke of the case.
func TestVanillaScriptHasNoSetLine(t *testing.T) {
	got := script("", []byte("dw"))
	if bytes.Contains(got, []byte(":set")) {
		t.Errorf("a vanilla script sends a set line: %q", got)
	}
}

// TestTrailerLeavesEveryModeFirst. The case before it may have ended in insert
// mode, in operator-pending, half way through a command line, or at a hit-enter
// prompt an error left behind. Each of those eats one key and none of them
// survives four Escapes.
func TestTrailerLeavesEveryModeFirst(t *testing.T) {
	if !bytes.HasPrefix(trailer, []byte("\x1b\x1b\x1b\x1b")) {
		t.Errorf("the trailer starts %q, want four Escapes", trailer[:8])
	}
	if !bytes.HasSuffix(trailer, []byte(":wq!\r")) {
		t.Error("the trailer does not end by writing and quitting")
	}
	for _, want := range []string{
		"cursor", "getregtype", "getpos", "getchangelist", "undotree",
		stateName, ":redir END\r",
	} {
		if !bytes.Contains(trailer, []byte(want)) {
			t.Errorf("the trailer does not mention %s", want)
		}
	}
}

// TestCaseRoundTrip: a promoted fuzz finding is written as three files and read
// back as the same case, byte for byte, including a keys file full of control
// characters.
func TestCaseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := testCase{
		name: "round-trip",
		in:   []byte("one\ntwo\n"),
		keys: []byte("2dwiabc\x1b"),
		opts: "sw=4",
	}
	if err := writeCase(dir, want); err != nil {
		t.Fatal(err)
	}

	got, err := loadCase(dir, want.name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.in, want.in) || !bytes.Equal(got.keys, want.keys) || got.opts != want.opts {
		t.Errorf("round trip gave %+v, want %+v", got, want)
	}
}

// TestCaseWithoutOptsIsVanilla. An absent .opts file and an empty one mean the
// same thing, and every case in the repository has an empty one, so the absent
// path has to be exercised here or it is never exercised at all.
func TestCaseWithoutOptsIsVanilla(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []struct{ name, body string }{{"bare.in", "x\n"}, {"bare.keys", "dw"}} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := loadCase(dir, "bare")
	if err != nil {
		t.Fatal(err)
	}
	if got.opts != "" {
		t.Errorf("opts %q from a case with no .opts file", got.opts)
	}
}

// TestLoadCasesIsSorted, so that two runs print their results in the same order
// and a diff of two runs is about the editor.
func TestLoadCasesIsSorted(t *testing.T) {
	cases, err := loadCases("../../testdata/keys")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(cases); i++ {
		if cases[i-1].name >= cases[i].name {
			t.Fatalf("cases are not sorted: %s came before %s", cases[i-1].name, cases[i].name)
		}
	}
	for _, c := range cases {
		if len(c.keys) == 0 {
			t.Errorf("%s has an empty keys file", c.name)
		}
	}
}

// TestArgvIsWhatThePlanSpecifies. --clean and not -u NONE, and the reason is in
// the comment on argv: -u NONE leaves 'compatible' on, and compatible changes
// cw, backspace and <, so every case would diff for reasons that have nothing
// to do with pvim.
func TestArgvIsWhatThePlanSpecifies(t *testing.T) {
	vim := runner{kind: kindVim}
	got := vim.argv("k", "f")
	want := []string{"--clean", "-i", "NONE", "--not-a-term", "-s", "k", "f"}
	if len(got) != len(want) {
		t.Fatalf("argv %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv %v, want %v", got, want)
		}
	}

	pvim := runner{kind: kindPvim}
	if got := pvim.argv("k", "f"); got[0] != "--oracle" || got[1] != "-s" {
		t.Errorf("the candidate is run as %v, want --oracle -s KEYS FILE", got)
	}
}
