package regex

import "fmt"

// Refusal is the part every refused-atom error carries: the offending atom
// exactly as it was written, and the 0-based byte column it started at.
//
// The column is a byte offset into the vim pattern as the caller wrote it, not
// into the Go source the translator emits, because the only pattern the person
// at the keyboard ever sees is the one they typed.
type Refusal struct {
	Atom string
	Col  int
}

// Refused reports the atom and column. It exists so that Refusal satisfies the
// Refused interface by promotion into every error type that embeds it.
func (r Refusal) Refused() Refusal { return r }

// say builds the one message shape every refusal uses: an E-code, the atom, the
// column, and why the atom is gone rather than broken.
func (r Refusal) say(code, why string) string {
	return fmt.Sprintf("%s: %s at column %d: %s", code, r.Atom, r.Col, why)
}

// Refused is implemented by every error the translator returns for an atom vim
// has and RE2 cannot express.
//
// The distinction matters to a caller: a Refused error means the pattern is
// valid vim that pvim will not run, which is a register entry and a message,
// and a plain error means the pattern is broken, which is the user's typo.
type Refused interface {
	error
	Refused() Refusal
}

// The refusals. One type per atom, because "unsupported" as a single type puts
// the caller back to string-matching the message to find out which atom it was,
// and because the day one of these grows an implementation it is a type that
// stops being returned rather than a case in a switch nobody can find.
//
// The E-codes are pvim's own and start above E1575, the highest vim 9.2 uses,
// so they can never collide with a code the real vim prints.
type (
	// MatchStartError is \zs, which moves the reported start of the match away
	// from where matching began.
	MatchStartError struct{ Refusal }

	// MatchEndError is \ze, the same trick at the other end.
	MatchEndError struct{ Refusal }

	// LookaheadError is \@=, a zero-width assertion that the preceding group
	// matches ahead.
	LookaheadError struct{ Refusal }

	// NegLookaheadError is \@!.
	NegLookaheadError struct{ Refusal }

	// LookbehindError is \@<=.
	LookbehindError struct{ Refusal }

	// NegLookbehindError is \@<!.
	NegLookbehindError struct{ Refusal }

	// AtomicGroupError is \@>, which forbids backtracking into the group.
	AtomicGroupError struct{ Refusal }

	// BackreferenceError is \1 through \9.
	BackreferenceError struct{ Refusal }

	// ExternalMatchError is \z(, \z1 through \z9, which only ever meant
	// anything to :syntax, and pvim has no syntax highlighting at all.
	ExternalMatchError struct{ Refusal }

	// VisualAreaError is \%V.
	VisualAreaError struct{ Refusal }

	// OptionalSequenceError is \%[, a sequence of optionally matched atoms.
	OptionalSequenceError struct{ Refusal }

	// LineError is \%23l and its \%<23l and \%>23l forms.
	LineError struct{ Refusal }

	// ColumnError is \%23c and its \%<23c and \%>23c forms.
	ColumnError struct{ Refusal }

	// VirtualColumnError is \%23v and its \%<23v and \%>23v forms.
	VirtualColumnError struct{ Refusal }

	// CursorError is \%#, the cursor position.
	CursorError struct{ Refusal }

	// MarkError is \%'m, a mark position.
	MarkError struct{ Refusal }

	// BranchError is \&, vim's concat-and operator.
	BranchError struct{ Refusal }

	// UnsupportedError is the rest: atoms vim has, RE2 cannot express, and
	// nobody has asked for. \Z, \%C and [=a=] are the whole list.
	UnsupportedError struct{ Refusal }
)

func (e MatchStartError) Error() string {
	return e.say("E1801", "the match start marker has no RE2 equivalent; put the prefix in a group instead")
}

func (e MatchEndError) Error() string {
	return e.say("E1802", "the match end marker has no RE2 equivalent; put the suffix in a group instead")
}

func (e LookaheadError) Error() string {
	return e.say("E1803", "RE2 has no lookahead")
}

func (e NegLookaheadError) Error() string {
	return e.say("E1804", "RE2 has no negative lookahead")
}

func (e LookbehindError) Error() string {
	return e.say("E1805", "RE2 has no lookbehind")
}

func (e NegLookbehindError) Error() string {
	return e.say("E1806", "RE2 has no negative lookbehind")
}

func (e AtomicGroupError) Error() string {
	return e.say("E1807", "RE2 does not backtrack, so it has no atomic group")
}

func (e BackreferenceError) Error() string {
	return e.say("E1808", "RE2 has no backreferences")
}

func (e ExternalMatchError) Error() string {
	return e.say("E1809", "external matches belong to :syntax, and pvim has no syntax highlighting")
}

func (e VisualAreaError) Error() string {
	return e.say("E1810", "the pattern would have to know the visual selection, and a compiled pattern knows nothing")
}

func (e OptionalSequenceError) Error() string {
	return e.say("E1811", "an optional sequence is a shorthand RE2 has no form for; write the alternatives out")
}

func (e LineError) Error() string {
	return e.say("E1812", "the pattern would have to know the line number, and a compiled pattern knows nothing")
}

func (e ColumnError) Error() string {
	return e.say("E1813", "the pattern would have to know the column, and a compiled pattern knows nothing")
}

func (e VirtualColumnError) Error() string {
	return e.say("E1814", "the pattern would have to know the display column, and a compiled pattern knows nothing")
}

func (e CursorError) Error() string {
	return e.say("E1815", "the pattern would have to know the cursor position, and a compiled pattern knows nothing")
}

func (e MarkError) Error() string {
	return e.say("E1816", "the pattern would have to know where a mark is, and a compiled pattern knows nothing")
}

func (e BranchError) Error() string {
	return e.say("E1817", "RE2 cannot require two concats to match at one place")
}

func (e UnsupportedError) Error() string {
	return e.say("E1818", "pvim does not translate this atom")
}

// SyntaxError is a pattern vim itself would reject: an unmatched bracket, a
// multi with nothing in front of it, a \{ that never closes.
//
// It is deliberately not a Refusal. A refusal is a feature pvim does not have;
// this is a pattern nobody has.
type SyntaxError struct {
	Pattern string
	Col     int
	Msg     string
}

func (e SyntaxError) Error() string {
	return fmt.Sprintf("E1819: %s at column %d in %q", e.Msg, e.Col, e.Pattern)
}

// CompileError wraps the standard library's refusal to compile source the
// translator produced. Seeing one means either a repeat count over RE2's limit
// of 1000 or a bug in the translator, and the emitted source is in the message
// because that is the only way to tell those two apart.
type CompileError struct {
	Pattern string
	Source  string
	Err     error
}

func (e CompileError) Error() string {
	return fmt.Sprintf("E1820: vim pattern %q translated to %q which does not compile: %v", e.Pattern, e.Source, e.Err)
}

// Unwrap gives errors.Is and errors.As the regexp package's own error.
func (e CompileError) Unwrap() error { return e.Err }
