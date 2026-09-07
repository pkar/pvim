package screen

// CursorShape is where the caret is drawn, which vim ties to the mode.
type CursorShape uint8

// The cursor shapes. Hollow is the unfocused window; vim's GUI blink is not
// here and is not coming.
const (
	CursorBlock CursorShape = iota
	CursorBar
	CursorUnderline
	CursorHollow
)

// Region is a rectangle of cells: where one part of the screen lives. A region
// with Rows == 0 is one that is not on screen at all, which is what a tabline
// with a single tab open is.
type Region struct {
	Row  int
	Col  int
	Rows int
	Cols int
}

// Empty reports whether the region has no cells in it.
func (r Region) Empty() bool { return r.Rows <= 0 || r.Cols <= 0 }

// Layout is the option state that decides how many rows each part of the screen
// gets. It holds no content and does no drawing: it turns six numbers into four
// rectangles, and that is the whole of its job.
//
// The three-valued options are vim's own, spelled as vim spells them, because
// the vimrc sets them by those names and a translation layer between `set
// laststatus=2` and a bool here would only be somewhere for a bug to live.
type Layout struct {
	Rows int
	Cols int

	// ShowTabline is 'showtabline': 0 never, 1 when more than one tab is open,
	// 2 always. The tabline is text here rather than an AppKit control because
	// the vimrc says guioptions-=e.
	ShowTabline int

	// Tabs is how many tab pages are open, which is what ShowTabline == 1 asks
	// about. Zero is treated as one.
	Tabs int

	// LastStatus is 'laststatus': 0 never, 1 when more than one window is open,
	// 2 always.
	LastStatus int

	// Windows is how many windows the current tab page holds, which is what
	// LastStatus == 1 asks about. Zero is treated as one.
	Windows int

	// CmdHeight is 'cmdheight'. Vim's minimum is 1 and so is this one; the
	// vimrc sets it to 1 to be rid of the press-enter prompt.
	CmdHeight int
}

// DefaultLayout returns the layout vim --clean starts with at the given size:
// one tab, one window, showtabline=1, laststatus=1, cmdheight=1. Checked
// against vim 9.2.0321, where those defaults on a 24-row screen give a text
// area of 23 rows and the command line on the last one.
func DefaultLayout(rows, cols int) Layout {
	return Layout{
		Rows:        rows,
		Cols:        cols,
		ShowTabline: 1,
		Tabs:        1,
		LastStatus:  1,
		Windows:     1,
		CmdHeight:   1,
	}
}

// Regions is where each part of the screen ended up, top to bottom.
type Regions struct {
	Tabline Region
	Text    Region
	Status  Region
	Cmdline Region
}

// Regions divides the screen into its four horizontal bands.
//
// Vim's arithmetic, verified against winheight() in vim 9.2.0321, is plainly
// text = lines - tabline - status - cmdheight, and that is the whole rule when
// the screen is big enough. When it is not, this shrinks in a fixed order:
// cmdheight down to 1 first, then the tabline goes, then the status line, and
// the text area is what is left. That order is a choice and not vim's, because
// vim answers a screen that small by refusing to resize at all and this editor
// has to draw something; it is exercised by a test and nothing else depends on
// it.
func (l Layout) Regions() Regions {
	cmd := l.CmdHeight
	if cmd < 1 {
		cmd = 1
	}
	tab := 0
	if l.showTabline() {
		tab = 1
	}
	status := 0
	if l.showStatus() {
		status = 1
	}

	// Give the text area at least one row by taking rows back in order.
	for tab+status+cmd+1 > l.Rows && cmd > 1 {
		cmd--
	}
	if tab+status+cmd+1 > l.Rows {
		tab = 0
	}
	if tab+status+cmd+1 > l.Rows {
		status = 0
	}

	text := l.Rows - tab - status - cmd
	if text < 0 {
		text = 0
	}

	row := 0
	var rs Regions
	if tab > 0 {
		rs.Tabline = Region{Row: row, Rows: tab, Cols: l.Cols}
		row += tab
	}
	rs.Text = Region{Row: row, Rows: text, Cols: l.Cols}
	row += text
	if status > 0 {
		rs.Status = Region{Row: row, Rows: status, Cols: l.Cols}
		row += status
	}
	rs.Cmdline = Region{Row: row, Rows: l.Rows - row, Cols: l.Cols}
	if rs.Cmdline.Rows < 0 {
		rs.Cmdline.Rows = 0
	}
	return rs
}

// showTabline applies 'showtabline' to the open tab count.
func (l Layout) showTabline() bool {
	switch {
	case l.ShowTabline >= 2:
		return true
	case l.ShowTabline <= 0:
		return false
	default:
		return l.Tabs > 1
	}
}

// showStatus applies 'laststatus' to the open window count.
func (l Layout) showStatus() bool {
	switch {
	case l.LastStatus >= 2:
		return true
	case l.LastStatus <= 0:
		return false
	default:
		return l.Windows > 1
	}
}

// Screen is one frame: everything a frontend needs to paint and nothing more.
//
// The editor goroutine owns it and hands it over; a frontend reads it and never
// writes to it. Every window, the tabline, the statusline, the command line and
// any popup menu have already been composited into Grid by the time a frontend
// sees one, so a frontend has no idea splits exist. Layout is carried along so
// that a frontend which wants to treat the command line specially, as the
// terminal one does for the cursor, can find it without guessing.
type Screen struct {
	Grid   Grid
	Layout Layout

	// HL is the highlight table every Cell's HL indexes. It is never nil on a
	// screen from NewScreen, and Look copes if somebody built one by hand.
	HL *Table

	// CursorRow and CursorCol are 0-based cell coordinates in Grid.
	CursorRow int
	CursorCol int

	CursorShape CursorShape
}

// NewScreen returns a cleared screen of the given size with vim's default
// layout and a highlight table holding only Normal.
func NewScreen(rows, cols int) *Screen {
	s := &Screen{HL: NewTable(), Layout: DefaultLayout(rows, cols)}
	s.Grid.Resize(rows, cols)
	return s
}

// Resize changes the screen size, clears the grid and recomputes the layout.
func (s *Screen) Resize(rows, cols int) {
	s.Layout.Rows, s.Layout.Cols = rows, cols
	s.Grid.Resize(rows, cols)
}

// Regions is the current division of the screen, shorthand for
// s.Layout.Regions().
func (s *Screen) Regions() Regions { return s.Layout.Regions() }

// Look returns the resolved highlight for id. A frontend must never index the
// table directly: a colourscheme reload can shrink it under a frame already in
// flight.
func (s *Screen) Look(id HLID) Highlight { return s.HL.Look(id) }
