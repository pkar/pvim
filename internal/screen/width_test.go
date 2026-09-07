package screen

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The widths this package uses are vim's. These are hand-picked spot checks;
// TestRuneWidthAgainstVim below is the exhaustive one.
func TestRuneWidthSpotChecks(t *testing.T) {
	cases := []struct {
		r rune
		w int
	}{
		{' ', 1},
		{'a', 1},
		{'~', 1},
		{'é', 1},     // precomposed, one cell
		{0x0301, 0},  // COMBINING ACUTE ACCENT, no cell of its own
		{0x2500, 1},  // box drawing, narrow, which is why the tree can use it
		{0x6F22, 2},  // 漢
		{0x3000, 2},  // ideographic space
		{0xFF21, 2},  // fullwidth A
		{0x1F600, 2}, // an emoji
		{0x1100, 2},  // hangul jamo
		{0xD7A3, 2},  // end of the hangul syllables block
		{0x20000, 2}, // plane 2 CJK
		{0x30000, 2}, // plane 3 CJK, which vim also draws wide
		{0xE0100, 0}, // variation selector supplement
		{0x200B, 1},  // see RuneWidth: vim draws this as <200b>, we do not
	}

	for _, c := range cases {
		if got := RuneWidth(c.r); got != c.w {
			t.Errorf("RuneWidth(U+%04X) = %d, want %d", c.r, got, c.w)
		}
	}
}

// TestRuneWidthAgainstVim is the oracle: every codepoint from U+0020 to
// U+2FFFF, asked of the vim this editor has to agree with.
//
// It shells out to /opt/homebrew/bin/vim and skips when that is not there, so
// the package still tests on a box with no vim. strdisplaywidth is asked about
// "a" plus the rune and one is subtracted, because a lone combining mark at the
// start of a string is drawn standing on its own and measures 1.
func TestRuneWidthAgainstVim(t *testing.T) {
	const vim = "/opt/homebrew/bin/vim"
	if _, err := os.Stat(vim); err != nil {
		t.Skipf("no vim to ask: %v", err)
	}

	dir := t.TempDir()
	script := dir + "/widths.vim"
	out := dir + "/widths.txt"
	src := "set encoding=utf-8 ambiwidth=single\n" +
		"let l = []\n" +
		"for c in range(0x20, 0x2FFFF)\n" +
		"  if (c >= 0xD800 && c <= 0xDFFF) || (c >= 0x7F && c <= 0xA0)\n" +
		"    continue\n" +
		"  endif\n" +
		"  let w = strdisplaywidth('a' . nr2char(c)) - 1\n" +
		"  if w != 1\n" +
		"    call add(l, printf('%X %d', c, w))\n" +
		"  endif\n" +
		"endfor\n" +
		"call writefile(l, '" + out + "')\n" +
		"qall!\n"
	if err := os.WriteFile(script, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(vim, "--clean", "-es", "-S", script)
	cmd.Stdin = strings.NewReader("")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running vim: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// vim renders 34 format-control codepoints as <200b>, six cells wide. That
	// expansion is the window renderer's job and not the grid's, so
	// here they are width 1 and this test says so out loud rather than by
	// silently passing.
	unprintable := 0
	odd := map[rune]int{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			t.Fatalf("unexpected line from vim: %q", sc.Text())
		}
		cp, err := strconv.ParseInt(fields[0], 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		w, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatal(err)
		}
		if w > 2 {
			unprintable++
			if RuneWidth(rune(cp)) != 1 {
				t.Errorf("RuneWidth(U+%04X) = %d; vim draws it as <%x> and we give it one cell",
					cp, RuneWidth(rune(cp)), cp)
			}
			continue
		}
		odd[rune(cp)] = w
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(odd) == 0 {
		t.Fatal("vim reported no wide or zero-width runes at all; the script did not run")
	}
	if unprintable != 34 {
		t.Errorf("vim has %d unprintable codepoints below U+30000, want the 34 this table was built against", unprintable)
	}

	mismatches := 0
	for cp := rune(0x20); cp <= 0x2FFFF; cp++ {
		if (cp >= 0xD800 && cp <= 0xDFFF) || (cp >= 0x7F && cp <= 0xA0) {
			continue
		}
		want, ok := odd[cp]
		if !ok {
			want = 1
		}
		if got := RuneWidth(cp); got != want {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("RuneWidth(U+%04X) = %d, vim says %d", cp, got, want)
			}
		}
	}
	if mismatches > 20 {
		t.Errorf("... and %d more", mismatches-20)
	}
}
