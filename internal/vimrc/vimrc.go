// Package vimrc reads the subset of vimscript that ~/.vimrc uses, and refuses
// the rest out loud.
//
// It is not a vimscript interpreter and will not become one. The dividing
// rule below is the whole specification of this package:
//
//	pvim runs a statement if it changes an option, a mapping, an autocmd, a
//	user command, a highlight, or a g: variable pvim itself reads. It logs and
//	ignores a "let g:" it does not read, so the eleven g:go_* lines and the
//	g:vim_ai_* lines are inert without noise. It errors on an unknown command,
//	on the message line, with the file and the line number, and startup
//	continues, exactly as vim does.
//
// The three ways that goes wrong, in order of how bad they are: running
// something that should have been refused, which makes pvim pretend to be vim
// and fail at it somewhere subtle; refusing something that should have run,
// which is a line the user has to delete out of a file that has to keep
// working under MacVim; and being silent about either.
//
// # The seam
//
// The parser does not touch the editor. It produces statements and hands them
// to a Sink, which is an interface for a real reason and not for tidiness:
// this package would otherwise import internal/mode, internal/window and
// half the tree to apply what it parsed, and the test that matters here --
// "does the real ~/.vimrc parse with no errors" -- would need a whole editor to
// run. With the Sink it needs a struct that records what it was told. Config
// is that struct, written out in full, and it is the loaded configuration the
// editor reads rather than a test double.
//
// # What is not here
//
// No user functions, no :try, no :while, no :for, no dictionaries beyond a
// literal assigned to a variable. A ":function" block is stepped over and
// logged, and a ":call" of a function stepped over in the same load is logged
// the same way, because a call pvim declines to make is the same event as a
// variable pvim declines to read and belongs in the same list. That line is
// where this project stops and neovim starts.
//
// Two of the refusals moved, and both moved a measured
// distance rather than being dropped.
//
// ":call" now runs a call of one of the ten builtins in funcs.go and nothing
// else. It had to: the live vimrc's only way to make the directory it is about
// to point 'undodir' at is "call mkdir(s:vim_state, 'p', 0700)", and mkdir()
// is a builtin whether it is reached from an expression or from a statement.
// A ":call" of anything that is not in that table is still E117.
//
// ":execute" now runs a command it built out of an expression, and it is NOT
// vim's ":execute". Vim's takes any expression and runs any command; this one
// evaluates the same closed expression grammar the rest of this package has --
// string literals, variables, the ten builtins, "." and ".." concatenation --
// and then requires the result to be a command this loader already
// understands. A result that is not is an error with the RESOLVED text in the
// message, so that
//
//	execute 'set runtimepath^=' . fnameescape(s:vim_home)
//
// failing says what it tried to run and not what was typed. It is a
// concatenation that reaches the same table a typed line reaches, and it can
// no more build a command out of thin air than a typed line can. Nested
// ":execute" is refused for the same reason, which is that the first thing a
// real one would be used for is escaping this list.
package vimrc

import (
	"fmt"
	"strings"

	"github.com/pkar/pvim/internal/ex"
	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/options"
)

// Pos is where a statement came from: the file and the 1-based line.
//
// Every statement carries one because every error message needs one. Vim
// prints the file and the line for an error in a sourced script and startup
// continues, and a loader that says "E492: Not an editor command" with no line
// number in front of it is a loader nobody can use on a 243-line file.
//
// The line is the FIRST physical line of the logical line, which is what vim
// reports: a backslash continuation that fails is blamed on the line the
// statement started at, measured by sourcing a file whose line 2
// is a stray "\ 'oops'" and reading back "line 1: E518".
type Pos struct {
	File string
	Line int
}

// String is "file, line 12", which is what a message line prefix wants.
func (p Pos) String() string { return fmt.Sprintf("%s, line %d", p.File, p.Line) }

