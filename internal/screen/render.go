package screen

import (
	"github.com/pkar/pvim/internal/text"
	"github.com/pkar/pvim/internal/window"
)

// Render paints a whole frame: the tabline, every window of the current tab
// with its status line and separators, the command line or the message area,
// and the completion menu over the top.
//
// It clears the grid first. A frontend that wants to know what changed diffs
// the result against the frame it last painted, which is what Grid.Diff is
// for; nothing here tries to be clever about which rows moved.
func (s *Screen) Render(f *Frame) {
	if s.HL == nil {
		s.HL = NewTable()
	}
	g := LookupGroups(s.HL)
	s.Grid.Clear()

	o := f.opts()
	if f.Tabs == nil {
		f.Tabs = window.NewTabs(window.NewTabPage(window.New(1, nil, o.GW)))
	}
	tab := f.Tabs.Current()
	wins := 0
	if tab != nil {
		wins = len(tab.Windows())
	}
	tabs := 0
	if f.Tabs != nil {
		tabs = len(f.Tabs.Pages)
	}

	s.Layout = Layout{
		Rows:        s.Grid.Rows,
		Cols:        s.Grid.Cols,
		ShowTabline: o.G.ShowTabline,
		Tabs:        tabs,
		LastStatus:  o.G.LastStatus,
		Windows:     wins,
		CmdHeight:   o.G.CmdHeight,
	}

	band, cmd := s.bands()
	if s.Layout.showTabline() {
		f.drawTabline(s, g)
	}

	if tab != nil {
		LayoutTree(tab.Root, Region{Row: band.Row, Col: 0, Rows: band.Rows, Cols: band.Cols}, s.Layout.showStatus())
		for _, w := range tab.Windows() {
			f.drawWindow(s, g, w, w == tab.Cur, band)
		}
	}

	f.drawCmdArea(s, g, cmd)

	// The ruler lives on the very last row when no window has a status line to
	// put it on, and it goes on after the command line because the two share
	// the row: vim redraws the ruler over the tail of a finished command.
	// comp_col() decides the column and it is measured: on a 40-column screen
	// the ruler starts at column 22 and 'showcmd' at column 12.
	//
	// A message on that row does not push the ruler off it. vim's showmode()
	// ends with win_redr_ruler() and msg_end() leaves the ruler where it is,
	// so the two sit side by side: measured at 40x10, ":set ruler?" gives
	//
	//	 ruler 1,1 All
	//
	// and "i" gives the same row with "-- INSERT --" in front of the ruler.
	// What does take the row is a message area that has overflowed, which the
	// press-enter prompt is the end of.
	if !s.Layout.showStatus() && tab != nil && tab.Cur != nil && o.G.Ruler &&
		!f.Cmdline.Active && !f.msgOverflows(cmd.Rows) {
		f.drawRuler(s, g, tab.Cur, s.Grid.Rows-1, 0, s.Grid.Cols, rulerCol(s.Grid.Cols), false, ' ')
	}
	if f.Pum != nil {
		f.drawPum(s, g)
	}
}

// bands splits the screen into the rows the windows get and the rows the
// command line gets, with the tabline taken off the top.
//
// It is not Layout.Regions(): that one splits out the last status line as a
// band of its own, which is right for a screen with one window and wrong for
// one with three, where two of the status lines are inside the text area. Here
// a window's rectangle holds its own status line and the arithmetic stays in
// one place.
func (s *Screen) bands() (band, cmd Region) {
	rows, cols := s.Grid.Rows, s.Grid.Cols
	ch := s.Layout.CmdHeight
	if ch < 1 {
		ch = 1
	}
	top := 0
	if s.Layout.showTabline() {
		top = 1
	}
	if top+ch+1 > rows {
		ch = 1
	}
	if top+ch+1 > rows {
		top = 0
	}
	h := rows - top - ch
	if h < 0 {
		h = 0
	}
	return Region{Row: top, Col: 0, Rows: h, Cols: cols},
		Region{Row: top + h, Col: 0, Rows: rows - top - h, Cols: cols}
}

