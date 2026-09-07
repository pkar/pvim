package ex

import "strings"

// Handler runs one command. It is given the editor state and the parsed
// command, and returns an error whose text goes on the message line.
type Handler func(ctx *Context, c Cmd) error

// RangeKind says what range a command takes.
type RangeKind uint8

// The range kinds, from the flags in vim's own ex_cmds.h.
const (
	// RangeNone is a command that takes no range: ":set", ":edit". A range in
	// front of one is E481.
	RangeNone RangeKind = iota
	// RangeLine is a command that takes a line range and defaults to the
	// current line: ":delete", ":substitute", ":join".
	RangeLine
	// RangeFile is a command that takes a range and defaults to the whole
	// file: ":write", ":global", ":print" under some spellings.
	RangeFile
	// RangeCount is a command whose range is really a count: ":tabnext 3" is
	// the third tab and not lines one to three.
	RangeCount
)

// ArgKind says what a command does with its arguments.
type ArgKind uint8

// The argument kinds. They decide what wildmenu completes after the name and
// nothing else; a Handler still parses its own arguments.
const (
	ArgNone ArgKind = iota
	ArgText
	ArgFile
	ArgDir
	ArgBuffer
	ArgOption
	ArgCommand
	ArgHighlight
	ArgTag
	ArgPattern
	ArgColorscheme
)

// Command is one entry of the built-in command table.
type Command struct {
	// Name is the full name, which is what Cmd.Name is set to.
	Name string
	// Range and Args are the shapes above.
	Range RangeKind
	Args  ArgKind
	// Bang says a "!" is allowed. A bang on a command without one is E477.
	Bang bool
	// Reg says the first argument may be a register name, which is what makes
	// ":d a" delete into register a. Vim's EX_REGSTR.
	//
	// The rule is one character and no separator, which is why ":y foo" yanks
	// into register f and then answers E488 for "oo". Measured: ":1,2 d extra"
	// says "E488: Trailing characters: xtra", with the e gone.
	Reg bool
	// Count says a trailing number turns the range into that many lines
	// starting at its last line. Vim's EX_COUNT. It also stops a digit being
	// read as a register, which is why ":d 3" deletes three lines rather than
	// deleting into register 3.
	Count bool
	// Dest says the command takes a destination address after its name:
	// ":m", ":copy" and ":t".
	Dest bool
	// Zero says a line 0 address is meaningful rather than being bumped to
	// line 1. Vim's EX_ZEROR, and the reason ":0put" puts above the first
	// line while ":0d" deletes it.
	Zero bool
	// Handler runs it. Nil means the command is in the table so that it
	// parses and resolves, and answers "not implemented" when run, which is a
	// better failure than E492 for a command pvim means to support.
	Handler Handler
}