// Stmt is one parsed statement. The set is closed: the types below are all of
// it, and the marker method is unexported so that a type switch over them is
// exhaustive and stays that way.
type Stmt interface {
	Where() Pos
	stmt()
}

// SetScope is which of the three ":set" forms a SetOption came from.
type SetScope uint8

// The three forms. They differ, and the vimrc uses two of them: ":set" on its
// own lines and ":setlocal" inside its autocmds, where the difference is
// exactly whether the setting leaks into the next file opened.
const (
	SetBoth SetScope = iota
	SetLocalScope
	SetGlobalScope
)

// Where maps a SetScope onto the option layer's own enum.
func (s SetScope) Where() options.Where {
	switch s {
	case SetLocalScope:
		return options.SetLocal
	case SetGlobalScope:
		return options.SetGlobal
	default:
		return options.Both
	}
}

// SetOption is ":set", ":setlocal" or ":setglobal".
//
// Args is the argument list unsplit, because options.ApplyLine already knows
// how to split it on unescaped white space and a second splitter here would be
// a second place for ":set listchars=tab:>\ " to go wrong. The trailing
// comment is gone by the time it gets here, because ":set t_vb= " Disable ALL
// bells"" on line 76 of the vimrc is one option and a comment and not two
// options.
type SetOption struct {
	Pos
	Scope SetScope
	Args  string
}

// Let is ":let", ":let g:x = y" or ":let mapleader = ','".
type Let struct {
	Pos
	// Name is the variable with its scope prefix filled in, which is where
	// vim keeps it: the vimrc writes "NERDTreeWinSize" and "mapleader" with
	// no prefix at all, and both are globals, so both arrive here as
	// "g:NERDTreeWinSize" and "g:mapleader". Read takes either spelling.
	Name string
	// Op is "=", ".=", "+=" or "-=".
	Op string
	// Value is the right-hand side as written, with the "\" continuation lines
	// already joined and the trailing comment removed.
	Value string
	// Val is the right-hand side evaluated. Its Kind is what the value looks
	// like; ValueDict is there for one line in the vimrc,
	// g:ctrlp_custom_ignore, which is a dictionary literal spread over three
	// lines with backslash continuations and which the finder
	// reads.
	Val Val
	// Unlet says this was ":unlet" rather than ":let".
	Unlet bool
}

// Map is one of the map family: ":map", ":nmap", ":nnoremap", ":vnoremap",
// ":inoremap" and the unmap forms.
type Map struct {
	Pos
	// Modes is the mode letters the command implies: "n" for :nmap, "v" for
	// :vmap, "i" for :imap, and "nvo" for a bare :map, which is the one that
	// catches people. The vimrc's "map { gT" is nvo, so { is gT in operator
	// pending too, and "d{" therefore means "delete to the previous tab",
	// which is nothing, which is why it beeps.
	Modes string
	// LHS is the left-hand side in vim's notation, with <leader> already
	// expanded from 'mapleader' at the time the line ran. That timing matters:
	// the vimrc sets mapleader on line 58 and uses <leader> on line 125, and
	// a loader that expanded it at the end would get a different mapping.
	LHS []key.Key
	// LHSText is the left-hand side as written, before <leader> expansion. It
	// is what an error message and a ":map" listing quote.
	LHSText string
	// RHS is the right-hand side, unexpanded and with any trailing comment
	// still on it, because :map has no comments: vim maps <Space> to
	// `za " Spacebar to unfold`, all twenty-four characters of it, and pvim
	// has to as well. Measured with :execute('nmap <Space>').
	RHS string
	// NoRemap is the "nore" in the command name.
	NoRemap bool
	// Buffer, Silent, Expr, Unique and Nowait are the <buffer>, <silent>,
	// <expr>, <unique> and <nowait> arguments. The vimrc uses <buffer> once,
	// on line 160.
	Buffer, Silent, Expr, Unique, Nowait bool
	// Unmap says this was an unmap or a mapclear.
	Unmap bool
	// Clear says this was a mapclear.
	Clear bool
}

