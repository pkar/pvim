// Package substitute is ":s", ":g" and ":v": the three ex commands that take a
// pattern and act on the lines it matches, plus the small pieces ":normal"
// needs that do not belong to the mode machine.
//
// It sits below internal/ex rather than inside it because ":s" is where every
// pattern the user types meets internal/regex, and because the replacement
// grammar -- "\0" through "\9", "&", "~", "\u", "\U", "\l", "\L", "\e", "\E",
// "\r" and "\n" -- is a small language of its own that deserves its own tests.
//
// ":g" is the odd one out. It matches lines and then runs an ex command over
// each, and this package cannot run an ex command without importing the layer
// above it. So it does the half it can: Marks returns the lines the pattern
// selected, in vim's own two-pass order, and internal/ex runs the command over
// them. That split is also what makes ":g/x/normal dd" testable without an
// editor. ":normal" is the same shape: ParseNormal splits the command line and
// says what an incomplete command has to be finished with, and internal/ex
// feeds the keys to internal/mode.
//
// Everything in here that says what vim does was measured against
// /opt/homebrew/bin/vim 9.2.321 rather than remembered. The doc comments name
// the command that was run wherever the answer is surprising, because the
// surprising ones are the ones somebody will "fix" in two years.
package substitute

import (
	"errors"
	"strings"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/regex"
	"github.com/pkar/pvim/internal/text"
)

// The errors ":s", ":g" and ":normal" answer with. Every string here was read
// off vim's own message line, not written from memory; the oracle diffs it.
var (
	// ErrNoPrevSub is E33, which a repeat form raises when there has been no
	// ":s" yet: ":s", ":&", ":&&" and a normal-mode "&" all take this route.
	ErrNoPrevSub = errors.New("E33: No previous substitute regular expression")
	// ErrNoPrevPattern is E35, which an empty pattern raises when nothing has
	// been searched for or substituted yet.
	ErrNoPrevPattern = errors.New("E35: No previous regular expression")
	// ErrGlobalMissing is E148, ":g" with nothing after it at all.
	ErrGlobalMissing = errors.New("E148: Regular expression missing from :global")
	// ErrGlobalRecursive is E147. Vim allows a ":g" inside a ":g" and refuses
	// only when the inner one carries a range, which is not what the name
	// suggests and is what the message says.
	ErrGlobalRecursive = errors.New("E147: Cannot do :global recursive with a range")
	// ErrBackslashDelim is E10, ":s\a\b\": a backslash is not a delimiter.
	ErrBackslashDelim = errors.New(`E10: \ should be followed by /, ? or &`)
	// ErrArgRequired is E471, ":normal" with no keys after it.
	ErrArgRequired = errors.New("E471: Argument required")
	// ErrExpression is the refusal of "\=": the replacement is a vimscript
	// expression, this editor has no expression evaluator and is not getting
	// one, so this is refused with an E-code rather than mistranslated. Vim has no code for "we do not do this", so
	// E486's neighbour E479 is borrowed and the wording says what happened,
	// which is difference D-002.
	ErrExpression = errors.New("E479: Invalid replacement: pvim has no expression evaluator, so \\= is refused")
)

// NotFoundError is E486, which a pattern that matched nothing raises unless the
// "e" flag was given. It carries the pattern because vim prints it: the message
// is "E486: Pattern not found: foo" and half the value of the message is the
// half after the colon.
type NotFoundError struct{ Pattern string }

func (e NotFoundError) Error() string { return "E486: Pattern not found: " + e.Pattern }

// TrailingError is E488, whatever was left on the command line after the flags
// and the count had taken what they could.
type TrailingError struct{ Rest string }

func (e TrailingError) Error() string { return "E488: Trailing characters: " + e.Rest }

// ErrInvalidCommand is E476, which vim prints BEHIND E33 or E35 when a ":s"
// with a delimiter in it cannot resolve its pattern.
var ErrInvalidCommand = errors.New("E476: Invalid command")

