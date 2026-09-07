package screen

import (
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// There are two layout implementations in this tree: window.Node.layout, which
// has always honoured Size, and LayoutTree here, which until now shared by
// window count and threw Size away. That gap was invisible rather than broken:
// a drag on a split separator set the right size and the next frame put the
// divider back where it was, and CTRL-W + and CTRL-W < were dead the same way.
//
// These tests are the reason the duplication is survivable. If they start
// failing because window's layout grew a rule this one has not, the fix is to
// delegate to window's layout, not to copy the rule across.

func sizedWin(id int, size int) *window.Node {
	b := text.Read([]byte("one\ntwo\nthree\n"))
	w := window.New(id, b, options.Defaults().GW)
	n := &window.Node{Win: w}
	n.Size = size
	return n
}

// TestLayoutTreeHonoursSize is the bug in one test: a horizontal split whose
// children have asked for particular heights must get them.
func TestLayoutTreeHonoursSize(t *testing.T) {
	root := &window.Node{
		Dir:      window.Horizontal,
		Children: []*window.Node{sizedWin(1, 8), sizedWin(2, 3)},
	}
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 11, Cols: 40}, false)

	if got, want := root.Children[0].Rect.Rows, 8; got != want {
		t.Errorf("top window got %d rows, want %d", got, want)
	}
	if got, want := root.Children[1].Rect.Rows, 3; got != want {
		t.Errorf("bottom window got %d rows, want %d", got, want)
	}
	// And the two together must still fill the band exactly, or the frame has
	// a hole in it.
	if got := root.Children[0].Rect.Rows + root.Children[1].Rect.Rows; got != 11 {
		t.Errorf("the two windows cover %d rows of 11", got)
	}
}

// TestLayoutTreeHonoursWidth is the same for a vertical split, which is the one
// the separator drag actually moves.
func TestLayoutTreeHonoursWidth(t *testing.T) {
	root := &window.Node{
		Dir:      window.Vertical,
		Children: []*window.Node{sizedWin(1, 30), sizedWin(2, 9)},
	}
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 11, Cols: 40}, false)

	// The left window's frame carries the divider column, so its text is 30 and
	// its rectangle is 31.
	if got, want := root.Children[0].Win.View.Width, 30; got != want {
		t.Errorf("left window shows %d columns of text, want %d", got, want)
	}
	if got, want := root.Children[1].Win.View.Width, 9; got != want {
		t.Errorf("right window shows %d columns of text, want %d", got, want)
	}
	if got := root.Children[0].Rect.Cols + root.Children[1].Rect.Cols; got != 40 {
		t.Errorf("the two windows cover %d columns of 40", got)
	}
}

// TestLayoutTreeSharesWhenNobodyAsked pins the old behaviour: a tree where no
// node has an opinion must lay out exactly as it did before Size was read, with
// the odd cell going to the leading child.
func TestLayoutTreeSharesWhenNobodyAsked(t *testing.T) {
	root := &window.Node{
		Dir:      window.Horizontal,
		Children: []*window.Node{sizedWin(1, 0), sizedWin(2, 0), sizedWin(3, 0)},
	}
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 11, Cols: 40}, false)

	got := []int{
		root.Children[0].Rect.Rows,
		root.Children[1].Rect.Rows,
		root.Children[2].Rect.Rows,
	}
	want := []int{4, 4, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows are %v, want %v", got, want)
		}
	}
}

// TestLayoutTreeMixesSizedAndNot pins a rule that is surprising until you read
// window.share: Size is honoured only when EVERY child has one and they add up
// to the space available. One child with an opinion among three is not a
// partially-pinned layout, it is a tree whose sizes do not add up, and the whole
// row is shared evenly.
//
// That is deliberate in window and this test exists so that nobody "fixes" it
// here. It is also why a drag sets every child's size and not just the two on
// either side of the divider.
func TestLayoutTreeMixesSizedAndNot(t *testing.T) {
	root := &window.Node{
		Dir:      window.Horizontal,
		Children: []*window.Node{sizedWin(1, 6), sizedWin(2, 0), sizedWin(3, 0)},
	}
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 12, Cols: 40}, false)

	for i, n := range root.Children {
		if got, want := n.Rect.Rows, 4; got != want {
			t.Errorf("window %d got %d rows, want %d: sizes that do not add up are shared", i, got, want)
		}
	}

	// Give all three an opinion that adds up and every one of them is kept.
	root.Children[0].Size, root.Children[1].Size, root.Children[2].Size = 6, 3, 3
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 12, Cols: 40}, false)
	for i, want := range []int{6, 3, 3} {
		if got := root.Children[i].Rect.Rows; got != want {
			t.Errorf("window %d got %d rows, want %d", i, got, want)
		}
	}
}

// TestLayoutTreeSurvivesAnImpossibleAsk. A tree asking for more than the screen
// has must not push a window off the bottom or produce a zero-height one; it
// scales, keeping the proportions.
func TestLayoutTreeSurvivesAnImpossibleAsk(t *testing.T) {
	root := &window.Node{
		Dir:      window.Horizontal,
		Children: []*window.Node{sizedWin(1, 100), sizedWin(2, 50), sizedWin(3, 0)},
	}
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 10, Cols: 40}, false)

	total := 0
	for i, n := range root.Children {
		if n.Rect.Rows < 1 {
			t.Errorf("window %d got %d rows", i, n.Rect.Rows)
		}
		total += n.Rect.Rows
	}
	if total != 10 {
		t.Errorf("the windows cover %d rows of 10", total)
	}
	// The one that asked for twice as much still gets more.
	if root.Children[0].Rect.Rows <= root.Children[1].Rect.Rows {
		t.Errorf("proportions lost: %d and %d",
			root.Children[0].Rect.Rows, root.Children[1].Rect.Rows)
	}
}

// TestScreenAndWindowAgreeOnSizedNodes is the pin. Both implementations lay out
// the same tree in the same band; the heights they assign have to match, or a
// window command and the frame that draws it disagree about where the divider
// is and the editor shows one thing while believing another.
func TestScreenAndWindowAgreeOnSizedNodes(t *testing.T) {
	build := func() *window.Node {
		return &window.Node{
			Dir:      window.Horizontal,
			Children: []*window.Node{sizedWin(1, 7), sizedWin(2, 4)},
		}
	}
	o := options.Defaults()

	byWindow := build()
	tab := window.NewTabPage(byWindow.Children[0].Win)
	tab.Root = byWindow
	tab.Layout(window.Rect{Row: 0, Col: 0, Rows: 11, Cols: 40}, &o)

	byScreen := build()
	// The same status-line answer both sides, or this compares two different
	// questions: window derives it from 'laststatus', which defaults to 1 and
	// therefore draws one under every window once there are two.
	LayoutTree(byScreen, Region{Row: 0, Col: 0, Rows: 11, Cols: 40}, hasStatus(&o, 2))

	for i := range byWindow.Children {
		w, s := byWindow.Children[i].Rect, byScreen.Children[i].Rect
		if w.Rows != s.Rows {
			t.Errorf("child %d: window says %d rows, screen says %d", i, w.Rows, s.Rows)
		}
	}
}

// hasStatus mirrors window's own 'laststatus' rule. It is duplicated here
// rather than exported because the day these two layouts are merged it goes
// away with the rest of the duplication.
func hasStatus(o *options.Options, n int) bool {
	ls := 1
	if o != nil {
		ls = o.G.LastStatus
	}
	return ls >= 2 || (ls == 1 && n > 1)
}
