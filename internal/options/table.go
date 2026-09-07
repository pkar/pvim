package options

// Spec is one option: its two names, its type, its scope and the two pointers
// that reach its value.
//
// The table below is the single place a name becomes a field. Everything that
// takes an option name from outside the program -- ":set", ":setlocal",
// "&option" in the vimrc, wildmenu completion -- goes through it, so an option
// added as a field and forgotten here is invisible from a vimrc and an option
// listed here with no field does not compile.
type Spec struct {
	// Name is vim's long name, without quotes.
	Name string
	// Abbrev is vim's short name, empty when there is none: 'magic', 'list',
	// 'wrap', 'spell' and 'report' have no abbreviation at all.
	Abbrev string
	Kind   Kind
	Scope  Scope

	// ref returns a pointer to the field holding this option's value in one
	// half of one scope: *bool, *int or *string per Kind. w is SetLocal or
	// SetGlobal and never Both, which Apply resolves by calling twice.
	//
	// For a purely global option both halves are the same field, so
	// ":setlocal ignorecase" and ":set ignorecase" do the same thing, which is
	// what vim does with a global option under :setlocal.
	ref func(o *Options, w Where) any
}

// glb, buf, win and gwin build the ref for each scope, so the table below is
// one line per option instead of four.
func glb(f func(*Global) any) func(*Options, Where) any {
	return func(o *Options, _ Where) any { return f(&o.G) }
}

func buf(f func(*Buffer) any) func(*Options, Where) any {
	return func(o *Options, w Where) any {
		if w == SetGlobal {
			return f(&o.GB)
		}
		return f(&o.B)
	}
}

func win(f func(*Window) any) func(*Options, Where) any {
	return func(o *Options, w Where) any {
		if w == SetGlobal {
			return f(&o.GW)
		}
		return f(&o.W)
	}
}

// gbuf and gwin are the global-local scopes: :setglobal writes the field in
// Global and :setlocal the one in Buffer or Window, and the fallback between
// them is the XxxValue methods in options.go and not this table's business.
func gbuf(l func(*Buffer) any, g func(*Global) any) func(*Options, Where) any {
	return func(o *Options, w Where) any {
		if w == SetGlobal {
			return g(&o.G)
		}
		return l(&o.B)
	}
}

func gwinp(l func(*Window) any, g func(*Global) any) func(*Options, Where) any {
	return func(o *Options, w Where) any {
		if w == SetGlobal {
			return g(&o.G)
		}
		return l(&o.W)
	}
}

