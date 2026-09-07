package diff

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The differential test: every fixture through this package and through the
// vim on the box, with the fillers, the highlight of every column, where ]c
// and [c land and what do and dp move all compared.
//
// This is the oracle for diff mode. Vim has diff mode built in and exposes
// all of it to vimscript -- diff_filler() is
// the filler count above a line and diff_hlID() is the highlight of one cell
// -- so there is no screen scraping and no guessing: the two implementations
// answer the same questions and the answers are compared as numbers.
//
// It skips rather than fails when there is no vim, because a Linux box has no
// /opt/homebrew and the rest of the package's tests are worth running there.

const vimPath = "/opt/homebrew/bin/vim"

// fixture is two files to diff and the 'diffopt' to diff them under.
type fixture struct {
	name string
	opt  string
	a, b []string
}

// The corpus. Every shape the editor draws differently: a changed line, a
// changed block of a different size on each side, an insertion, a deletion,
// a change at the very top and at the very bottom, an empty file against a
// full one, repeated lines where the diff is ambiguous, and the three white
// space flags.
var fixtures = []fixture{
	{"one-changed-line", "internal,filler",
		[]string{"one", "two", "three"},
		[]string{"one", "2", "three"}},
	{"change-and-add", "internal,filler",
		[]string{"a", "b", "c", "d"},
		[]string{"a", "X", "Y", "Z", "d"}},
	{"add-at-end", "internal,filler",
		[]string{"a", "b", "c"},
		[]string{"a", "b", "c", "d", "e"}},
	{"delete-in-middle", "internal,filler",
		[]string{"a", "b", "c", "d"},
		[]string{"a", "d"}},
	{"delete-at-start", "internal,filler",
		[]string{"a", "b", "c"},
		[]string{"c"}},
	{"add-at-start", "internal,filler",
		[]string{"c"},
		[]string{"a", "b", "c"}},
	{"two-changes", "internal,filler",
		[]string{"a", "b", "c", "d", "e", "f", "g", "h"},
		[]string{"a", "B", "c", "d", "e", "F", "g", "h"}},
	{"identical", "internal,filler",
		[]string{"a", "b", "c"},
		[]string{"a", "b", "c"}},
	{"inline-insert", "internal,filler",
		[]string{"hello world", "same"},
		[]string{"hello there world", "same"}},
	{"inline-suffix", "internal,filler",
		[]string{"abcdef"},
		[]string{"abcXYZdef"}},
	{"repeated-run", "internal,filler",
		[]string{"aaa"},
		[]string{"aaaa"}},
	{"empty-line-changed", "internal,filler",
		[]string{"a", "", "c"},
		[]string{"a", "x", "c"}},
	{"blank-inserted", "internal,filler",
		[]string{"a", "c"},
		[]string{"a", "", "c"}},
	{"whole-file-different", "internal,filler",
		[]string{"1", "2", "3"},
		[]string{"9", "8", "7"}},
	{"tabs-and-spaces", "internal,filler",
		[]string{"x\ty", "z"},
		[]string{"x y", "z"}},
	{"iwhite-amount", "internal,filler,iwhite",
		[]string{"x  y", "z"},
		[]string{"x y", "z"}},
	{"iwhite-leading", "internal,filler,iwhite",
		[]string{"x", "y"},
		[]string{"  x", "y"}},
	{"iwhite-trailing", "internal,filler,iwhite",
		[]string{"x ", "y"},
		[]string{"x", "y"}},
	{"iwhiteall-leading", "internal,filler,iwhiteall",
		[]string{"x", "y"},
		[]string{"  x", "y"}},
	{"icase", "internal,filler,icase",
		[]string{"Hello", "b"},
		[]string{"hello", "B"}},
	{"the-vimrc-diffopt", "vertical,filler,iwhite",
		[]string{"func main() {", "\tprintln(1)", "}"},
		[]string{"func main() {", "  println(2)", "\treturn", "}"}},
	{"repeated-lines-ambiguous", "internal,filler",
		[]string{"x", "a", "a", "y"},
		[]string{"x", "a", "a", "a", "y"}},
	{"moved-block", "internal,filler",
		[]string{"p", "q", "r", "s", "t"},
		[]string{"r", "s", "p", "q", "t"}},
	{"vims-default-diffopt", "internal,filler,closeoff,indent-heuristic,inline:char",
		[]string{"a", "  b", "  c", "d", "  b", "  c", "e"},
		[]string{"a", "  b", "  c", "d", "e"}},
	{"indented-block-moved", "internal,filler,indent-heuristic",
		[]string{"func a() {", "\tx()", "}", "", "func b() {", "\ty()", "}"},
		[]string{"func a() {", "\tx()", "}", "", "func mid() {", "\tz()", "}", "", "func b() {", "\ty()", "}"}},
	{"long-common-prefix", "internal,filler",
		[]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"},
		[]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "TEN"}},
}

