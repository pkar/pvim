package textobj

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/register"
	"github.com/pkar/pvim/internal/text"
)

// This file is the oracle. Every expectation in testdata/vim92.tsv was
// produced by /opt/homebrew/bin/vim 9.2.0321 and not by anyone's memory of
// what vim does, because a text object that reads plausibly and is one byte
// wrong is a bug you find in six months in a file you have already saved.
//
// Regenerate with:
//
//	go test ./internal/textobj/ -run TestVimTable -update
//
// which runs the yank of every object at every cursor position of every
// fixture through vim and rewrites the table. Yank and not delete: op_delete
// has a linewise rule of its own (a multi-line charwise delete whose last line
// is left blank and whose first line is left with only indent turns linewise)
// which belongs to the operator and not to the object, and yanking is the only
// way to see the span the object actually produced.

var update = flag.Bool("update", false, "regenerate testdata/vim92.tsv from /opt/homebrew/bin/vim")

// vimBinary is the oracle. Hard-coded rather than found on $PATH: the answers
// in the table are this build's answers, /usr/bin/vim on macOS is vim 8.2 with
// different quote behaviour, and a table regenerated from the wrong binary
// would look like a passing test.
const vimBinary = "/opt/homebrew/bin/vim"

// fixtures are the files the table is measured over, and each one is chosen
// for the objects it makes hard: words.txt has a blank line and a line of
// trailing white space, brackets.txt has a brace block whose braces are alone
// on their lines, quotes.txt has an escaped quote and an unclosed one, and
// tags.txt has a '>' inside an attribute value.
//
// The last five are each one rule the package got wrong and vim was asked
// about: esc.txt has brackets behind runs of one, two and three backslashes,
// because vim counts the run and takes an even one as a real bracket;
// tagattr.txt has a quoted attribute value that crosses a line break and
// tagnl.txt a '<' that is the last byte of its line; term.txt, bang.txt,
// bangq.txt, termtail.txt, termclose.txt and termblank.txt each end in a
// sentence that is nothing but terminators, the last three of them after real
// prose and with closers and trailing blanks, because findsent() takes no step
// at all there and the step it does not take is what decides whether "dis"
// deletes the last line or nothing; and wide.txt is CJK, kana and an
// emoji, which vim sorts into a class per script so that iw over CJK takes one
// run and not the whole line.
var fixtures = []string{
	"words.txt", "quotes.txt", "brackets.txt", "para.txt", "sent.txt", "tags.txt",
	"edge.txt", "nonl.txt", "esc.txt", "tagattr.txt", "tagnl.txt", "term.txt",
	"wide.txt", "bang.txt", "bangq.txt", "termtail.txt", "termclose.txt",
	"termblank.txt",
}

// objectKeys is every text object, in the order the table lists them.
var objectKeys = []byte{'w', 'W', 's', 'p', '(', ')', 'b', '{', '}', 'B', '[', ']', '<', '>', '"', '\'', '`', 't'}

// counts are the counts each object is measured with. Three is not decoration:
// 3aw is not aw three times, and a count on a bracket object nests outwards
// from the inside and inwards from the outside.
var counts = []int{1, 2, 3}

// keySpec is a text object as it is typed: "3aw", "i(" and so on.
type keySpec struct {
	count int
	inner bool
	key   byte
}

func (k keySpec) String() string {
	s := ""
	if k.count > 1 {
		s = strconv.Itoa(k.count)
	}
	if k.inner {
		return s + "i" + string(rune(k.key))
	}
	return s + "a" + string(rune(k.key))
}

// parseKeySpec reads "3aw" back.
func parseKeySpec(s string) (keySpec, error) {
	k := keySpec{count: 1}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 {
		n, err := strconv.Atoi(s[:i])
		if err != nil {
			return k, err
		}
		k.count = n
	}
	if len(s) != i+2 {
		return k, fmt.Errorf("%q is not a text object", s)
	}
	switch s[i] {
	case 'i':
		k.inner = true
	case 'a':
	default:
		return k, fmt.Errorf("%q starts with neither i nor a", s)
	}
	k.key = s[i+1]
	return k, nil
}

// allKeySpecs is every object at every count, in table order.
func allKeySpecs() []keySpec {
	var out []keySpec
	for _, c := range counts {
		for _, inner := range []bool{true, false} {
			for _, k := range objectKeys {
				out = append(out, keySpec{count: c, inner: inner, key: k})
			}
		}
	}
	return out
}

// outcome is one object's answer, in the form the table stores: "-" for no
// such object, "V first-last" for a linewise one and "c line,col-line,col"
// for a charwise one with a half-open end and zero-based columns.
func outcome(r Result) string {
	if !r.Ok {
		return "-"
	}
	if r.Type == register.TypeLine {
		return fmt.Sprintf("V %d-%d", r.Range.Start.Line, r.Range.End.Line)
	}
	return fmt.Sprintf("c %d,%d-%d,%d",
		r.Range.Start.Line, r.Range.Start.Col, r.Range.End.Line, r.Range.End.Col)
}

