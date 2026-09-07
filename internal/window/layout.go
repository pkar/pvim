package window

import (
	"errors"

	"github.com/pkar/pvim/internal/options"
)

// Dir is which way a split divides its space.
type Dir uint8

// The two split directions. Horizontal is ":split", where the children are
// stacked one above another; Vertical is ":vsplit", where they sit side by
// side. The names are what the divider looks like in vim's own documentation,
// not what the children do, which is the confusion worth naming once here.
const (
	Horizontal Dir = iota
	Vertical
)

// Dir4 is a compass direction: what CTRL-W h, j, k and l take.
type Dir4 uint8

// The four directions.
const (
	Left Dir4 = iota
	Down
	Up
	Right
)

// The errors the window commands answer with, spelled with vim's E-codes so
// that a message line matches.
var (
	// ErrLastWindow is E444, which ":q" on the only window of the only tab
	// answers instead of quitting.
	ErrLastWindow = errors.New("E444: Cannot close last window")
	// ErrNoRoom is E36: a split in a window too small to divide.
	ErrNoRoom = errors.New("E36: Not enough room")
	// ErrNoSuchWindow is what a CTRL-W move with nothing in that direction
	// beeps about. Vim has no E-code for it; it beeps.
	ErrNoSuchWindow = errors.New("no window in that direction")
	// ErrNotFound is a window or tab page that is not in this tree, which is
	// a caller bug rather than a user one.
	ErrNotFound = errors.New("window not in this tab page")
)

// Node is one node of a tab page's layout tree.
//
// A leaf holds a window and no children. A branch holds a direction and two or
// more children and no window. Vim's own layout is exactly this: a frame is
// either a window, a row or a column, and ":sp" inside a row makes a column out
// of one of its cells.
type Node struct {
	// Win is the window at a leaf, nil at a branch.
	Win *Window
	// Dir is which way a branch divides. Meaningless at a leaf.
	Dir Dir
	// Children are a branch's children, in screen order: top to bottom for
	// Horizontal, left to right for Vertical. Empty at a leaf.
	Children []*Node
	// Rect is where this node sits, filled in by Layout. It covers the whole
	// region including the status line under a window and the separator column
	// to the right of one, which is why a leaf's Rect is one row taller and
	// one column wider than the text its window shows.
	Rect Rect
	// Size is the node's text extent along its parent's axis: rows for a child
	// of a Horizontal branch, columns for a child of a Vertical one. Zero means
	// "no opinion", and Layout then shares the space out by window count, which
	// is what 'equalalways' asks for and what a fresh tree wants.
	Size int
}

// Leaf reports whether n holds a window rather than children.
func (n *Node) Leaf() bool { return n != nil && n.Win != nil }

// Windows returns every window under n, in screen order: the order CTRL-W w
// cycles through and the order ":ls" would list them in for one tab.
func (n *Node) Windows() []*Window {
	if n == nil {
		return nil
	}
	if n.Leaf() {
		return []*Window{n.Win}
	}
	var out []*Window
	for _, c := range n.Children {
		out = append(out, c.Windows()...)
	}
	return out
}

// Count is how many windows are under n.
func (n *Node) Count() int {
	if n == nil {
		return 0
	}
	if n.Leaf() {
		return 1
	}
	total := 0
	for _, c := range n.Children {
		total += c.Count()
	}
	return total
}

// Span is how many windows deep the frame is along one axis: rows for
// Horizontal, columns for Vertical.
//
// It is the weight Layout shares space by and the count of status lines or
// separator columns the frame costs, and it is a span rather than a window
// count because those are different numbers the moment a frame nests. A column
// holding two windows side by side is one window deep vertically: it costs one
// status row, not two, which is why ":sp" then ":vs" leaves three windows of
// 11, 11 and 10 rows and not of 11, 11 and 9.
func (n *Node) Span(dir Dir) int {
	if n == nil {
		return 0
	}
	if n.Leaf() {
		return 1
	}
	if n.Dir == dir {
		total := 0
		for _, c := range n.Children {
			total += c.Span(dir)
		}
		return total
	}
	deepest := 0
	for _, c := range n.Children {
		if d := c.Span(dir); d > deepest {
			deepest = d
		}
	}
	return deepest
}

