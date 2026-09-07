package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
)

// The grammar. Every list here is keys that edit or move and nothing else: no
// colon, no ZZ, no Q, no q, no @, no bang. A generated script must be unable to
// quit the editor, record a macro over a register the trailer is about to read,
// or start a shell, because all three turn a diff into a mystery.
var (
	fuzzMotions = []string{
		"h", "j", "k", "l", "w", "W", "b", "B", "e", "E", "ge", "gE",
		"0", "^", "$", "gg", "G", "{", "}", "(", ")", "%", "H", "M", "L",
		"+", "-", "_", "|", "``", "n", "N",
	}
	fuzzCharMotions = []string{"f", "t", "F", "T"}
	fuzzTextObjects = []string{
		"iw", "aw", "iW", "aW", "is", "as", "ip", "ap",
		`i"`, `a"`, "i'", "a'", "i(", "a(", "i[", "a[", "i{", "a{", "i<", "a<",
		"it", "at",
	}
	fuzzOperators = []string{"d", "y", "<", ">", "=", "gu", "gU", "g~", "gq"}
	fuzzSimple    = []string{"x", "X", "D", "Y", "~", "J", "gJ", "p", "P", "u", "\x12", ".", "ma", "mb", "'a", "`b"}
	fuzzInserts   = []string{"i", "I", "a", "A", "o", "O"}
	fuzzRegisters = []string{"", `"a`, `"b`, `"z`, `"A`, `"0`, `"1`, `"_`}
	fuzzCounts    = []string{"", "", "", "2", "3", "5", "12"}
	fuzzTargets   = []byte("aeiost .,\"'()[]{}<>\tz")
)

// generator makes keystroke scripts. Everything it does comes out of one PCG
// seeded by the caller, so a failing run is a seed and an index and not a story
// about what happened on Tuesday.
type generator struct{ r *rand.Rand }

// newGenerator seeds a generator from a run seed and a script index, so script
// i of a run is reproducible on its own.
func newGenerator(seed uint64, index int) *generator {
	return &generator{rand.New(rand.NewPCG(seed, uint64(index)))}
}

// pick returns a random element.
func (g *generator) pick(from []string) string { return from[g.r.IntN(len(from))] }

// script returns a script as a list of items. It stays a list until it is
// written out, because the minimiser drops whole keystrokes and a flat byte
// slice has already forgotten where one ended.
func (g *generator) script() [][]byte {
	n := 1 + g.r.IntN(6)
	items := make([][]byte, 0, n)
	for range n {
		items = append(items, g.item())
	}
	return items
}

// item is one keystroke or one indivisible group of them: an operator with its
// motion, an insert command with its burst and its Escape.
func (g *generator) item() []byte {
	switch g.r.IntN(10) {
	case 0, 1, 2:
		return []byte(g.pick(fuzzCounts) + g.motion())
	case 3, 4:
		return []byte(g.pick(fuzzRegisters) + g.pick(fuzzCounts) + g.pick(fuzzOperators) + g.target())
	case 5, 6:
		return g.insert()
	case 7:
		return g.search()
	case 8:
		return []byte(g.pick(fuzzRegisters) + g.pick(fuzzCounts) + g.pick(fuzzSimple))
	default:
		return g.change()
	}
}

// motion is a motion, including the ones that need a character after them.
func (g *generator) motion() string {
	if g.r.IntN(6) == 0 {
		return g.pick(fuzzCharMotions) + string(fuzzTargets[g.r.IntN(len(fuzzTargets))])
	}
	return g.pick(fuzzMotions)
}

// target is what an operator is applied to: a motion, or a text object, which
// is the half of operator-pending mode with all the edge cases in it.
func (g *generator) target() string {
	if g.r.IntN(3) == 0 {
		return g.pick(fuzzTextObjects)
	}
	return g.pick(fuzzCounts) + g.motion()
}

// insert is an insert command, a burst of text, and the Escape that closes it.
// The item always closes itself: a script that ends in insert mode leaves the
// trailer to be typed into the buffer.
func (g *generator) insert() []byte {
	return []byte(g.pick(fuzzCounts) + g.pick(fuzzInserts) + g.burst() + "\x1b")
}

