package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/key"
)

// The window a headless run looks through, and what the scrolling keys do to
// it.
//
// Every number in this file came out of /opt/homebrew/bin/vim 9.2.0321 through
// cmd/oracle, by putting
//
//	:echo line("w0") line("w$") line(".")
//
// at the end of a keys file and reading the reference side's msgs.txt. None of
// it is reasoned about, because reasoning about vim's scrolling is how the
// last fuzz run ended up with 211 differences: CTRL-D stops when the last line
// is on screen and CTRL-F does not, zt honours 'scrolloff' and zz does not,
// and no two of those rules follow from each other.

// keysOf decodes a key string the way a keys file is decoded, so that a test
// case reads the same as the case in testdata/keys that covers it.
func keysOf(t *testing.T, s string) []key.Key {
	t.Helper()
	return scriptKeys([]byte(s))
}

// numbered builds the corpus file the measurements were taken on: n lines of
// "line NN aaa bbb".
func numbered(n int) []byte {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %02d aaa bbb\n", i)
	}
	return []byte(b.String())
}

// TestScrollAgainstVim drives the session with the same keys the oracle sends
// and compares the window against what vim reported.
//
// The screen is 40 by 120 because that is what the harness's pseudo-terminal
// is, so the text area is 39 rows and vim answers &window 39 and &scroll 19.
// 'scrolloff' is 5, which is what "vim --clean" ends up with: defaults.vim
// sets it, and a run that took the documented default of 0 would answer H and
// L five lines out on every file.
func TestScrollAgainstVim(t *testing.T) {
	cases := []struct {
		name             string
		lines            int
		keys             string
		top, bottom, cur int
	}{
		// Nothing typed: the window starts at the top and the cursor with it.
		{"start", 60, "", 1, 39, 1},

		// CTRL-D and CTRL-U move by 'scroll', which is half the window, and
		// take the cursor with them. The second CTRL-D is the one that
		// matters: the top line stops at 22, where the last buffer line sits
		// on the last row, while the cursor carries on to 44.
		{"ctrl-d", 60, "\x04", 20, 58, 25},
		{"ctrl-d twice", 60, "\x04\x04", 22, 60, 44},
		{"ctrl-u after G", 60, "G\x15", 3, 41, 36},

		// CTRL-F is a window less two, and it is allowed to scroll past the
		// point CTRL-D stops at: the top line goes to 38 and three rows of
		// "~" show under the last line.
		{"ctrl-f", 60, "\x06", 38, 60, 43},
		{"ctrl-f at the end", 60, "\x06\x06", 60, 60, 60},
		{"ctrl-b after G", 60, "G\x02", 1, 39, 34},

		// CTRL-E and CTRL-Y scroll one line and leave the cursor alone unless
		// 'scrolloff' pushes it.
		{"ctrl-e", 60, "\x05", 2, 40, 7},
		{"ctrl-y after G", 60, "G\x19", 21, 59, 54},

		// A file shorter than the window. CTRL-D has nowhere to scroll and
		// moves the cursor to the last line; CTRL-E scrolls anyway, because
		// vim lets the last line be dragged up to the top row.
		{"ctrl-d on a short file", 10, "\x04", 1, 10, 10},
		{"ctrl-e on a short file", 10, "\x05", 2, 10, 7},
		{"ctrl-f on a short file", 10, "\x06", 10, 10, 10},

		// A count on CTRL-D or CTRL-U is a new 'scroll' and not a repeat.
		{"3 ctrl-d", 60, "3\x04", 4, 42, 9},
		{"4 ctrl-e", 60, "4\x05", 5, 43, 10},

		// The z family. zt honours 'scrolloff' and puts the cursor five lines
		// down, zz centres, and zb has nothing to scroll to on line 30 of a
		// 39-row window.
		{"zt", 60, "30Gzt", 25, 60, 30},
		{"zz", 60, "30Gzz", 11, 49, 30},
		{"zb", 60, "30Gzb", 1, 39, 30},
		{"z CR", 60, "30Gz\r", 25, 60, 30},
		{"z dot", 60, "30Gz.", 11, 49, 30},
		{"z minus", 60, "30Gz-", 1, 39, 30},

		// An ordinary motion scrolls the window minimally and stops where the
		// last line is on the last row, which is the rule the scrolling
		// commands are allowed to break and a motion is not.
		{"G", 60, "G", 22, 60, 60},
		{"G on a short file", 10, "G", 1, 10, 10},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ed, err := newEditor(numbered(c.lines), "buf.txt", 40, 120, "")
			if err != nil {
				t.Fatal(err)
			}
			ed.ed.SetScriptInput(true)
			if err := ed.sess.Run(keysOf(t, c.keys)); err != nil {
				t.Fatalf("running %q: %v", c.keys, err)
			}
			v := ed.sess.win().Visible()
			cur := ed.ed.Cursor().Line
			if v.Top != c.top || v.Bottom != c.bottom || cur != c.cur {
				t.Errorf("after %q: w0=%d w$=%d line=%d, vim says %d %d %d",
					c.keys, v.Top, v.Bottom, cur, c.top, c.bottom, c.cur)
			}
		})
	}
}

