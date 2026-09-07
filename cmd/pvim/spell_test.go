package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/screen"
)

// The four word-list keys and the undercurl, none of which cmd/oracle can
// grade.
//
// Why not: vim's own spell checking on this machine reads a .spl file compiled
// from a hunspell dictionary and pvim reads /usr/share/dict/words, so the two
// disagree about which words are bad before either of them draws anything --
// and vim's message for "zG" names a temp file whose name is different on
// every run. The one thing a case CAN grade is the refusal, and
// testdata/keys/z_spellfile_not_set does: with no 'spellfile' and no
// ~/.cache/vim, "zg" is "E764: Option 'spellfile' is not set" in both editors.
// Everything else about spelling is tested here.

// spellFixture points spellDict at a small word list and returns an editor
// over the text with 'spell' set on its window.
func spellFixture(t *testing.T, text string) *editor {
	t.Helper()
	dir := t.TempDir()
	dict := filepath.Join(dir, "words")
	if err := os.WriteFile(dict, []byte("the\nquick\nbrown\nfox\nword\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := spellDict
	spellDict = dict
	t.Cleanup(func() { spellDict = old })

	// ":setlocal spell", and not a write into the window's own options,
	// because the path the vimrc uses is the whole thing being tested: its
	// "au BufEnter *.txt,*.md setlocal spell" goes through the ex layer's
	// pushOptions and ex.ApplyWindowOptions, and 'spell' reached opt.W and
	// stopped there until that was fixed.
	e := newTestEditor(t, text)
	if err := e.sess.runLine("setlocal spell"); err != nil {
		t.Fatal(err)
	}
	if !e.sess.win().Opt.Spell {
		t.Fatal(":setlocal spell did not reach the window")
	}
	return e
}

// setSpellFile points 'spellfile' at a fresh .add and returns its name.
func setSpellFile(t *testing.T, e *editor) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "my.add")
	if err := e.sess.runLine("setlocal spellfile=" + name); err != nil {
		t.Fatal(err)
	}
	return name
}

// TestZgAddsAWordToTheSpellFile is the message and the file, both measured
// against vim 9.2.0321 with 'spellfile' set: "Word 'wurdz' added to my.add",
// and the word on a line of its own.
func TestZgAddsAWordToTheSpellFile(t *testing.T) {
	e := spellFixture(t, "the wurdz here\n")
	name := setSpellFile(t, e)
	runKeys(t, e, "wzg")

	if got, want := e.message(), "Word 'wurdz' added to "+name; got != want {
		t.Errorf("the message is %q, want %q", got, want)
	}
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "wurdz\n" {
		t.Errorf("the file holds %q, want %q", body, "wurdz\n")
	}
	// And the word is good from here on, without a reload.
	if e.sess.spellStateFor().checker().Bad("wurdz") {
		t.Error("the word is still bad after zg")
	}
}

// TestZwRejectsAWord: the same message and the "/!" flag, which is what makes
// a word list able to say "not this one".
func TestZwRejectsAWord(t *testing.T) {
	e := spellFixture(t, "the quick fox\n")
	name := setSpellFile(t, e)
	runKeys(t, e, "wzw")

	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "quick/!\n" {
		t.Errorf("the file holds %q, want %q", body, "quick/!\n")
	}
	if !e.sess.spellStateFor().checker().Bad("quick") {
		t.Error("a word zw rejected is still good")
	}
}

// TestZGAddsToTheInternalList is the key that used to fail loudly: the list is
// a temp file that lasts the session, exactly as vim's is, and the message
// names it.
func TestZGAddsToTheInternalList(t *testing.T) {
	e := spellFixture(t, "the wurdz here\n")
	runKeys(t, e, "wzG")

	st := e.sess.spellStateFor()
	if st.internal == nil {
		t.Fatal("zG made no internal list")
	}
	name := st.internal.Name
	t.Cleanup(func() { os.Remove(name) })
	if got, want := e.message(), "Word 'wurdz' added to "+name; got != want {
		t.Errorf("the message is %q, want %q", got, want)
	}
	if st.checker().Bad("wurdz") {
		t.Error("the word is still bad after zG")
	}
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("the message names a file that is not there: %v", err)
	}
	if string(body) != "wurdz\n" {
		t.Errorf("the internal list holds %q", body)
	}

	// zW is the same list and the reject flag.
	runKeys(t, e, "0zW")
	if !st.checker().Bad("the") {
		t.Error("zW did not reject the word")
	}
}