// commands is the built-in command table, in resolution order.
//
// # How a name resolves
//
// Vim resolves an abbreviated command by scanning its table in order and
// taking the first entry the typed text is a prefix of. The table's order is
// therefore the specification: it is what makes ":e" mean ":edit" and not
// ":earlier", ":n" mean ":next" and not ":new", and ":c" mean ":change" and
// not ":copy". It is roughly alphabetical with the commands people type most
// pulled in front of their neighbours, and it is not derivable from anything.
//
// So this table is ordered to reproduce vim's answers, and the answers were
// measured rather than guessed: fullcommand() in vim 9.2.0321
// gives e->edit, s->substitute, w->write, q->quit, b->buffer, c->change,
// cl->clist, cn->cnext, cp->cprevious, co->copy, cw->cwindow, n->next,
// d->delete, y->yank, p->print, r->read, f->file, h->help, g->global,
// v->vglobal, j->join, l->list, x->xit, u->undo, sp->split, vs->vsplit,
// on->only, clo->close, bd->bdelete, se->set, ma->mark, no->noremap,
// au->autocmd, so->source, hi->highlight, norm->normal, ta->tag, wq->wq and
// qa->qall. TestMeasuredAbbreviations asserts every one of them, so an entry
// added in the wrong place fails a test rather than quietly stealing ":c".
//
// Three of vim's rules are not prefix matching at all and are handled in
// Lookup rather than by ordering:
//
// - ":s" swallows its own flags. ":sg", ":sr" and ":si" are all
// :substitute with the flag letters written against the name, which is
// why fullcommand('si') answers "substitute" even though "si" is not a
// prefix of it.
// - ":k" and ":t" are one-letter commands with no longer form, and a
// following character is an argument: ":ka" sets mark a.
// - A name that starts with an uppercase letter is a user command and never
// a built-in, which is what keeps ":JsonPretty" out of this table.
//
// The Handler column is filled in handlers.go and not here, so that a reader
// checking this order against vim's sees names and flags and nothing else. A
// name with no handler answers E319 when it is run, which is not the same
// thing as E492: E492 means vim has no such command either, and E319 means
// pvim knows this one and has not written it yet.
//
// The map family is here except for select mode. ":smap", ":snoremap",
// ":sunmap" and ":smapclear" are left out on purpose and not by oversight:
// pvim has no select mode, and putting them in the table would make ":sm",
// ":sn" and ":sno" resolve to them where vim answers smagic, snext and
// snomagic. Three wrong abbreviations bought for four commands that would do
// nothing is a bad trade; the day select mode exists, smagic, snext and
// snomagic go in first and these four go in behind them.
var commands = []Command{
	{Name: "append", Range: RangeLine, Bang: true},
	{Name: "abbreviate", Args: ArgText},
	{Name: "aboveleft", Args: ArgCommand},
	{Name: "all", Range: RangeCount},
	{Name: "argument", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "autocmd", Bang: true, Args: ArgText},
	{Name: "augroup", Args: ArgText},

	{Name: "buffer", Range: RangeCount, Bang: true, Args: ArgBuffer},
	// "buffers" is ":ls" under another name. It sits after "buffer" because
	// vim answers buffer for "buf" and buffers only for the whole word,
	// measured with fullcommand().
	{Name: "buffers", Args: ArgText},
	{Name: "ball", Range: RangeCount},
	{Name: "bdelete", Range: RangeCount, Bang: true, Args: ArgBuffer},
	{Name: "belowright", Args: ArgCommand},
	{Name: "bfirst", Bang: true},
	{Name: "blast", Bang: true},
	{Name: "bnext", Range: RangeCount, Bang: true},
	{Name: "botright", Args: ArgCommand},
	{Name: "bprevious", Range: RangeCount, Bang: true},
	{Name: "browse", Args: ArgCommand},

	{Name: "change", Range: RangeLine, Bang: true},
	{Name: "cc", Range: RangeCount, Bang: true},
	{Name: "cclose", Range: RangeCount},
	{Name: "cd", Bang: true, Args: ArgDir},
	{Name: "center", Range: RangeLine, Count: true},
	{Name: "cfile", Bang: true, Args: ArgFile},
	{Name: "cfirst", Range: RangeCount, Bang: true},
	{Name: "cgetfile", Args: ArgFile},
	{Name: "clist", Range: RangeCount, Bang: true},
	{Name: "clast", Range: RangeCount, Bang: true},
	{Name: "close", Range: RangeCount, Bang: true},
	{Name: "cmap", Args: ArgText},
	{Name: "cmapclear", Args: ArgText},
	{Name: "cnext", Range: RangeCount, Bang: true},
	// fullcommand('cn') is cnext and fullcommand('cnf') is cnfile, measured.
	{Name: "cnfile", Range: RangeCount, Bang: true},
	{Name: "cnoremap", Args: ArgText},
	{Name: "cunmap", Args: ArgText},
	{Name: "copy", Range: RangeLine, Args: ArgText, Dest: true, Zero: true},
	// After "copy" and not beside "cclose", because the table is in vim's own
	// resolution order and a prefix takes the first row that matches: vim
	// answers "copy" for both "co" and "cop" and only reaches "copen" at
	// "cope". Measured with fullcommand().
	{Name: "copen", Range: RangeCount},
	{Name: "colorscheme", Args: ArgColorscheme},
	{Name: "command", Bang: true, Args: ArgCommand},
	{Name: "confirm", Args: ArgCommand},
	{Name: "cprevious", Range: RangeCount, Bang: true},
	{Name: "cquit", Range: RangeCount, Bang: true},
	{Name: "cwindow", Range: RangeCount},

	{Name: "delete", Range: RangeLine, Args: ArgText, Reg: true, Count: true},
	{Name: "delcommand", Bang: true, Args: ArgCommand},
	{Name: "display", Args: ArgText},
	{Name: "doautocmd", Args: ArgText},

	{Name: "edit", Bang: true, Args: ArgFile},
	{Name: "earlier", Args: ArgText},
	{Name: "echo", Args: ArgText},
	{Name: "echomsg", Args: ArgText},
	{Name: "else", Args: ArgNone},
	{Name: "elseif", Args: ArgText},
	{Name: "endif", Args: ArgNone},
	{Name: "enew", Bang: true},

	{Name: "file", Bang: true, Args: ArgFile},
	{Name: "filetype", Args: ArgText},
	{Name: "finish", Args: ArgNone},
	{Name: "fold", Range: RangeLine},

	{Name: "global", Range: RangeFile, Bang: true, Args: ArgPattern},

	{Name: "help", Bang: true, Args: ArgTag},
	{Name: "highlight", Bang: true, Args: ArgHighlight},
	{Name: "history", Args: ArgText},

	{Name: "insert", Range: RangeLine, Bang: true},
	{Name: "if", Args: ArgText},
	{Name: "imap", Args: ArgText},
	{Name: "imapclear", Args: ArgText},
	{Name: "inoremap", Args: ArgText},
	{Name: "iunmap", Args: ArgText},

	{Name: "join", Range: RangeLine, Bang: true, Count: true},
	{Name: "clearjumps", Args: ArgNone},
	{Name: "jumps", Args: ArgNone},

	// ":k" is one letter and takes the next character as its argument, which
	// is why Lookup has a rule for it. ":keepmarks" is the exception vim
	// carves out with its own "p[1] != 'e'" test.
	{Name: "k", Range: RangeLine, Args: ArgText},
	// keepmarks before the other two: fullcommand('kee') answers keepmarks,
	// which is not what alphabetical order would give.
	{Name: "keepmarks", Args: ArgCommand},
	{Name: "keepalt", Args: ArgCommand},
	{Name: "keepjumps", Args: ArgCommand},

	{Name: "list", Range: RangeLine, Count: true},
	{Name: "last", Bang: true},
	{Name: "lcd", Bang: true, Args: ArgDir},
	{Name: "left", Range: RangeLine, Args: ArgText, Count: true},
	{Name: "let", Args: ArgText},
	{Name: "lmap", Args: ArgText},
	{Name: "lmapclear", Args: ArgText},
	{Name: "lnoremap", Args: ArgText},
	{Name: "ls", Bang: true, Args: ArgText},
	{Name: "lunmap", Args: ArgText},

	{Name: "move", Range: RangeLine, Args: ArgText, Dest: true, Zero: true},
	{Name: "mark", Range: RangeLine, Args: ArgText},
	{Name: "marks", Args: ArgText},
	{Name: "make", Bang: true, Args: ArgText},
	// ":map" and ":mapclear" go after ":mark" and ":make" because
	// fullcommand('ma') is mark and fullcommand('m') is move; ":map" itself
	// is only ever typed in full, and ":mapc" is the only abbreviation of
	// ":mapclear" that resolves. Both measured.
	{Name: "map", Bang: true, Args: ArgText},
	{Name: "mapclear", Bang: true, Args: ArgText},

	{Name: "next", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "new", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "nmap", Args: ArgText},
	{Name: "nmapclear", Args: ArgText},
	{Name: "nnoremap", Args: ArgText},
	{Name: "noremap", Bang: true, Args: ArgText},
	{Name: "noautocmd", Args: ArgCommand},
	{Name: "nohlsearch", Args: ArgNone},
	{Name: "normal", Range: RangeLine, Bang: true, Args: ArgText},
	{Name: "number", Range: RangeLine, Count: true},
	{Name: "nunmap", Args: ArgText},

	{Name: "open", Range: RangeLine},
	{Name: "only", Range: RangeCount, Bang: true},
	{Name: "omap", Args: ArgText},
	{Name: "omapclear", Args: ArgText},
	{Name: "onoremap", Args: ArgText},
	{Name: "ounmap", Args: ArgText},

	{Name: "print", Range: RangeLine, Count: true},
	{Name: "put", Range: RangeLine, Bang: true, Args: ArgText, Reg: true, Count: true, Zero: true},
	{Name: "pwd", Args: ArgNone},

	{Name: "quit", Range: RangeCount, Bang: true},
	{Name: "qall", Bang: true},

	{Name: "read", Range: RangeLine, Bang: true, Args: ArgFile, Zero: true},
	// redo before redir: fullcommand('red') is "redo" and fullcommand('redi')
	// is "redir".
	{Name: "redo", Args: ArgNone},
	{Name: "redir", Bang: true, Args: ArgText},
	{Name: "registers", Args: ArgText},
	{Name: "right", Range: RangeLine, Args: ArgText, Count: true},
	{Name: "runtime", Bang: true, Args: ArgFile},

	{Name: "substitute", Range: RangeLine, Args: ArgPattern},
	{Name: "set", Args: ArgOption},
	{Name: "setglobal", Args: ArgOption},
	{Name: "setlocal", Args: ArgOption},
	{Name: "silent", Bang: true, Args: ArgCommand},
	// "source" before "sort": "so" is a prefix of both and vim answers
	// source, while "sor" is a prefix of only sort. Swapping them breaks the
	// first and not the second, which is why the order is written down here.
	{Name: "source", Bang: true, Args: ArgFile},
	{Name: "sort", Range: RangeFile, Bang: true, Args: ArgText},
	{Name: "split", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "syntax", Args: ArgText},

	// ":t" is ":copy" under another name and has no longer form, so it has to
	// sit in front of everything else beginning with a t or ":t" resolves to
	// ":tag".
	{Name: "t", Range: RangeLine, Args: ArgText, Dest: true, Zero: true},
	{Name: "tag", Range: RangeCount, Bang: true, Args: ArgTag},
	{Name: "tab", Args: ArgCommand},
	{Name: "tabclose", Range: RangeCount, Bang: true},
	{Name: "tabedit", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "tabnext", Range: RangeCount},
	{Name: "tabnew", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "tabonly", Range: RangeCount, Bang: true},
	{Name: "tabprevious", Range: RangeCount},
	{Name: "topleft", Args: ArgCommand},

	{Name: "undo", Range: RangeCount, Bang: true},
	{Name: "unlet", Bang: true, Args: ArgText},
	{Name: "unmap", Bang: true, Args: ArgText},

	{Name: "vglobal", Range: RangeFile, Args: ArgPattern},
	{Name: "version", Args: ArgNone},
	{Name: "vertical", Args: ArgCommand},
	{Name: "vimgrep", Bang: true, Args: ArgPattern},
	{Name: "vmap", Args: ArgText},
	{Name: "vmapclear", Args: ArgText},
	// vnoremap in FRONT of vnew: fullcommand('vn') is vnoremap and not vnew,
	// measured, which is the one place in the map family where the order is
	// not the order a reader would write.
	{Name: "vnoremap", Args: ArgText},
	{Name: "vnew", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "vsplit", Range: RangeCount, Bang: true, Args: ArgFile},
	{Name: "vunmap", Args: ArgText},

	{Name: "write", Range: RangeFile, Bang: true, Args: ArgFile},
	{Name: "wall", Bang: true},
	{Name: "wq", Range: RangeFile, Bang: true, Args: ArgFile},
	{Name: "wqall", Bang: true, Args: ArgFile},
	// The two ":win..." commands go after the four ":w..." ones because
	// fullcommand('w') is write. Between themselves the order is vim's and is
	// not alphabetical the way it looks: fullcommand('win') is winsize and
	// only fullcommand('winc') is wincmd, measured. winsize is
	// in the table so that ":win" does not silently become ":wincmd"; it has
	// no handler and never will, because there is no ":winsize" worth having
	// in an editor whose window is the terminal.
	{Name: "winsize", Args: ArgText},
	{Name: "wincmd", Range: RangeCount, Args: ArgText},

	{Name: "xit", Range: RangeFile, Bang: true, Args: ArgFile},
	{Name: "xall", Bang: true},
	{Name: "xmap", Args: ArgText},
	{Name: "xmapclear", Args: ArgText},
	{Name: "xnoremap", Args: ArgText},
	{Name: "xunmap", Args: ArgText},

	{Name: "yank", Range: RangeLine, Args: ArgText, Reg: true, Count: true},

	{Name: "z", Range: RangeLine, Args: ArgText},

	// The commands spelled with punctuation. Lookup finds them by the same
	// prefix scan as everything else, except for ":>>" and ":<<", which are
	// one command written twice and are folded in Lookup rather than being
	// five entries each.
	//
	// ":!" carries RangeLine so that ":1,2!cmd" resolves a range and ":!cmd"
	// does not: the handler reads Given, which is the difference between
	// filtering two lines and running a command with the output in a split.
	{Name: "!", Range: RangeLine, Args: ArgText},
	{Name: "<", Range: RangeLine, Count: true},
	{Name: ">", Range: RangeLine, Count: true},
	{Name: "=", Range: RangeLine},
	{Name: "#", Range: RangeLine, Count: true},
	{Name: "&", Range: RangeLine, Args: ArgText},
	{Name: "~", Range: RangeLine, Args: ArgText},
}

