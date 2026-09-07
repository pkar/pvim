package spell

import (
	"os"
	"path/filepath"
	"testing"
)

// list builds a word list from a body in the format of a word file.
func list(t *testing.T, body string) *List {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "words")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestCaseRules is vim's: an all-lowercase entry matches the word lowercase,
// Capitalised or ALLCAPS, and an entry with a capital in it matches only that
// spelling and its ALLCAPS form.
func TestCaseRules(t *testing.T) {
	c := New(list(t, "the\nKurds\n"))
	for _, tc := range []struct {
		word string
		bad  bool
	}{
		{"the", false},
		{"The", false},
		{"THE", false},
		{"tHe", true},
		{"Kurds", false},
		{"KURDS", false},
		{"kurds", true},
	} {
		if got := c.Bad(tc.word); got != tc.bad {
			t.Errorf("Bad(%q) = %v, want %v", tc.word, got, tc.bad)
		}
	}
}

// TestAddedWordsWinOverTheDictionary, in both directions: "zg" accepts a word
// no dictionary has and "zw" rejects one it does.
func TestAddedWordsWinOverTheDictionary(t *testing.T) {
	dict := list(t, "the\ncolour\n")
	added := list(t, "pvim\ncolour/!\n")
	c := New(dict, added)

	if c.Bad("pvim") {
		t.Error("a word zg added is still bad")
	}
	if !c.Bad("colour") {
		t.Error("a word zw rejected is still good")
	}
	if c.Bad("the") {
		t.Error("the dictionary stopped being consulted")
	}
}

// TestAddFileFormat: the "/!" flag, the "#" a zug leaves behind, and the other
// affix flags a real .add file may carry.
func TestAddFileFormat(t *testing.T) {
	l := list(t, "one\ntwo/!\n#three\nfour/S\n\n  five  \n")
	c := New(l)
	for _, tc := range []struct {
		word string
		bad  bool
	}{
		{"one", false},
		{"two", true},
		{"three", true}, // commented out by a zug
		{"four", false}, // flags other than ! are not read and the word counts
		{"five", false}, // white space around the word comes off
	} {
		if got := c.Bad(tc.word); got != tc.bad {
			t.Errorf("Bad(%q) = %v, want %v", tc.word, got, tc.bad)
		}
	}
}

// TestReadOfAMissingFileIsEmptyAndNotAnError, because an empty spell.add is
// the state of every machine where nobody has pressed zg yet.
func TestReadOfAMissingFileIsEmptyAndNotAnError(t *testing.T) {
	l, err := Read(filepath.Join(t.TempDir(), "nope.add"))
	if err != nil {
		t.Fatalf("Read of a missing file: %v", err)
	}
	if l.Len() != 0 {
		t.Errorf("it holds %d words", l.Len())
	}
}

// TestStemsMakeABaseFormDictionaryUsable is the whole reason the affix table
// exists: /usr/share/dict/words holds "word" and not "words", and a checker
// without this underlines the plural of every noun in the file.
func TestStemsMakeABaseFormDictionaryUsable(t *testing.T) {
	c := New(list(t, "word\nfile\nbox\nwatch\nfly\ncity\njump\nuse\nstop\nplan\nrun\nsit\nquick\neditor\ndon\n"))
	for _, w := range []string{
		"words", "files", "boxes", "watches", "flies", "cities",
		"jumped", "used", "stopped", "planned",
		"jumping", "using", "running", "sitting",
		"quickly", "quicker", "quickest",
		"editor's", "don't", "Files", "WORDS",
	} {
		if c.Bad(w) {
			t.Errorf("%q is bad; the affix table should have found its stem", w)
		}
	}
	// And the cost, stated as a test so that nobody reads the feature as more
	// than it is: a misspelling that is a dictionary word plus a stripped
	// suffix is not caught.
	if c.Bad("flys") {
		t.Error("flys is caught; the doc comment says it is not, so one of the two is wrong")
	}
	for _, w := range []string{"wurdz", "teh", "recieve"} {
		if !c.Bad(w) {
			t.Errorf("%q is good", w)
		}
	}
}

// TestWordsSplitsALine is what the undercurl is drawn over, and mostly it is
// about what is NOT a word: an identifier, a version number and a path are
// left alone so that a spell-checked README does not underline its own code.
func TestWordsSplitsALine(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"the quick brown fox", []string{"the", "quick", "brown", "fox"}},
		{"don't stop", []string{"don't", "stop"}},
		{"'quoted' words", []string{"quoted", "words"}},
		{"utf8 and sha256 and x2", []string{"and", "and"}},
		{"spell_add and foo_bar", []string{"and"}},
		{"see ~/.cache/vim/spell.add now", []string{"see", "now"}},
		{"end. Next one", []string{"end", "Next", "one"}},
		{"", nil},
		{"1234", nil},
	} {
		var got []string
		for _, w := range Words([]byte(tc.line)) {
			got = append(got, w.Text)
		}
		if len(got) != len(tc.want) {
			t.Errorf("Words(%q) = %q, want %q", tc.line, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Words(%q)[%d] = %q, want %q", tc.line, i, got[i], tc.want[i])
			}
		}
	}
}

// TestWordsReportsByteRanges, because the highlight is over bytes and a
// multi-byte rune before the word moves it.
func TestWordsReportsByteRanges(t *testing.T) {
	line := []byte("é wurdz")
	got := Words(line)
	if len(got) != 2 {
		t.Fatalf("%d words, want 2: %+v", len(got), got)
	}
	if got[1].Text != "wurdz" || got[1].Start != 3 || got[1].End != 8 {
		t.Errorf("the second word is %+v, want wurdz at 3..8", got[1])
	}
	if string(line[got[1].Start:got[1].End]) != "wurdz" {
		t.Error("the range does not cover the word")
	}
}

// TestBadWordsOverALine is the two halves together.
func TestBadWordsOverALine(t *testing.T) {
	c := New(list(t, "the\nquick\nfox\n"))
	got := c.BadWords([]byte("the quik fox"))
	if len(got) != 1 || got[0].Text != "quik" || got[0].Start != 4 {
		t.Fatalf("BadWords gave %+v, want quik at 4", got)
	}
}

// TestTheSystemDictionaryIsUsable is the only test that reads the machine's
// own word list, and it is here because the whole design rests on what is in
// it: 235,976 base forms and not one inflection.
//
// Skipped where there is no dictionary, which is what a Linux box without
// wamerican installed looks like.
func TestTheSystemDictionaryIsUsable(t *testing.T) {
	const path = "/usr/share/dict/words"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("no %s on this machine", path)
	}
	l, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Len() < 100000 {
		t.Fatalf("%s holds %d words, which is not the dictionary this expects", path, l.Len())
	}
	c := New(l)
	for _, w := range []string{"the", "editor", "editors", "buffer", "buffers", "files", "jumped", "The"} {
		if c.Bad(w) {
			t.Errorf("%q reads as a misspelling against the real dictionary", w)
		}
	}
	for _, w := range []string{"wurdz", "asdfgh", "teh"} {
		if !c.Bad(w) {
			t.Errorf("%q reads as a word", w)
		}
	}
}
