package vimrc

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
)

// testEnv is the world the evaluator tests ask questions of. It is not
// DefaultEnv because these tests are about the evaluator and not about the
// filesystem.
type testEnv struct {
	gui   bool
	opts  options.Options
	files map[string]bool
	env   map[string]string

	// The world the six filesystem builtins ask about, faked so that no test
	// in this package touches a disk. made records what mkdir() was asked for,
	// in order, which is the only way to tell "the vimrc created its state
	// directory" from "the vimrc decided it did not have to".
	dirs  map[string]bool
	execs map[string]bool
	links map[string]string
	made  []string
	cwd   string
}

func newTestEnv() *testEnv {
	return &testEnv{
		opts:  options.Defaults(),
		files: map[string]bool{},
		env:   map[string]string{"MYVIMRC": "/home/x/.vimrc", "MYGVIMRC": "/home/x/.gvimrc"},
		dirs:  map[string]bool{},
		execs: map[string]bool{},
		links: map[string]string{},
		cwd:   "/work",
	}
}

func (e *testEnv) Has(f string) bool {
	if f == "gui_running" {
		return e.gui
	}
	return trueFeatures[f]
}

func (e *testEnv) Exists(name string) bool {
	if opt, ok := strings.CutPrefix(name, "&"); ok {
		_, err := e.opts.Get(opt)
		return err == nil
	}
	return false
}

func (e *testEnv) Option(name string) (options.Value, error) { return e.opts.Get(name) }

func (e *testEnv) Expand(s string) string {
	if name, ok := strings.CutPrefix(s, "$"); ok {
		rest := ""
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name, rest = name[:i], name[i:]
		}
		return e.env[name] + rest
	}
	if after, ok := strings.CutPrefix(s, "~"); ok {
		return "/home/x" + after
	}
	return s
}

func (e *testEnv) FileReadable(p string) bool  { return e.files[p] }
func (e *testEnv) Executable(name string) bool { return e.execs[name] }
func (e *testEnv) IsDirectory(p string) bool   { return e.dirs[p] }
func (e *testEnv) Cwd() string                 { return e.cwd }

func (e *testEnv) MkDir(path, flags string, mode int) error {
	if e.dirs[path] {
		return errors.New("file exists")
	}
	e.made = append(e.made, fmt.Sprintf("%s %s %04o", path, flags, mode))
	e.dirs[path] = true
	return nil
}

func (e *testEnv) Resolve(p string) string {
	if to, ok := e.links[p]; ok {
		return to
	}
	return p
}

// TestEvalTheVimrcsConditions is every ":if" in ~/.vimrc, both ways round.
//
// These eight lines are the whole reason this package has an expression
// evaluator, and the one that matters most is has('gui_running'): the vimrc's
// FastEscape autocommands and its <C-c>/<C-v> clipboard mappings are behind
// !has('gui_running'), so the terminal frontend has to answer false and get
// them and the window has to answer true and not.
func TestEvalTheVimrcsConditions(t *testing.T) {
	for _, tc := range []struct {
		expr     string
		gui      bool
		want     bool
		vars     map[string]Val
		scrolled bool
	}{
		{expr: "has('gui_running')", gui: true, want: true},
		{expr: "has('gui_running')", gui: false, want: false},
		{expr: "!has('gui_running')", gui: false, want: true},
		{expr: "has('mouse')", want: true},
		{expr: "has('clipboard') && !has('gui_running')", gui: false, want: true},
		{expr: "has('clipboard') && !has('gui_running')", gui: true, want: false},
		{expr: "has('persistent_undo')", want: true},
		{expr: `has("autocmd")`, want: true},
		{expr: "has('conceal')", want: true},
		{expr: "has('python3')", want: false},
		{expr: `exists("syntax_on")`, want: false},
		{expr: `!exists("g:nofrils_strbackgrounds")`, want: true},
		{expr: `!exists("g:nofrils_strbackgrounds")`, vars: map[string]Val{"g:nofrils_strbackgrounds": Number(0)}, want: false},
		{expr: "has('gui_running') && filereadable($MYGVIMRC)", gui: true, want: false},
	} {
		env := newTestEnv()
		env.gui = tc.gui
		vars := tc.vars
		if vars == nil {
			vars = map[string]Val{}
		}
		v, n, err := eval(tc.expr, env, vars, "")
		if err != nil {
			t.Errorf("%q: %v", tc.expr, err)
			continue
		}
		if n != len(tc.expr) {
			t.Errorf("%q: consumed %d of %d bytes", tc.expr, n, len(tc.expr))
		}
		if got := v.Truthy(); got != tc.want {
			t.Errorf("%q with gui=%v = %v, want %v", tc.expr, tc.gui, got, tc.want)
		}
	}
}

