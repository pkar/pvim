// Package options is every vim option this editor reads, as a typed field.
//
// Not a map[string]any. An option in a map is an option nothing has to read:
// ":set shiftround" parses, lands in the map, and the editor goes on rounding
// nothing, which looks exactly like an editor that works. A field has a name a
// compiler checks and a reader a person can grep for, and an option added here
// that nobody reads shows up as an unused field rather than as a bug report in
// six months.
//
// This package is a leaf: it imports nothing else in this module and nothing
// else in this module may be imported from it. Options are read by the mode
// machine, the screen model, the ex layer and both frontends, and the day this
// package can see any of them is the day one of them can be asked a question
// from inside a:set.
//
// # Scope
//
// Vim gives every option one of four scopes and this package models all four,
// because retrofitting the split later touches every caller:
//
// - global: one value for the process. 'ignorecase', 'shell', 'cmdheight'.
// - buffer-local: one value per buffer. 'tabstop', 'expandtab', 'textwidth'.
// - window-local: one value per window. 'wrap', 'number', 'scroll'.
// - global-local: a local value that falls back to a global one when it is
// unset. 'scrolloff', 'completeopt', 'errorformat'.
//
// Every local option also has a global value, which is not the value anything
// reads: it is what a newly opened buffer or window is initialised from, and it
// is what ":setglobal ts=4" writes. That is why Options carries GB and GW
// beside B and W. ":set ts=4" writes both halves, ":setlocal ts=4" writes B
// alone, ":setglobal ts=4" writes GB alone, and a buffer opened afterwards
// starts from GB. Getting that wrong is the reason ":setlocal" in an autocmd
// leaks into the next file opened.
//
// # Where the numbers came from
//
// Nothing in this package was typed from memory or copied out of prose. The
// name, abbreviation, type and scope of every option came out of vim 9.2.0321's
// own options.txt, which is generated alongside the C table it describes;
// testdata/extract.sh is the twenty lines that turn it into a .tsv and
// TestScopesMatchVimsDocumentation is what compares the table against it.
//
// Every default, every value limit, every legal-character set and every error
// message came out of running /opt/homebrew/bin/vim headless and reading the
// answer back through writefile(). The two recorded files under testdata are
// what it said: one at startup, one after the real ~/.vimrc's ":set" lines, both
// in the exact text ":set name?" prints. TestRecordedVimIsStillWhatVimSays
// regenerates them from the vim on the machine, so a brew upgrade that changes a
// default fails with the option's name on it.
//
// There are two default tables and the difference matters. Builtin is vim's
// compiled-in default: what ":set name&" resets to. Defaults is what
// "vim --clean" actually starts with, which is Builtin plus the six lines of
// defaults.vim that still show: 'scrolloff' 5 and not 0, 'mouse' "a" and not "",
// 'incsearch' and 'ttimeout' on, 'ttimeoutlen' 100 and not -1, and 'nrformats'
// with octal taken out. One table cannot say both, and an editor with one table
// gets ":set scrolloff&" wrong in a way nobody notices for a year.
package options

import "strings"

// Kind is an option's value type. Vim has exactly three and so does this.
type Kind uint8

// The three option types.
const (
	Bool Kind = iota
	Number
	String
)

// Scope is where an option's value lives.
type Scope uint8

// The four scopes. The two global-local ones are spelled by which side they
// are local to, because the fallback rule differs: an unset local number is
// -1 and an unset local string is empty.
const (
	ScopeGlobal Scope = iota
	ScopeBuffer
	ScopeWindow
	ScopeGlobalBuffer
	ScopeGlobalWindow
)

// Where says which half of an option a:set is writing.
type Where uint8

// The three forms of :set. Both is ":set", which writes the local value and
// the global one; vim does this so that a:set in a vimrc reaches the buffer
// that is already open and every buffer opened after it.
const (
	Both Where = iota
	SetLocal
	SetGlobal
)