// specs is every option this editor knows, in the order ":set all" would print
// them, which is alphabetical by long name.
//
// The order is alphabetical and not vim's internal table order because nothing
// resolves an option name by prefix: vim requires the full name or the
// documented abbreviation and answers E518 for anything else, unlike ex
// commands, where prefix matching is the rule. So this order is for humans
// reading a diff of it.
var specs = []Spec{
	{"autochdir", "acd", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.AutoChdir })},
	{"autoindent", "ai", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.AutoIndent })},
	{"background", "bg", String, ScopeGlobal, glb(func(g *Global) any { return &g.Background })},
	{"backspace", "bs", String, ScopeGlobal, glb(func(g *Global) any { return &g.Backspace })},
	{"backup", "bk", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Backup })},
	{"breakindent", "bri", Bool, ScopeWindow, win(func(w *Window) any { return &w.BreakIndent })},
	{"bufhidden", "bh", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.BufHidden })},
	{"buflisted", "bl", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.BufListed })},
	{"buftype", "bt", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.BufType })},
	{"cinwords", "cinw", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.CinWords })},
	{"clipboard", "cb", String, ScopeGlobal, glb(func(g *Global) any { return &g.Clipboard })},
	// 'cmdheight' is the one option in this table whose scope is a lie, and it
	// is the only lie: vim calls it "global or local to tab page", a fifth
	// scope no other option in the table uses and one this editor has nowhere
	// to put, because a tab page here is a set of windows and not a thing that
	// owns options. One command line height for the process is what the vimrc
	// asks for and what every tab gets.
	{"cmdheight", "ch", Number, ScopeGlobal, glb(func(g *Global) any { return &g.CmdHeight })},
	{"complete", "cpt", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.Complete })},
	{"completeopt", "cot", String, ScopeGlobalBuffer, gbuf(func(b *Buffer) any { return &b.CompleteOpt }, func(g *Global) any { return &g.CompleteOpt })},
	{"concealcursor", "cocu", String, ScopeWindow, win(func(w *Window) any { return &w.ConcealCursor })},
	{"conceallevel", "cole", Number, ScopeWindow, win(func(w *Window) any { return &w.ConcealLevel })},
	{"confirm", "cf", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Confirm })},
	{"cursorline", "cul", Bool, ScopeWindow, win(func(w *Window) any { return &w.CursorLine })},
	{"diffopt", "dip", String, ScopeGlobal, glb(func(g *Global) any { return &g.DiffOpt })},
	{"directory", "dir", String, ScopeGlobal, glb(func(g *Global) any { return &g.Directory })},
	{"encoding", "enc", String, ScopeGlobal, glb(func(g *Global) any { return &g.Encoding })},
	{"endofline", "eol", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.EndOfLine })},
	{"equalalways", "ea", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.EqualAlways })},
	{"errorbells", "eb", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.ErrorBells })},
	{"errorformat", "efm", String, ScopeGlobalBuffer, gbuf(func(b *Buffer) any { return &b.ErrorFormat }, func(g *Global) any { return &g.ErrorFormat })},
	{"expandtab", "et", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.ExpandTab })},
	{"fileencoding", "fenc", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.FileEncoding })},
	{"fileformat", "ff", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.FileFormat })},
	{"filetype", "ft", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.FileType })},
	{"fillchars", "fcs", String, ScopeGlobalWindow, gwinp(func(w *Window) any { return &w.FillChars }, func(g *Global) any { return &g.FillChars })},
	{"fixendofline", "fixeol", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.FixEndOfLine })},
	{"foldcolumn", "fdc", Number, ScopeWindow, win(func(w *Window) any { return &w.FoldColumn })},
	{"foldenable", "fen", Bool, ScopeWindow, win(func(w *Window) any { return &w.FoldEnable })},
	{"foldlevel", "fdl", Number, ScopeWindow, win(func(w *Window) any { return &w.FoldLevel })},
	{"foldmethod", "fdm", String, ScopeWindow, win(func(w *Window) any { return &w.FoldMethod })},
	{"formatoptions", "fo", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.FormatOpts })},
	{"gdefault", "gd", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.GDefault })},
	{"guifont", "gfn", String, ScopeGlobal, glb(func(g *Global) any { return &g.GuiFont })},
	{"guioptions", "go", String, ScopeGlobal, glb(func(g *Global) any { return &g.GuiOptions })},
	{"hidden", "hid", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Hidden })},
	{"history", "hi", Number, ScopeGlobal, glb(func(g *Global) any { return &g.History })},
	{"hlsearch", "hls", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.HlSearch })},
	{"ignorecase", "ic", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.IgnoreCase })},
	{"incsearch", "is", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.IncSearch })},
	{"infercase", "inf", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.InferCase })},
	{"isfname", "isf", String, ScopeGlobal, glb(func(g *Global) any { return &g.IsFname })},
	{"iskeyword", "isk", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.IsKeyword })},
	{"joinspaces", "js", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.JoinSpaces })},
	{"laststatus", "ls", Number, ScopeGlobal, glb(func(g *Global) any { return &g.LastStatus })},
	{"lazyredraw", "lz", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.LazyRedraw })},
	{"linebreak", "lbr", Bool, ScopeWindow, win(func(w *Window) any { return &w.LineBreak })},
	{"linespace", "lsp", Number, ScopeGlobal, glb(func(g *Global) any { return &g.LineSpace })},
	{"list", "", Bool, ScopeWindow, win(func(w *Window) any { return &w.List })},
	{"listchars", "lcs", String, ScopeGlobalWindow, gwinp(func(w *Window) any { return &w.ListChars }, func(g *Global) any { return &g.ListChars })},
	{"magic", "", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Magic })},
	{"matchpairs", "mps", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.MatchPairs })},
	{"maxmapdepth", "mmd", Number, ScopeGlobal, glb(func(g *Global) any { return &g.MaxMapDepth })},
	{"modeline", "ml", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.ModeLine })},
	{"modifiable", "ma", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.Modifiable })},
	{"more", "", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.More })},
	{"mouse", "", String, ScopeGlobal, glb(func(g *Global) any { return &g.Mouse })},
	{"nrformats", "nf", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.NrFormats })},
	{"number", "nu", Bool, ScopeWindow, win(func(w *Window) any { return &w.Number })},
	{"numberwidth", "nuw", Number, ScopeWindow, win(func(w *Window) any { return &w.NumberWidth })},
	{"omnifunc", "ofu", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.OmniFunc })},
	{"paragraphs", "para", String, ScopeGlobal, glb(func(g *Global) any { return &g.Paragraphs })},
	{"path", "pa", String, ScopeGlobalBuffer, gbuf(func(b *Buffer) any { return &b.Path }, func(g *Global) any { return &g.Path })},
	{"pumheight", "ph", Number, ScopeGlobal, glb(func(g *Global) any { return &g.PumHeight })},
	{"readonly", "ro", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.ReadOnly })},
	{"relativenumber", "rnu", Bool, ScopeWindow, win(func(w *Window) any { return &w.RelNumber })},
	{"report", "", Number, ScopeGlobal, glb(func(g *Global) any { return &g.Report })},
	{"ruler", "ru", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Ruler })},
	{"scroll", "scr", Number, ScopeWindow, win(func(w *Window) any { return &w.Scroll })},
	{"scrolljump", "sj", Number, ScopeGlobal, glb(func(g *Global) any { return &g.ScrollJump })},
	{"scrolloff", "so", Number, ScopeGlobalWindow, gwinp(func(w *Window) any { return &w.ScrollOff }, func(g *Global) any { return &g.ScrollOff })},
	{"sections", "sect", String, ScopeGlobal, glb(func(g *Global) any { return &g.Sections })},
	{"selection", "sel", String, ScopeGlobal, glb(func(g *Global) any { return &g.Selection })},
	{"shell", "sh", String, ScopeGlobal, glb(func(g *Global) any { return &g.Shell })},
	{"shellcmdflag", "shcf", String, ScopeGlobal, glb(func(g *Global) any { return &g.ShellCmdFlag })},
	{"shellpipe", "sp", String, ScopeGlobal, glb(func(g *Global) any { return &g.ShellPipe })},
	{"shellredir", "srr", String, ScopeGlobal, glb(func(g *Global) any { return &g.ShellRedir })},
	{"shelltemp", "stmp", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.ShellTemp })},
	{"shiftround", "sr", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.ShiftRound })},
	{"shiftwidth", "sw", Number, ScopeBuffer, buf(func(b *Buffer) any { return &b.ShiftWidth })},
	{"showcmd", "sc", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.ShowCmd })},
	{"showmatch", "sm", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.ShowMatch })},
	{"showtabline", "stal", Number, ScopeGlobal, glb(func(g *Global) any { return &g.ShowTabline })},
	{"sidescroll", "ss", Number, ScopeGlobal, glb(func(g *Global) any { return &g.SideScroll })},
	{"sidescrolloff", "siso", Number, ScopeGlobalWindow, gwinp(func(w *Window) any { return &w.SideScrollOff }, func(g *Global) any { return &g.SideScrollOff })},
	{"smartcase", "scs", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.SmartCase })},
	{"smartindent", "si", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.SmartIndent })},
	{"softtabstop", "sts", Number, ScopeBuffer, buf(func(b *Buffer) any { return &b.SoftTabStop })},
	{"spell", "", Bool, ScopeWindow, win(func(w *Window) any { return &w.Spell })},
	{"spellfile", "spf", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.SpellFile })},
	{"spelllang", "spl", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.SpellLang })},
	{"splitbelow", "sb", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.SplitBelow })},
	{"splitright", "spr", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.SplitRight })},
	{"startofline", "sol", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.StartOfLine })},
	{"statusline", "stl", String, ScopeGlobalWindow, gwinp(func(w *Window) any { return &w.StatusLine }, func(g *Global) any { return &g.StatusLine })},
	{"swapfile", "swf", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.SwapFile })},
	{"switchbuf", "swb", String, ScopeGlobal, glb(func(g *Global) any { return &g.SwitchBuf })},
	{"syntax", "syn", String, ScopeBuffer, buf(func(b *Buffer) any { return &b.Syntax })},
	{"tabpagemax", "tpm", Number, ScopeGlobal, glb(func(g *Global) any { return &g.TabPageMax })},
	{"tabstop", "ts", Number, ScopeBuffer, buf(func(b *Buffer) any { return &b.TabStop })},
	{"textwidth", "tw", Number, ScopeBuffer, buf(func(b *Buffer) any { return &b.TextWidth })},
	{"timeout", "to", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Timeout })},
	{"timeoutlen", "tm", Number, ScopeGlobal, glb(func(g *Global) any { return &g.TimeoutLen })},
	{"title", "", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Title })},
	{"ttimeout", "", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.TTimeout })},
	{"ttimeoutlen", "ttm", Number, ScopeGlobal, glb(func(g *Global) any { return &g.TTimeoutLen })},
	{"ttyfast", "tf", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.TTyFast })},
	{"undodir", "udir", String, ScopeGlobal, glb(func(g *Global) any { return &g.UndoDir })},
	{"undofile", "udf", Bool, ScopeBuffer, buf(func(b *Buffer) any { return &b.UndoFile })},
	{"undolevels", "ul", Number, ScopeGlobalBuffer, gbuf(func(b *Buffer) any { return &b.UndoLevels }, func(g *Global) any { return &g.UndoLevels })},
	{"virtualedit", "ve", String, ScopeGlobalWindow, gwinp(func(w *Window) any { return &w.VirtualEdit }, func(g *Global) any { return &g.VirtualEdit })},
	{"visualbell", "vb", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.VisualBell })},
	{"whichwrap", "ww", String, ScopeGlobal, glb(func(g *Global) any { return &g.WhichWrap })},
	{"wildignore", "wig", String, ScopeGlobal, glb(func(g *Global) any { return &g.WildIgnore })},
	{"wildmenu", "wmnu", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.WildMenu })},
	{"wildmode", "wim", String, ScopeGlobal, glb(func(g *Global) any { return &g.WildMode })},
	{"winheight", "wh", Number, ScopeGlobal, glb(func(g *Global) any { return &g.WinHeight })},
	{"winminheight", "wmh", Number, ScopeGlobal, glb(func(g *Global) any { return &g.WinMinHeight })},
	{"winminwidth", "wmw", Number, ScopeGlobal, glb(func(g *Global) any { return &g.WinMinWidth })},
	{"winwidth", "wiw", Number, ScopeGlobal, glb(func(g *Global) any { return &g.WinWidth })},
	{"wrap", "", Bool, ScopeWindow, win(func(w *Window) any { return &w.Wrap })},
	{"warn", "", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.Warn })},
	{"wrapscan", "ws", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.WrapScan })},
	{"writebackup", "wb", Bool, ScopeGlobal, glb(func(g *Global) any { return &g.WriteBackup })},
}

