package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/screen"
	"github.com/pkar/pvim/internal/vimrc"
)

// The gate clause that is about the real ~/.vimrc, measured end to end
// through the editor rather than through internal/vimrc's Config.
//
// internal/vimrc/load_test.go already reads the file into a Config and checks
// what it parsed. That is the parser's gate and it is not this one: a line can
// parse perfectly and reach no option, no command and no highlight, and the
// only way to tell is to load it into the thing that has all three. Everything
// here goes through editor.loadVimrc, which is the same call cmd/pvim makes on
// startup.
//
// Every expectation in this file was read off /opt/homebrew/bin/vim 9.2.0321,
// and the ones that can be re-read are re-read: vimAsks runs vim again inside
// the test, so a wrong guess fails here rather than six months from now in a
// file somebody was editing.

// theVim is the oracle. Missing it skips the halves of the gate that compare
// against it and leaves the halves that compare against the committed
// measurement.
const theVim = "/opt/homebrew/bin/vim"

// gateEditor loads the real vimrc into a fresh editor.
//
// gui is has('gui_running'): false is the terminal frontend and true is the
// window, and this vimrc runs different lines under each.
func gateEditor(t *testing.T, gui bool) *editor {
	t.Helper()
	if _, err := os.Stat(realVimrc); err != nil {
		t.Skipf("no vimrc at %s", realVimrc)
	}
	e := newTestEditor(t, "one\n")
	e.loadVimrc(realVimrc, gui)
	return e
}

