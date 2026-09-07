package vimrc

import (
	"errors"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
)

// TestNothingIsLostLoadingTheVimrc counts what came out against what vim has
// after sourcing the same file.
//
// The mapping count is vim's, measured with
// "vim -u NONE -i NONE -s" sourcing the vimrc and counting the lines of
// execute('map') and execute('map!'): eight, because has('gui_running') is
// false there as it is in the terminal frontend. The autocommand count is the
// twenty-five lines from 159 to 183 plus two in FastEscape, two in
// spell_settings and one in myvimrc.
//
// It is a count and not a list because the lists are asserted line by line
// elsewhere. What a count catches that a list does not is a statement quietly
// dropped: a parser that fails to recognise ":au filetype go ..." because the
// event is spelled in lower case loses one row and every other assertion in
// this package still passes.
func TestNothingIsLostLoadingTheVimrc(t *testing.T) {
	cfg, _, res := load(t, false)

	if len(res.Errors) != 0 {
		t.Fatalf("the vimrc did not load cleanly: %v", res.Log())
	}
	if got, want := len(cfg.Maps), 8; got != want {
		t.Errorf("%d mappings, want %d; vim has %d after sourcing the same file", got, want, want)
	}
	if got, want := len(cfg.AutoCmds), 30; got != want {
		t.Errorf("%d autocommands, want %d", got, want)
	}
	if got, want := len(cfg.Commands), 1; got != want {
		t.Errorf("%d user commands, want %d", got, want)
	}
	if got, want := len(cfg.Vars), 11; got != want {
		t.Errorf("%d variables applied, want %d: %v", got, want, cfg.Vars)
	}
}

// TestAnUnknownCommandDoesNotStopTheFile is vim's own behaviour and the reason
// Unknown is a statement and not an error return. Measured vim
// prints "line 3:" and "E492: Not an editor command: frobnicate foo" and
// carries on to line 4.
func TestAnUnknownCommandDoesNotStopTheFile(t *testing.T) {
	cfg := NewConfig()
	src := []byte("set ts=3\nfrobnicate foo\nset sw=7\n")
	res := Run(src, "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})

	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one", res.Errors)
	}
	d := res.Errors[0]
	if d.Line != 2 {
		t.Errorf("the error is on line %d, want 2", d.Line)
	}
	if !strings.Contains(d.Error(), "E492") || !strings.Contains(d.Error(), "frobnicate foo") {
		t.Errorf("message is %q, want an E492 quoting the line", d.Error())
	}
	if v, _ := cfg.Opts.Get("shiftwidth"); v.String() != "=7" {
		t.Errorf("the file stopped at the bad line; &shiftwidth = %q", v.String())
	}
}

// TestFinishStopsTheFile.
func TestFinishStopsTheFile(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("set ts=3\nfinish\nset sw=7\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if !res.Finished {
		t.Error("Finished is false after a :finish")
	}
	if v, _ := cfg.Opts.Get("shiftwidth"); v.String() == "=7" {
		t.Error("the line after :finish ran")
	}
}

// TestIfNesting. The case that matters is the last one: a condition inside a
// branch that is being skipped is never evaluated, so a broken expression
// there is not an error. Vim behaves the same way, and the vimrc leans on it:
// line 193 sits inside "if has('autocmd')" and calls filereadable() on a path
// that need not exist.
func TestIfNesting(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want string // the value of &shiftwidth afterwards
		errs int
	}{
		{src: "if 1\nset sw=1\nendif\n", want: "=1"},
		{src: "if 0\nset sw=1\nendif\n", want: "=8"},
		{src: "if 0\nset sw=1\nelse\nset sw=2\nendif\n", want: "=2"},
		{src: "if 0\nset sw=1\nelseif 1\nset sw=2\nelse\nset sw=3\nendif\n", want: "=2"},
		{src: "if 1\nset sw=1\nelseif 1\nset sw=2\nendif\n", want: "=1"},
		{src: "if 0\nif 1\nset sw=1\nendif\nendif\n", want: "=8"},
		{src: "if 1\nif 0\nset sw=1\nelse\nset sw=2\nendif\nendif\n", want: "=2"},
		// The expression in the skipped branch is never parsed, so its
		// refused operator is never reached.
		{src: "if 0\nif 'a' =~ 'b'\nset sw=1\nendif\nendif\n", want: "=8"},
		// And the same expression in a branch that does run is an error.
		{src: "if 1\nif 'a' =~ 'b'\nset sw=1\nendif\nendif\n", want: "=8", errs: 1},
		// An :endif with no :if, and an :if with no :endif.
		{src: "endif\n", want: "=8", errs: 1},
		{src: "if 1\nset sw=1\n", want: "=1", errs: 1},
	} {
		cfg := NewConfig()
		res := Run([]byte(tc.src), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
		if len(res.Errors) != tc.errs {
			t.Errorf("%q: errors = %v, want %d", tc.src, res.Errors, tc.errs)
		}
		if v, _ := cfg.Opts.Get("shiftwidth"); v.String() != tc.want {
			t.Errorf("%q: &shiftwidth = %q, want %q", tc.src, v.String(), tc.want)
		}
	}
}

