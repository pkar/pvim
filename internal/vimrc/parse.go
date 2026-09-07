package vimrc

import (
	"fmt"
	"strings"

	"github.com/pkar/pvim/internal/key"
)

// cmdKind is which statement a command name produces.
type cmdKind uint8

const (
	kindSet cmdKind = iota
	kindLet
	kindUnlet
	kindMap
	kindUnmap
	kindMapClear
	kindAutoCmd
	kindAugroup
	kindCommand
	kindDelCommand
	kindIf
	kindElseIf
	kindElse
	kindEndIf
	kindColorscheme
	kindHighlight
	kindSource
	kindFinish
	kindFunction
	kindEndFunction
	kindCall
	kindFileType
	kindSyntax
	kindInert
	kindExecute
	kindPack
	kindEx
)

// barRule is where one command's arguments stop.
//
// It is two of vim's EX_ flags and not one property, which is the whole of
// this type's reason to exist. A single "literal" flag conflated them and got
// the map family wrong in both directions at once: a bar in a mapping ended it
// in vim and did not here, so the mapping got a right-hand side vim never gave
// it and the command after the bar silently never ran.
type barRule uint8

const (
	// barComment is EX_TRLBAR without EX_NOTRLCOM, which is most of the
	// table: an unescaped "|" starts the next command and an unescaped
	// double quote starts a comment that runs to the end of the line.
	// Measured `set ts=8 " a " | set sw=8` sets 'tabstop' to 8
	// and leaves 'shiftwidth' where it was.
	barComment barRule = iota

	// barOnly is EX_TRLBAR with EX_NOTRLCOM, which is the map family and
	// nothing else here. A bar ends the mapping; a double quote does not,
	// which is why the vimrc's `nnoremap <Space> za " Spacebar to unfold`
	// maps to the comment and all. Measured
	// `nnoremap <F2> :echo "a" | set sw=8` leaves maparg('<F2>','n') as
	// `:echo "a" ` -- trailing space and no bar -- and 'shiftwidth' at 8,
	// and writing the bar as `\|` or <Bar> puts one in the mapping instead.
	barOnly

	// barNone is a command with no EX_TRLBAR at all :autocmd, :command,
	// :normal, :function and the :unmap family take the rest of the line,
	// bars and quotes and all. It is what makes the vimrc's BufWritePost
	// line ONE autocommand whose command has three bars in it, and what
	// makes `nunmap <F3> | set sw=9` an E31 for a mapping whose name is
	// `<F3> | set sw=9`.
	barNone

	// barExpr has no EX_TRLBAR either, but the command's own expression
	// parser decides where the arguments stop and what follows them :if,
	// :elseif and :let. Vim's ends_excmd2 ends the command at a bar, a
	// double quote or the end of the line, and check_nextcmd takes what is
	// after a bar and nothing else, so `let g:x = "a|b" | set et` sets both
	// and `let g:x = 1 " a " | set sw=8` sets only g:x. Measured.
	barExpr

	// barString is :call, whose argument is an expression this package does
	// not parse: it reads the function name and drops the rest. Splitting
	// on a bar outside a quoted string is the closest thing to vim's answer
	// that does not need the parser.
	barString
)

// cmdSpec is one entry of the command table.
//
// The abbreviations are vim's own, read out of the installed binary on
// with fullcommand() rather than copied from map.txt, which caught
// the one that is not what it looks like: ":sm" is :smagic and not :smap, so
// :smap's shortest form is ":sma".
type cmdSpec struct {
	// name is the full command name and min the shortest abbreviation of it
	// vim accepts. A typed name matches when it is a prefix of name and at
	// least min bytes long, which is exactly vim's rule and is unambiguous
	// on its own: "se" is :set because :setlocal needs four.
	name string
	min  int
	kind cmdKind

	// bar is where the command's arguments stop.
	bar barRule

	// scope is which ":set" form this is.
	scope SetScope
	// modes and noremap are the map family's.
	modes   string
	noremap bool
}