// Global is every option with one value for the whole editor, plus the global
// half of every global-local one.
type Global struct {
	// Searching.
	IgnoreCase bool // 'ignorecase' ic
	SmartCase  bool // 'smartcase' scs
	HlSearch   bool // 'hlsearch' hls
	IncSearch  bool // 'incsearch' is
	WrapScan   bool // 'wrapscan' ws
	Warn       bool // 'warn' -- warn before a shell command over a changed buffer
	Magic      bool // 'magic'
	GDefault   bool // 'gdefault' gd
	ShowMatch  bool // 'showmatch' sm

	// Editing.
	ShiftRound  bool   // 'shiftround' sr
	JoinSpaces  bool   // 'joinspaces' js
	StartOfLine bool   // 'startofline' sol
	Backspace   string // 'backspace' bs
	Selection   string // 'selection' sel
	WhichWrap   string // 'whichwrap' ww
	Paragraphs  string // 'paragraphs' para
	Sections    string // 'sections' sect
	Report      int    // 'report'
	MaxMapDepth int    // 'maxmapdepth' mmd

	// Clipboard and registers.
	Clipboard string // 'clipboard' cb

	// The command line and the message area.
	ShowCmd     bool   // 'showcmd' sc
	Ruler       bool   // 'ruler' ru
	WildMenu    bool   // 'wildmenu' wmnu
	WildIgnore  string // 'wildignore' wig
	WildMode    string // 'wildmode' wim
	CmdHeight   int    // 'cmdheight' ch
	History     int    // 'history' hi
	More        bool   // 'more'
	Confirm     bool   // 'confirm' cf
	LazyRedraw  bool   // 'lazyredraw' lz
	LastStatus  int    // 'laststatus' ls
	ShowTabline int    // 'showtabline' stal
	PumHeight   int    // 'pumheight' ph
	TabPageMax  int    // 'tabpagemax' tpm
	Title       bool   // 'title'

	// Scrolling. ScrollOff and SideScrollOff are the global half of a
	// global-local pair; Window holds the local half.
	ScrollOff     int // 'scrolloff' so
	SideScrollOff int // 'sidescrolloff' siso
	ScrollJump    int // 'scrolljump' sj
	SideScroll    int // 'sidescroll' ss

	// Splits.
	EqualAlways  bool // 'equalalways' ea
	SplitBelow   bool // 'splitbelow' sb
	SplitRight   bool // 'splitright' spr
	WinHeight    int  // 'winheight' wh
	WinWidth     int  // 'winwidth' wiw
	WinMinHeight int  // 'winminheight' wmh
	WinMinWidth  int  // 'winminwidth' wmw

	// Files.
	AutoChdir   bool   // 'autochdir' acd
	Path        string // 'path' pa -- the global half; Buffer.Path is the local one
	Backup      bool   // 'backup' bk
	WriteBackup bool   // 'writebackup' wb
	UndoDir     string // 'undodir' udir
	Directory   string // 'directory' dir
	Hidden      bool   // 'hidden' hid
	Encoding    string // 'encoding' enc
	Background  string // 'background' bg
	DiffOpt     string // 'diffopt' dip

	// The shell, which the vimrc points at homebrew bash for :JsonPretty.
	// ShellPipe and ShellRedir are the two vim builds out of the shell's name
	// at startup: :make needs the first and :r! the second, and a csh gets
	// different ones.
	Shell        string // 'shell' sh
	ShellCmdFlag string // 'shellcmdflag' shcf
	ShellPipe    string // 'shellpipe' sp
	ShellRedir   string // 'shellredir' srr
	ShellTemp    bool   // 'shelltemp' stmp
	SwitchBuf    string // 'switchbuf' swb
	IsFname      string // 'isfname' isf

	// Timing and bells. The vimrc's notimeout/ttimeout/ttimeoutlen=10 dance
	// is the whole reason these are here: it is what makes a bare Escape in a
	// terminal resolve in 10ms instead of a second.
	Timeout     bool   // 'timeout' to
	TTimeout    bool   // 'ttimeout'
	TimeoutLen  int    // 'timeoutlen' tm
	TTimeoutLen int    // 'ttimeoutlen' ttm
	TTyFast     bool   // 'ttyfast' tf
	ErrorBells  bool   // 'errorbells' eb
	VisualBell  bool   // 'visualbell' vb
	VisualBellT string // t_vb, which the vimrc empties so no bell ever fires

	// The GUI. Read by internal/gui and internal/raster, never by the core.
	GuiFont    string // 'guifont' gfn
	GuiOptions string // 'guioptions' go
	LineSpace  int    // 'linespace' lsp
	Mouse      string // 'mouse'

	// The global half of the global-local buffer options.
	CompleteOpt string // 'completeopt' cot
	ErrorFormat string // 'errorformat' efm
	UndoLevels  int    // 'undolevels' ul

	// The global half of the global-local window options.
	VirtualEdit string // 'virtualedit' ve
	StatusLine  string // 'statusline' stl
	ListChars   string // 'listchars' lcs
	FillChars   string // 'fillchars' fcs
}

