package options

import (
	"strconv"
	"strings"
)

// Error is a vim option error: an E-code, the sentence after it and the ":set"
// argument that caused it.
//
// The code is what habits and scripts key off and it is matched exactly. So is
// the sentence, wherever this package raises one of vim's own codes: every
// message below was read out of vim 9.2 patches 1-321 by running the command
// inside a try/catch and writing v:exception to a file, because cmd/oracle
// diffs the message line and a sentence that is nearly right fails the same way
// a sentence that is nonsense does. D-002 in the register covers the other
// direction, an E-code pvim raises that vim has no equivalent for.
//
// Name is the argument as it was typed, abbreviation and value included, which
// is what vim quotes: ":set ts=abc" is "E521: Number required after =: ts=abc"
// and not "...: tabstop".
type Error struct {
	Code string
	Msg  string
	Name string
}

func (e *Error) Error() string {
	if e.Name == "" {
		return e.Code + ": " + e.Msg
	}
	return e.Code + ": " + e.Msg + ": " + e.Name
}

// The option E-codes, spelled as vim spells them.
func errUnknown(name string) error {
	return &Error{Code: "E518", Msg: "Unknown option", Name: name}
}

func errNumber(name string) error {
	return &Error{Code: "E521", Msg: "Number required after =", Name: name}
}

func errInvalid(name string) error {
	return &Error{Code: "E474", Msg: "Invalid argument", Name: name}
}

// errTrailing is E488, which ":set" answers for anything left over after an
// option name it has finished reading: ":set tabstop!" on a number, ":set ts&x"
// with a suffix that is neither "vi" nor "vim", ":set ts?extra".
func errTrailing(name string) error {
	return &Error{Code: "E488", Msg: "Trailing characters", Name: name}
}

// errRange is the message for a number below its option's minimum. Almost
// every option answers E487; 'scroll' has its own code because CTRL-U and
// CTRL-D write it from a count.
func errRange(option, arg string) error {
	code, msg := numMinCode(option)
	return &Error{Code: code, Msg: msg, Name: arg}
}

// errIllegalChar is E539, which a character-list option answers for a flag
// letter it does not have: ":set fo^=qz" is "E539: Illegal character <z>".
func errIllegalChar(c byte, name string) error {
	return &Error{Code: "E539", Msg: "Illegal character <" + string(c) + ">", Name: name}
}

// withArg puts the whole ":set" argument in an error's Name, which is what vim
// quotes. An error raised deep in the assignment path knows the option name and
// not the argument that reached it, so it is rewritten here, where the argument
// is still in scope.
func withArg(err error, arg string) error {
	if e, ok := err.(*Error); ok && e.Name != arg {
		clone := *e
		clone.Name = arg
		return &clone
	}
	return err
}

// Value is one option's value, whichever of the three types it has.
type Value struct {
	Kind Kind
	Bool bool
	Num  int
	Str  string
}

// String renders the part of a ":set name?" line that follows the name:
// "=8" for a number or a string, and nothing at all for a boolean, whose state
// is carried by the "no" prefix the caller writes instead.
func (v Value) String() string {
	switch v.Kind {
	case Bool:
		if v.Bool {
			return ""
		}
		return "no"
	case Number:
		return "=" + strconv.Itoa(v.Num)
	default:
		return "=" + v.Str
	}
}

// Get reads an option by long or short name.
//
// It answers what the editor acts on, which for a global-local option is the
// resolved value and not the raw local half: ":echo &scrolloff" in a window
// that never set one is 5 and not -1, and a vimrc that reads "if !&scrolloff"
// -- which this one does, on line 198 -- would take the wrong branch otherwise.
// GetRaw is the one that hands back the unset sentinel.
func (o *Options) Get(name string) (Value, error) {
	v, err := o.GetRaw(name, SetLocal)
	if err != nil {
		return v, err
	}
	s, ok := Lookup(name)
	if !ok {
		return v, nil
	}
	switch s.Scope {
	case ScopeGlobalBuffer, ScopeGlobalWindow:
		if unset(v) {
			return o.GetRaw(name, SetGlobal)
		}
	}
	return v, nil
}