// has reports whether w is one of the windows under n.
func (n *Node) has(w *Window) bool {
	if n == nil || w == nil {
		return false
	}
	if n.Leaf() {
		return n.Win == w
	}
	for _, c := range n.Children {
		if c.has(w) {
			return true
		}
	}
	return false
}

// find returns the path from n down to the leaf holding w, root first, or nil.
func (n *Node) find(w *Window) []*Node {
	if n == nil {
		return nil
	}
	if n.Leaf() {
		if n.Win == w {
			return []*Node{n}
		}
		return nil
	}
	for _, c := range n.Children {
		if p := c.find(w); p != nil {
			return append([]*Node{n}, p...)
		}
	}
	return nil
}

// TabPage is one tab: a layout tree and whichever of its windows is current.
type TabPage struct {
	// Root is the top of the layout tree, always non-nil for a live tab.
	Root *Node
	// Cur is the current window, which is always one of Root's leaves.
	Cur *Window
	// Prev is the window CTRL-W p goes back to, which vim keeps per tab page.
	Prev *Window
	// Label is what the tabline shows. Empty means the tabline uses the
	// current window's file name, which is what vim does with no
	// 'guitablabel'.
	Label string
	// CWD is this tab's working directory, which ":tcd" sets and which
	// 'autochdir' does not touch.
	CWD string
	// Area is the region Layout was last given, so that a command which
	// changes the tree can re-lay it out without the caller passing the
	// screen size in again.
	Area Rect

	// opt is the option block Layout was last given. Every command that
	// changes the tree lays it out again, and every one of them would
	// otherwise have to be handed the options a second time; ":sp" wants
	// 'equalalways', ":q" wants it too, and CTRL-W _ wants 'winminheight'.
	// Nil until the first Layout, which is a tab page nobody has drawn.
	opt *options.Options
}

// NewTabPage returns a tab page holding one window.
func NewTabPage(w *Window) *TabPage {
	return &TabPage{Root: &Node{Win: w}, Cur: w}
}

// Windows returns every window in the tab, in screen order.
func (t *TabPage) Windows() []*Window { return t.Root.Windows() }

// Split divides w and puts fresh beside it.
//
// The caller builds fresh with New, because it owns the window-id counter and
// decides which buffer the new window shows: ":sp" shares the buffer, ":new"
// makes an empty one, ":sp file" reads a different one.
//
// dir is Horizontal for ":sp" and Vertical for ":vs". before puts the new
// window above or to the left; the caller reads 'splitbelow' and 'splitright'
// and passes the answer, because they are global and this package should not
// have to be told which one applies to which direction.
//
// size is the new window's height or width in cells, or 0 for vim's default,
// which is half the space w had. A ":5sp" in a 23-row screen leaves the new
// window 5 rows and the old one 16, the two status lines making up the rest.
//
// The new window becomes current, which is what every one of ":sp", ":vs",
// ":new" and ":vnew" does, and which the sizing depends on: 'equalalways' gives
// the odd row to the frame holding the window that is about to be current, so a
// split that left the cursor behind would come out one row different.
func (t *TabPage) Split(w, fresh *Window, dir Dir, before bool, size int) error {
	path := t.Root.find(w)
	if path == nil {
		return ErrNotFound
	}
	leaf := path[len(path)-1]

	// Room: a split needs a status line for the new window on top of the two
	// text rows, or a separator column on top of the two text columns.
	if dir == Horizontal && w.View.Height < 2*minHeight+1 {
		return ErrNoRoom
	}
	if dir == Vertical && w.View.Width < 2*minWidth+1 {
		return ErrNoRoom
	}

	// Vim's default size is half of what the window had, with the new status
	// line or separator column taken off the top first. Measured in a 24-row
	// 80-column screen: the first ":sp" gives 11 rows and 10, because the two
	// status lines cost two of the 23; ":vs" gives 40 columns and 39.
	avail, keep, asked := splitRoom(w, dir), 0, size
	if size > 0 {
		size = clamp(size, minimumFor(dir), avail-minimumFor(dir))
	} else {
		size = (avail + 1) / 2
	}
	keep = avail - size

	old := &Node{Win: w, Size: keep}
	fresh.Rect = Rect{}
	next := &Node{Win: fresh, Size: size}

	if parent := parentOf(path); parent != nil && parent.Dir == dir {
		// Same direction as the frame this window already lives in: insert a
		// sibling rather than nesting, which is what keeps three ":sp" in a row
		// a flat stack of four and not a chain of nested pairs.
		at := indexOf(parent.Children, leaf)
		leaf.Size = keep
		if !before {
			at++
		}
		parent.Children = insertNode(parent.Children, at, next)
	} else {
		leaf.Win = nil
		leaf.Dir = dir
		if before {
			leaf.Children = []*Node{next, old}
		} else {
			leaf.Children = []*Node{old, next}
		}
	}
	t.Prev, t.Cur = w, fresh
	if asked == 0 {
		// An explicit size survives 'equalalways'. Measured: ":5sp" in a
		// 23-row screen leaves 5 rows and 16 with 'equalalways' on, where a
		// bare ":sp" leaves 11 and 10.
		t.settle(dir)
	} else {
		t.Relayout()
	}
	return nil
}