// Buffer is every option with one value per buffer.
type Buffer struct {
	// Indent and white space.
	TabStop     int    // 'tabstop' ts
	SoftTabStop int    // 'softtabstop' sts
	ShiftWidth  int    // 'shiftwidth' sw
	ExpandTab   bool   // 'expandtab' et
	TextWidth   int    // 'textwidth' tw
	AutoIndent  bool   // 'autoindent' ai
	SmartIndent bool   // 'smartindent' si
	CinWords    string // 'cinwords' cinw

	// Words and pairs.
	IsKeyword  string // 'iskeyword' isk
	MatchPairs string // 'matchpairs' mps
	NrFormats  string // 'nrformats' nf
	FormatOpts string // 'formatoptions' fo
	InferCase  bool   // 'infercase' inf
	Complete   string // 'complete' cpt
	OmniFunc   string // 'omnifunc' ofu

	// The file on disk.
	FileEncoding string // 'fileencoding' fenc
	FileFormat   string // 'fileformat' ff
	FileType     string // 'filetype' ft
	Syntax       string // 'syntax' syn
	EndOfLine    bool   // 'endofline' eol
	FixEndOfLine bool   // 'fixendofline' fixeol
	Modifiable   bool   // 'modifiable' ma
	ReadOnly     bool   // 'readonly' ro
	SwapFile     bool   // 'swapfile' swf
	UndoFile     bool   // 'undofile' udf
	ModeLine     bool   // 'modeline' ml
	BufListed    bool   // 'buflisted' bl
	BufType      string // 'buftype' bt
	BufHidden    string // 'bufhidden' bh

	// Spelling, which the spell autocommands turn on for *.txt and *.md.
	SpellLang string // 'spelllang' spl
	SpellFile string // 'spellfile' spf

	// The local half of the global-local buffer options. An empty string or a
	// negative number here means "ask Global".
	CompleteOpt string // 'completeopt' cot
	ErrorFormat string // 'errorformat' efm
	UndoLevels  int    // 'undolevels' ul
	Path        string // 'path' pa
}

// Window is every option with one value per window.
type Window struct {
	Wrap        bool // 'wrap'
	LineBreak   bool // 'linebreak' lbr
	BreakIndent bool // 'breakindent' bri
	Number      bool // 'number' nu
	RelNumber   bool // 'relativenumber' rnu
	NumberWidth int  // 'numberwidth' nuw
	List        bool // 'list'
	CursorLine  bool // 'cursorline' cul
	Spell       bool // 'spell'

	// Folds. The vimrc maps <Space> to za, so foldmethod matters even though
	// only manual and indent are ever implemented.
	FoldMethod string // 'foldmethod' fdm
	FoldLevel  int    // 'foldlevel' fdl
	FoldEnable bool   // 'foldenable' fen
	FoldColumn int    // 'foldcolumn' fdc

	// Conceal. Set by the vimrc and inert: with no syntax highlighting
	// nothing ever carries a conceal attribute.
	ConcealLevel  int    // 'conceallevel' cole
	ConcealCursor string // 'concealcursor' cocu

	// Scroll is 'scroll': how far CTRL-U and CTRL-D move. It is window-local
	// and it is written by the commands themselves -- a count on either sets
	// it and the new value persists for the window -- which is why it is an
	// option and not a field on the View. Measured against vim 9.2.0321 in a
	// 23-row window: it starts at 11, "5<C-U>" sets it to 5, and a bare
	// "<C-U>" afterwards moves 5.
	Scroll int // 'scroll' scr

	// The local half of the global-local window options. -1 and "" mean "ask
	// Global"; that sentinel is vim's own for 'scrolloff' and 'sidescrolloff'.
	ScrollOff     int    // 'scrolloff' so
	SideScrollOff int    // 'sidescrolloff' siso
	VirtualEdit   string // 'virtualedit' ve
	StatusLine    string // 'statusline' stl
	ListChars     string // 'listchars' lcs
	FillChars     string // 'fillchars' fcs
}