// Lookup resolves a typed command name.
//
// Prefix matching against the table above, first match wins, with the two
// exceptions vim's find_ex_command() carves out before it consults its table.
// Both are transcribed from that function rather than invented, because both
// have a guard in them that is not guessable:
//
// - ":k" takes the next character as its argument, so ":ka" sets mark a. The
// guard is on the two characters after it, not one: ":ke" is still :k and
// only ":kee" reaches the ":keep..." family. Measured, because a one
// character guard gets ":ke" wrong and nothing else.
// - ":s" may be written with its flag letters against the name, so ":sg",
// ":si" and ":sr" are all :substitute. The guards keep ":silent", ":sign",
// ":simalt", ":scriptnames", ":scs" and ":sre" out of it, which is why
// fullcommand('si') is "substitute" and fullcommand('sil') is "silent".
//
// A name starting with an uppercase letter is a user command and is never a
// built-in, which is what keeps ":JsonPretty" out of this table. An empty name
// is vim's bare ":", which moves to the line the range named; it comes back
// false and the caller handles it.
func Lookup(typed string) (*Command, bool) {
	if typed == "" {
		return nil, false
	}
	if typed[0] >= 'A' && typed[0] <= 'Z' {
		return nil, false
	}
	// ":>>>" is ":>" three times over, and the same for ":<". They are folded
	// here rather than being five table entries each, and the fold is why
	// exShift reads the length of what was typed to know how far to indent.
	if typed[0] == '<' || typed[0] == '>' {
		if strings.Trim(typed, string(typed[0])) == "" {
			return find(string(typed[0]))
		}
	}
	if typed[0] == 'k' && (at(typed, 1) != 'e' || at(typed, 2) != 'e') {
		return find("k")
	}
	if typed[0] == 's' && substituteWithFlags(typed) {
		return find("substitute")
	}
	for i := range commands {
		if strings.HasPrefix(commands[i].Name, typed) {
			return &commands[i], true
		}
	}
	return nil, false
}

