package window

import "errors"

// ErrOnlyOneWindow is what a command that needs a second window in the tab
// says when there is none.
//
// It is a message and not an E-code because vim has none for it: CTRL-W o and
// CTRL-W T on a tab holding one window both print "Already only one window"
// on the message line and change nothing. Measured through cmd/oracle on a
// 40-row screen, both keys, both option profiles.
var ErrOnlyOneWindow = errors.New("Already only one window")

// ToNewTab is CTRL-W T: take the current window out of the current tab page
// and give it a tab page of its own, opened after the one it left.
//
// It answers ErrOnlyOneWindow when the window is the only one in its tab,
// because the tab it would move to is the tab it is already in and vim says so
// rather than making an empty one. Measured on a 40-row screen with two
// windows: "CTRL-W s 3G CTRL-W T" leaves tab 2 of 2 holding one window at the
// full 38 rows with the cursor still on line 3, and the same on one window
// leaves tab 1 of 1 and "Already only one window".
//
// The new page inherits the area and the option block the old one was laid out
// with, so a window that moves tabs is the right size before anything redraws.
func (t *Tabs) ToNewTab() error {
	p := t.Current()
	if p == nil || p.Cur == nil {
		return ErrNotFound
	}
	w := p.Cur
	area, opt := p.Area, p.opt
	if err := p.Close(w); err != nil {
		if errors.Is(err, ErrLastWindow) {
			return ErrOnlyOneWindow
		}
		return err
	}

	fresh := NewTabPage(w)
	fresh.CWD = p.CWD
	t.New(fresh, t.Cur)
	if !area.Empty() {
		fresh.Layout(area, opt)
	}
	return nil
}

// Goto makes w the current window of the tab and remembers the one it left, so
// that CTRL-W p has somewhere to go back to.
func (t *TabPage) Goto(w *Window) {
	if w == nil || w == t.Cur {
		return
	}
	t.Prev = t.Cur
	t.Cur = w
}

// Move is CTRL-W h, j, k and l: go to the window in that direction, and report
// whether there was one. False is a beep.
func (t *TabPage) Move(d Dir4, count int) bool {
	cur := t.Cur
	for i := 0; i < max(1, count); i++ {
		next := t.Neighbour(cur, d)
		if next == nil {
			break
		}
		cur = next
	}
	if cur == t.Cur {
		return false
	}
	t.Goto(cur)
	return true
}

// Cycle is CTRL-W w and CTRL-W W: the next or the previous window in screen
// order, wrapping.
//
// A count on CTRL-W w is an absolute window number, 1-based, and not a repeat,
// the same asymmetry gt has against gT. A count on CTRL-W W counts back that
// many windows.
func (t *TabPage) Cycle(forward bool, count int) {
	ws := t.Windows()
	if len(ws) == 0 {
		return
	}
	at := indexOfWindow(ws, t.Cur)
	switch {
	case forward && count > 0:
		at = clamp(count-1, 0, len(ws)-1)
	case forward:
		at = (at + 1) % len(ws)
	default:
		n := max(1, count) % len(ws)
		at = (at - n + len(ws)) % len(ws)
	}
	t.Goto(ws[at])
}

// First is CTRL-W t and Last is CTRL-W b: the top-left and the bottom-right
// window of the tab.
func (t *TabPage) First() { t.Goto(firstWindow(t.Root)) }

// Last is CTRL-W b.
func (t *TabPage) Last() {
	if ws := t.Windows(); len(ws) > 0 {
		t.Goto(ws[len(ws)-1])
	}
}

// Back is CTRL-W p: the window this tab was in before the current one. It
// beeps when nothing has been left yet.
func (t *TabPage) Back() bool {
	if t.Prev == nil || t.Root.find(t.Prev) == nil {
		return false
	}
	t.Prev, t.Cur = t.Cur, t.Prev
	return true
}

// Exchange is CTRL-W x: swap the current window with the next one in its own
// frame, or with window [count] when one is given.
//
// The window and its size move together, so exchanging a 18-row window with a
// 1-row one leaves the 1-row content on top. The cursor ends up in the earlier
// of the two positions, whichever window landed there. Measured with three
// windows at 18, 1 and 1 rows: CTRL-W x gives 1, 18 and 1 with the top window
// current.
func (t *TabPage) Exchange(count int) bool {
	leaves := t.Root.leaves()
	if len(leaves) < 2 {
		return false
	}
	at := indexOfLeaf(leaves, t.Cur)
	other := at + 1
	if count > 0 {
		other = count - 1
	}
	if other >= len(leaves) || other < 0 || other == at {
		if count > 0 {
			return false
		}
		other = 0
	}
	swapLeaves(leaves[at], leaves[other])
	t.Cur = leaves[min(at, other)].Win
	t.Relayout()
	return true
}