// Options is one window on one buffer's worth of option state.
//
// B and W are the values the editor reads. GB and GW are the global values of
// those same local options: what ":setglobal" writes and what the next buffer
// or window is initialised from. Nothing reads GB or GW to decide behaviour.
type Options struct {
	G  Global
	B  Buffer
	W  Window
	GB Buffer
	GW Window
}

// Builtin is vim's compiled-in default for every option: what ":set name&"
// resets one to, and the state a vim built without a runtime directory starts
// in.
//
// It is not what this editor starts with. Defaults is, and the two differ by
// exactly the six lines defaults.vim runs, which is why they are two functions
// rather than one: ":set scrolloff&" is 0 and the editor starts at 5, and a
// single table cannot say both.
//
// Measured against vim 9.2 patches 1-321, by asking ":set name&" for every
// option in this package's table and reading the value back through
// writefile(), not by reading the documentation. Three values are deliberately
// not the measured ones because the measurement is a property of this machine
// rather than of vim:
//
// - 'shell' is $SHELL, which measured as /bin/zsh here. The documented
// fallback "sh" is used so that a test does not depend on the box.
// - 'shellpipe' and 'shellredir' are chosen from the shell's name at startup;
// the values here are the ones a Bourne-family shell gets, which is every
// shell this editor will meet.
func Builtin() Options {
	o := Options{
		G: Global{
			WrapScan:      true,
			Warn:          true,
			Magic:         true,
			JoinSpaces:    true,
			StartOfLine:   true,
			Backspace:     "indent,eol,start",
			Selection:     "inclusive",
			WhichWrap:     "b,s",
			Paragraphs:    "IPLPPPQPP TPHPLIPpLpItpplpipbp",
			Sections:      "SHNHH HUnhsh",
			Report:        2,
			MaxMapDepth:   1000,
			ShowCmd:       true,
			Ruler:         true,
			WildMenu:      true,
			WildMode:      "full",
			CmdHeight:     1,
			History:       200,
			More:          true,
			LastStatus:    1,
			ShowTabline:   1,
			TabPageMax:    10,
			ScrollJump:    1,
			EqualAlways:   true,
			WinHeight:     1,
			WinWidth:      20,
			WinMinHeight:  1,
			WinMinWidth:   1,
			WriteBackup:   true,
			UndoDir:       ".",
			Directory:     ".,~/tmp,/var/tmp,/tmp",
			Encoding:      "utf-8",
			Background:    "light",
			DiffOpt:       "internal,filler,closeoff,indent-heuristic,inline:char",
			Shell:         "sh",
			ShellCmdFlag:  "-c",
			ShellPipe:     "2>&1| tee",
			ShellRedir:    ">%s 2>&1",
			ShellTemp:     true,
			IsFname:       "@,48-57,/,.,-,_,+,,,#,$,%,~,=",
			Path:          ".,/usr/include,,",
			Timeout:       true,
			TimeoutLen:    1000,
			TTimeoutLen:   -1,
			TTyFast:       true,
			GuiOptions:    "egmrLk",
			CompleteOpt:   "menu,preview",
			ErrorFormat:   DefaultErrorFormat,
			UndoLevels:    1000,
			ListChars:     "eol:$",
			FillChars:     "vert:|,fold:-,eob:~,lastline:@",
			ScrollOff:     0,
			SideScrollOff: 0,
		},
		B: Buffer{
			TabStop:      8,
			ShiftWidth:   8,
			IsKeyword:    "@,48-57,_,192-255",
			MatchPairs:   "(:),{:},[:]",
			NrFormats:    "bin,octal,hex",
			FormatOpts:   "tcq",
			Complete:     ".,w,b,u,t,i",
			CinWords:     "if,else,while,do,for,switch",
			FileFormat:   "unix",
			EndOfLine:    true,
			FixEndOfLine: true,
			Modifiable:   true,
			SwapFile:     true,
			ModeLine:     true,
			BufListed:    true,
			SpellLang:    "en",
			UndoLevels:   -1,
		},
		W: Window{
			Wrap:        true,
			NumberWidth: 4,
			FoldMethod:  "manual",
			FoldEnable:  true,

			// 'scroll' is half the window height and no window exists here.
			// Vim answers 11 in the 23-row window a 24-row terminal gives it,
			// and 0 is vim's own "nobody has said yet": the screen model sets
			// it when it lays a window out, and CTRL-U and CTRL-D write it
			// from their count after that.
			Scroll: 0,

			// -1 and "" are vim's unset markers for the local half of a
			// global-local option, not zero values that happen to work.
			ScrollOff:     -1,
			SideScrollOff: -1,
		},
	}
	o.GB, o.GW = o.B, o.W
	return o
}