// accepted is the set of option names pvim parses and deliberately does
// nothing with.
//
// It is not the same thing as an unknown option and it must not answer E518.
// The vimrc sets 'compatible' off and 't_vb' empty, and
// 'conceallevel' is accepted and inert; a vimrc line that raises an error on
// startup is a line the user has to delete, and the whole point of the vimrc
// loader is that the file keeps working under MacVim unchanged. Anything here
// is a promise that the option was seen and dropped on purpose, and the day
// one of them is implemented it moves into specs and out of this map.
var accepted = map[string]string{
	"compatible": "cp",
	"cpoptions":  "cpo",
	"t_vb":       "",
	"viminfo":    "vi",
	"ttybuiltin": "tbi",
	"term":       "",
	"lines":      "",
	"columns":    "co",
	"secure":     "",
	"exrc":       "ex",
	"langmenu":   "lm",
	"helplang":   "hlg",
	"belloff":    "bo",
	"synmaxcol":  "smc",
}

// byName and byAbbrev index specs, and acceptedNames indexes the accepted map
// both ways round. All three are built once at init.
var (
	byName        = map[string]*Spec{}
	byAbbrev      = map[string]*Spec{}
	acceptedNames = map[string]bool{}
)

func init() {
	for i := range specs {
		s := &specs[i]
		byName[s.Name] = s
		if s.Abbrev != "" {
			byAbbrev[s.Abbrev] = s
		}
	}
	for long, short := range accepted {
		acceptedNames[long] = true
		if short != "" {
			acceptedNames[short] = true
		}
	}
}

// Lookup finds an option by its long or its short name.
//
// Exact match only, both ways round. Vim does not prefix-match option names
// the way it prefix-matches ex commands: ":set ignorec" is E518 and so is
// ":set i". Every abbreviation in the table is one vim documents.
func Lookup(name string) (*Spec, bool) {
	if s, ok := byName[name]; ok {
		return s, true
	}
	s, ok := byAbbrev[name]
	return s, ok
}

// Names returns every option's long name, alphabetically. It is what wildmenu
// completion after ":set " offers.
func Names() []string {
	names := make([]string, 0, len(specs))
	for i := range specs {
		names = append(names, specs[i].Name)
	}
	return names
}

// Accepted reports whether name is an option pvim parses and ignores. See the
// accepted map for why that is not the same as an unknown option.
func Accepted(name string) bool { return acceptedNames[name] }
