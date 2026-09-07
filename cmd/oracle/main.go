// Command oracle diffs pvim against the vim installed on this machine.
//
// A case is three files in testdata/keys sharing a base name: NAME.in is the
// buffer before, NAME.keys is the raw keystrokes vim -s consumes, NAME.opts is
// a:set line applied before them. The oracle runs the same script through both
// editors and diffs three artifacts: the file each one wrote, the editor state
// each one reports, and the messages each one printed. Every case runs under
// two option profiles, vanilla and the one generated from the real ~/.vimrc.
//
// # Exit codes
//
//	0 identical, or different only where the register says so
//	4 a difference nobody has registered
//	5 a side produced no artifact at all
//	1 the harness itself failed
//
// A registered difference is a pass. The register is the list of differences that
// have been looked at and accepted, so a run holding nothing worse than those
// has to leave make green; the ~ line and the id on stdout are the whole of the
// signal it needs to give. Five is its own code and not a large four. A candidate that crashed before
// writing anything has to look different from a candidate that put a space in
// the wrong place, or the day pvim stops starting is a day the oracle reports
// as a small diff.
//
// # What the candidate has to do
//
// pvim is run as `pvim --oracle -s KEYS FILE` and is handed byte-identical
// input to the reference. In its working directory it must leave:
//
// - the edited FILE, written by the trailer's:wq!
// - state.txt, one entry per line, tab separated:
// "cursor\tLINE COL VIRTCOL", then "reg R\tTYPE\tCONTENT" for the unnamed
// register, the search register, 0-9 and a-z, then "mark M\tLINE COL" for
// a-z, then "changelist\tVALUE" and "undoseq\tN". CONTENT escapes
// backslash, newline, carriage return and tab and nothing else.
// - msgs.txt, everything the message line said while the case ran.
//
// The trailer that makes vim produce those is vimscript, appended to the keys
// file by the oracle. pvim is not expected to interpret it; --oracle recognises
// the trailer and dumps the same state in the same format natively.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The four codes, plus one for the harness falling over.
const (
	exitSame    = 0
	exitHarness = 1
	exitDiffer  = 4
	exitMissing = 5
)

// outcome is one case under one profile. The order is the precedence order: a
// run's exit code is the worst outcome in it.
type outcome int

const (
	outcomeSame outcome = iota
	outcomeRegistered
	outcomeDiffer
	outcomeMissing
)

// code maps an outcome onto the process exit code. outcomeRegistered is
// deliberately not its own code: the register exists to accept a difference,
// and a run that exits non-zero on one it accepted is a run whose register
// nobody keeps up.
func (o outcome) code() int {
	switch o {
	case outcomeDiffer:
		return exitDiffer
	case outcomeMissing:
		return exitMissing
	default:
		return exitSame
	}
}

// oracle is the configured harness.
type oracle struct {
	ref     runner
	cand    runner
	reg     *register
	vimrc   string
	scratch string
	timeout time.Duration
	keep    bool
	profile string
	out     io.Writer
}

// result is one case under one profile, after both sides have run.
type result struct {
	caseName string
	profile  string
	outcome  outcome
	entry    difference
	diffs    []artifactDiff
	note     string
}

