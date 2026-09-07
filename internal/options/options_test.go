package options

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// vimrcPath is the config this editor exists to run. It is read at run time and
// never copied into this package: a list of option names transcribed by hand
// goes stale the first time a line in the real file changes, and then this test
// is certifying a table against a vimrc nobody has.
// vimrcPath is the copy internal/vimrc keeps, not ~/.vimrc. The recorded
// profile beside this file was measured under that config, and ~/.vimrc is a
// symlink that was repointed at a different vimrc which spells
// "set encoding=utf-8" where this one spells "set enc=utf-8". Reading the live
// symlink graded this package against one config using a measurement of
// another. See internal/vimrc/load_test.go for the whole note.
var vimrcPath = filepath.Join("..", "vimrc", "testdata", "vimrc")

// TestEveryVimrcOptionExists is the reason this package has a table: every
// option the user's ~/.vimrc names has to be here, or the vimrc loader answers
// E518 on a line that has worked for years.
//
// The names come out of the file itself, in whatever form it spells them, so a
// line added to the vimrc tomorrow is a failure here.
func TestEveryVimrcOptionExists(t *testing.T) {
	for _, name := range vimrcOptionNames(t) {
		if known(name) {
			continue
		}
		t.Errorf("the vimrc sets %q and this package has never heard of it", name)
	}
}

// vimrcOptionNames reads every option name the vimrc's ":set" lines mention,
// with any "no" or "inv" prefix and any operator stripped.
func vimrcOptionNames(t *testing.T) []string {
	t.Helper()

	f, err := os.Open(vimrcPath)
	if err != nil {
		t.Skipf("the vimrc is not on this machine: %v", err)
	}
	defer f.Close()

	var names []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "set ") {
			continue
		}
		// Strip a trailing comment. The vimrc has plenty, none of them with a
		// quote inside an option value, which is the case this would get wrong.
		if i := strings.Index(line, `"`); i >= 0 {
			line = line[:i]
		}
		for _, a := range splitArgs(strings.TrimPrefix(line, "set ")) {
			p, err := parse(a.text)
			if err != nil {
				t.Errorf("the vimrc has %q and this package cannot parse it: %v", a.text, err)
				continue
			}
			names = append(names, p.name)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", vimrcPath, err)
	}
	if len(names) < 40 {
		t.Fatalf("only %d option names came out of the vimrc, which has about fifty; the reader is broken", len(names))
	}
	return names
}

// TestSetSyntax runs each operator ":set" understands, on the vimrc's own uses
// of them where there is one.
func TestSetSyntax(t *testing.T) {
	o := Defaults()

	if _, err := o.Apply("ts=2", Both); err != nil {
		t.Fatalf("ts=2: %v", err)
	}
	if o.B.TabStop != 2 || o.GB.TabStop != 2 {
		t.Errorf(":set writes both halves: local %d global %d, want 2 and 2", o.B.TabStop, o.GB.TabStop)
	}

	// The vimrc's line 83. A char-list -= takes one letter out.
	if _, err := o.Apply("formatoptions-=t", Both); err != nil {
		t.Fatalf("fo-=t: %v", err)
	}
	if o.B.FormatOpts != "cq" {
		t.Errorf("formatoptions after -=t is %q, want \"cq\"", o.B.FormatOpts)
	}

	// The vimrc's line 14. Same shape, different option.
	if _, err := o.Apply("guioptions-=e", Both); err != nil {
		t.Fatalf("go-=e: %v", err)
	}
	if strings.ContainsRune(o.G.GuiOptions, 'e') {
		t.Errorf("guioptions after -=e is %q, which still has the e in it", o.G.GuiOptions)
	}

	// A comma list takes a whole item, not a letter, and += on an item that is
	// already there changes nothing.
	for _, step := range []struct{ arg, want string }{
		{"wildignore=*.o,*.pyc", "*.o,*.pyc"},
		{"wildignore-=*.o", "*.pyc"},
		{"wildignore-=*.nothing", "*.pyc"},
		{"wildignore+=*.zip", "*.pyc,*.zip"},
		{"wildignore+=*.zip", "*.pyc,*.zip"},
		{"wildignore^=*.a", "*.a,*.pyc,*.zip"},
	} {
		if _, err := o.Apply(step.arg, Both); err != nil {
			t.Fatalf("%s: %v", step.arg, err)
		}
		if o.G.WildIgnore != step.want {
			t.Errorf("after :set %s wildignore is %q, want %q", step.arg, o.G.WildIgnore, step.want)
		}
	}

	// A number ^= multiplies. That is vim's, and it is the one operator nobody
	// guesses right.
	o.B.ShiftWidth = 4
	if _, err := o.Apply("sw^=2", Both); err != nil {
		t.Fatal(err)
	}
	if o.B.ShiftWidth != 8 {
		t.Errorf("sw^=2 with sw=4 gave %d, want 8", o.B.ShiftWidth)
	}

	// no, inv and !.
	for _, step := range []struct {
		arg  string
		want bool
	}{{"nowrap", false}, {"wrap!", true}, {"invwrap", false}, {"wrap", true}} {
		if _, err := o.Apply(step.arg, Both); err != nil {
			t.Fatalf("%s: %v", step.arg, err)
		}
		if o.W.Wrap != step.want {
			t.Errorf("after :set %s 'wrap' is %v, want %v", step.arg, o.W.Wrap, step.want)
		}
	}

	// ":set name<" on a window-local option copies the global down.
	o.GW.Wrap, o.W.Wrap = true, false
	if _, err := o.Apply("wrap<", Both); err != nil {
		t.Fatal(err)
	}
	if !o.W.Wrap {
		t.Error("wrap< did not take the global value")
	}
}

