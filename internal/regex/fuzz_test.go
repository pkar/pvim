package regex

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// vimBinary is the oracle. It is the same build the table was generated from
// and the same one the editor is measured against everywhere else in this
// repository.
const vimBinary = "/opt/homebrew/bin/vim"

// fuzzCases is how many patterns the differential fuzz generates. It is the
// number the gate asks for, and it is the default rather than something
// PVIM_FUZZ_N has to be set to reach: a gate only somebody who knows the
// environment variable can run is not a gate. It costs 3 seconds of the
// repository's 24, because all hundred thousand go through one vim process and
// the generator is the slow half.
const fuzzCases = 100000

// fuzzSeed is fixed so that a failure can be reproduced by running the test
// again. PVIM_FUZZ_SEED moves it, which is how a long hunt covers new ground.
const fuzzSeed = 20260902

// TestFuzzTranslator generates patterns from a grammar of the atoms the
// translator claims, runs each through both vim and the translator, and reports
// every disagreement.
//
// The table pins the behaviours somebody thought of. This is for the ones
// nobody did: `\v` around a collection with a range in it, a lazy multi on a
// group inside an alternation, a `$` that turns out to be in the middle of the
// pattern after all. Both halves see the same pattern and the same input and
// the only thing being compared is where the match started and what it was.
func TestFuzzTranslator(t *testing.T) {
	if testing.Short() {
		t.Skip("the differential fuzz needs vim")
	}
	if _, err := os.Stat(vimBinary); err != nil {
		t.Skipf("no oracle at %s", vimBinary)
	}

	n := envInt(t, "PVIM_FUZZ_N", fuzzCases)
	seed := envInt(t, "PVIM_FUZZ_SEED", fuzzSeed)
	rng := rand.New(rand.NewSource(int64(seed)))

	type fcase struct{ pat, in string }
	cases := make([]fcase, 0, n)
	for len(cases) < n {
		g := &patternGen{rng: rng, magic: Magic(rng.Intn(4))}
		body, _ := g.pattern(0)
		pat := g.prefix() + body
		cases = append(cases, fcase{pat, fuzzInput(rng)})
	}

	pairs := make([][2]string, len(cases))
	for i, c := range cases {
		pairs[i] = [2]string{c.pat, c.in}
	}
	answers, err := askVim(t, pairs)
	if err != nil {
		t.Fatalf("asking vim: %v", err)
	}

	opt := Options{LastSubstitute: "bc"}

	// suspects are the cases where pvim and vim's default engine disagreed.
	// They get a second hearing below rather than being reported straight away.
	type suspect struct {
		i        int
		pat, in  string
		vimStart int
		vimMatch string
		ourStart int
		ourMatch string
		source   string
	}
	var suspects []suspect
	var skipped int

	for i, c := range cases {
		a := answers[i]
		if a.err != "" {
			// A pattern vim will not run says nothing about the translator.
			skipped++
			continue
		}
		re, err := Compile(c.pat, opt)
		if err != nil {
			t.Errorf("case %d: vim ran %q over %q and pvim would not compile it: %v", i, c.pat, c.in, err)
			continue
		}
		start, match := -1, ""
		if loc := re.FindStringIndex(c.in); loc != nil {
			start, match = loc[0], c.in[loc[0]:loc[1]]
		}
		if start != a.start || match != a.match {
			suspects = append(suspects, suspect{i, c.pat, c.in, a.start, a.match, start, match, re.Source()})
		}
	}

	// Vim ships two regexp engines and they do not agree with each other.
	// \%#=1 is the old backtracking one, which is Perl's semantics and so Go's;
	// \%#=2 is the NFA, which is the default and which on a handful of patterns
	// with an empty alternative in them returns a longer match than a
	// backtracking search would. Where pvim agrees with vim's own backtracking
	// engine and only the NFA is the odd one out, that is a difference between
	// vim and vim, and TestKnownGaps carries the example. Where both vim
	// engines agree and pvim does not, it is a bug here.
	retry := make([][2]string, len(suspects))
	for i, s := range suspects {
		retry[i] = [2]string{`\%#=1` + s.pat, s.in}
	}
	var engineSplits, bugs int
	if len(retry) > 0 {
		old, err := askVim(t, retry)
		if err != nil {
			t.Fatalf("asking vim's backtracking engine: %v", err)
		}
		for i, s := range suspects {
			if old[i].err == "" && old[i].start == s.ourStart && old[i].match == s.ourMatch {
				engineSplits++
				if engineSplits <= 3 {
					t.Logf("case %d: vim's two engines disagree about %q over %q: NFA says start %d match %q, backtracking and pvim say start %d match %q",
						s.i, s.pat, s.in, s.vimStart, s.vimMatch, s.ourStart, s.ourMatch)
				}
				continue
			}
			bugs++
			if bugs <= 20 {
				t.Errorf("case %d: %q over %q\n vim: start %d match %q\npvim: start %d match %q\nsource %s",
					s.i, s.pat, s.in, s.vimStart, s.vimMatch, s.ourStart, s.ourMatch, s.source)
			}
		}
	}
	if bugs > 20 {
		t.Errorf("%d disagreements in total; the first twenty are above", bugs)
	}
	// A handful of engine splits is the state of vim. A flood of them is this
	// package having broken something and hidden it behind the excuse.
	if limit := len(cases)/200 + 5; engineSplits > limit {
		t.Errorf("%d of %d cases came down to vim's two engines disagreeing, which is more than the %d that is normal",
			engineSplits, len(cases), limit)
	}
	t.Logf("%d patterns, %d disagreements, %d vim engine splits, %d skipped because vim rejected the pattern",
		len(cases), bugs, engineSplits, skipped)
}

