package register

import "bytes"

// Clipboard is the system clipboard behind "* and "+.
//
// An interface supplied by the frontend, because internal/register may not
// import anything platform-specific: on macOS the pasteboard is an AppKit call
// that has to be marshalled to the main thread, on Linux it is an X11
// selection, and in a test it is a two-field struct. A File with no Clipboard
// treats "* and "+ as ordinary registers, which is what a headless oracle run
// wants and what keeps every test in this package free of a window server.
type Clipboard interface {
	Read() (Value, error)
	Write(v Value) error
}

// Options are the settings that change which register another one means.
type Options struct {
	// Unnamed is 'clipboard' containing "unnamed": a yank or a delete with no
	// register named also lands on "*, and a put with no register named reads
	// it back.
	Unnamed bool
	// UnnamedPlus is 'clipboard' containing "unnamedplus", the same thing for
	// "+. On macOS "+ and "* are one pasteboard, so with both set the
	// difference is invisible; the vimrc this editor exists to run sets both.
	UnnamedPlus bool
}

// File is the whole set of registers.
//
// One File belongs to one editor, not to one buffer: vim's registers are
// global and a yank in one window pastes in another.
//
// # The unnamed register is a pointer
//
// "" is not a copy of the last yank, it is an alias for whichever register the
// last yank or delete wrote, which is vim's y_previous. Measured:
//
//	"ayy then :let @a = 'XYZ' -> @" is XYZ
//	dd then :let @1 = 'XYZ' -> @" is XYZ
//	x then :let @- = 'XYZ' -> @" is XYZ
//	any of those then :let @b = 'BBB' -> @" is unchanged
//
// so a later write to the register behind the alias is visible through "", and
// a write to any other register is not. Only a yank or a delete moves the
// pointer: :let, :call setreg() and recording a macro with q all write their
// register and leave it where it was.
//
// The one exception, also measured: a write to "" itself does not follow the
// pointer. ":let @" = 'Q'" after "ayy leaves "a alone, puts Q in "0 and moves
// the pointer to "0, because vim resolves a register name it cannot place to
// register 0 and remembers what it wrote through.
type File struct {
	// vals holds every yank register that has been written, keyed by the slot
	// it lives in: '0' to '9', 'a' to 'z', '-', '*' and '+'. A missing key is
	// an empty register, which is not an error.
	vals map[byte]Value
	// previous is vim's y_previous: the slot "" is an alias of, or zero when
	// nothing has been yanked or deleted yet, in which case "" reads "0.
	previous byte

	// The five registers vim fills itself. They are separate fields and not
	// slots because they are always typed, even when empty: getregtype('/')
	// on a vim that has never searched answers "v" where getregtype('0')
	// answers nothing at all.
	lastInsert  Value
	lastCommand Value
	lastSearch  Value
	filename    Value
	altFilename Value

	opt  Options
	clip Clipboard
}

// NewFile returns an empty register file with no clipboard behind "* and "+.
func NewFile() *File {
	return &File{
		vals:        map[byte]Value{},
		lastInsert:  charText(""),
		lastCommand: charText(""),
		lastSearch:  charText(""),
		filename:    charText(""),
		altFilename: charText(""),
	}
}

// SetOptions replaces the 'clipboard' settings. The mode machine calls it when
// :set clipboard runs, and nothing caches the answer.
func (f *File) SetOptions(o Options) { f.opt = o }

// SetClipboard installs the system clipboard. The frontend does this at
// startup; a nil c takes it away again, which is what a test wants.
func (f *File) SetClipboard(c Clipboard) { f.clip = c }

