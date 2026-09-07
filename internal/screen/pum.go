package screen

// The completion popup menu.
//
// Every number here is vim 9.2.0321's, taken off screen dumps at 30 columns
// and 16 rows with a buffer of one-word lines and CTRL-N typed at the end:
//
// - six items, cursor on line 8: the menu occupies rows 8 to 13, 0-based,
// directly under the cursor line, and is 15 cells wide even though the
// widest item is 9. Fifteen is 'pumwidth'.
// - twelve items, cursor on line 14 of a 16-row screen: the menu goes above
// the cursor line instead, rows 1 to 12, because there is more room there.
// - with 'completeopt' holding noselect nothing is drawn in PmenuSel, and
// with it removed the first row is.

// PumDefHeight is vim's PUM_DEF_HEIGHT: the height the placement rule
// pretends the menu has when deciding whether to put it above or below the
// cursor. It is not a cap on the menu itself.
const PumDefHeight = 10

// PumWidth is 'pumwidth': the narrowest the menu is ever drawn. Vim's default
// is 15 and nothing in this editor's config changes it.
const PumWidth = 15

// PumItem is one completion.
type PumItem struct {
	// Word is what would be inserted.
	Word string
	// Kind is the one-letter type vim shows in a column of its own: "v" for a
	// variable, "f" for a function. Empty for the buffer-word completion the
	// editor has.
	Kind string
	// Menu is the extra text after the kind: gopls puts a package path here.
	Menu string
}

// Pum is the completion menu as the renderer needs it.
type Pum struct {
	// Items are the completions, in the order CTRL-N walks them.
	Items []PumItem

	// Selected is the index of the highlighted item, or -1 for none, which is
	// what 'completeopt' noselect leaves it at and what the vimrc asks for.
	Selected int

	// Col is the display column, inside the current window's text area, of
	// the start of the text being completed. Vim moves the cursor there
	// before drawing so the menu lines up with the word and not with the
	// caret.
	Col int

	// Scroll is the index of the first item shown, which is nonzero only when
	// there are more items than rows.
	Scroll int
}

// drawPum paints the completion menu over whatever is already on the grid.
func (f *Frame) drawPum(s *Screen, g Groups) {
	p := f.Pum
	if p == nil || len(p.Items) == 0 {
		return
	}
	tab := f.Tabs.Current()
	if tab == nil || tab.Cur == nil {
		return
	}
	w := tab.Cur

	// The rows the menu may use: the current window's text area, ending where
	// the command line starts.
	above := w.Rect.Row
	_, cmd := s.bands()
	below := cmd.Row
	anchor := s.CursorRow

	size := len(p.Items)
	height := size
	if height > PumDefHeight {
		height = PumDefHeight
	}
	if ph := f.opts().G.PumHeight; ph > 0 && height > ph {
		height = ph
	}

	var row int
	if anchor+2 >= below-height && anchor-above > (below-above)/2 {
		// Above the cursor line.
		if anchor-above < size {
			row, height = above, anchor-above
		} else {
			row, height = anchor-size, size
		}
		if ph := f.opts().G.PumHeight; ph > 0 && height > ph {
			row += height - ph
			height = ph
		}
	} else {
		row = anchor + 1
		if below-row < size {
			height = below - row
		} else {
			height = size
		}
		if ph := f.opts().G.PumHeight; ph > 0 && height > ph {
			height = ph
		}
	}
	if height <= 0 {
		return
	}

	maxWidth, kindWidth, extraWidth := 0, 0, 0
	for _, it := range p.Items {
		if n := LineWidth([]byte(it.Word), 8); n > maxWidth {
			maxWidth = n
		}
		if it.Kind != "" {
			if n := LineWidth([]byte(it.Kind), 8) + 1; n > kindWidth {
				kindWidth = n
			}
		}
		if it.Menu != "" {
			if n := LineWidth([]byte(it.Menu), 8) + 1; n > extraWidth {
				extraWidth = n
			}
		}
	}
	baseWidth := maxWidth

	scrollbar := 0
	if height < size {
		scrollbar = 1
		maxWidth++
	}
	col := w.Rect.Col + p.Col
	width := s.Grid.Cols - col - scrollbar
	if width > maxWidth+kindWidth+extraWidth+1 && width > PumWidth {
		width = maxWidth + kindWidth + extraWidth + 1
		if width < PumWidth {
			width = PumWidth
		}
	}
	if width > s.Grid.Cols-col-scrollbar {
		width = s.Grid.Cols - col - scrollbar
	}
	if width <= 0 {
		return
	}

	first := p.Scroll
	if first > size-height {
		first = size - height
	}
	if first < 0 {
		first = 0
	}

	for i := 0; i < height; i++ {
		it := p.Items[first+i]
		hl := g.Pmenu
		if first+i == p.Selected {
			hl = g.PmenuSel
		}
		r := row + i
		s.Grid.Fill(r, col, width, ' ', hl)
		s.Grid.SetString(r, col, it.Word, hl)
		if it.Kind != "" {
			s.Grid.SetString(r, col+baseWidth+1, it.Kind, hl)
		}
		if it.Menu != "" {
			s.Grid.SetString(r, col+baseWidth+kindWidth+1, it.Menu, hl)
		}
	}

	if scrollbar == 1 {
		f.drawPumScrollbar(s, g, row, col+width, height, size, first)
	}
}

// drawPumScrollbar paints the one-column bar down the right-hand side of a
// menu with more items than rows: PmenuSbar for the trough and PmenuThumb for
// the part that stands for what is showing.
func (f *Frame) drawPumScrollbar(s *Screen, g Groups, row, col, height, size, first int) {
	thumb := height * height / size
	if thumb < 1 {
		thumb = 1
	}
	top := 0
	if size > height {
		top = first * (height - thumb) / (size - height)
	}
	for i := 0; i < height; i++ {
		hl := g.PmenuSbar
		if i >= top && i < top+thumb {
			hl = g.PmenuThumb
		}
		s.Grid.Set(row+i, col, ' ', hl)
	}
}