// TestResetIsVimsDefaultAndNotOurs is the difference between Builtin and
// Defaults, which is the whole reason there are two of them.
//
// ":set scrolloff&" is 0 in vim even though "vim --clean" starts at 5, because
// defaults.vim set it and & does not care what defaults.vim did. Resetting to
// the value the editor happened to start with would make ":set name&" mean
// "undo my changes", which is a different and much less useful thing.
func TestResetIsVimsDefaultAndNotOurs(t *testing.T) {
	o := Defaults()
	if got := o.ScrollOffValue(); got != 5 {
		t.Fatalf("this editor starts at scrolloff %d, and vim --clean starts at 5", got)
	}
	if _, err := o.Apply("scrolloff&", Both); err != nil {
		t.Fatal(err)
	}
	if o.G.ScrollOff != 0 {
		t.Errorf(":set scrolloff& left the global at %d, and vim answers 0", o.G.ScrollOff)
	}
	if o.W.ScrollOff != 0 {
		t.Errorf(":set scrolloff& left the local at %d, and vim answers 0", o.W.ScrollOff)
	}

	// A string global-local resets its local half to the unset marker instead,
	// which is vim's own inconsistency and was measured both ways round.
	o = Defaults()
	if err := o.SetIn("completeopt", "menu", SetLocal); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Apply("completeopt&", Both); err != nil {
		t.Fatal(err)
	}
	if o.B.CompleteOpt != "" {
		t.Errorf(":set completeopt& left the local at %q, and vim leaves it empty", o.B.CompleteOpt)
	}
	if o.G.CompleteOpt != "menu,preview" {
		t.Errorf(":set completeopt& left the global at %q, want \"menu,preview\"", o.G.CompleteOpt)
	}

	// &vi is a different table for eleven options, and 'backspace' is one.
	o = Defaults()
	if _, err := o.Apply("backspace&vi", Both); err != nil {
		t.Fatal(err)
	}
	if o.G.Backspace != "" {
		t.Errorf(":set backspace&vi is %q, and vi has none", o.G.Backspace)
	}
	if _, err := o.Apply("backspace&vim", Both); err != nil {
		t.Fatal(err)
	}
	if o.G.Backspace != "indent,eol,start" {
		t.Errorf(":set backspace&vim is %q, want \"indent,eol,start\"", o.G.Backspace)
	}
}

// TestUnknownOptionIsE518 is the whole error contract the vimrc loader keys off:
// an option nobody has heard of is E518 and an option accepted on purpose is
// silence.
func TestUnknownOptionIsE518(t *testing.T) {
	o := Defaults()
	_, err := o.Apply("nosuchoption", Both)
	var e *Error
	if !errors.As(err, &e) || e.Code != "E518" {
		t.Fatalf("unknown option gave %v, want an E518", err)
	}
	if _, err := o.Apply("nocompatible", Both); err != nil {
		t.Errorf("nocompatible is accepted and inert, got %v", err)
	}
	if _, err := o.Apply("t_vb=", Both); err != nil {
		t.Errorf("the vimrc's t_vb= is accepted and inert, got %v", err)
	}
}

