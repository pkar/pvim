package text

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The numbers in this table were read off vim 9.2.0321 with virtcol(), not
// worked out on paper. A tab advances to the next multiple of 'tabstop' from
// wherever it starts, a wide rune takes two cells, a combining mark takes none
// and belongs to the character in front of it, a control character shows as ^X
// in two cells and a C1 control as <80> in four.
func TestDisplayCol(t *testing.T) {
	cases := []struct {
		name string
		line string
		ts   int
		// want[i] is the display column of byte i, and the last entry is the
		// column of the position after the last byte.
		want []int
	}{
		{"plain ascii", "abc", 8, []int{0, 1, 2, 3}},
		{"leading tab", "\tab", 8, []int{0, 8, 9, 10}},
		{"tab after one char", "a\tb", 8, []int{0, 1, 8, 9}},
		{"tab at a tabstop", "abcdefgh\tx", 8, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 16, 17}},
		{"tabstop two", "a\tb", 2, []int{0, 1, 2, 3}},
		{"tabstop four", "a\tb\tc", 4, []int{0, 1, 4, 5, 8, 9}},
		// The two ideographs are three bytes each and two cells each.
		{"wide runes", "a世界b", 8, []int{0, 1, 1, 1, 3, 3, 3, 5, 6}},
		// The combining acute is two bytes and no cells at all.
		{"combining mark", "e\u0301x", 8, []int{0, 0, 0, 1, 2}},
		{"control char", "a\x01b", 8, []int{0, 1, 3, 4}},
		{"c1 control shows as <80>", "a\u0080b", 8, []int{0, 1, 1, 5, 6}},
		{"empty line", "", 8, []int{0}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			line := []byte(c.line)
			for col, want := range c.want {
				if got := DisplayCol(line, col, c.ts); got != want {
					t.Errorf("DisplayCol(%q, %d, ts=%d) = %d, want %d", c.line, col, c.ts, got, want)
				}
			}
			if got, want := DisplayWidth(line, c.ts), c.want[len(c.want)-1]; got != want {
				t.Errorf("DisplayWidth(%q, ts=%d) = %d, want %d", c.line, c.ts, got, want)
			}
		})
	}
}

// The inverse has to land on a byte a cursor can actually sit on: the start of
// the tab you clicked halfway through, not the middle of it.
func TestByteColForDisplay(t *testing.T) {
	cases := []struct {
		name string
		line string
		ts   int
		// want[d] is the byte column for display column d.
		want []int
	}{
		{"plain ascii", "abc", 8, []int{0, 1, 2, 3, 3}},
		{"inside a tab lands on the tab", "a\tb", 8, []int{0, 1, 1, 1, 1, 1, 1, 1, 2, 3}},
		{"second cell of a wide rune lands on the rune", "a世b", 8, []int{0, 1, 1, 4, 5}},
		{"past the end is the end", "ab", 8, []int{0, 1, 2, 2, 2}},
		{"empty line", "", 8, []int{0, 0}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for disp, want := range c.want {
				if got := ByteColForDisplay([]byte(c.line), disp, c.ts); got != want {
					t.Errorf("ByteColForDisplay(%q, %d, ts=%d) = %d, want %d", c.line, disp, c.ts, got, want)
				}
			}
		})
	}
}

// DisplayCol and ByteColForDisplay have to agree, or a cursor that moves down a
// column and back up again drifts.
func TestColumnRoundTrip(t *testing.T) {
	lines := []string{"abc", "\ta\tb", "a世界b", "e\u0301x\u0301y", "\t\t", ""}
	for _, ts := range []int{2, 4, 8} {
		for _, s := range lines {
			line := []byte(s)
			for col := 0; col <= len(line); col++ {
				d := DisplayCol(line, col, ts)
				back := ByteColForDisplay(line, d, ts)
				// back is the start of the character col falls in, so going
				// forward again has to give the same display column.
				if got := DisplayCol(line, back, ts); got != d {
					t.Errorf("ts=%d %q col %d: display %d -> byte %d -> display %d", ts, s, col, d, back, got)
				}
			}
		}
	}
}

// VirtCol is the bridge to vim's numbering, so its two edges are worth pinning
// separately from the table above.
func TestVirtCol(t *testing.T) {
	line := []byte("a\t世b")
	cases := []struct{ col, want int }{
		{0, 1},  // "a" occupies cell 1
		{1, 8},  // the tab ends at cell 8
		{2, 10}, // the ideograph covers cells 9 and 10, and vim reports the last
		{3, 10}, // a byte inside it reports the same
		{5, 11}, // "b"
		{6, 12}, // the position after the last byte
	}
	for _, c := range cases {
		if got := VirtCol(line, c.col, 8); got != c.want {
			t.Errorf("VirtCol(%q, %d) = %d, want %d", line, c.col, got, c.want)
		}
	}
}

// vimPath is the oracle. It is a fixed path rather than a $PATH lookup because
// this binary at this version; a different vim on the path would
// answer a slightly different question and the failure would be baffling.
const vimPath = "/opt/homebrew/bin/vim"