// PatternError is the pair of lines a ":s" prints when it has a delimiter and
// no pattern to put behind it.
//
// Two lines and not one, which is measured and which nothing but an oracle
// would ever notice:
//
//	:s//X/ E35: No previous regular expression
//	 E476: Invalid command
//	:s//X/e E35: No previous regular expression
//	:s E33: No previous substitute regular expression
//
// Vim's search_regcomp() says the first line and its caller adds the second
// when the "e" flag has not turned errors off; a repeat form fails before
// either of them and prints one line. Error joins them with a newline so that
// a caller which prints the error at all prints both; Messages splits them
// again for a caller that puts one line on the screen at a time.
type PatternError struct {
	// Err is ErrNoPrevPattern or ErrNoPrevSub, the line vim says first.
	Err error
	// Extra is the E476 line following it, which the "e" flag suppresses.
	Extra bool
}

func (e PatternError) Error() string { return strings.Join(e.Messages(), "\n") }

// Unwrap gives the E33 or E35 behind the pair, so errors.Is finds it.
func (e PatternError) Unwrap() error { return e.Err }

// Messages is the lines to print, in order.
func (e PatternError) Messages() []string {
	if e.Extra {
		return []string{e.Err.Error(), ErrInvalidCommand.Error()}
	}
	return []string{e.Err.Error()}
}

// State is the memory the repeat forms read and write, and it is a value so a
// macro, a test and the day there are two windows can each have one.
//
// Vim keeps two pattern slots and a note of which was touched last, and every
// repeat form differs only in which slot it reads:
//
// - ":s/pat/" writes SubPattern and points Last at it.
// - "/pat" writes SearchPattern and points Last at it. internal/search owns
// that one, so internal/ex copies it in and out either side of a command.
// - ":g/pat/" writes BOTH and points Last at the substitute slot, which is
// vim's RE_BOTH and is why ":g/x/p" followed by ":%s//Q/" replaces x.
// - an empty pattern, ":~", and the "r" flag all read Last.
// - ":s" with no delimiter and ":&" read SubPattern, and raise E33 rather
// than E35 when it is empty.
type State struct {
	// SearchPattern is vim's spats[RE_SEARCH]: what "/" last looked for.
	SearchPattern string
	// SubPattern is vim's spats[RE_SUBST]: what ":s" last looked for.
	SubPattern string
	// LastIsSearch says which of the two was written most recently, which is
	// vim's last_idx and what an empty pattern resolves to.
	LastIsSearch bool

	// Replacement is vim's old_sub: the replacement of the last ":s" exactly
	// as it was typed, before "~" was expanded in it. It is what a repeat
	// form reuses, and it being the RAW text is why ":s/x/~Y/" followed by
	// three bare ":s" gives Y, YY, YYY and YYYY rather than Y four times.
	// Measured; a model with one replacement string in it cannot produce
	// that.
	Replacement string
	// HaveReplacement separates "the last replacement was empty" from "there
	// has not been one", which is the difference between ":s" working and
	// raising E33.
	HaveReplacement bool

	// Tilde is vim's reg_prev_sub: the same replacement AFTER "~" was
	// expanded in it. It is what the next "~" expands to, in a replacement
	// and in a pattern, and it is a second field because vim saves old_sub
	// before regtilde runs and reg_prev_sub after.
	Tilde string
	// HaveTilde is reg_prev_sub having been set at all. A "~" with nothing
	// behind it expands to nothing and is not an error.
	HaveTilde bool

	// Flags is vim's static subflags: what the "&" flag inherits. The letters
	// after it toggle rather than set, so ":s/a/b/&g" after a ":s/x/y/g"
	// turns "g" off.
	Flags Flags
	// haveFlags is Flags having been written by a ":s" that ran. Vim's
	// subflags is a static with an initialiser in it, and a zero Flags is not
	// that initialiser: it has KeepErrors off, which is E486 off. So the
	// first "&" of a session reads InitialFlags instead, and this is what
	// tells the two apart. A State is made by internal/ex as a zero value,
	// which is why the seed lives here and not in the constructor.
	haveFlags bool

	// GlobalDepth is how many ":g" commands are running. It is what E147
	// tests, and it is what makes a ":s" inside a ":g" hold its message back
	// so the global can print one total at the end.
	GlobalDepth int
	// globalSubs, globalLines and globalFound accumulate across every ":s" run
	// by the global that is currently outermost.
	globalSubs, globalLines int
	globalFound             bool
}

