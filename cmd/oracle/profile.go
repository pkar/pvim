package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// profile is one .opts line, applied with :set before a case's keystrokes run.
type profile struct {
	name string
	opts string
}

// vanilla is --clean and nothing else: whatever the case's own .opts file says
// and no more.
const vanillaProfile = "vanilla"

// vimrcProfileName is the second profile every case runs under.
const vimrcProfileName = "vimrc"

// defaultVimrc is the config this editor exists to run. It is read at run time
// and never copied into this repository: a profile transcribed by hand goes
// stale the first time a line in the real file changes, and then the oracle is
// certifying an editor against a config nobody has.
var defaultVimrc = filepath.Join(os.Getenv("HOME"), ".vimrc")

// hasTrue is what has() answers inside the vimrc, and it is the same list
// internal/vimrc will answer, so the profile the oracle builds is the option
// set pvim's own loader will end up with. Everything not named here is false.
var hasTrue = map[string]bool{
	"gui_running":     true,
	"mouse":           true,
	"clipboard":       true,
	"persistent_undo": true,
	"autocmd":         true,
	"conceal":         true,
}

// optsSkip names options dropped from the generated profile, with the reason
// each one is dropped. Every entry is a side effect outside the scratch
// directory, not a behaviour the oracle would rather not test.
//
// Keys are option names and the abbreviations :set accepts for them, because a
// case's own .opts line is written by hand and may spell an option either way,
// and escapingOpt refuses that line by the same table.
var optsSkip = map[string]string{
	// Both write an undo file next to the edit. $HOME already points into the
	// scratch directory so nothing escapes, but the write fails there (vim does
	// not create undodir, and the real ~/.cache/vim does not exist either), and
	// a failure message on every single run is noise in front of the messages
	// the case is actually about.
	"undofile": "writes an undo file per run",
	"udf":      "writes an undo file per run",
	"undodir":  "writes an undo file per run",
	"udir":     "writes an undo file per run",

	// The vimrc's clipboard=unnamed,unnamedplus,autoselect makes "* and "+ the
	// live macOS pasteboard, and the vim on this machine is built +clipboard.
	// Under that profile a yank overwrites whatever the person had copied and a
	// put reads whatever is on the board at that instant, so the run is not
	// reproducible: a second oracle, a clipboard manager or a cmd-C in another
	// window changes the answer between one run and the next, and a case as
	// ordinary as yy j P starts failing at random. There is no way to point vim
	// at a private board -- the pasteboard is one per-login singleton, which is
	// the one thing $HOME in the scratch directory cannot contain -- so the
	// option comes out and the whole vimrc profile stops touching it.
	//
	// The cost is real and it is named here rather than hidden: the vimrc line
	// this drops is a line pvim has to get right, and under this profile
	// nothing checks that it does. internal/mode's own tests cover the register
	// redirection, and the alternative is a gate that reports a different
	// answer depending on what somebody copied a minute ago, which certifies
	// nothing.
	"clipboard": "reads and writes the machine-wide macOS pasteboard",
	"cb":        "reads and writes the machine-wide macOS pasteboard",
}

// optName is the option a :set token names, with everything :set allows around
// it taken off: the no and inv prefixes and the ? and ! suffixes of a boolean,
// and the +, - and ^ of an append, remove or prepend.
func optName(tok string) string {
	name, _, isSet := strings.Cut(tok, "=")
	if !isSet {
		name = strings.TrimSuffix(strings.TrimSuffix(tok, "?"), "!")
		return strings.TrimPrefix(strings.TrimPrefix(name, "no"), "inv")
	}
	name = strings.TrimSuffix(strings.TrimSuffix(name, "+"), "-")
	return strings.TrimSuffix(name, "^")
}

// escapingOpt finds the first token of a:set line that names an option in
// optsSkip, which is to say an option whose effect leaves the scratch
// directory.
//
// vimrcProfile drops those tokens as it reads them, but a case's .opts line is
// copied into the script as it stands. A case that set 'clipboard' would put
// the machine pasteboard back in the middle of the run, and the run after it
// would be judged against whatever the run before had copied. The harness
// refuses such a case instead: a diff whose real cause is another window is a
// day of looking in the wrong place.
func escapingOpt(opts string) (tok, reason string, found bool) {
	for _, t := range strings.Fields(opts) {
		if r, ok := optsSkip[optName(t)]; ok {
			return t, r, true
		}
	}
	return "", "", false
}