// AutoCmd is ":autocmd", ":au!" and the ":augroup" they sit inside.
type AutoCmd struct {
	Pos
	// Group is the enclosing ":augroup" name, empty outside one. The vimrc
	// has three: FastEscape, spell_settings and myvimrc.
	Group string
	// Events are the event names in vim's own spelling, which is the name vim
	// prints and not always the name that was typed: ":au BufWritePre" and
	// ":au BufWrite" are one event and vim lists both as "BufWrite", and
	// ":au BufReadPost" lists as "BufRead". Measured with
	// :execute('autocmd BufReadPost *.go').
	Events []string
	// Patterns are the file patterns, split on commas.
	Patterns []string
	// Cmd is the command to run, which may itself be another ":autocmd".
	//
	// The vimrc has one of those, on line 183:
	//
	//	autocmd Filetype python,java,javascript,go autocmd BufWritePre * :%s/\s\+$//e
	//
	// which registers a global BufWritePre the first time any of those
	// filetypes is entered, and then strips trailing white space in every
	// buffer for the rest of the session, markdown included. That is a bug in
	// the vimrc that vim has been faithfully executing for years, and pvim
	// reproduces it exactly, because the oracle says so. See
	// TestTheNestedAutocmdLeaks, which asserts the leak as intended
	// behaviour.
	Cmd string
	// Nested and Once are the ++nested and ++once arguments.
	Nested, Once bool
	// Clear says this was ":autocmd!" with no command: remove the autocmds
	// matching, which is what the "au!" at the top of each augroup does.
	Clear bool
}

// Command is ":command!", the user-command definition.
//
// The vimrc defines exactly one, JsonPretty on line 130, and it is the whole
// of the feature that has to work.
type Command struct {
	Pos
	Name  string
	Bang  bool
	Attrs []string
	Repl  string
	// Delete says this was ":delcommand".
	Delete bool
}

// Highlight is ":hi" or ":highlight".
//
// The four nofrils colourscheme files are made of these and nothing else,
// which is why a colourscheme is 400 of these statements and not a feature.
type Highlight struct {
	Pos
	// Group is the highlight group name.
	Group string
	// Link is the group this one links to, for "hi link A B". Empty otherwise.
	Link string
	// Default says "hi default", which does not overwrite an existing
	// definition.
	Default bool
	// Clear says "hi clear" with or without a group.
	Clear bool
	// Args are the key=value pairs: guifg, guibg, gui, ctermfg, ctermbg,
	// cterm, term.
	Args map[string]string
}

// FileType is ":filetype", the command the vimrc's line 2 spells
// "filetype plugin indent on".
//
// It stopped being inert. It had to: the file has eight FileType
// autocommands and three lines that set 'filetype' by hand, and none of them
// can fire in an editor that never works out what kind of file is open, so
// accepting the line and doing nothing ran a third of the config as dead code.
// ":syntax" is still inert and stays that way -- syntax highlighting was put
// out of scope with a tree-sitter decision attached, and a vimrc that says
// "syntax enable" to an editor that colours nothing should not be told
// otherwise.
//
// The three switches are vim's three, and the line names any of them:
//
//	:filetype on detection
//	:filetype plugin on detection and ftplugin sourcing
//	:filetype indent on detection and indent-script sourcing
//	:filetype plugin indent on all three, which is what the vimrc says
//	:filetype off all three off
//	:filetype detect re-run detection on the current buffer now
//	:filetype report the three states
//
// Turning "plugin" or "indent" on also turns detection on, in vim and here,
// because sourcing a script per filetype means nothing without a filetype.
//
// What pvim does with each is not the same as what vim does with each, and the
// difference is deliberate: filetype indent and ":filetype plugin indent on"
// are out of scope, and 'autoindent' plus the vimrc's 'smartindent cinwords'
// for python (a 'cinwords' line-start match followed by an extra
// 'shiftwidth') is the entire indent engine. So Detect is real and Plugin and Indent are recorded and source
// nothing: there are no ftplugin or indent scripts to source, and every option
// a filetype ends up with in pvim comes from the vimrc's own FileType
// autocommands. Recorded rather than dropped so that ":filetype" can report
// what the file asked for and so that a test can tell "the line was
// understood" from "the line was skipped".
type FileType struct {
	Pos
	// Detect, Plugin and Indent are which of the three switches this line
	// changes. A line that names none of them and says "on" or "off" changes
	// all three, which is what ":filetype off" means.
	Detect, Plugin, Indent bool
	// On is whether they are being turned on. False is "off", and it is also
	// the zero value, so a Redetect or a Report carries no meaning here.
	On bool
	// Redetect is ":filetype detect": run detection over the current buffer
	// now, without changing any of the three switches.
	Redetect bool
	// Report is a bare ":filetype", which prints the three states.
	Report bool
	// Args is the argument list as written, for the error message.
	Args string
}