// TestSetLocalDoesNotLeak is the reason Options carries GB and GW. An autocmd
// doing ":setlocal noexpandtab" on a Go file must not change what the next
// buffer opens with, and the two values are what tells them apart.
func TestSetLocalDoesNotLeak(t *testing.T) {
	o := Defaults()
	if err := o.SetIn("tabstop", "4", SetLocal); err != nil {
		t.Fatal(err)
	}
	if o.B.TabStop != 4 {
		t.Errorf("setlocal did not reach the buffer: %d", o.B.TabStop)
	}
	if o.GB.TabStop != 8 {
		t.Errorf("setlocal reached the global value too: %d, want 8", o.GB.TabStop)
	}
	if err := o.SetIn("tabstop", "2", SetGlobal); err != nil {
		t.Fatal(err)
	}
	if o.B.TabStop != 4 {
		t.Errorf("setglobal reached the buffer: %d, want 4", o.B.TabStop)
	}
}

// TestSetOnAGlobalLocalOptionLeavesTheWindowAlone pins the rule that keeps the
// vimrc's "set scrolloff=3" from pinning every window ever opened.
//
// Measured ":set so=3" in a window with no local value leaves
// the local at -1 and writes only the global, and does write the local when the
// window already had one. A string does neither: it clears the local.
func TestSetOnAGlobalLocalOptionLeavesTheWindowAlone(t *testing.T) {
	o := Defaults()
	if err := o.Set("scrolloff", "3"); err != nil {
		t.Fatal(err)
	}
	if o.W.ScrollOff != -1 {
		t.Errorf(":set so=3 wrote %d into the window; vim leaves the unset marker there", o.W.ScrollOff)
	}
	if o.G.ScrollOff != 3 {
		t.Errorf(":set so=3 left the global at %d", o.G.ScrollOff)
	}

	if err := o.SetIn("scrolloff", "9", SetLocal); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("scrolloff", "4"); err != nil {
		t.Fatal(err)
	}
	if o.W.ScrollOff != 4 {
		t.Errorf(":set so=4 over a window that had 9 left %d, want 4", o.W.ScrollOff)
	}

	if err := o.SetIn("completeopt", "menu", SetLocal); err != nil {
		t.Fatal(err)
	}
	if err := o.Set("completeopt", "longest"); err != nil {
		t.Fatal(err)
	}
	if o.B.CompleteOpt != "" {
		t.Errorf(":set cot=longest left %q in the buffer; vim clears it", o.B.CompleteOpt)
	}
	if o.G.CompleteOpt != "longest" {
		t.Errorf(":set cot=longest left the global at %q", o.G.CompleteOpt)
	}
}

// TestGlobalLocalFallback pins the resolution the vimrc depends on: it sets
// 'scrolloff' globally and every window reads it through the local -1.
func TestGlobalLocalFallback(t *testing.T) {
	o := Defaults()
	if err := o.Set("scrolloff", "3"); err != nil {
		t.Fatal(err)
	}
	if got := o.ScrollOffValue(); got != 3 {
		t.Errorf("scrolloff resolved to %d, want 3", got)
	}
	o.W.ScrollOff = 0
	if got := o.ScrollOffValue(); got != 0 {
		t.Errorf("a local 0 must win over the global 3, got %d", got)
	}
	o.W.ScrollOff = -1
	if got := o.ScrollOffValue(); got != 3 {
		t.Errorf("a local -1 is unset and must fall back to 3, got %d", got)
	}

	// ":setlocal scrolloff=-1" is how a window gives the option back, and it is
	// the one place a negative gets past the minimum check.
	if err := o.SetIn("scrolloff", "-1", SetLocal); err != nil {
		t.Errorf("setlocal so=-1 is how vim spells unset, and this refused it: %v", err)
	}
	// ":set" and ":setglobal" refuse the same number.
	for _, w := range []Where{Both, SetGlobal} {
		if err := o.SetIn("scrolloff", "-1", w); err == nil {
			t.Errorf("so=-1 in scope %v was accepted; vim answers E487", w)
		}
	}

	// ":setlocal name<" puts the marker back rather than freezing the global.
	if err := o.SetIn("scrolloff", "9", SetLocal); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Apply("scrolloff<", SetLocal); err != nil {
		t.Fatal(err)
	}
	if o.W.ScrollOff != -1 {
		t.Errorf("setlocal so< left %d in the window, want the unset marker -1", o.W.ScrollOff)
	}
}

