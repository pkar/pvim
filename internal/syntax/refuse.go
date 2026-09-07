package syntax

import "fmt"

// Every way this package can say "vim does that and I do not", one type per
// reason.
//
// The rule the whole package is built around: an item this package cannot run
// correctly is dropped and named, never approximated. A dropped item leaves
// text in the colour of whatever encloses it, which is the same colour vim
// gives text no rule matched; an approximated item paints the wrong thing in a
// confident colour, and there is no way for a reader to tell that apart from a
// bug in their file. Refusals are collected on the loaded Syntax so that
// `:syntax refusals` -- and the tests -- can print exactly what a file lost.
//
// The E-codes are pvim's own and continue internal/regex's block, which starts
// at E1801 and ends at E1820.

// Refusal is what every refusal here carries: where it happened and what was
// refused.
type Refusal struct {
	// File is the syntax file, as vim would name it: "syntax/go.vim".
	File string
	// Line is the 1-based line in that file.
	Line int
	// Group is the syntax group the refused item would have defined, empty
	// when the refusal is not about one item.
	Group string
	// Detail is the offending text: the atom, the option, the command.
	Detail string
}

func (r Refusal) where() string {
	if r.Group != "" {
		return fmt.Sprintf("%s:%d: %s: %q", r.File, r.Line, r.Group, r.Detail)
	}
	return fmt.Sprintf("%s:%d: %q", r.File, r.Line, r.Detail)
}

// Refused is implemented by every error below, so a caller can tell a
// construct pvim does not have from a syntax file that is broken.
type Refused interface {
	error
	Refused() Refusal
}

// Refused reports the location. It is here so every type below satisfies
// Refused by embedding.
func (r Refusal) Refused() Refusal { return r }

type (
	// CommandError is a:syn subcommand this package does not implement.
	CommandError struct{ Refusal }

	// OptionError is an option on a syn item that this package does not
	// implement, on an item it otherwise would have taken.
	OptionError struct{ Refusal }

	// PatternError is a pattern internal/regex would not take, with its own
	// error underneath.
	PatternError struct {
		Refusal
		Err error
	}

	// MultiLineError is a pattern that reaches across a line break, with \n
	// or a \_ class. The matcher here is per line by construction, because
	// that is what makes a 40,000-line file cost one line's work per frame.
	MultiLineError struct{ Refusal }

	// LookaroundError is a \@! or \@<! that cannot be turned into \zs or \ze.
	// A leading \@<= and a trailing \@= are rewritten; the negative forms and
	// a lookaround in the middle of a pattern are not.
	LookaroundError struct{ Refusal }

	// SplitError is a \zs or \ze this package cannot split the pattern at:
	// one inside a group, or one in a pattern with a top-level alternation,
	// where the two halves are not the two halves of a concat.
	SplitError struct{ Refusal }

	// ScriptError is a vimscript statement in a syntax file that the reader
	// in this package does not evaluate.
	ScriptError struct{ Refusal }

	// MissingError is a syntax file that is not in the runtime at all.
	MissingError struct{ Refusal }
)

func (e CommandError) Error() string {
	return fmt.Sprintf("E1830: %s: unimplemented :syntax subcommand", e.where())
}

func (e OptionError) Error() string {
	return fmt.Sprintf("E1831: %s: unimplemented :syntax option", e.where())
}

func (e PatternError) Error() string {
	return fmt.Sprintf("E1832: %s: %v", e.where(), e.Err)
}

// Unwrap gives errors.As the internal/regex error underneath, so a caller can
// ask which atom internal/regex refused without matching on a message.
func (e PatternError) Unwrap() error { return e.Err }

func (e MultiLineError) Error() string {
	return fmt.Sprintf("E1833: %s: the pattern crosses a line break and the matcher is per line", e.where())
}

func (e LookaroundError) Error() string {
	return fmt.Sprintf("E1834: %s: lookaround that is not a leading \\@<= or a trailing \\@=", e.where())
}

func (e SplitError) Error() string {
	return fmt.Sprintf("E1835: %s: \\zs or \\ze the pattern cannot be split at", e.where())
}

func (e ScriptError) Error() string {
	return fmt.Sprintf("E1836: %s: unimplemented vimscript in a syntax file", e.where())
}

func (e MissingError) Error() string {
	return fmt.Sprintf("E1837: %s: no such syntax file in the runtime", e.where())
}
