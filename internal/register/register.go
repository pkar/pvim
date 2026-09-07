// Package register holds vim's registers: the values every yank, delete, put
// and macro reads and writes.
//
// A register is not a string. It is a string plus a type -- charwise, linewise
// or blockwise with a width -- and that type is what makes p paste on a new
// line or in the middle of this one. It is also where naive implementations go
// wrong, because the type has to survive a yank, an append with "A, a macro
// recording, a round trip through the system clipboard and the numbered shift
// on delete. vim's getregtype() prints it as "v", "V" and CTRL-V followed by
// the width, the oracle diffs that string for every register on every case, and
// a type that is right for the buffer and wrong for the register is a case that
// fails on state.txt with an identical buf.txt.
//
// The same three-way split is what an operator acts on, so Type is the
// vocabulary the whole mode machine shares rather than a detail of storage.
//
// Everything in this package that could be guessed was measured instead,
// against /opt/homebrew/bin/vim 9.2 with --clean, by scripting an edit and
// dumping getreg() and getregtype() for every register through writefile().
// The comments say which run decided which rule, because half of these rules
// disagree with a plain reading of :help.
package register

import (
	"bytes"
	"errors"
	"strconv"
)

// Type is what a register holds, and equally what an operator acts on: a span
// of characters, a run of whole lines, or a rectangle of display columns.
type Type int

const (
	// TypeChar is charwise. The text may start and end in the middle of a
	// line, p puts it after the cursor without breaking the line, and
	// getregtype() prints "v".
	TypeChar Type = iota
	// TypeLine is linewise: whole lines, p makes new ones below the cursor,
	// and getregtype() prints "V".
	TypeLine
	// TypeBlock is blockwise: a rectangle, p inserts it into each line at the
	// cursor's display column, and getregtype() prints CTRL-V followed by the
	// width in display columns.
	TypeBlock
)

// String names the type for a message and for a test failure, not for
// getregtype. Value.RegType is what vim's spelling lives in, because the
// blockwise spelling needs the width and a Type does not carry one.
func (t Type) String() string {
	switch t {
	case TypeChar:
		return "charwise"
	case TypeLine:
		return "linewise"
	case TypeBlock:
		return "blockwise"
	default:
		return "register.Type(" + strconv.Itoa(int(t)) + ")"
	}
}

// The register names that mean something other than "the letter you typed".
// They are bytes because that is how they arrive from the keyboard, from a "x
// prefix and from an ex command's register argument.
const (
	// Unnamed is "", which every yank and delete writes and which p reads when
	// no register was named. It is an alias rather than a copy: see File.
	Unnamed = '"'
	// SmallDelete is "-, the last delete of less than one line.
	SmallDelete = '-'
	// BlackHole is "_: writes are discarded and reads are empty. It is the one
	// register a delete may not touch on its way past.
	BlackHole = '_'
	// LastInsert is ".: the text of the last insert. Set by the mode machine
	// on leaving insert, never by a user's yank.
	LastInsert = '.'
	// LastCommand is ":, the last command line.
	LastCommand = ':'
	// LastSearch is "/, the last search pattern. Unlike the other read-only
	// ones, vim does let :let @/ = ... write it, and so does this.
	LastSearch = '/'
	// Filename is "%, the current file's name as it was typed.
	Filename = '%'
	// AltFilename is "#, the alternate file. Reading it gives that name;
	// writing it through a yank is not a write to any such thing, see slot.
	AltFilename = '#'
	// Expression is "=, which evaluates vimscript. pvim has no expression
	// evaluator, so it refuses with ErrUnsupported rather than pretending.
	Expression = '='
	// Dropped is "~, the text of the last drag and drop. pvim has no drag and
	// drop, so it reads empty and never fills. It is here because vim accepts
	// the name and pvim refusing it would be a difference nobody chose.
	Dropped = '~'
	// ClipboardStar and ClipboardPlus are "* and "+. On macOS they are one
	// pasteboard and the Clipboard the frontend supplies backs both.
	ClipboardStar = '*'
	ClipboardPlus = '+'
)

// Errors a register operation can return. They are values rather than E-coded
// strings because the E-code belongs on the message line, which is the ex
// layer's business, and a caller that wants to know "was that name legal" must
// not have to match on a sentence.
var (
	// ErrBadName is vim's E354: a register name that is not a letter, a digit
	// or one of the punctuation names above.
	ErrBadName = errors.New("register: invalid register name")
	// ErrReadOnly is a write to a register only the editor may set: ". and ":.
	// vim answers E354 here too; pvim distinguishes them because the two
	// mistakes have different fixes.
	ErrReadOnly = errors.New("register: register is read-only")
	// ErrUnsupported is "=, the expression register, which needs the
	// vimscript evaluator this editor does not have.
	ErrUnsupported = errors.New("register: register is not supported")
)