// TestVirtColAgainstVim asks the real thing. Everything above is a table of
// numbers somebody typed; this is the same numbers coming out of vim 9.2 for
// every byte of every line of the corpus at three tabstops, which is the only
// version of "matches vim" worth having.
func TestVirtColAgainstVim(t *testing.T) {
	if _, err := os.Stat(vimPath); err != nil {
		t.Skipf("no vim at %s to compare against", vimPath)
	}
	// unicode.txt is the file the East Asian Width table alone gets wrong: a
	// line that starts with a combining mark, the format controls vim spells
	// out as <200b>, and the emoji vim widens that Unicode calls neutral.
	for _, name := range []string{"wide.txt", "crlf.txt", "noeol.txt", "lastblank.txt", "unicode.txt"} {
		for _, ts := range []int{2, 4, 8} {
			t.Run(fmt.Sprintf("%s/ts%d", name, ts), func(t *testing.T) {
				data := readCorpus(t, name)
				b := Read(data)
				for _, w := range vimVirtCols(t, data, ts) {
					line := b.Line(w.line)
					if line == nil {
						t.Fatalf("vim reported line %d, buffer has %d", w.line, b.LineCount())
					}
					if got := VirtCol(line, w.col, ts); got != w.virt {
						t.Errorf("line %d byte col %d: VirtCol = %d, vim virtcol() = %d (line %q)",
							w.line, w.col, got, w.virt, line)
					}
				}
			})
		}
	}
}

// vimCol is one measurement: the 0-based byte column and what vim's virtcol()
// says about it.
type vimCol struct {
	line, col, virt int
}

// vimVirtCols runs vim over data and reports virtcol() at every byte position.
//
// --clean and -i NONE so no user config or viminfo can change the answer,
// --not-a-term so it does not try to be interactive, -s so the keys file is the
// whole session. This is the harness layer B, in miniature.
func vimVirtCols(t *testing.T, data []byte, tabstop int) []vimCol {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	keys := ":set ts=" + strconv.Itoa(tabstop) + "\r" +
		":let r=[]\r" +
		`:for l in range(1,line("$")) | for i in range(1, len(getline(l))) | call cursor(l,i) | call add(r, l.",".i."=".virtcol(".")) | endfor | endfor` + "\r" +
		`:call writefile(r,"state.txt")` + "\r" +
		":q!\r"
	if err := os.WriteFile(filepath.Join(dir, "k.txt"), []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(vimPath, "--clean", "-i", "NONE", "--not-a-term", "-s", "k.txt", "f.txt")
	cmd.Dir = dir
	// Vim's exit status is not the check. With stdin closed it exits 1 after a
	// perfectly good ":q!", and with stdin on a pipe it exits 0 for the same
	// session; the file it wrote on the way out is the only reliable signal,
	// and the oracle will need to know that too.
	runErr := cmd.Run()

	out, err := os.ReadFile(filepath.Join(dir, "state.txt"))
	if err != nil {
		t.Fatalf("vim wrote no state file (vim exited with %v): %v", runErr, err)
	}
	var got []vimCol
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln == "" {
			continue
		}
		var l, c, v int
		if _, err := fmt.Sscanf(ln, "%d,%d=%d", &l, &c, &v); err != nil {
			t.Fatalf("cannot parse vim output %q: %v", ln, err)
		}
		got = append(got, vimCol{line: l, col: c - 1, virt: v})
	}
	if len(got) == 0 {
		t.Fatal("vim reported no columns")
	}
	return got
}

// TestDisplayWidthAgainstVim covers the one position virtcol() at every byte
// cannot reach: the end of the line. DisplayWidth is what 'nowrap' horizontal
// scrolling and the ruler are built on, and it is where a rune the table gets
// wrong shows up as the whole line being drawn short.
func TestDisplayWidthAgainstVim(t *testing.T) {
	if _, err := os.Stat(vimPath); err != nil {
		t.Skipf("no vim at %s to compare against", vimPath)
	}
	for _, name := range []string{"wide.txt", "unicode.txt"} {
		for _, ts := range []int{2, 4, 8} {
			t.Run(fmt.Sprintf("%s/ts%d", name, ts), func(t *testing.T) {
				data := readCorpus(t, name)
				b := Read(data)
				for n, want := range vimLineWidths(t, data, ts) {
					line := b.Line(n + 1)
					if got := DisplayWidth(line, ts); got != want {
						t.Errorf("line %d: DisplayWidth = %d, vim strdisplaywidth() = %d (line %q)",
							n+1, got, want, line)
					}
				}
			})
		}
	}
}

// vimLineWidths runs vim over data and reports strdisplaywidth() per line.
func vimLineWidths(t *testing.T, data []byte, tabstop int) []int {
	t.Helper()
	keys := ":set ts=" + strconv.Itoa(tabstop) + "\r" +
		":let r=[]\r" +
		`:for l in range(1,line("$")) | call add(r, strdisplaywidth(getline(l))) | endfor` + "\r" +
		`:call writefile(map(r,'string(v:val)'),"state.txt")` + "\r" +
		":q!\r"
	var got []int
	for _, ln := range vimState(t, data, keys) {
		n, err := strconv.Atoi(ln)
		if err != nil {
			t.Fatalf("vim wrote %q, want a width", ln)
		}
		got = append(got, n)
	}
	return got
}

