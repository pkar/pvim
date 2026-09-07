// Package ex is the colon line: ranges, command parsing, the command table and
// the dispatch that runs one.
//
// A command is a name, an optional range, a bang, arguments and sometimes a
// destination address. Everything typed after ":" comes through Parse and
// everything that happens afterwards comes through a Handler out of the table
// in table.go. The vimrc's own ":command! -range -nargs=0 -bar JsonPretty" and
// the ":so $MYVIMRC" in its BufWritePost autocmd both arrive here.
//
// This package is where the editor's pieces meet: it imports internal/options,
// internal/window, internal/quickfix and internal/substitute for the state, and
// internal/mode for the editor the commands act on. Nothing imports it except
// internal/vimrc and cmd/pvim, and it must never import internal/screen: a
// command produces text and a message, and what those look like is the display
// layer's business.
package ex

import (
	"errors"

	"github.com/pkar/pvim/internal/text"
)

// AddrKind is how one address of a range was written.
type AddrKind uint8

// The address forms, from ":help :range". Every one of them may carry an
// offset: "/foo/+3", "'a-1", ".+5", "$-1".
const (
	// AddrNone is an address that was not given at all.
	AddrNone AddrKind = iota
	// AddrLine is a literal line number: "5".
	AddrLine
	// AddrCurrent is ".", and is also what an omitted address means once a
	// command has supplied its default.
	AddrCurrent
	// AddrLast is "$".
	AddrLast
	// AddrMark is "'a", and "'<" and "'>" for the visual selection.
	AddrMark
	// AddrSearchFwd is "/pat/" and AddrSearchBack is "?pat?".
	AddrSearchFwd
	AddrSearchBack
	// AddrNextMatch is "\/" and AddrPrevMatch is "\?": the next and previous
	// match of the last search pattern.
	AddrNextMatch
	AddrPrevMatch
	// AddrLastSubst is "\&": the next match of the last substitute pattern.
	AddrLastSubst
	// AddrAll is "%", which is shorthand for the whole range and expands to
	// two addresses rather than one.
	AddrAll
)

// Addr is one address of a range.
type Addr struct {
	Kind AddrKind
	// Line is the number for AddrLine.
	Line int
	// Mark is the character after "'" for AddrMark.
	Mark byte
	// Pattern is the pattern for the two search forms, without its
	// delimiters and in vim's dialect.
	Pattern string
	// Offset is the signed "+3" or "-1" after the address, summed when
	// several were written.
	Offset int
}

// Range is the line range in front of a command.
type Range struct {
	From, To Addr
	// Given is how many addresses the user actually typed: 0, 1 or 2. It is
	// not derivable from From and To, and it is the field every command reads
	// first, because ":d" and ":.d" and ":.,.d" do the same thing while ":w"
	// and ":.w" do not.
	Given int
	// Semicolon says the separator was ";" rather than ",", which moves the
	// cursor to From before To is resolved. That is why "/a/;/b/" finds the
	// first b after the first a and "/a/,/b/" finds the first b in the file.
	Semicolon bool
	// Earlier are the addresses in front of From, oldest first, each with the
	// separator written after it.
	//
	// Only the last two addresses decide which lines a command acts on, but
	// every ";" moves the cursor as it is passed and the ones before the
	// surviving pair move it too: vim's get_address is a single left-to-right
	// walk with no memory of how many addresses it has seen. Throwing these
	// away made ":3;.,.+1d" delete lines 1 and 2 where vim deletes 3 and 4.
	Earlier []Sep
}

// Sep is one address of a range with the separator that followed it.
type Sep struct {
	Addr      Addr
	Semicolon bool
}