func main() {
	var (
		vimPath   = flag.String("vim", "/opt/homebrew/bin/vim", "reference vim")
		pvimPath  = flag.String("pvim", "", "candidate binary (default bin/pvim, then $PATH)")
		candKind  = flag.String("candidate-kind", "pvim", "command line the candidate wants: pvim or vim")
		casesDir  = flag.String("cases", "testdata/keys", "directory of cases")
		one       = flag.String("case", "", "run only this case")
		profile   = flag.String("profile", "all", "which opts profile: all, vanilla or vimrc")
		vimrcPath = flag.String("vimrc", defaultVimrc, "the vimrc the vimrc profile is generated from")
		scratch   = flag.String("scratch", "testdata/scratch", "where runs happen")
		keep      = flag.Bool("keep", false, "keep the scratch directory of every run, not only the failures")
		timeout   = flag.Duration("timeout", 20*time.Second, "per-run timeout")

		doFuzz  = flag.Bool("fuzz", false, "generate scripts instead of reading cases")
		n       = flag.Int("n", 1000, "fuzz: how many scripts")
		seed    = flag.Uint64("seed", 1, "fuzz: the seed, so a failing run is reproducible")
		corpus  = flag.String("corpus", "testdata/fuzz", "fuzz: corpus directory, built in if it is empty or absent")
		index   = flag.Int("index", -1, "fuzz: run only this script index, which with -seed reproduces one finding")
		promote = flag.String("promote", "testdata/keys", "fuzz: where a minimised failing script is written")
	)
	flag.Parse()

	if err := validProfile(*profile); err != nil {
		fatal(err)
	}

	o := &oracle{
		ref:     runner{name: "vim", bin: *vimPath, kind: kindVim},
		vimrc:   *vimrcPath,
		scratch: *scratch,
		timeout: *timeout,
		keep:    *keep,
		profile: *profile,
		out:     os.Stdout,
	}

	cand, kind, err := candidate(*pvimPath, *candKind)
	if err != nil {
		fatal(err)
	}
	o.cand = runner{name: "pvim", bin: cand, kind: kind}

	o.reg = loadRegister()

	ctx := context.Background()
	if *doFuzz {
		first, count := 0, *n
		if *index >= 0 {
			first, count = *index, 1
		}
		code, err := o.fuzz(ctx, first, count, *seed, *corpus, *promote)
		if err != nil {
			fatal(err)
		}
		os.Exit(code)
	}

	var cases []testCase
	if *one != "" {
		c, err := loadCase(*casesDir, *one)
		if err != nil {
			fatal(err)
		}
		cases = []testCase{c}
	} else if cases, err = loadCases(*casesDir); err != nil {
		fatal(err)
	}

	worst := outcomeSame
	for _, c := range cases {
		results, err := o.runCase(ctx, c)
		if err != nil {
			fatal(err)
		}
		for _, r := range results {
			o.print(r)
			if r.outcome > worst {
				worst = r.outcome
			}
		}
	}
	fmt.Fprintf(o.out, "%d cases, worst outcome %s\n", len(cases), worst)
	os.Exit(worst.code())
}

// candidate resolves the binary under test and the command line it wants.
func candidate(path, kind string) (string, runnerKind, error) {
	k := kindPvim
	switch kind {
	case "pvim":
	case "vim":
		k = kindVim
	default:
		return "", 0, fmt.Errorf("-candidate-kind is pvim or vim, not %q", kind)
	}

	// Always absolute: every case runs with the working directory set to its
	// own scratch dir, so a relative -pvim would resolve against that and not
	// against the tree it was typed in.
	if path != "" {
		if strings.ContainsRune(path, filepath.Separator) {
			abs, err := filepath.Abs(path)
			return abs, k, err
		}
		found, err := exec.LookPath(path)
		if err != nil {
			return "", 0, fmt.Errorf("no candidate: %q is not on $PATH", path)
		}
		return found, k, nil
	}
	if _, err := os.Stat("bin/pvim"); err == nil {
		abs, err := filepath.Abs("bin/pvim")
		return abs, k, err
	}
	found, err := exec.LookPath("pvim")
	if err != nil {
		return "", 0, fmt.Errorf("no candidate: bin/pvim does not exist and pvim is not on $PATH")
	}
	return found, k, nil
}