// GetIn reads an option the way one of the three ":set" commands would.
//
// Both is ":set", which resolves a global-local option; SetLocal is
// ":setlocal", which does not, because ":setlocal scrolloff?" in a fresh window
// prints -1 in vim and printing 5 there would hide the difference between a
// window that has an opinion and one that does not.
func (o *Options) GetIn(name string, w Where) (Value, error) {
	if w == Both {
		return o.Get(name)
	}
	return o.GetRaw(name, w)
}

// unset reports whether a global-local option's local half is the sentinel
// meaning "ask the global". Vim spells it negative for a number and empty for a
// string, and this is the one place either is written down.
func unset(v Value) bool {
	switch v.Kind {
	case Number:
		return v.Num < 0
	case String:
		return v.Str == ""
	default:
		return false
	}
}

// GetRaw reads one half of one option with no fallback: the value the field
// holds, sentinel and all. It is what ":setlocal name?" prints and what a
// caller writing the field back needs.
func (o *Options) GetRaw(name string, w Where) (Value, error) {
	s, ok := Lookup(name)
	if !ok {
		if Accepted(name) {
			// An accepted-and-inert option has no field to read. Answering
			// with a zero value is a lie a vimrc's "if &compatible" would act
			// on, so this is the honest refusal instead.
			return Value{}, &Error{Code: "E518", Msg: "Option is accepted and inert, and has no value", Name: name}
		}
		return Value{}, errUnknown(name)
	}
	if w == Both {
		w = SetLocal
	}
	switch p := s.ref(o, w).(type) {
	case *bool:
		return Value{Kind: Bool, Bool: *p}, nil
	case *int:
		return Value{Kind: Number, Num: *p}, nil
	case *string:
		return Value{Kind: String, Str: *p}, nil
	default:
		return Value{}, errUnknown(name)
	}
}

// Set assigns one option by name, as ":set name=value" does.
//
// It is the plain form: no ?, !, &, +=, -= or ^=. Apply is the one that speaks
// the whole of vim's:set syntax and this is what it calls underneath. For a
// boolean, value is "" for on and "no" for off, which are the only two spellings
// ":set" itself has; anything else is E474, as ":set ignorecase=1" is in vim.
func (o *Options) Set(name, value string) error { return o.SetIn(name, value, Both) }

// SetIn assigns one option in one half of its scope: Both for ":set",
// SetLocal for ":setlocal", SetGlobal for ":setglobal".
func (o *Options) SetIn(name, value string, w Where) error {
	s, ok := Lookup(name)
	if !ok {
		if Accepted(name) {
			return nil // parsed and dropped on purpose; see the accepted map
		}
		return errUnknown(name)
	}
	v, err := parseValue(s, value, w)
	if err != nil {
		return err
	}
	o.write(s, v, w)
	return nil
}

// parseValue turns the text after "=" into a typed value and checks it, so
// that an option is never half-written: vim assigns first and validates after,
// and ":setglobal scrolloff=-1" leaves the global at 0 on its way to raising
// E487. Nothing reads a rejected value here.
func parseValue(s *Spec, value string, w Where) (Value, error) {
	switch s.Kind {
	case Bool:
		switch value {
		case "":
			return Value{Kind: Bool, Bool: true}, nil
		case "no":
			return Value{Kind: Bool}, nil
		default:
			// ":set ignorecase=1" is E474 in vim, not a boolean assignment,
			// and neither is ":set ic=on". A boolean has no value syntax at
			// all: it has "name", "noname" and "invname".
			return Value{}, errInvalid(s.Name)
		}
	case Number:
		n, ok := vimNumber(value)
		if !ok {
			return Value{}, errNumber(s.Name)
		}
		if err := checkRange(s, n, w); err != nil {
			return Value{}, err
		}
		return Value{Kind: Number, Num: n}, nil
	default:
		if err := checkString(s.Name, value); err != nil {
			return Value{}, err
		}
		return Value{Kind: String, Str: value}, nil
	}
}