// change is the operators that leave insert mode open, paired with the burst
// that closes them.
func (g *generator) change() []byte {
	reg, count := g.pick(fuzzRegisters), g.pick(fuzzCounts)
	switch g.r.IntN(4) {
	case 0:
		return []byte(reg + count + "s" + g.burst() + "\x1b")
	case 1:
		return []byte(reg + count + "S" + g.burst() + "\x1b")
	case 2:
		return []byte(reg + count + "C" + g.burst() + "\x1b")
	default:
		return []byte(reg + count + "c" + g.target() + g.burst() + "\x1b")
	}
}

// burst is what gets typed in insert mode: printable text, sometimes a tab or a
// newline, sometimes the two insert-mode kills the vimrc's backspace setting
// makes interesting.
func (g *generator) burst() string {
	var b strings.Builder
	for n := g.r.IntN(8); n >= 0; n-- {
		switch g.r.IntN(16) {
		case 0:
			b.WriteByte('\t')
		case 1:
			b.WriteByte('\r')
		case 2:
			b.WriteByte(0x17) // <C-w>, kill word back
		case 3:
			b.WriteByte(0x15) // <C-u>, kill line back
		case 4:
			b.WriteByte(0x08) // backspace, which 'backspace' governs
		default:
			b.WriteByte(byte(0x20 + g.r.IntN(0x5f)))
		}
	}
	return b.String()
}

// search is / or ? with a pattern, or the shorthands over the word under the
// cursor. Failing searches are wanted: E486 and "search hit BOTTOM" are exactly
// the message-line behaviour the third artifact exists to diff.
func (g *generator) search() []byte {
	switch g.r.IntN(4) {
	case 0:
		return []byte("*")
	case 1:
		return []byte("#")
	default:
		dir := "/"
		if g.r.IntN(2) == 0 {
			dir = "?"
		}
		var pat strings.Builder
		for n := g.r.IntN(4); n >= 0; n-- {
			pat.WriteByte("abcdeinorstuz 019"[g.r.IntN(17)])
		}
		return []byte(dir + pat.String() + "\r")
	}
}