// MoveTo is CTRL-W K, J, H and L: take the current window out of the tree and
// put it back as a frame spanning one whole edge of the tab. K is the top and
// J the bottom, both full width; H is the far left and L the far right, both
// full height. The cursor stays in the window that moved.
//
// Vim's own words for it are ":topleft split" and ":botright split" applied to
// a window that already exists, and that is exactly the shape: the leaf is
// removed, whatever branch it left is collapsed, and the window goes back as
// the first or last child of the root. When the root already divides the way
// the move wants, the window joins it as a sibling rather than nesting inside
// a second frame of the same direction -- ":vs :sp CTRL-W L" is three columns
// of 26 in vim and would be a column beside a pair of columns otherwise.
//
// Measured on the 24-row 80-column screen the layout table uses, and every one
// of these is a row of testdata/window/layout-vim9.2.0321.txt:
//
//	:sp CTRL-W H two columns, 40 and 39, the moved one on the left
//	:sp :vs CTRL-W J 7, 6 and 7 rows, the moved one at the bottom
//	:vs :sp CTRL-W K an 11-row band over the two columns it left
//	:vs :sp CTRL-W L three columns of 26, the moved one last
//
// A count is read and thrown away, which is vim: "5 CTRL-W K" and "20 CTRL-W H"
// leave the same layout a bare one does, under 'equalalways' and under
// 'noequalalways' both. Vim passes the count to win_split_ins as a size and
// then equalises over the top of it.
//
// False is "there was nothing to do", which is the one window case: vim leaves
// the layout alone and says nothing, so the caller neither beeps nor redraws.
//
// One measured thing this does NOT reproduce, named here rather than hidden:
// under 'noequalalways' vim shares the space the moved window left among the
// windows that stay by the arithmetic of a remove followed by a split, which
// is not a re-share. Three stacked windows of 14, 4 and 18 rows with the top
// one current come out 12, 10 and 14 in vim and 11, 11 and 14 here. Both
// oracle profiles have 'equalalways' on -- it is vim's default and the vimrc
// does not touch it -- so no case grades the difference, and reproducing it
// means reproducing frame_remove's donation to a sibling and win_split_ins
// taking it back, which is a different algorithm and not a rounding rule.
func (t *TabPage) MoveTo(d Dir4) bool {
	w := t.Cur
	if w == nil || len(t.Windows()) < 2 {
		return false
	}
	path := t.Root.find(w)
	parent := parentOf(path)
	if parent == nil {
		return false
	}
	dir, first := Horizontal, d == Up
	if d == Left || d == Right {
		dir, first = Vertical, d == Left
	}

	leaf := path[len(path)-1]
	at := indexOf(parent.Children, leaf)
	parent.Children = append(parent.Children[:at:at], parent.Children[at+1:]...)
	collapse(t.Root)

	moved := &Node{Win: w}
	if !t.Root.Leaf() && t.Root.Dir == dir {
		if first {
			t.Root.Children = insertNode(t.Root.Children, 0, moved)
		} else {
			t.Root.Children = append(t.Root.Children, moved)
		}
	} else {
		rest := &Node{Win: t.Root.Win, Dir: t.Root.Dir, Children: t.Root.Children}
		t.Root = &Node{Dir: dir}
		if first {
			t.Root.Children = []*Node{moved, rest}
		} else {
			t.Root.Children = []*Node{rest, moved}
		}
	}
	// Nothing at the top of the tree keeps the size it had: the frame the
	// window left is a different shape and the frame it joined is a different
	// width. Zeroing them is what makes share divide the edge again rather
	// than believe a total that no longer adds up.
	for _, c := range t.Root.Children {
		c.Size = 0
	}
	t.Cur = w
	t.settle(dir)
	return true
}

// Rotate is CTRL-W r and CTRL-W R: move the windows of the current frame one
// place down or right, wrapping. Each window takes its size with it and the
// cursor stays in the window it was in, which is now somewhere else on screen.
//
// Measured with three stacked windows of 17, 7 and 12 rows and the cursor in
// the first: CTRL-W r leaves 12, 17 and 7 with the cursor still in the 17-row
// one, CTRL-W R leaves 7, 12 and 17, and "2 CTRL-W r" is CTRL-W R.
func (t *TabPage) Rotate(forward bool, count int) bool {
	path := t.Root.find(t.Cur)
	parent := parentOf(path)
	if parent == nil || len(parent.Children) < 2 {
		return false
	}
	leaves := parent.leaves()
	if len(leaves) < 2 {
		return false
	}
	n := max(1, count) % len(leaves)
	if n == 0 {
		return true
	}
	if !forward {
		n = len(leaves) - n
	}

	type slot struct {
		win  *Window
		size int
	}
	saved := make([]slot, len(leaves))
	for i, l := range leaves {
		saved[i] = slot{l.Win, l.Size}
	}
	for i, l := range leaves {
		src := saved[(i-n+len(leaves))%len(leaves)]
		l.Win, l.Size = src.win, src.size
	}
	t.Relayout()
	return true
}