// write puts a checked value into the halves one ":set" form writes.
//
// The rules are vim's and two of them are not guessable, so both were measured
// with ":setlocal name=x" followed by ":set name=y" and a read of
// &l:name and &g:name:
//
// - ":set" on a global-local NUMBER writes the global, and writes the local
// only when the window already had one. That is what keeps ":set so=3" in a
// vimrc from pinning every window ever opened to 3 instead of leaving them
// following the global.
// - ":set" on a global-local STRING writes the global and clears the local.
// Not the same rule, and vim does not explain why either.
func (o *Options) write(s *Spec, v Value, w Where) {
	if w != Both {
		put(s.ref(o, w), v)
		return
	}
	switch s.Scope {
	case ScopeGlobalBuffer, ScopeGlobalWindow:
		put(s.ref(o, SetGlobal), v)
		local := s.ref(o, SetLocal)
		if v.Kind == Number {
			if p, ok := local.(*int); ok && *p >= 0 {
				*p = v.Num
			}
			return
		}
		put(local, Value{Kind: String})
	default:
		put(s.ref(o, SetLocal), v)
		put(s.ref(o, SetGlobal), v)
	}
}

// put stores a value through the pointer the table handed back.
func put(p any, v Value) {
	switch p := p.(type) {
	case *bool:
		*p = v.Bool
	case *int:
		*p = v.Num
	case *string:
		*p = v.Str
	}
}

// vimNumber parses a number the way ":set" does, which is not strconv.Atoi.
//
// Measured: "0x10" is 16, "011" is 9, "0b11" is 3, "-1" parses and is then
// refused by the option's minimum, and "+4" is E521. So: an optional minus, a
// base from the prefix, and nothing after the digits. A leading plus is the one
// that catches people, because every other number in vim's grammar takes one.
//
// A value that does not fit in an int is refused rather than wrapped. Vim
// stores it in a long and lets it overflow; nobody has ever typed ":set
// laststatus=9223372036854775808" on purpose and the honest refusal is worth
// more than the faithful wrap.
func vimNumber(s string) (int, bool) {
	if s == "" || s == "-" || s[0] == '+' {
		return 0, false
	}
	neg := s[0] == '-'
	digits := s
	if neg {
		digits = s[1:]
	}

	base := 10
	switch {
	case len(digits) > 2 && digits[0] == '0' && (digits[1] == 'x' || digits[1] == 'X'):
		base, digits = 16, digits[2:]
	case len(digits) > 2 && digits[0] == '0' && (digits[1] == 'b' || digits[1] == 'B'):
		base, digits = 2, digits[2:]
	case len(digits) > 1 && digits[0] == '0':
		base, digits = 8, digits[1:]
	}

	n := 0
	for i := 0; i < len(digits); i++ {
		d := digitValue(digits[i])
		if d < 0 || d >= base {
			return 0, false
		}
		next := n*base + d
		if next < n {
			return 0, false // overflow
		}
		n = next
	}
	if neg {
		n = -n
	}
	return n, true
}

func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// checkRange applies the measured minimum and maximum for a number option.
//
// The one exception is the local half of a global-local number, where a
// negative is not a value at all but the marker for "this window has no
// opinion": ":setlocal scrolloff=-1" is how a window gives the option back to
// the global one, and vim takes it while refusing the same number from ":set"
// and ":setglobal". Measured both ways round.
//
// The argument named in the error is the option name here and the caller
// rewrites it to the whole ":set" argument, because that is what vim quotes.
func checkRange(s *Spec, n int, w Where) error {
	globalLocal := s.Scope == ScopeGlobalBuffer || s.Scope == ScopeGlobalWindow
	if globalLocal && w == SetLocal && n < 0 {
		return nil
	}
	if min, ok := numMin[s.Name]; ok && n < min {
		return errRange(s.Name, s.Name)
	}
	if max, ok := numMax[s.Name]; ok && n > max {
		return errInvalid(s.Name)
	}
	return nil
}