// TestHasIsNotContains is the 'clipboard' trap: "unnamedplus" contains
// "unnamed", so a Contains-based reader is wrong everywhere but macOS.
func TestHasIsNotContains(t *testing.T) {
	if Has("unnamedplus", "unnamed") {
		t.Error(`Has("unnamedplus", "unnamed") is true; it is one item, not two`)
	}
	if !Has("unnamed,unnamedplus,autoselect", "unnamed") {
		t.Error("Has missed an item that is really there")
	}
}

// TestSplitArgsKeepsEscapedSpaces covers ":set listchars=tab:>\ ", which the
// vimrc does not have and which a splitter on plain white space breaks. It also
// pins the trailing white space each argument carries, which is not decoration:
// vim quotes it back in an error message.
func TestSplitArgsKeepsEscapedSpaces(t *testing.T) {
	got := splitArgs(`ts=2 listchars=tab:>\ ,eol:$  sw=4`)
	want := []arg{
		{`ts=2`, " "},
		{`listchars=tab:>\ ,eol:$`, "  "},
		{`sw=4`, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("split into %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d is %#v, want %#v", i, got[i], want[i])
		}
	}
}

// TestEveryFieldIsReachable makes the table the contract it claims to be: no
// spec may name a field the ref function cannot produce a pointer for, and no
// two specs may share a name.
func TestEveryFieldIsReachable(t *testing.T) {
	o := Defaults()
	seen := map[string]bool{}
	for i := range specs {
		s := &specs[i]
		if seen[s.Name] {
			t.Errorf("%q is in the table twice", s.Name)
		}
		seen[s.Name] = true
		for _, w := range []Where{SetLocal, SetGlobal} {
			switch p := s.ref(&o, w).(type) {
			case *bool:
				if s.Kind != Bool {
					t.Errorf("%s is declared %v and points at a bool", s.Name, s.Kind)
				}
			case *int:
				if s.Kind != Number {
					t.Errorf("%s is declared %v and points at an int", s.Name, s.Kind)
				}
			case *string:
				if s.Kind != String {
					t.Errorf("%s is declared %v and points at a string", s.Name, s.Kind)
				}
			default:
				t.Errorf("%s points at %T, which is not an option value", s.Name, p)
			}
		}
	}
}

// TestLimitTablesNameRealOptions keeps limits.go honest. A minimum or a legal
// character set for an option that is not in the table is a measurement nothing
// consults, and it is how a rename quietly turns validation off.
func TestLimitTablesNameRealOptions(t *testing.T) {
	for _, m := range []struct {
		what  string
		names []string
	}{
		{"numMin", keys(numMin)},
		{"numMax", keys(numMax)},
		{"flagChars", keys(flagChars)},
		{"listItems", keys(listItems)},
		{"listPrefixes", keys(listPrefixes)},
		{"unvalidated", unvalidated},
		{"viDefaults", keys(viDefaults)},
	} {
		for _, name := range m.names {
			s, ok := Lookup(name)
			if !ok {
				t.Errorf("%s names %q, which is not an option", m.what, name)
				continue
			}
			if s.Name != name {
				t.Errorf("%s names %q, which is the abbreviation for %q; these tables key on long names", m.what, name, s.Name)
			}
		}
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestGetResolvesGlobalLocal is the reason Get and GetRaw are two functions.
//
// The vimrc reads "&scrolloff" on line 198 -- "if !&scrolloff | set
// scrolloff=1 | endif" -- and a Get that handed back the unset sentinel of -1
// would make that condition false and quietly skip the line. Vim answers with
// the effective value, so this does too, and GetRaw is what a caller writing the
// field back asks for.
func TestGetResolvesGlobalLocal(t *testing.T) {
	o := Defaults()
	v, err := o.Get("scrolloff")
	if err != nil {
		t.Fatal(err)
	}
	if v.Num != 5 {
		t.Errorf("&scrolloff is %d, and vim --clean answers 5", v.Num)
	}
	raw, err := o.GetRaw("scrolloff", SetLocal)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Num != -1 {
		t.Errorf("the raw local 'scrolloff' is %d, want the unset sentinel -1", raw.Num)
	}

	// A local value wins, including a local zero, which is the case a
	// truthiness check would get wrong in the other direction.
	if err := o.SetIn("scrolloff", "0", SetLocal); err != nil {
		t.Fatal(err)
	}
	if v, _ := o.Get("scrolloff"); v.Num != 0 {
		t.Errorf("a local 0 resolved to %d, want 0", v.Num)
	}
}

// TestNumberSyntax pins vim's number parser, which is not strconv.Atoi.
func TestNumberSyntax(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
		ok   bool
	}{
		{"4", 4, true},
		{"0x10", 16, true},
		{"0X10", 16, true},
		{"011", 9, true},  // octal, and it looks like eleven
		{"0b11", 3, true}, // binary
		{"-1", -1, true},  // parses; the option's minimum is what refuses it
		{"+4", 0, false},  // vim takes no leading plus, and every other number in its grammar does
		{"", 0, false},
		{"abc", 0, false},
		{"4x", 0, false},
		{" 4", 0, false},
		{"99999999999999999999999999", 0, false},
	} {
		got, ok := vimNumber(tc.text)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("vimNumber(%q) = %d, %v; want %d, %v", tc.text, got, ok, tc.want, tc.ok)
		}
	}
}

