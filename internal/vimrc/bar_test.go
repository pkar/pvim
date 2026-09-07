package vimrc

import "testing"

// runSrc loads one script into a fresh Config and fails the test on any
// diagnostic. Every test in this file is about a line vim accepts, so an
// error is always a bug and never the thing being measured.
func runSrc(t *testing.T, src string) *Config {
	t.Helper()
	cfg := NewConfig()
	res := Run([]byte(src), "x.vim", cfg, &DefaultEnv{Opts: cfg.Opts})
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	return cfg
}

// wantOpt asserts a number option, which options.Value prints with a leading
// "=".
func wantOpt(t *testing.T, cfg *Config, name, want, why string) {
	t.Helper()
	v, err := cfg.Opts.Get(name)
	if err != nil {
		t.Fatalf("&%s: %v", name, err)
	}
	if v.String() != want {
		t.Errorf("&%s = %q, want %q: %s", name, v.String(), want, why)
	}
}

// TestABarEndsAMapping.
//
// Vim's :map family carries EX_TRLBAR and EX_NOTRLCOM, which are two
// different things and were one flag here. The bar half is the one that was
// wrong. Measured against /opt/homebrew/bin/vim 9.2.0321 sourcing
//
//	set ts=2 sw=2
//	nnoremap <F2> :echo "a" | set sw=8
//
// maparg('<F2>','n') comes back as `:echo "a" ` -- trailing space, no bar,
// nothing after it -- and 'shiftwidth' is 8. Before this test the mapping got
// the whole line as its right-hand side and the :set never ran, and both
// halves of that failed silently.
func TestABarEndsAMapping(t *testing.T) {
	cfg := runSrc(t, "set ts=2 sw=2\n"+`nnoremap <F2> :echo "a" | set sw=8`+"\n")

	m, ok := findMap(cfg, "<F2>")
	if !ok {
		t.Fatalf("<F2> is not mapped: %+v", cfg.Maps)
	}
	if want := `:echo "a" `; m.RHS != want {
		t.Errorf("rhs = %q, want %q, which is vim's maparg('<F2>','n')", m.RHS, want)
	}
	wantOpt(t, cfg, "shiftwidth", "=8", "the command after the bar never ran")
}

// TestAnEscapedBarStaysInAMapping is the other half of the same rule, and the
// reason :help map-bar tells you to write "\|".
//
// Measured `nnoremap <F5> :echo "b" \| set sw=6` leaves
// maparg('<F5>','n') as `:echo "b" | set sw=6` -- the backslash gone, the bar
// kept -- and 'shiftwidth' at 2. The backslash comes off in vim's
// separate_nextcmd, before :map ever sees the line.
func TestAnEscapedBarStaysInAMapping(t *testing.T) {
	cfg := runSrc(t, "set sw=2\n"+`nnoremap <F5> :echo "b" \| set sw=6`+"\n")

	m, ok := findMap(cfg, "<F5>")
	if !ok {
		t.Fatalf("<F5> is not mapped: %+v", cfg.Maps)
	}
	if want := `:echo "b" | set sw=6`; m.RHS != want {
		t.Errorf("rhs = %q, want %q, which is vim's maparg('<F5>','n')", m.RHS, want)
	}
	wantOpt(t, cfg, "shiftwidth", "=2", "the escaped bar split the line")
}

// TestAQuoteDoesNotStartACommentInAMapping is EX_NOTRLCOM, the half of the
// old flag that was right, held here so that fixing the bar half did not
// take it with it.
//
// The vimrc's own `nnoremap <Space> za " Spacebar to unfold` maps to the
// comment and all, in vim and here. Measured.
func TestAQuoteDoesNotStartACommentInAMapping(t *testing.T) {
	cfg := runSrc(t, `nnoremap <F7> za " Spacebar to unfold`+"\n")

	m, ok := findMap(cfg, "<F7>")
	if !ok {
		t.Fatalf("<F7> is not mapped: %+v", cfg.Maps)
	}
	if want := `za " Spacebar to unfold`; m.RHS != want {
		t.Errorf("rhs = %q, want %q, which is vim's maparg('<F7>','n')", m.RHS, want)
	}
}

// TestAnUnmapTakesTheRestOfTheLine. :unmap has no EX_TRLBAR at all, so the
// bar is part of the name of the mapping it is looking for. Measured
// `nunmap <F3> | set sw=9` is E31 for a mapping called
// "<F3> | set sw=9" and leaves 'shiftwidth' alone.
func TestAnUnmapTakesTheRestOfTheLine(t *testing.T) {
	cfg := runSrc(t, "set sw=2\n"+`nunmap <F3> | set sw=9`+"\n")
	wantOpt(t, cfg, "shiftwidth", "=2", "the :unmap gave up the rest of its line")
}

// TestASetCommentSwallowsTheBar.
//
// :set has EX_TRLBAR and not EX_NOTRLCOM, so vim ends the command at the
// first unescaped double quote and everything after it, bars included, is
// comment. Measured `set ts=8 " a " | set sw=8` after
// `set ts=2 sw=2` gives &ts 8 and &sw 2. The splitter that thought a quote
// opened a string closed it at the second one and ran the :set after the bar.
func TestASetCommentSwallowsTheBar(t *testing.T) {
	cfg := runSrc(t, "set ts=2 sw=2\n"+`set ts=8 " a " | set sw=8`+"\n")

	wantOpt(t, cfg, "tabstop", "=8", "the option before the comment did not take")
	wantOpt(t, cfg, "shiftwidth", "=2", "a bar inside a comment ran the next command")
}