// vimrcProfile collapses the :set lines of a real vimrc onto one :set line.
//
// It is a line reader and not a vimscript interpreter, and it needs to
// understand exactly three things beyond "set": if, else and endif. Without
// them the file's own last word on an option is the wrong one. ~/.vimrc says
//
//	set scrolloff=3
//	if !&scrolloff
//	 set scrolloff=1
//	endif
//
// and a reader that takes every set line in order ends at scrolloff=1, which is
// the opposite of the value this profile has to exercise.
func vimrcProfile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// live[i] is whether the lines at nesting depth i are taken. depth 0 is the
	// file itself and is always taken.
	live := []bool{true}
	// seen tracks the value of each option the file has set so far, which is
	// all the state `if !&option` needs.
	seen := map[string]string{}
	var out []string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		switch {
		case strings.HasPrefix(line, "if "):
			cond := evalCond(strings.TrimSpace(line[len("if "):]), seen)
			live = append(live, live[len(live)-1] && cond)
			continue
		case line == "else":
			// An else is taken when its if was not, and only if the block
			// around the whole thing is live.
			outer := live[len(live)-2]
			live[len(live)-1] = outer && !live[len(live)-1]
			continue
		case line == "endif":
			if len(live) > 1 {
				live = live[:len(live)-1]
			}
			continue
		}
		if !live[len(live)-1] {
			continue
		}

		body, ok := strings.CutPrefix(line, "set ")
		if !ok {
			continue
		}
		body = stripComment(body)
		for _, tok := range strings.Fields(body) {
			name := optName(tok)
			if _, value, isSet := strings.Cut(tok, "="); isSet {
				seen[name] = value
			}
			if _, skip := optsSkip[name]; skip {
				continue
			}
			out = append(out, tok)
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if len(out) == 0 {
		return "", fmt.Errorf("%s: no set lines found", path)
	}
	return strings.Join(out, " "), nil
}

// evalCond answers the guards this vimrc uses and refuses to guess at anything
// else. An unrecognised condition is false, which drops the block: a set line
// that should have been in the profile and is not shows up as a missing option
// in the profile test, and a set line that should not be in it and is shows up
// as a diff in every case.
func evalCond(cond string, seen map[string]string) bool {
	neg := false
	for strings.HasPrefix(cond, "!") {
		neg = !neg
		cond = strings.TrimSpace(cond[1:])
	}

	var v bool
	switch {
	case strings.HasPrefix(cond, "has(") && strings.HasSuffix(cond, ")") && bare(cond[len("has("):len(cond)-1]):
		v = hasTrue[strings.Trim(cond[len("has("):len(cond)-1], `"'`)]
	case strings.HasPrefix(cond, "&") && bare(cond[1:]):
		// &option in a boolean position: zero and empty are false.
		val := seen[cond[1:]]
		v = val != "" && val != "0"
	default:
		v = false
	}
	return v != neg
}

// bare reports whether s is a single quoted word or a single word, with no
// operator hiding in it. `if has('clipboard') && !has('gui_running')` ends in a
// close paren like a plain has() does, and without this check it would be read
// as has() of a nonsense feature name, which happens to give the right answer
// for the wrong reason and would stop doing so the day somebody writes
// `if has('a') || has('b')`.
func bare(s string) bool {
	s = strings.Trim(s, `"'`)
	if s == "" {
		return false
	}
	for i := range len(s) {
		c := s[i]
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
		if !letter {
			return false
		}
	}
	return true
}

// stripComment cuts a trailing vimscript comment off a set line.
//
// A double quote only starts a comment after whitespace, which is what keeps
// `set noerrorbells visualbell t_vb= " Disable ALL bells"` from losing t_vb and
// what keeps an option value containing a quote intact. Cutting at the last
// quote instead of the first leaves the comment text in the profile, and vim
// then reads "Disable" as an option name.
func stripComment(s string) string {
	if i := strings.Index(s, ` "`); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	if strings.HasPrefix(s, `"`) {
		return ""
	}
	return strings.TrimSpace(s)
}

// profiles returns the two profiles every case runs under, with the case's own
// .opts line appended to each so a case can still ask for something specific.
func profiles(vimrcPath, caseOpts string) ([]profile, error) {
	if tok, reason, found := escapingOpt(caseOpts); found {
		return nil, fmt.Errorf("case opts set %s, which %s; a run has to depend on nothing but its own scratch directory", tok, reason)
	}
	vimrcOpts, err := vimrcProfile(vimrcPath)
	if err != nil {
		return nil, err
	}
	return []profile{
		{name: vanillaProfile, opts: strings.TrimSpace(caseOpts)},
		{name: vimrcProfileName, opts: strings.TrimSpace(vimrcOpts + " " + caseOpts)},
	}, nil
}

// validProfile refuses a -profile value that names no profile.
//
// The flag's only use is a skip in runCase, so a name nothing matches skips
// every profile, runs zero comparisons and lets the summary line report success
// over a candidate nobody looked at. -candidate-kind already refuses a value it
// does not know; a differential harness that reports a pass without running is
// worse than one that will not start.
func validProfile(name string) error {
	switch name {
	case "all", vanillaProfile, vimrcProfileName:
		return nil
	}
	return fmt.Errorf("-profile is all, %s or %s, not %q", vanillaProfile, vimrcProfileName, name)
}