// LayoutTree assigns a rectangle to every node of a tab page's layout tree and
// sets each window's height, width and 'scroll' from it.
//
// band is the whole area the windows share: what is left of the screen after
// the tabline and the command line. lastStatus says whether the windows along
// the bottom edge carry a status line, which is 'laststatus' already applied
// to the window count.
//
// A window's Rect includes its status line and its vertical separator; its
// View.Height and View.Width do not, which is the one place those numbers
// differ and the reason both exist.
//
// The division is even, with the remainder going to the leading children: on a
// 60-column screen three windows side by side get 20, 19 and 19 cells with a
// separator between each pair, which is what vim 9.2.0321 draws. Vim's own
// resize policy is more than that -- 'equalalways', 'winwidth' holding the
// current window at 20 columns, 'winfixheight' -- and it belongs to whoever
// writes ":sp", because it decides sizes when a window is created while this
// function only shares out what it is given. Measured, so that whoever writes
// it has the number: the same three-way split on a 40-column screen is 20, 9
// and 9, because 'winwidth' keeps the current window at 20.
func LayoutTree(n *window.Node, band Region, lastStatus bool) {
	right, bottom := band.Col+band.Cols, band.Row+band.Rows

	var walk func(*window.Node, Region)
	walk = func(n *window.Node, r Region) {
		if n == nil {
			return
		}
		n.Rect = window.Rect{Row: r.Row, Col: r.Col, Rows: r.Rows, Cols: r.Cols}
		if n.Leaf() {
			w := n.Win
			w.Rect = n.Rect
			sep := 0
			if r.Col+r.Cols < right {
				sep = 1
			}
			status := 0
			if lastStatus || r.Row+r.Rows < bottom {
				status = 1
			}
			if r.Rows-status < 1 {
				status = 0
			}
			w.View.Width = r.Cols - sep
			w.SetHeight(r.Rows - status)
			return
		}
		kids := n.Children
		if len(kids) == 0 {
			return
		}
		// The sizes come from window.ShareSizes, the same function
		// TabPage.Resize, CTRL-W + and a separator drag write through. This
		// used to share by window count and never read Node.Size, so all three
		// set the right number and the next frame put the divider back.
		if n.Dir == window.Vertical {
			// The dividers cost a column each and they belong to the window
			// on their left, so the text widths are shared out of what is
			// left and each child but the last gets its divider back. On a
			// 60-column screen that is 20, 19, 19 cells of text in frames of
			// 21, 20 and 19, which is where vim puts the bars.
			last := len(kids) - 1
			sizes := window.ShareSizes(r.Cols-last, kids, window.Vertical, nil)
			off := r.Col
			for i := range kids {
				cols := sizes[i]
				if i < last {
					cols++
				}
				walk(kids[i], Region{Row: r.Row, Col: off, Rows: r.Rows, Cols: cols})
				off += cols
			}
			return
		}
		// Size excludes the status line, so the rows it costs come off before
		// the text is shared and go back on after. One per row of frames, which
		// is the span and not the child count: a row of two side-by-side
		// windows is one row deep and pays for one.
		status := lastStatus || r.Row+r.Rows < bottom
		text := r.Rows
		if status {
			text -= n.Span(window.Horizontal)
		}
		sizes := window.ShareSizes(text, kids, window.Horizontal, nil)
		off := r.Row
		for i, c := range kids {
			rows := sizes[i]
			if status {
				rows += c.Span(window.Horizontal)
			}
			walk(kids[i], Region{Row: off, Col: r.Col, Rows: rows, Cols: r.Cols})
			off += rows
		}
	}
	walk(n, band)
}