// splitRoom is how many text rows or columns are left for the two windows
// after a split of w has paid for the new status line or separator.
//
// It works off the laid-out rectangle when there is one, because that is where
// the answer differs: the only window of a tab has no status line under
// 'laststatus' 1, so splitting it costs two rows and not one.
func splitRoom(w *Window, dir Dir) int {
	if dir == Vertical {
		region := w.View.Width
		if w.Rect.Cols > region {
			region = w.Rect.Cols
		}
		return region - 1 - (region - w.View.Width)
	}
	region := w.View.Height
	if w.Rect.Rows > region {
		region = w.Rect.Rows
	}
	return region - 2
}

// minimumFor is 'winminheight' or 'winminwidth'.
func minimumFor(dir Dir) int {
	if dir == Vertical {
		return minWidth
	}
	return minHeight
}

// settle is what every command that changes the tree ends with: forget the
// stored sizes along one axis when 'equalalways' is on, then lay the tab out
// again.
//
// One axis and not both. Vim equalises in the direction of the split, so ":vs"
// then ":sp" leaves the two columns 40 and 39 columns wide and only shares the
// rows out again; equalising both would make the column holding two windows
// wider than the one holding one. And on a split and a close and not on a
// redraw, which is why this is here and not in Layout: CTRL-W + has to survive
// the next frame.
func (t *TabPage) settle(dir Dir) {
	if t.opt == nil || t.opt.G.EqualAlways {
		forgetAlong(t.Root, dir)
	}
	t.Relayout()
}

// forgetAlong clears the stored sizes of every frame that divides along dir.
func forgetAlong(n *Node, dir Dir) {
	if n == nil || n.Leaf() {
		return
	}
	if n.Dir == dir {
		for _, c := range n.Children {
			c.Size = 0
		}
	}
	for _, c := range n.Children {
		forgetAlong(c, dir)
	}
}

// Close removes a window from the tab, giving its space to a neighbour.
//
// It answers ErrLastWindow when w is the only window in the tab, which is what
// makes ":q" on the last window a quit rather than a close: the caller sees the
// error and decides.
//
// The window that becomes current is the one vim picks: the next sibling in the
// frame, or the previous one when the closed window was last.
func (t *TabPage) Close(w *Window) error {
	if len(t.Windows()) <= 1 {
		return ErrLastWindow
	}
	path := t.Root.find(w)
	if path == nil {
		return ErrNotFound
	}
	leaf := path[len(path)-1]
	parent := parentOf(path)

	at := indexOf(parent.Children, leaf)
	parent.Children = append(parent.Children[:at:at], parent.Children[at+1:]...)

	heir := at
	if heir >= len(parent.Children) {
		heir = len(parent.Children) - 1
	}
	next := firstWindow(parent.Children[heir])

	// A branch left with one child stops being a branch. Collapsing it is what
	// keeps ":vs" then ":sp" then ":q" back to a plain vertical split rather
	// than a column with one cell in it.
	collapse(t.Root)
	if len(parent.Children) == 1 && parent == t.Root {
		collapse(t.Root)
	}

	if t.Cur == w {
		t.Cur = next
	}
	if t.Prev == w {
		t.Prev = nil
	}
	t.settle(parent.Dir)
	return nil
}