// TestRuneWidthAgainstVim asks vim how wide every code point is, all 1.1 million
// of them, and holds the table to the answer.
//
// It is exhaustive rather than sampled because sampling is what let the table
// ship wrong: it was generated from EastAsianWidth.txt, spot-checked against
// vim at 806 points, and disagreed with vim at 167 others. Vim carries its own
// emoji table on top of East Asian Width, so U+1F441 and U+261D and the whole
// regional-indicator block are two cells to vim and one to Unicode, and this
// package's entire job is to return the column vim's virtcol() returns.
func TestRuneWidthAgainstVim(t *testing.T) {
	if _, err := os.Stat(vimPath); err != nil {
		t.Skipf("no vim at %s to compare against", vimPath)
	}
	keys := ":let r=[]\r" +
		`:for c in range(1, 1114111) | let w = strdisplaywidth("a" . nr2char(c)) - 1 | if w != 1 | call add(r, c . " " . w) | endif | endfor` + "\r" +
		`:call writefile(r,"state.txt")` + "\r" +
		":q!\r"

	odd := map[rune]int{}
	for _, ln := range vimState(t, []byte("x\n"), keys) {
		var c, w int
		if _, err := fmt.Sscanf(ln, "%d %d", &c, &w); err != nil {
			t.Fatalf("vim wrote %q: %v", ln, err)
		}
		odd[rune(c)] = w
	}
	if len(odd) < 100000 {
		t.Fatalf("vim reported only %d non-single-width code points, the sweep did not run", len(odd))
	}

	bad := 0
	for r := rune(1); r <= 0x10FFFF; r++ {
		switch {
		case r == '\t':
			// A tab's width depends on where it starts and charAt owns it.
			continue
		case r >= 0xD800 && r <= 0xDFFF:
			// No valid utf-8 file holds a surrogate and Go's decoder never
			// hands one back, so vim's answer for it is not a fact about text.
			continue
		}
		want, ok := odd[r]
		if !ok {
			want = 1
		}
		if got := runeWidth(r); got != want {
			bad++
			if bad <= 20 {
				t.Errorf("runeWidth(U+%04X) = %d, vim strdisplaywidth() = %d", r, got, want)
			}
		}
	}
	if bad > 20 {
		t.Errorf("%d code points disagree with vim in total", bad)
	}
}

// vimState runs vim over data with the given keys and returns the lines the
// script wrote to state.txt.
func vimState(t *testing.T, data []byte, keys string) []string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "k.txt"), []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(vimPath, "--clean", "-i", "NONE", "--not-a-term", "-s", "k.txt", "f.txt")
	cmd.Dir = dir
	// The exit status is not the check; see vimVirtCols.
	runErr := cmd.Run()

	out, err := os.ReadFile(filepath.Join(dir, "state.txt"))
	if err != nil {
		t.Fatalf("vim wrote no state file (vim exited with %v): %v", runErr, err)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

// TestSpellWidthsMatchRuneWidth is the invariant Spell's doc comment claims:
// for every rune vim spells out rather than draws, the spelling is exactly as
// many cells wide as runeWidth says the rune is.
//
// It is a property and not a table because the two are separate switches over
// the same classification, and the failure it is watching for is one of them
// growing a case the other did not: a cursor column and the bytes under it
// disagreeing by four cells, on a line nobody looks at until they do.
func TestSpellWidthsMatchRuneWidth(t *testing.T) {
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogates: no valid utf-8 file holds one
		}
		s := Spell(r)
		if s == string(r) {
			continue // drawn as itself; its width is the font's business
		}
		if got, want := len(s), runeWidth(r); got != want {
			t.Fatalf("Spell(%#x) is %q, %d bytes, and runeWidth says %d cells", r, s, got, want)
		}
	}
}

// TestSpellIsVimsTranschar holds the four shapes against what vim's message
// line writes, measured through "ga": see internal/mode/ascii.go
// for the runs.
func TestSpellIsVimsTranschar(t *testing.T) {
	cases := []struct {
		r    rune
		want string
	}{
		{0x00, "^@"},
		{0x01, "^A"},
		{0x09, "^I"},
		{0x1b, "^["},
		{0x7f, "^?"},
		{0x80, "<80>"},
		{0x9f, "<9f>"},
		{0xa0, "\u00a0"}, // drawn as itself: a non-breaking space
		{0x200b, "<200b>"},
		{0x2028, "\u2028"}, // drawn as itself: vim's nonprint runs are 200b-200f and 202a-202e
		{0xfffd, "\ufffd"},
	}
	for _, c := range cases {
		if got := Spell(c.r); got != c.want {
			t.Errorf("Spell(%#x) = %q, want %q", c.r, got, c.want)
		}
	}
}
