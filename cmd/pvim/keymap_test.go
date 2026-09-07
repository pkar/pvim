package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkar/pvim/internal/key"
	"github.com/pkar/pvim/internal/keymap"
	"github.com/pkar/pvim/internal/register"
)

// The six mappings in the vimrc this editor exists to run, fired.
//
// Fired, and not looked up. internal/vimrc's own tests already prove the six
// lines parse and vimrc_gate_test.go already proves they reach the editor's map
// list; what neither of them can say is whether pressing the key does anything,
// which for a long time it did not. Every test here types the left-hand side
// into a whole editor with the real vimrc loaded and reads the buffer, the
// message line or a register afterwards.
//
// has('gui_running') is false throughout, because three of the six are inside
// the vimrc's "if !has('gui_running')" and that is the frontend this editor is
// used from.

// mapEditor is a gate editor over some text, with a session that can take keys.
func mapEditor(t *testing.T, text string) *editor {
	t.Helper()
	e := newTestEditor(t, text)
	e.loadVimrc(realVimrc, false)
	if e.sess.maps == nil {
		t.Fatal("the vimrc loaded and no mapping reached the session")
	}
	return e
}

// typeMapped feeds vim notation through the session the way a frontend does.
//
// Through the session and not through editor.key, which persistwire_test.go's
// typeKeys uses: the mapping layer is in session.Key and a test that went round
// it would be testing nothing at all.
func typeMapped(t *testing.T, e *editor, notation string) {
	t.Helper()
	keys, err := key.Parse(notation, ",")
	if err != nil {
		t.Fatalf("parsing %q: %v", notation, err)
	}
	// One key at a time through session.Key, which is what both frontends do
	// with an event. session.Run is the script path and it ends by resolving a
	// half-typed mapping the way a timeout would; a test that went through it
	// could never see the machine waiting.
	for _, k := range keys {
		if err := e.sess.Key(k); err != nil {
			t.Fatalf("typing %q: %v", notation, err)
		}
	}
}

// lastMessage is the message line after a command.
func lastMessage(e *editor) string {
	log := e.ed.Messages()
	if len(log) == 0 {
		return ""
	}
	return log[len(log)-1]
}

// TestVimrcMapsBraceToTabPrevious is "map { gT" and "map } gt", the first two
// of the six, fired in normal mode over four tab pages.
//
// The measurement it is graded against: vim --clean with those two lines, four
// tab pages open and the cursor on the fourth, answers tabpagenr() 3 after "{"
// and 2 after "2{". The count is not this layer's doing and that is the point
// of measuring it -- a digit is not a mapping, so it reaches the mode machine
// on its own and is still there when the g and the T arrive.
func TestVimrcMapsBraceToTabPrevious(t *testing.T) {
	e := mapEditor(t, "one\n")
	for i := 0; i < 3; i++ {
		if err := e.ctx.RunLine("tabnew"); err != nil {
			t.Fatalf("tabnew: %v", err)
		}
	}
	if got := e.tabs.Cur; got != 3 {
		t.Fatalf("after three :tabnew the current tab index is %d, want 3", got)
	}

	typeMapped(t, e, "{")
	if got := e.tabs.Cur; got != 2 {
		t.Errorf("{ left the editor on tab %d, want 2: the mapping to gT did not fire", got)
	}
	typeMapped(t, e, "}")
	if got := e.tabs.Cur; got != 3 {
		t.Errorf("} left the editor on tab %d, want 3", got)
	}
	typeMapped(t, e, "2{")
	if got := e.tabs.Cur; got != 1 {
		t.Errorf("2{ left the editor on tab %d, want 1: the count belongs to the gT", got)
	}
}

// TestVimrcBraceMapIsOperatorPendingToo is the half of "map { gT" that reads
// like a bug and is faithfully reproduced: a bare :map is normal, visual AND
// operator-pending, so "d}" under this vimrc means delete to the next tab page,
// which is not a motion, so it deletes nothing.
//
// Measured: vim --clean with the two map lines, "d}" on "alpha beta gamma"
// leaves the line exactly as it was.
func TestVimrcBraceMapIsOperatorPendingToo(t *testing.T) {
	e := mapEditor(t, "alpha beta gamma\ndelta epsilon\nzeta eta\n")
	typeMapped(t, e, "d}")
	if got := string(e.buf.Line(1)); got != "alpha beta gamma" {
		t.Errorf("d} left line 1 as %q, want it untouched: } is mapped in operator-pending", got)
	}
	if got := e.buf.LineCount(); got != 3 {
		t.Errorf("d} left %d lines, want 3", got)
	}
}