// Only closes every window in the tab but w, which is CTRL-W o and ":only".
func (t *TabPage) Only(w *Window) error {
	if t.Root.find(w) == nil {
		return ErrNotFound
	}
	t.Root = &Node{Win: w, Size: 0}
	t.Cur = w
	t.Prev = nil
	t.Relayout()
	return nil
}

// Neighbour returns the window in the given direction from w, or nil.
//
// It is answered from the laid-out rectangles rather than by walking the frame
// tree, which is the same answer for every layout these commands can build and
// a tenth of the code. The tie-break is vim's: among the windows that touch the
// edge being crossed, take the one whose span covers the current window's
// cursor row or column, so CTRL-W j out of a vertical split lands under the
// cursor and not at the left edge.
//
// Layout has to have run. A tab page nobody has laid out has no rectangles and
// this returns nil, which is the honest answer to "what is left of a window
// that is nowhere".
func (t *TabPage) Neighbour(w *Window, d Dir4) *Window {
	if w == nil || w.Rect.Empty() {
		return nil
	}
	me := w.Rect
	// The cursor's screen position, which is what decides between two
	// candidates that both touch the edge, held inside the window's own
	// rectangle so that a stale cursor cannot point at a neighbour directly.
	//
	// The column is computed at a tabstop of 8 rather than the buffer's,
	// because a window does not know its buffer's options and this is the only
	// place in the package that wants one. It is wrong only for a line with
	// tabs in it, and only by choosing the other of two windows that are both
	// under the cursor, which is a beep-free wrong answer rather than a bug.
	// TODO: pass the real 'tabstop' in if it ever matters.
	row := clamp(me.Row+(w.View.Cursor.Line-w.View.TopLine), me.Row, me.Row+me.Rows-1)
	col := clamp(me.Col+(w.CursorDispCol(8)-w.View.LeftCol), me.Col, me.Col+me.Cols-1)

	var best *Window
	for _, o := range t.Windows() {
		if o == w || o.Rect.Empty() {
			continue
		}
		r := o.Rect
		switch d {
		case Up:
			if r.Row+r.Rows <= me.Row && spans(r.Col, r.Cols, col) {
				if best == nil || r.Row > best.Rect.Row {
					best = o
				}
			}
		case Down:
			if r.Row >= me.Row+me.Rows && spans(r.Col, r.Cols, col) {
				if best == nil || r.Row < best.Rect.Row {
					best = o
				}
			}
		case Left:
			if r.Col+r.Cols <= me.Col && spans(r.Row, r.Rows, row) {
				if best == nil || r.Col > best.Rect.Col {
					best = o
				}
			}
		case Right:
			if r.Col >= me.Col+me.Cols && spans(r.Row, r.Rows, row) {
				if best == nil || r.Col < best.Rect.Col {
					best = o
				}
			}
		}
	}
	return best
}

// spans reports whether v falls inside the half-open range [start, start+n).
func spans(start, n, v int) bool { return v >= start && v < start+n }

// Layout assigns a rectangle to every node in the tree and to every window in
// it, and recomputes 'scroll' for each.
//
// r is the whole text area of the screen: what is left after the tabline and
// the command line have taken their rows.
//
// A leaf's Rect covers its text plus the status line under it and the separator
// column to its right, where it has them; View.Height and View.Width are the
// text alone, which is the one place those numbers differ and the reason both
// exist. Whether a window has a status line is 'laststatus': 2 always, 1 only
// when the tab holds more than one window, 0 never.
//
// Space is shared by window count. Measured a 24-row 80-column
// vim: one ":sp" gives 11 and 10 rows, two give 7, 7 and 6, three give 5, 5, 5
// and 4, and ":vs" gives 40 and 39 columns with the separator in column 41. The
// nesting is what makes the second of those 7, 7, 6 rather than 11, 5, 5: the
// frame holding two windows gets two thirds of the screen and then halves it.
func (t *TabPage) Layout(r Rect, o *options.Options) {
	t.Area, t.opt = r, o
	status := hasStatusLine(o, len(t.Windows()))
	t.Root.layout(r, o, status, false, t.Cur)
}