// TestBarSplittingRespectsTheIf is the shape the vimrc's BufWritePost
// autocommand runs as, reduced to one line so the control flow is the only
// thing under test.
func TestBarSplittingRespectsTheIf(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want string
	}{
		{src: "set sw=1 | set ts=3", want: "=1"},
		{src: "if 0 | set sw=1 | endif", want: "=8"},
		{src: "if 1 | set sw=1 | endif", want: "=1"},
		{src: "set sw=1 | if 0 | set sw=2 | endif", want: "=1"},
		{src: "set sw=1 | if 1 | set sw=2 | endif", want: "=2"},
	} {
		cfg := NewConfig()
		res := Run([]byte(tc.src+"\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
		for _, d := range res.Errors {
			t.Errorf("%q: %v", tc.src, d)
		}
		if v, _ := cfg.Opts.Get("shiftwidth"); v.String() != tc.want {
			t.Errorf("%q: &shiftwidth = %q, want %q", tc.src, v.String(), tc.want)
		}
	}
}

// TestSetScopes. ":setlocal" writes the buffer half alone, which is exactly
// what makes an autocmd's setting stop at the buffer it was for.
func TestSetScopes(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("set ts=2\nsetlocal ts=4\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if v, _ := cfg.Opts.Get("tabstop"); v.String() != "=4" {
		t.Errorf("&tabstop = %q, want =4", v.String())
	}
	if v, _ := cfg.Opts.GetIn("tabstop", options.SetGlobal); v.String() != "=2" {
		t.Errorf("&g:tabstop = %q, want =2", v.String())
	}
}

// TestMapArguments. The vimrc uses <buffer> once and nothing else, so the
// other four are here to stop the first config that uses one from being
// parsed as part of the left-hand side.
func TestMapArguments(t *testing.T) {
	cfg := NewConfig()
	src := []byte("nnoremap <silent> <buffer> <nowait> <unique> <expr> gx foo()\n")
	res := Run(src, "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	m, ok := findMap(cfg, "gx")
	if !ok {
		t.Fatalf("gx is not mapped: %v", cfg.Maps)
	}
	if !m.Silent || !m.Buffer || !m.Nowait || !m.Unique || !m.Expr {
		t.Errorf("arguments = %+v, want all five set", m)
	}
	if m.RHS != "foo()" {
		t.Errorf("rhs = %q, want foo()", m.RHS)
	}
}

// TestUnmapAndMapclear.
func TestUnmapAndMapclear(t *testing.T) {
	cfg := NewConfig()
	src := []byte("nnoremap gx foo\nvnoremap gy bar\nnunmap gx\n")
	Run(src, "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if _, ok := findMap(cfg, "gx"); ok {
		t.Error("gx survived :nunmap")
	}
	if _, ok := findMap(cfg, "gy"); !ok {
		t.Error(":nunmap took a visual-mode mapping with it")
	}

	Run([]byte("mapclear\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if _, ok := findMap(cfg, "gy"); ok {
		t.Error("gy survived :mapclear")
	}
}

// TestAugroupClear is the "au!" at the top of each of the vimrc's three
// augroups: it removes what that group had and leaves every other group
// alone.
func TestAugroupClear(t *testing.T) {
	cfg := NewConfig()
	src := []byte(
		"augroup A\nau BufRead *.go set sw=1\naugroup END\n" +
			"augroup B\nau BufRead *.md set sw=2\naugroup END\n" +
			"augroup A\nau!\naugroup END\n")
	res := Run(src, "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if got := len(cfg.Match("BufRead", "x.go")); got != 0 {
		t.Errorf("group A still has %d autocommands after au!", got)
	}
	if got := len(cfg.Match("BufRead", "x.md")); got != 1 {
		t.Errorf("group B lost its autocommands to group A's au!; it has %d", got)
	}
}

// TestUnlet.
func TestUnlet(t *testing.T) {
	cfg := NewConfig()
	ld := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts})
	ld.Run([]byte("let g:ctrlp_max_height = 40\nunlet g:ctrlp_max_height\n"), "x.vim")
	if _, ok := cfg.Vars["g:ctrlp_max_height"]; ok {
		t.Error("the variable survived :unlet")
	}
	if _, ok := ld.Vars["g:ctrlp_max_height"]; ok {
		t.Error("the loader still sees it, so exists() would say true")
	}
}

// TestSourceBangIsRefused. ":source!" reads a file as normal-mode keystrokes,
// which is a different feature wearing the same name, and mistaking one for
// the other would run a config file as a macro.
func TestSourceBangIsRefused(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("source! /tmp/x\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one", res.Errors)
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("it was sourced anyway: %v", cfg.Sources)
	}
}