// TestFillersAndHighlightAgainstVim is the main measurement: for every
// fixture, both sides, every line.
func TestFillersAndHighlightAgainstVim(t *testing.T) {
	skipWithoutVim(t)
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			want := vimDump(t, f)
			p := New(byteLines(f.a), byteLines(f.b), ParseOptions(f.opt))
			for _, s := range []Side{A, B} {
				n := p.Lines(s)
				for lnum := 1; lnum <= n+1; lnum++ {
					if got, w := p.Filler(s, lnum), want.filler[s][lnum]; got != w {
						t.Errorf("side %d line %d: filler %d, vim says %d", s, lnum, got, w)
					}
				}
				for lnum := 1; lnum <= n; lnum++ {
					if got, w := p.marks(s, lnum), want.hl[s][lnum]; got != w {
						t.Errorf("side %d line %d %q: highlight %q, vim says %q",
							s, lnum, string(p.line(s, lnum)), got, w)
					}
				}
			}
		})
	}
}

// marks renders one line's highlight the way the vimscript probe renders it:
// one character per column, "." for none, "C" for DiffChange, "T" for the
// DiffText run inside it and "A" for DiffAdd. An empty line is one column,
// because that is what vim's col() reports and what diff_hlID answers for.
func (p *Pair) marks(s Side, lnum int) string {
	n := max(1, len(p.line(s, lnum)))
	k := p.Kind(s, lnum)
	if k == Same {
		return strings.Repeat(".", n)
	}
	if k == Added {
		return strings.Repeat("A", n)
	}
	out := []byte(strings.Repeat("C", n))
	if lo, hi, ok := p.TextSpan(s, lnum); ok {
		for i := lo; i < hi && i < n; i++ {
			out[i] = 'T'
		}
	}
	return string(out)
}

// TestJumpsAgainstVim runs ]c and [c from every line of every fixture, with
// counts of one and two, and compares the line vim's cursor ends on.
func TestJumpsAgainstVim(t *testing.T) {
	skipWithoutVim(t)
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			p := New(byteLines(f.a), byteLines(f.b), ParseOptions(f.opt))
			for _, s := range []Side{A, B} {
				for lnum := 1; lnum <= p.Lines(s); lnum++ {
					for _, count := range []int{1, 2} {
						for _, fwd := range []bool{true, false} {
							keys := strconv.Itoa(count)
							if fwd {
								keys += "]c"
							} else {
								keys += "[c"
							}
							want := vimJump(t, f, s, lnum, keys)
							var got int
							if fwd {
								got, _ = p.Next(s, lnum, count)
							} else {
								got, _ = p.Prev(s, lnum, count)
							}
							if got != want {
								t.Errorf("side %d from line %d, %s: landed on %d, vim on %d",
									s, lnum, keys, got, want)
							}
						}
					}
				}
			}
		})
	}
}