// Value is one register's contents.
//
// Lines never carry a line terminator: a charwise value that spans a line break
// is two entries, and a linewise value of one line is one entry, exactly as a
// charwise value of one line is. Type is the only thing that tells those two
// apart, which is the whole reason it travels with the text.
type Value struct {
	Lines [][]byte
	Type  Type
	// Width is the blockwise width in display columns. It is what getregtype()
	// prints after the CTRL-V and what a put uses to pad short lines. Zero for
	// every other type.
	Width int
	// ToEOL records a blockwise yank made with $: every line runs to its own
	// end and the block has no right edge, so a put appends to each line
	// rather than padding to Width.
	//
	// vim keeps this as a width of MAXCOL internally but does not print
	// MAXCOL: getregtype() after CTRL-V j $ y over "abcdefgh", "ab", "abcde"
	// answers CTRL-V 7, the widest line the yank actually took. So Width is
	// filled in for a $ block too, by the caller that did the yank and knows
	// the tabstop, and ToEOL is the fact that Width alone cannot carry.
	ToEOL bool
}

// Char builds a charwise value.
func Char(lines ...[]byte) Value { return Value{Lines: lines, Type: TypeChar} }

// LineValue builds a linewise value. The name is not Line because Line is
// already a type constant and a package with both reads as a typo.
func LineValue(lines ...[]byte) Value { return Value{Lines: lines, Type: TypeLine} }

// BlockValue builds a blockwise value of the given display width.
func BlockValue(width int, lines ...[]byte) Value {
	return Value{Lines: lines, Type: TypeBlock, Width: width}
}

// charText is the shape every read-only text register has: one charwise line,
// even when that line is empty.
//
// Measured: getregtype('/') on a vim that has never searched answers "v" and
// getreg('/') answers "", while getregtype('0') on the same vim answers "".
// The five text registers are always typed and the yank registers are typed
// only once something has been put in them, and the oracle's state dump prints
// "/ on every single case.
func charText(s string) Value { return Value{Lines: [][]byte{[]byte(s)}, Type: TypeChar} }

// Empty reports whether the value holds no text at all. A single empty line is
// not empty: linewise, it is a blank line that p will insert.
func (v Value) Empty() bool { return len(v.Lines) == 0 }

// Bytes joins the value the way getreg() does: lines separated by newlines,
// with a trailing newline on a linewise value and none on a charwise one.
func (v Value) Bytes() []byte {
	b := bytes.Join(v.Lines, []byte("\n"))
	if v.Type == TypeLine && len(v.Lines) > 0 {
		b = append(b, '\n')
	}
	return b
}

// String is Bytes as a string, which is what a message line and a test failure
// both want.
func (v Value) String() string { return string(v.Bytes()) }

// RegType is vim's getregtype() spelling: "v", "V", or CTRL-V and the width.
//
// A register that holds nothing has no type at all and this returns the empty
// string, which is not the same as charwise. Measured, not assumed: a state
// dump from vim --clean after dw prints
//
//	reg "	v	hello
//	reg /	v
//	reg 0
//
// so the unnamed register is charwise, the search register is charwise and
// empty, and register 0, which nothing has written, has no type. An
// implementation that answers "v" for every empty register differs on
// thirty-odd lines of every single case.
//
// The oracle compares this string for every register on every case, so it is
// the one place the blockwise width becomes visible and the one place it can be
// wrong without the buffer noticing.
func (v Value) RegType() string {
	if len(v.Lines) == 0 {
		return ""
	}
	switch v.Type {
	case TypeLine:
		return "V"
	case TypeBlock:
		return "\x16" + strconv.Itoa(v.Width)
	default:
		return "v"
	}
}

// AppendYank appends n to v the way "A does on a yank or a delete, and returns
// the result. v is the register as it stands and n is the new text.
//
// vim's rule, measured over the seven combinations rather than read out of the
// help, which does not state it:
//
// - the type becomes linewise if the NEW text is linewise, and otherwise
// keeps the OLD register's type. So block + char stays blockwise, char +
// block stays charwise, and either one plus a linewise yank is linewise.
// - the first new line joins the end of the last old line only when the
// result is charwise. char + char merges, char + block merges (the result
// is charwise), block + char does not.
// - the width of a blockwise register does not change. A two-wide block
// yanked into "a, then a three-wide one appended with "A, still prints
// CTRL-V 2.
//
// Appending to a register nothing has written is a plain write, type and all.
func (v Value) AppendYank(n Value) Value {
	if len(v.Lines) == 0 {
		return n
	}
	if len(n.Lines) == 0 {
		return v
	}

	out := v
	if n.Type == TypeLine {
		out.Type = TypeLine
	}
	out.Lines = joinLines(v.Lines, n.Lines, out.Type == TypeChar)
	return out
}