// TestEvalOptionConditions is "if !&scrolloff" and "if !&sidescrolloff",
// lines 198 and 201 of the vimrc.
func TestEvalOptionConditions(t *testing.T) {
	env := newTestEnv()
	if err := env.opts.Set("scrolloff", "3"); err != nil {
		t.Fatal(err)
	}
	v, _, err := eval("!&scrolloff", env, map[string]Val{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Truthy() {
		t.Error("!&scrolloff is true with scrolloff=3")
	}
	if err := env.opts.Set("scrolloff", "0"); err != nil {
		t.Fatal(err)
	}
	v, _, err = eval("!&scrolloff", env, map[string]Val{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Truthy() {
		t.Error("!&scrolloff is false with scrolloff=0")
	}
}

// TestEvalValues is the right-hand sides the vimrc and the colourschemes
// assign, including the dictionary literal out.
func TestEvalValues(t *testing.T) {
	env := newTestEnv()
	for _, tc := range []struct {
		expr string
		want string
		kind ValueKind
	}{
		{expr: `","`, want: ",", kind: ValueString},
		{expr: `1`, want: "1", kind: ValueNumber},
		{expr: `0`, want: "0", kind: ValueNumber},
		{expr: `40`, want: "40", kind: ValueNumber},
		{expr: `'results:40'`, want: "results:40", kind: ValueString},
		{expr: `expand('~/.vimgo')`, want: "/home/x/.vimgo", kind: ValueString},
		{expr: `"/tmp/vim_ai_debug.log"`, want: "/tmp/vim_ai_debug.log", kind: ValueString},
		{expr: `['all']`, want: "['all']", kind: ValueList},
		// The NERDTreeIgnore list, whose single-quoted strings keep every
		// backslash: vim's string() prints it back exactly like this.
		{
			expr: `['\~$','\.pyc$','\*NTUSER*','\*ntuser*','\NTUSER.DAT','\ntuser.ini']`,
			want: `['\~$', '\.pyc$', '\*NTUSER*', '\*ntuser*', '\NTUSER.DAT', '\ntuser.ini']`,
			kind: ValueList,
		},
		// g:ctrlp_custom_ignore, joined from its three continuation lines.
		// The expected text is what vim's string() prints for it, measured,
		// with the keys in sorted order because a vim dictionary
		// has none.
		{
			expr: `{ 'dir':  '\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$', 'file': '\.so$\|\.dat$|\.DS_Store|\.pyc$' }`,
			want: `{'dir': '\.git$\|\.yardoc\|public$|log\|tmp|\.hg|\.svn$', 'file': '\.so$\|\.dat$|\.DS_Store|\.pyc$'}`,
			kind: ValueDict,
		},
	} {
		v, n, err := eval(tc.expr, env, map[string]Val{}, "")
		if err != nil {
			t.Errorf("%q: %v", tc.expr, err)
			continue
		}
		if n != len(tc.expr) {
			t.Errorf("%q: consumed %d of %d bytes", tc.expr, n, len(tc.expr))
		}
		if v.Kind != tc.kind {
			t.Errorf("%q is a %s, want a %s", tc.expr, v.Kind, tc.kind)
		}
		if got := v.String(); got != tc.want {
			t.Errorf("%q = %s, want %s", tc.expr, got, tc.want)
		}
	}
}

// TestEvalStopsAtAComment is what lets a ":let" have one.
//
// `let g:go_metalinter_enabled = ['all'] " ['vet', 'revive', ...]` assigns a
// one-element list in vim, measured. It works because the
// expression parser stops after the "]" and the caller then finds a comment.
// A comment stripper run first would have cut
// `let g:vim_ai_debug_log_file = "/tmp/vim_ai_debug.log"` in half at its
// opening quote, which is why stripComment is not used for :let.
func TestEvalStopsAtAComment(t *testing.T) {
	const expr = `['all'] " ['vet', 'revive', 'errcheck', '']`
	v, n, err := eval(expr, newTestEnv(), map[string]Val{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "['all']" {
		t.Errorf("value = %s, want ['all']", v.String())
	}
	if err := trailing(expr[n:]); err != nil {
		t.Errorf("what follows is not a comment: %v", err)
	}
}

// TestEvalRefusals is the line this package draws, as a test. Each of these is
// something vim does and pvim will not, and each has to say so rather than
// answer wrong.
func TestEvalRefusals(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want error
	}{
		{`'a' =~ 'b'`, ErrNoRegex},
		{`'a' !~ 'b'`, ErrNoRegex},
		{`Frobnicate()`, ErrNoFunc},
		{`strlen('abc')`, ErrNoFunc},
		{`g:never_set`, ErrNoVar},
		{``, ErrNoExpr},
	} {
		_, _, err := eval(tc.expr, newTestEnv(), map[string]Val{}, "")
		if !errors.Is(err, tc.want) {
			t.Errorf("eval(%q) = %v, want %v", tc.expr, err, tc.want)
		}
	}
}

// TestEvalComparisons covers the operators . A bare "==" is
// case-sensitive here and follows 'ignorecase' in vim; nothing in the vimrc or
// the four colourschemes compares two strings, so the difference is
// unreachable from the config this package exists to read.
func TestEvalComparisons(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want bool
	}{
		{`1 == 1`, true},
		{`1 != 1`, false},
		{`2 > 1`, true},
		{`'a' ==# 'a'`, true},
		{`'a' ==# 'A'`, false},
		{`'a' ==? 'A'`, true},
		{`'a' !=# 'A'`, true},
		{`1 && 0`, false},
		{`1 || 0`, true},
		{`!0`, true},
		{`1 ? 2 : 3`, true},
		{`'abc' . 'def' ==# 'abcdef'`, true},
	} {
		v, _, err := eval(tc.expr, newTestEnv(), map[string]Val{}, "")
		if err != nil {
			t.Errorf("%q: %v", tc.expr, err)
			continue
		}
		if got := v.Truthy(); got != tc.want {
			t.Errorf("%q = %v, want %v", tc.expr, got, tc.want)
		}
	}
}