// vimAsks runs a vim script under "--clean" with the real vimrc sourced first,
// and returns everything the script sent to a ":redir".
//
// "--clean" and then ":source", rather than "-u ~/.vimrc", because "-u file"
// tells vim not to read defaults.vim and pvim's built-in option defaults are
// defaults.vim's. The two disagree about 'nrformats' and nothing else in this
// file's list, measured.
//
// "set nomore" is the first line of every script for a reason worth writing
// down: without it a script that prints more than a screenful stops at vim's
// hit-enter prompt with the redirection half written, and the test reads a
// truncated answer as a real one.
//
// The ":for" over pack/*/start/* is what "--clean" took away and the editor
// has to have back. Vim adds every start package to 'runtimepath' during
// startup, which is how "colorscheme nofrils-dark" on line 5 finds a file that
// lives under ~/.vim/pack/pkar/start/nofrils/colors/. "--clean" skips packages
// entirely, so without this line vim answers E185 for the one colourscheme
// this vimrc asks for and the comparison is against a vim that is less set up
// than the one the user runs.
func vimAsks(t *testing.T, body string) string {
	t.Helper()
	if _, err := os.Stat(theVim); err != nil {
		t.Skipf("no vim at %s", theVim)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	script := filepath.Join(dir, "ask.vim")
	src := fmt.Sprintf("for s:d in glob('~/.vim/pack/*/start/*', 0, 1)\n  let &runtimepath = s:d .. ',' .. &runtimepath\nendfor\nsource %s\nset nomore\nredir! > %s\n%s\nredir END\nqa!\n", realVimrc, out, body)
	if err := os.WriteFile(script, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(theVim, "--clean", "-i", "NONE", "--not-a-term", "-S", script, os.DevNull)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v", theVim, err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("vim wrote no output: %v", err)
	}
	return string(got)
}

// nonBlank splits vim's redirected output into its non-empty lines. Vim opens
// a redirection with a blank line and puts one between some answers, and none
// of them carries information.
func nonBlank(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// TestTheVimrcSaysNothingOnTheMessageLine is the gate's first clause: zero
// E-codes.
//
// It is checked under both answers to has('gui_running') because they run
// different lines, and the count is reported rather than just asserted, so a
// failure names every line that errored instead of the first one.
func TestTheVimrcSaysNothingOnTheMessageLine(t *testing.T) {
	for _, gui := range []bool{false, true} {
		e := gateEditor(t, gui)
		msgs := e.ed.Messages()
		for _, m := range msgs {
			t.Errorf("gui_running=%v: %s", gui, m)
		}
		if len(msgs) != 0 {
			t.Errorf("gui_running=%v: %d messages, want 0", gui, len(msgs))
		}
	}

	// And vim says nothing either, which is what makes zero the right number
	// rather than a number pvim chose. ":messages" after startup is one line,
	// the maintainer banner vim always prints.
	for _, l := range nonBlank(vimAsks(t, "messages")) {
		if code := eCode(l); code != "" {
			t.Errorf("vim itself said %q loading this vimrc; %s is a real error in the file and not a pvim gap", l, code)
		}
	}
}

// eCode returns the "E123" an error message starts one of its words with, and
// "" for a line that carries none. Matching on a bare "E" would find the E in
// "Messages maintainer".
func eCode(line string) string {
	for _, word := range strings.Fields(line) {
		if len(word) < 3 || word[0] != 'E' {
			continue
		}
		digits := 0
		for digits+1 < len(word) && word[digits+1] >= '0' && word[digits+1] <= '9' {
			digits++
		}
		if digits > 0 && digits+1 < len(word) && word[digits+1] == ':' {
			return word[:digits+1]
		}
	}
	return ""
}

// TestTheVimrcsOptionsArePrintedTheWayVimPrintsThem is the gate's second
// clause, and it is deliberately about the printed text and not the stored
// value: ":set tabstop?" is " tabstop=2" with two leading spaces, a boolean
// that is on is " smartcase" with no value at all and one that is off is
// "nowrap" with no indent, and an editor whose numbers are all right and whose
// ":set" output is not is an editor whose ":set" output is wrong.
//
// testdata/vimrc_set.txt is vim's side, one row per option. When vim is on the
// machine the file is re-read from it, so the committed answer cannot go stale
// without the test saying so.
func TestTheVimrcsOptionsArePrintedTheWayVimPrintsThem(t *testing.T) {
	e := gateEditor(t, false)

	rows, err := readSetGolden(filepath.Join("testdata", "vimrc_set.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// The thirteen the gate names by hand, so that a golden that lost a row
	// still fails rather than passing with less to check.
	for _, name := range []string{
		"tabstop", "softtabstop", "shiftwidth", "shiftround", "smartcase",
		"ignorecase", "hlsearch", "wrap", "scrolloff", "sidescrolloff",
		"textwidth", "backspace", "clipboard", "completeopt", "cmdheight",
		"number", "confirm", "autochdir", "conceallevel", "shell",
		"directory", "undodir",
	} {
		if _, ok := rows[name]; !ok {
			t.Errorf("testdata/vimrc_set.txt has no row for '%s', which the gate names", name)
		}
	}

	for name, want := range rows {
		lines, err := e.opt.ApplyLine(name+"?", options.Both)
		if err != nil {
			t.Errorf(":set %s? is %v; vim prints %q", name, err, want)
			continue
		}
		if len(lines) != 1 {
			t.Errorf(":set %s? printed %d lines, want 1", name, len(lines))
			continue
		}
		if lines[0] != want {
			t.Errorf(":set %s? printed %q, vim prints %q", name, lines[0], want)
		}
	}

	// Now the same question to vim, which is what keeps the golden honest.
	var body strings.Builder
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		fmt.Fprintf(&body, "set %s?\n", name)
	}
	said := nonBlank(vimAsks(t, body.String()))
	if len(said) != len(names) {
		t.Fatalf("vim printed %d lines for %d options", len(said), len(names))
	}
	for i, name := range names {
		if said[i] != rows[name] {
			t.Errorf("testdata/vimrc_set.txt says %s is %q; vim now prints %q. Copy the new answer in and say in the commit what moved", name, rows[name], said[i])
		}
	}
}

// readSetGolden reads the "name<TAB>what vim printed" file.
func readSetGolden(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	rows := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, line, ok := strings.Cut(sc.Text(), "\t")
		if !ok {
			return nil, fmt.Errorf("%s: %q has no tab in it", path, sc.Text())
		}
		rows[name] = line
	}
	return rows, sc.Err()
}

// sortStrings is an insertion sort over a handful of option names. The package
// has no other reason to import "sort".
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestTheVimrcsMappings is the gate's third clause: the leader and the four
// mappings the file makes at the top level.
//
// The one that matters is the last. ":NERDTreeToggle" is a command pvim does
// not have and will never have -- internal/tree replaces the plugin -- and the
// rule is that a mapping's right-hand side is not looked at when the mapping
// is made. It has to load without a word on the
// message line and fail when the keys are pressed, which is exactly what vim
// does with a plugin that failed to install.
func TestTheVimrcsMappings(t *testing.T) {
	e := gateEditor(t, false)

	if got := e.leader(); got != "," {
		t.Errorf("mapleader is %q, want %q", got, ",")
	}

	for _, tc := range []struct{ lhs, modes, rhs string }{
		// A bare ":map" is normal, visual and operator-pending, which is why
		// "d{" under this vimrc means "delete to the previous tab".
		{"{", "nvo", "gT"},
		{"}", "nvo", "gt"},
		// ":map" has no comment syntax, so the trailing comment is part of the
		// right-hand side. Vim maps <Space> to all twenty-four characters.
		{"<Space>", "n", `za " Spacebar to unfold`},
		{"<leader>,", "n", ":NERDTreeToggle<CR>"},
	} {
		m, ok := e.mapping(tc.lhs)
		if !ok {
			t.Errorf("%s is not mapped", tc.lhs)
			continue
		}
		if m.Modes != tc.modes {
			t.Errorf("%s is mapped in %q, want %q", tc.lhs, m.Modes, tc.modes)
		}
		if m.RHS != tc.rhs {
			t.Errorf("%s maps to %q, want %q", tc.lhs, m.RHS, tc.rhs)
		}
	}

	// The leader was "," by line 58 and the mapping is on line 125, so the
	// left-hand side is two commas. A loader that expanded <leader> before the
	// file ran would have mapped a backslash.
	m, ok := e.mapping("<leader>,")
	if !ok {
		t.Fatal("<leader>, is not mapped")
	}
	var keys strings.Builder
	for _, k := range m.LHS {
		keys.WriteRune(k.Rune)
	}
	if got := keys.String(); got != ",," {
		t.Errorf("<leader>, is the keys %q, want %q", got, ",,")
	}

	// And the right-hand side fails, with E492 and the command's name, only
	// now that something ran it.
	err := e.ctx.RunLine("NERDTreeToggle")
	if err == nil {
		t.Fatal("running NERDTreeToggle succeeded; it is a plugin command pvim does not have")
	}
	if want := "E492: Not an editor command: NERDTreeToggle"; err.Error() != want {
		t.Errorf("running it says %q, want %q", err, want)
	}
}

// leader is what "let mapleader" left behind, which is the "," on line 58.
func (e *editor) leader() string {
	if v, ok := e.vars["g:mapleader"]; ok {
		return v.Val.Str
	}
	return ""
}

// mapping looks a mapping up by its left-hand side as the vimrc wrote it.
func (e *editor) mapping(lhs string) (vimrc.Map, bool) {
	for _, m := range e.maps {
		if m.LHSText == lhs {
			return m, true
		}
	}
	return vimrc.Map{}, false
}

// TestJsonPrettyFiltersThroughJq is the gate's fourth clause, and it runs the
// command rather than checking that it was defined.
//
// The whole chain has to be there for this to pass: the ":command!" with
// -range -nargs=0 -bar, the <line1> and <line2> substitution, the range
// arithmetic, the ":{range}!" filter, 'shell' being the homebrew bash the
// vimrc sets on line 129, and jq itself. Vim's answer to the same keys on the
// same input was read and is the "want" below, byte for byte.
func TestJsonPrettyFiltersThroughJq(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("no jq on this machine")
	}
	if _, err := os.Stat(realVimrc); err != nil {
		t.Skipf("no vimrc at %s", realVimrc)
	}
	e := newTestEditor(t, "{ \"b\": 2, \"a\": 1 }\nuntouched   \n")
	e.loadVimrc(realVimrc, false)

	// 'shell' is what the filter runs under, and it is the reason this test
	// is here and not in internal/ex: the vimrc sets it and nothing else does.
	if got := e.opt.G.Shell; got != "/opt/homebrew/bin/bash" {
		t.Fatalf("'shell' is %q; the vimrc sets /opt/homebrew/bin/bash and the filter runs under it", got)
	}

	if err := e.ctx.RunLine("1,1JsonPretty"); err != nil {
		t.Fatalf(":1,1JsonPretty: %v", err)
	}

	want := "{\n  \"a\": 1,\n  \"b\": 2\n}\nuntouched   \n"
	if got := string(e.buf.Bytes()); got != want {
		t.Errorf("after :1,1JsonPretty the buffer is\n%q\nwant\n%q", got, want)
	}

	// -nargs=0 means an argument is a refusal and not something passed on.
	if err := e.ctx.RunLine("1,1JsonPretty extra"); err == nil {
		t.Error(":JsonPretty took an argument; it is defined -nargs=0")
	}
}

// TestTheTrailingWhitespaceAutocmdLeaks is the gate's fifth clause, and the
// leak is the assertion.
//
// Line 183 of the vimrc is
//
//	autocmd Filetype python,java,javascript,go autocmd BufWritePre * :%s/\s\+$//e
//
// which registers a GLOBAL BufWritePre with the pattern "*" the first time any
// of those four filetypes is entered, and then strips trailing white space out
// of every buffer written for the rest of the session, markdown included. That
// is a bug in the vimrc and vim executes it faithfully, so pvim has to as well.
//
// Measured "vim --clean" with the vimrc sourced, two markdown files
// each holding "title \nbody \n":
//
//	edit n3.md, write -> title \nbody \n unchanged
//	edit a.go, edit n4.md, write -> title\nbody\n stripped
//
// The second one is the leak. It needs filetype detection on to fire at all,
// which is defaults.vim's doing and not the vimrc's -- the vimrc's own
// "filetype plugin indent on" is commented out -- so under "vim -u ~/.vimrc",
// where defaults.vim never runs, neither file is stripped and the leak is
// invisible.
func TestTheTrailingWhitespaceAutocmdLeaks(t *testing.T) {
	if _, err := os.Stat(realVimrc); err != nil {
		t.Skipf("no vimrc at %s", realVimrc)
	}

	// The autocmd table has to be asked with vim's matching rules, which is
	// internal/vimrc's Config. The editor keeps the same statements in a flat
	// list, so this half of the gate loads the file a second time through the
	// parser's own sink.
	src, err := os.ReadFile(realVimrc)
	if err != nil {
		t.Fatal(err)
	}
	cfg := vimrc.NewConfig()
	ld := vimrc.NewLoader(cfg, &vimrc.DefaultEnv{Opts: cfg.Opts})
	for _, d := range ld.Run(src, realVimrc).Errors {
		t.Fatalf("%v", d)
	}

	// Before any Go file, nothing strips anything.
	if got := cfg.Match("BufWritePre", "notes.md"); len(got) != 0 {
		t.Fatalf("BufWritePre already has %v at startup", got)
	}

	// Entering a Go file runs the FileType bodies, one of which is itself an
	// ":autocmd".
	for _, a := range cfg.Match("FileType", "go") {
		for _, d := range ld.Line(a.Cmd, a.Pos).Errors {
			t.Fatalf("running %q: %v", a.Cmd, d)
		}
	}

	strip := cfg.Match("BufWritePre", "main.go")
	if len(strip) != 1 {
		t.Fatalf("a .go file gets %d BufWritePre autocommands, want 1", len(strip))
	}
	if want := `:%s/\s\+$//e`; strip[0].Cmd != want {
		t.Fatalf("the command is %q, want %q", strip[0].Cmd, want)
	}
	if got := strip[0].Patterns; len(got) != 1 || got[0] != "*" {
		t.Errorf("the pattern is %v, want [*], which is the whole leak", got)
	}
	if strip[0].Group != "" {
		t.Errorf("it landed in augroup %q; vim registers it outside every group", strip[0].Group)
	}

	// THE LEAK. Markdown now gets it, and a file with no extension too.
	for _, name := range []string{"README.md", "Makefile", "notes.txt"} {
		if got := len(cfg.Match("BufWritePre", name)); got != 1 {
			t.Errorf("%s gets %d BufWritePre autocommands, want 1: the leak is the behaviour, and losing it is a difference from the oracle", name, got)
		}
	}

	// And the command has teeth: run it over a markdown buffer through the
	// editor and the white space is gone, which is the byte-for-byte answer
	// vim gave n4.md above.
	e := newTestEditor(t, "title   \nbody  \n")
	e.loadVimrc(realVimrc, false)
	if err := e.ctx.RunLine(strings.TrimPrefix(strip[0].Cmd, ":")); err != nil {
		t.Fatalf("running the stripper: %v", err)
	}
	if got, want := string(e.buf.Bytes()), "title\nbody\n"; got != want {
		t.Errorf("after the stripper the markdown buffer is %q, want %q", got, want)
	}
}

// TestTheIgnoredVariables is the gate's sixth clause.
//
// The rule's phrasing is "the eleven g:go_* lets, the four NERDTree* lets,
// g:terraform_*, g:ctrlp_* and g:vim_ai_* are in the ignored-with-log list",
// and the file has moved under it: nine of the eleven g:go_* lines are
// commented out, and internal/vimrc.ReadVars says the NERDTree, ctrlp,
// terraform and g:go_bin_path names ARE read, because internal/tree,
// internal/finder and internal/fmt read them. Ignoring a variable one of
// those layers needs is the expensive half of the dividing rule, so this test
// names both lists rather than counting either.
func TestTheIgnoredVariables(t *testing.T) {
	e := gateEditor(t, false)

	var ignored []string
	for _, ig := range e.ignored {
		// Matched against the path the gate loaded, not a ".vimrc" suffix: the
		// fixture is called "vimrc" with no dot, and a suffix test silently
		// filtered every variable out and reported an empty list as a mismatch.
		if strings.HasPrefix(ig.Name, "g:") && ig.Pos.File == realVimrc {
			ignored = append(ignored, ig.Name)
		}
	}
	want := []string{
		"g:go_metalinter_enabled",
		"g:vim_ai_token_file_path",
		"g:vim_ai_roles_config_file",
		"g:vim_ai_debug",
		"g:vim_ai_debug_log_file",
	}
	if strings.Join(ignored, " ") != strings.Join(want, " ") {
		t.Errorf("the vimrc's ignored variables are %v, want %v", ignored, want)
	}
	for _, name := range want {
		if _, ok := e.vars[name]; ok {
			t.Errorf("%s reached the editor; it is supposed to be inert", name)
		}
	}

	// The ones those layers read, with the values vim gives them. The dict
	// is the row that matters: it is three physical lines joined by backslash
	// continuations, and internal/finder reads both of its patterns.
	for name, want := range map[string]string{
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
		v, ok := e.vars[name]
		if !ok {
			t.Errorf("%s never reached the editor", name)
			continue
		}
		if got := v.Val.String(); got != want {
			t.Errorf("%s is %s, want %s", name, got, want)
		}
	}

	// g:go_bin_path went through expand(), so a caller can hand it to
	// exec.LookPath without expanding a tilde of its own.
	v, ok := e.vars["g:go_bin_path"]
	if !ok {
		t.Fatal("g:go_bin_path never reached the editor")
	}
	if strings.HasPrefix(v.Val.Str, "~") || !strings.HasSuffix(v.Val.Str, "/.vimgo") {
		t.Errorf("g:go_bin_path is %q, want an absolute path ending in /.vimgo", v.Val.Str)
	}
}

// TestTheColorschemeLoads is the gate's seventh clause.
//
// The vimrc's line 5 is "colorscheme nofrils-dark" and the file it wants is
// under ~/.vim/pack/pkar/start/nofrils/colors/, which is a package directory
// and not a plain 'runtimepath' entry. Recording the name and never opening
// the file, which is what this editor did before this pass, leaves every
// highlight at the built-in default and the screen the wrong colour with
// nothing on the message line to say so.
func TestTheColorschemeLoads(t *testing.T) {
	e := gateEditor(t, false)

	if e.colorscheme != "nofrils-dark" {
		t.Fatalf("colorscheme is %q, want nofrils-dark", e.colorscheme)
	}
	if e.hl.Len() < 40 {
		t.Fatalf("the highlight table holds %d groups; nofrils-dark defines about ninety, so the file was never read", e.hl.Len())
	}

	// The groups, and what vim prints for each. "fg", "bg" and an omitted
	// colour all resolve to Normal's, because screen.Highlight is what a cell
	// looks like with nothing left to inherit.
	for _, tc := range []struct{ group, vimSaid, fg, bg string }{
		{"Normal", "guifg=#eeeeee guibg=#262626", "#eeeeee", "#262626"},
		{"Comment", "guifg=#6C6C6C", "#6C6C6C", "#262626"},
		{"StatusLine", "guifg=black guibg=fg", "#000000", "#eeeeee"},
		{"LineNr", "guifg=#808080 guibg=bg", "#808080", "#262626"},
		{"Search", "guifg=black guibg=#00CDCD", "#000000", "#00CDCD"},
	} {
		got := e.hl.Look(e.hl.ID(tc.group))
		if fg, bg := hex(got.FG), hex(got.BG); fg != strings.ToLower(tc.fg) || bg != strings.ToLower(tc.bg) {
			t.Errorf("%s is guifg=%s guibg=%s, want guifg=%s guibg=%s (vim prints %q)",
				tc.group, fg, bg, strings.ToLower(tc.fg), strings.ToLower(tc.bg), tc.vimSaid)
		}
	}

	// 'background' is set by the scheme as well as by line 6 of the vimrc, and
	// a scheme that never loaded would leave line 6 as the only one that did.
	if v, err := e.opt.Get("background"); err != nil || v.Str != "dark" {
		t.Errorf("'background' is %v (%v), want dark", v.Str, err)
	}

	// And what vim prints for Normal, which is the half of this that
	// cannot go stale.
	said := nonBlank(vimAsks(t, "hi Normal"))
	if len(said) != 1 {
		t.Fatalf("vim printed %d lines for :hi Normal", len(said))
	}
	for _, want := range []string{"guifg=#eeeeee", "guibg=#262626"} {
		if !strings.Contains(said[0], want) {
			t.Errorf("vim prints %q for :hi Normal, which no longer has %s in it", said[0], want)
		}
	}
}

// hex spells a colour the way a colourscheme file writes one.
func hex(c screen.RGB) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// TestTheGuiRunningBranchesBothWays is the gate's eighth clause: this vimrc
// runs different lines in the terminal and in the window, and both halves have
// to be right because the two frontends are peers.
//
// In the terminal, has('gui_running') is false, so lines 18 to 27 run:
// 'notimeout', 'ttimeout', ttimeoutlen=10 and the FastEscape augroup, which is
// the dance that makes a bare Escape not wait a second in terminal vim. So do
// the four clipboard mappings on lines 40 to 45, which are behind
// has('clipboard') && !has('gui_running').
//
// In the window, lines 8 to 11 run instead and set guifont=Monaco:h13. Line 14,
// "set guioptions-=e", is outside every if and runs in both, which is what
// makes the tabline text in the Grid rather than an AppKit control.
func TestTheGuiRunningBranchesBothWays(t *testing.T) {
	term := gateEditor(t, false)
	win := gateEditor(t, true)

	// The terminal half.
	for _, tc := range []struct{ name, want string }{
		{"timeout", "notimeout"},
		{"ttimeout", "  ttimeout"},
		{"ttimeoutlen", "  ttimeoutlen=10"},
	} {
		lines, err := term.opt.ApplyLine(tc.name+"?", options.Both)
		if err != nil || len(lines) != 1 || lines[0] != tc.want {
			t.Errorf("terminal: :set %s? is %v %v, want %q", tc.name, lines, err, tc.want)
		}
	}
	for _, lhs := range []string{"<C-c>", "<C-x>", "<C-v>"} {
		if _, ok := term.mapping(lhs); !ok {
			t.Errorf("terminal: %s is not mapped; the vimrc maps it behind !has('gui_running')", lhs)
		}
	}
	if n := countAutocmds(term, "InsertEnter"); n != 1 {
		t.Errorf("terminal: %d InsertEnter autocommands, want 1 (FastEscape)", n)
	}
	if v, err := term.opt.Get("guifont"); err != nil || v.Str != "" {
		t.Errorf("terminal: 'guifont' is %q (%v), want empty: line 10 is behind has('gui_running')", v.Str, err)
	}

	// The window half.
	if v, err := win.opt.Get("guifont"); err != nil || v.Str != "Monaco:h13" {
		t.Errorf("window: 'guifont' is %q (%v), want Monaco:h13", v.Str, err)
	}
	for _, lhs := range []string{"<C-c>", "<C-x>", "<C-v>"} {
		if _, ok := win.mapping(lhs); ok {
			t.Errorf("window: %s is mapped; the vimrc puts it behind !has('gui_running')", lhs)
		}
	}
	if n := countAutocmds(win, "InsertEnter"); n != 0 {
		t.Errorf("window: %d InsertEnter autocommands, want 0", n)
	}
	// 'timeout' is vim's default, on, because line 19 did not run.
	if v, err := win.opt.Get("timeout"); err != nil || !v.Bool {
		t.Errorf("window: 'timeout' is %v (%v), want on: line 19 is behind !has('gui_running')", v.Bool, err)
	}

	// Line 14 is outside every if, so "e" is gone from 'guioptions' in both.
	for _, tc := range []struct {
		name string
		e    *editor
	}{{"terminal", term}, {"window", win}} {
		v, err := tc.e.opt.Get("guioptions")
		if err != nil {
			t.Errorf("%s: 'guioptions' is %v", tc.name, err)
			continue
		}
		if strings.Contains(v.Str, "e") {
			t.Errorf("%s: 'guioptions' is %q; line 14 takes the e out", tc.name, v.Str)
		}
	}
}

// countAutocmds counts the autocommands the vimrc registered for one event.
func countAutocmds(e *editor, event string) int {
	n := 0
	for _, a := range e.autocmds {
		for _, ev := range a.Events {
			if strings.EqualFold(ev, event) {
				n++
			}
		}
	}
	return n
}
