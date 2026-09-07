package quickfix

import "errors"

// Format is a compiled 'errorformat': the comma-separated list of scanf-like
// patterns vim matches each line of compiler output against.
//
// It is compiled once and matched many times because 'errorformat' is long --
// vim's default is fourteen patterns -- and a ":make" over a large package
// runs every one of them against every line of output.
//
// The conversions, from ":help errorformat":
//
//	%f file name %l line number %c column
//	%v virtual column %n error number %t type character
//	%m the message %r the rest, for a multi-line format
//	%p a pointer line (" ^"), whose length is the column
//	%o module name %s search pattern
//	%% a literal percent %# a repeat of the previous atom
//	%*{conv} a scanf conversion, which is where "%*\D" comes from
//	%.%# vim's "anything", which is why patterns end in it
//
// The prefixes, which are the half that makes multi-line output work:
//
//	%E %W %I start a multi-line error, warning or informational message
//	%A start one of unspecified type
//	%C continue one
//	%Z end one
//	%G a line to ignore entirely
//	%O an overread line
//	%D %X entering and leaving a directory, which is how "make -C"
//	 output ends up with the right file names
//	%- %+ drop the line, or include the whole of it in the message
//
// TODO: only the shape is here. The parser is a translation of each
// pattern into a matcher and then a scan; it is the largest single piece of
// work in this package and it has a natural test corpus in the go toolchain's
// own output, which is what the vimrc's ":GoBuild" equivalent will feed it.
//
// It must not reach for the standard library's regexp. 'errorformat' is not a
// regular expression, it is scanf with vim's own extensions, and the one place
// in this tree allowed to compile a pattern is internal/regex. Where a
// conversion needs a pattern -- "%*\D" is vim's regex for "one or more
// non-digits" -- it goes through internal/regex, which means this package
// grows that import when the parser lands and internal/deps_test.go gets a
// line saying so.
type Format struct {
	// Patterns are the compiled halves of the 'errorformat' string, in the
	// order they appear, which is the order they are tried in.
	Patterns []Pattern
}

// Pattern is one comma-separated piece of an 'errorformat'.
type Pattern struct {
	// Prefix is the %E, %W, %C, %Z, %G, %D or %X character, or zero for an
	// ordinary single-line pattern.
	Prefix byte
	// Minus is the %- flag: match the line and throw it away.
	Minus bool
	// Plus is the %+ flag: put the whole line in the message rather than only
	// what %m captured.
	Plus bool
	// Src is the pattern as it was written, kept for the error message and
	// for a test that wants to say which pattern matched.
	Src string
}

// ErrBadFormat is E376, an invalid 'errorformat'.
var ErrBadFormat = errors.New("E376: Invalid % in the errorformat string")

// Compile turns an 'errorformat' string into a Format.
//
// TODO: unimplemented. Compile answers a Format with the patterns
// split out and nothing matched, so that a caller can be written and tested
// against the signature before the matcher exists.
func Compile(errorformat string) (*Format, error) {
	return &Format{}, nil
}

// Parse runs lines through the format and returns the entries it made.
//
// It is a whole-input function and not a line-at-a-time one because the
// multi-line prefixes need it: %A opens an entry that %C lines add to and %Z
// closes, and a parser that cannot see the next line cannot know whether the
// entry it is holding is finished.
//
// TODO: unimplemented.
func (f *Format) Parse(lines []string) ([]Entry, error) { return nil, nil }