// envInt reads an integer out of the environment, or gives the default.
func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%s=%q is not a number", name, s)
	}
	return n
}

// fuzzAlphabet is what the inputs are built from: the boundaries of every
// character class, both cases, a tab, and three code points above ASCII that
// the classes disagree about. No line break, because vim's match() over a
// String reads one as an ordinary character and a buffer does not, and a
// difference of opinion about the oracle is not a difference in the translator.
var fuzzAlphabet = []rune{'a', 'b', 'A', 'B', '0', '9', '_', ' ', '\t', '.', '/', '-', '#', '~', '|', '(', ')', '[', ']', '*', 'é', '中', 'Ā'}

// fuzzInput builds one subject string.
func fuzzInput(rng *rand.Rand) string {
	var b strings.Builder
	for n := rng.Intn(9); n > 0; n-- {
		b.WriteRune(fuzzAlphabet[rng.Intn(len(fuzzAlphabet))])
	}
	return b.String()
}

// patternGen writes random vim patterns at one magic level.
//
// The level is fixed for the whole pattern rather than switched mid-way,
// because a generator that switches has to track the level to spell the next
// atom and would then be a second copy of the translator's own table, agreeing
// with it by construction and testing nothing.
type patternGen struct {
	rng   *rand.Rand
	magic Magic
}

// Every production reports whether what it wrote can match the empty string,
// and piece uses that to never put a multi on something nullable.
//
// That is not tidiness. It is the one place Go's regexp and a backtracking
// search genuinely part company: over "ab", `\%(a\=\)*` matches "a" in both of
// vim's engines and in Perl, and the empty string in Go, because RE2 takes zero
// iterations of a loop whose body can match nothing where a backtracker runs it
// once and lets the body be greedy. TestKnownGaps carries the example, and a
// fuzz that keeps generating the shape reports it a few times per hundred
// thousand patterns and buries everything else.

// pattern is a list of alternatives. It is nullable when any branch is.
func (g *patternGen) pattern(depth int) (string, bool) {
	part, nullable := g.concat(depth)
	parts := []string{part}
	for g.rng.Intn(4) == 0 {
		p, n := g.concat(depth)
		parts = append(parts, p)
		nullable = nullable || n
	}
	return strings.Join(parts, g.spell(`|`)), nullable
}

// concat is a run of pieces. It is nullable when every piece is.
func (g *patternGen) concat(depth int) (string, bool) {
	var b strings.Builder
	nullable := true
	if g.rng.Intn(8) == 0 {
		b.WriteString(g.spellCaret())
	}
	for n := 1 + g.rng.Intn(3); n > 0; n-- {
		p, pn := g.piece(depth)
		b.WriteString(p)
		nullable = nullable && pn
	}
	if g.rng.Intn(8) == 0 {
		b.WriteString(g.spellDollar())
	}
	return b.String(), nullable
}

// piece is an atom and at most one multi, and a multi only ever goes on an atom
// that has to match something.
func (g *patternGen) piece(depth int) (string, bool) {
	a, nullable := g.atom(depth)
	if nullable || g.rng.Intn(3) != 0 {
		return a, nullable
	}
	m, optional := g.multi()
	return a + m, optional
}