// UnnamedName is the register an unnamed put reads and an unnamed yank
// mirrors to, given 'clipboard'. It is Unnamed itself unless one of the two
// clipboard settings is on.
//
// It is not what Get(Unnamed) reads, and the difference is vim's, measured:
// with clipboard=unnamed,unnamedplus, yy then an external pbcopy then p pastes
// what pbcopy put there, while getreg('"') on the same vim still answers the
// yanked line. The put path follows 'clipboard' and the register value does
// not, so the oracle's state dump and the editor's own p disagree about what
// "" means and both are right.
func (f *File) UnnamedName() byte {
	switch {
	case f.opt.UnnamedPlus:
		return ClipboardPlus
	case f.opt.Unnamed:
		return ClipboardStar
	default:
		return Unnamed
	}
}

// Previous is the register "" currently aliases, or zero when nothing has been
// yanked or deleted yet. Exported for tests and for the oracle's state dump,
// not for the mode machine, which should be asking Get.
func (f *File) Previous() byte { return f.previous }

// slotFor maps a register name onto the slot a write goes to, and reports
// whether the write appends.
//
// The odd case is the default. vim's get_yank_register sends every name that
// is not a digit, a letter, "-, "* or "+ to register 0, which is why "#dd
// leaves the deleted line in "0: measured, and the reason this is a default
// rather than an error.
func slotFor(name byte) (slot byte, appends bool) {
	switch {
	case name >= 'A' && name <= 'Z':
		return name + ('a' - 'A'), true
	case name >= 'a' && name <= 'z', name >= '0' && name <= '9':
		return name, false
	case name == SmallDelete, name == ClipboardStar, name == ClipboardPlus:
		return name, false
	default:
		return '0', false
	}
}

// readSlot maps a register name onto the slot a read comes from, following the
// alias for "".
func (f *File) readSlot(name byte) byte {
	if name == Unnamed {
		if f.previous != 0 {
			return f.previous
		}
		return '0'
	}
	slot, _ := slotFor(name)
	return slot
}

// readAt returns what is in a slot, through the clipboard when the slot is one
// of the two the frontend owns and a frontend has been installed.
func (f *File) readAt(slot byte) (Value, error) {
	if f.clip != nil && (slot == ClipboardStar || slot == ClipboardPlus) {
		return f.clip.Read()
	}
	return f.vals[slot].clone(), nil
}

// writeAt puts a value in a slot, appending to what is there when appends is
// set. append is the yank rule, not the :let rule; Set does its own.
func (f *File) writeAt(slot byte, v Value, appends bool) error {
	if appends {
		old, err := f.readAt(slot)
		if err != nil {
			return err
		}
		v = old.AppendYank(v)
	}
	if f.clip != nil && (slot == ClipboardStar || slot == ClipboardPlus) {
		return f.clip.Write(v)
	}
	f.vals[slot] = v.clone()
	return nil
}

// mirror copies a value onto the clipboard registers 'clipboard' names, which
// is what makes yy under clipboard=unnamed land on the pasteboard.
//
// It does not move the unnamed alias. Measured: with clipboard=unnamed,
// unnamedplus, yy then :let @0 = 'Z' leaves @" reading Z, so the pointer is on
// "0 and not on the clipboard register the same yank also wrote.
//
// Only a yank or delete with no register named at all mirrors. A typed ""yy
// does not, which is vim's adjust_clip_reg testing the name for zero and is
// the one rule here taken from reading vim rather than from running it: a
// headless run cannot tell the two apart, because both write "0 and both then
// have the pasteboard holding what they just wrote.
func (f *File) mirror(v Value) error {
	if !f.opt.Unnamed && !f.opt.UnnamedPlus {
		return nil
	}
	if f.clip != nil {
		// One pasteboard on macOS; writing it twice would be two trips to the
		// main thread for the same bytes.
		return f.clip.Write(v)
	}
	if f.opt.Unnamed {
		f.vals[ClipboardStar] = v.clone()
	}
	if f.opt.UnnamedPlus {
		f.vals[ClipboardPlus] = v.clone()
	}
	return nil
}