// fuzz generates scripts, runs both editors over each one, and stops at the
// first disagreement with the script minimised and written into the case
// directory, which is where it stops being a fuzz finding and becomes a test.
func (o *oracle) fuzz(ctx context.Context, first, n int, seed uint64, corpusDir, promoteDir string) (int, error) {
	files := loadCorpus(corpusDir, o.vimrc)
	if len(files) == 0 {
		return exitHarness, fmt.Errorf("no corpus")
	}
	all, err := profiles(o.vimrc, "")
	if err != nil {
		return exitHarness, err
	}

	overran, stalled, flaky := 0, 0, 0
	for i := first; i < first+n; i++ {
		g := newGenerator(seed, i)
		cf := files[g.r.IntN(len(files))]
		p := all[i%len(all)]
		items := g.script()

		verdict, cand, err := o.reproduces(ctx, cf, p, items)
		if err != nil {
			return exitHarness, fmt.Errorf("seed %d index %d: %w", seed, i, err)
		}
		switch verdict {
		case fuzzOverran:
			overran++
			continue
		case fuzzStalled:
			stalled++
			continue
		case fuzzAgreed:
			continue
		case fuzzMissing:
			// Stop here, and stop loudly. Minimising this would reduce against
			// a condition that holds for every script over every buffer, and
			// what it promoted into testdata/keys would be a case assembled out
			// of a candidate that never ran.
			note := "pvim wrote no " + strings.Join(cand.missing, ", ")
			if cand.exitCode != 0 {
				note += fmt.Sprintf(" (exit %d)", cand.exitCode)
			}
			fmt.Fprintf(o.out, "MISS at seed %d index %d, corpus %s, profile %s: %s\n  %s; not minimised and not promoted\n",
				seed, i, cf.name, p.name, visible(string(bytes.Join(items, nil))), note)
			return exitMissing, nil
		}

		// Run it again before believing it. Two runs of one script through the
		// same vim do not always agree: at seed 1 index 41 they disagreed about
		// a cursor column three times out of three on a loaded machine and zero
		// times out of ten on a quiet one, so something in vim's startup under
		// a pty is timing-sensitive. Whatever it is, minimising a diff like
		// that is worse than dropping it: the predicate the minimiser reduces
		// against holds at random, and what lands in testdata/keys is four
		// keystrokes that pass for anybody who runs them.
		if again, _, err := o.reproduces(ctx, cf, p, items); err != nil {
			return exitHarness, fmt.Errorf("seed %d index %d, confirming: %w", seed, i, err)
		} else if again != fuzzDiffer {
			flaky++
			fmt.Fprintf(o.out, "unrepeatable diff at seed %d index %d, corpus %s, profile %s: %s\n",
				seed, i, cf.name, p.name, visible(string(bytes.Join(items, nil))))
			continue
		}

		fmt.Fprintf(o.out, "diff at seed %d index %d, corpus %s, profile %s: %s\n",
			seed, i, cf.name, p.name, visible(string(bytes.Join(items, nil))))

		items = o.minimiseScript(ctx, cf, p, items)
		cf.data = o.shrinkInput(ctx, cf, p, items)

		name := fmt.Sprintf("fuzz-%d-%d", seed, i)
		c := testCase{name: name, in: cf.data, keys: bytes.Join(items, nil)}
		if err := writeCase(promoteDir, c); err != nil {
			return exitHarness, err
		}
		fmt.Fprintf(o.out, "minimised to %s and written to %s\n",
			visible(string(c.keys)), filepath.Join(promoteDir, name+".keys"))
		return exitDiffer, nil
	}

	// The three counters are reported rather than swallowed. None of them is a
	// finding: they are scripts the reference could not finish, scripts that
	// left it waiting at a prompt, and diffs that did not survive a second run.
	// A run where most of the scripts land in one of those is a run that tested
	// almost nothing, and that has to be visible on the summary line.
	fmt.Fprintf(o.out, "%d scripts over %d corpus files, seed %d, no unregistered diff (%d over-ran the trailer, %d left vim at a prompt, %d disagreed once and not twice)\n",
		n, len(files), seed, overran, stalled, flaky)
	return exitSame, nil
}

// fuzzVerdict is what one generated script proved about the candidate. It is
// the split runOne makes, minus the register (the fuzz half has no case names to
// look anything up in) and plus one verdict for a reference that never came
// back, which only a generated script can produce.
type fuzzVerdict int

const (
	// fuzzAgreed: both editors left the same three artifacts.
	fuzzAgreed fuzzVerdict = iota
	// fuzzOverran: the reference could not finish the script, so there is
	// nothing here for the candidate to be wrong about.
	fuzzOverran
	// fuzzStalled: the reference is still running when the timeout expires.
	// Same class as fuzzOverran and counted separately because it has a
	// different cause: the script left vim at a prompt rather than past the end
	// of its trailer.
	fuzzStalled
	// fuzzDiffer: the candidate wrote everything and one artifact differs.
	// This is the only verdict worth minimising, because it is the only one
	// whose reduction is still about the keystrokes.
	fuzzDiffer
	// fuzzMissing: the candidate produced no artifact at all.
	fuzzMissing
)

