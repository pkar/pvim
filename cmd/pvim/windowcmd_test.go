package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/window"
)

// The CTRL-W family, in Go rather than in testdata/keys.
//
// testdata/keys is the better test and most of these keys have a case there.
// What it cannot see is the shape behind the answer: an oracle case compares
// the file, the registers and the message line, and two windows that share one
// buffer look exactly like one window in all three. Everything here is about
// the layout the file cannot show -- how many windows there are, which one is
// current, which buffer each holds, and what the tab list looks like
// afterwards.
//
// The paired oracle cases are named on each test, because these two halves
// have to be read together: if a test here passes and the case fails, this
// test is measuring the wrong thing.

// runKeys feeds a script to an editor and fails on anything the session
// refuses, which for this file means a key that is still refusing loudly.
func runKeys(t *testing.T, e *editor, keys string) {
	t.Helper()
	if err := e.sess.Run(keysOf(t, keys)); err != nil {
		t.Fatalf("%q: %v", keys, err)
	}
}

// windowsOf is the current tab's windows.
func windowsOf(e *editor) []*window.Window { return e.tabs.Current().Windows() }

// TestCtrlWSplitsAndTheNewWindowIsCurrent is CTRL-W s and CTRL-W S, with
// testdata/keys/ctrl_w_s_splits_and_closes and ctrl_w_shift_s_is_ctrl_w_s
// beside it.
//
// Three claims, and vim was asked about each: the new window is the one the
// cursor is in, it is the FIRST in screen order rather than the second, and it
// shows the same buffer. The screen order matters because CTRL-W j out of it
// has to reach the old one, which is what the oracle case leans on and what
// this can state directly.
func TestCtrlWSplitsAndTheNewWindowIsCurrent(t *testing.T) {
	for _, keys := range []string{"3G\x17s", "3G\x17S"} {
		t.Run(keys, func(t *testing.T) {
			e := newTestEditor(t, "aaaa\nbbbb\ncccc\ndddd\neeee\n")
			runKeys(t, e, keys)

			ws := windowsOf(e)
			if len(ws) != 2 {
				t.Fatalf("%d windows after %q, want 2", len(ws), keys)
			}
			tab := e.tabs.Current()
			if tab.Cur != ws[0] {
				t.Error("the window CTRL-W s made is not the first in screen order, or is not current")
			}
			if ws[0].Buf != ws[1].Buf {
				t.Error("CTRL-W s gave the new window a different buffer; it shares the old one")
			}
			if got := e.ed.Cursor().Line; got != 3 {
				t.Errorf("the new window is on line %d, want the 3 the cursor was on", got)
			}
		})
	}
}

// TestCtrlWVSplitsSideways is CTRL-W v, with
// testdata/keys/ctrl_w_v_splits_sideways beside it.
//
// The direction is the whole difference from CTRL-W s and neither the buffer
// nor the cursor shows it, so this asks the rectangles: two windows side by
// side share a row and not a column.
func TestCtrlWVSplitsSideways(t *testing.T) {
	e := newTestEditor(t, "aaaa\nbbbb\ncccc\n")
	runKeys(t, e, "\x17v")

	ws := windowsOf(e)
	if len(ws) != 2 {
		t.Fatalf("%d windows after CTRL-W v, want 2", len(ws))
	}
	if ws[0].Rect.Row != ws[1].Rect.Row {
		t.Errorf("CTRL-W v stacked the windows at rows %d and %d; a vertical split shares a row",
			ws[0].Rect.Row, ws[1].Rect.Row)
	}
	if ws[0].Rect.Col == ws[1].Rect.Col {
		t.Errorf("both windows start at column %d; a vertical split does not", ws[0].Rect.Col)
	}
}

// TestCtrlWSplitTakesACount is "5 CTRL-W s", with
// testdata/keys/ctrl_w_s_count_sizes_the_new_window beside it.
//
// Measured through cmd/oracle on the harness's 40-row screen: a bare CTRL-W s
// leaves the new window 19 rows and "5 CTRL-W s" leaves it 5.
func TestCtrlWSplitTakesACount(t *testing.T) {
	e := newTestEditor(t, strings.Repeat("line\n", 60))
	runKeys(t, e, "5\x17s")

	ws := windowsOf(e)
	if len(ws) != 2 {
		t.Fatalf("%d windows, want 2", len(ws))
	}
	if got := ws[0].View.Height; got != 5 {
		t.Errorf("the new window is %d rows, want the 5 the count asked for", got)
	}
}

