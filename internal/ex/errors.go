package ex

import "errors"

// The E-codes this package raises beyond the ones ex.go declares.
//
// Every message here was read off /opt/homebrew/bin/vim 9.2 patches 1-321 on
// through "vim --clean -i NONE --not-a-term -s" with ":redir"
// around the script, not out of the source and not from memory. Where the
// wording is pvim's own rather than vim's, the comment says so and difference
// D-002 covers it.
var (
	// ErrMarkNotSet is E20, measured with ":'zd" on a buffer with no mark z.
	ErrMarkNotSet = errors.New("E20: Mark not set")
	// ErrInvalidMark is E78, a "'" with nothing after it.
	ErrInvalidMark = errors.New("E78: Unknown mark")
	// ErrBackslash is E10, a "\" in an address that is not \/ \? or \&.
	ErrBackslash = errors.New(`E10: \\ should be followed by /, ? or &`)
	// ErrNoPrevRegexp is E35, measured with ":\/d" before any search.
	ErrNoPrevRegexp = errors.New("E35: No previous regular expression")
	// ErrPatternNotFound is E486. The pattern is appended by the caller,
	// because vim prints it: "E486: Pattern not found: nosuch".
	ErrPatternNotFound = errors.New("E486: Pattern not found")
	// ErrMoveIntoItself is E134, measured with ":1,2m1".
	ErrMoveIntoItself = errors.New("E134: Cannot move a range of lines into itself")
	// ErrNothingInRegister is E353. vim's format string is
	// "E353: Nothing in register %s", so the name is joined with a space and
	// not with the colon withName uses; measured, ":put" before any yank says
	// `E353: Nothing in register "`.
	ErrNothingInRegister = errors.New("E353: Nothing in register")
	// ErrNoFileName is E32, ":w" on a buffer that has never had a name.
	ErrNoFileName = errors.New("E32: No file name")
	// ErrNoAlternate is E23, "#" with no alternate file.
	ErrNoAlternate = errors.New("E23: No alternate file")
	// ErrNoAltName is E194, which is what a "#" in a FILE NAME argument
	// answers when there is no alternate file: ":e#" after a ":new" and a
	// ":q" says this where ":b#" says E23. Measured, both.
	ErrNoAltName = errors.New("E194: No alternate file name to substitute for '#'")
	// ErrReadOnly is E45. vim answers it to a ":w" over a buffer whose
	// 'readonly' is set and runs the write on a ":w!", which is what the
	// parenthetical is telling you. Measured with ":set ro" and ":w".
	ErrReadOnly = errors.New("E45: 'readonly' option is set (add ! to override)")
	// ErrMoreThanOneMatch is E93, ":b" with a substring that matches two
	// listed buffers.
	ErrMoreThanOneMatch = errors.New("E93: More than one match for")
	// ErrNoSuchBuffer is E86.
	ErrNoSuchBuffer = errors.New("E86: Buffer does not exist")
	// ErrBufferModified is E89, ":bd" on a modified buffer with no bang.
	ErrBufferModified = errors.New("E89: No write since last change for buffer")
	// ErrLastFileName is E499, an empty file name where one was needed.
	ErrLastFileName = errors.New("E499: Empty file name for '%' or '#', only works with \":p:h\"")
	// ErrNoHelpFor is E149, a ":help" for a tag that is in no tags file.
	ErrNoHelpFor = errors.New("E149: Sorry, no help for")
	// ErrHelpNotFound is E152 for a doc directory that is not there at all,
	// which is the failure difference D-001 exists to name.
	ErrHelpNotFound = errors.New("E152: Cannot open help file for writing")
	// ErrCannotOpen is E484. vim's format string is
	// "E484: Can't open file %s", so the name is joined with a space and not
	// with the colon withName uses, and it is the short name: ":r nosuch.txt"
	// says `E484: Can't open file nosuch.txt` and not the full path.
	ErrCannotOpen = errors.New("E484: Can't open file")
	// ErrCannotWrite is E212, which is what a write to a name that cannot be
	// opened for writing answers -- a directory that does not exist, most of
	// the time. vim quotes the file name in FRONT of it, from msg_add_fname:
	// `"nodir/x.txt" E212: Can't open file for writing`.
	ErrCannotWrite = errors.New("E212: Can't open file for writing")
	// ErrNotImplemented is pvim's own, for a command that is in the table so
	// that it parses and resolves but has no body yet. It is deliberately not
	// silence: a command that quietly does nothing shows up three keystrokes
	// later as a buffer that is wrong, and one that says so shows up as
	// itself. Difference D-002 covers the wording.
	ErrNotImplemented = errors.New("E319: pvim has not implemented this command")
)

// nameError is an error with a name appended after a colon, which is the shape
// of half of vim's messages: "E492: Not an editor command: foo", "E486:
// Pattern not found: nosuch", "E518: Unknown option: nosuchopt".
type nameError struct {
	err  error
	name string
}

func (e *nameError) Error() string { return e.err.Error() + ": " + e.name }
func (e *nameError) Unwrap() error { return e.err }

// withSpace appends an argument after a space rather than after a colon,
// which is what vim's messages with a "%s" in them rather than a trailing
// ": %s" want.
func withSpace(err error, name string) error {
	if name == "" {
		return err
	}
	return &spaceError{err: err, name: name}
}

type spaceError struct {
	err  error
	name string
}

func (e *spaceError) Error() string { return e.err.Error() + " " + e.name }
func (e *spaceError) Unwrap() error { return e.err }

// withQuotedName puts a quoted file name in FRONT of an error, which is what
// vim's msg_add_fname does for the write failures: buf_write builds
// `"name" ` in IObuff and then concatenates the message onto it.
func withQuotedName(err error, name string) error {
	if name == "" {
		return err
	}
	return &quotedError{err: err, name: name}
}

type quotedError struct {
	err  error
	name string
}

func (e *quotedError) Error() string { return `"` + e.name + `" ` + e.err.Error() }
func (e *quotedError) Unwrap() error { return e.err }

// withName appends a name to an error the way vim appends one. Passing an
// empty name gives the bare error back, because ":" with nothing after it has
// no name to quote.
func withName(err error, name string) error {
	if name == "" {
		return err
	}
	return &nameError{err: err, name: name}
}