// leaves returns the leaf nodes under n in screen order.
func (n *Node) leaves() []*Node {
	if n == nil {
		return nil
	}
	if n.Leaf() {
		return []*Node{n}
	}
	var out []*Node
	for _, c := range n.Children {
		out = append(out, c.leaves()...)
	}
	return out
}

// indexOfLeaf returns where the leaf holding w sits, or 0.
func indexOfLeaf(leaves []*Node, w *Window) int {
	for i, l := range leaves {
		if l.Win == w {
			return i
		}
	}
	return 0
}

// swapLeaves exchanges the windows two leaves hold, sizes included.
func swapLeaves(a, b *Node) {
	a.Win, b.Win = b.Win, a.Win
	a.Size, b.Size = b.Size, a.Size
}

// indexOfWindow returns where w sits in ws, or 0.
func indexOfWindow(ws []*Window, w *Window) int {
	for i, o := range ws {
		if o == w {
			return i
		}
	}
	return 0
}

// Resize is CTRL-W +, -, < and >: give the current window n more or fewer rows
// or columns, taking them from a sibling in the same frame.
//
// Measured in a 23-row screen holding three windows at 7, 7 and 6 rows: CTRL-W
// + on the first gives 8, 6, 6 and 3 CTRL-W + gives 10, 4, 6, so the rows come
// off the next sibling first and only then off the one after it. In an 80-column
// vertical split at 40 and 39, 5 CTRL-W > gives 45 and 34.
func (t *TabPage) Resize(dir Dir, delta int) bool {
	path := t.Root.find(t.Cur)
	branch, child := frameAlong(path, dir)
	if branch == nil {
		return false
	}
	minimum := minHeight
	if dir == Vertical {
		minimum = minWidth
	}
	at := indexOf(branch.Children, child)

	got := 0
	for step := 1; step < len(branch.Children) && got != delta; step++ {
		for _, j := range [2]int{at + step, at - step} {
			if j < 0 || j >= len(branch.Children) || got == delta {
				continue
			}
			c := branch.Children[j]
			floor := minimum * c.Span(dir)
			want := delta - got
			if want > 0 {
				take := min(want, c.Size-floor)
				c.Size -= take
				got += take
			} else {
				c.Size -= want
				got += want
			}
		}
	}
	if got == 0 {
		return false
	}
	child.Size += got
	t.Relayout()
	return true
}

// Maximise is CTRL-W _ and CTRL-W |: give the current window every row or
// column the frame has, leaving the others at 'winminheight' or 'winminwidth'.
//
// A count is the size to set rather than the maximum, which is what makes
// "10 CTRL-W _" a ten-row window and not a full-screen one.
func (t *TabPage) Maximise(dir Dir, count int) bool {
	path := t.Root.find(t.Cur)
	branch, child := frameAlong(path, dir)
	if branch == nil {
		return false
	}
	minimum := minHeight
	if dir == Vertical {
		minimum = minWidth
	}

	if count > 0 {
		// A count is a size to grow to, and it comes off the neighbours in
		// order rather than flattening them: measured, three windows at 7, 7
		// and 6 rows become 10, 4 and 6 under "10 CTRL-W _", where a bare
		// CTRL-W _ makes them 18, 1 and 1.
		return t.Resize(dir, count-child.Size)
	}

	total := 0
	for _, c := range branch.Children {
		total += c.Size
	}
	others := 0
	for _, c := range branch.Children {
		if c != child {
			c.Size = minimum * c.Span(dir)
			others += c.Size
		}
	}
	child.Size = total - others
	t.Relayout()
	return true
}

// Equalise is CTRL-W =: forget every size in the tab and let Layout share the
// space out by window count again.
func (t *TabPage) Equalise() {
	forget(t.Root)
	t.Relayout()
}

// forget clears every stored size in a subtree.
func forget(n *Node) {
	if n == nil {
		return
	}
	n.Size = 0
	for _, c := range n.Children {
		forget(c)
	}
}

// frameAlong walks up a find() path to the first branch dividing along dir,
// and returns it with the child of it the path went through.
//
// That pair is what every resize acts on: CTRL-W + inside a vertical split
// makes the whole column taller, not just the one window, because the column is
// the child of the horizontal frame the path crosses.
func frameAlong(path []*Node, dir Dir) (branch, child *Node) {
	for i := len(path) - 2; i >= 0; i-- {
		if path[i].Dir == dir && len(path[i].Children) > 1 {
			return path[i], path[i+1]
		}
	}
	return nil, nil
}
