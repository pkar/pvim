package vimrc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
)

// The second gate: the vimrc ~/.vimrc points at TODAY.
//
// This file does not replace the one in load_test.go and must not. That gate
// reads testdata/vimrc, the 243-line file this package was surveyed against, and it
// is the only surviving copy: ~/.vimrc was repointed and the
// directory the original lived in was deleted. Moving that gate onto a new file
// would throw away every measurement in it. So there are two, and the parser
// has to pass both.
//
// What the live file uses that the preserved one does not, grepped from both
// rather than remembered:
//
//	executable fnamemodify resolve isdirectory mkdir fnameescape
//	:execute with string concatenation, :packloadall, and "s:" variables
//
// which is why this file exists at all: the live vimrc used to stop at line 5
// with E117 and produce twelve errors, and every one of them was on this
// list.

// liveVimrc is the committed copy and liveSource is where it came from.
//
// The copy is what the gate runs, for the same reason the preserved one is a
// copy: ~/.vimrc is a symlink its owner repoints, and a gate that reads
// whatever it points at grades a different file every week.
// TestTheLiveCopyMatchesTheRealFile is what keeps the copy from going stale.
var (
	liveVimrc  = filepath.Join("testdata", "vimrc.live")
	liveSource = "~/.vimrc"
)

// theVim is the oracle. Missing it skips the halves that compare against it.
const theVim = "/opt/homebrew/bin/vim"