// TestVimrcMapsSpaceToFoldToggle is "nnoremap <Space> za " Spacebar to unfold",
// all twenty-four characters of it, because :map has no comment syntax.
//
// Two things are graded. The za fires, which on a buffer with no folds is
// "E490: No fold found" -- pvim's own message for the fold commands this
// editor leaves unwritten, and vim's for the same keys, measured. And the
// twenty-one characters behind the za do NOT run: vim throws the rest of a mapping away
// when one of its keys errors, so the file comes back byte-identical, where an
// editor that kept going would have hit the quote as a register prefix and the
// S as a substitute and lost the line.
func TestVimrcMapsSpaceToFoldToggle(t *testing.T) {
	e := mapEditor(t, "alpha beta gamma\ndelta epsilon\n")
	typeMapped(t, e, "<Space>")

	if want := "E490: No fold found"; lastMessage(e) != want {
		t.Errorf("<Space> says %q, want %q: the za did not fire", lastMessage(e), want)
	}
	if got := string(e.buf.Line(1)); got != "alpha beta gamma" {
		t.Errorf("<Space> left line 1 as %q; the comment on the right-hand side got typed into the buffer", got)
	}
	if got := e.buf.LineCount(); got != 2 {
		t.Errorf("<Space> left %d lines, want 2", got)
	}
}

// TestVimrcMapsLeaderCommaToTheTree is "nnoremap <leader>, :NERDTreeToggle<CR>"
// with 'mapleader' set to a comma on line 58, so the left-hand side is two
// commas and the right-hand side is a command pvim does not have.
//
// The rule this proves: a mapping's right-hand side is not
// looked at when the mapping is made. The vimrc loads with nothing on the
// message line, and E492 arrives when, and only when, the keys are pressed.
// That is what vim does with a plugin that failed to install, measured:
// "E492: Not an editor command: NERDTreeToggle", the same string on both sides.
func TestVimrcMapsLeaderCommaToTheTree(t *testing.T) {
	e := mapEditor(t, "one\n")
	if msgs := e.ed.Messages(); len(msgs) != 0 {
		t.Fatalf("the vimrc said %q on the way in; the tree mapping must be silent until it is pressed", msgs)
	}

	typeMapped(t, e, ",,")
	if want := "E492: Not an editor command: NERDTreeToggle"; lastMessage(e) != want {
		t.Errorf(",, says %q, want %q", lastMessage(e), want)
	}
}

// TestVimrcLeaderCommaWaitsForTheSecondComma is the ambiguity 'notimeout' is
// set for, seen from the outside: one comma is a complete key in vim -- it
// repeats the last f backwards -- and it is also half of the tree mapping, and
// under this vimrc's "set notimeout" the editor waits for the second comma
// however long it takes.
//
// The wait is what is graded, not a clock: nothing here sleeps, and the machine
// resolves nothing until the key that decides arrives. Under 'notimeout' the
// frontend never runs a timer at all, so this is the whole of the behaviour.
func TestVimrcLeaderCommaWaitsForTheSecondComma(t *testing.T) {
	e := mapEditor(t, "one\n")
	if e.opt.G.Timeout {
		t.Fatal("the vimrc's 'notimeout' did not reach the options")
	}
	if _, ok := e.mapTimeout(); ok {
		t.Error("mapTimeout offers a mapping delay under 'notimeout'")
	}

	typeMapped(t, e, ",")
	if !e.sess.MapPending() {
		t.Fatal("one comma resolved on its own; it is half of the <leader>, mapping and has to wait")
	}
	if msgs := e.ed.Messages(); len(msgs) != 0 {
		t.Errorf("one comma said %q, want silence while it waits", msgs)
	}

	typeMapped(t, e, ",")
	if e.sess.MapPending() {
		t.Error("the second comma left the machine waiting")
	}
	if want := "E492: Not an editor command: NERDTreeToggle"; lastMessage(e) != want {
		t.Errorf("the pair says %q, want %q", lastMessage(e), want)
	}
}

// TestVimrcCommaThenSomethingElseIsTheOldComma is the other side of that
// ambiguity: a comma followed by anything that is not a comma is the ordinary
// comma command, and the key that ended the wait is executed behind it.
//
// Measured shape: with `ab` and `abc` both mapped, "abx" runs the ab mapping
// and then the x. Here the mapping is "," and there is nothing on a bare ",",
// so the comma reaches the mode machine, finds no f to repeat, and beeps in
// silence -- and the j behind it still moves a line, which is the thing being
// checked.
func TestVimrcCommaThenSomethingElseIsTheOldComma(t *testing.T) {
	e := mapEditor(t, "one\ntwo\nthree\n")
	typeMapped(t, e, ",j")
	if e.sess.MapPending() {
		t.Error("the machine is still waiting after a comma and a j")
	}
	if got := e.ed.Cursor().Line; got != 2 {
		t.Errorf("the j behind the comma left the cursor on line %d, want 2", got)
	}
	if msgs := e.ed.Messages(); len(msgs) != 0 {
		t.Errorf("a lone comma said %q, want silence", msgs)
	}
}

