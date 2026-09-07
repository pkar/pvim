// Package quickfix is the error list: what ":cc", ":cn", ":cp", ":copen" and
// ":cclose" walk, and the 'errorformat' parser that fills it.
//
// Two things fill it: the output of ":make", ":grep" and a ":{range}!filter"
// that looks like errors, run through the 'errorformat' parser, and gopls's
// publishDiagnostics, which arrive already structured and skip the parser
// entirely. The list does not care which, and neither does the window
// that shows it.
//
// This package imports internal/text and nothing else: an entry is a file
// name, a line, a column and a message, and jumping to one is somebody else's
// problem. Nothing here opens a file or moves a cursor.
package quickfix

import (
	"errors"

	"github.com/pkar/pvim/internal/text"
)

// Kind is the single character vim puts in an entry's type field: 'e' for an
// error, 'w' for a warning, 'i' for information, 'n' for a note, and zero for
// an entry that did not say.
type Kind byte

// The entry kinds gopls and the go toolchain actually produce.
const (
	KindNone    Kind = 0
	KindError   Kind = 'e'
	KindWarning Kind = 'w'
	KindInfo    Kind = 'i'
	KindNote    Kind = 'n'
)

// Entry is one line of the quickfix list.
//
// The field names are vim's own from getqflist(), because ":help quickfix" is
// the documentation for this struct and a rename would only mean translating
// at every boundary.
type Entry struct {
	// FileName is the file the entry points at, as the compiler spelled it:
	// possibly relative, possibly to a directory the %D and %X patterns
	// tracked. Resolving it against a working directory is the caller's job.
	FileName string
	// Module is a module name where the format supplied one instead of a file
	// name, which is what %o captures.
	Module string
	// LNum and Col are 1-based, 0 when the format did not capture them. Col
	// is a byte column unless VCol is set, in which case it is a display
	// column, which is the difference that puts the cursor in the wrong place
	// on a line with a tab in it.
	LNum, Col int
	VCol      bool
	// EndLNum and EndCol bound a multi-character range, which gopls gives and
	// no 'errorformat' ever does. Zero means the entry is a point.
	EndLNum, EndCol int
	// Pattern is a search pattern to find the line by, from %p or a compiler
	// that gave one instead of a line number.
	Pattern string
	// Nr is the error number a compiler gave, from %n.
	Nr int
	// Kind is the type character.
	Kind Kind
	// Text is the message.
	Text string
	// Valid says the entry points at a real place. An invalid entry is a line
	// of output the format matched but that carries no location: ":cn" skips
	// them and ":copen" still shows them.
	Valid bool
}

// List is one quickfix list: its entries and which of them is current.
type List struct {
	// Title is what ":copen" puts on the status line, usually the command
	// that produced the list.
	Title string
	// Entries are in the order they were parsed.
	Entries []Entry
	// Idx is the current entry, 0-based, and -1 before anything has been
	// jumped to. ":cc" with no count goes here.
	Idx int
	// ID is stable for the life of the list, which is what lets a diagnostic
	// update replace the list gopls filled without disturbing a ":grep" the
	// user ran in between.
	ID int
}

// Stack is vim's list of the last ten quickfix lists, which ":colder" and
// ":cnewer" walk.
//
// Ten because that is vim's, and because a deeper stack is a feature nobody
// has asked for and a shallower one loses the ":grep" you ran before the
// ":make" that overwrote it.
type Stack struct {
	Lists []*List
	// Cur indexes Lists, and is the list every :c command acts on.
	Cur int
}

// MaxDepth is how many lists a Stack keeps, which is vim's ten.
const MaxDepth = 10

// ErrNoList is E42, which every :c command answers when nothing has filled a
// list yet.
var ErrNoList = errors.New("E42: No Errors")

// ErrNoMore is E553, ":cn" past the last entry.
var ErrNoMore = errors.New("E553: No more items")

// Current returns the list the :c commands act on, or nil.
func (s *Stack) Current() *List {
	if s == nil || s.Cur < 0 || s.Cur >= len(s.Lists) {
		return nil
	}
	return s.Lists[s.Cur]
}

// Push makes l the current list, dropping the oldest when the stack is full.
//
// TODO: unimplemented. The stack arithmetic is four lines
// and the reason it is not written here is that ":colder" after a push has to
// leave Cur pointing at the list it was on, which is a decision about what
// ":cnewer" means and belongs with the command that makes it.
func (s *Stack) Push(l *List) {}

// Next moves to the entry count on from the current one and returns it, which
// is ":cn". An invalid entry is skipped.
//
// TODO: unimplemented.
func (l *List) Next(count int) (Entry, error) { return Entry{}, ErrNoMore }

// Prev is ":cp".
//
// TODO: unimplemented.
func (l *List) Prev(count int) (Entry, error) { return Entry{}, ErrNoMore }

// At returns entry n, 1-based, which is what ":cc n" asks for.
//
// TODO: unimplemented.
func (l *List) At(n int) (Entry, error) { return Entry{}, ErrNoList }

// Buffer renders the list as the lines ":copen" shows, which is also the
// buffer ":cbuffer" would read back.
//
// The format is vim's: "file|line col| message" for a valid entry and "|| the
// whole line" for one that carries no location.
//
// TODO: unimplemented.
func (l *List) Buffer() *text.Buffer { return text.New() }