// atomLiterals are the characters an atom can be. Punctuation is in here on
// purpose: half of what the magic table decides is whether one of these is an
// operator.
var atomLiterals = []string{"a", "b", "A", "0", "_", " ", "-", "/", "#", "é", "中"}

func (g *patternGen) atom(depth int) (string, bool) {
	// \< and \> are not in here. Both become Go's \b, which is one symmetric
	// boundary where vim has two directional ones, so a generator that emits
	// them would report the same known difference a few hundred times and hide
	// everything else. TestKnownGaps pins that difference instead.
	//
	// Groups get rarer with depth so that a pattern terminates.
	kinds := 9
	if depth < 2 {
		kinds = 11
	}
	switch g.rng.Intn(kinds) {
	case 0:
		return atomLiterals[g.rng.Intn(len(atomLiterals))], false
	case 1:
		return `\` + string(classAtomLetters[g.rng.Intn(len(classAtomLetters))]), false
	case 2:
		return g.spell(`.`), false
	case 3:
		return g.collection(), false
	case 4, 5:
		return g.escape(), false
	case 6:
		return g.numeric(), false
	case 7, 8:
		return `\_` + string(classAtomLetters[g.rng.Intn(len(classAtomLetters))]), false
	case 9:
		inner, nullable := g.pattern(depth + 1)
		return g.spell(`(`) + inner + g.spell(`)`), nullable
	default:
		inner, nullable := g.pattern(depth + 1)
		return g.spell(`%(`) + inner + g.spell(`)`), nullable
	}
}

// classAtomLetters is every character class letter, which is the one part of
// the dialect that is spelled the same at every magic level.
const classAtomLetters = "sSdDxXoOwWhHaAlLuUiIkKfFpP"

// escape is one of the five character escapes. \n is left out: it matches a
// line break, and the inputs deliberately have none.
func (g *patternGen) escape() string {
	return []string{`\t`, `\e`, `\r`, `\b`}[g.rng.Intn(4)]
}

// numeric is one of the \%d, \%x, \%o, \%u forms naming a character.
func (g *patternGen) numeric() string {
	switch g.rng.Intn(4) {
	case 0:
		return g.spell(`%`) + "d" + strconv.Itoa(32+g.rng.Intn(90))
	case 1:
		return g.spell(`%`) + "x" + fmt.Sprintf("%02x", 32+g.rng.Intn(90))
	case 2:
		return g.spell(`%`) + "o" + strconv.FormatInt(int64(32+g.rng.Intn(90)), 8)
	default:
		return g.spell(`%`) + "u" + fmt.Sprintf("%04x", 32+g.rng.Intn(90))
	}
}

// collLiterals are the single characters a generated collection is built from.
// No dash: a dash in range position immediately before a "[:" trips a parser
// bug in vim 9.2 that TestKnownGaps records, and a fuzz that keeps rediscovering
// it reports nothing else.
var collLiterals = []string{"a", "b", "A", "0", "_", " ", "/", "#", "é", "中"}

// collection builds a [] with ranges, negation and the odd POSIX class.
func (g *patternGen) collection() string {
	var b strings.Builder
	b.WriteString(g.spell(`[`))
	if g.rng.Intn(3) == 0 {
		b.WriteString("^")
	}
	if g.rng.Intn(4) == 0 {
		// A dash is only ever generated first, which is the position vim reads
		// as a literal at every magic level and the way anyone writes it.
		b.WriteString("-")
	}
	for n := 1 + g.rng.Intn(3); n > 0; n-- {
		switch g.rng.Intn(6) {
		case 0:
			b.WriteString("a-c")
		case 1:
			b.WriteString("0-9")
		case 2:
			b.WriteString("[:alpha:]")
		case 3:
			b.WriteString("[:digit:]")
		case 4:
			b.WriteString(`\t`)
		default:
			b.WriteString(collLiterals[g.rng.Intn(len(collLiterals))])
		}
	}
	b.WriteString("]")
	return b.String()
}

// multi is one of the repetition forms, in the spelling this magic level uses.
// The bool says the form allows zero repetitions, which is what makes the piece
// it is attached to nullable.
func (g *patternGen) multi() (string, bool) {
	lo, hi := g.rng.Intn(3), g.rng.Intn(3)
	if hi < lo {
		lo, hi = hi, lo
	}
	switch g.rng.Intn(9) {
	case 0:
		return "*", true
	case 1:
		return g.spell(`+`), false
	case 2:
		return g.spell(`?`), true
	case 3:
		return g.spell(`=`), true
	case 4:
		return g.spell(`{`) + strconv.Itoa(lo) + "}", lo == 0
	case 5:
		return g.spell(`{`) + strconv.Itoa(lo) + ",}", lo == 0
	case 6:
		return g.spell(`{`) + strconv.Itoa(lo) + "," + strconv.Itoa(hi) + "}", lo == 0
	case 7:
		return g.spell(`{`) + "-}", true
	default:
		return g.spell(`{`) + "-" + strconv.Itoa(lo) + "," + strconv.Itoa(hi) + "}", lo == 0
	}
}

// spell writes an operator at this generator's magic level: bare where the
// level makes the bare form the operator, backslashed where it does not.
func (g *patternGen) spell(op string) string {
	if bareIsOperator(op[0], g.magic) {
		return op
	}
	return `\` + op
}

// spellCaret and spellDollar exist because the anchors are the two operators
// whose spelling is not the whole story: \V wants them backslashed, and every
// other level wants them bare.
func (g *patternGen) spellCaret() string  { return g.spell(`^`) }
func (g *patternGen) spellDollar() string { return g.spell(`$`) }

// prefix is the atom that puts a pattern into this generator's magic level.
func (g *patternGen) prefix() string {
	if g.magic == MagicOn {
		return ""
	}
	return g.magic.String()
}

// vimAnswer is what vim said about one pattern and input.
type vimAnswer struct {
	start int
	match string
	err   string
}

// askVim runs every pattern and input through one vim process.
//
// This is a second copy of the runner in testdata/regex/extract, and it has to
// be: the go tool will not let anything import a package under testdata, and
// moving the runner into internal/regex would mean the editor shipped code
// whose only job is to start vim.
func askVim(t *testing.T, pairs [][2]string) ([]vimAnswer, error) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "cases.vim")
	outFile := filepath.Join(dir, "out.tsv")

	var b strings.Builder
	b.WriteString(fuzzOracleScript)
	b.WriteString("let s:out = " + vimQuoteTest(outFile) + "\n")
	b.WriteString("let s:cases = [\n")
	for _, p := range pairs {
		b.WriteString("  \\ [" + vimQuoteTest(p[0]) + ", " + vimQuoteTest(p[1]) + "],\n")
	}
	b.WriteString("  \\ ]\n")
	b.WriteString("call s:Run(s:cases, s:out)\n")
	if err := os.WriteFile(script, []byte(b.String()), 0o644); err != nil {
		return nil, err
	}

	cmd := exec.Command(vimBinary, "--clean", "-es", "--not-a-term", "-S", script, "-c", "qa!")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	f, err := os.Open(outFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []vimAnswer
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		col := strings.Split(sc.Text(), "\t")
		if len(col) != 3 {
			return nil, fmt.Errorf("vim wrote %q", sc.Text())
		}
		start, err := strconv.Atoi(col[0])
		if err != nil {
			return nil, fmt.Errorf("vim wrote a bad offset %q", col[0])
		}
		out = append(out, vimAnswer{start: start, match: unescapeTable(col[1]), err: unescapeTable(col[2])})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(pairs) {
		return nil, fmt.Errorf("asked %d questions and got %d answers", len(pairs), len(out))
	}
	return out, nil
}

const fuzzOracleScript = `
set noignorecase nosmartcase magic
new
call setline(1, 'xxx')
silent! %s/x/bc/
func! s:Enc(s)
  let r = substitute(a:s, '\\', '\\\\', 'g')
  let r = substitute(r, "\t", '\\t', 'g')
  let r = substitute(r, "\n", '\\n', 'g')
  let r = substitute(r, "\r", '\\r', 'g')
  let r = substitute(r, "\e", '\\e', 'g')
  return r
endfunc
func! s:Run(cases, out)
  let rows = []
  for [pat, txt] in a:cases
    let start = -1
    let mtext = ''
    let ex = ''
    try
      let start = match(txt, pat)
      let mtext = matchstr(txt, pat)
    catch
      let start = -1
      let mtext = ''
      let ex = v:exception
    endtry
    call add(rows, join([start, s:Enc(mtext), s:Enc(ex)], "\t"))
  endfor
  call writefile(rows, a:out)
endfunc
`

// vimQuoteTest writes a Go string as a vimscript double-quoted string, in pure
// ASCII so that the generated script cannot be broken by a locale.
func vimQuoteTest(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == 0x1b:
			b.WriteString(`\e`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