// TestAWordAddedLastSessionIsStillGood: the spellfile is read on the first
// check and not only when a "zg" writes to it, which is the difference between
// remembering a word and underlining it again on the next launch.
func TestAWordAddedLastSessionIsStillGood(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, cacheFallback), 0o700); err != nil {
		t.Fatal(err)
	}
	add := filepath.Join(home, cacheFallback, "spell.add")
	if err := os.WriteFile(add, []byte("wurdz\nquick/!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	e := spellFixture(t, "the quick wurdz fox\n")
	s := e.draw(40, 120)

	bad := e.hl.ID(screen.GroupSpellBad)
	const line = "the quick wurdz fox"
	for col := range len(line) {
		got := s.Grid.At(0, col).HL
		// "wurdz" was added and is good; "quick" was rejected with a /! and is
		// bad, even though the dictionary holds it.
		want := screen.Normal
		if col >= 4 && col < 9 {
			want = bad
		}
		if got != want {
			t.Errorf("column %d (%q) is highlight %v, want %v", col, line[col:col+1], got, want)
		}
	}
}

// TestZgWithNoSpellFileIsE764, which is the answer under --oracle and the
// reason testdata/keys/z_spellfile_not_set still passes.
func TestZgWithNoSpellFileIsE764(t *testing.T) {
	e := spellFixture(t, "the wurdz here\n")
	t.Setenv("HOME", t.TempDir()) // no ~/.cache/vim in it

	runKeys(t, e, "wzg")
	if got := e.message(); got != "E764: Option 'spellfile' is not set" {
		t.Errorf("the message is %q, want E764", got)
	}
}

// TestSpellFileDefaultsToTheCacheDirectory is the rule: an empty
// 'spellfile' means ~/.cache/vim/spell.add, and only when that directory is
// already there. cmd/pvim/main.go makes it on every launch but --oracle.
func TestSpellFileDefaultsToTheCacheDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, cacheFallback), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	e := spellFixture(t, "the wurdz here\n")
	runKeys(t, e, "wzg")

	want := filepath.Join(home, cacheFallback, "spell.add")
	if got := e.message(); got != "Word 'wurdz' added to "+want {
		t.Errorf("the message is %q, want the cache directory", got)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("nothing was written to %s: %v", want, err)
	}
	if string(body) != "wurdz\n" {
		t.Errorf("%s holds %q", want, body)
	}
}

// TestZgOnAnEmptyLineIsE349: vim asks for the word before it asks for the
// file, so a "zg" with nothing under the cursor never mentions 'spellfile'.
// Measured.
func TestZgOnAnEmptyLineIsE349(t *testing.T) {
	e := spellFixture(t, "the wurdz\n\n")
	runKeys(t, e, "2Gzg")
	if got := e.message(); got != "E349: No identifier under cursor" {
		t.Errorf("the message is %q, want E349", got)
	}
}

// TestZgCountPicksTheSpellFile is vim's E765, measured with one entry in
// 'spellfile' and a count of two.
func TestZgCountPicksTheSpellFile(t *testing.T) {
	e := spellFixture(t, "the wurdz here\n")
	setSpellFile(t, e)
	runKeys(t, e, "w2zg")
	if got := e.message(); got != "E765: 'spellfile' does not have 2 entries" {
		t.Errorf("the message is %q, want E765", got)
	}
}

