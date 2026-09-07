package ex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The differential test: the same ex command line through this package and
// through /opt/homebrew/bin/vim, with the buffer and the cursor compared.
//
// It is the same idea as cmd/oracle and deliberately not the same code. The
// oracle drives whole keystroke scripts through the built binary and diffs
// three artifacts; this drives one colon line through the package and diffs
// two things, so that a failure names the command rather than the run. When
// the ex layer is wired into cmd/pvim these cases belong in testdata/keys as
// well.
//
// It skips rather than fails when vim is not on the box, because a Linux CI
// box has no /opt/homebrew and the rest of the package's tests are worth
// running there.

const vimPath = "/opt/homebrew/bin/vim"

// vimResult is what one run of vim left behind.
type vimResult struct {
	text string
	line int
	col  int
}

// runVim runs one ex command line over the given lines and returns the buffer
// and the cursor. before is a normal-mode prologue, which is how a case puts
// the cursor somewhere other than line 1.
func runVim(t *testing.T, lines []string, before, cmdline string) vimResult {
	t.Helper()
	// os.MkdirTemp and not t.TempDir: t.TempDir puts the subtest's name in the
	// path, and these subtests are named after ex command lines, so the
	// directory would hold characters like ">" and ";" that then go into a
	// vimscript string literal unescaped.
	dir, err := os.MkdirTemp("", "pvim-ex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pos := filepath.Join(dir, "pos.txt")
	script := before + ":" + cmdline + "\r" +
		`:call writefile([line(".") . " " . col(".")], "` + pos + `")` + "\r" +
		":wq!\r"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(vimPath, "--clean", "-i", "NONE", "--not-a-term", "-s", keys, file)
	cmd.Dir = dir
	out, rerr2 := cmd.CombinedOutput()
	if rerr2 != nil {
		t.Fatalf("vim: %v\n%s", rerr2, tail(out))
	}
	data, rerr := os.ReadFile(file)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var res vimResult
	res.text = strings.TrimSuffix(string(data), "\n")
	if p, err := os.ReadFile(pos); err == nil {
		fields := strings.Fields(string(p))
		if len(fields) == 2 {
			res.line, _ = strconv.Atoi(fields[0])
			res.col, _ = strconv.Atoi(fields[1])
		}
	}
	return res
}

// tail is the last of vim's terminal output, which is where its complaint
// lands when it refuses to start.
func tail(b []byte) string {
	if len(b) > 400 {
		b = b[len(b)-400:]
	}
	return string(b)
}

// TestAgainstVim runs every case through both and compares.
//
// A case that this package does not implement yet is not in the list: the
// point of the table is that everything in it is byte-identical to vim,
// so an addition that is not is a failure and not a known gap.
func TestAgainstVim(t *testing.T) {
	if _, err := os.Stat(vimPath); err != nil {
		t.Skipf("%s is not on this box", vimPath)
	}
	lines := tenLines()
	indented := []string{
		"  alpha", "  bravo", "  charlie", "  delta", "  echo",
		"  foxtrot", "  golf", "  hotel", "  india", "  juliet",
	}
	bars := []string{"a|b", "abc", "a|c"}
	globs := []string{"a1", "a2", "a3", "z4", "z5", "z6", "z7", "z8", "z9"}
	nest := []string{"a1", "a2", "b1"}
	joins := []string{"abcdef", "b2", "a3"}

	for _, tc := range []struct {
		file   []string
		before string
		cmd    string
	}{
		{lines, "", "1,4d"},
		{lines, "", "1,2d"},
		{lines, "", "%d"},
		{lines, "", "0d"},
		{lines, "", "0,2d"},
		{lines, "", "d 3"},
		{lines, "", "$d"},
		{lines, "", "2,4d a"},
		{lines, "5G", "1,3d"},
		{lines, "5G", "1,3y"},
		{lines, "", "1,4m8"},
		{lines, "", "1,3t0"},
		{lines, "", "1,4co8"},
		{lines, "", "1,4j"},
		{lines, "", "2j"},
		{lines, "", "1,5>"},
		{lines, "", "2,3>>"},
		{lines, "", "1,2>3"},
		{lines, "3G", ".,+2d"},
		{lines, "", "2,+3d"},
		{lines, "", "/three/,/five/d"},
		{lines, "", "1;/two/d"},
		{lines, "", "1,/two/d"},
		{lines, "", "$-1,$d"},
		{lines, "", "-1d"},
		{lines, "", ".+d"},
		{lines, "", "1,3!tr a-z A-Z"},
		{lines, "", "%!sort"},
		{lines, "", "1r !echo hello"},
		{lines, "", "%normal A;"},
		{lines, "", "1,3normal x"},
		{indented, "5G", "1,3d"},
		{indented, "5G", "2,3co6"},
		{indented, "5G", "2,3m6"},
		{indented, "1G", "2,4j"},
		{indented, "5G", "$d"},
		{indented, "1G", "8,10d"},
		{indented, "5G", "2,3t0"},
		{indented, "5G", "1,3>"},
		{lines, "", "1,4j!"},
		{lines, "", "2,3<"},
		{lines, "", "1,3>|1,3<"},
		{lines, "", "1y a|3pu a"},
		{lines, "", "1,2y z|$pu z"},
		{lines, "", "2,4d a|0put a"},
		{lines, "", "1,2y|3pu!"},
		{lines, "", "3k a|'a,$d"},
		{lines, "", "3ka|'ad"},
		{lines, "", "1,2m$"},
		{lines, "", "$t0"},
		{lines, "", "2,3t."},
		{lines, "5G", "1,3d|u"},
		{lines, "", "%normal ID"},
		{lines, "", "2,3normal dw"},
		{lines, "", "1,3!cat -n"},
		{lines, "", "$r !echo tail"},
		{lines, "", "0r !echo head"},
		{indented, "", "%>"},
		{indented, "", "%<"},
		{indented, "", "1,10j"},
		// The forms testdata/keys carries, so that the cases the oracle will
		// run once cmd/pvim wires this package in are already covered here.
		{lines, "2G", "t."},
		{lines, "2G", "m+1"},
		{lines, "", "1t$"},
		{lines, "", "1m$"},
		{lines, "", "5m0"},
		{lines, "", "2,3co0"},
		{lines, "", "1j 3"},
		{lines, "", "j"},
		{lines, "", "2>3"},
		{lines, "5G", "-2,.d"},
		{lines, "", "/three/+1d"},
		{lines, "", "/two/;+2d"},
		{lines, "2Gma5Gmb", "'a,'bd"},
		{lines, "", "2d 3"},
		{lines, "", "2y b 3"},
		{lines, "", "$d|$d"},

		// A bar inside a ":s" belongs to the command: do_sub reads the
		// delimiter, the pattern, the replacement and the flags before it
		// looks for one. Splitting at the first bar wrote "|b" into the file.
		{bars, "", "s/a|b/X/"},
		{bars, "2G", "s/a/X|Y/"},
		{bars, "", `%s/\vb|$/-/g`},
		{bars, "", "s/a/X/ | s/b/Y/"},

		// ":normal" over a range is a do-while over line1++ with no
		// line-count test, so it keeps going after the buffer has shrunk past
		// the range and lets check_cursor clamp.
		{[]string{"a", "b", "c", "d"}, "", "%normal dd"},
		{[]string{"x", "\ty z"}, "", "%normal dd"},
		{lines, "", "%normal 2dd"},

		// A ";" that is not the last separator moves the cursor too.
		{lines, "", "3;.,.+1d"},
		{lines, "", "2;+0,+1d"},
		{lines, "", "1y a|1,2;3pu a"},
		{indented, "3G", "1;+1y"},
		{indented, "", "3;.,.+1normal I-"},

		// Two addresses naming one line join nothing at all, and the cursor
		// still moves to line1 because ex_join assigns it first.
		{joins, "", "2,2j"},
		{joins, "", "1,1j"},
		{joins, "", "0,1j"},
		{joins, "3G3|", "2,2j"},
		{joins, "1G6|", "2,2j"},

		// A ":g" moves only the marked lines that a change happened ABOVE, and
		// a nested one tests the current line instead of running a whole pass.
		{globs, "", "g/^a/+3d"},
		{nest, "", "g/a/g/1/d"},
		{nest, "", "g/a/v/1/d"},
		{nest, "", "g/a/%g/1/d"},
		{[]string{"a1", "a2"}, "", "g/^/g/zz/d"},
	} {
		name := tc.before + ":" + tc.cmd
		t.Run(name, func(t *testing.T) {
			want := runVim(t, tc.file, tc.before, tc.cmd)

			h := newHarness(t, tc.file...)
			if tc.before != "" {
				if err := h.ctx.runKeys(tc.before); err != nil {
					t.Fatalf("prologue %q: %v", tc.before, err)
				}
			}
			if err := h.ctx.RunLine(tc.cmd); err != nil {
				t.Fatalf(":%s: %v", tc.cmd, err)
			}
			if got := h.text(); got != want.text {
				t.Errorf("buffer\n got %q\nwant %q", got, want.text)
			}
			if l, c := h.cursor(); l != want.line || c != want.col {
				t.Errorf("cursor = %d,%d, vim leaves %d,%d", l, c, want.line, want.col)
			}
		})
	}
}