// TestHeadlessWindowIsWhatVimReports pins the measured default.
//
// Several other packages take this number on trust, so it is written down in
// one place with the way to remeasure it beside it: put ":echo &lines
// &columns" in a keys file and run it through cmd/oracle with -keep.
//
//	vim --clean -i NONE --not-a-term -s KEYS FILE with no terminal at all
//	 &lines 24, &columns 80, &window 23, &scroll 11
//	the same command line on cmd/oracle's pseudo-terminal
//	 &lines 40, &columns 120, &window 39, &scroll 19
//
// The second is the default because every case in testdata/keys is graded
// through that harness.
func TestHeadlessWindowIsWhatVimReports(t *testing.T) {
	if defaultRows != 40 || defaultCols != 120 {
		t.Fatalf("defaults are %d by %d; vim on the harness pty reports 40 by 120",
			defaultRows, defaultCols)
	}
	ed, err := newEditor(numbered(60), "buf.txt", defaultRows, defaultCols, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := ed.sess.win().View.Height; got != 39 {
		t.Errorf("text area %d rows, vim reports &window 39", got)
	}
	if got := ed.sess.so; got != 5 {
		t.Errorf("'scrolloff' %d, vim --clean reports 5", got)
	}
}

// TestScrolloffFromTheOptionsLine: the vimrc profile sets 'scrolloff' to 3 and
// H has to land three lines down rather than five. Measured: CTRL-F on the
// 60-line file leaves the cursor on 43 at 'scrolloff' 5 and on 41 at 3.
func TestScrolloffFromTheOptionsLine(t *testing.T) {
	ed, err := newEditor(numbered(60), "buf.txt", 40, 120, "scrolloff=3")
	if err != nil {
		t.Fatal(err)
	}
	if ed.sess.so != 3 {
		t.Fatalf("'scrolloff' %d, want 3 from the :set line", ed.sess.so)
	}
	if err := ed.sess.Run(keysOf(t, "\x06")); err != nil {
		t.Fatal(err)
	}
	if got := ed.ed.Cursor().Line; got != 41 {
		t.Errorf("CTRL-F left the cursor on %d, vim says 41 at 'scrolloff' 3", got)
	}
}

// TestScrollKeysAreNotScrollKeysInInsertMode. CTRL-E, CTRL-Y, CTRL-D and
// CTRL-U are different commands in insert mode -- the character below, the
// character above, and the two indent keys -- and the router has to let them
// through. CTRL-E on the second line copies the character below.
func TestScrollKeysAreNotScrollKeysInInsertMode(t *testing.T) {
	ed, err := newEditor([]byte("alpha aaa bbb\nbeta bbb ccc\n"), "buf.txt", 40, 120, "")
	if err != nil {
		t.Fatal(err)
	}
	ed.ed.SetScriptInput(true)
	if err := ed.sess.Run(keysOf(t, "i\x05\x1b")); err != nil {
		t.Fatal(err)
	}
	// Measured: vim leaves "balpha aaa bbb", the b copied off the line below.
	if got, want := string(ed.buf.Line(1)), "balpha aaa bbb"; got != want {
		t.Errorf("line 1 = %q, want %q: CTRL-E in insert mode is the character below", got, want)
	}
}

// TestASplitSharesTheScreen is the layout half of the window keys.
//
// internal/ex makes the second window and gives it the first one's size,
// because it has no idea how big the screen is; cmd/pvim knows and has to say
// so before anything measures a window. Until it did, both halves of a split
// thought they were the whole screen and every screen-relative key answered
// against a window that was not on the screen: on 60 lines in the harness's
// 40-row window, ":sp" then L left vim on line 14 and this on 34, which is
// where L lands when the window is still 39 rows tall.
func TestASplitSharesTheScreen(t *testing.T) {
	e := newTestEditor(t, sixtyLines())
	if got := e.sess.win().View.Height; got != 39 {
		t.Fatalf("one window is %d rows, want 39", got)
	}
	if err := e.ctx.RunLine("sp"); err != nil {
		t.Fatal(err)
	}
	e.sess.sync()
	for i, w := range e.tabs.Current().Windows() {
		if w.View.Height >= 39 {
			t.Errorf("window %d is %d rows after a split, want less than the whole screen", i, w.View.Height)
		}
	}
	if err := e.sess.Key(key.Rune('L')); err != nil {
		t.Fatal(err)
	}
	if got, want := e.ed.Cursor().Line, 14; got != want {
		t.Errorf("L in the top half of a split landed on line %d, want %d, which is what vim answers", got, want)
	}
}

// TestACountDoesNotUndoAScroll. vim reaches update_topline only with
// VALID_TOPLINE cleared and typing the "3" of "3H" clears nothing, so the
// window stays where CTRL-F put it. Measured on five lines in a 39-row window
// at 'scrolloff' 5: CTRL-F then `Hx` empties line 5 in both editors, and
// CTRL-F then `3Hx` empties vim's line 5 and, before this, line 3 here.
func TestACountDoesNotUndoAScroll(t *testing.T) {
	e := newTestEditor(t, "one\ntwo\nthree\nfour\nfive\n")
	if err := e.sess.Key(key.Ctrl('f')); err != nil {
		t.Fatal(err)
	}
	top := e.sess.win().View.TopLine
	if top == 1 {
		t.Fatal("CTRL-F on a five-line file did not scroll, so this test cannot see what it is about")
	}
	if err := e.sess.Key(key.Rune('3')); err != nil {
		t.Fatal(err)
	}
	if got := e.sess.win().View.TopLine; got != top {
		t.Errorf("typing a count moved the top line from %d to %d", top, got)
	}
}

// TestTheSidewaysZFamilyScrolls. All six only do anything under 'nowrap',
// which is why no case caught them being missing and why this one sets it.
// The numbers are internal/window's, measured there; what is
// checked here is that the keys reach it at all and that the cursor the window
// moved comes back to the mode machine.
func TestTheSidewaysZFamilyScrolls(t *testing.T) {
	line := make([]byte, 300)
	for i := range line {
		line[i] = byte('0' + i%10)
	}
	e := newTestEditor(t, string(line)+"\n")
	e.opt.W.Wrap = false
	e.sess.win().Opt.Wrap = false

	if err := e.sess.Run([]key.Key{key.Rune('z'), key.Rune('l')}); err != nil {
		t.Fatal(err)
	}
	if got := e.sess.win().View.LeftCol; got != 1 {
		t.Errorf("zl left 'leftcol' at %d, want 1", got)
	}
	if got := e.ed.Cursor().Col; got == 0 {
		t.Error("zl scrolled the cursor's own column off the window and left the cursor on it")
	}
	if err := e.sess.Run([]key.Key{key.Rune('z'), key.Rune('h')}); err != nil {
		t.Fatal(err)
	}
	if got := e.sess.win().View.LeftCol; got != 0 {
		t.Errorf("zh left 'leftcol' at %d, want 0", got)
	}
}

// TestZPlusAndZCaretReachTheWindow. Both were dispatched into the mode machine,
// which has no window, and came back out of zFallback as a bell.
func TestZPlusAndZCaretReachTheWindow(t *testing.T) {
	e := newTestEditor(t, sixtyLines())
	if err := e.sess.Run([]key.Key{key.Rune('z'), key.Rune('+')}); err != nil {
		t.Fatal(err)
	}
	if got := e.sess.win().View.TopLine; got == 1 {
		t.Error("z+ did not move the window")
	}
	if err := e.sess.Run([]key.Key{key.Rune('z'), key.Rune('^')}); err != nil {
		t.Fatal(err)
	}
	if got := e.sess.win().View.TopLine; got != 1 {
		t.Errorf("z^ back from the second screenful left the top line at %d, want 1", got)
	}
}