// TestCtrlWCloseGivesBackTheOtherWindowsCursor is CTRL-W c, with
// testdata/keys/ctrl_w_s_splits_and_closes beside it.
//
// This is the one that was wrong first. internal/ex's Sync writes the editor's
// cursor into whichever window it leaves current, so ":close" handed the
// surviving window the dead one's cursor; measured on vim, "3G CTRL-W s 5G
// CTRL-W c" ends on line 3 and it ended here on 5.
func TestCtrlWCloseGivesBackTheOtherWindowsCursor(t *testing.T) {
	e := newTestEditor(t, "aaaa\nbbbb\ncccc\ndddd\neeee\nffff\n")
	runKeys(t, e, "3G\x17s5G\x17c")

	if got := len(windowsOf(e)); got != 1 {
		t.Fatalf("%d windows after CTRL-W c, want 1", got)
	}
	if got := e.ed.Cursor().Line; got != 3 {
		t.Errorf("cursor on line %d after the split closed, want 3", got)
	}
}

// TestCtrlWMovesBetweenWindowsWithTheirOwnCursors is CTRL-W j, k, w, W, t, b
// and p, with seven cases in testdata/keys behind them.
//
// One editor, one buffer, two windows on different lines: every one of these
// keys has to bring its window's own line with it, which is the thing the
// frontend owns because internal/mode has one cursor and a tab page has one
// per window.
func TestCtrlWMovesBetweenWindowsWithTheirOwnCursors(t *testing.T) {
	// After this the top window is on line 5 and is current, and the bottom
	// one is on line 3.
	const setup = "3G\x17s5G"
	cases := []struct {
		keys string
		want int
	}{
		{"\x17j", 3},
		{"\x17k", 5},
		{"\x17w", 3},
		{"\x17W", 3},
		{"\x17b", 3},
		{"\x17t", 5},
		{"\x17j\x17p", 5},
	}
	for _, c := range cases {
		t.Run(c.keys, func(t *testing.T) {
			e := newTestEditor(t, "aaaa\nbbbb\ncccc\ndddd\neeee\nffff\ngggg\n")
			runKeys(t, e, setup+c.keys)
			if got := e.ed.Cursor().Line; got != c.want {
				t.Errorf("cursor on line %d, want %d", got, c.want)
			}
		})
	}
}

// TestCtrlWNewOpensAnEmptyBuffer is CTRL-W n, with
// testdata/keys/ctrl_w_n_opens_an_empty_window beside it.
//
// The case can only show that the file came back unharmed. What it cannot show
// is the buffer in the new window, which is the whole of the difference
// between CTRL-W n and CTRL-W s.
func TestCtrlWNewOpensAnEmptyBuffer(t *testing.T) {
	e := newTestEditor(t, "aaaa\nbbbb\ncccc\n")
	runKeys(t, e, "\x17n")

	ws := windowsOf(e)
	if len(ws) != 2 {
		t.Fatalf("%d windows after CTRL-W n, want 2", len(ws))
	}
	if ws[0].Buf == ws[1].Buf {
		t.Fatal("CTRL-W n put the same buffer in both windows; it opens a new one")
	}
	if got := e.ed.Buffer().LineCount(); got != 1 {
		t.Errorf("the new buffer has %d lines, want the 1 empty one", got)
	}
	if got := string(e.ed.Buffer().Line(1)); got != "" {
		t.Errorf("the new buffer's first line is %q, want it empty", got)
	}
}

// TestCtrlWCaretWithoutAnAlternateFile is CTRL-W ^, with
// testdata/keys/ctrl_w_caret_has_no_alternate_file beside it.
//
// vim's exact wording, and the part the oracle case cannot see: no window is
// made. A split that had to be undone afterwards would leave the layout
// jittering on every mistyped CTRL-W ^.
func TestCtrlWCaretWithoutAnAlternateFile(t *testing.T) {
	e := newTestEditor(t, "aaaa\nbbbb\n")
	runKeys(t, e, "\x17^")

	if got := e.message(); got != "E23: No alternate file" {
		t.Errorf("message is %q, want %q", got, "E23: No alternate file")
	}
	if got := len(windowsOf(e)); got != 1 {
		t.Errorf("%d windows, want the 1 there was before the refusal", got)
	}
}