// checkString applies the measured legal-value set for a string option: E539
// for a character outside a flag list, E474 for an item outside a comma list.
// An option in neither table takes anything, which limits.go's unvalidated list
// spells out and explains.
func checkString(name, value string) error {
	if legal, ok := flagChars[name]; ok {
		for i := 0; i < len(value); i++ {
			if !strings.ContainsRune(legal, rune(value[i])) {
				return errIllegalChar(value[i], name)
			}
		}
		return nil
	}
	items, hasItems := listItems[name]
	prefixes, hasPrefixes := listPrefixes[name]
	if !hasItems && !hasPrefixes {
		return nil
	}
	for _, item := range splitList(value) {
		if contains(items, item) {
			continue
		}
		if word, _, found := strings.Cut(item, ":"); found && contains(prefixes, word) {
			continue
		}
		return errInvalid(name)
	}
	return nil
}

// Apply runs one ":set" argument, in vim's own syntax, and returns the line
// vim would print for it.
//
// The forms, all of them from ":help :set":
//
//	name boolean on; for a number or a string, show the value
//	noname boolean off
//	invname, name! boolean toggled
//	name? show the value
//	name& reset to vim's built-in default
//	name&vi reset to vi's default, which differs for eleven options
//	name&vim reset to vim's, which is the same as name&
//	name< take the global value: for a global-local option, forget the
//	 local one so that the global shows through again
//	name=v, name:v assign
//	name+=v add: a number sums, a comma list appends an item, a flag list
//	 moves the letters to the end
//	name-=v subtract: a number subtracts, a list removes the item
//	name^=v vim's "multiply or prepend": a number multiplies, a list gets
//	 the item put on the front
//
// The returned string is empty for anything that only sets, and is the
// " tabstop=2" or "nowrap" line for the forms that show. An unknown option is
// E518 and nothing is written.
func (o *Options) Apply(arg string, w Where) (string, error) {
	return o.apply(arg, arg, w)
}

// apply is Apply with the text vim would quote in an error held separately from
// the text it parses. ApplyLine passes an arg with the white space that
// followed it still attached, because that is what vim's message line carries:
// ":set ts=abc sw=9" is "E521: Number required after =: ts=abc ", trailing space
// and all.
func (o *Options) apply(arg, quoted string, w Where) (string, error) {
	if arg == "" {
		return "", nil
	}

	// ":set all&" resets every option. The other "all" forms print the whole
	// table and belong to whatever draws the message area, not here.
	if arg == "all&" {
		o.ResetAll()
		return "", nil
	}

	p, err := parse(arg)
	if err != nil {
		return "", withArg(err, quoted)
	}

	s, ok := Lookup(p.name)
	if !ok {
		if !Accepted(p.name) {
			return "", errUnknown(quoted)
		}
		if p.op == "?" {
			// Accepted and inert means there is no field, and answering with
			// a zero value is a lie a vimrc's "if &compatible" would act on.
			return "", &Error{Code: "E518", Msg: "Option is accepted and inert, and has no value", Name: quoted}
		}
		return "", nil // parsed and dropped on purpose; see the accepted map
	}

	switch p.op {
	case "?":
		line, err := o.show(p.name, w)
		return line, withArg(err, quoted)
	case "!":
		if s.Kind != Bool {
			// E488 and not E474: vim reads the "!" as something left over
			// after a name it has already finished with.
			return "", errTrailing(quoted)
		}
		return "", withArg(o.toggle(s, w), quoted)
	case "&", "&vim":
		return "", withArg(o.reset(s, w, false), quoted)
	case "&vi":
		return "", withArg(o.reset(s, w, true), quoted)
	case "<":
		return "", withArg(o.takeGlobal(s), quoted)
	}

	// The prefixes only mean anything on a boolean, and only on a boolean with
	// nothing after it. ":set notabstop" and ":set invtabstop" are both E474
	// and neither is a number assignment; so is ":set nowrap=1", on an option
	// that is a boolean, because a boolean takes no value.
	if p.prefix != 0 && (s.Kind != Bool || p.op != "") {
		return "", errInvalid(quoted)
	}
	switch p.prefix {
	case prefixNo:
		return "", withArg(o.SetIn(p.name, "no", w), quoted)
	case prefixInv:
		return "", withArg(o.toggle(s, w), quoted)
	}

	// A boolean has no value syntax: every operator that carries one is E474,
	// including ":set ic=" with nothing after the equals.
	if s.Kind == Bool && p.op != "" {
		return "", errInvalid(quoted)
	}

	switch p.op {
	case "":
		if s.Kind == Bool {
			return "", withArg(o.SetIn(p.name, "", w), quoted)
		}
		return o.show(p.name, w)
	case "=", ":":
		return "", withArg(o.SetIn(p.name, p.value, w), quoted)
	default:
		return "", withArg(o.applyOp(s, p.op, p.value, w), quoted)
	}
}