// SetSelection is 'clipboard' containing "autoselect": the visual selection
// goes onto the clipboard as it changes.
//
// It is not a yank. Nothing names a register, "0 is not written and the unnamed
// alias does not move; the only thing that changes is what "* and "+ hold, and
// on macOS those are one pasteboard so both are written. What it costs is
// measured and surprising: with the vimrc's clipboard=unnamed,autoselect,
// "yy j V p" pastes the line V selected and not the line yy yanked, because
// under "unnamed" the register p reads is the one autoselect just wrote.
func (f *File) SetSelection(v Value) error {
	if f.clip != nil {
		return f.clip.Write(v)
	}
	f.vals[ClipboardStar] = v.clone()
	f.vals[ClipboardPlus] = v.clone()
	return nil
}

// Get reads a register, exactly as vim's getreg() and getregtype() see it.
//
// An empty register is a zero Value and a nil error: nothing has been yanked
// into it yet and that is not a mistake. An error means the name itself cannot
// be read -- "= needs an expression evaluator, a clipboard read can fail --
// and the caller puts the E-code on the message line.
//
// Name zero and Unnamed both mean the unnamed register, which is read through
// the alias and not through 'clipboard'. A put wants ForPut instead.
func (f *File) Get(name byte) (Value, error) {
	if name == 0 {
		name = Unnamed
	}
	if !Valid(name) {
		return Value{}, ErrBadName
	}
	switch name {
	case Expression:
		return Value{}, ErrUnsupported
	case BlackHole:
		// Measured: getregtype('_') is "v" and getreg('_') is "", on a vim
		// that has just had :let @_ = 'x' swallowed by it.
		return charText(""), nil
	case LastInsert:
		return f.lastInsert, nil
	case LastCommand:
		return f.lastCommand, nil
	case LastSearch:
		return f.lastSearch, nil
	case Filename:
		return f.filename, nil
	case AltFilename:
		return f.altFilename, nil
	case Dropped:
		// pvim has no drag and drop, so "~ is always empty. Measured, because
		// it is the one text register that is not typed when empty:
		// getregtype('~') answers "" where getregtype('/') answers "v".
		return Value{}, nil
	}
	return f.readAt(f.readSlot(name))
}

// ForPut is the value a put pastes with this register name, which is not
// always the value Get returns for it.
//
// With 'clipboard' set to unnamed or unnamedplus, a put with no register named
// reads the system clipboard, so text copied in another application pastes
// with a bare p. Get(Unnamed) keeps answering what vim's getreg('"') answers,
// because that is the string the oracle diffs. See UnnamedName for the
// measurement that separates them.
func (f *File) ForPut(name byte) (Value, error) {
	if name == 0 || name == Unnamed {
		return f.Get(f.UnnamedName())
	}
	return f.Get(name)
}

// Set writes a register directly, which is what :let @a = ..., :call setreg()
// and a macro recording do. An uppercase name appends to the lowercase
// register under the rule AppendSet documents, which is not the rule an
// appending yank follows.
//
// Set is not the path a yank or a delete takes. Those have rules about which
// other registers they also write and about the unnamed alias, and Yank and
// Delete are where the rules live so that no caller has to remember them.
//
// The registers vim fills itself refuse: ". ": "% "# and "~ answer ErrReadOnly
// where vim answers E354, and "/ does not, because :let @/ = 'pat' is how a
// script sets the search pattern and vim allows exactly that one.
func (f *File) Set(name byte, v Value) error {
	raw := name
	if name == 0 {
		name = Unnamed
	}
	if !Valid(name) {
		return ErrBadName
	}
	switch name {
	case Expression:
		return ErrUnsupported
	case BlackHole:
		return nil // swallowed, and not an error
	case LastSearch:
		f.lastSearch = charwiseText(v)
		return nil
	case LastInsert, LastCommand, Filename, AltFilename, Dropped:
		return ErrReadOnly
	}

	slot, appends := slotFor(name)
	if appends {
		old, err := f.readAt(slot)
		if err != nil {
			return err
		}
		v = old.AppendSet(v)
	}
	if f.clip != nil && (slot == ClipboardStar || slot == ClipboardPlus) {
		if err := f.clip.Write(v); err != nil {
			return err
		}
	} else {
		f.vals[slot] = v.clone()
	}
	// A write to "" moves the alias onto what it wrote, and a write to any
	// other register leaves it alone. Both measured; see the File comment.
	if raw == 0 || raw == Unnamed {
		f.previous = slot
	}
	return nil
}