// TestFlagListAppendMoves is the one string operator that does not do what it
// looks like.
//
// ":set fo+=t" on "tcq" is "cqt" in vim, not "tcq": the added letters come out
// of where they were and go on the end. Measured, because there is no reason to
// expect it and no test anywhere else that would catch it.
func TestFlagListAppendMoves(t *testing.T) {
	for _, tc := range []struct{ start, op, want string }{
		{"tcq", "fo+=t", "cqt"},
		{"tcq", "fo+=j", "tcqj"},
		{"tcq", "fo+=jt", "cqjt"},
		{"tcq", "fo^=q", "tcq"},
		{"tcq", "fo^=a", "atcq"},
		{"tcq", "fo-=z", "tcq"},
		{"tcq", "fo-=cq", "t"},
	} {
		o := Defaults()
		o.B.FormatOpts, o.GB.FormatOpts = tc.start, tc.start
		if _, err := o.Apply(tc.op, Both); err != nil {
			t.Fatalf("%q then :set %s: %v", tc.start, tc.op, err)
		}
		if o.B.FormatOpts != tc.want {
			t.Errorf("%q then :set %s gave %q, want %q", tc.start, tc.op, o.B.FormatOpts, tc.want)
		}
	}
}

// TestErrorCodesMatchVim.
//
// Every expectation here was read out of vim 9.2 patches 1-321 by
// running the command inside a try/catch and writing v:exception out. None of
// them is guessable and three are actively surprising: ":set tabstop!" is E488
// and not the E474 that ":set ignorecase=1" gets, ":set notabstop" is E474 and
// not the E521 a number assignment of "no" would give, and 'scroll' has an
// E-code of its own for a number below its minimum.
func TestErrorCodesMatchVim(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"nosuchoption", "E518: Unknown option: nosuchoption"},
		{"ts=abc", "E521: Number required after =: ts=abc"},
		{"ts=", "E521: Number required after =: ts="},
		{"ts+=abc", "E521: Number required after =: ts+=abc"},
		{"ignorecase=1", "E474: Invalid argument: ignorecase=1"},
		{"ic=", "E474: Invalid argument: ic="},
		{"tabstop!", "E488: Trailing characters: tabstop!"},
		{"ts?extra", "E488: Trailing characters: ts?extra"},
		{"ts&x", "E488: Trailing characters: ts&x"},
		{"notabstop", "E474: Invalid argument: notabstop"},
		{"invtabstop", "E474: Invalid argument: invtabstop"},
		{"nowrap=1", "E474: Invalid argument: nowrap=1"},
		{"ts=-1", "E487: Argument must be positive: ts=-1"},
		{"ts=0", "E487: Argument must be positive: ts=0"},
		{"sw-=6", "E487: Argument must be positive: sw-=6"},
		{"ch=0", "E487: Argument must be positive: ch=0"},
		{"scroll=-1", "E49: Invalid scroll size: scroll=-1"},
		{"ts=10000", "E474: Invalid argument: ts=10000"},
		{"cole=4", "E474: Invalid argument: cole=4"},
		{"fdc=13", "E474: Invalid argument: fdc=13"},
		{"fo^=qz", "E539: Illegal character <z>: fo^=qz"},
		{"go+=z", "E539: Illegal character <z>: go+=z"},
		{"mouse=q", "E539: Illegal character <q>: mouse=q"},
		{"cocu=x", "E539: Illegal character <x>: cocu=x"},
		{"bg=blue", "E474: Invalid argument: bg=blue"},
		{"sel=nosuch", "E474: Invalid argument: sel=nosuch"},
		{"swb=nosuch", "E474: Invalid argument: swb=nosuch"},
		{"cb=nosuch", "E474: Invalid argument: cb=nosuch"},
		{"bs=4", "E474: Invalid argument: bs=4"},
	} {
		o := Defaults()
		// sw-=6 needs a shiftwidth of 4 under it, which is the vimrc's.
		o.B.ShiftWidth, o.GB.ShiftWidth = 4, 4
		_, err := o.Apply(tc.arg, Both)
		if err == nil {
			t.Errorf(":set %s produced no error, vim says %q", tc.arg, tc.want)
			continue
		}
		if err.Error() != tc.want {
			t.Errorf(":set %s is %q, vim says %q", tc.arg, err, tc.want)
		}
	}
}