// TestVimrcClipboardMapsAreTerminalOnly is the last three of the six:
// "vnoremap <C-c> \"+y", "vnoremap <C-x> \"+d" and "inoremap <C-v>
// <C-r><C-o>+", all inside "if has('clipboard') | if !has('gui_running')".
//
// Terminal-only is not a detail of where they are written, it is the whole
// reason the loader is told which frontend it is loading for: in the window
// the OS delivers Cmd-C and these three keys keep their vim meanings, and in
// the terminal they are the clipboard. So the same file is loaded twice here
// and the mappings have to be there once and absent once.
func TestVimrcClipboardMapsAreTerminalOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		gui  bool
		want bool
	}{
		{"terminal", false, true},
		{"window", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEditor(t, "one\n")
			e.loadVimrc(realVimrc, tc.gui)
			if e.sess.keys == nil {
				t.Fatal("no map table at all")
			}
			for _, m := range []struct {
				mode keymap.Mode
				lhs  string
			}{
				{keymap.Visual, "<C-c>"},
				{keymap.Visual, "<C-x>"},
				{keymap.Visual, "<C-v>"},
				{keymap.Insert, "<C-v>"},
			} {
				lhs, err := key.Parse(m.lhs, ",")
				if err != nil {
					t.Fatal(err)
				}
				_, ok := e.sess.keys.Get(m.mode, lhs, e.mapBuf())
				if ok != tc.want {
					t.Errorf("%v %s mapped=%v, want %v", m.mode, m.lhs, ok, tc.want)
				}
			}
		})
	}
}

// TestVimrcVisualCtrlCYanksToPlus fires "vnoremap <C-c> \"+y" and reads the
// register back.
//
// A File with no Clipboard installed keeps "+ in memory, which is what a test
// wants and what pvim does over SSH: internal/register's own doc says the
// clipboard is injected and the register works without it. So the assertion is
// that the plus register holds the selection, which it can only do if the CTRL-C
// became a quote, a plus and a y.
func TestVimrcVisualCtrlCYanksToPlus(t *testing.T) {
	e := mapEditor(t, "alpha beta\n")
	typeMapped(t, e, "vll<C-c>")
	v, err := e.ed.Registers().Get('+')
	if err != nil {
		t.Fatalf("reading the + register: %v", err)
	}
	if got := string(v.Bytes()); got != "alp" {
		t.Errorf("the + register holds %q after v l l CTRL-C, want %q", got, "alp")
	}
	if got := string(e.buf.Line(1)); got != "alpha beta" {
		t.Errorf("the yank changed the line to %q", got)
	}
}

// TestVimrcVisualCtrlXCutsToPlus fires "vnoremap <C-x> \"+d".
//
// CTRL-X in normal mode is decrement, so this also proves the mapping is in
// visual mode and only there: the same key outside a selection has to still be
// the number command, which the second half checks by leaving the number alone.
func TestVimrcVisualCtrlXCutsToPlus(t *testing.T) {
	e := mapEditor(t, "alpha beta\n")
	typeMapped(t, e, "vll<C-x>")
	v, err := e.ed.Registers().Get('+')
	if err != nil {
		t.Fatalf("reading the + register: %v", err)
	}
	if got := string(v.Bytes()); got != "alp" {
		t.Errorf("the + register holds %q after v l l CTRL-X, want %q", got, "alp")
	}
	if got := string(e.buf.Line(1)); got != "ha beta" {
		t.Errorf("CTRL-X left the line as %q, want %q", got, "ha beta")
	}

	e2 := mapEditor(t, "7\n")
	typeMapped(t, e2, "<C-x>")
	if got := string(e2.buf.Line(1)); got != "6" {
		t.Errorf("normal-mode CTRL-X left %q, want %q: the mapping is visual only", got, "6")
	}
}