// commands is the whole vimscript subset, one row per command name.
//
// A command that is not here is E492 on the message line and startup
// continues. That is the dividing rule as a table: adding a row is a decision
// to run something, and being out of scope is the reason most of
// vim's 500 commands have no row.
var commands = []cmdSpec{
	{name: "autocmd", min: 2, kind: kindAutoCmd, bar: barNone},
	{name: "augroup", min: 3, kind: kindAugroup},
	{name: "call", min: 3, kind: kindCall, bar: barString},
	{name: "cmap", min: 2, kind: kindMap, bar: barOnly, modes: "c"},
	{name: "cmapclear", min: 5, kind: kindMapClear, modes: "c"},
	{name: "cnoremap", min: 3, kind: kindMap, bar: barOnly, modes: "c", noremap: true},
	{name: "colorscheme", min: 4, kind: kindColorscheme},
	{name: "command", min: 3, kind: kindCommand, bar: barNone},
	{name: "cunmap", min: 2, kind: kindUnmap, bar: barNone, modes: "c"},
	{name: "delcommand", min: 4, kind: kindDelCommand},
	{name: "else", min: 2, kind: kindElse},
	{name: "elseif", min: 5, kind: kindElseIf, bar: barExpr},
	{name: "endif", min: 2, kind: kindEndIf},
	{name: "endfunction", min: 4, kind: kindEndFunction},
	// ":execute" is pvim's restricted one, not vim's. See the package doc for
	// what "restricted" means and the Loader's doExecute for where it stops.
	// barExpr because vim's does end at a bar: measured,
	// "execute 'set sw=3' | set ts=9" sets both.
	{name: "execute", min: 3, kind: kindExecute, bar: barExpr},
	{name: "filetype", min: 5, kind: kindFileType},
	{name: "finish", min: 4, kind: kindFinish},
	{name: "function", min: 2, kind: kindFunction, bar: barNone},
	{name: "highlight", min: 2, kind: kindHighlight},
	{name: "if", min: 2, kind: kindIf, bar: barExpr},
	{name: "imap", min: 2, kind: kindMap, bar: barOnly, modes: "i"},
	{name: "imapclear", min: 5, kind: kindMapClear, modes: "i"},
	{name: "inoremap", min: 3, kind: kindMap, bar: barOnly, modes: "i", noremap: true},
	{name: "iunmap", min: 2, kind: kindUnmap, bar: barNone, modes: "i"},
	{name: "let", min: 3, kind: kindLet, bar: barExpr},
	{name: "map", min: 3, kind: kindMap, bar: barOnly, modes: "nvo"},
	{name: "mapclear", min: 4, kind: kindMapClear, modes: "nvo"},
	{name: "nmap", min: 2, kind: kindMap, bar: barOnly, modes: "n"},
	{name: "nmapclear", min: 5, kind: kindMapClear, modes: "n"},
	{name: "nnoremap", min: 2, kind: kindMap, bar: barOnly, modes: "n", noremap: true},
	{name: "nohlsearch", min: 3, kind: kindEx},
	{name: "noremap", min: 2, kind: kindMap, bar: barOnly, modes: "nvo", noremap: true},
	{name: "normal", min: 4, kind: kindEx, bar: barNone},
	{name: "nunmap", min: 3, kind: kindUnmap, bar: barNone, modes: "n"},
	{name: "omap", min: 2, kind: kindMap, bar: barOnly, modes: "o"},
	{name: "omapclear", min: 5, kind: kindMapClear, modes: "o"},
	{name: "onoremap", min: 3, kind: kindMap, bar: barOnly, modes: "o", noremap: true},
	{name: "ounmap", min: 2, kind: kindUnmap, bar: barNone, modes: "o"},
	// ":packadd" has no row and is E492 on purpose: it names one package to
	// load, and pvim loads no package. ":packloadall" has one because what it
	// does HERE is put the pack directories on 'runtimepath', which is how the
	// colourscheme on the next line of the live vimrc is found. The shortest
	// form is five letters, because ":pa" through ":packad" are all
	// ":packadd"; measured with fullcommand().
	{name: "packloadall", min: 5, kind: kindPack},
	{name: "set", min: 2, kind: kindSet, scope: SetBoth},
	{name: "setglobal", min: 4, kind: kindSet, scope: SetGlobalScope},
	{name: "setlocal", min: 4, kind: kindSet, scope: SetLocalScope},
	// ":silent" and ":silent!" take a command and run it with messages
	// suppressed, so the whole of the rest of the line belongs to internal/ex
	// and not to this parser. It is here because the vimrc's BufWritePre
	// autocommand body is "silent! %s/\s\+$//e" and cmd/pvim runs an
	// autocommand body through this loader: without the row the strip-trailing-
	// whitespace rule would be E492 on every write of a .go file.
	{name: "silent", min: 3, kind: kindEx, bar: barNone},
	{name: "smap", min: 3, kind: kindMap, bar: barOnly, modes: "s"},
	{name: "smapclear", min: 5, kind: kindMapClear, modes: "s"},
	{name: "snoremap", min: 4, kind: kindMap, bar: barOnly, modes: "s", noremap: true},
	{name: "source", min: 2, kind: kindSource},
	{name: "sunmap", min: 4, kind: kindUnmap, bar: barNone, modes: "s"},
	{name: "syntax", min: 2, kind: kindSyntax},
	{name: "unlet", min: 3, kind: kindUnlet},
	{name: "unmap", min: 3, kind: kindUnmap, bar: barNone, modes: "nvo"},
	{name: "vmap", min: 2, kind: kindMap, bar: barOnly, modes: "v"},
	{name: "vmapclear", min: 5, kind: kindMapClear, modes: "v"},
	{name: "vnoremap", min: 2, kind: kindMap, bar: barOnly, modes: "v", noremap: true},
	{name: "vunmap", min: 2, kind: kindUnmap, bar: barNone, modes: "v"},
	{name: "xmap", min: 2, kind: kindMap, bar: barOnly, modes: "x"},
	{name: "xmapclear", min: 5, kind: kindMapClear, modes: "x"},
	{name: "xnoremap", min: 2, kind: kindMap, bar: barOnly, modes: "x", noremap: true},
	{name: "xunmap", min: 2, kind: kindUnmap, bar: barNone, modes: "x"},
}