// TestNothingIsWrittenOnAnError. Vim assigns and then validates, so
// ":setglobal scrolloff=-1" leaves the global at 0 on its way to raising E487.
// This package checks first, because a half-written option is a bug that shows
// up three commands later with nothing pointing back here.
func TestNothingIsWrittenOnAnError(t *testing.T) {
	o := Defaults()
	before := o
	for _, bad := range []string{"ts=0", "ts=abc", "fo^=qz", "bg=blue", "ch=0"} {
		if _, err := o.Apply(bad, Both); err == nil {
			t.Errorf(":set %s was accepted", bad)
		}
	}
	if o != before {
		t.Error("a rejected :set changed an option anyway")
	}
}

// TestApplyLineStopsAtTheFirstError, and keeps what it applied before it, which
// is what vim does: ":set ic ts=abc sw=9" leaves 'ignorecase' on, 'shiftwidth'
// alone, and quotes the failing argument with the space that followed it.
func TestApplyLineStopsAtTheFirstError(t *testing.T) {
	o := Defaults()
	_, err := o.ApplyLine("ic ts=abc sw=9", Both)
	if err == nil {
		t.Fatal("no error from a line with ts=abc in it")
	}
	if want := "E521: Number required after =: ts=abc "; err.Error() != want {
		t.Errorf("error is %q, vim says %q", err, want)
	}
	if !o.G.IgnoreCase {
		t.Error("the arguments before the bad one were rolled back; vim keeps them")
	}
	if o.B.ShiftWidth != 8 {
		t.Errorf("the arguments after the bad one were applied: shiftwidth is %d", o.B.ShiftWidth)
	}
}

// TestShowPrintsTheLongName. Vim answers ":set ts?" with " tabstop=8", whatever
// name it was asked by, and a boolean that is off puts its "no" where the two
// spaces would be: "nowrap", not " nowrap".
func TestShowPrintsTheLongName(t *testing.T) {
	o := Defaults()
	for _, tc := range []struct{ arg, want string }{
		{"ts?", "  tabstop=8"},
		{"tabstop?", "  tabstop=8"},
		{"wrap?", "  wrap"},
		{"nowrap?", "  wrap"}, // the ? wins and the "no" is ignored
		{"ic?", "noignorecase"},
		{"so?", "  scrolloff=5"},
		{"ve?", "  virtualedit="},
		{"ts", "  tabstop=8"}, // a bare name shows a number
	} {
		got, err := o.Apply(tc.arg, Both)
		if err != nil {
			t.Fatalf(":set %s: %v", tc.arg, err)
		}
		if got != tc.want {
			t.Errorf(":set %s printed %q, vim prints %q", tc.arg, got, tc.want)
		}
	}

	// ":setlocal" does not resolve a global-local option, so a fresh window
	// prints the unset marker rather than the value it is following.
	got, err := o.Apply("so?", SetLocal)
	if err != nil {
		t.Fatal(err)
	}
	if got != "  scrolloff=-1" {
		t.Errorf(":setlocal so? printed %q, vim prints \"  scrolloff=-1\"", got)
	}
}