// TestCtrlWCaretSplitsTheAlternateFile is the other half of CTRL-W ^.
//
// There is no oracle case for it and there cannot be a useful one yet: the
// only way to give a headless run an alternate file is ":e", and pvim's ":e"
// does not print vim's `"name" [New]` line, so every such case fails on the
// ":e" before it reaches the CTRL-W. Reported to whoever owns internal/ex; in
// the meantime this is the measurement.
func TestCtrlWCaretSplitsTheAlternateFile(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(other, []byte("zzzz\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := newTestEditor(t, "aaaa\nbbbb\n")
	first := e.ed.Buffer()
	if err := e.ctx.Run("edit " + other); err != nil {
		t.Fatal(err)
	}
	runKeys(t, e, "\x17^")

	ws := windowsOf(e)
	if len(ws) != 2 {
		t.Fatalf("%d windows after CTRL-W ^, want 2", len(ws))
	}
	if ws[0].Buf != first {
		t.Error("the new window is not showing the alternate file")
	}
	if e.ed.Buffer() != first {
		t.Error("the editor is not on the alternate file the new window shows")
	}
}

// TestCtrlWShiftTMovesTheWindowToItsOwnTab is CTRL-W T, with
// testdata/keys/ctrl_w_shift_t_moves_window_to_a_tab and
// ctrl_w_shift_t_then_g_shift_t_returns beside it.
func TestCtrlWShiftTMovesTheWindowToItsOwnTab(t *testing.T) {
	e := newTestEditor(t, "aaaa\nbbbb\ncccc\ndddd\neeee\n")
	runKeys(t, e, "3G\x17s5G\x17T")

	if got := len(e.tabs.Pages); got != 2 {
		t.Fatalf("%d tab pages after CTRL-W T, want 2", got)
	}
	if e.tabs.Cur != 1 {
		t.Errorf("tab %d is current, want the new one at index 1", e.tabs.Cur)
	}
	if got := len(e.tabs.Pages[0].Windows()); got != 1 {
		t.Errorf("the tab left behind holds %d windows, want 1", got)
	}
	if got := len(e.tabs.Pages[1].Windows()); got != 1 {
		t.Errorf("the new tab holds %d windows, want 1", got)
	}
	if got := e.ed.Cursor().Line; got != 5 {
		t.Errorf("the moved window is on line %d, want the 5 it was on", got)
	}
}

// TestCtrlWShiftTOnOneWindowSaysSo is CTRL-W T with nothing to move, which is
// vim's "Already only one window" and not a new tab. Measured; the oracle case
// is testdata/keys/ctrl_w_z_closes_no_preview_window's neighbour in the same
// commit.
func TestCtrlWShiftTOnOneWindowSaysSo(t *testing.T) {
	e := newTestEditor(t, "aaaa\nbbbb\n")
	runKeys(t, e, "\x17T")

	if got := e.message(); got != "Already only one window" {
		t.Errorf("message is %q, want %q", got, "Already only one window")
	}
	if got := len(e.tabs.Pages); got != 1 {
		t.Errorf("%d tab pages, want 1: CTRL-W T made one anyway", got)
	}
}

// TestCtrlWGWalksTheTabs is CTRL-W g t and CTRL-W g T, with
// testdata/keys/ctrl_w_g_t_wraps_to_the_first_tab beside them.
//
// The g is a prefix and this is the test that says so: the key after it is a
// third keystroke and the count typed before the CTRL-W has to survive both.
func TestCtrlWGWalksTheTabs(t *testing.T) {
	cases := []struct {
		keys string
		want int
	}{
		{"\x17gt", 0},
		{"\x17gT", 0},
		{"\x17gt\x17gt", 1},
		{"1\x17gt", 0},
		{"2\x17gt", 1},
	}
	for _, c := range cases {
		t.Run(c.keys, func(t *testing.T) {
			e := newTestEditor(t, "aaaa\nbbbb\n")
			if err := e.ctx.Run("tabnew"); err != nil {
				t.Fatal(err)
			}
			runKeys(t, e, c.keys)
			if e.tabs.Cur != c.want {
				t.Errorf("tab %d is current, want %d", e.tabs.Cur, c.want)
			}
		})
	}
}

// TestCtrlWZAndUpperXChangeNothing is CTRL-W z and CTRL-W X, the two keys that
// were finished by being measured rather than by being written.
//
// CTRL-W z closes the preview window and there has never been one, so silence
// is the whole command. CTRL-W X is in no vim help file and vim beeps at it,
// which is what it does to every key after CTRL-W that is not a command; it
// was refused loudly here on the strength of looking like a cousin of
// CTRL-W x. Both oracle cases sit in testdata/keys.
func TestCtrlWZAndUpperXChangeNothing(t *testing.T) {
	for _, keys := range []string{"3G\x17s5G\x17z", "3G\x17s5G\x17X"} {
		t.Run(keys, func(t *testing.T) {
			e := newTestEditor(t, "aaaa\nbbbb\ncccc\ndddd\neeee\n")
			runKeys(t, e, keys)

			ws := windowsOf(e)
			if len(ws) != 2 {
				t.Fatalf("%d windows, want the 2 that were there", len(ws))
			}
			if e.tabs.Current().Cur != ws[0] {
				t.Error("the current window moved")
			}
			if got := e.ed.Cursor().Line; got != 5 {
				t.Errorf("cursor on line %d, want the 5 it was on", got)
			}
			if got := e.message(); got != "" {
				t.Errorf("message is %q, want nothing said", got)
			}
		})
	}
}

// TestCtrlWXExchangesTheWindows is the lowercase CTRL-W x, with
// testdata/keys/ctrl_w_x_exchanges_the_windows beside it.
//
// Measured through cmd/oracle with a 5-row window over a 32-row one: the sizes
// travel with the windows and the cursor ends up in whichever window landed in
// the earlier position, so the editor is looking at line 3 afterwards and not
// at the 5 it was on.
func TestCtrlWXExchangesTheWindows(t *testing.T) {
	e := newTestEditor(t, "aaaa\nbbbb\ncccc\ndddd\neeee\nffff\n")
	runKeys(t, e, "3G\x17s5G\x17x")

	if got := e.ed.Cursor().Line; got != 3 {
		t.Errorf("cursor on line %d after CTRL-W x, want 3", got)
	}
}

// TestCtrlWCountDoesNotLeakIntoTheNextCommand.
//
// Every CTRL-W command eats the count typed in front of it, whether it reads
// it or not. It did not: while all of these were no-ops "3 CTRL-W w x" left
// the 3 in the mode machine and deleted three characters where vim deletes
// one.
func TestCtrlWCountDoesNotLeakIntoTheNextCommand(t *testing.T) {
	e := newTestEditor(t, "abcdefgh\n")
	runKeys(t, e, "3\x17wx")

	if got := string(e.ed.Buffer().Line(1)); got != "bcdefgh" {
		t.Errorf("line is %q, want %q: the count in front of the CTRL-W reached the x", got, "bcdefgh")
	}
}

// The tag keys, which used to be the other half of the notes loud-key
// bullet and are now the six tests below.
//
// Every one of them has an oracle case beside it -- testdata/keys/ctrl_w_*
// and tag_* -- and the cases are the better test of what the keys do. What a
// case cannot see is the layout: two windows on one buffer write the same
// file, so "did it split" and "which window is the preview" are only visible
// from in here.

// tagFixture writes a tags file and a source file into a temp directory, makes
// it the working directory, and returns an editor over the source.
//
// The tags file is written to disk rather than typed into the buffer the way
// the oracle cases have to do it, because a Go test can just write the file.
func tagFixture(t *testing.T, tags, text string) *editor {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tags"), []byte(tags), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "buf.txt"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return newTestEditor(t, text)
}

// The fixture every tag test below shares: two tags, one of them twice, in the
// file the editor is looking at.
const (
	tagFile = "bar\tbuf.txt\t/^int bar/\nfoo\tbuf.txt\t/^int foo/\nfoo\tbuf.txt\t/^int foo2/\n"
	tagText = "int foo\nint foo2\nint bar\ncall foo here\n"
)

// TestCtrlWBracketSplitsOverTheTag is CTRL-W ], with
// testdata/keys/ctrl_w_bracket_splits_over_the_tag beside it.
func TestCtrlWBracketSplitsOverTheTag(t *testing.T) {
	e := tagFixture(t, tagFile, tagText)
	runKeys(t, e, "4G0w\x17]")

	if got := len(windowsOf(e)); got != 2 {
		t.Fatalf("CTRL-W ] left %d windows, want 2: %s", got, e.message())
	}
	if got := e.ed.Cursor().Line; got != 1 {
		t.Errorf("the new window is on line %d, want 1 (the first match)", got)
	}
	// The window it split from is still where it was, which is the whole
	// difference between a split and a jump.
	other := windowsOf(e)[1]
	if got := other.View.Cursor.Line; got != 4 {
		t.Errorf("the window below is on line %d, want 4", got)
	}
}

// TestCtrlWBracketCountSizesTheWindow: the count is the height and not the
// match, which is the one asymmetry against CTRL-].
func TestCtrlWBracketCountSizesTheWindow(t *testing.T) {
	e := tagFixture(t, tagFile, tagText)
	runKeys(t, e, "4G0w5\x17]")

	if got := e.sess.win().View.Height; got != 5 {
		t.Errorf("5 CTRL-W ] made a window %d rows high, want 5", got)
	}
	if got := e.ed.Cursor().Line; got != 1 {
		t.Errorf("5 CTRL-W ] landed on line %d, want 1: the count is the height, not the match", got)
	}
}

// TestCtrlWBraceFillsThePreviewWindow is CTRL-W }, CTRL-W P and CTRL-W z, all
// three of which need a preview window and none of which had one until
// internal/tags existed.
func TestCtrlWBraceFillsThePreviewWindow(t *testing.T) {
	e := tagFixture(t, tagFile, tagText)
	runKeys(t, e, "4G0w\x17}")

	if got := len(windowsOf(e)); got != 2 {
		t.Fatalf("CTRL-W } left %d windows, want 2: %s", got, e.message())
	}
	prev := e.sess.previewWindow()
	if prev == nil {
		t.Fatal("CTRL-W } made a window and did not mark it the preview window")
	}
	if prev == e.sess.win() {
		t.Error("CTRL-W } left the cursor in the preview window; vim goes back")
	}
	if got := prev.View.Height; got != previewHeight {
		t.Errorf("the preview window is %d rows, want 'previewheight' at %d", got, previewHeight)
	}
	if got := prev.View.Cursor.Line; got != 1 {
		t.Errorf("the preview window is on line %d, want 1", got)
	}
	if got := e.ed.Cursor().Line; got != 4 {
		t.Errorf("the cursor moved to line %d; CTRL-W } shows a tag and does not go to it", got)
	}

	runKeys(t, e, "\x17P")
	if e.sess.win() != prev {
		t.Error("CTRL-W P did not go to the preview window")
	}
	runKeys(t, e, "\x17z")
	if got := len(windowsOf(e)); got != 1 {
		t.Errorf("CTRL-W z left %d windows, want 1", got)
	}
	if e.sess.previewWindow() != nil {
		t.Error("CTRL-W z left a preview window behind")
	}
}

// TestCtrlWGBracketAsksAndSplits is CTRL-W g], the ":tselect" form: the list,
// the prompt, and a window only for a match that was chosen.
func TestCtrlWGBracketAsksAndSplits(t *testing.T) {
	e := tagFixture(t, tagFile, tagText)
	runKeys(t, e, "4G0w\x17g]2\r")

	if got := len(windowsOf(e)); got != 2 {
		t.Fatalf("CTRL-W g] answered 2 left %d windows, want 2: %s", got, e.message())
	}
	if got := e.ed.Cursor().Line; got != 2 {
		t.Errorf("CTRL-W g] answered 2 landed on line %d, want 2", got)
	}

	e = tagFixture(t, tagFile, tagText)
	runKeys(t, e, "4G0w\x17g]\r")
	if got := len(windowsOf(e)); got != 1 {
		t.Errorf("a cancelled CTRL-W g] left %d windows, want 1", got)
	}
}

// TestTagJumpToAnotherFile is the case cmd/oracle cannot grade and the one
// every real tags file is made of.
//
// Why it cannot: the harness dumps the state of the buffer it pointed pvim at,
// deliberately, so that a script ending in a ":!" scratch split still
// describes the file being graded. vim's trailer asks the CURRENT buffer. A
// case that changes buffers therefore reads as a difference in the harness
// whatever either editor did, which is why testdata/keys holds only same-file
// tag cases and this is here.
func TestTagJumpToAnotherFile(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"tags":      "foo\tother.txt\t/^int foo/\n",
		"other.txt": "aaa\nbbb\nint foo\n",
		"buf.txt":   "call foo here\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	e := newTestEditor(t, "call foo here\n")
	runKeys(t, e, "0w\x1d")

	if got := filepath.Base(e.file); got != "other.txt" {
		t.Fatalf("CTRL-] left the editor on %q, want other.txt: %s", got, e.message())
	}
	if got := e.ed.Cursor().Line; got != 3 {
		t.Errorf("the cursor is on line %d of other.txt, want 3", got)
	}
	// And back, which is the half that needs the file name on the stack.
	runKeys(t, e, "\x14")
	if got := filepath.Base(e.file); got != "buf.txt" {
		t.Fatalf("CTRL-T left the editor on %q, want buf.txt: %s", got, e.message())
	}
	if got := e.ed.Cursor(); got.Line != 1 || got.Col != 5 {
		t.Errorf("CTRL-T came back to %+v, want line 1 column 5", got)
	}
}

// TestTagStackIsPerWindow is the rule that decides what CTRL-T does after a
// window closes: the entry CTRL-W ] pushed went onto the new window's copy of
// the stack, so the window it split from has nothing to go back to.
func TestTagStackIsPerWindow(t *testing.T) {
	e := tagFixture(t, tagFile, tagText)
	runKeys(t, e, "4G0w\x17]\x17c\x14")

	if got := e.message(); got != "E555: At bottom of tag stack" {
		t.Errorf("CTRL-T in the window CTRL-W ] split from says %q, want E555", got)
	}
	if got := e.ed.Cursor().Line; got != 4 {
		t.Errorf("it moved the cursor to line %d as well", got)
	}
}

// TestCtrlWGTabGoesToTheLastAccessedTab is CTRL-W g<Tab>, which is three lines
// over internal/window's Tabs.Back and was refused loudly until Tabs kept a
// history rather than an index.
func TestCtrlWGTabGoesToTheLastAccessedTab(t *testing.T) {
	e := newTestEditor(t, "alpha\nbeta\n")
	runKeys(t, e, ":tabnew\r")
	if got := len(e.tabs.Pages); got != 2 {
		t.Fatalf("%d tab pages after :tabnew, want 2", got)
	}
	runKeys(t, e, "\x17g\t")
	if got := e.tabs.Cur; got != 0 {
		t.Fatalf("CTRL-W g<Tab> left tab %d current, want the first", got+1)
	}
	// And back again: the tab it left becomes the one to go back to.
	runKeys(t, e, "\x17g\t")
	if got := e.tabs.Cur; got != 1 {
		t.Errorf("a second CTRL-W g<Tab> left tab %d current, want the second", got+1)
	}
}

// TestCtrlWGTabWithOneTabSaysNothing: measured, vim neither moves nor
// complains.
func TestCtrlWGTabWithOneTabSaysNothing(t *testing.T) {
	e := newTestEditor(t, "alpha\nbeta\n")
	runKeys(t, e, "\x17g\t")
	if got := e.message(); got != "" {
		t.Errorf("CTRL-W g<Tab> with one tab said %q, want nothing", got)
	}
}

// TestWindowCommandsListsOnlyRealCommands guards the table the beep falls out
// of.
//
// Every letter in it has to be either answered in the switch above it or
// refused loudly, and every letter outside it has to be a key vim beeps at.
// X was in it and is not a vim command; this is the check that would have said
// so.
func TestWindowCommandsListsOnlyRealCommands(t *testing.T) {
	for _, c := range "X*.?\\\"" {
		if windowCommands(c) {
			t.Errorf("windowCommands says CTRL-W %q is a command; vim beeps at it", c)
		}
	}
	for _, c := range "sSvn^qcowWjkhltbpPrRxd+-<>=_|]}gfFizTHJKL" {
		if !windowCommands(c) {
			t.Errorf("windowCommands says CTRL-W %q is not a command; :help CTRL-W says it is", c)
		}
	}
}

// TestCtrlWMoveToAnEdge is CTRL-W H, J, K and L, with
// testdata/keys/ctrl_w_shift_h_moves_the_window_left and the three beside it.
//
// The oracle cases grade these the only way a keystroke case can -- move the
// window, walk back out of it with CTRL-W h or CTRL-W k, and delete a
// character in whichever window that reached -- so what they prove is that the
// window ended up on the far side of the one it left. This states the shape
// directly: the tree the move built, the sizes it came out with, and that the
// window that moved is still the one the cursor is in.
//
// The numbers are vim's, measured on the harness's 40-row 120-column screen.
// Two windows share 39 rows as 19 and 18 with two status lines, and the
// current window gets the odd one; two columns share 120 as 60 and 59 with the
// separator between them.
func TestCtrlWMoveToAnEdge(t *testing.T) {
	for _, tc := range []struct {
		keys   string
		height int
		width  int
		first  bool
	}{
		{"3G\x17s5G\x17K", 19, 120, true},
		{"3G\x17s5G\x17J", 19, 120, false},
		{"3G\x17s5G\x17H", 38, 60, true},
		{"3G\x17s5G\x17L", 38, 60, false},
	} {
		t.Run(tc.keys, func(t *testing.T) {
			e := newTestEditor(t, "aaaa\nbbbb\ncccc\ndddd\neeee\nffff\ngggg\nhhhh\n")
			runKeys(t, e, tc.keys)

			ws := windowsOf(e)
			if len(ws) != 2 {
				t.Fatalf("%d windows, want the 2 there were", len(ws))
			}
			tab := e.tabs.Current()
			moved := ws[0]
			if !tc.first {
				moved = ws[1]
			}
			if tab.Cur != moved {
				t.Errorf("the window that moved is not the current one; CTRL-W H and friends keep the cursor where it was")
			}
			if got := moved.View.Height; got != tc.height {
				t.Errorf("the moved window is %d rows, vim makes it %d", got, tc.height)
			}
			if got := moved.View.Width; got != tc.width {
				t.Errorf("the moved window is %d columns, vim makes it %d", got, tc.width)
			}
			if got := e.ed.Cursor().Line; got != 5 {
				t.Errorf("the cursor is on line %d, want the 5 it was on", got)
			}
		})
	}
}

// TestCtrlWMoveToOnOneWindowChangesNothing is the refusal, which is not one:
// vim leaves the layout alone and says nothing at all, so this has to be a
// silent no-op and not a beep and not an error.
// testdata/keys/ctrl_w_shift_l_on_one_window_changes_nothing is beside it.
func TestCtrlWMoveToOnOneWindowChangesNothing(t *testing.T) {
	for _, keys := range []string{"\x17H", "\x17J", "\x17K", "\x17L"} {
		t.Run(keys, func(t *testing.T) {
			e := newTestEditor(t, "aaaa\nbbbb\n")
			before := windowsOf(e)[0].View.Height
			runKeys(t, e, keys)
			ws := windowsOf(e)
			if len(ws) != 1 {
				t.Fatalf("%q made %d windows out of one", keys, len(ws))
			}
			if got := ws[0].View.Height; got != before {
				t.Errorf("%q resized the only window from %d to %d", keys, before, got)
			}
			if got := e.message(); got != "" {
				t.Errorf("%q said %q; vim says nothing", keys, got)
			}
		})
	}
}

// TestGotoFileOpensTheFileUnderTheCursor is gf, gF, CTRL-W f, CTRL-W F,
// CTRL-W gf and CTRL-W gF on a name that resolves.
//
// The oracle grades the refusals -- E446, E447, E347 and E37 all leave the
// buffer alone, which is what a keystroke case can see -- and cannot grade
// these: --oracle dumps the state of the buffer the run started on, and every
// one of these ends on a different one. So the successes are measured here.
//
// The line number is the half worth stating: gF, CTRL-W F and CTRL-W gF read
// the "2" of "other.txt:2" and put the cursor on line 2, and gf, CTRL-W f and
// CTRL-W gf ignore it. Measured against vim, which does the same.
func TestGotoFileOpensTheFileUnderTheCursor(t *testing.T) {
	for _, tc := range []struct {
		keys    string
		line    int
		windows int
		tabs    int
	}{
		{"gf", 1, 1, 1},
		{"gF", 2, 1, 1},
		{"\x17f", 1, 2, 1},
		{"\x17F", 2, 2, 1},
		{"\x17gf", 1, 1, 2},
		{"\x17gF", 2, 1, 2},
	} {
		t.Run(tc.keys, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)

			e := newTestEditor(t, "other.txt:2\n")
			runKeys(t, e, tc.keys)

			if got := filepath.Base(e.file); got != "other.txt" {
				t.Fatalf("%q left the editor on %q, want other.txt: %s", tc.keys, got, e.message())
			}
			if got := e.ed.Cursor().Line; got != tc.line {
				t.Errorf("%q left the cursor on line %d, want %d", tc.keys, got, tc.line)
			}
			if got := len(windowsOf(e)); got != tc.windows {
				t.Errorf("%q left %d windows, want %d", tc.keys, got, tc.windows)
			}
			if got := len(e.tabs.Pages); got != tc.tabs {
				t.Errorf("%q left %d tab pages, want %d", tc.keys, got, tc.tabs)
			}
		})
	}
}