// TestGetPutAgainstVim runs do and dp from every line of every fixture and
// compares both files afterwards.
func TestGetPutAgainstVim(t *testing.T) {
	skipWithoutVim(t)
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			for _, s := range []Side{A, B} {
				a, b := byteLines(f.a), byteLines(f.b)
				p := New(a, b, ParseOptions(f.opt))
				for lnum := 1; lnum <= p.Lines(s); lnum++ {
					for _, put := range []bool{false, true} {
						keys := "do"
						if put {
							keys = "dp"
						}
						wantA, wantB := vimGetPut(t, f, s, lnum, keys)
						gotA, gotB := apply(t, f, s, lnum, put)
						if strings.Join(gotA, "\n") != strings.Join(wantA, "\n") ||
							strings.Join(gotB, "\n") != strings.Join(wantB, "\n") {
							t.Errorf("side %d line %d %s:\n got a=%q b=%q\nvim a=%q b=%q",
								s, lnum, keys, gotA, gotB, wantA, wantB)
						}
					}
				}
			}
		})
	}
}

// apply is do or dp through this package: find the block under the cursor,
// work out the replacement, and splice it in.
func apply(t *testing.T, f fixture, s Side, lnum int, put bool) ([]string, []string) {
	t.Helper()
	a, b := byteLines(f.a), byteLines(f.b)
	p := New(a, b, ParseOptions(f.opt))
	c, ok := p.BlockAt(s, lnum)
	if !ok {
		return f.a, f.b
	}
	from := s
	if !put {
		from = s.Other()
	}
	dst, src := b, a
	if from == B {
		dst, src = a, b
	}
	first, last, lines := Get(c, from, src)
	out := splice(dst, first, last, lines)
	if from == B {
		return stringLines(out), f.b
	}
	return f.a, stringLines(out)
}

// splice replaces lines first..last of dst with repl. last < first is an
// insertion in front of first.
func splice(dst [][]byte, first, last int, repl [][]byte) [][]byte {
	out := make([][]byte, 0, len(dst)+len(repl))
	out = append(out, dst[:first-1]...)
	out = append(out, repl...)
	if last >= first {
		out = append(out, dst[last:]...)
	} else {
		out = append(out, dst[first-1:]...)
	}
	return out
}

// vimState is what one run of vim reported.
type vimState struct {
	filler [2]map[int]int
	hl     [2]map[int]string
}

// vimDump runs vim -d over the fixture and reads back diff_filler and
// diff_hlID for every line and column of both windows.
func vimDump(t *testing.T, f fixture) vimState {
	t.Helper()
	dir := writeFixture(t, f)
	out := filepath.Join(dir, "dump.txt")
	script := `
func! Dump(out)
  let o = []
  for w in [1,2]
    exe w . 'wincmd w'
    for l in range(1, line('$'))
      let s = ''
      for cc in range(1, max([1, col([l,'$'])-1]))
        let id = diff_hlID(l, cc)
        let nm = id == 0 ? '.' : synIDattr(id, 'name')
        let s .= (nm == 'DiffText' ? 'T' : nm == 'DiffChange' ? 'C' : nm == 'DiffAdd' ? 'A' : nm == 'DiffDelete' ? 'D' : '.')
      endfor
      call add(o, printf('%d %d %d %s', w, l, diff_filler(l), s))
    endfor
    call add(o, printf('%d %d %d -', w, line('$') + 1, diff_filler(line('$') + 1)))
  endfor
  call writefile(o, a:out)
endfunc
`
	runVim(t, dir, f, script, `call Dump("`+out+`")`)
	st := vimState{}
	for i := range st.filler {
		st.filler[i] = map[int]int{}
		st.hl[i] = map[int]string{}
	}
	for _, line := range readLines(t, out) {
		var w, l, fill int
		var marks string
		if _, err := fmt.Sscanf(line, "%d %d %d %s", &w, &l, &fill, &marks); err != nil {
			t.Fatalf("parsing %q: %v", line, err)
		}
		st.filler[w-1][l] = fill
		if marks != "-" {
			st.hl[w-1][l] = marks
		}
	}
	return st
}