// Colorscheme is ":colorscheme name". The vimrc has one, nofrils-dark.
type Colorscheme struct {
	Pos
	Name string
}

// Pack is ":packloadall", which the live vimrc runs on line 8.
//
// In vim it does two things: it adds every "pack/*/start/*" directory under
// 'packpath' to 'runtimepath', and then it sources every plugin script inside
// them. In pvim it does the first and not the second, and that is not a gap to
// be filled in later: no plugin will ever run here, because the moment a
// plugin can run this is neovim and it is ten years long. The eight directories under ~/.vim/pack
// hold 43,360 lines of vimscript that this editor replaces in Go rather than
// executes.
//
// What the first half is still worth: 'runtimepath' is how ":colorscheme
// nofrils-dark" on the next line finds a file that lives at
// pack/*/start/nofrils/colors/nofrils-dark.vim, and that colourscheme is
// the whole of what this vimrc gets out of its packages.
type Pack struct {
	Pos
	// Bang is ":packloadall!", which in vim re-runs the load for scripts
	// already sourced. Nothing is sourced here, so it changes nothing and is
	// carried rather than refused: the line means what it means and pvim
	// simply has less to do.
	Bang bool
}

// Source is ":source" or ":so", including the "so $MYVIMRC" in the vimrc's own
// BufWritePost autocmd.
//
// The loader never opens the file. Reading one means knowing about
// 'runtimepath', which is the caller's business, and a parser that touched the
// disk would need a fake filesystem in every test.
type Source struct {
	Pos
	// Path is the argument with $VARs and ~ expanded through Env.Expand.
	Path string
	// Raw is the argument as written, which is what a message quotes.
	Raw string
	// Bang says ":source!", which reads the file as normal-mode keys rather
	// than as vimscript. Nothing uses it and it is refused rather than
	// mistaken for the ordinary form.
	Bang bool
}

// Ex is a statement that is an ordinary ex command and is handed straight to
// internal/ex: ":nohlsearch", ":silent", and any line that opens with a range
// rather than a command name. They are carried as a statement rather than being
// run inline so that the whole file can be parsed before anything is applied,
// which is what makes a dry run possible.
//
// The Cmd is built here rather than by ex.Parse. That is not a comment on
// ex.Parse: it is that this package has already found the command name and
// resolved its abbreviation by the time it gets here, and handing the string
// back to a second parser to have it find the same name again is a second
// place for ":sy" to mean something else.
type Ex struct {
	Pos
	Cmd ex.Cmd
	// Line is the command as written, which is what the error message quotes.
	Line string
}

// IgnoreReason is why a statement was logged and dropped rather than run.
type IgnoreReason uint8