// defaultsVim is the whole of defaults.vim this package has to reproduce: the
// six ":set" lines whose effect is still visible in the option table when
// "vim --clean" finishes starting up.
//
// defaults.vim runs about thirty ":set" lines. Twenty-four of them set an
// option to the value it already had, and the six here are the ones that do
// not, found by diffing every option's startup value against its ":set name&"
// value. Reproducing the file line by line would be reproducing a lot of
// nothing; reproducing the difference is the same table with a reason attached
// to every row.
//
// This is the reason --clean and not -u NONE, in this package as everywhere
// else in this tree: -u NONE skips defaults.vim, leaves 'compatible' on, and
// every oracle diff it produces is noise about a vim nobody runs.
var defaultsVim = []string{
	"incsearch",        // line 48, inside if has("reltime")
	"mouse=a",          // line 76, inside if has('mouse') and not a tty-less build
	"nrformats-=octal", // line 53
	"scrolloff=5",      // line 44
	"ttimeout",         // line 36
	"ttimeoutlen=100",  // line 37
}

// Defaults is what "vim --clean -i NONE --not-a-term -s" starts with, which is
// Builtin with defaults.vim applied.
//
// This is the state every oracle case begins from, so it is the state this
// editor has to begin from. Anything reading an option before a vimrc has run
// is reading these values.
func Defaults() Options {
	o := Builtin()
	for _, arg := range defaultsVim {
		if _, err := o.Apply(arg, Both); err != nil {
			// Unreachable: every argument above is a literal in this file and
			// every option it names is in the table. A panic here means
			// somebody edited one of the two and not the other, which is a
			// build-time mistake wearing a run-time disguise.
			panic("options: defaults.vim line " + arg + ": " + err.Error())
		}
	}
	return o
}

// viDefaults is every option in this package's table whose ":set name&vi"
// value differs from its ":set name&vim" value.
//
// Eleven of a hundred and twenty-six, found by asking vim for all three of
// &vi, &vim and & for every option and keeping the rows where they disagreed.
// Nobody types ":set ts&vi", and it is here because vim accepts the syntax and
// answers E488 for a suffix it does not know, so the alternative to a correct
// table is either a wrong answer or an error on a line vim takes.
var viDefaults = map[string]string{
	"backspace":     "",
	"formatoptions": "vt",
	"history":       "0",
	"iskeyword":     "@,48-57,_",
	"modeline":      "no",
	"more":          "no",
	"numberwidth":   "8",
	"ruler":         "no",
	"shelltemp":     "no",
	"showcmd":       "no",
	"whichwrap":     "",
}