// charwiseText forces a value to the shape a text register holds: charwise,
// and typed even when it is empty.
func charwiseText(v Value) Value {
	if len(v.Lines) == 0 {
		return charText("")
	}
	out := v.clone()
	out.Type = TypeChar
	out.Width = 0
	out.ToEOL = false
	return out
}

// Yank records a yank.
//
// vim's rule, and the reason this is not Set: a yank with no register named
// writes "0 and points the unnamed alias at it. A yank with a register named
// writes that register and leaves "0 alone, which is measured and is the
// opposite of what a reader expects from "0 being called the yank register.
// A yank never shifts the numbered registers and never touches "-.
//
// name is zero when the user typed no "x prefix.
func (f *File) Yank(name byte, v Value) error {
	raw := name
	if name == 0 {
		name = Unnamed
	}
	if name == BlackHole {
		return nil
	}
	if !Valid(name) {
		return ErrBadName
	}
	if !Writable(name) {
		return ErrReadOnly
	}

	slot, appends := slotFor(name)
	if err := f.writeAt(slot, v, appends); err != nil {
		return err
	}
	f.previous = slot

	if raw == 0 {
		return f.mirror(v)
	}
	return nil
}

// Delete records a delete or a change.
//
// Two rules, and they are independent, which is the thing this package exists
// to get right:
//
// - "1 is written, and "1 through "8 shift up into "2 through "9 first, when
// the delete is linewise, or spans more than one line, or was made with
// one of the ten motions :help quote_number names. useRegOne is the
// caller's answer to that last one, because only the caller knows the
// motion; MotionForcesNumbered turns a motion into it.
// - "- is written when the delete is within one line and not linewise AND no
// register was named, or the one named is the one 'clipboard' had already
// claimed. See smallDeleteFills.
//
// Both can fire on the same delete. Measured: d/beta over a single line puts
// "alpha " in "1 and in "-, and shifts whatever "1 held into "2. The help
// sentence reads as if the numbered registers replaced "- for those motions;
// they do not.
//
// A named register gets the text as well, and the numbered shift still happens
// after it: "add leaves the line in "a and in "1, and "1dd leaves it in "1 and
// in "2, because the named write lands first and the shift then moves it. "_
// is the exception that swallows everything and updates nothing, the unnamed
// alias included.
//
// An uppercase name appends, and that changes where the unnamed alias ends up:
// a shifting delete normally leaves it on "1, and an appending one leaves it on
// the register it appended to. Measured: "ayyj"Add leaves @" reading both
// lines and @1 reading the second.
func (f *File) Delete(name byte, v Value, useRegOne bool) error {
	raw := name
	if name == 0 {
		name = Unnamed
	}
	if name == BlackHole {
		return nil
	}
	if !Valid(name) {
		return ErrBadName
	}
	if !Writable(name) {
		return ErrReadOnly
	}

	// A quote with no name after it counts as naming a register here: ""x
	// leaves the character in "0 and "- empty, measured, so the rules below
	// ask about the name the user typed and not the one it resolved to.
	named := raw != 0
	// appends is vim's y_append, which get_yank_register sets for an uppercase
	// name and clears for every other one. The shift below needs it as well as
	// the named write does, so it is computed once here.
	slot, appends := slotFor(name)

	if named {
		if err := f.writeAt(slot, v, appends); err != nil {
			return err
		}
		f.previous = slot
	}

	if v.Type == TypeLine || len(v.Lines) > 1 || useRegOne {
		f.shift()
		f.vals['1'] = v.clone()
		// The shift happens whether or not the delete appended, and the alias
		// only moves when it did not: vim's shift_delete_registers guards its
		// y_previous assignment with "if (!y_append)". Measured over
		// alpha beta gamma / second line here: "ayyj"Add leaves @" holding
		// both lines, which is "a, and "1 holding the second line alone.
		if !appends {
			f.previous = '1'
		}
	}

	if f.smallDeleteFills(raw) && v.Type != TypeLine && len(v.Lines) == 1 {
		f.vals[SmallDelete] = v.clone()
		f.previous = SmallDelete
	}

	if !named {
		return f.mirror(v)
	}
	return nil
}