// TestGotoFileFindsTheNameThroughPath is the 'path' walk itself: a file that
// is not in the working directory and is in a directory 'path' names.
//
// It is the one thing no oracle case reaches, because a case has one input
// file and one directory, and it is the whole reason 'path' exists.
func TestGotoFileFindsTheNameThroughPath(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "deep.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	e := newTestEditor(t, "deep.txt\n")
	runKeys(t, e, "gf")
	if got := e.message(); got != `E447: Can't find file "deep.txt" in path` {
		t.Fatalf("with the default 'path' the message is %q, want E447", got)
	}

	if _, err := e.opt.ApplyLine("path=.,lib", options.Both); err != nil {
		t.Fatal(err)
	}
	runKeys(t, e, "gf")
	if got := filepath.Base(e.file); got != "deep.txt" {
		t.Fatalf("with 'path' naming lib the editor is on %q, want deep.txt: %s", got, e.message())
	}
}

// TestGotoFileRefusesAPathItCannotWalk is the loud half of 'path'.
//
// vim's "**" walks a directory tree and this does not, so a 'path' holding one
// stops the key with the item quoted rather than searching the entries it
// understood and reporting the file missing. Neither oracle profile sets
// 'path', so nothing grades this and nothing can: it is a refusal vim does not
// have.
func TestGotoFileRefusesAPathItCannotWalk(t *testing.T) {
	e := newTestEditor(t, "deep.txt\n")
	if _, err := e.opt.ApplyLine("path=.,lib/**", options.Both); err != nil {
		t.Fatal(err)
	}
	err := e.sess.Run(keysOf(t, "gf"))
	if !errors.Is(err, errPathItem) {
		t.Fatalf("gf under 'path' holding \"lib/**\" returned %v, want errPathItem", err)
	}
	if !strings.Contains(err.Error(), "lib/**") {
		t.Errorf("the refusal is %q and does not name the item it could not walk", err)
	}
}