// TestASetBarRunsTheNextCommand is the half that has to keep working: a bar
// with no comment in front of it still separates two commands, and an
// escaped one ends up in the value with its backslash gone. Measured
// `set efm=%f\|%l | set ts=3` sets 'errorformat' to "%f|%l" and
// 'tabstop' to 3.
func TestASetBarRunsTheNextCommand(t *testing.T) {
	cfg := runSrc(t, "set ts=2 sw=2\nset ts=8 | set sw=8\n")

	wantOpt(t, cfg, "tabstop", "=8", "")
	wantOpt(t, cfg, "shiftwidth", "=8", "the command after the bar never ran")

	cfg = runSrc(t, "set ts=2\n"+`set efm=%f\|%l | set ts=3`+"\n")
	wantOpt(t, cfg, "errorformat", "=%f|%l", "the backslash before the bar stayed in the value")
	wantOpt(t, cfg, "tabstop", "=3", "the command after the bar never ran")
}

// TestALetCommentSwallowsTheBar.
//
// :let has no EX_TRLBAR: its own expression parser stops, and vim's
// ends_excmd2 then reads a double quote as the start of a comment and a bar
// as the start of the next command. A comment reaches the end of the line, so
// a bar inside one is text. Measured `let g:x = 1 " a " | set
// sw=9` after `set sw=2` leaves 'shiftwidth' at 2.
func TestALetCommentSwallowsTheBar(t *testing.T) {
	cfg := runSrc(t, "set sw=2\n"+`let g:x = 1 " a " | set sw=9`+"\n")
	wantOpt(t, cfg, "shiftwidth", "=2", "a bar inside a comment ran the next command")
}

// TestALetBarRunsTheNextCommand is the same two halves for :let, and the
// reason the split cannot be a byte scan: the bar inside the string is not a
// separator and the one after it is. Measured
// `let g:x = "a|b" | set sw=6` sets g:x to "a|b" and 'shiftwidth' to 6.
func TestALetBarRunsTheNextCommand(t *testing.T) {
	cfg := NewConfig()
	ld := NewLoader(cfg, &DefaultEnv{Opts: cfg.Opts})
	res := ld.Run([]byte("set sw=2\n"+`let g:x = "a|b" | set sw=6`+"\n"), "x.vim")
	for _, d := range res.Errors {
		t.Errorf("%v", d)
	}
	if got := ld.Vars["g:x"].String(); got != "a|b" {
		t.Errorf("g:x = %q, want %q", got, "a|b")
	}
	wantOpt(t, cfg, "shiftwidth", "=6", "the command after the bar never ran")
}

// TestAnIfCommentSwallowsTheBar. Same rule again, through the other caller of
// the expression parser. Measured with 'shiftwidth' at 2,
//
//	set sw=2
//	if 1 " a " | set sw=8
//	endif
//
// leaves it at 2. The condition is true, so this is not the branch being
// skipped: the :set after the bar is inside the comment and never was a
// command.
func TestAnIfCommentSwallowsTheBar(t *testing.T) {
	cfg := runSrc(t, "set sw=2\n"+`if 1 " a " | set sw=8`+"\nendif\n")
	wantOpt(t, cfg, "shiftwidth", "=2", "a bar inside a comment ran the next command")
}

// TestSeparateNextCmd is vim's function, unit by unit.
//
// Every row was measured by sourcing the corresponding command
// in /opt/homebrew/bin/vim 9.2.0321 and reading back what it set. The
// backslash rows are the ones that look wrong and are not: vim deletes ONE
// backslash in front of the character it stopped at and then walks past,
// without looking at what is now in front of it, which is why `\\|` comes out
// as `\|` and still does not split.
func TestSeparateNextCmd(t *testing.T) {
	for _, tc := range []struct {
		in         string
		comments   bool
		head, rest string
		found      bool
	}{
		{in: "ts=2", comments: true, head: "ts=2"},
		{in: "ts=2 | set sw=4", comments: true, head: "ts=2 ", rest: " set sw=4", found: true},
		// The comment ends the command and does not start another one.
		{in: `ts=8 " a " | set sw=8`, comments: true, head: "ts=8 "},
		{in: `t_vb= " Disable ALL bells"`, comments: true, head: "t_vb= "},
		// One backslash comes off and the character stays.
		{in: `efm=%f\|%l | set ts=3`, comments: true, head: "efm=%f|%l ", rest: " set ts=3", found: true},
		{in: `efm=%f\\|%l`, comments: true, head: `efm=%f\|%l`},
		{in: `x=\" y`, comments: true, head: `x=" y`},
		// EX_NOTRLCOM: the map family stops at a bar and not at a quote.
		{in: `<F2> :echo "a" | set sw=8`, head: `<F2> :echo "a" `, rest: " set sw=8", found: true},
		{in: `<F7> za " Spacebar to unfold`, head: `<F7> za " Spacebar to unfold`},
		{in: `<F5> :echo "b" \| set sw=6`, head: `<F5> :echo "b" | set sw=6`},
	} {
		head, rest, found := separateNextCmd(tc.in, tc.comments)
		if head != tc.head || rest != tc.rest || found != tc.found {
			t.Errorf("separateNextCmd(%q, %v) = %q, %q, %v; want %q, %q, %v",
				tc.in, tc.comments, head, rest, found, tc.head, tc.rest, tc.found)
		}
	}
}
