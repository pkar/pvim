package screen

import (
	"strings"
	"testing"

	"github.com/pkar/pvim/internal/options"
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// leaves builds a branch of n windows over one buffer.
func leaves(n int, dir window.Dir) (*window.Node, []*window.Window) {
	o := options.Defaults()
	b := text.Read([]byte("one\ntwo\n"))
	root := &window.Node{Dir: dir}
	var wins []*window.Window
	for i := 0; i < n; i++ {
		w := window.New(i+1, b, o.GW)
		w.View.Cursor = text.Pos{Line: 1}
		root.Children = append(root.Children, &window.Node{Win: w})
		wins = append(wins, w)
	}
	return root, wins
}

// Three windows side by side on a 60-column screen get 20, 19 and 19 cells of
// text, in frames of 21, 20 and 19: the two dividers cost a column each and
// belong to the window on their left.
//
// vim --clean, :set lines=12 columns=60, :vsplit twice: the bars land in
// columns 20 and 40.
func TestLayoutVerticalWidths(t *testing.T) {
	root, wins := leaves(3, window.Vertical)
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 11, Cols: 60}, true)

	wantCols := []int{20, 19, 19}
	wantAt := []int{0, 21, 41}
	for i, w := range wins {
		if w.View.Width != wantCols[i] {
			t.Errorf("window %d is %d cells of text, want %d", i, w.View.Width, wantCols[i])
		}
		if w.Rect.Col != wantAt[i] {
			t.Errorf("window %d starts at column %d, want %d", i, w.Rect.Col, wantAt[i])
		}
	}
	if got := wins[2].Rect.Col + wins[2].Rect.Cols; got != 60 {
		t.Errorf("the rightmost window ends at %d, want the screen edge at 60", got)
	}
}

// Two windows stacked on eleven rows get six and five, and each keeps a row
// for its status line, so the text areas are five and four.
//
// vim --clean, :set lines=12 columns=40, :split.
func TestLayoutHorizontalHeights(t *testing.T) {
	root, wins := leaves(2, window.Horizontal)
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 11, Cols: 40}, true)

	if wins[0].Rect.Rows != 6 || wins[1].Rect.Rows != 5 {
		t.Errorf("frames are %d and %d rows, want 6 and 5", wins[0].Rect.Rows, wins[1].Rect.Rows)
	}
	if wins[0].View.Height != 5 || wins[1].View.Height != 4 {
		t.Errorf("text areas are %d and %d rows, want 5 and 4",
			wins[0].View.Height, wins[1].View.Height)
	}
	if wins[1].Rect.Row != 6 {
		t.Errorf("the lower window starts at row %d, want 6", wins[1].Rect.Row)
	}
}

// With 'laststatus' saying no, the window along the bottom edge has no status
// line and gets the row back.
func TestLayoutLastStatusOff(t *testing.T) {
	root, wins := leaves(2, window.Horizontal)
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 11, Cols: 40}, false)

	if wins[0].View.Height != 5 {
		t.Errorf("the upper window has %d text rows, want 5: it still has a status line",
			wins[0].View.Height)
	}
	if wins[1].View.Height != 5 {
		t.Errorf("the lower window has %d text rows, want 5: no status line to pay for",
			wins[1].View.Height)
	}
}

// 'scroll' follows the window height, which is what makes CTRL-D move further
// in a window somebody dragged taller.
func TestLayoutRecomputesScroll(t *testing.T) {
	root, wins := leaves(1, window.Horizontal)
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 24, Cols: 80}, false)
	if wins[0].Opt.Scroll != 12 {
		t.Errorf("'scroll' is %d in a 24-row window, want 12", wins[0].Opt.Scroll)
	}
	LayoutTree(root, Region{Row: 0, Col: 0, Rows: 12, Cols: 80}, false)
	if wins[0].Opt.Scroll != 6 {
		t.Errorf("'scroll' is %d after the window shrank to 12 rows, want 6", wins[0].Opt.Scroll)
	}
}

// The two columns vim's comp_col() decides, on the two screen widths this
// package was measured at.
func TestFurnitureColumns(t *testing.T) {
	cases := []struct {
		cols, ruler, showcmd int
	}{
		{40, 22, 12},
		{60, 42, 32},
		{30, 12, 2},
	}
	for _, c := range cases {
		if got := rulerCol(c.cols); got != c.ruler {
			t.Errorf("rulerCol(%d) = %d, want %d", c.cols, got, c.ruler)
		}
		if got := showCmdCol(c.cols, true, false); got != c.showcmd {
			t.Errorf("showCmdCol(%d) = %d, want %d", c.cols, got, c.showcmd)
		}
	}
}

// 'fillchars' is read by item name, and an item nobody set keeps its default.
func TestFillChars(t *testing.T) {
	const spec = "vert:|,fold:-,eob:~,lastline:@"
	cases := []struct {
		item string
		def  rune
		want rune
	}{
		{"vert", 'X', '|'},
		{"fold", 'X', '-'},
		{"eob", 'X', '~'},
		{"lastline", 'X', '@'},
		{"stl", ' ', ' '},
		{"nothing", 'Z', 'Z'},
	}
	for _, c := range cases {
		if got := fillChar(spec, c.item, c.def); got != c.want {
			t.Errorf("fillChar(%q) = %q, want %q", c.item, got, c.want)
		}
	}
}

// The relative-position field: All, Top, Bot and a percentage.
//
// vim --clean, :set lines=7 columns=30 scrolloff=0 over a ten-line file, 3Gzt:
// the ruler says "3,1 50%", which is two lines above and two below.
func TestRelPos(t *testing.T) {
	o := options.Defaults()
	b := text.Read([]byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"))
	cases := []struct {
		top, height int
		want        string
	}{
		{1, 10, "All"},
		{1, 5, "Top"},
		{6, 5, "Bot"},
		{3, 5, "40%"},
	}
	for _, c := range cases {
		w := window.New(1, b, o.GW)
		w.View.TopLine = c.top
		w.View.Height = c.height
		if got := relPos(w, b, 0); got != c.want {
			t.Errorf("top %d height %d: relPos = %q, want %q", c.top, c.height, got, c.want)
		}
	}

	// Vim writes the percentage with "%2d", so a single digit is padded to two
	// and the field stays three cells wide however far down the file is.
	long := text.Read([]byte(strings.Repeat("x\n", 30)))
	w := window.New(1, long, o.GW)
	w.View.TopLine, w.View.Height = 2, 5
	if got := relPos(w, long, 0); got != " 4%" {
		t.Errorf("relPos near the top of a 30-line file = %q, want %q", got, " 4%")
	}
}