// at returns the byte at index i, or zero past the end. Vim reads a NUL there
// and every guard below is written against a character that is not one, so
// zero is the right answer and not a special case.
func at(s string, i int) byte {
	if i >= len(s) {
		return 0
	}
	return s[i]
}

// substituteWithFlags is vim's own test for ":s" written with its flags
// against the name, transcribed from find_ex_command() in ex_docmd.c.
func substituteWithFlags(p string) bool {
	switch at(p, 1) {
	case 'c':
		return at(p, 2) != 's' && at(p, 2) != 'r' && (at(p, 3) != 'i' || at(p, 4) != 'p')
	case 'g':
		return true
	case 'i':
		return at(p, 2) != 'm' && at(p, 2) != 'l' && at(p, 2) != 'g'
	case 'I':
		return true
	case 'r':
		return at(p, 2) != 'e'
	}
	return false
}

// find returns the table entry with exactly this name.
func find(name string) (*Command, bool) {
	for i := range commands {
		if commands[i].Name == name {
			return &commands[i], true
		}
	}
	return nil, false
}

// Names returns every built-in command name in table order, which is what
// wildmenu completion after ":" offers.
func Names() []string {
	names := make([]string, 0, len(commands))
	for i := range commands {
		names = append(names, commands[i].Name)
	}
	return names
}