// smallDeleteFills reports whether a delete written with the typed register
// name raw also fills "-, ignoring the size of the delete, which Delete tests
// separately.
//
// The rule is not simply "no register was named". vim's op_delete asks
//
//	((clip_unnamed & CLIP_UNNAMED) && regname == '*')
//	 || ((clip_unnamed & CLIP_UNNAMED_PLUS) && regname == '+')
//	 || regname == 0
//
// so a register 'clipboard' has already claimed counts as no register at all:
// typing it asks for the register the delete was going to write anyway, and
// the small-delete rule still applies. The asymmetry is real and each half of
// 'clipboard' only excuses its own register.
//
// Measured on vim 9.2 over "alpha beta" with the cursor at the start, and this
// is why it matters, because a typed "*x then "-p pastes the character in vim:
//
//	clipboard=unnamed,unnamedplus "*x -> "- = "a" charwise, @" aliases "-
//	clipboard=unnamed,unnamedplus "+x -> "- = "a" charwise, @" aliases "-
//	clipboard=unnamed "+x -> "- empty
//	clipboard=unnamedplus "*x -> "- empty
//	clipboard= "*x -> "- empty
//	clipboard=unnamed,unnamedplus "ax -> "- empty
//
// ~/.vimrc sets clipboard=unnamed,unnamedplus,autoselect, so the first two
// lines are the configuration this editor exists to run.
func (f *File) smallDeleteFills(raw byte) bool {
	switch {
	case raw == 0:
		return true
	case raw == ClipboardStar:
		return f.opt.Unnamed
	case raw == ClipboardPlus:
		return f.opt.UnnamedPlus
	default:
		return false
	}
}

// shift moves "1 through "8 up into "2 through "9 and leaves "1 for the caller
// to overwrite. "9 falls off the end and "0 is not part of it.
func (f *File) shift() {
	for r := byte('9'); r > '1'; r-- {
		if v, ok := f.vals[r-1]; ok {
			f.vals[r] = v
		} else {
			delete(f.vals, r)
		}
	}
}

// SetLastInsert sets ".: the text of the insert that just ended. Only the mode
// machine calls it, which is why Set refuses the same register.
//
// The text is split on newlines and held charwise, which is what vim shows:
// after i one CR two Escape, getreg('.') is "one\ntwo" and getregtype('.') is
// "v".
func (f *File) SetLastInsert(text []byte) {
	f.lastInsert = Value{Lines: bytes.Split(text, []byte("\n")), Type: TypeChar}
}

// SetLastCommand sets ":, the command line that just ran, without its colon.
func (f *File) SetLastCommand(cmd string) { f.lastCommand = charText(cmd) }

// SetLastSearch sets "/, the pattern that was last searched for.
func (f *File) SetLastSearch(pattern string) { f.lastSearch = charText(pattern) }

// SetFilename sets "% and "#, the current and alternate file names, as they
// were typed rather than as they resolve.
func (f *File) SetFilename(current, alternate string) {
	f.filename = charText(current)
	f.altFilename = charText(alternate)
}
