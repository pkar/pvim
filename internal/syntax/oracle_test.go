package syntax

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The gate: pvim's answer against vim's, position by position.
//
// vim can be asked what it highlighted, headlessly and per byte column, with
// synIDattr(synID(line, col, 1), "name"), and that is the only measurement of
// this package worth having. Everything else -- a span that looks right in a
// terminal, a test asserting the group a keyword gets -- is an opinion about
// what vim does. This runs the real binary over the same file and diffs.
//
// The harness is skipped under -short, like every other test in this tree that
// shells out to vim.

// lines is Source over a []string, which is what the fixtures are.
type lines []string

func (l lines) LineCount() int { return len(l) }
func (l lines) Line(n int) []byte {
	if n < 1 || n > len(l) {
		return nil
	}
	return []byte(l[n-1])
}

// vimDump is what vim says about one file: group name per (line, byte column).
type vimDump map[[2]int]string

// dumpScript is the vimscript the oracle runs. It is written to a file rather
// than passed with -c because -c arguments are limited and this is not.
const dumpScript = `set nomore
syntax enable
filetype on
let s:out = []
for l in range(1, line('$'))
  let s:txt = getline(l)
  let s:c = 1
  while s:c <= max([1, len(s:txt)])
    let s:id = synID(l, s:c, 1)
    call add(s:out, l . "\t" . s:c . "\t" . (s:id == 0 ? '' : synIDattr(s:id, 'name')))
    let s:c += 1
  endwhile
endfor
call writefile(s:out, g:out)
qa!
`

// askVim runs vim over a file and returns what it highlighted.
func askVim(t *testing.T, path string) vimDump {
	t.Helper()
	vim := "/opt/homebrew/bin/vim"
	if _, err := os.Stat(vim); err != nil {
		t.Skipf("no vim at %s", vim)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "dump.vim")
	if err := os.WriteFile(script, []byte(dumpScript), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.tsv")
	cmd := exec.Command(vim, "--clean", "-es", "--not-a-term",
		"-c", "let g:out="+strconv.Quote(out), "-S", script, path)
	cmd.Stdin = strings.NewReader("")
	if err := cmd.Run(); err != nil {
		t.Fatalf("vim: %v", err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading vim's answer: %v", err)
	}
	d := vimDump{}
	for _, row := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		f := strings.SplitN(row, "\t", 3)
		if len(f) < 2 {
			continue
		}
		l, _ := strconv.Atoi(f[0])
		c, _ := strconv.Atoi(f[1])
		name := ""
		if len(f) > 2 {
			name = f[2]
		}
		d[[2]int{l, c}] = name
	}
	return d
}

// ours returns pvim's answer in the same shape.
func ours(t *testing.T, ft string, src []string) vimDump {
	t.Helper()
	s, err := Load(DefaultRuntime(), ft)
	if err != nil {
		t.Fatalf("loading %s: %v", ft, err)
	}
	h := NewHighlighter(s, lines(src))
	d := vimDump{}
	for i, line := range src {
		lnum := i + 1
		for c := 1; c <= max(1, len(line)); c++ {
			d[[2]int{lnum, c}] = ""
		}
		for _, sp := range h.SpansOn(lnum) {
			for b := sp.Start; b < sp.End; b++ {
				d[[2]int{lnum, b + 1}] = s.GroupName(sp.Group)
			}
		}
	}
	return d
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// compare reports the agreement between the two answers.
func compare(t *testing.T, name string, want, got vimDump, src []string, show int) float64 {
	t.Helper()
	agree, total := 0, 0
	shown := 0
	byPair := map[string]int{}
	for k, w := range want {
		total++
		g := got[k]
		if g == w {
			agree++
			continue
		}
		byPair[fmt.Sprintf("vim=%q pvim=%q", w, g)]++
		if shown < show {
			line := ""
			if k[0]-1 < len(src) {
				line = src[k[0]-1]
			}
			t.Logf("  %s:%d:%d %q: vim=%s pvim=%s", name, k[0], k[1], line, w, g)
			shown++
		}
	}
	if total == 0 {
		return 0
	}
	pct := 100 * float64(agree) / float64(total)
	t.Logf("%s: %d/%d positions agree (%.1f%%)", name, agree, total, pct)
	type pair struct {
		s string
		n int
	}
	var ps []pair
	for s, n := range byPair {
		ps = append(ps, pair{s, n})
	}
	for i := range ps {
		for j := i + 1; j < len(ps); j++ {
			if ps[j].n > ps[i].n {
				ps[i], ps[j] = ps[j], ps[i]
			}
		}
	}
	for i, p := range ps {
		if i >= 12 {
			t.Logf("  ... %d more kinds", len(ps)-i)
			break
		}
		t.Logf("  %4d  %s", p.n, p.s)
	}
	return pct
}