// rangeCmd is the synthetic spec for a line that begins with a range rather
// than a command name. It has no name and takes the whole line, which is what
// hands it to internal/ex intact.
var rangeCmd = cmdSpec{kind: kindEx, bar: barNone}

// isRangeByte reports whether a byte can open an ex range.
//
// Vim's get_address reads these: a line number, "." for the current line, "$"
// for the last, "%" for the whole file, "'" and a mark, "/" or "?" and a
// pattern, and "+" or "-" for an offset from the current line. "," and ";" are
// separators and can start a range whose first half is implied.
//
// Vim's "\/", "\?" and "\&" -- the last search pattern and the last
// substitute pattern as addresses -- are deliberately not here. Nothing has
// ever written one, and a leading backslash in a vimrc is a continuation line
// that failed to join: answering E492 for that says what went wrong and
// handing it to internal/ex would answer something about an address instead.
func isRangeByte(b byte) bool {
	switch b {
	case '%', '.', '$', '\'', '/', '?', ',', ';', '+', '-':
		return true
	}
	return b >= '0' && b <= '9'
}

// lookupCmd resolves a typed command name.
func lookupCmd(typed string) (cmdSpec, bool) {
	for _, c := range commands {
		if len(typed) >= c.min && len(typed) <= len(c.name) && strings.HasPrefix(c.name, typed) {
			return c, true
		}
	}
	return cmdSpec{}, false
}

// header is a command line's name and its arguments, before anything knows
// what the command means.
type header struct {
	spec  cmdSpec
	typed string
	bang  bool
	args  string
	// rest is the command line after this command's "|", empty when there is
	// none or when the command swallows the line.
	rest string
	ok   bool
}