// InGlobal reports whether a ":g" is running, which is vim's global_busy.
func (s *State) InGlobal() bool { return s != nil && s.GlobalDepth > 0 }

// prevFlags is what a leading "&" inherits: the flags of the last ":s", or
// vim's static initialiser when there has not been one.
func (s *State) prevFlags() Flags {
	if s == nil || !s.haveFlags {
		return InitialFlags()
	}
	return s.Flags
}

// EnterGlobal marks a ":g" as running and returns the error a nested one with a
// range has to fail with. The caller pairs it with LeaveGlobal.
//
// hasRange is the whole test, and it is vim's: ":g/x/g/1/d" runs, and
// ":g/x/1,2g/1/d" is E147. Measured, because the message says "recursive" and
// reads like the nesting alone is refused.
func (s *State) EnterGlobal(hasRange bool) error {
	if s.GlobalDepth > 0 && hasRange {
		return ErrGlobalRecursive
	}
	if s.GlobalDepth == 0 {
		s.globalSubs, s.globalLines, s.globalFound = 0, 0, false
	}
	s.GlobalDepth++
	return nil
}

// LeaveGlobal ends one ":g" and, for the outermost one, returns the message
// line its substitutes add up to, empty when there is nothing to say.
//
// The total and not one message per line: ":g/x/s//Q/" over six lines says "3
// substitutions on 3 lines" once, which is vim holding every inner message
// back while global_busy is set.
func (s *State) LeaveGlobal(o *options.Options) string {
	if s.GlobalDepth > 0 {
		s.GlobalDepth--
	}
	if s.GlobalDepth > 0 || !s.globalFound {
		return ""
	}
	return countMessage(s.globalSubs, s.globalLines, false, report(o))
}

// Request is one substitution, asked for.
type Request struct {
	// Buf is the buffer. Every write goes through it, so the undo tree sees
	// one block per Do and not one per line.
	Buf *text.Buffer
	// First and Last are the line range, 1-based and inclusive, already
	// resolved by internal/ex. A trailing count on the command moves them:
	// see Flags.Count.
	First, Last int
	// Cmd is the parsed command line: pattern, replacement and flags.
	Cmd
	// Cursor is where the cursor is now. It is only read when nothing was
	// replaced: the "n" flag leaves the cursor on its own line and still
	// moves it to the first non-blank of it, which is vim's beginline() at
	// the end of every substitute that found something.
	Cursor text.Pos
	// Opt is the option state: 'ignorecase', 'smartcase', 'gdefault',
	// 'magic', 'report' and 'numberwidth'.
	Opt *options.Options
	// State carries the repeat forms across commands. A nil one is a
	// throwaway, which is what a test with one substitution in it wants.
	State *State
	// Confirm is the "c" flag's prompt. It is a callback and not a reader
	// because the prompt belongs on the message line of whichever frontend is
	// attached, and because ^E and ^Y scroll a window this package has never
	// heard of: the callback owns the scrolling and only ever returns a
	// decision. A nil one with "c" set answers Yes to everything, which is
	// what cmd/oracle wants and what a test wants.
	Confirm ConfirmFunc
}

// Answer is what the "c" flag's prompt came back with.
type Answer int

// The answers vim takes at "replace with X (y/n/a/q/l/^E/^Y)?". CTRL-E and
// CTRL-Y are not here: they scroll the window and ask again, which is a loop
// inside the callback and never reaches this package.
const (
	// Yes replaces this match and moves to the next.
	Yes Answer = iota
	// No skips this match and moves to the next.
	No
	// All replaces this one and every one after it without asking.
	All
	// Quit stops, keeping what was replaced so far. Escape and CTRL-C are
	// this too.
	Quit
	// Last replaces this one and then stops.
	Last
)