// vimJump runs the given jump from the given line and returns the line the
// cursor ended on.
func vimJump(t *testing.T, f fixture, s Side, lnum int, keys string) int {
	t.Helper()
	dir := writeFixture(t, f)
	out := filepath.Join(dir, "j.txt")
	runVim(t, dir, f, "",
		fmt.Sprintf("%dwincmd w", int(s)+1),
		strconv.Itoa(lnum),
		"silent! normal "+keys,
		`call writefile([line(".")], "`+out+`")`)
	lines := readLines(t, out)
	if len(lines) != 1 {
		t.Fatalf("vim wrote %d lines, want 1", len(lines))
	}
	n, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// vimGetPut runs do or dp and returns both files afterwards.
func vimGetPut(t *testing.T, f fixture, s Side, lnum int, keys string) ([]string, []string) {
	t.Helper()
	dir := writeFixture(t, f)
	runVim(t, dir, f, "",
		fmt.Sprintf("%dwincmd w", int(s)+1),
		strconv.Itoa(lnum),
		"silent! normal "+keys,
		"silent! wall")
	return readLines(t, filepath.Join(dir, "a.txt")), readLines(t, filepath.Join(dir, "b.txt"))
}

// runVim starts vim -d on the fixture's two files, sources the script if
// there is one, runs the commands and quits.
func runVim(t *testing.T, dir string, f fixture, script string, cmds ...string) {
	t.Helper()
	args := []string{"-d", "-N", "-u", "NONE", "-i", "NONE", "--not-a-term",
		"-c", "set diffopt=" + f.opt}
	if script != "" {
		path := filepath.Join(dir, "probe.vim")
		if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-c", "source "+path)
	}
	for _, c := range cmds {
		args = append(args, "-c", c)
	}
	args = append(args, "-c", "qa!", "a.txt", "b.txt")
	cmd := exec.Command(vimPath, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vim: %v\n%s", err, out)
	}
}

// writeFixture puts the two files in a fresh directory.
func writeFixture(t *testing.T, f fixture) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pvim-diff")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, lines := range map[string][]string{"a.txt": f.a, "b.txt": f.b} {
		body := ""
		if len(lines) > 0 {
			body = strings.Join(lines, "\n") + "\n"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func byteLines(ss []string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

func stringLines(bs [][]byte) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = string(b)
	}
	return out
}

func skipWithoutVim(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("shells out to vim")
	}
	if _, err := os.Stat(vimPath); err != nil {
		t.Skipf("no vim at %s", vimPath)
	}
}

// TestRandomPairsAgainstVim is the corpus above with the fixtures chosen by a
// generator instead of by hand.
//
// It is what would find a hunk boundary this package and vim disagree about
// on input nobody thought to write down: short lines from a four-letter
// alphabet, so repeated lines are common and the diff is often ambiguous,
// which is exactly where Myers and xdiff can pick different answers that are
// both minimal. Two hundred pairs, a fixed seed, and the whole filler and
// highlight comparison on each.
func TestRandomPairsAgainstVim(t *testing.T) {
	skipWithoutVim(t)
	r := rand.New(rand.NewPCG(7, 11))
	for i := 0; i < 200; i++ {
		a := randLines(r, 1+r.IntN(14))
		b := mutate(r, a)
		f := fixture{
			name: "random-" + strconv.Itoa(i),
			opt:  "internal,filler",
			a:    stringLines(a),
			b:    stringLines(b),
		}
		t.Run(f.name, func(t *testing.T) {
			want := vimDump(t, f)
			p := New(byteLines(f.a), byteLines(f.b), ParseOptions(f.opt))
			for _, s := range []Side{A, B} {
				for lnum := 1; lnum <= p.Lines(s)+1; lnum++ {
					if got, w := p.Filler(s, lnum), want.filler[s][lnum]; got != w {
						t.Errorf("side %d line %d: filler %d, vim says %d\na=%q\nb=%q",
							s, lnum, got, w, f.a, f.b)
					}
				}
				for lnum := 1; lnum <= p.Lines(s); lnum++ {
					if got, w := p.marks(s, lnum), want.hl[s][lnum]; got != w {
						t.Errorf("side %d line %d %q: highlight %q, vim says %q\na=%q\nb=%q",
							s, lnum, string(p.line(s, lnum)), got, w, f.a, f.b)
					}
				}
			}
		})
	}
}