// TestSpellUnderlinesTheBadWords is the feature itself, through the whole
// stack: an editor with 'spell' set on the window, a redraw, and the cells of
// the misspelt word carrying the SpellBad highlight while the rest do not.
func TestSpellUnderlinesTheBadWords(t *testing.T) {
	e := spellFixture(t, "the quick wurdz fox\n")
	s := e.draw(40, 120)

	bad := e.hl.ID(screen.GroupSpellBad)
	const line = "the quick wurdz fox"
	for col := range len(line) {
		got := s.Grid.At(0, col).HL
		want := screen.Normal
		if col >= strings.Index(line, "wurdz") && col < strings.Index(line, "wurdz")+len("wurdz") {
			want = bad
		}
		if got != want {
			t.Errorf("column %d (%q) is highlight %v, want %v", col, line[col:col+1], got, want)
		}
	}
}

// TestTheVimrcUnderlinesAMarkdownFile is the gate that matters, and it is the
// only test here that uses neither a fake dictionary nor a hand-set option: it
// opens a .md file through the same function the terminal and the window both
// start from, with the real ~/.vimrc, and asks what got drawn.
//
// What it proves, in one run: the vimrc's "au BufEnter *.txt,*.md setlocal
// spell spelllang=en_us" fires, 'spell' reaches the window, the dictionary at
// /usr/share/dict/words is read, and the five cells of the one misspelt word
// carry SpellBad while the other fourteen do not.
//
// Skipped where either the vimrc or the dictionary is missing, which is what a
// CI box looks like.
func TestTheVimrcUnderlinesAMarkdownFile(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if _, err := os.Stat(filepath.Join(home, ".vimrc")); err != nil {
		t.Skip("no ~/.vimrc on this machine")
	}
	if _, err := os.Stat(spellDict); err != nil {
		t.Skipf("no %s on this machine", spellDict)
	}

	dir := t.TempDir()
	md := filepath.Join(dir, "notes.md")
	const line = "the quick wurdz fox"
	if err := os.WriteFile(md, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	e, err := newFrontendEditor(config{file: md, rows: 40, cols: 120}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !e.sess.win().Opt.Spell {
		t.Fatalf("the vimrc did not turn 'spell' on for a .md file: filetype %q", e.opt.B.FileType)
	}

	s := e.draw(40, 120)
	bad := e.hl.ID(screen.GroupSpellBad)
	for col := range len(line) {
		got := s.Grid.At(0, col).HL
		want := screen.Normal
		if col >= strings.Index(line, "wurdz") && col < strings.Index(line, "wurdz")+len("wurdz") {
			want = bad
		}
		if got != want {
			t.Errorf("column %d (%q) is highlight %v, want %v", col, line[col:col+1], got, want)
		}
	}
}

// TestSpellDrawsNothingWithoutTheOption, which is every window but a .txt or
// a .md one: the vimrc's "au BufEnter *.txt,*.md setlocal spell" is the only
// thing that turns it on.
func TestSpellDrawsNothingWithoutTheOption(t *testing.T) {
	e := spellFixture(t, "the quick wurdz fox\n")
	e.sess.win().Opt.Spell = false
	s := e.draw(40, 120)

	for col := range len("the quick wurdz fox") {
		if got := s.Grid.At(0, col).HL; got != screen.Normal {
			t.Fatalf("column %d is highlight %v with 'nospell'", col, got)
		}
	}
}

// TestSpellWithNoDictionaryUnderlinesNothing. A machine with no
// /usr/share/dict/words gets spell checking that is off rather than spell
// checking that says every word is wrong.
func TestSpellWithNoDictionaryUnderlinesNothing(t *testing.T) {
	e := spellFixture(t, "the quick wurdz fox\n")
	old := spellDict
	spellDict = filepath.Join(t.TempDir(), "no-such-dictionary")
	defer func() { spellDict = old }()
	e.sess.spell = nil

	s := e.draw(40, 120)
	for col := range len("the quick wurdz fox") {
		if got := s.Grid.At(0, col).HL; got != screen.Normal {
			t.Fatalf("column %d is highlight %v with no dictionary", col, got)
		}
	}
}
