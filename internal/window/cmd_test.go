package window

import (
	"errors"
	"testing"
)

// TestToNewTab is CTRL-W T.
//
// Measured through cmd/oracle on the harness's 40-row screen, with
// testdata/keys/ctrl_w_shift_t_moves_window_to_a_tab and
// ctrl_w_shift_t_then_g_shift_t_returns behind it: "CTRL-W s 3G CTRL-W T"
// leaves tab 2 of 2 holding one window at the full height, the tab it left
// holding the one window that stayed, and the cursor still where it was.
//
// The four things checked are the four that can each be wrong on their own:
// the window count in both tabs, which tab is current, that the window is the
// same object and not a copy, and that it came out laid out rather than at
// zero by zero waiting for a redraw nobody asked for.
func TestToNewTab(t *testing.T) {
	tabs, o, next := tab()
	p := tabs.Current()
	stay := p.Cur

	*next++
	moved := New(*next, stay.Buf, o.GW)
	if err := p.Split(stay, moved, Horizontal, true, 0); err != nil {
		t.Fatal(err)
	}
	moved.View.Cursor.Line = 7

	if err := tabs.ToNewTab(); err != nil {
		t.Fatalf("ToNewTab: %v", err)
	}

	if got := len(tabs.Pages); got != 2 {
		t.Fatalf("%d tab pages, want 2", got)
	}
	if tabs.Cur != 1 {
		t.Errorf("tab page %d is current, want the new one at index 1", tabs.Cur)
	}
	if got := tabs.Pages[0].Windows(); len(got) != 1 || got[0] != stay {
		t.Errorf("the tab left behind holds %d windows, want the one that stayed", len(got))
	}
	fresh := tabs.Pages[1]
	if got := fresh.Windows(); len(got) != 1 || got[0] != moved {
		t.Errorf("the new tab holds %d windows, want the one that moved", len(got))
	}
	if fresh.Cur != moved {
		t.Error("the moved window is not the new tab's current window")
	}
	if moved.View.Cursor.Line != 7 {
		t.Errorf("the moved window is on line %d, want the 7 it was on", moved.View.Cursor.Line)
	}
	if moved.Rect.Rows != screen.Rows || moved.Rect.Cols != screen.Cols {
		t.Errorf("the moved window is %dx%d, want the whole %dx%d screen: the new tab was not laid out",
			moved.Rect.Rows, moved.Rect.Cols, screen.Rows, screen.Cols)
	}
}

// TestToNewTabNeedsASecondWindow is the refusal, which is vim's: CTRL-W T on
// the only window of a tab prints "Already only one window" and makes no tab,
// because the tab it would move to is the one it is already in.
func TestToNewTabNeedsASecondWindow(t *testing.T) {
	tabs, _, _ := tab()

	err := tabs.ToNewTab()
	if !errors.Is(err, ErrOnlyOneWindow) {
		t.Fatalf("ToNewTab on one window returned %v, want ErrOnlyOneWindow", err)
	}
	if got := len(tabs.Pages); got != 1 {
		t.Errorf("%d tab pages, want 1: a refused CTRL-W T made one anyway", got)
	}
	if got := len(tabs.Current().Windows()); got != 1 {
		t.Errorf("%d windows, want the 1 there was", got)
	}
}

// TestMoveToOnOneWindowDoesNothing is CTRL-W H, J, K and L with nothing to
// move past.
//
// False and not an error, and not a beep either: vim leaves the layout exactly
// as it was and prints nothing, measured on a single window for all four keys.
// The layout table covers every case with a second window in it;
// testdata/window/layout-vim9.2.0321.txt has sixteen rows of them.
func TestMoveToOnOneWindowDoesNothing(t *testing.T) {
	for _, d := range []Dir4{Left, Down, Up, Right} {
		tabs, _, _ := tab()
		p := tabs.Current()
		before := p.Cur.View.Height

		if p.MoveTo(d) {
			t.Errorf("MoveTo(%v) on one window reported it moved something", d)
		}
		if got := len(p.Windows()); got != 1 {
			t.Errorf("MoveTo(%v) left %d windows, want 1", d, got)
		}
		if got := p.Cur.View.Height; got != before {
			t.Errorf("MoveTo(%v) resized the only window from %d to %d", d, before, got)
		}
	}
}

// TestMoveToFlattensASameDirectionRoot is the rule that decides between three
// columns and a column beside a pair of them.
//
// vim's frames do not nest in the direction they already divide, so ":vs :sp
// CTRL-W L" is three columns of 26 on an 80-column screen. Nesting instead
// would put the moved window in a frame of its own beside the two it left, and
// the widths would come out 40 and two of 19. The layout table's
// ":vs :sp <C-W>L" row is the measurement; this states the shape behind it.
func TestMoveToFlattensASameDirectionRoot(t *testing.T) {
	tabs, o, next := tab()
	p := tabs.Current()

	first := p.Cur
	*next++
	if err := p.Split(first, New(*next, first.Buf, o.GW), Vertical, true, 0); err != nil {
		t.Fatal(err)
	}
	moved := p.Cur
	*next++
	if err := p.Split(moved, New(*next, moved.Buf, o.GW), Horizontal, true, 0); err != nil {
		t.Fatal(err)
	}

	if !p.MoveTo(Right) {
		t.Fatal("MoveTo(Right) reported nothing moved")
	}
	if p.Root.Leaf() || p.Root.Dir != Vertical {
		t.Fatal("the root is not the row the move asked for")
	}
	if got := len(p.Root.Children); got != 3 {
		t.Fatalf("the root has %d children, want the 3 vim's flat frame has", got)
	}
	for i, c := range p.Root.Children {
		if !c.Leaf() {
			t.Errorf("child %d is a frame, want a window: the move nested instead of joining", i)
		}
	}
	if p.Root.Children[2].Win != p.Cur {
		t.Error("the window that moved is not the last of the three")
	}
}