// Prompt is one question the "c" flag asks.
type Prompt struct {
	// Line is the line the match is on, 1-based, and Start and End are its
	// byte columns in that line.
	Line       int
	Start, End int
	// Text is the message vim puts on the cmdline, exactly:
	// `replace with X (y/n/a/q/l/^E/^Y)?`, where X is the replacement AS
	// TYPED and not as it will come out. ":s/a\(.\)/<\u\1>/c" prompts with
	// `<\u\1>`, which is measured and is the opposite of what a first guess
	// says.
	Text string
}

// ConfirmFunc answers one Prompt.
type ConfirmFunc func(Prompt) Answer

// Result is what a substitution did, which is mostly what its message line
// says.
type Result struct {
	// Lines is how many lines changed and Subs how many substitutions were
	// made, which are different numbers whenever "g" is on.
	Lines, Subs int
	// FirstLine is the first line a substitution changed, 0 when none did.
	// vim's do_sub reports the whole run to changed_lines() once, from the
	// first line it touched, so ":%s/a/X/g" over six lines leaves the
	// changelist on line 1 and not on line 6.
	FirstLine int
	// Found says the pattern matched at least once, whether or not anything
	// was replaced. It is what separates a ":s/a/b/c" answered "n" at every
	// prompt, which is silent, from a pattern that matched nothing, which is
	// E486.
	Found bool
	// Cursor is where the cursor ends up: the first non-blank of the last
	// line that changed. It has a zero Line when nothing changed, which is
	// the flag for "leave the cursor alone".
	Cursor text.Pos
	// Message is the line to print, empty when 'report' says nothing is worth
	// saying or when a ":g" is holding it back for its own total.
	Message string
	// Print is the line the "p", "l" and "#" flags print after the message,
	// empty when none of them was given.
	Print string
	// Quit says the "c" flag's prompt was answered q or Escape, which the ":g"
	// running the command has to stop on.
	Quit bool
	// Asked says the "c" flag put at least one prompt on the message line,
	// which decides whether the command wipes it again on the way out.
	Asked bool
	// DeferredFrom is the line an "a" answer switched the run back to an
	// ordinary substitute at, 0 when there was no "a". See FirstLine.
	DeferredFrom int
	// AnsweredAll says a prompt was answered "a", which stops the asking and
	// finishes the range without it. It matters because vim reports an
	// interactive run line by line and a deferred one once, from the top:
	// ":%s/aaa/X/gc" answered "yynq" leaves the changelist on the last line
	// it changed and the same command answered "a" leaves it on the first.
	AnsweredAll bool
}

// RegexOptions maps the option state onto what internal/regex needs to compile
// a pattern.
//
// It is the one place 'ignorecase', 'smartcase' and 'magic' turn into a
// translator setting, so that ":s", ":g" and the ":s" inside a ":g" all fold
// case the same way. Every pattern in this package goes through internal/regex
// and nothing here may reach for the standard library's regexp.
func RegexOptions(o *options.Options) regex.Options {
	if o == nil {
		return regex.Options{}
	}
	return regex.Options{
		IgnoreCase: o.G.IgnoreCase,
		SmartCase:  o.G.SmartCase,
		NoMagic:    !o.G.Magic,
	}
}

// regexOptionsFor is RegexOptions with the "i" and "I" flags folded in and the
// "~" atom's expansion attached.
//
// "i" and "I" override both options rather than one: ":set ic scs" and
// ":%s/Foo/X/gi" replaces every case of foo, which 'smartcase' alone would
// not. Measured.
func regexOptionsFor(o *options.Options, f Flags, prevSub string) regex.Options {
	ro := RegexOptions(o)
	switch {
	case f.IgnoreCase:
		ro.IgnoreCase, ro.SmartCase = true, false
	case f.MatchCase:
		ro.IgnoreCase, ro.SmartCase = false, false
	}
	ro.LastSubstitute = prevSub
	return ro
}

// report gives 'report', the number of changes a message has to beat before it
// is printed. The default is 2.
func report(o *options.Options) int {
	if o == nil {
		return 2
	}
	return o.G.Report
}