// The reasons. Every one of them is a thing vim would have done and pvim will
// not, which is why each lands in Result.Ignored where the gate can count
// them, rather than being silently skipped.
const (
	// IgnoreVar is a ":let" of a variable Read says nothing reads: the
	// g:vim_ai_* lines and g:go_metalinter_enabled.
	IgnoreVar IgnoreReason = iota
	// IgnoreFunc is a ":function" ... ":endfunction" block. pvim has no
	// functions; the four nofrils colourschemes define three each.
	IgnoreFunc
	// IgnoreCall is a ":call" of a function IgnoreFunc stepped over in the
	// same load. nofrils ends with "call NofrilsNormal()", whose body sets
	// the same five groups the file already set at the top level, so the
	// highlight table is the same either way -- checked against all four
	// files, see TestTheNofrilsCallIsANoOp.
	IgnoreCall
	// IgnoreInert is a command pvim accepts and does nothing with. There is
	// one: ":syntax", which is out of scope with a tree-sitter decision
	// attached, and which all four colourschemes use. ":filetype" was the other
	// until it moved; see the FileType statement for what moved it and why
	// ":syntax" did not move with it.
	IgnoreInert
)

// String names the reason for the log line.
func (r IgnoreReason) String() string {
	switch r {
	case IgnoreVar:
		return "variable nothing reads"
	case IgnoreFunc:
		return "function definition"
	case IgnoreCall:
		return "call of a skipped function"
	default:
		return "accepted and inert"
	}
}

// Ignored is one statement that was logged and dropped.
type Ignored struct {
	Pos
	// Name is the variable or function the statement is about, empty for a
	// command. It is what the gate greps for.
	Name string
	// Text is the statement as written.
	Text string
	// Reason is why it was dropped.
	Reason IgnoreReason
}

// String is the log line.
func (i Ignored) String() string {
	if i.Name != "" {
		return fmt.Sprintf("%s: ignored %s: %s", i.Pos, i.Reason, i.Name)
	}
	return fmt.Sprintf("%s: ignored %s: %s", i.Pos, i.Reason, i.Text)
}

// Unknown is a line the parser did not recognise.
//
// It is a statement and not an error return because startup continues: vim
// prints the error and carries on to the next line, and a loader that stops at
// the first unknown command turns a plugin call that slipped into the vimrc
// into an editor with no configuration at all.
type Unknown struct {
	Pos
	// Text is the line as written, which the message quotes after the E-code.
	Text string
	// Err is why: usually ex.ErrNotAnEditorCommand, sometimes a refusal from
	// the expression evaluator.
	Err error
}

func (p Pos) Where() Pos { return p }

func (SetOption) stmt()   {}
func (Let) stmt()         {}
func (Map) stmt()         {}
func (AutoCmd) stmt()     {}
func (Command) stmt()     {}
func (Highlight) stmt()   {}
func (Colorscheme) stmt() {}
func (Pack) stmt()        {}
func (FileType) stmt()    {}
func (Source) stmt()      {}
func (Ex) stmt()          {}
func (Ignored) stmt()     {}
func (Unknown) stmt()     {}

// Diag is one thing that went wrong, with the place it went wrong at.
type Diag struct {
	Pos
	// Text is the statement as written.
	Text string
	// Err is the failure, E-code first.
	Err error
}

// Error renders the diagnostic the way the message line wants it, which is one
// line. Vim spends three:
//
//	Error detected while processing ~/.vimrc:
//	line 14:
//	E518: Unknown option: frobnicate
//
// and 'cmdheight' is 1 in this vimrc on purpose, so pvim says it on one.
// the register D-002 covers the wording of an E-code pvim does not otherwise
// implement.
//
// The statement text goes on the end only when the E-code has not already
// said it. Half of them have: vim's own wording for E492 and E474 names the
// command that failed, and this package builds the error the same way, so
// printing Text after it said "NERDTreeToggle: NERDTreeToggle" on a message
// line one row tall. Vim prints the code once.
func (d Diag) Error() string {
	msg := fmt.Sprint(d.Err)
	if d.Text == "" || strings.Contains(msg, d.Text) {
		return fmt.Sprintf("%s: %s", d.Pos, msg)
	}
	return fmt.Sprintf("%s: %s: %s", d.Pos, msg, d.Text)
}

