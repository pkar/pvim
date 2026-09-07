// Package syntax highlights a buffer by running vim's own syntax files.
//
// It writes no rules for any language. Vim ships 773 files under
// runtime/syntax, every one of them built out of three commands -- `syn
// keyword`, `syn match` and `syn region` -- whose arguments are vim regexes,
// and internal/regex already translates those into Go's. So the thing to build
// is not a highlighter for Go and a highlighter for Python; it is an
// interpreter for the small language those three commands make up, plus enough
// of a vimscript reader to get from the top of a syntax file to the bottom of
// it. That is this package. Point it at a filetype and it reads the file vim
// would have read.
//
// syntax highlighting under "Do not build" and names tree-sitter
// as the route if it is ever wanted. Tree-sitter needs cgo and this binary has
// none, so that door was never open; this one was, and it costs a package
// instead of a C toolchain and a grammar per language.
//
// # The gate
//
// vim can be asked what it highlighted, headlessly and per byte column:
//
//	synIDattr(synID(line, col, 1), "name")
//
// TestAgreement sweeps a file position by position through the real
// /opt/homebrew/bin/vim and through this package and diffs the group names.
// That comparison is the only measurement of this package worth having;
// everything else is an opinion about what vim does. Measured
// against vim 9.2.0321, on the fixtures in testdata:
//
//	sample.go.txt 270/270 100.0%
//	sample_real.go.txt 17181/17198 99.9% (internal/screen/winline.go)
//	sample.py.txt 685/685 100.0%
//	sample.tf.txt 245/246 99.6%
//	sample.json.txt 187/188 99.5%
//	sample.md.txt 288/331 87.0%
//	sample.sh.txt 191/350 54.6%
//	sample.yaml.txt 102/274 37.2%
//
// Go, Python and Terraform are the ones this editor is pointed at all day and
// they are done. The one JSON position is a closing brace whose jsonFold region
// is refused. The markdown gap is entirely setext headings and indented code
// blocks, both of which are patterns that must cross a line break.
//
// sh and yaml are the two that are not good, and they are in the table because
// a gate that only measures what works is not a gate. yaml is 43 refusals deep
// and every one of them is the same thing: yaml.vim builds its patterns by
// calling substitute() with a `\=` replacement -- a vimscript expression
// evaluated per match, to make one character class out of another -- and that
// is a second evaluator inside the first. Every yaml difference is a colour
// that is missing and not one that is wrong; measured, 172 positions differ and
// all 172 are pvim saying nothing where vim said something.
//
// sh is not so clean. 129 of its 159 differences are missing colour, and 30 are
// the wrong group: a refused item leaves a lower-priority one showing through,
// so `echo` inside a `$(...)` comes out shEcho where vim says shStatement. That
// is the one failure mode refusing an item cannot prevent, and it is the same
// shape as json.vim's jsonBoolean, which is refused and lets jsonNoQuotesError
// paint `true` when it is not followed by a quote. Where the register of
// differences would go if this package had one, those two rows are it.
//
// # Speed
//
// Highlighting runs on every frame for every visible line, so the numbers
// matter more than the structure. On a 40,000-line Go file, go1.27.1, three
// runs of `-benchtime 10x` at load average 6.8 with an oracle running beside
// it:
//
//	whole file, every line from the top 619 ms 15 us a line
//	opening a file, first 60 lines 885 us (870/876/910)
//	every frame after that, 60 lines 0.38 us off the span cache
//	:40000 into an unwalked file 8.9 ms 500 lines of syn sync
//	a keystroke, 60 lines redrawn 706 us (693/705/721)
//
// The two that are on the path of a frame are the third and the fifth, and
// both are inside the 4 ms budget a whole repaint gets. The first
// is the work the cache exists to avoid and nothing asks for it in one go.
//
// Three caches, and the middle one is the whole design. The state at the end of
// every line -- the stack of regions still open and the nextgroup still pending
// -- is kept, so line 900 is one line of work when line 899 is known. The spans
// a line resolved to are kept too, so a redraw of a screen nothing has touched
// is a map lookup a line. And within one line, where each item's pattern next
// matches is cached, which is vim's own next_match_col and is what stops every
// column asking every item for a fresh regexp search.
//
// Changed(line) throws away everything from a line down, because that is how
// far an edit can reach: a quote typed on line 40 opens a string region that
// runs to the end of the file.
//
// # syn sync
//
// Walking from the top of the buffer is the only way to be certain, and it is
// what happens whenever it is affordable. A jump to line 40,000 of a file
// nobody has looked at is not: it would be 610 ms before the first frame. Vim's
// answer is `syn sync minlines=N` -- start N lines back and take the state
// there as nothing -- and this package takes it, with `fromstart` meaning what
// it says and a file that specifies neither getting 500, which is the number
// go.vim asks for. The cost is that a region longer than the sync window is
// wrong at the top of the screen after a jump, and right again as soon as the
// scroll reaches it from above. That is exactly the deal vim offers.
//
// `syn sync match`, `grouphere`, `groupthere`, `ccomment` and `linecont` are
// read and recorded as refusals. They only change where a recompute starts, so
// ignoring them costs accuracy after a jump and nothing else.
//
// # What it does not do, by name
//
// The rule the whole package is built around: an item that cannot be run
// correctly is dropped and named, never approximated. A dropped item leaves its
// text in the colour of whatever encloses it, which is what vim gives text no
// rule matched; an approximated item paints the wrong thing in a confident
// colour and there is no way to tell that from a bug in the file. Every refusal
// carries the syntax file, the line, the group and the offending text, and they
// are collected on Syntax.Refusals. See refuse.go.
//
// Measured what is refused, with how much of it is the filetype's
// own file rather than one it pulls in:
//
//	go 0
//	json 0
//	terraform 0
//	python 8 all 8 its own: doctests and the matrix-multiply rules
//	markdown 88 7 its own; the rest is html, xml and javascript
//	yaml 39 33 its own: substitute() with a \= replacement
//	sh 113 113 its own: 52 syn sync forms, 51 conditions on a
//	 getline() this package cannot answer, 10 lookarounds
//
// The constructs behind them:
//
// - Patterns that must cross a line break. A `\n` atom compiles and simply
// never matches, because the matcher is handed one line at a time and that
// is what makes a 40,000-line file cost one line's work a frame. `\_s` and
// the rest of the `\_` classes degrade to their same-line halves, which is
// what they already meant on one line. Both failures are a colour that is
// missing. What it costs: markdown's setext headings (`^.\+\n=\+$`) and its
// indented code blocks, and go's goPackageComment, which links to Comment
// and so looks identical to the goComment that takes its place.
// - Lookaround that is not a leading `\@<=` or a trailing `\@=`. Those two
// are rewritten into `\zs` and `\ze`, per top-level alternative, and a
// trailing `\@!` is run as a second anchored search at the match end. A
// `\@<!`, or any of them in the middle of a pattern, is refused: RE2 has no
// lookaround and constraining a match that is still being found is what a
// backtracking engine is for, which not to write.
// - `\%V`, `\%23l`, `\%23c`, `\%#`, `\%'m` and backreferences, which
// internal/regex refuses and this passes through by name.
// - vimscript beyond the subset in script.go, expr.go and builtin.go. What is
// there: if/elseif/else/endif, for/endfor, function/endfunction and calls
// to one, let with a heredoc, unlet, finish, execute, runtime, source, try
// as a pass-through, the whole expression grammar, and thirty-odd builtins
// including has(), exists(), get(), index(), matchstr(), substitute() and
// the =~ operator, both of which go through internal/regex like every other
// vim pattern here. What is not: while loops, dictionaries, `map()` and
// `filter()`, funcrefs, and a substitute() whose replacement starts `\=`,
// which is a vimscript expression evaluated per match -- a second evaluator
// inside the first, and the thing yaml.vim is built out of. An `if` whose
// condition will not evaluate takes the else branch, which in every file in
// the shipped runtime is the plain rule with the optional extra in the then
// branch, so an unreadable condition costs colour and never invents it.
// - `conceal`, `concealends` and `cchar` are parsed and inert. Concealing is
// out of scope, so the vimrc's `conceallevel=2` does nothing here.
// - `fold` is parsed and inert. Fold-by-syntax is on the do-not-build
// list beside `foldmethod=expr`.
// - `@Spell` and `syn spell` are read and dropped: spell checking is later work
// and is a wordlist and not a syntax attribute.
//
// # One deliberate departure in the matcher
//
// Vim lets a contained region that runs past its container push the container's
// end outward, unless the container said `keepend`. Here the inner one is cut
// short instead, always. The reason is not simplicity: a region that never
// finds its end, opened inside a `syn match` that ends on the line it started,
// would otherwise sit on the stack for the rest of the buffer and paint
// everything below it -- json.vim's jsonKeyword over a file whose last key has
// no colon, turning one malformed line into a file with no colour in it. A
// region cut short is a colour that stops early. A region that leaks is every
// colour after it wrong.
//
// # Where the syntax files are
//
// Not at /opt/homebrew/share/vim/vim92, which holds an empty "vimfiles" on this
// machine. /opt/homebrew/bin/vim is a symlink into MacVim, and $VIMRUNTIME is
//
//	/opt/homebrew/Cellar/macvim/9.2.0321/MacVim.app/Contents/Resources/vim/runtime
//
// DefaultRuntime probes $VIMRUNTIME first and then a list of known locations,
// and only accepts a directory that has syntax/syntax.vim in it, which is the
// check that skips the empty one. internal/filetype found the same thing a day
// earlier and says so in its own package doc.
//
// # What it looks like on this config
//
// Nearly plain, and that is correct. nofrils-dark defines Comment as #6C6C6C
// grey and Todo as green on black, and gives String, Character, Statement,
// Type, Constant, Identifier, PreProc, Special, Number, Keyword, Function,
// Operator and Label guifg=NONE guibg=NONE, which is Normal. The scheme's whole
// point is not highlighting, so a correct highlighter under it paints comments
// grey, TODO green, and everything else the colour it already was. Anyone
// checking this by eye should load a colourscheme with real colours first.
package syntax