// Relayout runs Layout again over the area and options it was last given, which
// is what every command that changes the tree wants and what saves each of them
// carrying the screen size around. A tab page nobody has laid out yet has no
// area and this does nothing.
func (t *TabPage) Relayout() {
	if !t.Area.Empty() {
		t.Layout(t.Area, t.opt)
	}
}

// hasStatusLine answers 'laststatus' for a tab holding n windows.
func hasStatusLine(o *options.Options, n int) bool {
	ls := 1
	if o != nil {
		ls = o.G.LastStatus
	}
	return ls >= 2 || (ls == 1 && n > 1)
}

// minHeight and minWidth are 'winminheight' and 'winminwidth', which vim
// defaults to 1 and which CTRL-W _ and CTRL-W | squeeze every other window
// down to.
const (
	minHeight = 1
	minWidth  = 1
)

// layout assigns rectangles down one subtree.
//
// sep says whether this node's region carries a trailing separator column,
// which happens when the node is not the last child of a vertical branch or
// when its parent's region carried one. cur is the tab's current window, which
// share gives the odd cell to.
func (n *Node) layout(r Rect, o *options.Options, status, sep bool, cur *Window) {
	n.Rect = r
	if n.Leaf() {
		w := n.Win
		w.Rect = r
		cols := r.Cols
		if sep {
			cols--
		}
		rows := r.Rows
		if status {
			rows--
		}
		w.View.Width = max(0, cols)
		w.SetHeight(max(0, rows))
		return
	}

	k := len(n.Children)
	if n.Dir == Horizontal {
		// Each row of frames costs one status line, which is the span and not
		// the window count: a column of two side-by-side windows is one row
		// deep and pays for one.
		text := r.Rows
		if status {
			text -= n.Span(Horizontal)
		}
		sizes := share(text, n.Children, Horizontal, minHeight, cur)
		row := r.Row
		for i, c := range n.Children {
			rows := sizes[i]
			if status {
				rows += c.Span(Horizontal)
			}
			c.layout(Rect{Row: row, Col: r.Col, Rows: rows, Cols: r.Cols}, o, status, sep, cur)
			row += rows
		}
		return
	}

	// Vertical: one separator column between each pair of columns, plus the
	// trailing one when the region itself carries it.
	text := r.Cols - (n.Span(Vertical) - 1)
	if sep {
		text--
	}
	sizes := share(text, n.Children, Vertical, minWidth, cur)
	col := r.Col
	for i, c := range n.Children {
		childSep := i < k-1 || sep
		cols := sizes[i] + c.Span(Vertical) - 1
		if childSep {
			cols++
		}
		c.layout(Rect{Row: r.Row, Col: col, Rows: r.Rows, Cols: cols}, o, status, childSep, cur)
		col += cols
	}
}

// ShareSizes is share, exported for internal/screen.
//
// There are two layouts in this tree and there should be one. This one is
// authoritative: it is what TabPage.Resize writes through, what CTRL-W + and
// CTRL-W < move, and what a drag on a split separator sets. internal/screen
// then draws the frame, and for a long time it shared the band by window count
// and never read Size at all, so all three of those set the right number and
// the next frame threw it away.
//
// Exporting the rule rather than copying it is the difference between one
// definition of what Size means and two that have to be kept in step by hand.
// The remaining duplication is the arithmetic around it, which
// TestScreenAndWindowAgreeOnSizedNodes pins.
//
// Size is the TEXT extent along the parent's axis and excludes the status line,
// which is why callers subtract the span before calling and add it back after.
func ShareSizes(total int, children []*Node, dir Dir, cur *Window) []int {
	minimum := minHeight
	if dir == Vertical {
		minimum = minWidth
	}
	return share(total, children, dir, minimum, cur)
}