// magic gives 'magic', which decides whether "&" and "~" are special in a
// replacement or have to be spelled "\&" and "\~".
func magic(o *options.Options) bool { return o == nil || o.G.Magic }

// Do runs one substitution.
//
// The order of operations, which is the part that has to be right before any
// of the rest matters: resolve the pattern out of the state, expand "~" in the
// replacement and remember the expansion, compile the pattern through
// internal/regex with the case flags folded in, apply the trailing count to the
// range, then walk the lines forwards rebuilding each one that matches.
//
// The line walk rebuilds the whole line from the ORIGINAL bytes and writes it
// once, which is vim's sub_firstline: a second match on a line is found in the
// text as it was, not in the text the first replacement left behind, so
// ":s/a/aa/g" terminates.
func Do(r Request) (Result, error) {
	st := r.State
	if st == nil {
		st = &State{}
	}
	o := r.Opt

	// The flags outlive the command: vim keeps them in a static that the next
	// "&" inherits, so ":s/a/b/g" and then ":s/x/y/&" is global again.
	st.Flags, st.haveFlags = r.Flags, true

	// The pattern's own "~" atom expands to the replacement of the substitute
	// BEFORE this one, so it has to be read before resolve overwrites it.
	// Vim compiles the pattern and only then runs regtilde over the
	// replacement, which is why ":s/one/two/" and then ":s/~/Q/" finds "two".
	prevSub := st.Tilde

	pattern, replacement, err := st.resolve(r.Cmd, magic(o))
	if err != nil {
		return Result{}, err
	}

	re, err := regex.Compile(pattern, regexOptionsFor(o, r.Flags, prevSub))
	if err != nil {
		return Result{}, err
	}

	// The trailing count does not shrink the range, it moves it: ":%s/a/b/ 3"
	// is the three lines starting at the LAST line of the range, so on a
	// six-line file it is lines 6 to 8 and not lines 1 to 3. It catches
	// everybody, so it is measured and it has a test.
	first, last := r.First, r.Last
	if r.Flags.Count > 0 {
		first = last
		last = first + r.Flags.Count - 1
	}
	if first < 1 {
		first = 1
	}
	if last > r.Buf.LineCount() {
		last = r.Buf.LineCount()
	}

	res := Result{}
	sub := subber{
		req:  r,
		st:   st,
		re:   re,
		repl: replacement,
		all:  r.Flags.All,
		ask:  r.Flags.Confirm,
		only: r.Flags.CountOnly,
	}
	if err := sub.run(first, last, &res); err != nil {
		return res, err
	}

	if !res.Found {
		// The "e" flag turns E486 into silence, and a ":s" inside a ":g" is
		// silent too: vim's global_busy suppresses the error so that
		// ":g/x/s/zzz/Q/" over a file full of x is not six errors.
		if r.Flags.KeepErrors && !st.InGlobal() {
			return res, NotFoundError{Pattern: pattern}
		}
		return res, nil
	}

	// Vim ends a substitute that found something with beginline(BL_WHITE), so
	// even a run that changed nothing leaves the cursor on the first
	// non-blank of the line it is on: the "n" flag on line 1 of " lead"
	// reports column 3. Measured, and invisible until an oracle diffs the
	// cursor.
	if sub.prompted > 0 && !sub.answerAll && !sub.promptedSub {
		// The "c" flag walked the cursor to each match as it asked, and vim
		// leaves it there: on the match, not on the first non-blank of its
		// line. An "a" answer ends the asking, and from there the command is
		// an ordinary substitute again and leaves the cursor where one does.
		if sub.prompted <= r.Buf.LineCount() {
			res.Cursor = text.Pos{Line: sub.prompted, Col: sub.promptedCol}
		}
	} else if res.Cursor.Line == 0 {
		at := r.Cursor.Line
		if at >= 1 && at <= r.Buf.LineCount() {
			res.Cursor = firstNonBlank(r.Buf, at)
		}
	}

	if st.InGlobal() {
		st.globalSubs += res.Subs
		st.globalLines += res.Lines
		st.globalFound = true
	} else {
		res.Message = countMessage(res.Subs, res.Lines, r.Flags.CountOnly, report(o))
	}
	// The print flags fire on a substitution having been counted, not on the
	// pattern having matched: ":s/x/y/np" prints the line it counted on, and
	// ":s/x/y/cp" answered no to everything prints nothing. Both measured.
	//
	// A ":g" does not turn them off either. global_busy holds the COUNT back
	// and nothing else: vim's do_sub skips its own message and still calls
	// print_line, so ":g/a/s//X/p" over a1, a2, a3 prints X1, X2 and X3 and
	// then one total. Measured, with "#" and "l" the same shape.
	if r.Flags.Print && res.Subs > 0 && res.Cursor.Line > 0 {
		res.Print = printLine(r.Buf, res.Cursor.Line, r.Flags, o)
	}
	return res, nil
}