// liveHome copies the live vimrc into a directory of its own and returns the
// path of the copy.
//
// A temporary directory and not testdata, because this file BUILDS PATHS OUT OF
// ITS OWN LOCATION and then creates them:
//
//	let s:vim_home = fnamemodify(resolve(expand('<sfile>:p')), ':h')
//	...
//	if !isdirectory(s:vim_state) | call mkdir(s:vim_state, 'p', 0700) | endif
//
// so loading it from testdata would put a state/undo, a state/swap and a
// viminfo beside the fixture on every run. It also makes the packages the gate
// needs: four empty pack/*/start directories, which is what ":packloadall"
// has to find.
func liveHome(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(liveVimrc)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "vimrc")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nerdtree", "nofrils", "vim-go", "vim-terraform"} {
		if err := os.MkdirAll(filepath.Join(dir, "pack", "pkar", "start", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// One colours file inside one package, because line 9 is
	// "colorscheme nofrils-dark" and finding it is the whole point of the
	// three lines above it.
	colors := filepath.Join(dir, "pack", "pkar", "start", "nofrils", "colors")
	if err := os.MkdirAll(colors, 0o755); err != nil {
		t.Fatal(err)
	}
	scheme, err := os.ReadFile(filepath.Join("testdata", "colors", "nofrils-dark.vim"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(colors, "nofrils-dark.vim"), scheme, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// loadLive runs the live vimrc from a directory of its own.
func loadLive(t *testing.T, gui bool) (*Config, string, *Result) {
	t.Helper()
	path := liveHome(t)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig()
	env := &DefaultEnv{
		GUI:  gui,
		Opts: cfg.Opts,
		Vars: map[string]string{"MYVIMRC": path, "MYGVIMRC": "/nonexistent/.gvimrc"},
	}
	return cfg, path, NewLoader(cfg, env).Run(src, path)
}

// TestTheLiveCopyMatchesTheRealFile keeps the fixture honest, the way
// TestTheCopiesMatchTheRealFiles does for the preserved one.
func TestTheLiveCopyMatchesTheRealFile(t *testing.T) {
	real, err := os.ReadFile(liveSource)
	if err != nil {
		t.Skipf("%s is not on this machine: %v", liveSource, err)
	}
	copied, err := os.ReadFile(liveVimrc)
	if err != nil {
		t.Fatal(err)
	}
	if string(real) != string(copied) {
		t.Errorf("%s and %s have drifted; copy the real file over the fixture and re-read this gate", liveSource, liveVimrc)
	}
}

// TestTheLiveVimrcLoadsWithNoErrors is the gate: zero E-codes, both answers to
// has('gui_running').
//
// The count is reported and every message printed, so that a failure names each
// line rather than the first.
func TestTheLiveVimrcLoadsWithNoErrors(t *testing.T) {
	for _, gui := range []bool{false, true} {
		_, _, res := loadLive(t, gui)
		for _, d := range res.Errors {
			t.Errorf("gui=%v: %v", gui, d)
		}
		if len(res.Errors) != 0 {
			t.Errorf("gui=%v: %d errors, want 0", gui, len(res.Errors))
		}
	}
}

// TestTheLiveVimrcIgnoresOnlyWhatItShould is the other half of the dividing
// rule. ONE statement is logged and dropped and no more: g:go_metalinter_enabled,
// which is golangci-lint over the whole package and is a ":!" away when wanted.
//
// "syntax enable" was the other one until a user overrode that decision and
// internal/syntax landed. It now reaches Sink.Syntax, so it belongs in
// neither list.
//
// The s: variables are the row worth reading. There are two of them and neither
// is here: a script-local variable is the loader's own machinery, it is what
// the next fifty lines are built out of, and putting it in the list of things
// pvim declined to do would say the opposite of what happened.
func TestTheLiveVimrcIgnoresOnlyWhatItShould(t *testing.T) {
	_, _, res := loadLive(t, false)

	var got []string
	for _, i := range res.Ignored {
		got = append(got, i.Name+"|"+i.Text)
	}
	want := []string{"g:go_metalinter_enabled|let g:go_metalinter_enabled=['all']"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ignored:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestTheLiveVimrcsScriptLocals is what the seven new builtins add up to.
//
// Line 5 is the line the whole file hangs off:
//
//	let s:vim_home = fnamemodify(resolve(expand('<sfile>:p')), ':h')
//
// and it is four features in one statement -- expand('<sfile>'), the ":p" and
// ":h" modifiers, resolve() over the symlink ~/.vimrc is, and an s: variable to
// keep the answer in. Everything from 'undodir' to 'directory' to
// 'viminfofile' is built out of it by concatenation, so getting it wrong makes
// six options wrong and none of them say so.
func TestTheLiveVimrcsScriptLocals(t *testing.T) {
	cfg, path, _ := loadLive(t, false)

	// resolve() is over the copy's own path, and on macOS a t.TempDir() under
	// /var is a symlink to /private/var, so this is the real thing and not a
	// path that happens to have no links in it.
	env := &DefaultEnv{Opts: cfg.Opts}
	home := env.Resolve(filepath.Dir(path))
	state := filepath.Join(home, "state")

	for name, want := range map[string]string{
		"undodir":     filepath.Join(state, "undo") + "//",
		"directory":   filepath.Join(state, "swap") + "//",
		"packpath":    home,
		"viminfofile": filepath.Join(state, "viminfo"),
	} {
		var got string
		switch name {
		case "packpath":
			got = cfg.Paths.PackPath
		case "viminfofile":
			got = cfg.Paths.ViminfoFile
		default:
			v, err := cfg.Opts.Get(name)
			if err != nil {
				t.Errorf("'%s' is %v", name, err)
				continue
			}
			got = v.Str
		}
		if got != want {
			t.Errorf("'%s' is %q, want %q", name, got, want)
		}
	}

	// And the three mkdir() calls made three directories. This is the bug the
	// plan found in the old vimrc -- "set undofile with undodir=~/.cache/vim
	// and that directory does not exist, so persistent undo has never
	// persisted" -- fixed in the file and only fixed in pvim if the calls run.
	for _, dir := range []string{state, filepath.Join(state, "undo"), filepath.Join(state, "swap")} {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("%s was not created: %v", dir, err)
			continue
		}
		// 0700, written in the file as an octal literal. A reader that took
		// 0700 for decimal seven hundred would have asked for 0o1274 here.
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s is mode %04o, want 0700: the vimrc writes the octal literal 0700", dir, perm)
		}
	}
}

// TestPackloadallPutsThePackagesOnRuntimepath is line 8, and the order is the
// assertion.
//
// Vim inserts each package directory immediately after the 'runtimepath' entry
// it lives under, so four packages under one entry come out in the reverse of
// the order they were found. Measured with "vim -u
// ~/.vimrc": the vim home, then vim-terraform,
// vim-go, nofrils, nerdtree, then ~/.vim. Appending them instead would put a
// colours file in ~/.vim ahead of a package's, which is the opposite of vim.
func TestPackloadallPutsThePackagesOnRuntimepath(t *testing.T) {
	cfg, path, _ := loadLive(t, false)

	env := &DefaultEnv{Opts: cfg.Opts}
	home := env.Resolve(filepath.Dir(path))
	got := strings.Split(cfg.Paths.RuntimePath, ",")

	want := []string{home}
	for _, name := range []string{"vim-terraform", "vim-go", "nofrils", "nerdtree"} {
		want = append(want, filepath.Join(home, "pack", "pkar", "start", name))
	}
	if len(got) < len(want) {
		t.Fatalf("'runtimepath' is %q, which is shorter than the five entries the file makes", cfg.Paths.RuntimePath)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("'runtimepath' entry %d is %q, want %q", i, got[i], w)
		}
	}

	// The colourscheme on line 9 is found through it, which is what the three
	// lines above are for.
	if _, ok := cfg.Paths.Find(filepath.Join("colors", "nofrils-dark.vim")); !ok {
		t.Error("colors/nofrils-dark.vim is not on 'runtimepath'; :colorscheme would answer E185")
	}
	if cfg.Scheme != "nofrils-dark" {
		t.Errorf("colorscheme is %q, want nofrils-dark", cfg.Scheme)
	}
}

// TestTheRestrictedExecuteRefusesWhatItShould is the fence around ":execute".
//
// pvim's is not vim's: it evaluates the same closed expression grammar the rest
// of this package has and then requires the result to be a command this loader
// already understands. Every row below is a thing vim would have run.
func TestTheRestrictedExecuteRefusesWhatItShould(t *testing.T) {
	for _, tc := range []struct {
		line string
		want string
	}{
		// The resolved text is in the message and the typed line is not,
		// which is the whole reason to run the expression before refusing.
		{`execute 'NERDTreeToggle'`, "E492: Not an editor command: NERDTreeToggle"},
		{`execute 'set frob' . 'nicate'`, "E518: Unknown option: frobnicate"},
		// Control flow and a nested :execute, refused by name.
		{`execute 'endif'`, "E15: Invalid expression: :execute may build"},
		{`execute 'execute "set sw=3"'`, "E15: Invalid expression: :execute may build"},
		// A function outside the ten.
		{`execute 'set sw=' . strlen('abc')`, "E117: Unknown function: strlen"},
		// Vim would join two space-separated expressions with a space. pvim
		// refuses: joining is the half of :execute that builds a command out
		// of parts nobody wrote down.
		{`execute 'set sw=4' 'set ts=4'`, "E488: Trailing characters"},
	} {
		cfg := NewConfig()
		res := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts}).Run([]byte(tc.line+"\n"), "x")
		if len(res.Errors) != 1 {
			t.Errorf("%s: %d errors, want 1: %v", tc.line, len(res.Errors), res.Log())
			continue
		}
		if got := res.Errors[0].Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%s\n got %q\nwant it to contain %q", tc.line, got, tc.want)
		}
	}
}