// AppendSet appends n to v the way :let @A = ... and :call setreg('A', ...)
// do, which is not the rule AppendYank follows. Measured, on the same vim:
//
//	:let @a = ... a linewise yank, then :let @A = 'APP'
//	 -> charwise, two lines
//	:call setreg('a', 'one', 'l') | call setreg('A', 'two', 'c')
//	 -> charwise, "one\ntwo"
//
// So here the NEW type wins outright, and the merge of the first new line onto
// the last old one happens when the OLD register was charwise, whatever the
// new text is: a charwise register appended with "X\n" comes out linewise and
// merged. Two rules, two functions, because one function with a flag would be
// a function nobody can read at the call site.
func (v Value) AppendSet(n Value) Value {
	if len(v.Lines) == 0 {
		return n
	}

	out := n
	out.Lines = joinLines(v.Lines, n.Lines, v.Type == TypeChar)
	return out
}

// joinLines concatenates two line slices, merging the first of b onto the last
// of a when merge says so. It always allocates: a register handed out by Get
// must not share its backing array with the one still in the file.
func joinLines(a, b [][]byte, merge bool) [][]byte {
	out := make([][]byte, 0, len(a)+len(b))
	out = append(out, a...)
	if merge && len(b) > 0 {
		last := len(out) - 1
		joined := make([]byte, 0, len(out[last])+len(b[0]))
		joined = append(joined, out[last]...)
		joined = append(joined, b[0]...)
		out[last] = joined
		b = b[1:]
	}
	return append(out, b...)
}

// clone copies a value deeply enough that the caller cannot reach back into the
// file through it. The lines themselves are shared, because nothing in this
// editor writes into a line slice in place; the slice of lines is not, because
// AppendYank does.
func (v Value) clone() Value {
	if v.Lines == nil {
		return v
	}
	out := v
	out.Lines = append([][]byte(nil), v.Lines...)
	return out
}

// Valid reports whether name is a register a user may type after a quote.
//
// Measured by typing "Xyy for each candidate X under vim -s and watching what
// happened: a name vim does not know beeps and drops the quote, so the yy that
// follows yanks into the unnamed register, while a known but read-only name
// eats the y and yanks nothing at all. That separates ! , ] (unknown) from
// / . % : ~ (known, read-only), which no message on the message line does,
// because an invalid register in normal mode is a silent beep.
func Valid(name byte) bool {
	if isAlnum(name) {
		return true
	}
	switch name {
	case Unnamed, SmallDelete, BlackHole, ClipboardStar, ClipboardPlus, AltFilename,
		Expression, LastSearch, LastInsert, Filename, LastCommand, Dropped:
		return true
	}
	return false
}

// Writable reports whether a yank or a delete may write name. It is Valid
// minus the five vim reads and never writes.
//
// "= is not writable here even though :let @= = 'expr' is legal in vim: that
// stores an expression for the evaluator this editor does not have, and
// storing text under the name would make "=p paste something vim would have
// evaluated.
func Writable(name byte) bool {
	switch name {
	case LastSearch, LastInsert, Filename, LastCommand, Dropped, Expression:
		return false
	}
	return Valid(name)
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// MotionForcesNumbered reports whether a delete made with this motion writes
// "1 and shifts the numbered registers even when it deleted less than one
// line. It is :help quote_number's list of ten, and it is why d/foo and dw
// leave different registers behind.
//
// Measured for %, (, ), backtick, /, ?, n and N, each of which writes "1, and
// each of which also writes "- and shifts "1 into "2. The help sentence reads
// as if "1 replaced "-; it does not, both happen, and the two conditions in
// File.Delete are independent for that reason.
//
// { and } are on vim's list and are not on that measured list because they
// cannot be measured: both motions land on a different line by construction,
// so a delete with one is never less than one line and the flag never decides
// anything. They are here because the help says so and because leaving them
// out would be a silent bet that the help is wrong about them too.
func MotionForcesNumbered(motion byte) bool {
	switch motion {
	case '%', '(', ')', '`', '/', '?', 'n', 'N', '{', '}':
		return true
	}
	return false
}

// Names lists every register the oracle's state dump wants, in the order it
// wants them: the unnamed register, the search register, 0 through 9, then a
// through z. Registers that were never written are included and come back
// empty, because the dump has a line for each one whether or not it holds
// anything.
func Names() []byte {
	names := []byte{Unnamed, LastSearch}
	for c := byte('0'); c <= '9'; c++ {
		names = append(names, c)
	}
	for c := byte('a'); c <= 'z'; c++ {
		names = append(names, c)
	}
	return names
}