// resolve turns the parsed command into the pattern and replacement to use,
// and writes back what a later repeat form will read.
//
// It is the whole of vim's two-slot pattern memory and the whole of regtilde,
// in that order, because "~" expands against the PREVIOUS replacement and the
// result becomes the next previous one.
//
// The order of the two failures is vim's and is measured. A repeat form with
// nothing to repeat stops at the replacement and prints one line, E33. A
// delimiter form saves its replacement FIRST and then fails on the pattern,
// which prints two lines and which is why a ":s//X/" that failed leaves a
// later ":&" printing E33 and E476 rather than E33 on its own: the failed
// command armed the repeat and only the pattern was missing.
func (s *State) resolve(c Cmd, magic bool) (pattern, replacement string, err error) {
	which := c.Which
	if c.Flags.Reverse {
		which = FromLast
	}

	if !c.HavePattern {
		if !s.HaveReplacement {
			return "", "", ErrNoPrevSub
		}
	} else {
		// vim's old_sub, saved before search_regcomp and therefore saved even
		// when search_regcomp is about to fail.
		s.Replacement, s.HaveReplacement = c.Replacement, true
	}

	pattern = c.Pattern
	if !c.HavePattern || pattern == "" {
		pattern = s.SubPattern
		if which == FromLast {
			pattern = s.last()
		}
		if pattern == "" {
			e := PatternError{Err: ErrNoPrevSub, Extra: c.Flags.KeepErrors}
			if which == FromLast {
				e.Err = ErrNoPrevPattern
			}
			return "", "", e
		}
	}

	s.SubPattern = pattern
	s.LastIsSearch = false

	// The RAW replacement, expanded now: a repeat of ":s/x/~Y/" is "~Y" with
	// "~" standing for what the last one produced, so the string grows by a Y
	// each time. Measured.
	replacement = tilde(s.Replacement, s.Tilde, s.HaveTilde, magic)
	s.Tilde, s.HaveTilde = replacement, true
	return pattern, replacement, nil
}

// last is vim's spats[last_idx]: the pattern of whichever of the two commands
// ran most recently.
func (s *State) last() string {
	if s.LastIsSearch {
		return s.SearchPattern
	}
	return s.SubPattern
}

// point records that a pattern was used again, which moves last_idx even when
// the pattern itself did not change.
func (s *State) point(pattern string) {
	s.SubPattern = pattern
	s.LastIsSearch = false
}

// NoteSearch records a pattern that "/" or "?" entered, so that a later ":s//"
// finds it. internal/ex calls it because internal/search owns the search state
// and this package may not import it.
func (s *State) NoteSearch(pattern string) {
	s.SearchPattern = pattern
	s.LastIsSearch = true
}

// NoteGlobal records the pattern a ":g" used. Vim saves it into both slots,
// which is why ":g/x/p" and then ":%s//Q/" replaces x, and it is the one place
// a command that is not ":s" writes the substitute slot.
func (s *State) NoteGlobal(pattern string) {
	s.SearchPattern = pattern
	s.SubPattern = pattern
	s.LastIsSearch = false
}