// share divides total text cells among children and writes the answer back to
// their Size fields, so that the tree remembers what it was given.
//
// A child with a Size set keeps it when the sizes add up. When they do not -- a
// fresh tree where nothing has an opinion, a screen that just changed size, a
// split or a close under 'equalalways' -- the space is shared out, and vim's
// rule has two halves that were both measured rather than reasoned about:
//
// - The frame holding the current window is served first and gets its share
// rounded up. Measured with ":set splitbelow" and two ":sp" in a 24-row
// screen, which gives 7, 6 and 7 rows with the current window last, where
// the same three windows with the current one first give 7, 7 and 6.
// - The rest share the remainder cumulatively, so that the rounding is spread
// rather than taken by whichever frame comes first. Four ":sp" give 4, 4, 3,
// 4, 3 and not 4, 4, 4, 3, 3.
func share(total int, children []*Node, dir Dir, minimum int, cur *Window) []int {
	sizes := make([]int, len(children))
	sum, opinionated := 0, true
	for i, c := range children {
		sizes[i] = c.Size
		if c.Size <= 0 {
			opinionated = false
		}
		sum += c.Size
	}

	if !opinionated || sum != total {
		span := 0
		for _, c := range children {
			span += c.Span(dir)
		}

		rest, restSpan := total, span
		at := -1
		for i, c := range children {
			if c.has(cur) {
				at = i
				break
			}
		}
		if at >= 0 && span > 0 {
			n := children[at].Span(dir)
			sizes[at] = (total*n + span - 1) / span
			rest -= sizes[at]
			restSpan -= n
		}

		done, seen, last := 0, 0, len(children)-1
		if at == last {
			last--
		}
		for i, c := range children {
			if i == at {
				continue
			}
			seen += c.Span(dir)
			cut := rest
			if restSpan > 0 && i != last {
				cut = (rest*seen + restSpan - 1) / restSpan
			}
			sizes[i] = cut - done
			done = cut
		}
	}

	// The floor is 'winminheight' or 'winminwidth' per window in the frame,
	// which is what CTRL-W _ leaves the others at.
	for i, c := range children {
		if lo := minimum * c.Span(dir); sizes[i] < lo {
			sizes[i] = lo
		}
		c.Size = sizes[i]
	}
	return sizes
}

// parentOf returns the node above the leaf a find() path ends at, or nil when
// the leaf is the root.
func parentOf(path []*Node) *Node {
	if len(path) < 2 {
		return nil
	}
	return path[len(path)-2]
}

// indexOf returns where child sits among nodes, or -1.
func indexOf(nodes []*Node, child *Node) int {
	for i, n := range nodes {
		if n == child {
			return i
		}
	}
	return -1
}

// insertNode puts n into nodes at index at.
func insertNode(nodes []*Node, at int, n *Node) []*Node {
	out := make([]*Node, 0, len(nodes)+1)
	out = append(out, nodes[:at]...)
	out = append(out, n)
	return append(out, nodes[at:]...)
}

// firstWindow returns the first window under n in screen order.
func firstWindow(n *Node) *Window {
	if ws := n.Windows(); len(ws) > 0 {
		return ws[0]
	}
	return nil
}

// collapse folds away every branch left holding one child, which is what a
// close leaves behind and what would otherwise turn the tree into a chain of
// one-cell frames.
func collapse(n *Node) {
	if n == nil || n.Leaf() {
		return
	}
	for _, c := range n.Children {
		collapse(c)
	}
	if len(n.Children) == 1 {
		c := n.Children[0]
		n.Win, n.Dir, n.Children = c.Win, c.Dir, c.Children
		if c.Size > 0 {
			n.Size = c.Size
		}
	}
}

// Tabs is the list of tab pages, of which one is current.
//
// The vimrc maps { to gT and } to gt, so tab switching is a two-key motion in
// daily use and this list is walked more often than it is changed.
type Tabs struct {
	Pages []*TabPage
	Cur   int

	// Last is the tab page that was current before this one, which is where
	// "g<Tab>" and CTRL-W g<Tab> go back to. Nil until something has moved
	// between two tabs, and nil again when the page it names is closed.
	//
	// A pointer and not an index: closing a tab renumbers every page after it
	// and an index kept across that points at the wrong one. Measured against
	// vim 9.2.0321 on three tabs -- ":tabnew", ":tabnew", ":tabfirst" then
	// "g<Tab>" lands on tab 3, which is the page that was current before the
	// ":tabfirst" and not the page before it in the list.
	Last *TabPage
}