// Mods are the command modifiers that may precede a command name: ":vertical
// sp", ":silent w", ":tab new", ":botright split".
//
// They are parsed off the front and carried rather than being made into
// commands of their own, because vim allows them to stack in any order and
// because ":vertical" changes what ":split" does rather than doing anything
// itself.
type Mods struct {
	Silent     bool // :silent
	SilentBang bool // :silent!, which also swallows errors
	Vertical   bool // :vertical
	Tab        int  // :tab, with the tab number or -1 for "after the current"
	Aboveleft  bool // :aboveleft, :leftabove
	Belowright bool // :belowright, :rightbelow
	Topleft    bool // :topleft
	Botright   bool // :botright
	Keepalt    bool // :keepalt
	Keepjumps  bool // :keepjumps
	Noautocmd  bool // :noautocmd
	Confirm    bool // :confirm
	Browse     bool // :browse, parsed and ignored: there is no file dialog
}

// Cmd is one parsed ex command.
type Cmd struct {
	// Name is the full command name after prefix resolution: ":s" arrives
	// here as "substitute" and ":e" as "edit". Typed is what was written,
	// which is what an error message quotes.
	Name  string
	Typed string

	// Bang is the "!" straight after the name.
	Bang bool

	// Range is the range in front of the name.
	Range Range

	// Reg is a register name given as an argument, for ":d a" and ":y A".
	Reg byte

	// Count is a numeric argument after the command, which for the commands
	// that take one turns the range into "Count lines starting at the last
	// line of the range". ":d 3" deletes three lines.
	Count int

	// Args is everything after the name, the bang, the register and the
	// count, with leading white space removed. It is unparsed on purpose:
	// every command's argument grammar is its own, and a Handler that wants
	// ":s"'s three delimited fields should not have to undo a generic split.
	Args string

	// Addr is the destination address of the commands that take one: ":m",
	// ":t" and ":copy".
	Addr Addr

	// Mods are the modifiers in front of the whole thing.
	Mods Mods

	// Line is the whole command as typed, without the leading colon. A user
	// command's <q-args> and the error message for an unknown command both
	// need it, and reconstructing it from the fields above loses white space.
	Line string

	// Lines is the range after it has been resolved against the buffer, the
	// marks and the cursor, with the zero-bump and the trailing count already
	// applied. RunCmd fills it in before it calls a Handler, so a Handler
	// never resolves anything and never has to know which of vim's four range
	// rules applied to it.
	Lines LineRange
}

// The errors the parser answers with. The code is what matters and is matched
// exactly; the sentence after it is pvim's own where pvim has not implemented
// what vim does, which is difference D-002.
var (
	// ErrNotAnEditorCommand is E492, which is what an unknown command answers
	// and what the vimrc loader prints with a file and a line number before
	// carrying on.
	ErrNotAnEditorCommand = errors.New("E492: Not an editor command")
	// ErrNoRangeAllowed is E481.
	ErrNoRangeAllowed = errors.New("E481: No range allowed")
	// ErrTrailing is E488.
	ErrTrailing = errors.New("E488: Trailing characters")
	// ErrNoBangAllowed is E477.
	ErrNoBangAllowed = errors.New("E477: No ! allowed")
	// ErrInvalidRange is E16.
	ErrInvalidRange = errors.New("E16: Invalid range")
	// ErrBackwardsRange is E493, which vim offers to swap for you and which
	// this editor answers the same way: the caller prompts.
	ErrBackwardsRange = errors.New("E493: Backwards range given")
	// ErrArgumentRequired is E471.
	ErrArgumentRequired = errors.New("E471: Argument required")
	// ErrNoWriteSince is E37, ":q" on a modified buffer without a bang.
	ErrNoWriteSince = errors.New("E37: No write since last change")
)

// LineRange is the resolved form: what a Handler actually acts on.
type LineRange struct {
	First, Last int
	// Given is carried through from the Range, because a command that behaves
	// differently with no range given needs to know after resolution too.
	Given int
}

// Empty reports whether the range covers no lines, which happens on an empty
// buffer and nowhere else.
func (r LineRange) Empty() bool { return r.Last < r.First }

// Cursor is where a command leaves the cursor when it has nothing better to
// say, which is the first non-blank of the last line it touched.
func (r LineRange) Cursor() text.Pos { return text.Pos{Line: r.Last} }