// TestASinkErrorLandsOnTheLine. A ":set" of an option internal/options does
// not have is E518, and the loader has to carry it to the message line with
// the file and the line on the front rather than swallowing it.
func TestASinkErrorLandsOnTheLine(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("set ts=2\nset frobnicate\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one", res.Errors)
	}
	d := res.Errors[0]
	if d.Line != 2 {
		t.Errorf("line = %d, want 2", d.Line)
	}
	if !strings.Contains(d.Error(), "E518") {
		t.Errorf("message = %q, want an E518", d.Error())
	}
	if len(cfg.Errors) != 1 {
		t.Errorf("the sink heard about %d errors, want 1", len(cfg.Errors))
	}
}

// TestSyntaxIsNotInertAnyMore. This test used to be the fence that said
// ":syntax" was accepted, logged and colouring nothing, because syntax
// highlighting was out of scope.
//
// That changed once the config actually being run had
// "syntax enable" uncommented and vim was therefore colouring files where
// pvim was not. internal/syntax reads vim's OWN syntax files -- there is no
// cgo here so tree-sitter was never available, and vim's files are written in
// vim regex, which internal/regex already translates -- so the decision that
// was being avoided turned out not to need making.
//
// What this now asserts is that the word reaches a Sink instead of a log. See
// TestFiletypeIsNotInertAnyMore, which ":filetype" went through first.
func TestSyntaxIsNotInertAnyMore(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("syntax on\nsyntax enable\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %v, want none", res.Errors)
	}
	if len(res.Ignored) != 0 {
		t.Fatalf("ignored = %v, want none: :syntax reaches Sink.Syntax now", res.Ignored)
	}
	if cfg.SyntaxArg != "enable" {
		t.Errorf("SyntaxArg = %q, want %q", cfg.SyntaxArg, "enable")
	}
	if cfg.FTDetect || cfg.FTPlugin || cfg.FTIndent {
		t.Errorf("a file with no \":filetype\" in it turned one of the switches on")
	}
}

