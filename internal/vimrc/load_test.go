package vimrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two files this package exists to read. They live outside the repository,
// so testdata holds a copy and TestTheCopiesMatchTheRealFiles fails when the
// two drift: a gate that passes against a stale copy of the config is a gate
// that has stopped measuring anything.
var (
	// realVimrc is the config this editor exists to run, and it is the copy in
	// testdata rather than ~/.vimrc on purpose. The gate has to grade the same
	// bytes every run, and ~/.vimrc is a symlink a user can and did repoint:
	// it now points at a different 149-line file, and the 243-line original it
	// used to point at was deleted along with the directory it lived in.
	// The copy here is the only surviving one. sourceVimrc names where it came
	// from so TestTheCopiesMatchTheRealFiles still checks for drift on a
	// machine that has the original; everywhere else the original is gone and
	// the test skips.
	//
	// Nothing about pvim's RUNTIME behaviour is pinned by this: cmd/pvim reads
	// ~/.vimrc, whatever it points at.
	realVimrc   = filepath.Join("testdata", "vimrc")
	sourceVimrc = "~/.vimrc"
	realColors  = filepath.Join(os.Getenv("HOME"), ".vim", "pack", "pkar", "start", "nofrils", "colors")
)

// load runs the vimrc copy in testdata against a fresh Config.
//
// gui is has('gui_running'). It is a parameter because the two answers run
// different halves of the file, and the terminal answer -- false -- is the one
// that runs more of it: the FastEscape autocommands and the <C-c>/<C-v>
// clipboard mappings are behind !has('gui_running').
func load(t *testing.T, gui bool) (*Config, *Loader, *Result) {
	t.Helper()

	src, err := os.ReadFile(filepath.Join("testdata", "vimrc"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig()
	env := &DefaultEnv{
		GUI:  gui,
		Opts: cfg.Opts,
		Vars: map[string]string{"MYVIMRC": realVimrc, "MYGVIMRC": "/nonexistent/.gvimrc"},
	}
	ld := NewLoader(cfg, env)
	return cfg, ld, ld.Run(src, "vimrc")
}

// TestTheCopiesMatchTheRealFiles keeps testdata honest.
func TestTheCopiesMatchTheRealFiles(t *testing.T) {
	pairs := [][2]string{{sourceVimrc, filepath.Join("testdata", "vimrc")}}
	for _, name := range []string{"nofrils-dark.vim", "nofrils-light.vim", "nofrils-sepia.vim", "nofrils-acme.vim"} {
		pairs = append(pairs, [2]string{filepath.Join(realColors, name), filepath.Join("testdata", "colors", name)})
	}

	for _, pair := range pairs {
		real, err := os.ReadFile(pair[0])
		if err != nil {
			// On any machine but this one the dotfiles are not there, and the
			// copy in testdata is the whole truth.
			t.Skipf("%s is not on this machine: %v", pair[0], err)
		}
		copied, err := os.ReadFile(pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if string(real) != string(copied) {
			t.Errorf("%s and %s have drifted; copy the real file over the fixture and re-read the gate", pair[0], pair[1])
		}
	}
}

// TestTheVimrcLoadsWithNoErrors is the first clause of the gate: the
// real ~/.vimrc, both halves of has('gui_running'), with nothing on the
// message line.
func TestTheVimrcLoadsWithNoErrors(t *testing.T) {
	for _, gui := range []bool{false, true} {
		_, _, res := load(t, gui)
		for _, d := range res.Errors {
			t.Errorf("gui=%v: %v", gui, d)
		}
	}
}

// TestTheVimrcsOptions is the second clause: the option values the gate names,
// plus the ones whose parsing is worth a row of its own.
func TestTheVimrcsOptions(t *testing.T) {
	cfg, _, _ := load(t, false)

	for name, want := range map[string]string{
		// The two the gate names.
		"tabstop":    "=2",
		"shiftwidth": "=4",
		// The rest of the file's own settings, one per shape: a number, a
		// boolean, a negated boolean, a comma list, a path with a "//" on the
		// end, and a termcap option emptied.
		"softtabstop":   "=2",
		"scrolloff":     "=3",
		"sidescrolloff": "=5",
		"conceallevel":  "=2",
		"cmdheight":     "=1",
		"textwidth":     "=0",
		"linespace":     "=0",
		"ignorecase":    "",
		"smartcase":     "",
		"hlsearch":      "",
		"incsearch":     "",
		"showmatch":     "",
		"shiftround":    "",
		"confirm":       "",
		"undofile":      "",
		"autochdir":     "",
		"breakindent":   "",
		"linebreak":     "",
		"infercase":     "",
		"wrap":          "no",
		"number":        "no",
		"backup":        "no",
		"writebackup":   "no",
		"errorbells":    "no",
		"backspace":     "=indent,eol,start",
		"completeopt":   "=menu,menuone,noselect,noinsert",
		"complete":      "=.,w,b,u,U,i,t",
		"clipboard":     "=unnamed,unnamedplus,autoselect",
		"diffopt":       "=vertical,filler,iwhite",
		"shell":         "=/opt/homebrew/bin/bash",
		"directory":     "=/tmp//",
		"undodir":       "=~/.cache/vim",
		"omnifunc":      "=syntaxcomplete#Complete",
		"encoding":      "=utf-8",
		"concealcursor": "=niv",
	} {
		v, err := cfg.Opts.Get(name)
		if err != nil {
			t.Errorf("&%s: %v", name, err)
			continue
		}
		if got := v.String(); got != want {
			t.Errorf("&%s = %q, want %q", name, got, want)
		}
	}

	// 'compatible' and 't_vb' are on line 1 and line 76 and internal/options
	// takes both and keeps no value for either, so there is nothing to read
	// back. What line 76 has to get right is that it is three options and a
	// comment and not four options, and a parser that strips the comment
	// wrong fails on "Disable" long before it gets to t_vb; that is
	// TestTheVimrcLoadsWithNoErrors's business and TestStripComment's.
}

// TestTheVimrcsLeaderAndMaps is the third and fourth clauses: mapleader is ","
// and "{" is mapped to gT.
func TestTheVimrcsLeaderAndMaps(t *testing.T) {
	cfg, ld, _ := load(t, false)

	if got := cfg.Leader(); got != "," {
		t.Errorf("mapleader = %q, want %q", got, ",")
	}
	if got := ld.Leader; got != "," {
		t.Errorf("the loader's leader = %q, want %q", got, ",")
	}

	for _, tc := range []struct {
		lhs, modes, rhs string
		noremap         bool
	}{
		// "map { gT" is a bare :map, so it is normal, visual AND
		// operator-pending, which is why "d{" means "delete to the previous
		// tab" and beeps.
		{lhs: "{", modes: "nvo", rhs: "gT"},
		{lhs: "}", modes: "nvo", rhs: "gt"},
		// The <Space> mapping keeps its trailing comment, because :map has no
		// comments: vim maps it to all twenty-four characters. Measured
		// with :execute('nmap <Space>').
		{lhs: "<Space>", modes: "n", rhs: `za " Spacebar to unfold`, noremap: true},
		// <leader>, is "," because mapleader was set on line 58 and this is
		// line 125. A loader that expanded <leader> at the end of the file
		// would get the same answer; one that expanded it at the start would
		// map "\,".
		{lhs: "<leader>,", modes: "n", rhs: ":NERDTreeToggle<CR>", noremap: true},
		// Line 160, the only <buffer> mapping in the file, inside an autocmd
		// body and so not registered until a Go file is entered.
	} {
		m, ok := findMap(cfg, tc.lhs)
		if !ok {
			t.Errorf("%q is not mapped", tc.lhs)
			continue
		}
		if m.Modes != tc.modes {
			t.Errorf("%q is mapped in %q, want %q", tc.lhs, m.Modes, tc.modes)
		}
		if m.RHS != tc.rhs {
			t.Errorf("%q maps to %q, want %q", tc.lhs, m.RHS, tc.rhs)
		}
		if m.NoRemap != tc.noremap {
			t.Errorf("%q noremap = %v, want %v", tc.lhs, m.NoRemap, tc.noremap)
		}
	}

	// <leader>, is "," once the leader is in: the keys, not the text.
	m, _ := findMap(cfg, "<leader>,")
	if got := keyText(m); got != ",," {
		t.Errorf("<leader>, expanded to %q, want %q", got, ",,")
	}
}

// TestTheClipboardMapsFollowTheFrontend is the has('gui_running') split, which
// is the one branch in this file whose answer differs between the two
// frontends. In the terminal the four clipboard mappings and the two
// FastEscape autocommands exist; in the window they do not.
func TestTheClipboardMapsFollowTheFrontend(t *testing.T) {
	term, _, _ := load(t, false)
	win, _, _ := load(t, true)

	for _, lhs := range []string{"<C-c>", "<C-x>", "<C-v>"} {
		if _, ok := findMap(term, lhs); !ok {
			t.Errorf("terminal: %q is not mapped; the vimrc maps it behind !has('gui_running')", lhs)
		}
		if _, ok := findMap(win, lhs); ok {
			t.Errorf("window: %q is mapped; the vimrc puts it behind !has('gui_running')", lhs)
		}
	}

	if got := len(term.Match("InsertEnter", "x.go")); got != 1 {
		t.Errorf("terminal: %d InsertEnter autocommands, want 1 (FastEscape)", got)
	}
	if got := len(win.Match("InsertEnter", "x.go")); got != 0 {
		t.Errorf("window: %d InsertEnter autocommands, want 0", got)
	}

	// The window half sets 'guifont' and the terminal half does not.
	if v, err := win.Opts.Get("guifont"); err != nil || v.String() != "=Monaco:h13" {
		t.Errorf("window: &guifont = %v, %v; want =Monaco:h13", v.String(), err)
	}
	if v, err := term.Opts.Get("guifont"); err != nil || v.String() != "=" {
		t.Errorf("terminal: &guifont = %v, %v; want empty", v.String(), err)
	}
}

// TestJsonPrettyExists is the fifth clause of the gate.
func TestJsonPrettyExists(t *testing.T) {
	cfg, _, _ := load(t, false)

	c, ok := cfg.Commands["JsonPretty"]
	if !ok {
		t.Fatalf("JsonPretty is not defined; the commands are %v", names(cfg.Commands))
	}
	if !c.Bang {
		t.Error("JsonPretty was defined without the bang; :command without one is E174 on a reload")
	}
	if want := []string{"-range", "-nargs=0", "-bar"}; !equal(c.Attrs, want) {
		t.Errorf("attrs = %v, want %v", c.Attrs, want)
	}
	if want := `<line1>,<line2>!jq --sort-keys '.'`; c.Repl != want {
		t.Errorf("replacement = %q, want %q", c.Repl, want)
	}
}

// TestTheVimrcsVariables is the last clause of the gate: which lets were
// applied and which were logged and dropped.
//
// The rule used to be phrased as "the four NERDTree* and eleven g:go_* lets
// are in the ignored-with-log list and nowhere else", and the file no longer
// matches it: nine of the eleven g:go_* lines are commented out, and ReadVars
// says the NERDTree names and g:go_bin_path are read, because the tree and
// gopls read them. What is actually left over is five lines, and this test
// names all five rather than counting them.
func TestTheVimrcsVariables(t *testing.T) {
	cfg, _, res := load(t, false)

	want := []string{
		"g:go_metalinter_enabled",
		"g:vim_ai_token_file_path",
		"g:vim_ai_roles_config_file",
		"g:vim_ai_debug",
		"g:vim_ai_debug_log_file",
	}
	if got := res.IgnoredNames(); !equal(got, want) {
		t.Errorf("ignored-with-log list is %v, want %v", got, want)
	}
	for _, name := range want {
		if _, ok := cfg.Vars[name]; ok {
			t.Errorf("%s reached the config; it is supposed to be inert", name)
		}
	}

	// The variables pvim does read, with the values vim gives them. Measured
	// by sourcing the vimrc and echoing string() of each.
	for name, want := range map[string]string{
		"g:mapleader":                  ",",
		"g:terraform_align":            "1",
		"g:terraform_fmt_on_save":      "1",
		"g:ctrlp_max_height":           "40",
		"g:ctrlp_match_window":         "results:40",
		"g:NERDTreeShowHidden":         "1",
		"g:NERDTreeWinSize":            "80",
		"g:go_code_completion_enabled": "1",
		"g:NERDTreeIgnore":             `['\~$', '\.pyc$', '\*NTUSER*', '\*ntuser*', '\NTUSER.DAT', '\ntuser.ini']`,
		"g:ctrlp_custom_ignore":        `{'dir': '\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$', 'file': '\.so$\|\.dat$|\.DS_Store|\.pyc$'}`,
	} {
		v, ok := cfg.Vars[name]
		if !ok {
			t.Errorf("%s was not applied", name)
			continue
		}
		if got := v.String(); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}

	// g:go_bin_path goes through expand(), so it is an absolute path and not
	// a tilde. gopls is looked for there before $PATH.
	v, ok := cfg.Vars["g:go_bin_path"]
	if !ok {
		t.Fatal("g:go_bin_path was not applied")
	}
	if strings.HasPrefix(v.Str, "~") || !strings.HasSuffix(v.Str, "/.vimgo") {
		t.Errorf("g:go_bin_path = %q, want an absolute path ending in /.vimgo", v.Str)
	}
}

// TestTheColorschemeIsNamed. The vimrc asks for nofrils-dark on line 5, and
// finding the file is the caller's job: this package records the name and
// never touches the disk.
func TestTheColorschemeIsNamed(t *testing.T) {
	cfg, _, _ := load(t, false)
	if cfg.Scheme != "nofrils-dark" {
		t.Errorf("colorscheme = %q, want nofrils-dark", cfg.Scheme)
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("the loader sourced %v; it must never open a file", cfg.Sources)
	}
}

// findMap looks a mapping up by the left-hand side as written.
func findMap(c *Config, lhs string) (Map, bool) {
	for _, m := range c.Maps {
		if m.LHSText == lhs {
			return m, true
		}
	}
	return Map{}, false
}

// keyText renders a mapping's parsed left-hand side back to text, which is how
// a <leader> expansion is checked.
func keyText(m Map) string {
	var b strings.Builder
	for _, k := range m.LHS {
		b.WriteRune(k.Rune)
	}
	return b.String()
}

func names(m map[string]Command) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDefaultEnvAnswersTheEditorsHas.
//
// This env is what every gate clause in this package grades the vimrc
// through, so an answer it gets wrong is a gate marking work nobody runs.
// cmd/pvim builds its own env from the six -- gui_running, mouse,
// clipboard, persistent_undo, autocmd, conceal -- and this one had eight.
//
// Measured against /opt/homebrew/bin/vim 9.2.0321: has('unnamed')
// is 0, and it was true here, which is a guess where vim had an answer.
// has('multi_byte') is 1 and is false in both pvim envs on purpose: pvim is
// utf-8 and latin1 only, so a branch guarded on it has nothing to switch on.
// Line 34 of ~/.vimrc is a commented-out `if has('unnamed')`; uncommenting it
// was all it took to make this env and the editor take different branches.
func TestDefaultEnvAnswersTheEditorsHas(t *testing.T) {
	env := &DefaultEnv{}
	for _, f := range []string{"mouse", "clipboard", "persistent_undo", "autocmd", "conceal"} {
		if !env.Has(f) {
			t.Errorf("has(%q) is false, want true", f)
		}
	}
	for _, f := range []string{"unnamed", "multi_byte", "nvim", "python3", "terminal", "gui_running"} {
		if env.Has(f) {
			t.Errorf("has(%q) is true, want false: it is false in cmd/pvim's env", f)
		}
	}
	if !(&DefaultEnv{GUI: true}).Has("gui_running") {
		t.Error("has('gui_running') is false with GUI set")
	}
}