// DefaultErrorFormat is vim's 'errorformat'.
//
// Measured, not typed from the documentation: this is what
// "vim --clean -i NONE --not-a-term -s" answers for &errorformat,
// read back through writefile(). It is one line, it is very long, and it is
// mostly gcc: the four %-G lines that throw away "In file included from" and
// the two %D/%X pairs that track "Entering directory" are what make a ":make"
// over a recursive Makefile put the right file name on the right error.
//
// Go's toolchain needs three of these patterns and no more -- "%f:%l:%c:%m",
// "%f:%l:%m" and the %-G that drops a "# package" header -- so a later change
// will almost certainly set its own. This is here because it is the default
// the parser has to cope with first.
const DefaultErrorFormat = `%*[^"]"%f"%*\D%l: %m,"%f"%*\D%l: %m,%-Gg%\?make[%*\d]: *** [%f:%l:%m,%-Gg%\?make: *** [%f:%l:%m,%-G%f:%l: (Each undeclared identifier is reported only once,%-G%f:%l: for each function it appears in.),%-GIn file included from %f:%l:%c:,%-GIn file included from %f:%l:%c\,,%-GIn file included from %f:%l:%c,%-GIn file included from %f:%l,%-G%*[ ]from %f:%l:%c,%-G%*[ ]from %f:%l:,%-G%*[ ]from %f:%l\,,%-G%*[ ]from %f:%l,%f:%l:%c:%m,%f(%l):%m,%f:%l:%m,"%f"\, line %l%*\D%c%*[^ ] %m,%D%*\a[%*\d]: Entering directory %*[` + "`" + `']%f',%X%*\a[%*\d]: Leaving directory %*[` + "`" + `']%f',%D%*\a: Entering directory %*[` + "`" + `']%f',%X%*\a: Leaving directory %*[` + "`" + `']%f',%DMaking %*\a in %f,%f|%l| %m`

// ScrollOffValue resolves the global-local 'scrolloff'.
//
// The rule is vim's: a local value of -1 is unset and the global one is used.
// It is a method rather than a field because the resolution is the whole point
// of a global-local option and a caller that reads o.W.ScrollOff directly gets
// -1 and scrolls the cursor off the screen.
func (o *Options) ScrollOffValue() int {
	if o.W.ScrollOff >= 0 {
		return o.W.ScrollOff
	}
	return o.G.ScrollOff
}

// SideScrollOffValue resolves the global-local 'sidescrolloff'. The vimrc sets
// it to 5, which is what holds a 'nowrap' window five columns off the edge.
func (o *Options) SideScrollOffValue() int {
	if o.W.SideScrollOff >= 0 {
		return o.W.SideScrollOff
	}
	return o.G.SideScrollOff
}

// CompleteOptValue resolves the global-local 'completeopt'. An empty local
// value means the global one, which is the string form of vim's unset.
func (o *Options) CompleteOptValue() string {
	if o.B.CompleteOpt != "" {
		return o.B.CompleteOpt
	}
	return o.G.CompleteOpt
}

// PathValue resolves the global-local 'path': the list of directories gf, gF,
// CTRL-W f, CTRL-W F, CTRL-W gf, CTRL-W gF and ":find" look a file name up in.
//
// An empty local value means the global one, which is vim's unset for a string
// and what a buffer nobody has run ":setlocal path=" in holds. Measured on a
// fresh vim: ":set path?" is ".,/usr/include,", ":setglobal path?" is the same
// and ":setlocal path?" is empty.
func (o *Options) PathValue() string {
	if o.B.Path != "" {
		return o.B.Path
	}
	return o.G.Path
}

// PathEntry is one item of 'path' after the commas have been taken out.
//
// Two of vim's three item shapes are a Dir and the third is a flag, which is
// why this is a struct and not a string: "." is the directory of the file
// being edited and is not known until there is a file, "" is the working
// directory, and everything else is a directory name as written.
type PathEntry struct {
	// Dir is the directory to look in, empty for the working directory.
	Dir string
	// Here is vim's ".": the directory holding the file being edited, which
	// the caller is the one that knows.
	Here bool
}