// Unwrap lets errors.Is reach the E-code underneath.
func (d Diag) Unwrap() error { return d.Err }

// Result is what one load produced.
//
// Errors and Ignored are the two halves of the dividing rule, kept apart on
// purpose: the gate is that the real ~/.vimrc loads with Errors empty
// and with exactly the plugin variables it sets in Ignored. One list would
// make that gate unwritable.
type Result struct {
	// Errors is one entry per statement that failed, in file order. A
	// non-empty list is not a reason to stop, and Run never stops for one.
	Errors []Diag
	// Ignored is one entry per statement that was logged and dropped.
	Ignored []Ignored
	// Finished says the file ended at a ":finish" rather than at its last
	// line.
	Finished bool
}

// Log is every diagnostic and every ignored statement as text, in file order
// within each half. It exists so a test can assert on a load with one
// comparison and a startup can print one block.
func (r *Result) Log() []string {
	out := make([]string, 0, len(r.Errors)+len(r.Ignored))
	for _, d := range r.Errors {
		out = append(out, d.Error())
	}
	for _, i := range r.Ignored {
		out = append(out, i.String())
	}
	return out
}

// IgnoredNames is the names in Ignored, in order, skipping the entries that
// have none. It is the "ignored-with-log list" the gate names.
func (r *Result) IgnoredNames() []string {
	var out []string
	for _, i := range r.Ignored {
		if i.Name != "" {
			out = append(out, i.Name)
		}
	}
	return out
}

// Sink is where parsed statements go.
//
// One method per statement type rather than a single Apply(Stmt), because the
// caller has to handle every one and a type switch in the caller is a type
// switch that quietly grows a default case. A Sink that returns an error stops
// nothing: the loader records it against the line and carries on, the way vim
// does.
type Sink interface {
	SetOption(SetOption) error
	Let(Let) error
	Map(Map) error
	AutoCmd(AutoCmd) error
	Command(Command) error
	Highlight(Highlight) error
	Colorscheme(Colorscheme) error
	// Pack is ":packloadall". See the Pack statement for why it changes
	// 'runtimepath' and sources nothing.
	Pack(Pack) error
	// FileType is ":filetype ... on".
	FileType(FileType) error
	// Syntax is ":syntax on|off|enable|reset|clear|manual". It used to be an
	// Ignored, because syntax highlighting was out of scope;
	// a user overrode that deliberately once the config they run had
	// "syntax enable" uncommented, so the word now reaches an implementation.
	Syntax(Syntax) error
	Source(Source) error
	Ex(Ex) error
	// Ignored is the logged-and-dropped path. It is separate from Unknown
	// because the two are different events: one is pvim declining to be vim
	// on purpose and the other is pvim not understanding the line.
	Ignored(Ignored) error
	// Unknown is the E492 path. A Sink that returns nil from it has decided
	// to swallow the error, which is what ":silent!" would do and what
	// nothing else should.
	Unknown(Unknown) error
}