// TestFileNameAtCursor is the name and the line number, taken apart.
//
// Every row is vim's, measured through gf and gF on the harness's pty. The
// four separators are the four :help gF names, and the two that look like
// mistakes are not: "see" is what vim reads when the cursor is on the word
// before the name, and a trailing full stop comes off because vim strips
// ".,:;!" from the end of a file name.
func TestFileNameAtCursor(t *testing.T) {
	for _, tc := range []struct {
		line string
		col  int
		name string
		lnum int
	}{
		{"other.txt", 0, "other.txt", 0},
		{"other.txt:2", 0, "other.txt", 2},
		{"other.txt @ 20", 0, "other.txt", 20},
		{"other.txt (30)", 0, "other.txt", 30},
		{"other.txt 40", 0, "other.txt", 40},
		{"other.txt line 12", 0, "other.txt", 12},
		{"see other.txt now", 0, "see", 0},
		{"see other.txt now", 4, "other.txt", 0},
		{"see other.txt.", 4, "other.txt", 0},
		{"  other.txt", 0, "other.txt", 0},
		{"", 0, "", 0},
		{"   ", 0, "", 0},
		{"sub/deep.txt", 0, "sub/deep.txt", 0},
	} {
		t.Run(tc.line+"@"+strconv.Itoa(tc.col), func(t *testing.T) {
			name, lnum := fileNameAtCursor(tc.line, tc.col)
			if name != tc.name || lnum != tc.lnum {
				t.Errorf("got %q and line %d, vim reads %q and line %d", name, lnum, tc.name, tc.lnum)
			}
		})
	}
}