// parseHeader reads one command off the front of a line.
//
// Leading colons and white space are dropped, which is what lets an autocmd's
// command be written ":%s/\s\+$//e" with the colon on and still parse when it
// is run. A name that resolves to a command that does not swallow the line is
// cut at its first bar, so a line runs as several commands.
func parseHeader(line string) header {
	s := strings.TrimLeft(line, " \t:")

	end := 0
	for end < len(s) && isAlphaByte(s[end]) {
		end++
	}
	if end == 0 {
		if len(s) > 0 && isRangeByte(s[0]) {
			// A line that opens with a range and not a name: ":%s/x//",
			// ":1,3d", ":.!jq". This parser has no ranges in it -- ranges are
			// internal/ex's, with marks and patterns and offsets and ";" --
			// so the whole line goes there as one ex command.
			//
			// Nothing in either vimrc starts a top-level line this way. What
			// does is an autocommand BODY, and cmd/pvim runs those through
			// this loader: the preserved vimrc's nested rule is
			//
			//	autocmd Filetype python,java,javascript,go autocmd BufWritePre * :%s/\s\+$//e
			//
			// whose body is exactly this shape, and without this it was E492
			// on every write of a Go file.
			return header{spec: rangeCmd, args: s, ok: true}
		}
		return header{}
	}
	typed := s[:end]
	rest := s[end:]

	bang := strings.HasPrefix(rest, "!")
	if bang {
		rest = rest[1:]
	}

	spec, ok := lookupCmd(typed)
	if !ok {
		return header{}
	}

	h := header{spec: spec, typed: typed, bang: bang, ok: true}

	var head, tail string
	var found bool
	switch spec.bar {
	case barNone, barExpr:
		// The command takes the rest of the line. For barExpr the handler
		// gives back whatever the expression parser left, which is the only
		// thing that can tell `let x = "a|b"` from `let x = 1 | set et`.
		h.args = strings.TrimLeft(rest, " \t")
		return h
	case barString:
		head, tail, found = splitBar(rest)
	default:
		head, tail, found = separateNextCmd(rest, spec.bar == barComment)
	}
	h.args = strings.TrimLeft(head, " \t")
	if found {
		h.rest = tail
	}
	return h
}

func isAlphaByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// parseHighlight reads the arguments of ":hi" and ":highlight".
//
// The four nofrils files are 189 lines each and 160 of them are this command,
// so the shapes it has to take are exactly the shapes those files use: "hi
// clear", "hi {group} key=value...", and "hi link A B" with an optional
// "default" in front of it.
func parseHighlight(args string, pos Pos) (Highlight, error) {
	h := Highlight{Pos: pos, Args: map[string]string{}}

	word, rest := splitWord(args)
	if strings.EqualFold(word, "default") {
		h.Default = true
		word, rest = splitWord(rest)
	}
	switch {
	case word == "":
		// A bare ":hi" lists every group. In a script it is a no-op with a
		// screenful of output, and pvim has nowhere to put the output at
		// startup, so it is a clear of nothing rather than an error.
		h.Clear = true
		return h, nil
	case strings.EqualFold(word, "clear"):
		h.Clear = true
		h.Group, _ = splitWord(rest)
		return h, nil
	case strings.EqualFold(word, "link"):
		from, to := splitWord(rest)
		to, _ = splitWord(to)
		if from == "" || to == "" {
			return h, fmt.Errorf("E412: Not enough arguments: highlight link %s", rest)
		}
		h.Group, h.Link = from, to
		return h, nil
	}

	h.Group = word
	for rest != "" {
		var arg string
		arg, rest = splitWord(rest)
		k, v, found := strings.Cut(arg, "=")
		if !found {
			return h, fmt.Errorf("E416: Missing equal sign: %s", arg)
		}
		h.Args[strings.ToLower(k)] = v
	}
	return h, nil
}

// parseCommandDef reads the arguments of ":command!".
//
// The vimrc defines one, and it is the whole of the feature that has to work:
//
//	command! -range -nargs=0 -bar JsonPretty <line1>,<line2>!jq --sort-keys '.'
//
// The attributes are kept as written rather than parsed into fields, because
// what they mean is internal/ex's business: this package's job is to know
// where they stop and the name starts.
func parseCommandDef(args string, bang bool, pos Pos) (Command, error) {
	c := Command{Pos: pos, Bang: bang}

	rest := args
	for strings.HasPrefix(rest, "-") {
		var attr string
		attr, rest = splitWord(rest)
		c.Attrs = append(c.Attrs, attr)
	}
	name, repl := splitWord(rest)
	if name == "" {
		// A bare ":command" lists the user commands, which at startup is a
		// screenful nobody asked for and not an error.
		return c, nil
	}
	if name[0] < 'A' || name[0] > 'Z' {
		return c, fmt.Errorf("E183: User defined commands must start with an uppercase letter: %s", name)
	}
	c.Name, c.Repl = name, repl
	return c, nil
}