// Env is the world the evaluator asks questions of.
//
// It is small on purpose. These five are every question the vimrc's ":if"
// lines ask, and a sixth would mean a feature has crept in.
type Env interface {
	// Has answers has(). The answers are pinned: true for gui_running,
	// mouse, clipboard, persistent_undo, autocmd, conceal, unnamed and
	// multi_byte, false for everything else, which is enough for every branch
	// in the file. Note that gui_running is false in the terminal frontend,
	// which is what makes the vimrc's FastEscape autocmds and its
	// <C-c>/<C-v> clipboard maps take effect there and not in the window.
	Has(feature string) bool
	// Exists answers exists(), for "&option", "*function" and a variable
	// name. The loader answers for variables it has seen itself before it
	// asks this.
	Exists(name string) bool
	// Option answers "&name", which the vimrc reads twice: "if !&scrolloff"
	// and "if !&sidescrolloff".
	Option(name string) (options.Value, error)
	// Expand answers expand() and environment variables: "~/.vimgo",
	// "$MYVIMRC", "$MYGVIMRC".
	Expand(s string) string
	// FileReadable answers filereadable(), which the vimrc's BufWritePost
	// autocmd uses on $MYGVIMRC.
	FileReadable(path string) bool
	// Executable answers executable(), which the live vimrc asks about
	// /opt/homebrew/bin/bash before it sets 'shell' to it.
	Executable(name string) bool
	// IsDirectory answers isdirectory(), which the live vimrc asks three
	// times before each of its mkdir() calls.
	IsDirectory(path string) bool
	// MkDir answers mkdir(). flags is vim's, of which only "p" -- create the
	// parents -- has any meaning here, and mode is the permission bits, which
	// the live vimrc writes as the octal literal 0700.
	//
	// It is the one Env method that changes the world rather than answering a
	// question about it, and it is here because the directory it makes is the
	// one 'undodir' and 'directory' are pointed at two lines later.
	MkDir(path, flags string, mode int) error
	// Resolve answers resolve(): follow the symbolic links in a path. The
	// live vimrc's line 5 uses it on <sfile>, because ~/.vimrc is a symlink
	// and the vim home it wants is where the link points, not where it sits.
	Resolve(path string) string
	// Cwd is the working directory, which is what fnamemodify's ":p" makes a
	// relative name absolute against. It is asked of Env rather than of the
	// operating system so that this package still touches neither.
	Cwd() string
}

// ReadVars is every g: variable pvim reads, which is the other half of the
// dividing rule: a let of a name in here is applied, and a let of anything
// else is logged once and dropped.
//
// The list is short for a reason: the finder, the tree and the
// formatters are the only things that read configuration out of a variable,
// and everything else the vimrc sets belongs to a plugin that is not being
// reimplemented. The eleven g:go_* lines are inert except for g:go_bin_path,
// which is where gopls will be looked for before $PATH.
var ReadVars = map[string]bool{
	"mapleader":                    true,
	"maplocalleader":               true,
	"g:ctrlp_max_height":           true,
	"g:ctrlp_match_window":         true,
	"g:ctrlp_custom_ignore":        true,
	"g:NERDTreeShowHidden":         true,
	"g:NERDTreeIgnore":             true,
	"g:NERDTreeWinSize":            true,
	"g:terraform_align":            true,
	"g:terraform_fmt_on_save":      true,
	"g:go_bin_path":                true,
	"g:go_fmt_autosave":            true,
	"g:go_code_completion_enabled": true,
}

// Read reports whether pvim reads this variable.
//
// A bare name in a vimrc is a global, and the vimrc writes four of them that
// way: NERDTreeShowHidden, NERDTreeIgnore, NERDTreeWinSize and mapleader. So a
// name with no scope prefix is tried both as itself and with "g:" in front.
func Read(name string) bool {
	if ReadVars[name] {
		return true
	}
	if bare, ok := strings.CutPrefix(name, "g:"); ok {
		return ReadVars[bare]
	}
	return ReadVars["g:"+name]
}

// Syntax is ":syntax", carrying the word after it.
//
// Only the argument is kept. Everything a syntax file itself contains --
// ":syn match", ":syn region", clusters, "hi link" -- is internal/syntax's
// language and not this one's: this statement decides whether that engine runs
// at all, which is the whole of what a vimrc says about syntax.
type Syntax struct {
	Pos
	// Arg is the lower-cased word: on, off, enable, reset, clear, manual. An
	// empty Arg is a bare ":syntax", which vim answers by listing the items.
	Arg string
	// Args is the argument list as written, for the error message.
	Args string
}
