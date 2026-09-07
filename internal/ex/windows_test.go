package ex

import (
	"errors"
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// The window commands, the per-window state they carry and the two things that
// used to be one function: putting a different buffer in THIS window, and
// making a DIFFERENT window current.
//
// Every expectation here was measured against /opt/homebrew/bin/vim 9.2
// patches 1-321 through "vim --clean -i NONE --not-a-term -s",
// and the ones that can be graded end to end have a case in testdata/keys
// beside them.

// TestSplitAndCloseKeepEachWindowsCursor is the bug this file exists for.
//
// Measured on eight lines: "3G", ":sp", "5G", ":q" leaves vim on line 3. The
// window that survives has been sitting on line 3 the whole time and the
// cursor the closed window ended on is not its business. Before the split
// between swapBuffer and followWindow, ":q" wrote the editor's 5 into it.
func TestSplitAndCloseKeepEachWindowsCursor(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.ctx.Ed.SetCursor(text.Pos{Line: 3})
	h.ctx.Sync()

	h.run(t, "split")
	if n := len(h.ctx.tab().Windows()); n != 2 {
		t.Fatalf("after :split there are %d windows, want 2", n)
	}
	h.ctx.Ed.SetCursor(text.Pos{Line: 5})
	h.ctx.Sync()

	h.run(t, "quit")
	if line, _ := h.cursor(); line != 3 {
		t.Errorf("after :sp 5G :q the cursor is on line %d; vim leaves it on 3", line)
	}
}

// TestSplitInheritsTheView is the other half of the same fact: the new window
// of a ":sp" shows the same lines as the one it split, so its top line comes
// across with it. Before this, swapBuffer reset it to 1 and a split halfway
// down a long file scrolled the new window back to the top.
func TestSplitInheritsTheView(t *testing.T) {
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = "line"
	}
	h := newHarness(t, lines...)
	h.ctx.Ed.SetCursor(text.Pos{Line: 120})
	h.ctx.Sync()
	top := h.ctx.Window().View.TopLine
	if top <= 1 {
		t.Fatalf("the setup did not scroll: top line %d", top)
	}

	h.run(t, "split")
	if got := h.ctx.Window().View.TopLine; got != top {
		t.Errorf("the new window's top line is %d; the window it split shows %d", got, top)
	}
	if line, _ := h.cursor(); line != 120 {
		t.Errorf("the new window's cursor is on %d, want 120", line)
	}
}

// TestWincmdMovesTheCursorBetweenWindows covers the other direction: CTRL-W j
// and CTRL-W k each pick up the cursor of the window they land in, and leave
// the one they came from where it was.
func TestWincmdMovesTheCursorBetweenWindows(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.ctx.Ed.SetCursor(text.Pos{Line: 3})
	h.ctx.Sync()
	h.run(t, "split")

	h.ctx.Ed.SetCursor(text.Pos{Line: 7})
	h.ctx.Sync()
	// CTRL-W w and not CTRL-W j: the directional ones read the rectangles
	// TabPage.Layout fills in, and this harness has a tree and no screen.
	if err := h.ctx.wincmd('w', 0); err != nil {
		t.Fatalf("CTRL-W w: %v", err)
	}
	if line, _ := h.cursor(); line != 3 {
		t.Errorf("the other window is on line %d, want the 3 it was left on", line)
	}
	if err := h.ctx.wincmd('w', 0); err != nil {
		t.Fatalf("CTRL-W w: %v", err)
	}
	if line, _ := h.cursor(); line != 7 {
		t.Errorf("coming back, the window is on line %d, want the 7 it was left on", line)
	}
}

// TestAlternateFileBelongsToTheWindow is vim's w_alt_fnum.
//
// Measured: ":new" then ":q" then CTRL-W ^ says "E23: No alternate file", and
// ":e other" then ":new" then ":q" then CTRL-W ^ opens the ORIGINAL file. The
// alternate the ":new" set belonged to the window the ":new" made and died
// with it; a global one left the [No Name] buffer as the answer to "#".
func TestAlternateFileBelongsToTheWindow(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.run(t, "new")
	if alt := h.ctx.Bufs.Alt; alt == nil {
		t.Fatal(":new left no alternate file in the window it made")
	}
	h.run(t, "close")
	if alt := h.ctx.Bufs.Alt; alt != nil {
		t.Errorf("after the :new window closed, # is %q; vim answers E23", alt.Display())
	}
}