// parseAutoCmd reads the arguments of ":autocmd".
//
// Vim's grammar is ":au[tocmd] [group] {event} {pat} [++once] [++nested]
// {cmd}", where the group is told from the event by looking the first word up
// in the event table. That lookup is why events.go holds all 130 of vim's
// event names and not the eight this vimrc uses: a name missing from the
// table turns its event into an augroup and drops the autocommand on the
// floor without a word.
func isEventList(word string) bool {
	// The first word is an event list when every comma-separated part of it is
	// an event, which is what ":au BufNewFile,BufRead *.go ..." on line 159 of
	// the vimrc is. Asking about the whole word instead was a bug for exactly
	// as long as it took to run the file: "BufNewFile,BufRead" is not an event
	// name, so the line registered an autocommand in an augroup called
	// "BufNewFile,BufRead" for an event called "*.go".
	if word == "" {
		return false
	}
	if word == "*" {
		return true
	}
	for _, name := range strings.Split(word, ",") {
		if _, ok := canonicalEvent(name); !ok {
			return false
		}
	}
	return true
}

func parseAutoCmd(args string, bang bool, group string, pos Pos) (AutoCmd, error) {
	a := AutoCmd{Pos: pos, Group: group, Clear: bang}

	word, rest := splitWord(args)
	if word == "" {
		// ":au" lists, ":au!" with no arguments removes every autocommand in
		// the current group, which is what the "au!" at the top of the
		// vimrc's three augroups does.
		return a, nil
	}

	if !isEventList(word) {
		a.Group = word
		word, rest = splitWord(rest)
	}
	if word == "" {
		return a, nil
	}

	if word != "*" {
		for _, name := range strings.Split(word, ",") {
			canonical, ok := canonicalEvent(name)
			if !ok {
				return a, fmt.Errorf("E216: No such group or event: %s", name)
			}
			a.Events = append(a.Events, canonical)
		}
	}

	if rest == "" {
		return a, nil
	}
	word, rest = splitWord(rest)
	a.Patterns = strings.Split(word, ",")

	for {
		flag, tail := splitWord(rest)
		switch strings.ToLower(flag) {
		case "++once":
			a.Once = true
		case "++nested", "nested":
			a.Nested = true
		default:
			a.Cmd = rest
			return a, nil
		}
		rest = tail
	}
}

// parseMap reads the arguments of one of the map family.
//
// leader and localLeader are substituted into the left-hand side here and not
// later, because that is when vim does it: 'mapleader' is read at the moment
// the :map command runs, so the vimrc setting it on line 58 and using
// <leader> on line 125 is the only order that gives ",".
func parseMap(spec cmdSpec, args string, bang bool, leader, localLeader string, pos Pos) (Map, error) {
	m := Map{Pos: pos, Modes: spec.modes, NoRemap: spec.noremap}
	if bang {
		// ":map!" is insert and command-line mode, which is a different pair
		// of modes and not a variant of the same one.
		m.Modes = "ic"
	}

	switch spec.kind {
	case kindMapClear:
		m.Clear, m.Unmap = true, true
		return m, nil
	case kindUnmap:
		m.Unmap = true
	}

	mods, rest := key.ScanMapArgs(args)
	m.Buffer, m.Silent, m.Expr = mods.Buffer, mods.Silent, mods.Expr
	m.Unique, m.Nowait = mods.Unique, mods.NoWait

	lhs, rhs := splitLHS(rest)
	if lhs == "" {
		// A bare ":nmap" lists the mappings. Nothing in a vimrc wants that
		// and there is nowhere to print it at startup.
		return m, fmt.Errorf("E474: listing mappings is not supported: %s", spec.name)
	}
	m.LHSText = lhs
	keys, err := key.ParseLeader(lhs, leader, localLeader)
	if err != nil {
		return m, err
	}
	m.LHS = keys

	if m.Unmap {
		return m, nil
	}
	if rhs == "" {
		return m, fmt.Errorf("E474: Invalid argument: %s %s", spec.name, args)
	}
	m.RHS = rhs
	return m, nil
}