// TestTheRestrictedExecuteRunsWhatItShould is the other side: the five shapes
// the live vimrc uses, and a bar after one, which vim honours. Measured
// "execute 'set sw=3' | set ts=9" sets both.
func TestTheRestrictedExecuteRunsWhatItShould(t *testing.T) {
	cfg := NewConfig()
	src := "let s:d = '/tmp/x'\n" +
		"execute 'set undodir=' . fnameescape(s:d . '/undo//')\n" +
		"execute 'set sw=3' | set ts=9\n"
	res := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts}).Run([]byte(src), "x")
	for _, d := range res.Errors {
		t.Fatalf("%v", d)
	}
	for name, want := range map[string]string{
		"undodir":    "=/tmp/x/undo//",
		"shiftwidth": "=3",
		"tabstop":    "=9",
	} {
		v, err := cfg.Opts.Get(name)
		if err != nil {
			t.Errorf("'%s': %v", name, err)
			continue
		}
		if got := v.String(); got != want {
			t.Errorf("'%s' is %q, want %q", name, got, want)
		}
	}
}

// TestScriptLocalsDoNotLeakIntoASourcedFile is what "script-local" means.
//
// The live vimrc keeps its vim home in s:vim_home and then loads a
// colourscheme. A colourscheme that used the same name and a loader that kept
// one scope for both would overwrite the path the next fifty lines of the vimrc
// are built out of, and nothing would say so.
func TestScriptLocalsDoNotLeakIntoASourcedFile(t *testing.T) {
	cfg := NewConfig()
	ld := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts})

	ld.Run([]byte("let s:x = 'outer'\n"), "outer")
	if _, ok := ld.Vars["s:x"]; ok {
		t.Error("s:x outlived the file that set it")
	}

	// And a global does outlive it, which is the difference.
	ld.Run([]byte("let g:x = 'outer'\n"), "outer")
	if v, ok := ld.Vars["g:x"]; !ok || v.Str != "outer" {
		t.Errorf("g:x is %v after the file ended, want outer", ld.Vars["g:x"])
	}
}