// TestSplitInheritsTheAlternateFile is the same field going the other way:
// vim's win_split copies it, so a ":sp" knows what "#" means.
func TestSplitInheritsTheAlternateFile(t *testing.T) {
	h := newHarness(t, tenLines()...)
	first := h.ctx.current()
	second := h.ctx.Bufs.Add("second", text.New())
	h.ctx.swapBuffer(second)
	if h.ctx.Bufs.Alt != first {
		t.Fatalf("the setup did not set the alternate file")
	}
	h.run(t, "split")
	if h.ctx.Bufs.Alt != first {
		t.Errorf("the split window's # is not the one it was split from")
	}
}

// TestEditHashWithNoAlternateIsE194 is the message vim gives for a "#" in a
// file NAME, which is not the E23 that ":b#" gives. Measured, both.
func TestEditHashWithNoAlternateIsE194(t *testing.T) {
	h := newHarness(t, tenLines()...)
	err := h.err("e#")
	if !errors.Is(err, ErrNoAltName) {
		t.Errorf(":e# with no alternate answered %v, want E194", err)
	}
	if before := h.text(); before != strings.Join(tenLines(), "\n") {
		t.Errorf(":e# opened something: the buffer is now %q", before)
	}
}

// TestTabStepKeepsEachTabsCursor is the tab-page version of the split case:
// ":tabnext" leaves the window it came from with the cursor it was on.
func TestTabStepKeepsEachTabsCursor(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.ctx.Ed.SetCursor(text.Pos{Line: 4})
	h.ctx.Sync()
	h.run(t, "tabnew")
	h.run(t, "tabnext")
	if len(h.ctx.Tabs.Pages) != 2 {
		t.Fatalf("there are %d tab pages, want 2", len(h.ctx.Tabs.Pages))
	}
	// Whichever of the two ":tabnext" landed on, walking back to the first one
	// has to find line 4 again.
	for i := 0; i < 2 && h.ctx.Tabs.Cur != 0; i++ {
		h.run(t, "tabnext")
	}
	if line, _ := h.cursor(); line != 4 {
		t.Errorf("the first tab came back on line %d, want 4", line)
	}
}

// TestApplyWindowOptionsCarriesTheWindowHalf is the fix item 5 asked for, from
// this side of the seam: the exported function every caller that moves the
// option state has to run. 'spell' is the one the vimrc's
// "au BufEnter *.txt,*.md setlocal spell" needs and the one the four-field
// copy this replaced did not carry.
func TestApplyWindowOptionsCarriesTheWindowHalf(t *testing.T) {
	h := newHarness(t, tenLines()...)
	w := h.ctx.Window()
	w.Opt.Scroll = 11

	h.run(t, "setlocal spell number nowrap conceallevel=2")
	if !w.Opt.Spell {
		t.Error("'spell' did not reach the window")
	}
	if !w.Opt.Number {
		t.Error("'number' did not reach the window")
	}
	if w.Opt.Wrap {
		t.Error("'nowrap' did not reach the window")
	}
	if w.Opt.ConcealLevel != 2 {
		t.Errorf("'conceallevel' is %d on the window, want 2", w.Opt.ConcealLevel)
	}
	if w.Opt.Scroll != 11 {
		t.Errorf("'scroll' is %d; the window owns that one and it must survive", w.Opt.Scroll)
	}
}

// TestWindowOptionsSurviveAWindowSwitch is what the wholesale copy must not
// break: ApplyWindowOptions writes the option state onto the CURRENT window
// and no other.
func TestWindowOptionsSurviveAWindowSwitch(t *testing.T) {
	h := newHarness(t, tenLines()...)
	h.run(t, "split")
	other := otherWindow(h.ctx.tab(), h.ctx.Window())
	other.Opt.Number = false

	h.run(t, "setlocal number")
	if !h.ctx.Window().Opt.Number {
		t.Error("'number' did not reach the current window")
	}
	if other.Opt.Number {
		t.Error("'number' reached a window that is not current")
	}
}

// otherWindow is the window of the tab that is not w.
func otherWindow(t *window.TabPage, w *window.Window) *window.Window {
	for _, x := range t.Windows() {
		if x != w {
			return x
		}
	}
	return nil
}