// UserCommand is a command defined with ":command!".
//
// The vimrc defines exactly one, on line 130:
//
//	command! -range -nargs=0 -bar JsonPretty <line1>,<line2>!jq --sort-keys '.'
//
// which is the whole of the feature that has to work: a bang to allow
// redefinition, -range to accept one, -nargs=0 to refuse arguments, -bar to
// let a "|" end it, and <line1> and <line2> substituted into the replacement.
type UserCommand struct {
	// Name always starts with an uppercase letter, which is vim's rule and
	// what keeps user commands out of the built-in table.
	Name string
	// Repl is the replacement text, with the <...> substitutions still in it.
	Repl string
	// NArgs is the -nargs attribute: "0", "1", "*", "?" or "+".
	NArgs string
	// Range is the -range attribute: "" for none, "." for a range allowed,
	// "%" for a default of the whole file, or a number for a count.
	Range string
	// Bar is -bar: a "|" ends the command rather than being part of it.
	Bar bool
	// Bang is -bang: the command may be called with a "!".
	Bang bool
	// Complete is -complete=..., parsed and carried for wildmenu.
	Complete string

	// Run is a command implemented in Go rather than as replacement text.
	//
	// It exists because pvim's plugin replacements are Go packages and not
	// vimscript, so :Gstatus, :NERDTreeToggle, :GoBuild and :tselect have nothing
	// to put in Repl, and before this each of them carried a private hook in
	// cmd/pvim that had to be consulted before the ex layer saw the line.
	// There were four such hooks and they all did the same thing.
	//
	// When Run is set, Repl is not expanded and not run: the range and the
	// argument rules are still checked first, so a Go command gets E481 and
	// E477 the same way a replacement one does, and then Run gets the Cmd with
	// its range resolved.
	Run func(*Context, Cmd) error
}

// UserCommands is the set defined so far, keyed by name.
type UserCommands map[string]*UserCommand