// row is one line of the table: an object, a line, a run of columns that all
// answer the same, and that answer.
type row struct {
	fixture  string
	keys     string
	line     int
	colFirst int // one-based, as vim's col() reports
	colLast  int
	out      string
}

// readTable loads testdata/vim92.tsv.
func readTable(t *testing.T) []row {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "vim92.tsv"))
	if err != nil {
		t.Fatalf("opening the table: %v", err)
	}
	defer f.Close()

	var rows []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			t.Fatalf("vim92.tsv:%d: %d fields, want 5", n, len(f))
		}
		var r row
		r.fixture, r.keys, r.out = f[0], f[1], f[4]
		r.line, err = strconv.Atoi(f[2])
		if err != nil {
			t.Fatalf("vim92.tsv:%d: %v", n, err)
		}
		cols := strings.SplitN(f[3], "-", 2)
		r.colFirst, _ = strconv.Atoi(cols[0])
		r.colLast = r.colFirst
		if len(cols) == 2 {
			r.colLast, _ = strconv.Atoi(cols[1])
		}
		rows = append(rows, r)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the table: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the table is empty")
	}
	return rows
}

// buffers loads the fixtures once, keyed by file name.
func buffers(t *testing.T) map[string]*text.Buffer {
	t.Helper()
	bufs := map[string]*text.Buffer{}
	for _, name := range fixtures {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		bufs[name] = text.Read(data)
	}
	return bufs
}

// run asks the package for one object, in the shape the table measured.
func run(b *text.Buffer, line, col int, k keySpec) Result {
	o, ok := ByKey(k.key)
	if !ok {
		return Result{}
	}
	return o.Find(Request{
		Buf:   b,
		At:    text.Pos{Line: line, Col: col - 1},
		Count: k.count,
		Inner: k.inner,
		Arg:   k.key,
		Opt:   DefaultOptions(),
	})
}

// TestVimTable is the differential test: every row of the table, against this
// package.
func TestVimTable(t *testing.T) {
	if *update {
		regenerate(t)
		return
	}
	bufs := buffers(t)
	rows := readTable(t)

	bad, cases := 0, 0
	perObject := map[byte]int{}
	for _, r := range rows {
		k, err := parseKeySpec(r.keys)
		if err != nil {
			t.Fatalf("%s: %v", r.keys, err)
		}
		for col := r.colFirst; col <= r.colLast; col++ {
			cases++
			got := outcome(run(bufs[r.fixture], r.line, col, k))
			if got != r.out {
				bad++
				perObject[k.key]++
				if bad <= 20 {
					t.Errorf("%s %s at %d,%d: got %s, vim says %s",
						r.fixture, r.keys, r.line, col, got, r.out)
				}
			}
		}
	}
	if bad > 0 {
		var summary []string
		for _, k := range objectKeys {
			if n := perObject[k]; n > 0 {
				summary = append(summary, fmt.Sprintf("%s:%d", string(rune(k)), n))
			}
		}
		t.Errorf("%d of %d cases disagree with vim (%s)", bad, cases, strings.Join(summary, " "))
	}
	t.Logf("%d cases", cases)
}

// vimResult is one case as the vim script reports it.
type vimResult struct {
	L  int      `json:"l"`
	C  int      `json:"c"`
	K  string   `json:"k"`
	Ok int      `json:"ok"`
	T  string   `json:"t"`
	S  []int    `json:"s"`
	R  []string `json:"r"`
}

// vimScript yanks every object at every cursor position into register z and
// reports the register's type, its contents and the '[ mark, which is where
// the yank started. The span is start plus the length of the text, which is
// the only encoding that distinguishes an empty object (iw on an empty line,
// which yanks nothing and succeeds) from a one-character one.
const vimScript = `
let s:orig = readfile($FIXTURE)
let s:keys = readfile($KEYS)
silent! %delete _
call setline(1, s:orig)
let s:out = []
for l in range(1, len(s:orig))
  let s:maxc = strlen(s:orig[l-1]) > 0 ? strlen(s:orig[l-1]) : 1
  for c in range(1, s:maxc)
    for k in s:keys
      call cursor(l, c)
      if line('.') != l || col('.') != c
        continue
      endif
      let @z = "SENTINELXX"
      call setpos("'[", [0, 1, 1, 0])
      silent! exe 'normal! "zy' . k
      let ok = (getreg('z') !=# "SENTINELXX") ? 1 : 0
      let rec = {'l': l, 'c': c, 'k': k, 'ok': ok}
      if ok
        let rec['t'] = getregtype('z')
        let rec['s'] = getpos("'[")[1:2]
        let rec['r'] = getreg('z', 1, 1)
      endif
      call add(s:out, json_encode(rec))
    endfor
  endfor
endfor
call writefile(s:out, $OUTFILE)
qa!
`