// TestResetAll is ":set all&", which puts every option back to vim's built-in
// default and not to the state this editor started in. Measured: after it,
// 'tabstop' is 8, 'scrolloff' is 0 and 'mouse' is empty, so the three things
// defaults.vim did are gone.
func TestResetAll(t *testing.T) {
	o := Defaults()
	if _, err := o.ApplyLine("ts=2 sw=4 hlsearch", Both); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Apply("all&", Both); err != nil {
		t.Fatal(err)
	}
	if o.B.TabStop != 8 || o.G.HlSearch {
		t.Errorf("after :set all& tabstop is %d and hlsearch is %v, want 8 and false", o.B.TabStop, o.G.HlSearch)
	}
	if o.G.ScrollOff != 0 || o.G.Mouse != "" {
		t.Errorf("after :set all& scrolloff is %d and mouse is %q; & is vim's default and not the one defaults.vim set", o.G.ScrollOff, o.G.Mouse)
	}
}

// TestPrefixAndSuffixTogether. ":set nowrap!" is a "no" prefix and a "!" suffix
// on one argument, and vim takes it: the prefix is dropped and the "!" toggles.
// Measured, because it looks like it ought to be an error.
func TestPrefixAndSuffixTogether(t *testing.T) {
	o := Defaults()
	if !o.W.Wrap {
		t.Fatal("this test wants 'wrap' on to start with")
	}
	if _, err := o.Apply("nowrap!", Both); err != nil {
		t.Fatalf("nowrap!: %v", err)
	}
	if o.W.Wrap {
		t.Error("nowrap! left 'wrap' on; vim toggles it off")
	}
}

// TestPathValueFallsBackToTheGlobal is 'path' as a global-local option.
//
// Measured on a fresh vim: ":set path?" and ":setglobal path?" both print
// ".,/usr/include," and ":setlocal path?" prints nothing after the name, so
// the local half starts unset and the value everything reads is the global
// one.
func TestPathValueFallsBackToTheGlobal(t *testing.T) {
	o := Defaults()
	if got := o.PathValue(); got != ".,/usr/include,," {
		t.Errorf("PathValue is %q, want vim's default", got)
	}
	if o.B.Path != "" {
		t.Errorf("the local half starts %q, want it unset", o.B.Path)
	}
	if _, err := o.ApplyLine("path=.,lib", SetLocal); err != nil {
		t.Fatal(err)
	}
	if got := o.PathValue(); got != ".,lib" {
		t.Errorf("after :setlocal, PathValue is %q, want the local value", got)
	}
	if got := o.G.Path; got != ".,/usr/include,," {
		t.Errorf(":setlocal wrote the global half too, which now says %q", got)
	}
}

// TestSplitPath is the comma grammar, with vim's three quirks in it: an empty
// item is the working directory and not a skipped entry, "." is the directory
// of the file being edited, and a backslash escapes a separator.
//
// The default is the row that matters. ".,/usr/include," is three entries and
// not two: an implementation that drops empty items never looks in the working
// directory, which is where gf finds almost everything it ever finds.
func TestSplitPath(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    []PathEntry
		refused []string
	}{
		{".,/usr/include,,", []PathEntry{{Here: true}, {Dir: "/usr/include"}, {}}, nil},
		{".", []PathEntry{{Here: true}}, nil},
		{"", []PathEntry{{}}, nil},
		{".,lib", []PathEntry{{Here: true}, {Dir: "lib"}}, nil},
		{"~/include", []PathEntry{{Dir: "~/include"}}, nil},
		{`a\,b`, []PathEntry{{Dir: "a,b"}}, nil},
		{`with\ space`, []PathEntry{{Dir: "with space"}}, nil},
		{".,**", []PathEntry{{Here: true}}, []string{"**"}},
		{"lib/**3", nil, []string{"lib/**3"}},
		{"$HOME/include", nil, []string{"$HOME/include"}},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, refused := SplitPath(tc.value)
			if len(got) != len(tc.want) {
				t.Fatalf("%d entries %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d is %+v, want %+v", i, got[i], tc.want[i])
				}
			}
			if len(refused) != len(tc.refused) {
				t.Fatalf("refused %v, want %v", refused, tc.refused)
			}
			for i := range refused {
				if refused[i] != tc.refused[i] {
					t.Errorf("refused %q, want %q", refused[i], tc.refused[i])
				}
			}
		})
	}
}