// TestVimrcInsertCtrlVPastesPlus fires "inoremap <C-v> <C-r><C-o>+", which is
// the one of the six whose right-hand side is nothing but keys with no ex
// command behind them.
//
// Measured in vim: with @+ set to "XYZ", "i CTRL-V Esc" on "a{b{c" leaves
// "XYZa{b{c". CTRL-R CTRL-O is the literal form of CTRL-R, which internal/mode
// has not written -- it reads the CTRL-O as a register name -- so the assertion
// here is that the mapping fired and reached the register machinery, and the
// exact text is registered as a gap in the report rather than asserted wrongly.
func TestVimrcInsertCtrlVPastesPlus(t *testing.T) {
	e := mapEditor(t, "abc\n")
	if err := e.ed.Registers().Set('+', register.Char([]byte("XYZ"))); err != nil {
		t.Fatalf("setting the + register: %v", err)
	}
	typeMapped(t, e, "i<C-v>")
	// The mapping fired if the CTRL-V did not reach insert mode as itself:
	// pvim's insert CTRL-V is the literal-next-character command, which would
	// be sitting waiting for a key.
	if e.ed.Waiting() {
		t.Error("insert CTRL-V is still waiting for a key; the mapping did not fire")
	}
	typeMapped(t, e, "<Esc>")
	if strings.Contains(string(e.buf.Line(1)), "\x16") {
		t.Errorf("a literal CTRL-V landed in the buffer: %q", e.buf.Line(1))
	}
}

// TestNoVimrcIsNoMapping is the promise --oracle depends on: an editor that
// read no vimrc has a nil resolver and the dispatch this file did not touch.
//
// It matters because cmd/oracle grades 722 cases through exactly that editor.
// If a mapping layer could be reached with no mappings in it, every one of
// those cases would be running new code.
func TestNoVimrcIsNoMapping(t *testing.T) {
	e := newTestEditor(t, "alpha\n")
	if e.sess.maps != nil {
		t.Fatal("an editor with no vimrc has a mapping resolver")
	}
	if e.sess.keys != nil {
		t.Fatal("an editor with no vimrc has a map table")
	}
	typeMapped(t, e, "x")
	if got := string(e.buf.Line(1)); got != "lpha" {
		t.Errorf("x with no vimrc gives %q, want %q", got, "lpha")
	}
}

// TestMapTimeoutFollowsTheOptionAndNotTheOtherOne is the two timeouts, kept
// apart.
//
// The vimrc's terminal branch sets 'notimeout' with 'ttimeout' and
// 'ttimeoutlen=10'. Those are two different timers and conflating them is the
// classic bug: a mapping must never fire on its own, and a bare Escape must be
// told from <Esc>[A in ten milliseconds. The mapping half is this; the key-code
// half is internal/tui's keyCodeTimeout and has its own test there.
func TestMapTimeoutFollowsTheOptionAndNotTheOtherOne(t *testing.T) {
	term := newTestEditor(t, "one\n")
	term.loadVimrc(realVimrc, false)
	if term.opt.G.TTimeoutLen != 10 {
		t.Errorf("the terminal's 'ttimeoutlen' is %d, want 10", term.opt.G.TTimeoutLen)
	}
	if d, ok := term.mapTimeout(); ok {
		t.Errorf("the terminal offers a mapping delay of %v; 'notimeout' says there is none", d)
	}

	win := newTestEditor(t, "one\n")
	win.loadVimrc(realVimrc, true)
	d, ok := win.mapTimeout()
	if !ok {
		t.Fatal("the window has no mapping delay; nothing in the vimrc's gui branch turns 'timeout' off")
	}
	if want := 1000 * time.Millisecond; d != want {
		t.Errorf("the window's mapping delay is %v, want %v", d, want)
	}
}

// TestScriptEndResolvesAHalfTypedMapping is the one place the clock is not the
// frontend's.
//
// Vim, fed the first half of an ambiguous mapping out of a -s file, waits for a
// keyboard that is not there and never exits: measured under 'timeout' and
// 'notimeout' alike, because a script running out is not a timeout. pvim cannot
// hang, so session.Run resolves what is held on its way out, the way a timeout
// would. Nothing graded reaches it -- --oracle reads no vimrc -- and this test
// is what says so out loud.
func TestScriptEndResolvesAHalfTypedMapping(t *testing.T) {
	e := mapEditor(t, "one\n")
	keys, err := key.Parse(",", ",")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.sess.Run(keys); err != nil {
		t.Fatalf("running a lone comma: %v", err)
	}
	if e.sess.MapPending() {
		t.Error("the script ended with a mapping still half typed")
	}
}