// NewTabs returns a tab list holding one page.
func NewTabs(p *TabPage) *Tabs { return &Tabs{Pages: []*TabPage{p}, Cur: 0} }

// Current returns the current tab page, or nil when there are none.
func (t *Tabs) Current() *TabPage {
	if t.Cur < 0 || t.Cur >= len(t.Pages) {
		return nil
	}
	return t.Pages[t.Cur]
}

// Window returns the current window: the current tab's current window.
func (t *Tabs) Window() *Window {
	p := t.Current()
	if p == nil {
		return nil
	}
	return p.Cur
}

// New inserts a tab page after the one at index at and makes it current, which
// is what ":tabnew" does with the current tab's index. Pass len(Pages)-1 for
// ":$tabnew".
func (t *Tabs) New(p *TabPage, at int) {
	t.Last = t.Current()
	at = clamp(at+1, 0, len(t.Pages))
	pages := make([]*TabPage, 0, len(t.Pages)+1)
	pages = append(pages, t.Pages[:at]...)
	pages = append(pages, p)
	t.Pages = append(pages, t.Pages[at:]...)
	t.Cur = at
}

// Close removes tab page i. It answers ErrLastWindow when it is the only one,
// for the same reason TabPage.Close does: the caller turns that into a quit.
//
// The tab that becomes current is the next one, or the previous one when the
// closed tab was last, which is vim's.
func (t *Tabs) Close(i int) error {
	if len(t.Pages) <= 1 {
		return ErrLastWindow
	}
	if i < 0 || i >= len(t.Pages) {
		return ErrNotFound
	}
	if t.Last == t.Pages[i] {
		// The page g<Tab> would have gone back to is the one being closed, so
		// there is nowhere to go back to any more.
		t.Last = nil
	}
	t.Pages = append(t.Pages[:i:i], t.Pages[i+1:]...)
	if t.Cur > i || t.Cur >= len(t.Pages) {
		t.Cur--
	}
	if t.Cur < 0 {
		t.Cur = 0
	}
	return nil
}

// Goto makes tab page i current, clamped to the list, and remembers the one it
// left so that g<Tab> has somewhere to go back to.
func (t *Tabs) Goto(i int) {
	if len(t.Pages) == 0 {
		return
	}
	i = clamp(i, 0, len(t.Pages)-1)
	if i == t.Cur {
		return
	}
	if t.Cur >= 0 && t.Cur < len(t.Pages) {
		t.Last = t.Pages[t.Cur]
	}
	t.Cur = i
}

// Back is g<Tab> and CTRL-W g<Tab>: the tab page that was current before this
// one, and false when there is none.
//
// False is silence and not a beep: measured on one tab page, vim says nothing
// at all and changes nothing.
func (t *Tabs) Back() bool {
	if t.Last == nil {
		return false
	}
	at := -1
	for i, p := range t.Pages {
		if p == t.Last {
			at = i
			break
		}
	}
	if at < 0 || at == t.Cur {
		return false
	}
	t.Goto(at)
	return true
}

// Next is gt: forward one tab, wrapping. With a count it is not a repeat but an
// absolute tab number, 1-based, which is the rule that catches everybody.
func (t *Tabs) Next(count int) {
	if len(t.Pages) == 0 {
		return
	}
	if count > 0 {
		t.Goto(count - 1)
		return
	}
	t.Goto((t.Cur + 1) % len(t.Pages))
}

// Prev is gT: back one tab, wrapping. A count here IS a repeat, unlike gt,
// which is vim being vim.
func (t *Tabs) Prev(count int) {
	if len(t.Pages) == 0 {
		return
	}
	n := max(1, count) % len(t.Pages)
	t.Goto((t.Cur - n + len(t.Pages)) % len(t.Pages))
}