// TestTheLiveVimrcAgreesWithVim is the comparison the gate is for: load the same
// file in vim and in pvim and diff what each one says and what each one set.
//
// "vim -u FILE file" and not "--clean" with a ":source", and it is written down
// here because it cost an hour once already: vim reads the file argument DURING
// startup and runs a -c or a -S AFTER it, so a vimrc sourced from -c registers
// its autocommands too late to fire on the buffer that is already open and
// every ":setlocal" in it lands on a window that was made before the rule
// existed. The same note is in cmd/pvim's filetype test.
//
// The -c here is only asking questions, which is after startup either way.
func TestTheLiveVimrcAgreesWithVim(t *testing.T) {
	if testing.Short() {
		// "make check-fast" is the gofmt, vet and the tests that do not shell
		// out to vim, which is what makes it a twenty-second loop.
		t.Skip("shells out to vim")
	}
	if _, err := os.Stat(theVim); err != nil {
		t.Skipf("no vim at %s", theVim)
	}

	// The options both sides can answer for, one per shape in the file: two
	// numbers, a boolean, a negated boolean, a comma list, the shell the
	// executable() branch chose, and the three paths built by concatenation.
	names := []string{
		"background", "backspace", "cmdheight", "completeopt", "conceallevel",
		"directory", "encoding", "hlsearch", "ignorecase", "linespace",
		"number", "scrolloff", "shell", "shiftround", "shiftwidth",
		"showmatch", "sidescrolloff", "smartcase", "softtabstop", "tabstop",
		"textwidth", "undodir", "undofile", "wildignore", "wrap",
	}

	cfg, path, res := loadLive(t, false)
	for _, d := range res.Errors {
		t.Errorf("pvim: %v", d)
	}

	var body strings.Builder
	body.WriteString("set nomore\n")
	for _, name := range names {
		fmt.Fprintf(&body, "set %s?\n", name)
	}
	for _, name := range []string{"runtimepath", "packpath", "viminfofile"} {
		fmt.Fprintf(&body, "echo '@%s=' . &%s\n", name, name)
	}
	said := vimSays(t, path, body.String())

	// Vim's own message log first. A real error in the file is a bug in the
	// file and not a gap in pvim, and it has to be told apart from one.
	for _, l := range vimMessages(t, path) {
		if code := eCode(l); code != "" {
			t.Errorf("vim itself said %q loading this vimrc; %s is a real error in the file", l, code)
		}
	}

	var opts []string
	paths := map[string]string{}
	for _, l := range said {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "@"); ok {
			name, value, _ := strings.Cut(rest, "=")
			paths[name] = value
			continue
		}
		opts = append(opts, l)
	}
	if len(opts) != len(names) {
		t.Fatalf("vim printed %d option lines for %d options: %v", len(opts), len(names), opts)
	}
	for i, name := range names {
		lines, err := cfg.Opts.ApplyLine(name+"?", options.Both)
		if err != nil || len(lines) != 1 {
			t.Errorf(":set %s? is %v %v; vim prints %q", name, lines, err, opts[i])
			continue
		}
		if lines[0] != opts[i] {
			t.Errorf(":set %s? prints %q, vim prints %q", name, lines[0], opts[i])
		}
	}

	// 'packpath' and 'viminfofile' have to agree exactly: both are one path
	// this file built and neither has a default underneath it.
	if got := cfg.Paths.PackPath; got != paths["packpath"] {
		t.Errorf("'packpath' is %q, vim says %q", got, paths["packpath"])
	}
	if got := cfg.Paths.ViminfoFile; got != paths["viminfofile"] {
		t.Errorf("'viminfofile' is %q, vim says %q", got, paths["viminfofile"])
	}

	// 'runtimepath' agrees on the five entries this file puts there and then
	// stops. Vim's tail is ~/.vim, three directories inside the installed
	// MacVim runtime, and ~/.vim/after; pvim has no $VIMRUNTIME to name and
	// does not invent one. See internal/vimrc/rtp.go.
	mine := strings.Split(cfg.Paths.RuntimePath, ",")
	theirs := strings.Split(paths["runtimepath"], ",")
	if len(mine) < 5 || len(theirs) < 5 {
		t.Fatalf("'runtimepath' is %v here and %v in vim", mine, theirs)
	}
	for i := 0; i < 5; i++ {
		if mine[i] != theirs[i] {
			t.Errorf("'runtimepath' entry %d is %q, vim says %q", i, mine[i], theirs[i])
		}
	}
}

// vimSays runs a body of ex commands in a vim started on the live vimrc, and
// returns the non-blank lines it redirected.
func vimSays(t *testing.T, vimrcPath, body string) []string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	ask := filepath.Join(dir, "ask.vim")
	src := fmt.Sprintf("redir! > %s\n%s\nredir END\nqa!\n", out, body)
	if err := os.WriteFile(ask, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(theVim, "-u", vimrcPath, "-i", "NONE", "--not-a-term",
		"-c", "source "+ask, target)
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v", theVim, err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("vim wrote no output: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(string(got), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// vimMessages is vim's message log after loading the live vimrc, minus the two
// lines it always prints: the maintainer banner and the file it opened.
func vimMessages(t *testing.T, vimrcPath string) []string {
	t.Helper()
	var out []string
	for _, l := range vimSays(t, vimrcPath, "set nomore\nmessages\n") {
		if strings.Contains(l, "Messages maintainer") || strings.HasPrefix(strings.TrimSpace(l), `"`) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// eCode returns the "E123" a message line starts one of its words with, and ""
// for a line that carries none. Matching on a bare "E" would find the E in
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