// share divides total between n children and calls f with each child's offset
// and size. The remainder goes to the leading children, one cell each, which
// is what makes a 60-column three-way split 20, 19, 19 rather than 19, 19, 20.
func share(n, total int, f func(i, off, size int)) {
	if n <= 0 {
		return
	}
	base, rem := total/n, total%n
	off := 0
	for i := 0; i < n; i++ {
		size := base
		if i < rem {
			size++
		}
		f(i, off, size)
		off += size
	}
}

// drawWindow paints one window: its text, its status line and its vertical
// separator.
func (f *Frame) drawWindow(s *Screen, g Groups, w *window.Window, current bool, band Region) {
	o := f.opts()
	r := Region{Row: w.Rect.Row, Col: w.Rect.Col, Rows: w.Rect.Rows, Cols: w.Rect.Cols}
	if r.Empty() {
		return
	}

	// The separator column, if this window does not reach the right edge. It
	// runs down the text rows only: a status line is drawn across the whole
	// frame, separator column included, which is why vim shows
	//
	//	g.txt 1,40 All g.txt 1,1 All
	//
	// with spaces where the bars are on the rows above.
	frame := r.Cols
	sep := 0
	if r.Col+r.Cols < s.Grid.Cols {
		sep = 1
		r.Cols--
	}

	status := 0
	if r.Row+r.Rows < band.Row+band.Rows || s.Layout.showStatus() {
		status = 1
	}
	if r.Rows-status < 1 {
		status = 0
	}

	if sep == 1 {
		vert := fillChar(o.G.FillChars, "vert", '|')
		for row := r.Row; row < r.Row+r.Rows-status; row++ {
			s.Grid.Set(row, r.Col+r.Cols, vert, g.VertSplit)
		}
	}

	c := &winCtx{
		f: f, s: s, w: w, g: g, fld: f.folds(w),
		text:        Region{Row: r.Row, Col: r.Col, Rows: r.Rows - status, Cols: r.Cols},
		tabstop:     max(1, o.B.TabStop),
		wrap:        w.Opt.Wrap,
		lineBreak:   w.Opt.LineBreak,
		breakIndent: w.Opt.BreakIndent,
		breakAt:     f.breakAt(),
		showBreak:   f.ShowBreak,
		leftCol:     w.View.LeftCol,
		cursorLine:  w.Opt.CursorLine && current,
		current:     current,
	}
	if w.Opt.Number || w.Opt.RelNumber {
		c.numWidth = numberWidth(w)
		// The number column never takes the whole window: a window with no
		// cells left for text is one every cursor calculation divides by
		// zero in.
		if c.numWidth > c.text.Cols-1 {
			c.numWidth = c.text.Cols - 1
		}
		if c.numWidth < 0 {
			c.numWidth = 0
		}
		c.text.Col += c.numWidth
		c.text.Cols -= c.numWidth
	}
	c.drawText()

	if status == 1 {
		f.drawStatus(s, g, w, current, r.Row+r.Rows-1, r.Col, r.Cols, frame)
	}
}

// numberWidth is the width of the 'number' column.
//
// Vim's number_width(): the digits in the last line number, but never fewer
// than 'numberwidth' minus one, plus one for the space after it. A 20-line
// buffer at the default 'numberwidth' of 4 gives " 1 ", and a 200-line one
// still gives "194 "; the column only grows at a thousand lines.
func numberWidth(w *window.Window) int {
	n := 1
	last := 0
	if w.Buf != nil {
		last = w.Buf.LineCount()
	}
	for last >= 10 {
		last /= 10
		n++
	}
	nuw := w.Opt.NumberWidth
	if nuw < 1 {
		nuw = 4
	}
	if n < nuw-1 {
		n = nuw - 1
	}
	return n + 1
}

// bufOf returns a window's buffer, never nil, so the furniture can ask it for
// a line count without a check at every call.
func bufOf(w *window.Window) *text.Buffer {
	if w == nil || w.Buf == nil {
		return text.New()
	}
	return w.Buf
}