// reproduces runs one generated script through both editors and judges the
// candidate. A reference that exits non-zero or writes nothing is a hole in the
// grammar, not a finding, and it is never minimised towards.
//
// The candidate artifacts come back with the verdict so the caller can say what
// was missing without running the script a second time.
func (o *oracle) reproduces(ctx context.Context, cf corpusFile, p profile, items [][]byte) (fuzzVerdict, artifacts, error) {
	keys := script(p.opts, bytes.Join(items, nil))
	base := filepath.Join(o.scratch, "fuzz")
	defer func() {
		if !o.keep {
			os.RemoveAll(base)
		}
	}()

	var cand artifacts
	ref, err := run(ctx, o.ref, filepath.Join(base, "ref"), cf.data, keys, o.timeout)
	var stalled timeoutError
	switch {
	case errors.As(err, &stalled):
		// The script asked vim a question and then ended. 'confirm' is in the
		// vimrc profile, so abandoning a modified buffer becomes "Save changes
		// to ...? [Y]es, (N)o, (C)ancel:", and under a pty with the keys file
		// exhausted vim waits at that prompt for a keystroke nobody will type.
		// There is no reference answer, so there is nothing to diff and nothing
		// to blame the candidate for; stopping the whole run over it would mean
		// one generated script in a few hundred ends every fuzz session.
		return fuzzStalled, cand, nil
	case err != nil:
		return fuzzOverran, cand, err
	}
	if ref.exitCode != 0 || !ref.has() {
		// The generated script ran off the end of its own trailer. That is a
		// hole in the grammar and there is nothing here for the candidate to
		// be wrong about.
		return fuzzOverran, cand, nil
	}
	cand, err = run(ctx, o.cand, filepath.Join(base, "cand"), cf.data, keys, o.timeout)
	if err != nil {
		return fuzzAgreed, cand, err
	}
	if !cand.has() {
		return fuzzMissing, cand, nil
	}
	if cand.exitCode != 0 || len(compare(ref, cand)) > 0 {
		return fuzzDiffer, cand, nil
	}
	return fuzzAgreed, cand, nil
}

// minimiseScript drops keystrokes while the disagreement survives.
//
// Two passes, both greedy from the end. The first drops whole items, which
// takes a six-item script to the one item that matters in a handful of runs.
// The second drops single bytes, which is what turns "3d2w" into "dw". Greedy
// and not delta debugging: the scripts are short by the time the second pass
// starts, and a minimiser nobody waits for is a minimiser nobody runs.
func (o *oracle) minimiseScript(ctx context.Context, cf corpusFile, p profile, items [][]byte) [][]byte {
	for changed := true; changed; {
		changed = false
		for i := len(items) - 1; i >= 0; i-- {
			try := without(items, i)
			if len(try) == 0 {
				continue
			}
			if v, _, err := o.reproduces(ctx, cf, p, try); err == nil && v == fuzzDiffer {
				items, changed = try, true
			}
		}
	}

	flat := bytes.Join(items, nil)
	for changed := true; changed; {
		changed = false
		for i := len(flat) - 1; i >= 0; i-- {
			try := append(append([]byte(nil), flat[:i]...), flat[i+1:]...)
			if len(try) == 0 {
				continue
			}
			if v, _, err := o.reproduces(ctx, cf, p, [][]byte{try}); err == nil && v == fuzzDiffer {
				flat, changed = try, true
			}
		}
	}
	return [][]byte{flat}
}

// shrinkInput cuts the buffer down the same way, by halves and then by lines,
// so that a diff found in a 40,000-line log promotes to a case somebody can
// read. A case whose .in file nobody opens is a case nobody fixes.
func (o *oracle) shrinkInput(ctx context.Context, cf corpusFile, p profile, items [][]byte) []byte {
	lines := bytes.SplitAfter(cf.data, []byte("\n"))
	if len(lines) < 2 {
		return cf.data
	}

	chunk := len(lines) / 2
	for chunk >= 1 {
		for start := 0; start+chunk <= len(lines); {
			try := append(append([][]byte(nil), lines[:start]...), lines[start+chunk:]...)
			probe := cf
			probe.data = bytes.Join(try, nil)
			if len(try) > 0 {
				if v, _, err := o.reproduces(ctx, probe, p, items); err == nil && v == fuzzDiffer {
					lines = try
					continue
				}
			}
			start += chunk
		}
		chunk /= 2
	}
	return bytes.Join(lines, nil)
}

// without returns items with element i removed.
func without(items [][]byte, i int) [][]byte {
	out := make([][]byte, 0, len(items)-1)
	out = append(out, items[:i]...)
	return append(out, items[i+1:]...)
}