// ApplyLine runs a whole ":set" argument list and returns every line it would
// print.
//
// Arguments are separated by white space, and a backslash escapes a space
// inside a value: the vimrc's wildignore list has none but ":set
// listchars=tab:>\ " does, and a splitter that does not know about it drops half
// the value. It stops at the first error, as vim does, and returns what it
// printed up to that point, because vim applies the arguments before the bad one
// and keeps them.
func (o *Options) ApplyLine(args string, w Where) ([]string, error) {
	var out []string
	for _, arg := range splitArgs(args) {
		line, err := o.apply(arg.text, arg.text+arg.trail, w)
		if err != nil {
			return out, err
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// ResetAll is ":set all&": every option back to vim's built-in default.
func (o *Options) ResetAll() { *o = Builtin() }

// The two prefixes a boolean option name can carry.
const (
	prefixNo  = 'n'
	prefixInv = 'i'
)

// parsed is one ":set" argument taken apart: the option name with any prefix
// removed, the prefix, the operator and the value the operator carries.
type parsed struct {
	name   string
	prefix byte
	op     string
	value  string
}

// parse splits a ":set" argument into its parts.
//
// The name is the leading run of letters, digits and underscores, which is what
// vim scans and which takes in the termcap names like "t_vb". If what that
// scans is not an option and it starts with "no" or "inv", the prefix comes off
// and the rest is tried again -- in that order, so that an option really called
// "nonsense" would win over "no" plus "nsense". Then the rest of the argument is
// the operator, and anything that is not one of the eleven operators is E488.
func parse(arg string) (parsed, error) {
	i := 0
	for i < len(arg) && isNameByte(arg[i]) {
		i++
	}
	name, rest := arg[:i], arg[i:]

	p := parsed{name: name}
	if !known(name) {
		switch {
		case strings.HasPrefix(name, "no") && known(name[2:]):
			p.prefix, p.name = prefixNo, name[2:]
		case strings.HasPrefix(name, "inv") && known(name[3:]):
			p.prefix, p.name = prefixInv, name[3:]
		}
	}

	switch {
	case rest == "":
	case rest == "?", rest == "!", rest == "<", rest == "&", rest == "&vi", rest == "&vim":
		p.op = rest
	case strings.HasPrefix(rest, "+="), strings.HasPrefix(rest, "-="), strings.HasPrefix(rest, "^="):
		p.op, p.value = rest[:2], unescape(rest[2:])
	case rest[0] == '=' || rest[0] == ':':
		p.op, p.value = rest[:1], unescape(rest[1:])
	default:
		return p, errTrailing(arg)
	}
	return p, nil
}

// isNameByte reports whether a byte can be part of an option name. Underscore
// and digits are in because the termcap options are spelled "t_vb" and "t_k1".
func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// applyOp handles the three operators that combine a value with the one already
// there. The read is of the half the write will go to, so that ":setglobal
// wildignore+=*.o" adds to the global list and not to the local one.
func (o *Options) applyOp(s *Spec, op, value string, w Where) error {
	if s.Kind == Bool {
		return errInvalid(s.Name)
	}
	cur, err := o.GetIn(s.Name, w)
	if err != nil {
		return err
	}
	if s.Kind == Number {
		n, ok := vimNumber(value)
		if !ok {
			return errNumber(s.Name)
		}
		switch op {
		case "+=":
			n = cur.Num + n
		case "-=":
			n = cur.Num - n
		case "^=":
			// Not a typo and not prepend: for a number option vim's ^=
			// multiplies. ":set sw^=2" with shiftwidth 4 gives 8.
			n = cur.Num * n
		}
		return o.SetIn(s.Name, strconv.Itoa(n), w)
	}
	return o.SetIn(s.Name, listOp(s.Name, cur.Str, op, value), w)
}

// listOp applies +=, -= or ^= to a string option.
//
// The two shapes are the whole of what those operators mean and the vimrc uses
// both: line 83's "formatoptions-=t" takes one letter out of a flag list, and a
// "wildignore-=*.o" would take a whole comma-separated item out. An
// implementation that treats every string option as a string gets one of them
// wrong, and the one it gets wrong is the one that leaves 'formatoptions' empty
// and wraps every line typed past the seventy-ninth column.
func listOp(name, cur, op, value string) string {
	if _, ok := flagChars[name]; ok {
		switch op {
		case "+=":
			// Measured, and not what it looks like: ":set fo+=t" on "tcq"
			// gives "cqt", not "tcq". Vim takes the added letters out of where
			// they were and puts them on the end.
			return removeChars(cur, value) + value
		case "-=":
			return removeChars(cur, value)
		default: // ^=
			return removeChars(value, cur) + cur
		}
	}
	items := splitList(cur)
	switch op {
	case "+=":
		for _, v := range splitList(value) {
			if !contains(items, v) {
				items = append(items, v)
			}
		}
	case "-=":
		for _, v := range splitList(value) {
			items = remove(items, v)
		}
	default: // ^=
		add := splitList(value)
		for _, v := range items {
			if !contains(add, v) {
				add = append(add, v)
			}
		}
		items = add
	}
	return strings.Join(items, ",")
}

// show renders the line ":set name?" prints.
//
// Vim prints the long name whatever name it was asked by, so ":set ts?" is
// " tabstop=8". A boolean that is off has its "no" where the two spaces would
// be and is not indented at all: "nowrap", not " nowrap".
func (o *Options) show(name string, w Where) (string, error) {
	s, ok := Lookup(name)
	if !ok {
		if Accepted(name) {
			return "", &Error{Code: "E518", Msg: "Option is accepted and inert, and has no value", Name: name}
		}
		return "", errUnknown(name)
	}
	v, err := o.GetIn(s.Name, w)
	if err != nil {
		return "", err
	}
	if v.Kind == Bool && !v.Bool {
		return "no" + s.Name, nil
	}
	return "  " + s.Name + v.String(), nil
}

// toggle flips a boolean, which is what ":set name!" and ":set invname" both do.
func (o *Options) toggle(s *Spec, w Where) error {
	v, err := o.GetIn(s.Name, w)
	if err != nil {
		return err
	}
	if v.Bool {
		return o.SetIn(s.Name, "no", w)
	}
	return o.SetIn(s.Name, "", w)
}

// reset puts an option back to a default: ":set name&" and ":set name&vim" to
// vim's built-in one, ":set name&vi" to vi's.
//
// Builtin and not Defaults: ":set scrolloff&" is 0 in vim and this editor
// starts at 5, because defaults.vim set it. Resetting to the value the editor
// happened to start with would make ":set name&" mean "undo my changes", which
// is a different and much less useful thing.
//
// The local half of a global-local option gets vim's own inconsistency, both
// halves of it measured: ":set scrolloff&" leaves the local at 0 and ":set
// completeopt&" leaves it empty, so a number takes the global default and a
// string takes the unset marker.
func (o *Options) reset(s *Spec, w Where, vi bool) error {
	def := Builtin()
	v, err := def.GetRaw(s.Name, SetGlobal)
	if err != nil {
		return err
	}
	if vi {
		if text, ok := viDefaults[s.Name]; ok {
			if v, err = parseValue(s, text, SetGlobal); err != nil {
				return err
			}
		}
	}

	switch s.Scope {
	case ScopeGlobalBuffer, ScopeGlobalWindow:
		if w != SetLocal {
			put(s.ref(o, SetGlobal), v)
		}
		if w != SetGlobal {
			if v.Kind == Number {
				put(s.ref(o, SetLocal), v)
			} else {
				put(s.ref(o, SetLocal), Value{Kind: String})
			}
		}
	default:
		o.write(s, v, w)
	}
	return nil
}

// takeGlobal is ":set name<": the local value gives up and lets the global one
// through.
//
// For a buffer-local or window-local option that means copying the global value
// down, because there is nothing else it could mean. For a global-local one it
// means putting the unset marker back, so that a later ":setglobal" is seen
// here too; copying's global value down would freeze it instead.
func (o *Options) takeGlobal(s *Spec) error {
	switch s.Scope {
	case ScopeGlobalBuffer, ScopeGlobalWindow:
		switch p := s.ref(o, SetLocal).(type) {
		case *int:
			*p = -1
		case *string:
			*p = ""
		}
		return nil
	}
	v, err := o.GetRaw(s.Name, SetGlobal)
	if err != nil {
		return err
	}
	put(s.ref(o, SetLocal), v)
	return nil
}

// known reports whether a name is an option at all: implemented, or accepted
// and inert. It is what tells ":set noexpandtab" from an option really called
// "noexpandtab", and it has to see the accepted list or ":set nocompatible"
// falls through to E518.
func known(name string) bool {
	if _, ok := Lookup(name); ok {
		return true
	}
	return Accepted(name)
}

// arg is one ":set" argument and the white space that followed it, which is
// what vim quotes back in an error message.
type arg struct {
	text  string
	trail string
}

// splitArgs splits a ":set" argument list on unescaped white space.
func splitArgs(s string) []arg {
	var out []arg
	var cur strings.Builder
	flush := func(trail string) {
		if cur.Len() > 0 {
			out = append(out, arg{cur.String(), trail})
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\' && i+1 < len(s):
			cur.WriteByte(c)
			i++
			cur.WriteByte(s[i])
		case c == ' ' || c == '\t':
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			flush(s[i:j])
			i = j - 1
		default:
			cur.WriteByte(c)
		}
	}
	flush("")
	return out
}

// unescape drops the backslashes splitArgs kept, so that a value reaches the
// field as the characters it stands for.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// splitList splits a comma-separated option value, dropping the empty string an
// empty option would otherwise produce.
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func contains(items []string, v string) bool {
	for _, item := range items {
		if item == v {
			return true
		}
	}
	return false
}

func remove(items []string, v string) []string {
	out := items[:0]
	for _, item := range items {
		if item != v {
			out = append(out, item)
		}
	}
	return out
}

// removeChars returns s without any character that appears in drop.
func removeChars(s, drop string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(drop, rune(s[i])) {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