// TestRecursiveMappingSaysE223 puts vim's own answer on the message line and
// clears the typeahead, and it does it through the session rather than through
// internal/keymap so that the message really reaches the line a person reads.
//
// Measured: `nmap a b` plus `nmap b a`, then "a", says "E223: Recursive
// mapping" and throws the half-typed command away.
func TestRecursiveMappingSaysE223(t *testing.T) {
	e := mapEditor(t, "alpha\n")
	for _, pair := range [][2]string{{"<F5>", "<F6>"}, {"<F6>", "<F5>"}} {
		lhs, err := key.Parse(pair[0], ",")
		if err != nil {
			t.Fatal(err)
		}
		rhs, err := key.Parse(pair[1], ",")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.sess.keys.Set(keymap.Mapping{
			Modes: keymap.Normal, LHS: lhs, RHS: rhs,
			LHSText: pair[0], RHSText: pair[1],
		}); err != nil {
			t.Fatal(err)
		}
	}
	typeMapped(t, e, "<F5>")
	if want := "E223: Recursive mapping"; lastMessage(e) != want {
		t.Errorf("a mutual recursion says %q, want %q", lastMessage(e), want)
	}
	if e.sess.MapPending() {
		t.Error("the typeahead survived E223")
	}
	if got := string(e.buf.Line(1)); got != "alpha" {
		t.Errorf("the recursion changed the buffer to %q", got)
	}
}

// TestExprMappingIsRefusedOnTheMessageLine: a vimrc with an <expr> mapping in
// it loads, says why the mapping is not there, and carries on. Refused when the
// mapping is made and not when it is pressed, because there is no expression
// evaluator and there is not going to be one.
func TestExprMappingIsRefusedOnTheMessageLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vimrc")
	if err := os.WriteFile(path, []byte("nnoremap <expr> gx Foo()\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newTestEditor(t, "one\n")
	e.loadVimrc(path, false)

	msgs := e.ed.Messages()
	// The loader puts the file and line number in front of every vimrc error,
	// which is what vim does and what makes a bad line findable.
	if len(msgs) != 1 || !strings.Contains(msgs[0], "E1821: <expr> mapping is not supported: gx") {
		t.Fatalf("loading an <expr> mapping said %q, want one E1821", msgs)
	}
	lhs, err := key.Parse("gx", ",")
	if err != nil {
		t.Fatal(err)
	}
	if e.sess.keys != nil {
		if _, ok := e.sess.keys.Get(keymap.Normal, lhs, 0); ok {
			t.Error("the refused mapping went into the table anyway")
		}
	}
}

// TestBufferLocalMappingFromAVimrc is <buffer>, which the vimrc uses once, on
// the line inside "au filetype go" that nothing fires yet. The argument still
// has to parse and land in the buffer's own trie rather than the global one.
func TestBufferLocalMappingFromAVimrc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vimrc")
	if err := os.WriteFile(path, []byte("inoremap <buffer> . .!\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newTestEditor(t, "one\n")
	e.loadVimrc(path, false)
	if msgs := e.ed.Messages(); len(msgs) != 0 {
		t.Fatalf("a <buffer> mapping said %q on the way in", msgs)
	}
	lhs, err := key.Parse(".", ",")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.sess.keys.Get(keymap.Insert, lhs, e.mapBuf()); !ok {
		t.Fatal("the <buffer> mapping is not in this buffer's table")
	}
	if _, ok := e.sess.keys.Get(keymap.Insert, lhs, e.mapBuf()+1); ok {
		t.Error("the <buffer> mapping is in another buffer's table too")
	}

	typeMapped(t, e, "ia.<Esc>")
	if got := string(e.buf.Line(1)); got != "a.!one" {
		t.Errorf("typing a dot in insert gives %q, want %q", got, "a.!one")
	}
}

// TestClearPrefixesDropsTheHeldG is the contract cmd/pvim/mouse.go's
// TestAClickThrowsAwayAHalfTypedPrefix names and cannot reach: a click throws
// away a half-typed command, internal/mode does that for the prefixes it holds
// itself, and the four this file holds -- z, CTRL-W, Z and the g of gt -- need
// a call to clear them.
//
// Measured in vim: "g", a click, then "x" deletes one character. Without the
// call the g is still armed and the x becomes a "gx", which is a beep and a
// character still there.
func TestClearPrefixesDropsTheHeldG(t *testing.T) {
	e := newTestEditor(t, "alpha\n")
	typeMapped(t, e, "g")
	if !e.sess.pendingG {
		t.Fatal("a bare g was not held")
	}
	e.sess.clearPrefixes()
	typeMapped(t, e, "x")
	if got := string(e.buf.Line(1)); got != "lpha" {
		t.Errorf("g, clearPrefixes, x gives %q, want %q", got, "lpha")
	}
}