// TestFiletypeIsNotInertAnyMore is the vimrc's line 2, which is the line this
// whole piece of work exists for.
//
// Every case is what /opt/homebrew/bin/vim 9.2.0321 does with the same
// argument, measured by running ":filetype off" and then the line
// and reading ":filetype" back.
func TestFiletypeIsNotInertAnyMore(t *testing.T) {
	for _, c := range []struct {
		line                   string
		detect, plugin, indent bool
		redetect               int
	}{
		{line: "filetype plugin indent on", detect: true, plugin: true, indent: true},
		{line: "filetype on", detect: true, plugin: true, indent: true},
		// Vim accepts both orders. Measured: ":filetype indent plugin on"
		// reports "detection:ON plugin:ON indent:ON", the same as the
		// documented spelling.
		{line: "filetype indent plugin on", detect: true, plugin: true, indent: true},
		{line: "filetype plugin on", detect: true, plugin: true},
		{line: "filetype indent on", detect: true, indent: true},
		{line: "filetype detect", detect: true, plugin: true, indent: true, redetect: 1},
		{line: "filetype plugin indent on\nfiletype plugin off", detect: true, indent: true},
		{line: "filetype plugin indent on\nfiletype off"},
		// A bare ":filetype" is a report and changes nothing.
		{line: "filetype plugin indent on\nfiletype", detect: true, plugin: true, indent: true},
	} {
		t.Run(strings.ReplaceAll(c.line, "\n", "; "), func(t *testing.T) {
			cfg := NewConfig()
			res := Run([]byte(c.line+"\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
			if len(res.Errors) != 0 {
				t.Fatalf("errors = %v, want none", res.Errors)
			}
			if len(res.Ignored) != 0 {
				t.Fatalf("ignored = %v; \":filetype\" is not inert any more", res.Ignored)
			}
			if cfg.FTDetect != c.detect || cfg.FTPlugin != c.plugin || cfg.FTIndent != c.indent {
				t.Errorf("detect/plugin/indent = %v/%v/%v, want %v/%v/%v",
					cfg.FTDetect, cfg.FTPlugin, cfg.FTIndent, c.detect, c.plugin, c.indent)
			}
			if cfg.FTRedetect != c.redetect {
				t.Errorf("redetect count = %d, want %d", cfg.FTRedetect, c.redetect)
			}
		})
	}
}

// TestFiletypeRefusesABadArgument. Measured ":filetype nonsense"
// prints "E475: Invalid argument: nonsense".
//
// The other two rows are stricter than the vim on this machine, deliberately.
// ":filetype plugin" and ":filetype on off" change nothing there and say
// nothing either, because vim reaches its own semsg with an empty argument;
// here each is an E475 naming the word that was wrong. A line that names a
// switch and never says on or off is a typo, and a typo that changes nothing
// in silence is the kind of thing somebody finds six months later wondering
// why their python files have no expandtab.
func TestFiletypeRefusesABadArgument(t *testing.T) {
	for _, line := range []string{"filetype nonsense", "filetype plugin", "filetype on off"} {
		t.Run(line, func(t *testing.T) {
			cfg := NewConfig()
			res := Run([]byte(line+"\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
			if len(res.Errors) != 1 {
				t.Fatalf("errors = %v, want one", res.Errors)
			}
			if !strings.Contains(res.Errors[0].Error(), "E475") {
				t.Errorf("message = %q, want an E475", res.Errors[0].Error())
			}
			if cfg.FTDetect {
				t.Error("a refused line turned detection on")
			}
		})
	}
}

// TestTheRefusedExpressionOperatorsSayWhy. "=~" and "!~" would need the
// vim-dialect translator, and internal/regex is not on this package's import
// list. Neither the vimrc nor the four colourschemes uses either operator, so
// this is a fence and not a loss -- but it is a fence that says its own name,
// which is the whole point of drawing a line at all.
func TestTheRefusedExpressionOperatorsSayWhy(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("if 'a' =~ 'b'\nendif\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one", res.Errors)
	}
	if !errors.Is(res.Errors[0], ErrNoRegex) {
		t.Errorf("error = %v, want ErrNoRegex", res.Errors[0])
	}
	if !strings.Contains(res.Errors[0].Error(), "regex engine") {
		t.Errorf("message = %q; it should name what is missing", res.Errors[0])
	}
}

// The compile-time checks that the concrete configuration and the concrete
// world still satisfy the two interfaces this package is written against.
var (
	_ Sink = (*Config)(nil)
	_ Env  = (*DefaultEnv)(nil)
)

// TestNormalTakesTheWholeLine is the third command as one a bar
// does not split, after :map and :autocmd. Nothing in this vimrc uses it and
// the first macro somebody puts in a config will.
func TestNormalTakesTheWholeLine(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("normal ciwfoo | bar\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if len(cfg.ExCmds) != 1 {
		t.Fatalf("%d ex commands, want 1: %v", len(cfg.ExCmds), cfg.ExCmds)
	}
	if got, want := cfg.ExCmds[0].Cmd.Args, "ciwfoo | bar"; got != want {
		t.Errorf("args = %q, want %q: :normal swallows the bar", got, want)
	}
}

// TestASkippedBranchStillFindsItsEndif. A command nobody recognises inside a
// branch that is not running is not an error, and it must not eat the ":endif"
// after it either: "if has('nvim') | lua ... | endif" is a shape a config that
// runs under both editors is written in.
func TestASkippedBranchStillFindsItsEndif(t *testing.T) {
	cfg := NewConfig()
	src := []byte("if 0 | lua require'x' | endif\nset sw=5\n")
	res := Run(src, "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if v, _ := cfg.Opts.Get("shiftwidth"); v.String() != "=5" {
		t.Errorf("&shiftwidth = %q, want =5", v.String())
	}
}

// TestLocalLeader. 'maplocalleader' is its own variable and <LocalLeader> its
// own notation. The vimrc sets neither, so this is here to keep the two from
// being collapsed into one the first time somebody does.
func TestLocalLeader(t *testing.T) {
	cfg := NewConfig()
	ld := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts})
	src := []byte("let mapleader = \",\"\nlet maplocalleader = \";\"\nnnoremap <leader>a x\nnnoremap <localleader>b y\n")
	res := ld.Run(src, "x.vim")
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if ld.Leader != "," || ld.LocalLeader != ";" {
		t.Fatalf("leader = %q, localleader = %q; want \",\" and \";\"", ld.Leader, ld.LocalLeader)
	}
	for _, tc := range []struct{ lhs, want string }{
		{"<leader>a", ",a"},
		{"<localleader>b", ";b"},
	} {
		m, ok := findMap(cfg, tc.lhs)
		if !ok {
			t.Errorf("%s is not mapped", tc.lhs)
			continue
		}
		if got := keyText(m); got != tc.want {
			t.Errorf("%s expanded to %q, want %q", tc.lhs, got, tc.want)
		}
	}
}

// TestAutocmdClearNarrows.
func TestAutocmdClearNarrows(t *testing.T) {
	cfg := NewConfig()
	src := []byte(
		"augroup A\n" +
			"au BufRead *.go set sw=1\n" +
			"au BufRead *.md set sw=2\n" +
			"au! BufRead *.go\n" +
			"augroup END\n")
	res := Run(src, "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if got := len(cfg.Match("BufRead", "x.go")); got != 0 {
		t.Errorf("*.go still has %d autocommands", got)
	}
	if got := len(cfg.Match("BufRead", "x.md")); got != 1 {
		t.Errorf("*.md has %d autocommands, want 1: the clear named a pattern", got)
	}
}

// TestSilentAndARangeReachTheExLayer. Two shapes this parser has no business
// understanding and every business handing on.
//
// Both arrived with cmd/pvim/filetype.go, which runs an autocommand's body
// through this loader rather than through internal/ex, because a body can be
// any of the three things: an option line, a mapping, or an ex command. The
// two vimrcs spell the strip-trailing-whitespace rule differently and each one
// needs a different half:
//
//	silent! %s/\s\+$//e the file that replaced the original
//	:%s/\s\+$//e the original, nested inside a FileType rule
//
// The first wanted a ":silent" row in the command table and the second wanted
// a parser that does not answer E492 to a line beginning with a range.
func TestSilentAndARangeReachTheExLayer(t *testing.T) {
	for _, line := range []string{
		`silent! %s/\s\+$//e`,
		`:%s/\s\+$//e`,
		`%s/\s\+$//e`,
		`1,3d`,
		`.!jq --sort-keys '.'`,
	} {
		t.Run(line, func(t *testing.T) {
			cfg := NewConfig()
			res := Run([]byte(line+"\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
			if len(res.Errors) != 0 {
				t.Fatalf("errors = %v, want none", res.Errors)
			}
			if len(cfg.ExCmds) != 1 {
				t.Fatalf("ex commands = %v, want one", cfg.ExCmds)
			}
			// The whole line goes on, leading colon and all, because
			// internal/ex parses the range itself and a parser that had
			// already eaten half of it would be a second range parser.
			if got := cfg.ExCmds[0].Line; got != line {
				t.Errorf("line handed on = %q, want %q", got, line)
			}
		})
	}
}

// TestALineThatIsNeitherIsStillE492. The range rule above widens what this
// parser accepts, and the dividing rule is what stops it widening too far: a
// line that is not a command and does not open a range is still an unknown
// command with its own name in the message.
func TestALineThatIsNeitherIsStillE492(t *testing.T) {
	for _, line := range []string{"NERDTreeToggle", "Plug 'x/y'", "&&"} {
		t.Run(line, func(t *testing.T) {
			cfg := NewConfig()
			res := Run([]byte(line+"\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
			if len(res.Errors) != 1 {
				t.Fatalf("errors = %v, want one", res.Errors)
			}
			if !strings.Contains(res.Errors[0].Error(), "E492") {
				t.Errorf("message = %q, want an E492", res.Errors[0].Error())
			}
		})
	}
}

// TestAStrayContinuationIsStillE492. A leading backslash is a continuation
// line whose statement is missing, and it has to stay an unknown command:
// vim's "\/" and "\?" address forms would have made it an ex command with a
// message about an address instead, which is why isRangeByte leaves them out.
func TestAStrayContinuationIsStillE492(t *testing.T) {
	cfg := NewConfig()
	res := Run([]byte("\\ 'oops'\n"), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	if len(res.Errors) != 1 {
		t.Fatalf("errors = %v, want one", res.Errors)
	}
	if !strings.Contains(res.Errors[0].Error(), "E492") {
		t.Errorf("message = %q, want an E492", res.Errors[0].Error())
	}
}