// regenerate rewrites testdata/vim92.tsv from vim.
func regenerate(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(vimBinary); err != nil {
		t.Fatalf("%s: %v", vimBinary, err)
	}
	dir := t.TempDir()

	script := filepath.Join(dir, "probe.vim")
	if err := os.WriteFile(script, []byte(vimScript), 0o644); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, k := range allKeySpecs() {
		keys = append(keys, k.String())
	}
	keyFile := filepath.Join(dir, "keys")
	if err := os.WriteFile(keyFile, []byte(strings.Join(keys, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# Generated by go test ./internal/textobj/ -run TestVimTable -update\n")
	fmt.Fprintf(&out, "# from %s, one row per run of cursor columns that answer alike.\n", vimVersion(t))
	fmt.Fprintf(&out, "# fixture\tobject\tline\tcolumns (1-based)\tspan: - none, V first-last, c line,col-line,col (0-based, half-open)\n")

	for _, name := range fixtures {
		results := runVim(t, dir, script, filepath.Join("testdata", name), keyFile)
		for _, r := range collapse(name, results) {
			cols := strconv.Itoa(r.colFirst)
			if r.colLast != r.colFirst {
				cols += "-" + strconv.Itoa(r.colLast)
			}
			fmt.Fprintf(&out, "%s\t%s\t%d\t%s\t%s\n", r.fixture, r.keys, r.line, cols, r.out)
		}
	}
	if err := os.WriteFile(filepath.Join("testdata", "vim92.tsv"), []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote testdata/vim92.tsv")
}

// vimVersion is the first line of vim --version, for the table's header.
func vimVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(vimBinary, "--version").Output()
	if err != nil {
		t.Fatalf("vim --version: %v", err)
	}
	first, _, _ := strings.Cut(string(out), "\n")
	patches := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Included patches:") {
			patches = ", " + line
		}
	}
	return strings.TrimSpace(first) + patches
}

// runVim runs one fixture through the script and parses what it wrote.
func runVim(t *testing.T, dir, script, fixture, keyFile string) []vimResult {
	t.Helper()
	outFile := filepath.Join(dir, "out.jsonl")
	os.Remove(outFile)

	cmd := exec.Command(vimBinary, "--clean", "-i", "NONE", "--not-a-term", "-es", "-c", "source "+script)
	cmd.Env = append(os.Environ(), "FIXTURE="+fixture, "KEYS="+keyFile, "OUTFILE="+outFile)
	cmd.Stdin = strings.NewReader("")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vim on %s: %v\n%s", fixture, err, out)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("vim wrote nothing for %s: %v", fixture, err)
	}
	var results []vimResult
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		var r vimResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("parsing %q: %v", line, err)
		}
		results = append(results, r)
	}
	return results
}

// vimOutcome encodes one of vim's answers the way outcome encodes ours.
func vimOutcome(r vimResult) string {
	if r.Ok == 0 {
		return "-"
	}
	if strings.HasPrefix(r.T, "V") {
		return fmt.Sprintf("V %d-%d", r.S[0], r.S[0]+len(r.R)-1)
	}
	line, col := r.S[0], r.S[1]-1
	endLine, endCol := line, col+len(r.R[0])
	if len(r.R) > 1 {
		endLine = line + len(r.R) - 1
		endCol = len(r.R[len(r.R)-1])
	}
	return fmt.Sprintf("c %d,%d-%d,%d", line, col, endLine, endCol)
}

// collapse turns vim's per-column answers into one row per run of columns that
// answer alike. Every case is still in the table; a run of five columns that
// all select the same word is one line of it instead of five, which is what
// makes a table of 50,000 cases something a person can read.
func collapse(fixture string, results []vimResult) []row {
	byKey := map[string][]vimResult{}
	var order []string
	for _, r := range results {
		if _, seen := byKey[r.K]; !seen {
			order = append(order, r.K)
		}
		byKey[r.K] = append(byKey[r.K], r)
	}
	var rows []row
	for _, key := range order {
		list := byKey[key]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].L != list[j].L {
				return list[i].L < list[j].L
			}
			return list[i].C < list[j].C
		})
		var cur *row
		for _, r := range list {
			out := vimOutcome(r)
			if cur != nil && cur.line == r.L && cur.colLast == r.C-1 && cur.out == out {
				cur.colLast = r.C
				continue
			}
			rows = append(rows, row{fixture: fixture, keys: key, line: r.L, colFirst: r.C, colLast: r.C, out: out})
			cur = &rows[len(rows)-1]
		}
	}
	return rows
}
