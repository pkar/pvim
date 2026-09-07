package vimrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ten builtins, against what vim answers.
//
// Every "want" in this file was read off /opt/homebrew/bin/vim 9.2.0321 on
// with "vim -u NONE -c 'source ask.vim'", because six of the ten
// were added that day and a guess about fnameescape's character set is a path
// that goes into an option looking right and comes out of a shell wrong.

// TestTheBuiltinsAnswerWhatVimAnswers is the table vim was asked for.
func TestTheBuiltinsAnswerWhatVimAnswers(t *testing.T) {
	env := newTestEnv()
	env.dirs["/tmp"] = true
	env.execs["/opt/homebrew/bin/bash"] = true
	env.links["/home/x/.vimrc"] = "/work/vim/vimrc"

	for _, tc := range []struct {
		expr string
		want string
	}{
		// vim: ":echo fnamemodify('/a/b/c.vim', ':h')" and the other three.
		{`fnamemodify('/a/b/c.vim', ':h')`, "/a/b"},
		{`fnamemodify('/a/b/c.vim', ':t')`, "c.vim"},
		{`fnamemodify('/a/b/c.vim', ':r')`, "/a/b/c"},
		{`fnamemodify('/a/b/c.vim', ':e')`, "vim"},
		// ":p" against the working directory, and the chain the live vimrc's
		// line 5 is: a relative name made absolute and then its directory.
		{`fnamemodify('rel/c.vim', ':p')`, "/work/rel/c.vim"},
		{`fnamemodify('rel/c.vim', ':p:h')`, "/work/rel"},
		// vim: ":echo fnameescape('/a b/c#d%e.vim')" is '/a\ b/c\#d\%e.vim'.
		{`fnameescape('/a b/c#d%e.vim')`, `/a\ b/c\#d\%e.vim`},
		{`fnameescape('/plain/path')`, "/plain/path"},
		// vim: 1 and 0 for both pairs.
		{`executable('/opt/homebrew/bin/bash')`, "1"},
		{`executable('/nope/nope')`, "0"},
		{`isdirectory('/tmp')`, "1"},
		{`isdirectory('/tmp/nosuchdir')`, "0"},
		// vim: resolve() on a symlink gives what it points at, and on a plain
		// path gives the path.
		{`resolve('/home/x/.vimrc')`, "/work/vim/vimrc"},
		{`resolve('/tmp')`, "/tmp"},
		// vim: ":echo 0700 0x1f 08 0 007" is "448 31 8 0 7". The octal row is
		// the one that matters: it is mkdir's permission argument.
		{`0700`, "448"},
		{`0x1f`, "31"},
		{`08`, "8"},
		{`007`, "7"},
		{`0`, "0"},
		// Concatenation, both spellings, which is how every path in the live
		// vimrc is built.
		{`'/a' . '/b'`, "/a/b"},
		{`'/a' .. '/b'`, "/a/b"},
	} {
		v, n, err := eval(tc.expr, env, map[string]Val{}, "/work/vimrc")
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if rest := strings.TrimSpace(tc.expr[n:]); rest != "" {
			t.Errorf("%s: stopped with %q left", tc.expr, rest)
		}
		if got := v.String(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// TestExpandAnswersSfile is the one builtin whose answer depends on which file
// is being read, which is why it is the evaluator's and not Env's.
func TestExpandAnswersSfile(t *testing.T) {
	env := newTestEnv()
	for _, tc := range []struct{ expr, script, want string }{
		{`expand('<sfile>')`, "/work/vim/vimrc", "/work/vim/vimrc"},
		{`expand('<sfile>:p')`, "/work/vim/vimrc", "/work/vim/vimrc"},
		{`expand('<sfile>:p:h')`, "/work/vim/vimrc", "/work/vim"},
		// A relative script name is made absolute against the working
		// directory, which is what ":p" means.
		{`expand('<sfile>:p:h')`, "vimrc", "/work"},
		// The two forms that are the world's and go to Env.
		{`expand('~/.vimgo')`, "/work/vimrc", "/home/x/.vimgo"},
		{`expand('$MYVIMRC')`, "/work/vimrc", "/home/x/.vimrc"},
	} {
		v, _, err := eval(tc.expr, env, map[string]Val{}, tc.script)
		if err != nil {
			t.Errorf("%s in %s: %v", tc.expr, tc.script, err)
			continue
		}
		if got := v.Str; got != tc.want {
			t.Errorf("%s in %s = %q, want %q", tc.expr, tc.script, got, tc.want)
		}
	}
}

// TestTheBuiltinsRefusals is the fence. Each row is a thing vim would have
// answered and pvim will not, with the code it says so under.
//
// The two expand() rows are the ones worth arguing about. Vim leaves an
// unknown "<...>" in the string, so expand('<afile>') outside an autocommand
// is the six characters "<afile>", and a path built out of that lands in an
// option and fails somewhere else entirely. And an unknown filename modifier
// is left in the name for the same reason. Both are refused here, loudly,
// because a path that is silently wrong is the failure this package exists to
// prevent.
func TestTheBuiltinsRefusals(t *testing.T) {
	env := newTestEnv()
	for _, tc := range []struct{ expr, want string }{
		{`strlen('abc')`, "E117: Unknown function: strlen"},
		{`printf('%d', 1)`, "E117: Unknown function: printf"},
		{`glob('*')`, "E117: Unknown function: glob"},
		{`fnamemodify('/a/b', ':s?a?b?')`, "E15: Invalid expression: unsupported filename modifier"},
		{`fnamemodify('/a/b', ':~')`, "E15: Invalid expression: unsupported filename modifier"},
		{`expand('<afile>')`, "pvim answers <sfile> and nothing else"},
		{`expand('<cword>')`, "pvim answers <sfile> and nothing else"},
		{`fnamemodify('/a/b')`, "E119: Not enough arguments for function: fnamemodify"},
		{`has('x', 'y')`, "E118: Too many arguments for function: has"},
		{`mkdir('/a', 'p', 0700, 'extra')`, "E118: Too many arguments for function: mkdir"},
	} {
		_, _, err := eval(tc.expr, env, map[string]Val{}, "/work/vimrc")
		if err == nil {
			t.Errorf("%s did not fail; want %s", tc.expr, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want it to contain %q", tc.expr, err, tc.want)
		}
	}

	// And expand('<sfile>') outside a file is an error rather than an empty
	// string, because a path built out of an empty <sfile> is rooted at "/".
	if _, _, err := eval(`expand('<sfile>')`, env, map[string]Val{}, ""); err == nil {
		t.Error("expand('<sfile>') with no file being sourced did not fail")
	}
}

// TestCallRunsABuiltinAndRefusesTheRest is the ":call" line.
//
// It moved and it moved exactly as far as mkdir(): the live
// vimrc's only way to make the directory it is about to point 'undodir' at is
// "call mkdir(s:vim_state, 'p', 0700)". Everything that is not one of the ten
// is still E117.
func TestCallRunsABuiltinAndRefusesTheRest(t *testing.T) {
	dir := t.TempDir()
	made := filepath.Join(dir, "a", "b")

	cfg := NewConfig()
	src := "let s:d = '" + made + "'\ncall mkdir(s:d, 'p', 0700)\n"
	res := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts}).Run([]byte(src), "x")
	for _, d := range res.Errors {
		t.Fatalf("%v", d)
	}
	info, err := os.Stat(made)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s was not created: %v", made, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("%s is mode %04o, want 0700", made, perm)
	}

	// A call of anything else is E117, and a call of a function the same load
	// stepped over is still the logged refusal it always was.
	cfg = NewConfig()
	res = NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts}).Run([]byte("call NERDTreeToggle()\n"), "x")
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error(), "E117: Unknown function: NERDTreeToggle") {
		t.Errorf("call of a plugin function said %v, want E117", res.Log())
	}
}

// TestMkdirRefusesAFlagItDoesNotHave is the other half of "refuse rather than
// pretend".
//
// Vim's mkdir(x, 'D') removes the directory when vim exits. Doing the create
// and quietly skipping the remove leaves a directory behind forever, and the
// way that is found is by running out of disk.
func TestMkdirRefusesAFlagItDoesNotHave(t *testing.T) {
	env := &DefaultEnv{}
	if err := env.MkDir(filepath.Join(t.TempDir(), "x"), "D", 0o700); err == nil {
		t.Error("mkdir with the 'D' flag succeeded; pvim has no delete-on-exit")
	}
}