// runCase runs one case under every profile it is asked for.
func (o *oracle) runCase(ctx context.Context, c testCase) ([]result, error) {
	all, err := profiles(o.vimrc, c.opts)
	if err != nil {
		return nil, err
	}

	var results []result
	for _, p := range all {
		if o.profile != "all" && o.profile != p.name {
			continue
		}
		r, err := o.runOne(ctx, c, p)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	// A profile name that matches nothing has already been refused by
	// validProfile before main gets here. This catches the same shape arriving
	// any other way, because a run that compared nothing and said so is
	// recoverable and one that compared nothing and reported success is not.
	if len(results) == 0 {
		return nil, fmt.Errorf("profile %q matched none of the %d profiles", o.profile, len(all))
	}
	return results, nil
}

// runOne runs both sides of one case under one profile and judges the result.
func (o *oracle) runOne(ctx context.Context, c testCase, p profile) (result, error) {
	r := result{caseName: c.name, profile: p.name}
	keys := script(p.opts, c.keys)
	base := filepath.Join(o.scratch, c.name, p.name)

	ref, err := run(ctx, o.ref, filepath.Join(base, "ref"), c.in, keys, o.timeout)
	if err != nil {
		return r, fmt.Errorf("%s/%s: reference: %w", c.name, p.name, err)
	}
	// The reference exiting non-zero is never the candidate's problem. It means
	// the script ran off the end of the keys file, which is a case that needs
	// fixing and not a difference between two editors.
	if ref.exitCode != 0 {
		return r, fmt.Errorf("%s/%s: reference vim exited %d; the script over-ran the trailer\n%s",
			c.name, p.name, ref.exitCode, tail(ref.terminal))
	}
	if !ref.has() {
		return r, fmt.Errorf("%s/%s: reference vim wrote no %s", c.name, p.name, strings.Join(ref.missing, ", "))
	}

	cand, err := run(ctx, o.cand, filepath.Join(base, "cand"), c.in, keys, o.timeout)
	if err != nil {
		return r, fmt.Errorf("%s/%s: candidate: %w", c.name, p.name, err)
	}

	switch {
	case !cand.has():
		r.outcome = outcomeMissing
		r.note = "pvim wrote no " + strings.Join(cand.missing, ", ")
		if cand.exitCode != 0 {
			r.note += fmt.Sprintf(" (exit %d)", cand.exitCode)
		}
	default:
		r.diffs = compare(ref, cand)
		if cand.exitCode != 0 {
			r.note = fmt.Sprintf("pvim exited %d", cand.exitCode)
		}
		switch {
		case len(r.diffs) == 0 && r.note == "":
			r.outcome = outcomeSame
		default:
			if d, ok := o.reg.lookup(c.name, p.name); ok {
				r.outcome, r.entry = outcomeRegistered, d
			} else {
				r.outcome = outcomeDiffer
			}
		}
	}

	if r.outcome == outcomeSame && !o.keep {
		os.RemoveAll(base)
	}
	return r, nil
}

// print writes one result the way a person reads a test run: a column of
// verdicts, and detail only where it is needed.
func (o *oracle) print(r result) {
	id := r.caseName + "/" + r.profile
	switch r.outcome {
	case outcomeSame:
		fmt.Fprintf(o.out, "ok    %s\n", id)
	case outcomeRegistered:
		fmt.Fprintf(o.out, "~     %s  %s  %s\n", id, r.entry.id, r.entry.what)
	case outcomeMissing:
		fmt.Fprintf(o.out, "MISS  %s  %s; scratch kept under %s\n", id, r.note,
			filepath.Join(o.scratch, r.caseName, r.profile))
	case outcomeDiffer:
		fmt.Fprintf(o.out, "FAIL  %s\n", id)
		var lines []string
		if r.note != "" {
			lines = append(lines, "  "+r.note)
		}
		for _, d := range r.diffs {
			lines = append(lines, d.report()...)
		}
		lines = append(lines, fmt.Sprintf("  not registered; scratch kept under %s",
			filepath.Join(o.scratch, r.caseName, r.profile)))
		for _, l := range cap40(lines) {
			fmt.Fprintln(o.out, l)
		}
	}
}

// String names an outcome for the summary line.
func (o outcome) String() string {
	switch o {
	case outcomeRegistered:
		return "registered"
	case outcomeDiffer:
		return "different"
	case outcomeMissing:
		return "missing"
	default:
		return "identical"
	}
}

// tail renders the end of a side's terminal output for a harness error. It is
// escape codes and redraw, so only the last few hundred bytes are worth having
// and they are printed with the control bytes visible.
func tail(b []byte) string {
	const n = 400
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return "  terminal tail: " + visible(string(b))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "oracle:", err)
	os.Exit(exitHarness)
}