// SplitPath takes a 'path' value apart into the entries a search walks and the
// items this editor will not walk, each named.
//
// The grammar is vim's and it has three quirks worth writing down once:
//
// - The separator is a comma, and a backslash escapes one, so an item may
// hold a comma of its own. A backslash also escapes a space, which is how
// a directory with a space in its name is spelled in a ":set" line.
// - An EMPTY item is the working directory, not a skipped entry. That is
// what the trailing "," of the default ".,/usr/include," is for, and an
// implementation that drops empty items searches ".", /usr/include and
// nowhere else, which is the working directory missing from every lookup.
// One trailing empty item is dropped, because ".,/usr/include," splits
// into four parts and vim reads three.
// - "." is the directory of the file being edited and NOT the working
// directory. The two are the same in most sessions and differ the moment
// ":cd" or an argument with a directory in it is used.
//
// Refused, by name rather than quietly, because each one is a search this
// editor does not do:
//
// - "**", "dir/**" and "dir/**3": vim's recursive wildcard, with an optional
// depth. Walking a tree is a different search from stat-ing a list of
// directories, and one that silently found nothing would look exactly like
// a file that is not there.
// - Anything else holding "*" or "?": a wildcard the shell would expand.
// - Anything holding "$": vim expands environment variables in 'path' and
// this does not.
//
// "~" is not refused: a leading "~/" is the home directory and the caller
// expands it, because this package may not ask the operating system anything.
func SplitPath(value string) (entries []PathEntry, refused []string) {
	items := splitPathItems(value)
	for i, it := range items {
		if it == "" {
			// The trailing comma of ".,/usr/include," produces a fourth,
			// empty part that vim does not read as a fourth entry.
			if i == len(items)-1 && i > 0 && items[i-1] == "" {
				continue
			}
			entries = append(entries, PathEntry{})
			continue
		}
		if it == "." {
			entries = append(entries, PathEntry{Here: true})
			continue
		}
		if strings.ContainsAny(it, "*?$") {
			refused = append(refused, it)
			continue
		}
		entries = append(entries, PathEntry{Dir: it})
	}
	return entries, refused
}

// splitPathItems cuts a 'path' value at its unescaped commas and takes the
// backslashes off what is left.
func splitPathItems(value string) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(value); i++ {
		switch {
		case value[i] == '\\' && i+1 < len(value):
			i++
			cur.WriteByte(value[i])
		case value[i] == ',':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(value[i])
		}
	}
	return append(out, cur.String())
}

// ErrorFormatValue resolves the global-local 'errorformat'.
func (o *Options) ErrorFormatValue() string {
	if o.B.ErrorFormat != "" {
		return o.B.ErrorFormat
	}
	return o.G.ErrorFormat
}

// VirtualEditValue resolves the global-local 'virtualedit'. This editor leaves
// it empty everywhere and every column function assumes so; it is resolved
// here so that the day somebody sets it, one place can fail loudly.
func (o *Options) VirtualEditValue() string {
	if o.W.VirtualEdit != "" {
		return o.W.VirtualEdit
	}
	return o.G.VirtualEdit
}

// Has reports whether a comma-separated option value contains a flag.
//
// Split and compare, not strings.Contains: "unnamedplus" contains "unnamed",
// so an editor that reads 'clipboard' with Contains puts every yank on the
// wrong selection on a machine where the two differ. macOS is not that
// machine, which is exactly why it would never be noticed here.
func Has(value, flag string) bool {
	for len(value) > 0 {
		item := value
		for i := 0; i < len(value); i++ {
			if value[i] == ',' {
				item, value = value[:i], value[i+1:]
				goto compare
			}
		}
		value = ""
	compare:
		if item == flag {
			return true
		}
	}
	return false
}

// HasChar reports whether a character-list option contains a character, which
// is how 'formatoptions', 'guioptions', 'mouse' and 'concealcursor' are
// spelled: no separators, one letter per flag.
func HasChar(value string, c byte) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == c {
			return true
		}
	}
	return false
}
