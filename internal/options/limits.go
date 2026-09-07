package options

// The value limits and legal-value sets ":set" enforces, all measured against
// vim 9.2 patches 1-321 rather than read out of the
// documentation.
//
// The measurement was mechanical: for every number option in this package's
// table, ":set name=-1" and ":set name=0" inside a try/catch with v:exception
// written out, then a bisection for the maximum; for every character-list
// option, all 94 printable ASCII characters one at a time; for every option
// with an enumerated value, each word the documentation names plus a bogus one.
// That is why the sets below have members the documentation does not mention
// ('formatoptions' takes "," and "/") and lacks ones it does (this build has no
// 'formatoptions' "k").
//
// Getting these wrong is not a cosmetic matter. cmd/oracle diffs vim's message
// line, so ":set fo^=qz" has to answer "E539: Illegal character <z>: fo^=qz"
// and not silently store a flag vim would have refused.

// numMin is the smallest value ":set" will store, per option. An option absent
// from this map takes any value, negative ones included: 'undolevels' is -1 for
// "no undo", 'linespace' takes a negative to overlap rows, and 'laststatus',
// 'showtabline', 'foldlevel', 'maxmapdepth', 'pumheight', 'scrolljump',
// 'softtabstop', 'tabpagemax' and 'ttimeoutlen' all measured as unbounded
// below.
//
// The distinction between 0 and 1 is not guessable and it is not uniform:
// 'shiftwidth' takes 0 (meaning "use 'tabstop'"), 'tabstop' does not.
var numMin = map[string]int{
	"cmdheight":     1,
	"conceallevel":  0,
	"foldcolumn":    0,
	"history":       0,
	"numberwidth":   1,
	"report":        0,
	"scroll":        0,
	"scrolloff":     0,
	"shiftwidth":    0,
	"sidescroll":    0,
	"sidescrolloff": 0,
	"tabstop":       1,
	"textwidth":     0,
	"timeoutlen":    0,
	"winheight":     1,
	"winminheight":  0,
	"winminwidth":   0,
	"winwidth":      1,
}

// numMax is the largest value ":set" will store, per option, for the five
// options that measured a ceiling. Everything else took 2^63-1 without
// complaint. Over the ceiling is E474 and not E487, which is vim's way of
// saying the number parsed and the option refused it.
var numMax = map[string]int{
	"conceallevel": 3,
	"foldcolumn":   12,
	"history":      10000,
	"numberwidth":  20,
	"tabstop":      9999,
}

// numMinCode is the E-code an option answers when it is given a number below
// its minimum. Every option answers E487 except 'scroll', which has its own,
// because CTRL-U and CTRL-D write 'scroll' from a count and vim wanted the
// message to name the command's argument rather than an option nobody typed.
func numMinCode(name string) (string, string) {
	if name == "scroll" {
		return "E49", "Invalid scroll size"
	}
	return "E487", "Argument must be positive"
}

// flagChars is the set of characters each character-list option accepts, one
// letter per flag and no separator. A character outside the set is E539.
//
// Every one of these was found by trying all 94 printable ASCII characters
// against a fresh vim, which is why 'formatoptions' has "," and "/" in it: they
// are real flags (see |fo-table|), they look like typos, and a hand-typed set
// would have dropped them.
var flagChars = map[string]string{
	"concealcursor": "cinv",
	"formatoptions": ",/12BM]abcjlmnopqrtvw",
	"guioptions":    "!ACFLMPRTabcdefghiklmprstv",
	"mouse":         "achinrv",
}

// listItems is the set of items each comma-separated option accepts. An item
// outside the set is E474.
//
// Only the options this editor branches on are here. 'complete', 'wildignore',
// 'iskeyword', 'isfname', 'matchpairs', 'paragraphs', 'sections',
// 'shellcmdflag' and the rest take anything, either because vim itself does not
// check them ('complete' accepts the vimrc's undocumented "U") or because
// checking would mean a second implementation of something else's parser.
var listItems = map[string][]string{
	"background":  {"light", "dark"},
	"backspace":   {"indent", "eol", "start", "nostop", "0", "1", "2", "3"},
	"bufhidden":   {"", "hide", "unload", "delete", "wipe"},
	"buftype":     {"", "nofile", "nowrite", "acwrite", "quickfix", "help", "terminal", "prompt", "popup"},
	"clipboard":   {"unnamed", "unnamedplus", "autoselect", "autoselectplus", "autoselectml", "html"},
	"completeopt": {"menu", "menuone", "longest", "preview", "popup", "popuphidden", "noinsert", "noselect", "fuzzy"},
	"diffopt": {"filler", "iblank", "icase", "iwhite", "iwhiteall", "iwhiteeol",
		"horizontal", "vertical", "closeoff", "hiddenoff", "followwrap", "internal", "indent-heuristic"},
	"fileformat":  {"unix", "dos", "mac"},
	"foldmethod":  {"manual", "indent", "expr", "marker", "syntax", "diff"},
	"nrformats":   {"alpha", "octal", "hex", "bin", "unsigned", "blank"},
	"selection":   {"inclusive", "exclusive", "old"},
	"switchbuf":   {"useopen", "usetab", "split", "vsplit", "newtab", "uselast"},
	"virtualedit": {"block", "insert", "all", "onemore", "none"},
	"wildmode":    {"", "full", "longest", "list", "lastused"},
}

// listPrefixes is the set of "word:argument" items each comma-separated option
// accepts on top of its plain words. 'diffopt' takes "context:4" and
// "algorithm:patience"; 'clipboard' takes "exclude:pattern".
//
// The argument is not checked. Vim does check it, and reproducing that would
// mean parsing a regexp for 'clipboard' and an algorithm name for 'diffopt',
// both of which are somebody else's parser and neither of which this editor
// reads yet.
var listPrefixes = map[string][]string{
	"clipboard": {"exclude"},
	"diffopt":   {"context", "foldcolumn", "algorithm", "inline"},
}

// unvalidated names the options with structure that this package deliberately
// does not check, so that the list is a thing a reader can see rather than an
// absence they have to infer.
//
// - 'encoding' and 'fileencoding': vim's accepted set depends on how the
// binary was built (this one refuses "ascii"), so a check here would refuse
// names another vim takes. The plan supports utf-8 and latin1 and nothing
// else reads the field.
// - 'listchars' and 'fillchars': every item is "word:character" and the word
// list was never measured, so a check here would be a guess, and a guess
// that refuses an item vim takes breaks a vimrc rather than catching a typo.
// - 'guifont', 'shell', 'directory', 'undodir', 'statusline', 'errorformat',
// 'omnifunc', 'wildignore', 'iskeyword', 'isfname', 'matchpairs',
// 'complete', 'paragraphs', 'path', 'sections', 'spelllang', 'spellfile':
// free text, or a grammar of their own that their reader parses.
// 'path' is the last of those: its items are directory names with vim's
// own "." and "**" among them, and Options.PathValue's reader is where
// the ones this editor does not walk are refused by name.
var unvalidated = []string{
	"complete", "directory", "encoding", "errorformat", "fileencoding", "fillchars",
	"guifont", "isfname", "iskeyword", "listchars", "matchpairs", "omnifunc",
	"paragraphs", "path", "sections", "shell", "shellcmdflag", "shellpipe", "shellredir",
	"spellfile", "spelllang", "statusline", "undodir", "wildignore",
}
